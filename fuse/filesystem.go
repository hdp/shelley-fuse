package fuse

import (
	"context"
	_ "embed"
	"fmt"
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"shelley-fuse/fuse/diag"
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

// Kernel cache timeout tiers for entry and attr caching.
// These override the global 0 timeout set in mount options.
// Per-node timeouts set via EntryOut.SetEntryTimeout/SetAttrTimeout (in Lookup)
// and AttrOut.SetTimeout (in Getattr) take precedence over the global defaults.
const (
	// cacheTTLImmutable is for nodes whose content never changes once created:
	// message field nodes, message directories, jsonfs subtrees.
	cacheTTLImmutable = 1 * time.Hour

	// cacheTTLStatic is for nodes that essentially never change:
	// FS root, ReadmeNode, /new symlink.
	cacheTTLStatic = 1 * time.Hour

	// cacheTTLModels is for model-related nodes that change very rarely:
	// ModelsDirNode, ModelNode, ModelFieldNode, ModelReadyNode, ModelNewDirNode, ModelCloneNode.
	cacheTTLModels = 5 * time.Minute

	// cacheTTLConversation is for conversation structural nodes that change
	// infrequently (new messages, archive status changes):
	// ConversationNode, ConversationListNode.
	cacheTTLConversation = 10 * time.Second

	// negTimeout is the negative-entry timeout for dynamic presence files
	// that may appear (e.g., "created" before backend creation, "model" before ctl write).
	// Short enough to notice state changes promptly.
	negTimeout = 1 * time.Second

	// immutableEntryTimeout is the positive-entry timeout for files that,
	// once they exist, never disappear or change identity (e.g., "created",
	// "model" symlink, "cwd" symlink).
	immutableEntryTimeout = 1 * time.Hour

	// volatileEntryTimeout is the positive-entry timeout for files whose
	// presence can toggle (e.g., "archived" can be created/removed).
	volatileEntryTimeout = 1 * time.Second
)

// setEntryTimeout sets the entry (name→inode) cache timeout on an EntryOut (used in Lookup).
// This controls how long the kernel caches that a name exists in a directory.
// Note: we intentionally do NOT set AttrTimeout here because our Lookup methods
// don't populate out.Attr — attribute caching is handled by Getattr via SetTimeout.
func setEntryTimeout(out *fuse.EntryOut, ttl time.Duration) {
	out.SetEntryTimeout(ttl)
}

// ParsedMessageCache caches parsed messages and toolMaps, keyed by conversation ID.
// The cache is content-addressed: it stores a checksum of the raw data and only
// returns the cached result if the raw data hasn't changed. This ensures that
// all nodes see consistent data — when the upstream CachingClient returns the
// same bytes, parsing is skipped; when it returns new bytes, the cache re-parses.

// --- SymlinkNode: a symlink pointing to a target path ---

type SymlinkNode struct {
	fs.Inode
	target    string
	startTime time.Time
}

var _ = (fs.NodeReadlinker)((*SymlinkNode)(nil))
var _ = (fs.NodeGetattrer)((*SymlinkNode)(nil))

func (s *SymlinkNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	return []byte(s.target), 0
}

func (s *SymlinkNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFLNK | 0777
	out.Size = uint64(len(s.target))
	setTimestamps(&out.Attr, s.startTime)
	return 0
}


type FS struct {
	fs.Inode
	client       shelley.ShelleyClient
	state        *state.Store
	cloneTimeout time.Duration
	startTime    time.Time
	parsedCache  *ParsedMessageCache // caches parsed messages and toolMaps
	Diag         *diag.Tracker       // tracks in-flight FUSE I/O operations
}

// NewFS creates a new Shelley FUSE filesystem.
// cloneTimeout specifies how long to wait before cleaning up unconversed clone IDs.
func NewFS(client shelley.ShelleyClient, store *state.Store, cloneTimeout time.Duration) *FS {
	return &FS{
		client:       client,
		state:        store,
		cloneTimeout: cloneTimeout,
		startTime:    time.Now(),
		parsedCache:  NewParsedMessageCache(),
		Diag:         diag.NewTracker(),
	}
}

// NewFSWithCacheTTL creates a new Shelley FUSE filesystem with a custom cache TTL.
func NewFSWithCacheTTL(client shelley.ShelleyClient, store *state.Store, cloneTimeout, cacheTTL time.Duration) *FS {
	return &FS{
		client:       client,
		state:        store,
		cloneTimeout: cloneTimeout,
		startTime:    time.Now(),
		parsedCache:  NewParsedMessageCache(),
		Diag:         diag.NewTracker(),
	}
}

// StartTime returns the time when the FUSE filesystem was created.
// Used by child nodes to set timestamps for static content.
func (f *FS) StartTime() time.Time {
	return f.startTime
}

var _ = (fs.NodeLookuper)((*FS)(nil))
var _ = (fs.NodeReaddirer)((*FS)(nil))
var _ = (fs.NodeGetattrer)((*FS)(nil))

func (f *FS) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	switch name {
	case "model":
		setEntryTimeout(out, cacheTTLModels)
		return f.NewInode(ctx, &ModelsDirNode{client: f.client, state: f.state, startTime: f.startTime, diag: f.Diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "new":
		setEntryTimeout(out, cacheTTLStatic)
		return f.NewInode(ctx, &SymlinkNode{target: "model/default/new", startTime: f.startTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	case "conversation":
		setEntryTimeout(out, cacheTTLConversation)
		return f.NewInode(ctx, &ConversationListNode{client: f.client, state: f.state, cloneTimeout: f.cloneTimeout, startTime: f.startTime, parsedCache: f.parsedCache, diag: f.Diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "README.md":
		setEntryTimeout(out, cacheTTLStatic)
		return f.NewInode(ctx, &ReadmeNode{startTime: f.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}
	return nil, syscall.ENOENT
}

func (f *FS) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "README.md", Mode: fuse.S_IFREG},
		{Name: "model", Mode: fuse.S_IFDIR},
		{Name: "new", Mode: syscall.S_IFLNK},
		{Name: "conversation", Mode: fuse.S_IFDIR},
	}), 0
}

func (f *FS) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, f.startTime)
	out.SetTimeout(cacheTTLStatic)
	return 0
}


// --- ReadmeNode: /README.md file with usage documentation ---

// readmeContent contains the embedded documentation for the FUSE filesystem.
// This makes the filesystem self-documenting — users can `cat README.md` from the mount point.
//
//go:embed README.md
var readmeContent string

type ReadmeNode struct {
	fs.Inode
	startTime time.Time
}

var _ = (fs.NodeOpener)((*ReadmeNode)(nil))
var _ = (fs.NodeReader)((*ReadmeNode)(nil))
var _ = (fs.NodeGetattrer)((*ReadmeNode)(nil))

func (r *ReadmeNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

func (r *ReadmeNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data := []byte(readmeContent)
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (r *ReadmeNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = uint64(len(readmeContent))
	setTimestamps(&out.Attr, r.startTime)
	out.SetTimeout(cacheTTLStatic)
	return 0
}


// --- helpers ---

// setTimestamps sets Atime, Mtime, and Ctime on an Attr to the given time.
func setTimestamps(attr *fuse.Attr, t time.Time) {
	sec := uint64(t.Unix())
	nsec := uint32(t.Nanosecond())
	attr.Atime = sec
	attr.Atimensec = nsec
	attr.Mtime = sec
	attr.Mtimensec = nsec
	attr.Ctime = sec
	attr.Ctimensec = nsec
}

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

// slugSanitizerRe matches non-alphanumeric characters for slug sanitization.
var slugSanitizerRe = regexp.MustCompile(`[^a-z0-9]+`)

// messageFileBase returns the base name for a message file, e.g. "00-user" or "01-bash-tool".
// The directory name uses 0-based indexing (seqID-1) to match JSON array conventions.
// The slug parameter should be obtained from shelley.MessageSlug() for proper tool naming.
// maxSeqID is the highest sequence ID in the conversation, used to determine the
// zero-padding width so that ls sorts correctly (e.g. maxSeqID=150 → 000 through 149).
func messageFileBase(seqID int, slug string, maxSeqID int) string {
	// Sanitize slug: replace any non-alphanumeric characters with hyphens
	sanitized := slugSanitizerRe.ReplaceAllString(strings.ToLower(slug), "-")
	sanitized = strings.Trim(sanitized, "-")
	if sanitized == "" {
		sanitized = "unknown"
	}
	// Calculate padding width from the maximum 0-indexed value (maxSeqID - 1).
	// This handles non-contiguous sequence IDs correctly.
	width := 1
	if maxSeqID > 1 {
		width = len(strconv.Itoa(maxSeqID - 1))
	}
	// Directory names are 0-indexed (seqID - 1) to match JSON array conventions
	return fmt.Sprintf("%0*d-%s", width, seqID-1, sanitized)
}

// maxSeqIDFromMessages returns the highest SequenceID in the message slice.
// Returns 0 if the slice is empty.
func maxSeqIDFromMessages(msgs []shelley.Message) int {
	max := 0
	for i := range msgs {
		if msgs[i].SequenceID > max {
			max = msgs[i].SequenceID
		}
	}
	return max
}

// messageDirRe matches message directory names like "0-user" or "1-agent".
var messageDirRe = regexp.MustCompile(`^(\d+)-[a-z0-9-]+$`)

// parseMessageDirName extracts the sequence ID from a message directory name.
// Directory names are 0-indexed, but returns the 1-indexed seqID for API lookups.
// Returns (seqID, ok).
func parseMessageDirName(name string) (int, bool) {
	m := messageDirRe.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	idx, err := strconv.Atoi(m[1])
	if err != nil || idx < 0 {
		return 0, false
	}
	// Convert 0-indexed directory name to 1-indexed seqID
	return idx + 1, true
}


func stableIno(parts ...string) uint64 {
	h := fnv.New64a()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0}) // separator
		}
		h.Write([]byte(p))
	}
	// Ensure non-zero (Ino=0 means auto-assign in go-fuse)
	ino := h.Sum64()
	if ino == 0 {
		ino = 1
	}
	return ino
}

// compile-time interface checks
var _ = (fs.InodeEmbedder)((*FS)(nil))
