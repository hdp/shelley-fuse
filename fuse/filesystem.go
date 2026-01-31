package fuse

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

// FS is the root inode of the Shelley FUSE filesystem.
type FS struct {
	fs.Inode
	client *shelley.Client
	state  *state.Store
}

// NewFS creates a new Shelley FUSE filesystem.
func NewFS(client *shelley.Client, store *state.Store) *FS {
	return &FS{client: client, state: store}
}

var _ = (fs.NodeLookuper)((*FS)(nil))
var _ = (fs.NodeReaddirer)((*FS)(nil))

func (f *FS) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	switch name {
	case "models":
		return f.NewInode(ctx, &ModelsNode{client: f.client}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "new":
		return f.NewInode(ctx, &NewDirNode{state: f.state}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "conversation":
		return f.NewInode(ctx, &ConversationListNode{client: f.client, state: f.state}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}
	return nil, syscall.ENOENT
}

func (f *FS) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "models", Mode: fuse.S_IFREG},
		{Name: "new", Mode: fuse.S_IFDIR},
		{Name: "conversation", Mode: fuse.S_IFDIR},
	}), 0
}

// --- ModelsNode: read-only file listing available models ---

type ModelsNode struct {
	fs.Inode
	client *shelley.Client
}

var _ = (fs.NodeOpener)((*ModelsNode)(nil))
var _ = (fs.NodeReader)((*ModelsNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelsNode)(nil))

func (m *ModelsNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (m *ModelsNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data, err := m.client.ListModels()
	if err != nil {
		return nil, syscall.EIO
	}
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (m *ModelsNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	return 0
}

// --- NewDirNode: /new/ directory containing clone ---

type NewDirNode struct {
	fs.Inode
	state *state.Store
}

var _ = (fs.NodeLookuper)((*NewDirNode)(nil))
var _ = (fs.NodeReaddirer)((*NewDirNode)(nil))

func (n *NewDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if name == "clone" {
		return n.NewInode(ctx, &CloneNode{state: n.state}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}
	return nil, syscall.ENOENT
}

func (n *NewDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "clone", Mode: fuse.S_IFREG},
	}), 0
}

// --- CloneNode: each Open generates a new conversation ID ---

type CloneNode struct {
	fs.Inode
	state *state.Store
}

var _ = (fs.NodeOpener)((*CloneNode)(nil))

func (c *CloneNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	id, err := c.state.Clone()
	if err != nil {
		return nil, 0, syscall.EIO
	}
	return &CloneFileHandle{id: id}, fuse.FOPEN_DIRECT_IO, 0
}

type CloneFileHandle struct {
	id string
}

var _ = (fs.FileReader)((*CloneFileHandle)(nil))

func (h *CloneFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data := []byte(h.id + "\n")
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

// --- ConversationListNode: /conversation/ directory ---

type ConversationListNode struct {
	fs.Inode
	client *shelley.Client
	state  *state.Store
}

var _ = (fs.NodeLookuper)((*ConversationListNode)(nil))
var _ = (fs.NodeReaddirer)((*ConversationListNode)(nil))

func (c *ConversationListNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	cs := c.state.Get(name)
	if cs == nil {
		return nil, syscall.ENOENT
	}
	return c.NewInode(ctx, &ConversationNode{
		localID: name,
		client:  c.client,
		state:   c.state,
	}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
}

func (c *ConversationListNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	ids := c.state.List()
	entries := make([]fuse.DirEntry, len(ids))
	for i, id := range ids {
		entries[i] = fuse.DirEntry{Name: id, Mode: fuse.S_IFDIR}
	}
	return fs.NewListDirStream(entries), 0
}

// --- ConversationNode: /conversation/{id}/ directory ---

type ConversationNode struct {
	fs.Inode
	localID string
	client  *shelley.Client
	state   *state.Store
}

var _ = (fs.NodeLookuper)((*ConversationNode)(nil))
var _ = (fs.NodeReaddirer)((*ConversationNode)(nil))

func (c *ConversationNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	switch name {
	case "ctl":
		return c.NewInode(ctx, &CtlNode{localID: c.localID, state: c.state}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "new":
		return c.NewInode(ctx, &ConvNewNode{localID: c.localID, client: c.client, state: c.state}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "status.json":
		return c.NewInode(ctx, &StatusNode{localID: c.localID, client: c.client, state: c.state}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "last":
		return c.NewInode(ctx, &QueryDirNode{localID: c.localID, client: c.client, state: c.state, kind: queryLast}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "since":
		return c.NewInode(ctx, &QueryDirNode{localID: c.localID, client: c.client, state: c.state, kind: querySince}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "from":
		return c.NewInode(ctx, &QueryDirNode{localID: c.localID, client: c.client, state: c.state, kind: queryFrom}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}

	// all.json, all.md, {N}.json, {N}.md
	format, ok := parseFormat(name)
	if !ok {
		return nil, syscall.ENOENT
	}

	base := strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".md")
	if base == "all" {
		return c.NewInode(ctx, &ConvContentNode{
			localID: c.localID, client: c.client, state: c.state,
			query: contentQuery{kind: queryAll, format: format},
		}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}

	n, err := strconv.Atoi(base)
	if err == nil && n > 0 {
		return c.NewInode(ctx, &ConvContentNode{
			localID: c.localID, client: c.client, state: c.state,
			query: contentQuery{kind: queryBySeq, seqNum: n, format: format},
		}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}

	return nil, syscall.ENOENT
}

func (c *ConversationNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "ctl", Mode: fuse.S_IFREG},
		{Name: "new", Mode: fuse.S_IFREG},
		{Name: "status.json", Mode: fuse.S_IFREG},
		{Name: "all.json", Mode: fuse.S_IFREG},
		{Name: "all.md", Mode: fuse.S_IFREG},
		{Name: "last", Mode: fuse.S_IFDIR},
		{Name: "since", Mode: fuse.S_IFDIR},
		{Name: "from", Mode: fuse.S_IFDIR},
	}), 0
}

// --- CtlNode: write key=value pairs, read-only after conversation created ---

type CtlNode struct {
	fs.Inode
	localID string
	state   *state.Store
}

var _ = (fs.NodeOpener)((*CtlNode)(nil))
var _ = (fs.NodeReader)((*CtlNode)(nil))
var _ = (fs.NodeWriter)((*CtlNode)(nil))
var _ = (fs.NodeGetattrer)((*CtlNode)(nil))
var _ = (fs.NodeSetattrer)((*CtlNode)(nil))

func (c *CtlNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (c *CtlNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	cs := c.state.Get(c.localID)
	if cs == nil {
		return nil, syscall.ENOENT
	}
	var parts []string
	if cs.Model != "" {
		parts = append(parts, "model="+cs.Model)
	}
	if cs.Cwd != "" {
		parts = append(parts, "cwd="+cs.Cwd)
	}
	data := []byte(strings.Join(parts, " ") + "\n")
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (c *CtlNode) Write(ctx context.Context, f fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	cs := c.state.Get(c.localID)
	if cs == nil {
		return 0, syscall.ENOENT
	}
	if cs.Created {
		return 0, syscall.EROFS
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return uint32(len(data)), 0
	}

	words := strings.Fields(content)
	for _, word := range words {
		k, v, ok := strings.Cut(word, "=")
		if !ok {
			return 0, syscall.EINVAL
		}
		if err := c.state.SetCtl(c.localID, k, v); err != nil {
			return 0, syscall.EINVAL
		}
	}
	return uint32(len(data)), 0
}

func (c *CtlNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	cs := c.state.Get(c.localID)
	if cs == nil {
		return syscall.ENOENT
	}
	if cs.Created {
		out.Mode = fuse.S_IFREG | 0444
	} else {
		out.Mode = fuse.S_IFREG | 0644
	}
	return 0
}

func (c *CtlNode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	// Accept truncate (from shell > redirect) silently
	return c.Getattr(ctx, f, out)
}

// --- ConvNewNode: write message, creates conversation if needed ---

type ConvNewNode struct {
	fs.Inode
	localID string
	client  *shelley.Client
	state   *state.Store
	mu      sync.Mutex
}

var _ = (fs.NodeOpener)((*ConvNewNode)(nil))
var _ = (fs.NodeWriter)((*ConvNewNode)(nil))
var _ = (fs.NodeGetattrer)((*ConvNewNode)(nil))
var _ = (fs.NodeSetattrer)((*ConvNewNode)(nil))

func (n *ConvNewNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (n *ConvNewNode) Write(ctx context.Context, f fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	n.mu.Lock()
	defer n.mu.Unlock()

	cs := n.state.Get(n.localID)
	if cs == nil {
		return 0, syscall.ENOENT
	}

	message := strings.TrimRight(string(data), "\n")
	if message == "" {
		return uint32(len(data)), 0
	}

	if !cs.Created {
		// First write: create the conversation on the Shelley backend
		shelleyID, err := n.client.StartConversation(message, cs.Model, cs.Cwd)
		if err != nil {
			return 0, syscall.EIO
		}
		if err := n.state.MarkCreated(n.localID, shelleyID); err != nil {
			return 0, syscall.EIO
		}
	} else {
		// Subsequent writes: send message to existing conversation
		if err := n.client.SendMessage(cs.ShelleyConversationID, message, ""); err != nil {
			return 0, syscall.EIO
		}
	}

	return uint32(len(data)), 0
}

func (n *ConvNewNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0222
	return 0
}

func (n *ConvNewNode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	return n.Getattr(ctx, f, out)
}

// --- StatusNode: read-only status.json ---

type StatusNode struct {
	fs.Inode
	localID string
	client  *shelley.Client
	state   *state.Store
}

var _ = (fs.NodeOpener)((*StatusNode)(nil))
var _ = (fs.NodeReader)((*StatusNode)(nil))
var _ = (fs.NodeGetattrer)((*StatusNode)(nil))

func (s *StatusNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (s *StatusNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	cs := s.state.Get(s.localID)
	if cs == nil {
		return nil, syscall.ENOENT
	}

	status := map[string]interface{}{
		"local_id": cs.LocalID,
		"created":  cs.Created,
		"model":    cs.Model,
		"cwd":      cs.Cwd,
	}
	if cs.ShelleyConversationID != "" {
		status["shelley_conversation_id"] = cs.ShelleyConversationID
	}

	if cs.Created && cs.ShelleyConversationID != "" {
		convData, err := s.client.GetConversation(cs.ShelleyConversationID)
		if err == nil {
			msgs, err := shelley.ParseMessages(convData)
			if err == nil {
				status["message_count"] = len(msgs)
			}
		}
	}

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return nil, syscall.EIO
	}
	data = append(data, '\n')
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (s *StatusNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	return 0
}

// --- Content query types ---

type queryKind int

const (
	queryAll   queryKind = iota
	queryBySeq           // {N}.json
	queryLast            // last/{N}
	querySince           // since/{person}/{N}
	queryFrom            // from/{person}/{N}
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
	localID string
	client  *shelley.Client
	state   *state.Store
	query   contentQuery
}

var _ = (fs.NodeOpener)((*ConvContentNode)(nil))
var _ = (fs.NodeGetattrer)((*ConvContentNode)(nil))

func (c *ConvContentNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
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
	msgs, err := shelley.ParseMessages(convData)
	if err != nil {
		return &ConvContentFileHandle{errno: syscall.EIO}, fuse.FOPEN_DIRECT_IO, 0
	}

	data, errno := c.formatResult(msgs)
	if errno != 0 {
		// Return handle that will report the error on read (preserves original behavior)
		return &ConvContentFileHandle{errno: errno}, fuse.FOPEN_DIRECT_IO, 0
	}
	return &ConvContentFileHandle{content: data}, fuse.FOPEN_DIRECT_IO, 0
}

// ConvContentFileHandle caches content for consistent reads across multiple read() calls
type ConvContentFileHandle struct {
	content []byte
	errno   syscall.Errno
}

var _ = (fs.FileReader)((*ConvContentFileHandle)(nil))

func (h *ConvContentFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if h.errno != 0 {
		return nil, h.errno
	}
	return fuse.ReadResultData(readAt(h.content, dest, off)), 0
}

func (c *ConvContentNode) formatResult(msgs []shelley.Message) ([]byte, syscall.Errno) {
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
		filtered = shelley.FilterSince(msgs, c.query.person, c.query.n)
		if filtered == nil {
			return nil, syscall.ENOENT
		}
	case queryFrom:
		m := shelley.FilterFrom(msgs, c.query.person, c.query.n)
		if m == nil {
			return nil, syscall.ENOENT
		}
		filtered = []shelley.Message{*m}
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
	return 0
}

// --- QueryDirNode: handles last/, since/, from/ and since/{person}/, from/{person}/ ---

type QueryDirNode struct {
	fs.Inode
	localID string
	client  *shelley.Client
	state   *state.Store
	kind    queryKind // queryLast, querySince, or queryFrom
	person  string    // set for since/{person}/ and from/{person}/
}

var _ = (fs.NodeLookuper)((*QueryDirNode)(nil))
var _ = (fs.NodeReaddirer)((*QueryDirNode)(nil))

func (q *QueryDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// If this is since/ or from/ (no person set), the child is a person directory
	if (q.kind == querySince || q.kind == queryFrom) && q.person == "" {
		return q.NewInode(ctx, &QueryDirNode{
			localID: q.localID, client: q.client, state: q.state,
			kind: q.kind, person: name,
		}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}

	// Otherwise, the child is {N}.json or {N}.md
	format, ok := parseFormat(name)
	if !ok {
		return nil, syscall.ENOENT
	}
	base := strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".md")
	n, err := strconv.Atoi(base)
	if err != nil || n <= 0 {
		return nil, syscall.ENOENT
	}

	return q.NewInode(ctx, &ConvContentNode{
		localID: q.localID, client: q.client, state: q.state,
		query: contentQuery{kind: q.kind, n: n, person: q.person, format: format},
	}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
}

func (q *QueryDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// Dynamic directories — contents discovered via Lookup
	return fs.NewListDirStream([]fuse.DirEntry{}), 0
}

// --- helpers ---

func parseFormat(name string) (contentFormat, bool) {
	if strings.HasSuffix(name, ".json") {
		return formatJSON, true
	}
	if strings.HasSuffix(name, ".md") {
		return formatMD, true
	}
	return 0, false
}

func readAt(data, dest []byte, off int64) []byte {
	if off >= int64(len(data)) {
		return []byte{}
	}
	end := int64(len(data))
	if int64(len(dest)) < end-off {
		end = off + int64(len(dest))
	}
	return data[off:end]
}

// compile-time interface checks
var _ = (fs.InodeEmbedder)((*FS)(nil))
