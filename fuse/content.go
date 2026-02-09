package fuse

import (
	"context"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"shelley-fuse/fuse/diag"
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

// --- Content query types ---

type queryKind int

const (
	queryAll   queryKind = iota
	queryBySeq           // {N}.json
	queryLast            // last/{N}
	querySince           // since/{person}/{N}

)

type contentFormat int

const (
	formatJSON contentFormat = iota
	formatMD
)

type contentQuery struct {
	kind   queryKind
	seqNum int
	n      int
	person string
	format contentFormat
}

// --- ConvContentNode: read-only conversation content file ---

type ConvContentNode struct {
	fs.Inode
	localID     string
	client      shelley.ShelleyClient
	state       *state.Store
	query       contentQuery
	startTime   time.Time // fallback if conversation has no CreatedAt
	messageTime time.Time // timestamp of specific message (for queryBySeq)
	parsedCache *ParsedMessageCache
	diag        *diag.Tracker
}

var _ = (fs.NodeOpener)((*ConvContentNode)(nil))
var _ = (fs.NodeGetattrer)((*ConvContentNode)(nil))

func (c *ConvContentNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	defer diag.Track(c.diag, "ConvContentNode", "Open", c.localID)()
	// Fetch and cache content at open time to ensure consistent reads.
	// Without caching, multiple read() calls would regenerate data each time,
	// and if the conversation changed between reads, the result would be corrupted.
	cs := c.state.Get(c.localID)
	if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
		// Return handle that will report ENOENT on read (preserves original behavior)
		return &ConvContentFileHandle{errno: syscall.ENOENT}, fuse.FOPEN_DIRECT_IO, 0
	}

	convData, err := c.client.GetConversation(cs.ShelleyConversationID)
	if err != nil {
		return &ConvContentFileHandle{errno: syscall.EIO}, fuse.FOPEN_DIRECT_IO, 0
	}
	msgs, toolMap, err := c.parsedCache.GetOrParse(cs.ShelleyConversationID, convData)
	if err != nil {
		return &ConvContentFileHandle{errno: syscall.EIO}, fuse.FOPEN_DIRECT_IO, 0
	}

	data, errno := c.formatResult(msgs, toolMap)
	if errno != 0 {
		// Return handle that will report the error on read (preserves original behavior)
		return &ConvContentFileHandle{errno: errno}, fuse.FOPEN_DIRECT_IO, 0
	}

	// Individual message content is immutable — use FOPEN_KEEP_CACHE so the
	// kernel can serve repeated reads from its page cache. FileGetattrer on
	// the handle reports the real size (Getattr on the node returns 0 since
	// content isn't fetched until Open).
	if c.query.kind == queryBySeq {
		return &ConvContentFileHandle{content: data, messageTime: c.messageTime, startTime: c.startTime, localID: c.localID, state: c.state}, fuse.FOPEN_KEEP_CACHE, 0
	}
	return &ConvContentFileHandle{content: data}, fuse.FOPEN_DIRECT_IO, 0
}

// ConvContentFileHandle caches content for consistent reads across multiple read() calls
type ConvContentFileHandle struct {
	content     []byte
	errno       syscall.Errno
	messageTime time.Time    // for Getattr timestamp (queryBySeq only)
	startTime   time.Time    // fallback timestamp
	localID     string       // for looking up conversation creation time
	state       *state.Store // for looking up conversation creation time
}

var _ = (fs.FileReader)((*ConvContentFileHandle)(nil))
var _ = (fs.FileGetattrer)((*ConvContentFileHandle)(nil))

func (h *ConvContentFileHandle) Getattr(ctx context.Context, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = uint64(len(h.content))
	if !h.messageTime.IsZero() {
		setTimestamps(&out.Attr, h.messageTime)
	} else if h.state != nil {
		if cs := h.state.Get(h.localID); cs != nil && !cs.CreatedAt.IsZero() {
			setTimestamps(&out.Attr, cs.CreatedAt)
		} else {
			setTimestamps(&out.Attr, h.startTime)
		}
	}
	return 0
}

func (h *ConvContentFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if h.errno != 0 {
		return nil, h.errno
	}
	return fuse.ReadResultData(readAt(h.content, dest, off)), 0
}

func (c *ConvContentNode) formatResult(msgs []shelley.Message, toolMap map[string]string) ([]byte, syscall.Errno) {
	var filtered []shelley.Message

	switch c.query.kind {
	case queryAll:
		filtered = msgs
	case queryBySeq:
		m := shelley.GetMessage(msgs, c.query.seqNum)
		if m == nil {
			return nil, syscall.ENOENT
		}
		filtered = []shelley.Message{*m}
	case queryLast:
		filtered = shelley.FilterLast(msgs, c.query.n)
	case querySince:
		filtered = shelley.FilterSinceWithToolMap(msgs, c.query.person, c.query.n, toolMap)
		if filtered == nil {
			return nil, syscall.ENOENT
		}

	}

	switch c.query.format {
	case formatMD:
		return shelley.FormatMarkdown(filtered), 0
	default:
		data, err := shelley.FormatJSON(filtered)
		if err != nil {
			return nil, syscall.EIO
		}
		data = append(data, '\n')
		return data, 0
	}
}

func (c *ConvContentNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	// For individual message files, use the message's timestamp
	if !c.messageTime.IsZero() {
		setTimestamps(&out.Attr, c.messageTime)
		return 0
	}
	// Use conversation creation time if available, otherwise fall back to FS start time
	cs := c.state.Get(c.localID)
	if cs != nil && !cs.CreatedAt.IsZero() {
		setTimestamps(&out.Attr, cs.CreatedAt)
	} else {
		setTimestamps(&out.Attr, c.startTime)
	}
	return 0
}

// --- QueryDirNode: handles last/, since/ and since/{person}/ ---

type QueryDirNode struct {
	fs.Inode
	localID     string
	client      shelley.ShelleyClient
	state       *state.Store
	kind        queryKind // queryLast or querySince
	person      string    // set for since/{person}/
	startTime   time.Time // fallback if conversation has no CreatedAt
	parsedCache *ParsedMessageCache
	diag        *diag.Tracker
}

var _ = (fs.NodeLookuper)((*QueryDirNode)(nil))
var _ = (fs.NodeReaddirer)((*QueryDirNode)(nil))
var _ = (fs.NodeGetattrer)((*QueryDirNode)(nil))

func (q *QueryDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(q.diag, "QueryDirNode", "Lookup", q.localID+"/"+name)()
	// If this is since/ (no person set), the child is a person directory
	if q.kind == querySince && q.person == "" {
		// Use a stable inode number so go-fuse reuses the existing node
		// across repeated path traversals (e.g., during ls -l).
		ino := stableIno("query-person", q.localID, name)
		return q.NewInode(ctx, &QueryDirNode{
			localID: q.localID, client: q.client, state: q.state,
			kind: q.kind, person: name, startTime: q.startTime, parsedCache: q.parsedCache, diag: q.diag,
		}, fs.StableAttr{Mode: fuse.S_IFDIR, Ino: ino}), 0
	}

	// The child is {N} - return a QueryResultDirNode.
	// Use a stable inode number so go-fuse reuses the existing node across
	// repeated path traversals. This is critical for performance: the node
	// caches filtered message results and name indices that are expensive to
	// recompute (FilterSinceWithToolMap is O(N) with JSON parsing per message).
	// Without stable inodes, ls -l on since/{person}/{N}/ would create a fresh
	// node for every Lstat call, causing O(N²) FilterSince computations.
	n, err := strconv.Atoi(name)
	if err != nil || n <= 0 {
		return nil, syscall.ENOENT
	}

	ino := stableIno("query-result", q.localID, q.person, name)
	return q.NewInode(ctx, &QueryResultDirNode{
		localID:     q.localID,
		client:      q.client,
		state:       q.state,
		kind:        q.kind,
		n:           n,
		person:      q.person,
		startTime:   q.startTime,
		parsedCache: q.parsedCache,
		diag:        q.diag,
	}, fs.StableAttr{Mode: fuse.S_IFDIR, Ino: ino}), 0
}

// getSymlinkTarget returns the symlink target for last/{N} or since/{person}/{N}.
// For last/{N}: returns "../../{NNN-{slug}}" pointing to the Nth-to-last message
// For since/{person}/{N}: returns "../../../{NNN-{slug}}" pointing to the Nth message after the last message from person
func (q *QueryDirNode) getSymlinkTarget(n int) (string, syscall.Errno) {
	cs := q.state.Get(q.localID)
	if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
		return "", syscall.ENOENT
	}

	convData, err := q.client.GetConversation(cs.ShelleyConversationID)
	if err != nil {
		return "", syscall.EIO
	}

	result, err := q.parsedCache.GetOrParseResult(cs.ShelleyConversationID, convData)
	if err != nil {
		return "", syscall.EIO
	}

	var targetMsg *shelley.Message
	var prefix string

	switch q.kind {
	case queryLast:
		// last/{N} -> Nth-to-last message
		targetMsg = shelley.GetNthLast(result.Messages, n)
		prefix = "../../" // up from last/, up from messages/
	case querySince:
		// since/{person}/{N} -> Nth message after the last message from person
		targetMsg = shelley.GetNthSinceWithToolMap(result.Messages, q.person, n, result.ToolMap)
		prefix = "../../../" // up from {N}/, up from {person}/, up from since/, into messages/
	}

	if targetMsg == nil {
		return "", syscall.ENOENT
	}

	slug := shelley.MessageSlug(targetMsg, result.ToolMap)
	base := messageFileBase(targetMsg.SequenceID, slug, result.MaxSeqID)
	return prefix + base, 0
}

func (q *QueryDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// Dynamic directories — contents discovered via Lookup
	return fs.NewListDirStream([]fuse.DirEntry{}), 0
}

func (q *QueryDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	// Use conversation creation time if available, otherwise fall back to FS start time
	cs := q.state.Get(q.localID)
	if cs != nil && !cs.CreatedAt.IsZero() {
		setTimestamps(&out.Attr, cs.CreatedAt)
	} else {
		setTimestamps(&out.Attr, q.startTime)
	}
	return 0
}

// --- QueryResultDirNode: represents last/{N}/ or since/{person}/{N}/ ---
// Contains symlinks to the actual message directories.

type QueryResultDirNode struct {
	fs.Inode
	localID     string
	client      shelley.ShelleyClient
	state       *state.Store
	kind        queryKind // queryLast or querySince
	n           int       // the N in last/{N} or since/{person}/{N}
	person      string    // set for since/{person}/{N}
	startTime   time.Time
	parsedCache *ParsedMessageCache
	diag        *diag.Tracker

	// Cached filtered results to avoid redundant filtering during ls -l.
	// When Readdir and subsequent Lookups happen on the same node within
	// a single ls -l, the cached result is reused if the underlying
	// conversation data hasn't changed (checked via parsedCache identity).
	cacheMu       sync.Mutex
	cachedMsgs    []shelley.Message    // the full parsed messages (from parsedCache)
	cachedToolMap map[string]string    // tool map from parsedCache
	cachedResult  *queryResultSnapshot // filtered result + name index
}

// queryResultSnapshot holds pre-computed filtered messages and a name→index
// map for O(1) Lookup by entry name.
type queryResultSnapshot struct {
	filtered []shelley.Message
	maxSeqID int
	nameIdx  map[string]int // entry name → index in filtered (for since/ queries)
}

var _ = (fs.NodeLookuper)((*QueryResultDirNode)(nil))
var _ = (fs.NodeReaddirer)((*QueryResultDirNode)(nil))
var _ = (fs.NodeGetattrer)((*QueryResultDirNode)(nil))

// getFilteredMessages returns the messages that match the query, along with the max
// sequence ID in the conversation (needed for consistent zero-padding of directory names).
// Results are cached on the node and reused when the underlying conversation data
// hasn't changed, avoiding redundant API calls and filtering during ls -l operations.
func (q *QueryResultDirNode) getFilteredMessages() (snap *queryResultSnapshot, toolMap map[string]string, err error) {
	cs := q.state.Get(q.localID)
	if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
		return nil, nil, nil
	}

	convData, err := q.client.GetConversation(cs.ShelleyConversationID)
	if err != nil {
		return nil, nil, err
	}

	result, err := q.parsedCache.GetOrParseResult(cs.ShelleyConversationID, convData)
	if err != nil {
		return nil, nil, err
	}

	// Fast path: if the parsedCache returned the same slice (same conversation
	// data), reuse the cached filtered result. This avoids redundant FilterSince
	// calls during ls -l where Readdir + N Lookups all hit the same data.
	q.cacheMu.Lock()
	defer q.cacheMu.Unlock()

	if q.cachedResult != nil && q.cachedMsgs != nil &&
		len(q.cachedMsgs) == len(result.Messages) &&
		len(result.Messages) > 0 && &q.cachedMsgs[0] == &result.Messages[0] {
		return q.cachedResult, q.cachedToolMap, nil
	}

	// Slow path: filter and build the snapshot
	var filtered []shelley.Message
	switch q.kind {
	case queryLast:
		filtered = shelley.FilterLast(result.Messages, q.n)
	case querySince:
		filtered = shelley.FilterSinceWithToolMap(result.Messages, q.person, q.n, result.ToolMap)
	}

	snap = &queryResultSnapshot{
		filtered: filtered,
		maxSeqID: result.MaxSeqID,
	}

	// Build name index for since/ queries to enable O(1) lookup by name
	if q.kind == querySince && filtered != nil {
		snap.nameIdx = make(map[string]int, len(filtered))
		for i := range filtered {
			slug := shelley.MessageSlug(&filtered[i], result.ToolMap)
			base := messageFileBase(filtered[i].SequenceID, slug, snap.maxSeqID)
			snap.nameIdx[base] = i
		}
	}

	q.cachedMsgs = result.Messages
	q.cachedToolMap = result.ToolMap
	q.cachedResult = snap
	return snap, result.ToolMap, nil
}

// symlinkPrefix returns the relative path prefix for symlinks.
// For last/{N}/, this is "../../" (up to last/, up to messages/)
// For since/{person}/{N}/, this is "../../../" (up to {N}/, up to {person}/, up to since/, up to messages/)
func (q *QueryResultDirNode) symlinkPrefix() string {
	if q.kind == queryLast {
		return "../../"
	}
	return "../../../"
}

func (q *QueryResultDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(q.diag, "QueryResultDirNode", "Lookup", q.localID+"/"+name)()
	snap, toolMap, err := q.getFilteredMessages()
	if err != nil {
		return nil, syscall.EIO
	}
	if snap == nil || snap.filtered == nil {
		return nil, syscall.ENOENT
	}

	// For last/{N}, entries are ordinal (0, 1, 2, ...)
	// For since/{person}/{N}, entries are message base names
	if q.kind == queryLast {
		// Parse the ordinal index
		idx, err := strconv.Atoi(name)
		if err != nil || idx < 0 || idx >= len(snap.filtered) {
			return nil, syscall.ENOENT
		}
		// idx 0 is oldest, idx len-1 is newest (msgs are already in oldest-first order from FilterLast)
		slug := shelley.MessageSlug(&snap.filtered[idx], toolMap)
		base := messageFileBase(snap.filtered[idx].SequenceID, slug, snap.maxSeqID)
		target := q.symlinkPrefix() + base
		return q.NewInode(ctx, &SymlinkNode{target: target, startTime: q.startTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	}

	// For since/{person}/{N}, use the pre-built name index for O(1) lookup
	if snap.nameIdx != nil {
		if _, ok := snap.nameIdx[name]; ok {
			target := q.symlinkPrefix() + name
			return q.NewInode(ctx, &SymlinkNode{target: target, startTime: q.startTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
		}
	}

	return nil, syscall.ENOENT
}

func (q *QueryResultDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	defer diag.Track(q.diag, "QueryResultDirNode", "Readdir", q.localID)()
	snap, toolMap, err := q.getFilteredMessages()
	if err != nil {
		return nil, syscall.EIO
	}
	if snap == nil {
		return fs.NewListDirStream(nil), 0
	}

	entries := make([]fuse.DirEntry, 0, len(snap.filtered))
	// For last/{N}, entries are ordinal (0, 1, 2, ...)
	// For since/{person}/{N}, entries are message base names
	if q.kind == queryLast {
		for i := range snap.filtered {
			entries = append(entries, fuse.DirEntry{Name: strconv.Itoa(i), Mode: syscall.S_IFLNK})
		}
	} else {
		for i := range snap.filtered {
			slug := shelley.MessageSlug(&snap.filtered[i], toolMap)
			base := messageFileBase(snap.filtered[i].SequenceID, slug, snap.maxSeqID)
			entries = append(entries, fuse.DirEntry{Name: base, Mode: syscall.S_IFLNK})
		}
	}

	return fs.NewListDirStream(entries), 0
}

func (q *QueryResultDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	cs := q.state.Get(q.localID)
	if cs != nil && !cs.CreatedAt.IsZero() {
		setTimestamps(&out.Attr, cs.CreatedAt)
	} else {
		setTimestamps(&out.Attr, q.startTime)
	}
	return 0
}

