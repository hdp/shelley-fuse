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

// --- SymlinkNode: a symlink pointing to a target path ---

type SymlinkNode struct {
	fs.Inode
	target string
}

var _ = (fs.NodeReadlinker)((*SymlinkNode)(nil))
var _ = (fs.NodeGetattrer)((*SymlinkNode)(nil))

func (s *SymlinkNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	return []byte(s.target), 0
}

func (s *SymlinkNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFLNK | 0777
	out.Size = uint64(len(s.target))
	return 0
}

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
		return f.NewInode(ctx, &ModelsDirNode{client: f.client}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "new":
		return f.NewInode(ctx, &NewDirNode{state: f.state}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "conversation":
		return f.NewInode(ctx, &ConversationListNode{client: f.client, state: f.state}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}
	return nil, syscall.ENOENT
}

func (f *FS) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "models", Mode: fuse.S_IFDIR},
		{Name: "new", Mode: fuse.S_IFDIR},
		{Name: "conversation", Mode: fuse.S_IFDIR},
	}), 0
}

// --- ModelsDirNode: /models/ directory listing available models ---

type ModelsDirNode struct {
	fs.Inode
	client *shelley.Client
}

var _ = (fs.NodeLookuper)((*ModelsDirNode)(nil))
var _ = (fs.NodeReaddirer)((*ModelsDirNode)(nil))

func (m *ModelsDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	models, err := m.client.ListModels()
	if err != nil {
		return nil, syscall.EIO
	}
	
	for _, model := range models {
		if model.ID == name {
			return m.NewInode(ctx, &ModelNode{model: model}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
		}
	}
	return nil, syscall.ENOENT
}

func (m *ModelsDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	models, err := m.client.ListModels()
	if err != nil {
		return nil, syscall.EIO
	}
	
	entries := make([]fuse.DirEntry, len(models))
	for i, model := range models {
		entries[i] = fuse.DirEntry{Name: model.ID, Mode: fuse.S_IFDIR}
	}
	return fs.NewListDirStream(entries), 0
}

// --- ModelNode: /models/{model-id}/ directory for a single model ---

type ModelNode struct {
	fs.Inode
	model shelley.Model
}

var _ = (fs.NodeLookuper)((*ModelNode)(nil))
var _ = (fs.NodeReaddirer)((*ModelNode)(nil))

func (m *ModelNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	switch name {
	case "id":
		return m.NewInode(ctx, &ModelFieldNode{value: m.model.ID}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "ready":
		value := "false"
		if m.model.Ready {
			value = "true"
		}
		return m.NewInode(ctx, &ModelFieldNode{value: value}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}
	return nil, syscall.ENOENT
}

func (m *ModelNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "id", Mode: fuse.S_IFREG},
		{Name: "ready", Mode: fuse.S_IFREG},
	}), 0
}

// --- ModelFieldNode: read-only file for a model field (id or ready) ---

type ModelFieldNode struct {
	fs.Inode
	value string
}

var _ = (fs.NodeOpener)((*ModelFieldNode)(nil))
var _ = (fs.NodeReader)((*ModelFieldNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelFieldNode)(nil))

func (m *ModelFieldNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (m *ModelFieldNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data := []byte(m.value + "\n")
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (m *ModelFieldNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
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
	// First check if it's a known local ID (the common case after Readdir adoption)
	cs := c.state.Get(name)
	if cs != nil {
		return c.NewInode(ctx, &ConversationNode{
			localID: name,
			client:  c.client,
			state:   c.state,
		}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}

	// Check if it's a known server ID (return symlink to local ID)
	if localID := c.state.GetByShelleyID(name); localID != "" {
		return c.NewInode(ctx, &SymlinkNode{target: localID}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	}

	// Check if it's a known slug (return symlink to local ID)
	if localID := c.state.GetBySlug(name); localID != "" {
		return c.NewInode(ctx, &SymlinkNode{target: localID}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	}

	// For backwards compatibility, also support lookup by Shelley server ID
	// that isn't yet tracked locally. This handles cases where someone has
	// a server ID from another source (e.g., web UI, API, or old scripts)
	// and wants to access it directly. The conversation will be adopted
	// and assigned a local ID, then a symlink returned.
	serverConvs, err := c.fetchServerConversations()
	if err == nil {
		for _, conv := range serverConvs {
			if conv.ConversationID == name {
				// Adopt this server conversation locally
				slug := ""
				if conv.Slug != nil {
					slug = *conv.Slug
				}
				localID, err := c.state.AdoptWithSlug(name, slug)
				if err != nil {
					return nil, syscall.EIO
				}
				// Return symlink to the local ID
				return c.NewInode(ctx, &SymlinkNode{target: localID}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
			}
			// Also check by slug for not-yet-adopted conversations
			if conv.Slug != nil && *conv.Slug == name {
				localID, err := c.state.AdoptWithSlug(conv.ConversationID, *conv.Slug)
				if err != nil {
					return nil, syscall.EIO
				}
				return c.NewInode(ctx, &SymlinkNode{target: localID}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
			}
		}
	}

	return nil, syscall.ENOENT
}

func (c *ConversationListNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// Adopt any server conversations that aren't tracked locally, and update
	// slugs for already-tracked conversations (slugs may be added later).
	serverConvs, err := c.fetchServerConversations()
	if err == nil {
		for _, conv := range serverConvs {
			slug := ""
			if conv.Slug != nil {
				slug = *conv.Slug
			}
			// AdoptWithSlug handles both cases:
			// - New conversation: creates local entry with slug
			// - Existing conversation: updates slug if it was previously empty
			// Errors are non-fatal; worst case the conversation won't appear
			// in this listing but will be adopted on next Lookup
			_, _ = c.state.AdoptWithSlug(conv.ConversationID, slug)
		}
	}
	// Note: if fetchServerConversations fails, we still return local entries
	// This is intentional - local state should always be accessible

	// Build entries: directories for local IDs, symlinks for server IDs and slugs
	mappings := c.state.ListMappings()
	
	// Track names we've used to avoid duplicates
	usedNames := make(map[string]bool)
	var entries []fuse.DirEntry

	// First add all local IDs as directories (they take priority)
	for _, cs := range mappings {
		entries = append(entries, fuse.DirEntry{Name: cs.LocalID, Mode: fuse.S_IFDIR})
		usedNames[cs.LocalID] = true
	}

	// Then add symlinks for server IDs and slugs (if they don't conflict)
	for _, cs := range mappings {
		// Add symlink for server ID if it exists and doesn't conflict
		if cs.ShelleyConversationID != "" && !usedNames[cs.ShelleyConversationID] {
			entries = append(entries, fuse.DirEntry{Name: cs.ShelleyConversationID, Mode: syscall.S_IFLNK})
			usedNames[cs.ShelleyConversationID] = true
		}

		// Add symlink for slug if it exists, is valid, and doesn't conflict
		if cs.Slug != "" && !usedNames[cs.Slug] && isValidFilename(cs.Slug) {
			entries = append(entries, fuse.DirEntry{Name: cs.Slug, Mode: syscall.S_IFLNK})
			usedNames[cs.Slug] = true
		}
	}

	return fs.NewListDirStream(entries), 0
}

// isValidFilename checks if a string is valid for use as a filename.
// Rejects empty strings and strings containing path separators or null bytes.
func isValidFilename(name string) bool {
	if name == "" {
		return false
	}
	// Reject path separators and null bytes
	if strings.ContainsAny(name, "/\x00") {
		return false
	}
	// Reject . and .. which have special meaning
	if name == "." || name == ".." {
		return false
	}
	return true
}

// fetchServerConversations retrieves the list of conversations from the Shelley server.
func (c *ConversationListNode) fetchServerConversations() ([]shelley.Conversation, error) {
	data, err := c.client.ListConversations()
	if err != nil {
		return nil, err
	}

	var convs []shelley.Conversation
	if err := json.Unmarshal(data, &convs); err != nil {
		return nil, err
	}
	return convs, nil
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
	case "id":
		return c.NewInode(ctx, &ConvMetaFieldNode{localID: c.localID, state: c.state, field: "id"}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "slug":
		return c.NewInode(ctx, &ConvMetaFieldNode{localID: c.localID, state: c.state, field: "slug"}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
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
		{Name: "id", Mode: fuse.S_IFREG},
		{Name: "slug", Mode: fuse.S_IFREG},
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
		result, err := n.client.StartConversation(message, cs.Model, cs.Cwd)
		if err != nil {
			return 0, syscall.EIO
		}
		if err := n.state.MarkCreated(n.localID, result.ConversationID, result.Slug); err != nil {
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

// --- ConvMetaFieldNode: read-only file for conversation metadata (id or slug) ---

type ConvMetaFieldNode struct {
	fs.Inode
	localID string
	state   *state.Store
	field   string // "id" or "slug"
}

var _ = (fs.NodeOpener)((*ConvMetaFieldNode)(nil))
var _ = (fs.NodeReader)((*ConvMetaFieldNode)(nil))
var _ = (fs.NodeGetattrer)((*ConvMetaFieldNode)(nil))

func (m *ConvMetaFieldNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (m *ConvMetaFieldNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	cs := m.state.Get(m.localID)
	if cs == nil {
		return nil, syscall.ENOENT
	}
	if !cs.Created {
		// Conversation not yet created on the backend — no id or slug available
		return nil, syscall.ENOENT
	}

	var value string
	switch m.field {
	case "id":
		value = cs.ShelleyConversationID
	case "slug":
		value = cs.Slug
	default:
		return nil, syscall.ENOENT
	}

	data := []byte(value + "\n")
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (m *ConvMetaFieldNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
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
