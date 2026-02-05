package fuse

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"shelley-fuse/jsonfs"
	"shelley-fuse/metadata"
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

// ParsedMessageCache caches parsed messages and toolMaps at the conversation level.
// This avoids re-parsing the same conversation data for every file lookup in messages/.
// The cache is invalidated when a message is sent to a conversation.
type ParsedMessageCache struct {
	mu      sync.RWMutex
	entries map[string]*parsedCacheEntry
	ttl     time.Duration
}

type parsedCacheEntry struct {
	messages  []shelley.Message
	toolMap   map[string]string
	expiresAt time.Time
}

// NewParsedMessageCache creates a new cache with the given TTL.
// A TTL of 0 disables caching.
func NewParsedMessageCache(ttl time.Duration) *ParsedMessageCache {
	return &ParsedMessageCache{
		entries: make(map[string]*parsedCacheEntry),
		ttl:     ttl,
	}
}

// GetOrParse returns cached messages and toolMap for a conversation, or parses the data and caches it.
// The rawData is the JSON response from GetConversation.
// If c is nil or caching is disabled (ttl=0), it parses without caching.
func (c *ParsedMessageCache) GetOrParse(conversationID string, rawData []byte) ([]shelley.Message, map[string]string, error) {
	if c != nil && c.ttl > 0 {
		c.mu.RLock()
		entry := c.entries[conversationID]
		c.mu.RUnlock()

		if entry != nil && time.Now().Before(entry.expiresAt) {
			return entry.messages, entry.toolMap, nil
		}
	}

	// Parse the conversation data
	msgs, err := shelley.ParseMessages(rawData)
	if err != nil {
		return nil, nil, err
	}

	// Build the tool name map
	msgPtrs := make([]*shelley.Message, len(msgs))
	for i := range msgs {
		msgPtrs[i] = &msgs[i]
	}
	toolMap := shelley.BuildToolNameMap(msgPtrs)

	// Cache the result
	if c != nil && c.ttl > 0 {
		c.mu.Lock()
		c.entries[conversationID] = &parsedCacheEntry{
			messages:  msgs,
			toolMap:   toolMap,
			expiresAt: time.Now().Add(c.ttl),
		}
		c.mu.Unlock()
	}

	return msgs, toolMap, nil
}

// Invalidate removes the cached entry for a conversation.
// Invalidate removes the cached entry for a conversation.
// Safe to call on nil receiver.
func (c *ParsedMessageCache) Invalidate(conversationID string) {
	if c != nil && c.ttl > 0 {
		c.mu.Lock()
		delete(c.entries, conversationID)
		c.mu.Unlock()
	}
}

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

// FS is the root inode of the Shelley FUSE filesystem.
type FS struct {
	fs.Inode
	client       shelley.ShelleyClient
	state        *state.Store
	cloneTimeout time.Duration
	startTime    time.Time
	parsedCache  *ParsedMessageCache // caches parsed messages and toolMaps
}

// NewFS creates a new Shelley FUSE filesystem.
// cloneTimeout specifies how long to wait before cleaning up unconversed clone IDs.
func NewFS(client shelley.ShelleyClient, store *state.Store, cloneTimeout time.Duration) *FS {
	return &FS{
		client:       client,
		state:        store,
		cloneTimeout: cloneTimeout,
		startTime:    time.Now(),
		parsedCache:  NewParsedMessageCache(5 * time.Second), // same as typical HTTP cache TTL
	}
}

// NewFSWithCacheTTL creates a new Shelley FUSE filesystem with a custom cache TTL.
func NewFSWithCacheTTL(client shelley.ShelleyClient, store *state.Store, cloneTimeout, cacheTTL time.Duration) *FS {
	return &FS{
		client:       client,
		state:        store,
		cloneTimeout: cloneTimeout,
		startTime:    time.Now(),
		parsedCache:  NewParsedMessageCache(cacheTTL),
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
	case "models":
		return f.NewInode(ctx, &ModelsDirNode{client: f.client, startTime: f.startTime}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "new":
		return f.NewInode(ctx, &NewDirNode{state: f.state, startTime: f.startTime}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "conversation":
		return f.NewInode(ctx, &ConversationListNode{client: f.client, state: f.state, cloneTimeout: f.cloneTimeout, startTime: f.startTime, parsedCache: f.parsedCache}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "README.md":
		return f.NewInode(ctx, &ReadmeNode{startTime: f.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}
	return nil, syscall.ENOENT
}

func (f *FS) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "README.md", Mode: fuse.S_IFREG},
		{Name: "models", Mode: fuse.S_IFDIR},
		{Name: "new", Mode: fuse.S_IFDIR},
		{Name: "conversation", Mode: fuse.S_IFDIR},
	}), 0
}

func (f *FS) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, f.startTime)
	return 0
}

// --- ReadmeNode: /README.md file with usage documentation ---

// readmeContent contains the embedded documentation for the FUSE filesystem.
// This makes the filesystem self-documenting — users can `cat /shelley/README.md`.
const readmeContent = `# Shelley FUSE

A FUSE filesystem that exposes the Shelley API, allowing shell tools to interact with Shelley conversations.

## Quick Start

` + "```" + `bash
# Allocate a new conversation
ID=$(cat new/clone)

# Configure model and working directory (optional)
echo "model=claude-sonnet-4.5 cwd=$PWD" > conversation/$ID/ctl

# Send first message (creates conversation on backend)
echo "Hello, Shelley!" > conversation/$ID/new

# Read the response
cat conversation/$ID/messages/all.md

# Send follow-up
echo "Thanks!" > conversation/$ID/new
` + "```" + `

## Filesystem Layout

` + "```" + `
/
  README.md              → this file
  models/                → available models
    default              → symlink to default model
    {model-id}/          → directory per model
      id                 → model ID
      ready              → present if model is ready (absence = not ready)
  new/
    clone                → read to allocate a new conversation ID
  conversation/          → all conversations
    {id}/                → directory per conversation
      ctl                → read/write config; read-only after first message
      new                → write here to send messages
      id                 → Shelley server conversation ID
      slug               → conversation slug (if set)
      created            → present if created on backend (absence = not created)
      messages/          → all message content
        all.json         → full conversation as JSON
        all.md           → full conversation as Markdown
        count            → number of messages
        001-user/        → message directory (named by slug)
          message_id     → message UUID
          conversation_id → conversation ID
          sequence_id    → sequence number
          type           → message type (user, agent, etc.)
          created_at     → timestamp
          content.md     → markdown rendering
          llm_data/      → unpacked JSON (if present)
          usage_data/    → unpacked JSON (if present)
        last/{N}/        → directory with symlinks to last N messages
          003-user       → symlink to ../../003-user
          004-agent      → symlink to ../../004-agent
          ...
        since/{slug}/{N}/ → directory with symlinks to messages after Nth {slug}
          005-agent      → symlink to ../../../005-agent
          006-user       → symlink to ../../../006-user
          ...

` + "```" + `

## Common Operations

` + "```" + `bash
# List available models
ls models/

# Check default model
readlink models/default

# List conversations
ls conversation/

# List last 5 messages (symlinks to message directories)
ls conversation/$ID/messages/last/5/

# Read content of all last 5 messages
cat conversation/$ID/messages/last/5/*/content.md

# List messages since your last message
ls conversation/$ID/messages/since/user/1/

# Read content of messages since your last message
cat conversation/$ID/messages/since/user/1/*/content.md

# Get message count
cat conversation/$ID/messages/count

# Check if conversation is created
test -e conversation/$ID/created && echo created
` + "```" + `
`

type ReadmeNode struct {
	fs.Inode
	startTime time.Time
}

var _ = (fs.NodeOpener)((*ReadmeNode)(nil))
var _ = (fs.NodeReader)((*ReadmeNode)(nil))
var _ = (fs.NodeGetattrer)((*ReadmeNode)(nil))

func (r *ReadmeNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (r *ReadmeNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data := []byte(readmeContent)
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (r *ReadmeNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = uint64(len(readmeContent))
	setTimestamps(&out.Attr, r.startTime)
	return 0
}

// --- ModelsDirNode: /models/ directory listing available models ---

type ModelsDirNode struct {
	fs.Inode
	client    shelley.ShelleyClient
	startTime time.Time
}

var _ = (fs.NodeLookuper)((*ModelsDirNode)(nil))
var _ = (fs.NodeReaddirer)((*ModelsDirNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelsDirNode)(nil))

func (m *ModelsDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	result, err := m.client.ListModels()
	if err != nil {
		return nil, syscall.EIO
	}
	
	// Handle "default" symlink
	if name == "default" {
		if result.DefaultModel == "" {
			return nil, syscall.ENOENT
		}
		return m.NewInode(ctx, &SymlinkNode{target: result.DefaultModel, startTime: m.startTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	}
	
	for _, model := range result.Models {
		if model.ID == name {
			return m.NewInode(ctx, &ModelNode{model: model, startTime: m.startTime}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
		}
	}
	return nil, syscall.ENOENT
}

func (m *ModelsDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	result, err := m.client.ListModels()
	if err != nil {
		return nil, syscall.EIO
	}
	
	// Capacity for models + optional default symlink
	entries := make([]fuse.DirEntry, 0, len(result.Models)+1)
	
	// Add "default" symlink if default model is set
	if result.DefaultModel != "" {
		entries = append(entries, fuse.DirEntry{Name: "default", Mode: syscall.S_IFLNK})
	}
	
	// Add all model directories
	for _, model := range result.Models {
		entries = append(entries, fuse.DirEntry{Name: model.ID, Mode: fuse.S_IFDIR})
	}
	return fs.NewListDirStream(entries), 0
}

func (m *ModelsDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, m.startTime)
	return 0
}

// --- ModelNode: /models/{model-id}/ directory for a single model ---

type ModelNode struct {
	fs.Inode
	model     shelley.Model
	startTime time.Time
}

var _ = (fs.NodeLookuper)((*ModelNode)(nil))
var _ = (fs.NodeReaddirer)((*ModelNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelNode)(nil))

func (m *ModelNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	switch name {
	case "id":
		return m.NewInode(ctx, &ModelFieldNode{value: m.model.ID, startTime: m.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "ready":
		// Presence/absence semantics: file exists only when model is ready
		if !m.model.Ready {
			return nil, syscall.ENOENT
		}
		return m.NewInode(ctx, &ModelReadyNode{startTime: m.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}
	return nil, syscall.ENOENT
}

func (m *ModelNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries := []fuse.DirEntry{
		{Name: "id", Mode: fuse.S_IFREG},
	}
	// Presence/absence semantics: only include "ready" if model is ready
	if m.model.Ready {
		entries = append(entries, fuse.DirEntry{Name: "ready", Mode: fuse.S_IFREG})
	}
	return fs.NewListDirStream(entries), 0
}

func (m *ModelNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, m.startTime)
	return 0
}

// --- ModelFieldNode: read-only file for a model field (id or ready) ---

type ModelFieldNode struct {
	fs.Inode
	value     string
	startTime time.Time
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
	setTimestamps(&out.Attr, m.startTime)
	return 0
}

// --- ModelReadyNode: empty file indicating model is ready (presence/absence semantics) ---

type ModelReadyNode struct {
	fs.Inode
	startTime time.Time
}

var _ = (fs.NodeOpener)((*ModelReadyNode)(nil))
var _ = (fs.NodeReader)((*ModelReadyNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelReadyNode)(nil))

func (m *ModelReadyNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (m *ModelReadyNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	// Empty file - presence indicates ready
	return fuse.ReadResultData(nil), 0
}

func (m *ModelReadyNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = 0
	setTimestamps(&out.Attr, m.startTime)
	return 0
}

// --- NewDirNode: /new/ directory containing clone ---

type NewDirNode struct {
	fs.Inode
	state     *state.Store
	startTime time.Time
}

var _ = (fs.NodeLookuper)((*NewDirNode)(nil))
var _ = (fs.NodeReaddirer)((*NewDirNode)(nil))
var _ = (fs.NodeGetattrer)((*NewDirNode)(nil))

func (n *NewDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if name == "clone" {
		return n.NewInode(ctx, &CloneNode{state: n.state, startTime: n.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}
	return nil, syscall.ENOENT
}

func (n *NewDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "clone", Mode: fuse.S_IFREG},
	}), 0
}

func (n *NewDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, n.startTime)
	return 0
}

// --- CloneNode: each Open generates a new conversation ID ---

type CloneNode struct {
	fs.Inode
	state     *state.Store
	startTime time.Time
}

var _ = (fs.NodeOpener)((*CloneNode)(nil))
var _ = (fs.NodeGetattrer)((*CloneNode)(nil))

func (c *CloneNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	id, err := c.state.Clone()
	if err != nil {
		return nil, 0, syscall.EIO
	}
	return &CloneFileHandle{id: id}, fuse.FOPEN_DIRECT_IO, 0
}

func (c *CloneNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	setTimestamps(&out.Attr, c.startTime)
	return 0
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
	client       shelley.ShelleyClient
	state        *state.Store
	cloneTimeout time.Duration
	startTime    time.Time
	parsedCache  *ParsedMessageCache
}

var _ = (fs.NodeLookuper)((*ConversationListNode)(nil))
var _ = (fs.NodeReaddirer)((*ConversationListNode)(nil))
var _ = (fs.NodeGetattrer)((*ConversationListNode)(nil))

func (c *ConversationListNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// First check if it's a known local ID (the common case after Readdir adoption)
	cs := c.state.Get(name)
	if cs != nil {
		return c.NewInode(ctx, &ConversationNode{
			localID:     name,
			client:      c.client,
			state:       c.state,
			startTime:   c.startTime,
			parsedCache: c.parsedCache,
		}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}

	// Check if it's a known server ID (return symlink to local ID)
	if localID := c.state.GetByShelleyID(name); localID != "" {
		localCS := c.state.Get(localID)
		symlinkTime := c.startTime
		if localCS != nil && !localCS.CreatedAt.IsZero() {
			symlinkTime = localCS.CreatedAt
		}
		return c.NewInode(ctx, &SymlinkNode{target: localID, startTime: symlinkTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	}

	// Check if it's a known slug (return symlink to local ID)
	if localID := c.state.GetBySlug(name); localID != "" {
		localCS := c.state.Get(localID)
		symlinkTime := c.startTime
		if localCS != nil && !localCS.CreatedAt.IsZero() {
			symlinkTime = localCS.CreatedAt
		}
		return c.NewInode(ctx, &SymlinkNode{target: localID, startTime: symlinkTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	}

	// For backwards compatibility, also support lookup by Shelley server ID
	// that isn't yet tracked locally. This handles cases where someone has
	// a server ID from another source (e.g., web UI, API, or old scripts)
	// and wants to access it directly. The conversation will be adopted
	// and assigned a local ID, then a symlink returned.
	//
	// We check both active and archived conversations to support accessing
	// archived conversations by their server ID or slug.
	serverConvs, err := c.fetchServerConversations()
	if err == nil {
		if inode, errno := c.lookupInConversationList(ctx, name, serverConvs); errno == 0 {
			return inode, 0
		}
	}

	// Also check archived conversations
	archivedConvs, err := c.fetchArchivedConversations()
	if err == nil {
		if inode, errno := c.lookupInConversationList(ctx, name, archivedConvs); errno == 0 {
			return inode, 0
		}
	}

	return nil, syscall.ENOENT
}

// lookupInConversationList searches for a conversation by ID or slug in the given list.
// If found, it adopts the conversation locally and returns a symlink to the local ID.
func (c *ConversationListNode) lookupInConversationList(ctx context.Context, name string, convs []shelley.Conversation) (*fs.Inode, syscall.Errno) {
	for _, conv := range convs {
		if conv.ConversationID == name {
			// Adopt this server conversation locally with API metadata
			slug := ""
			if conv.Slug != nil {
				slug = *conv.Slug
			}
			localID, err := c.state.AdoptWithMetadata(name, slug, conv.CreatedAt, conv.UpdatedAt)
			if err != nil {
				return nil, syscall.EIO
			}
			// Return symlink to the local ID - use API timestamp if available
			symlinkTime := c.getConversationTimestamps(localID).Ctime
			if symlinkTime.IsZero() {
				symlinkTime = c.startTime
			}
			return c.NewInode(ctx, &SymlinkNode{target: localID, startTime: symlinkTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
		}
		// Also check by slug for not-yet-adopted conversations
		if conv.Slug != nil && *conv.Slug == name {
			localID, err := c.state.AdoptWithMetadata(conv.ConversationID, *conv.Slug, conv.CreatedAt, conv.UpdatedAt)
			if err != nil {
				return nil, syscall.EIO
			}
			symlinkTime := c.getConversationTimestamps(localID).Ctime
			if symlinkTime.IsZero() {
				symlinkTime = c.startTime
			}
			return c.NewInode(ctx, &SymlinkNode{target: localID, startTime: symlinkTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
		}
	}
	return nil, syscall.ENOENT
}

// getConversationTimestamps returns timestamps for a conversation using the metadata mapping.
// Falls back to local CreatedAt if API timestamps are not available.
func (c *ConversationListNode) getConversationTimestamps(localID string) metadata.Timestamps {
	cs := c.state.Get(localID)
	if cs == nil {
		return metadata.Timestamps{}
	}
	// Use API timestamps if available
	if cs.APICreatedAt != "" || cs.APIUpdatedAt != "" {
		fields := metadata.ConversationFields{
			CreatedAt: cs.APICreatedAt,
			UpdatedAt: cs.APIUpdatedAt,
		}
		return metadata.ConversationMapping.Apply(fields.ToMap())
	}
	// Fall back to local CreatedAt for all timestamps
	if !cs.CreatedAt.IsZero() {
		return metadata.Timestamps{
			Ctime: cs.CreatedAt,
			Mtime: cs.CreatedAt,
			Atime: cs.CreatedAt,
		}
	}
	return metadata.Timestamps{}
}

func (c *ConversationListNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// Adopt any server conversations that aren't tracked locally, and update
	// slugs for already-tracked conversations (slugs are always provided immediately).
	serverConvs, err := c.fetchServerConversations()

	// Build a set of valid server conversation IDs for filtering stale entries
	validServerIDs := make(map[string]bool)
	serverFetchSucceeded := err == nil

	if serverFetchSucceeded {
		for _, conv := range serverConvs {
			validServerIDs[conv.ConversationID] = true
			slug := ""
			if conv.Slug != nil {
				slug = *conv.Slug
			}
			// AdoptWithMetadata handles the case where a conversation is not yet tracked locally
			// and also updates API timestamps. Errors are non-fatal; worst case the conversation
			// won't appear in this listing but will be adopted on next Lookup
			_, _ = c.state.AdoptWithMetadata(conv.ConversationID, slug, conv.CreatedAt, conv.UpdatedAt)
		}
	}
	// Note: if fetchServerConversations fails, we still return local entries
	// This is intentional - local state should always be accessible

	// Build entries: directories for local IDs, symlinks for server IDs and slugs
	mappings := c.state.ListMappings()

	// Filter mappings and handle cleanup:
	// - Only include created conversations in listing (uncreated ones are still accessible via Lookup)
	// - Clean up expired uncreated conversations (lazy cleanup)
	// - Filter out stale mappings with Shelley IDs that no longer exist on server
	var filteredMappings []state.ConversationState
	for _, cs := range mappings {
		if !cs.Created {
			// Uncreated conversation - check if it should be cleaned up
			if c.cloneTimeout > 0 && !cs.CreatedAt.IsZero() && time.Since(cs.CreatedAt) > c.cloneTimeout {
				// Expired - delete it (errors are non-fatal, will retry next Readdir)
				_ = c.state.Delete(cs.LocalID)
			}
			// Either way, don't include uncreated conversations in listing
			continue
		}

		if cs.ShelleyConversationID == "" {
			// Created but no server ID - shouldn't happen, but include it
			filteredMappings = append(filteredMappings, cs)
		} else if !serverFetchSucceeded {
			// Server fetch failed, include all to avoid data loss
			filteredMappings = append(filteredMappings, cs)
		} else if validServerIDs[cs.ShelleyConversationID] {
			// Has server ID and it still exists on server
			filteredMappings = append(filteredMappings, cs)
		}
		// Otherwise: has a Shelley ID that's not on server anymore - skip (stale)
	}

	// Track names we've used to avoid duplicates
	usedNames := make(map[string]bool)
	var entries []fuse.DirEntry

	// First add all local IDs as directories (they take priority)
	for _, cs := range filteredMappings {
		entries = append(entries, fuse.DirEntry{Name: cs.LocalID, Mode: fuse.S_IFDIR})
		usedNames[cs.LocalID] = true
	}

	// Then add symlinks for server IDs and slugs (if they don't conflict)
	for _, cs := range filteredMappings {
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

// fetchArchivedConversations retrieves the list of archived conversations from the Shelley server.
func (c *ConversationListNode) fetchArchivedConversations() ([]shelley.Conversation, error) {
	data, err := c.client.ListArchivedConversations()
	if err != nil {
		return nil, err
	}

	var convs []shelley.Conversation
	if err := json.Unmarshal(data, &convs); err != nil {
		return nil, err
	}
	return convs, nil
}

func (c *ConversationListNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, c.startTime)
	return 0
}

// --- ConversationNode: /conversation/{id}/ directory ---

type ConversationNode struct {
	fs.Inode
	localID     string
	client      shelley.ShelleyClient
	state       *state.Store
	startTime   time.Time // FS start time, used as fallback
	parsedCache *ParsedMessageCache
}

var _ = (fs.NodeLookuper)((*ConversationNode)(nil))
var _ = (fs.NodeReaddirer)((*ConversationNode)(nil))
var _ = (fs.NodeGetattrer)((*ConversationNode)(nil))
var _ = (fs.NodeCreater)((*ConversationNode)(nil))
var _ = (fs.NodeUnlinker)((*ConversationNode)(nil))

// getConversationTime returns the appropriate timestamp for this conversation.
// Uses conversation CreatedAt if available, otherwise falls back to FS start time.
// getConversationTimestamps returns timestamps for this conversation using the metadata mapping.
// This provides separate ctime and mtime values based on API metadata (created_at, updated_at).
func (c *ConversationNode) getConversationTimestamps() metadata.Timestamps {
	cs := c.state.Get(c.localID)
	if cs == nil {
		return metadata.Timestamps{}
	}
	// Use API timestamps if available
	if cs.APICreatedAt != "" || cs.APIUpdatedAt != "" {
		fields := metadata.ConversationFields{
			CreatedAt: cs.APICreatedAt,
			UpdatedAt: cs.APIUpdatedAt,
		}
		return metadata.ConversationMapping.Apply(fields.ToMap())
	}
	// Fall back to local CreatedAt for all timestamps
	if !cs.CreatedAt.IsZero() {
		return metadata.Timestamps{
			Ctime: cs.CreatedAt,
			Mtime: cs.CreatedAt,
			Atime: cs.CreatedAt,
		}
	}
	return metadata.Timestamps{}
}

// getConversationTime returns a single timestamp for backwards compatibility.
// Prefers ctime from API metadata, falls back to local CreatedAt, then startTime.
func (c *ConversationNode) getConversationTime() time.Time {
	ts := c.getConversationTimestamps()
	if !ts.Ctime.IsZero() {
		return ts.Ctime
	}
	return c.startTime
}

// buildConversationJSONMap builds a map of conversation data suitable for jsonfs.
// This exposes API fields as files at the conversation directory root.
func (c *ConversationNode) buildConversationJSONMap() map[string]any {
	cs := c.state.Get(c.localID)
	if cs == nil {
		return nil
	}

	result := make(map[string]any)

	// Always expose id (conversation_id from API, or empty if not created)
	if cs.ShelleyConversationID != "" {
		result["id"] = cs.ShelleyConversationID
	}

	// Always expose slug if set
	if cs.Slug != "" {
		result["slug"] = cs.Slug
	}

	// Fetch API data for created conversations
	if cs.Created && cs.ShelleyConversationID != "" {
		convData, err := c.client.GetConversation(cs.ShelleyConversationID)
		if err == nil {
			var conv shelley.Conversation
			if err := json.Unmarshal(convData, &conv); err == nil {
				if conv.CreatedAt != "" {
					result["created_at"] = conv.CreatedAt
				}
				if conv.UpdatedAt != "" {
					result["updated_at"] = conv.UpdatedAt
				}
			}
		}
	}

	return result
}

func (c *ConversationNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// Special files with custom behavior
	switch name {
	case "ctl":
		return c.NewInode(ctx, &CtlNode{localID: c.localID, state: c.state, startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "new":
		return c.NewInode(ctx, &ConvNewNode{localID: c.localID, client: c.client, state: c.state, startTime: c.startTime, parsedCache: c.parsedCache}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "messages":
		return c.NewInode(ctx, &MessagesDirNode{localID: c.localID, client: c.client, state: c.state, startTime: c.startTime, parsedCache: c.parsedCache}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "fuse_id":
		return c.NewInode(ctx, &ConvStatusFieldNode{localID: c.localID, client: c.client, state: c.state, field: "fuse_id", startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "created":
		// Presence/absence semantics: file exists only when conversation is created on backend
		cs := c.state.Get(c.localID)
		if cs == nil || !cs.Created {
			return nil, syscall.ENOENT
		}
		return c.NewInode(ctx, &ConvCreatedNode{localID: c.localID, state: c.state, startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "model":
		cs := c.state.Get(c.localID)
		if cs == nil || cs.Model == "" {
			return nil, syscall.ENOENT
		}
		target := "../../models/" + cs.Model
		return c.NewInode(ctx, &SymlinkNode{target: target, startTime: c.getConversationTime()}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	case "cwd":
		cs := c.state.Get(c.localID)
		if cs == nil || cs.Cwd == "" {
			return nil, syscall.ENOENT
		}
		return c.NewInode(ctx, &CwdSymlinkNode{
			localID:   c.localID,
			state:     c.state,
			startTime: c.startTime,
		}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	case "archived":
		// Presence/absence semantics: file exists only when conversation is archived
		cs := c.state.Get(c.localID)
		if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
			return nil, syscall.ENOENT
		}
		archived, err := c.client.IsConversationArchived(cs.ShelleyConversationID)
		if err != nil || !archived {
			return nil, syscall.ENOENT
		}
		return c.NewInode(ctx, &ArchivedNode{
			localID:   c.localID,
			client:    c.client,
			state:     c.state,
			startTime: c.startTime,
		}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}

	// For all other fields, use jsonfs to expose conversation JSON data
	convMap := c.buildConversationJSONMap()
	if convMap == nil {
		return nil, syscall.ENOENT
	}

	value, ok := convMap[name]
	if !ok {
		return nil, syscall.ENOENT
	}

	config := &jsonfs.Config{StartTime: c.getConversationTime()}
	node := jsonfs.NewNode(value, config)

	// Determine mode based on value type
	mode := uint32(fuse.S_IFREG)
	switch value.(type) {
	case map[string]any, []any:
		mode = fuse.S_IFDIR
	}

	return c.NewInode(ctx, node, fs.StableAttr{Mode: mode}), 0
}

func (c *ConversationNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// Special files always present
	entries := []fuse.DirEntry{
		{Name: "ctl", Mode: fuse.S_IFREG},
		{Name: "new", Mode: fuse.S_IFREG},
		{Name: "messages", Mode: fuse.S_IFDIR},
		{Name: "fuse_id", Mode: fuse.S_IFREG},
	}

	cs := c.state.Get(c.localID)
	// Presence/absence semantics: only include "created" if conversation is created on backend
	if cs != nil && cs.Created {
		entries = append(entries, fuse.DirEntry{Name: "created", Mode: fuse.S_IFREG})
	}

	// Include model and cwd symlinks only if set
	if cs != nil && cs.Model != "" {
		entries = append(entries, fuse.DirEntry{Name: "model", Mode: syscall.S_IFLNK})
	}
	if cs != nil && cs.Cwd != "" {
		entries = append(entries, fuse.DirEntry{Name: "cwd", Mode: syscall.S_IFLNK})
	}

	// Include archived file only if the conversation is archived
	if cs != nil && cs.Created && cs.ShelleyConversationID != "" {
		archived, err := c.client.IsConversationArchived(cs.ShelleyConversationID)
		if err == nil && archived {
			entries = append(entries, fuse.DirEntry{Name: "archived", Mode: fuse.S_IFREG})
		}
	}

	// Add JSON fields from conversation data via jsonfs
	convMap := c.buildConversationJSONMap()
	if convMap != nil {
		for name := range convMap {
			entries = append(entries, fuse.DirEntry{Name: name, Mode: fuse.S_IFREG})
		}
	}

	return fs.NewListDirStream(entries), 0
}

func (c *ConversationNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	c.getConversationTimestamps().ApplyWithFallback(&out.Attr, c.startTime)
	return 0
}

// Create handles creating files in the conversation directory.
// Only "archived" can be created, which archives the conversation.
func (c *ConversationNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	if name != "archived" {
		return nil, nil, 0, syscall.EPERM
	}

	cs := c.state.Get(c.localID)
	if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
		// Can't archive a conversation that doesn't exist on the backend
		return nil, nil, 0, syscall.ENOENT
	}

	// Archive the conversation
	if err := c.client.ArchiveConversation(cs.ShelleyConversationID); err != nil {
		return nil, nil, 0, syscall.EIO
	}

	// Return the archived file node
	inode := c.NewInode(ctx, &ArchivedNode{
		localID:   c.localID,
		client:    c.client,
		state:     c.state,
		startTime: c.startTime,
	}, fs.StableAttr{Mode: fuse.S_IFREG})

	return inode, nil, fuse.FOPEN_DIRECT_IO, 0
}

// Unlink handles removing files from the conversation directory.
// Only "archived" can be removed, which unarchives the conversation.
func (c *ConversationNode) Unlink(ctx context.Context, name string) syscall.Errno {
	if name != "archived" {
		return syscall.EPERM
	}

	cs := c.state.Get(c.localID)
	if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
		return syscall.ENOENT
	}

	// Check if the conversation is actually archived
	archived, err := c.client.IsConversationArchived(cs.ShelleyConversationID)
	if err != nil {
		return syscall.EIO
	}
	if !archived {
		return syscall.ENOENT
	}

	// Unarchive the conversation
	if err := c.client.UnarchiveConversation(cs.ShelleyConversationID); err != nil {
		return syscall.EIO
	}

	return 0
}

// --- MessagesDirNode: /conversation/{id}/messages/ directory ---

type MessagesDirNode struct {
	fs.Inode
	localID     string
	client      shelley.ShelleyClient
	state       *state.Store
	startTime   time.Time
	parsedCache *ParsedMessageCache
}

var _ = (fs.NodeLookuper)((*MessagesDirNode)(nil))
var _ = (fs.NodeReaddirer)((*MessagesDirNode)(nil))
var _ = (fs.NodeGetattrer)((*MessagesDirNode)(nil))

// getConversationTimestamps returns timestamps for the conversation using the metadata mapping.
func (m *MessagesDirNode) getConversationTimestamps() metadata.Timestamps {
	cs := m.state.Get(m.localID)
	if cs == nil {
		return metadata.Timestamps{}
	}
	// Use API timestamps if available
	if cs.APICreatedAt != "" || cs.APIUpdatedAt != "" {
		fields := metadata.ConversationFields{
			CreatedAt: cs.APICreatedAt,
			UpdatedAt: cs.APIUpdatedAt,
		}
		return metadata.ConversationMapping.Apply(fields.ToMap())
	}
	// Fall back to local CreatedAt for all timestamps
	if !cs.CreatedAt.IsZero() {
		return metadata.Timestamps{
			Ctime: cs.CreatedAt,
			Mtime: cs.CreatedAt,
			Atime: cs.CreatedAt,
		}
	}
	return metadata.Timestamps{}
}

func (m *MessagesDirNode) getConversationTime() time.Time {
	ts := m.getConversationTimestamps()
	if !ts.Ctime.IsZero() {
		return ts.Ctime
	}
	return m.startTime
}

func (m *MessagesDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	switch name {
	case "last":
		return m.NewInode(ctx, &QueryDirNode{localID: m.localID, client: m.client, state: m.state, kind: queryLast, startTime: m.startTime}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "since":
		return m.NewInode(ctx, &QueryDirNode{localID: m.localID, client: m.client, state: m.state, kind: querySince, startTime: m.startTime}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "count":
		return m.NewInode(ctx, &MessageCountNode{localID: m.localID, client: m.client, state: m.state, startTime: m.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}

	// all.json, all.md
	format, ok := parseFormat(name)
	if ok {
		base := strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".md")
		if base == "all" {
			return m.NewInode(ctx, &ConvContentNode{
				localID: m.localID, client: m.client, state: m.state,
				query: contentQuery{kind: queryAll, format: format}, startTime: m.startTime,
			}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
		}
	}

	// Named message directories: {NNN}-{slug}/
	// We need to verify the directory name matches the expected slug for that message
	if seqNum, ok := parseMessageDirName(name); ok {
		// Fetch conversation and verify the name matches the computed slug
		cs := m.state.Get(m.localID)
		if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
			return nil, syscall.ENOENT
		}

		convData, err := m.client.GetConversation(cs.ShelleyConversationID)
		if err != nil {
			return nil, syscall.EIO
		}

		// Use the parsed message cache for efficient repeated lookups
		msgs, toolMap, err := m.parsedCache.GetOrParse(cs.ShelleyConversationID, convData)
		if err != nil {
			return nil, syscall.EIO
		}

		// Find the message by sequence number
		msg := shelley.GetMessage(msgs, seqNum)
		if msg == nil {
			return nil, syscall.ENOENT
		}

		// Compute expected slug using the cached toolMap
		expectedSlug := shelley.MessageSlug(msg, toolMap)
		expectedName := messageFileBase(seqNum, expectedSlug)

		// Verify the directory name matches the expected slug
		if name != expectedName {
			return nil, syscall.ENOENT
		}

		return m.NewInode(ctx, &MessageDirNode{
			message:   *msg,
			toolMap:   toolMap,
			startTime: m.startTime,
		}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}

	return nil, syscall.ENOENT
}

func (m *MessagesDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries := []fuse.DirEntry{
		{Name: "all.json", Mode: fuse.S_IFREG},
		{Name: "all.md", Mode: fuse.S_IFREG},
		{Name: "count", Mode: fuse.S_IFREG},
		{Name: "last", Mode: fuse.S_IFDIR},
		{Name: "since", Mode: fuse.S_IFDIR},
	}

	// List individual messages as directories (001-user/, 002-agent/, ...)
	cs := m.state.Get(m.localID)
	if cs != nil && cs.Created && cs.ShelleyConversationID != "" {
		convData, err := m.client.GetConversation(cs.ShelleyConversationID)
		if err == nil {
			// Use the parsed message cache for efficiency
			msgs, toolMap, err := m.parsedCache.GetOrParse(cs.ShelleyConversationID, convData)
			if err == nil {
				for i := range msgs {
					slug := shelley.MessageSlug(&msgs[i], toolMap)
					base := messageFileBase(msgs[i].SequenceID, slug)
					entries = append(entries, fuse.DirEntry{Name: base, Mode: fuse.S_IFDIR})
				}
			}
		}
	}

	return fs.NewListDirStream(entries), 0
}

func (m *MessagesDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	m.getConversationTimestamps().ApplyWithFallback(&out.Attr, m.startTime)
	return 0
}

// --- MessageDirNode: /conversation/{id}/messages/{NNN}-{slug}/ directory ---
// Represents a single message as a directory with field files.

type MessageDirNode struct {
	fs.Inode
	message   shelley.Message
	toolMap   map[string]string // for computing markdown content
	startTime time.Time
}

var _ = (fs.NodeLookuper)((*MessageDirNode)(nil))
var _ = (fs.NodeReaddirer)((*MessageDirNode)(nil))
var _ = (fs.NodeGetattrer)((*MessageDirNode)(nil))

// messageTimestamps returns timestamps for this message using the metadata mapping.
func (m *MessageDirNode) messageTimestamps() metadata.Timestamps {
	fields := metadata.MessageFields{
		CreatedAt: m.message.CreatedAt,
	}
	return metadata.MessageMapping.Apply(fields.ToMap())
}

// messageTime returns a single timestamp for backwards compatibility.
func (m *MessageDirNode) messageTime() time.Time {
	ts := m.messageTimestamps()
	if !ts.Ctime.IsZero() {
		return ts.Ctime
	}
	return m.startTime
}

func (m *MessageDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	t := m.messageTime()
	switch name {
	case "message_id":
		return m.NewInode(ctx, &MessageFieldNode{value: m.message.MessageID, startTime: t}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "conversation_id":
		return m.NewInode(ctx, &MessageFieldNode{value: m.message.ConversationID, startTime: t}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "sequence_id":
		return m.NewInode(ctx, &MessageFieldNode{value: strconv.Itoa(m.message.SequenceID), startTime: t}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "type":
		return m.NewInode(ctx, &MessageFieldNode{value: m.message.Type, startTime: t}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "created_at":
		return m.NewInode(ctx, &MessageFieldNode{value: m.message.CreatedAt, startTime: t}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "llm_data":
		if m.message.LLMData == nil || *m.message.LLMData == "" {
			return nil, syscall.ENOENT
		}
		config := &jsonfs.Config{StartTime: t}
		node, err := jsonfs.NewNodeFromJSON([]byte(*m.message.LLMData), config)
		if err != nil {
			// If JSON parsing fails, return as a file
			return m.NewInode(ctx, &MessageFieldNode{value: *m.message.LLMData, startTime: t}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
		}
		return m.NewInode(ctx, node, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "usage_data":
		if m.message.UsageData == nil || *m.message.UsageData == "" {
			return nil, syscall.ENOENT
		}
		config := &jsonfs.Config{StartTime: t}
		node, err := jsonfs.NewNodeFromJSON([]byte(*m.message.UsageData), config)
		if err != nil {
			// If JSON parsing fails, return as a file
			return m.NewInode(ctx, &MessageFieldNode{value: *m.message.UsageData, startTime: t}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
		}
		return m.NewInode(ctx, node, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "content.md":
		// Generate markdown rendering of this single message
		content := shelley.FormatMarkdown([]shelley.Message{m.message})
		return m.NewInode(ctx, &MessageFieldNode{value: string(content), startTime: t, noNewline: true}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}
	return nil, syscall.ENOENT
}

func (m *MessageDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries := []fuse.DirEntry{
		{Name: "message_id", Mode: fuse.S_IFREG},
		{Name: "conversation_id", Mode: fuse.S_IFREG},
		{Name: "sequence_id", Mode: fuse.S_IFREG},
		{Name: "type", Mode: fuse.S_IFREG},
		{Name: "created_at", Mode: fuse.S_IFREG},
		{Name: "content.md", Mode: fuse.S_IFREG},
	}
	// Only include llm_data if present
	if m.message.LLMData != nil && *m.message.LLMData != "" {
		// Check if it's valid JSON object/array
		trimmed := strings.TrimSpace(*m.message.LLMData)
		if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
			(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			entries = append(entries, fuse.DirEntry{Name: "llm_data", Mode: fuse.S_IFDIR})
		} else {
			entries = append(entries, fuse.DirEntry{Name: "llm_data", Mode: fuse.S_IFREG})
		}
	}
	// Only include usage_data if present
	if m.message.UsageData != nil && *m.message.UsageData != "" {
		trimmed := strings.TrimSpace(*m.message.UsageData)
		if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
			(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			entries = append(entries, fuse.DirEntry{Name: "usage_data", Mode: fuse.S_IFDIR})
		} else {
			entries = append(entries, fuse.DirEntry{Name: "usage_data", Mode: fuse.S_IFREG})
		}
	}
	return fs.NewListDirStream(entries), 0
}

func (m *MessageDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	m.messageTimestamps().ApplyWithFallback(&out.Attr, m.startTime)
	return 0
}

// --- MessageFieldNode: read-only file for message field values ---

type MessageFieldNode struct {
	fs.Inode
	value     string
	startTime time.Time
	noNewline bool // if true, don't add trailing newline
}

var _ = (fs.NodeOpener)((*MessageFieldNode)(nil))
var _ = (fs.NodeReader)((*MessageFieldNode)(nil))
var _ = (fs.NodeGetattrer)((*MessageFieldNode)(nil))

func (m *MessageFieldNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (m *MessageFieldNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data := []byte(m.value)
	if !m.noNewline {
		data = append(data, '\n')
	}
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (m *MessageFieldNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	size := len(m.value)
	if !m.noNewline {
		size++
	}
	out.Size = uint64(size)
	setTimestamps(&out.Attr, m.startTime)
	return 0
}

// --- MessageCountNode: /conversation/{id}/messages/count ---

type MessageCountNode struct {
	fs.Inode
	localID   string
	client    shelley.ShelleyClient
	state     *state.Store
	startTime time.Time
}

var _ = (fs.NodeOpener)((*MessageCountNode)(nil))
var _ = (fs.NodeReader)((*MessageCountNode)(nil))
var _ = (fs.NodeGetattrer)((*MessageCountNode)(nil))

func (m *MessageCountNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (m *MessageCountNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	cs := m.state.Get(m.localID)
	value := "0"
	if cs != nil && cs.Created && cs.ShelleyConversationID != "" {
		convData, err := m.client.GetConversation(cs.ShelleyConversationID)
		if err == nil {
			msgs, err := shelley.ParseMessages(convData)
			if err == nil {
				value = strconv.Itoa(len(msgs))
			}
		}
	}
	data := []byte(value + "\n")
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (m *MessageCountNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	cs := m.state.Get(m.localID)
	if cs != nil && !cs.CreatedAt.IsZero() {
		setTimestamps(&out.Attr, cs.CreatedAt)
	} else {
		setTimestamps(&out.Attr, m.startTime)
	}
	return 0
}

// --- CtlNode: write key=value pairs, read-only after conversation created ---

type CtlNode struct {
	fs.Inode
	localID   string
	state     *state.Store
	startTime time.Time // fallback if conversation has no CreatedAt
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
	// Use conversation creation time if available, otherwise fall back to FS start time
	if !cs.CreatedAt.IsZero() {
		setTimestamps(&out.Attr, cs.CreatedAt)
	} else {
		setTimestamps(&out.Attr, c.startTime)
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
	localID     string
	client      shelley.ShelleyClient
	state       *state.Store
	startTime   time.Time // fallback if conversation has no CreatedAt
	parsedCache *ParsedMessageCache
}

var _ = (fs.NodeOpener)((*ConvNewNode)(nil))
var _ = (fs.NodeGetattrer)((*ConvNewNode)(nil))
var _ = (fs.NodeSetattrer)((*ConvNewNode)(nil))

func (n *ConvNewNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return &ConvNewFileHandle{
		node: n,
	}, fuse.FOPEN_DIRECT_IO, 0
}

// ConvNewFileHandle buffers writes and sends the message on Flush (close)
type ConvNewFileHandle struct {
	node    *ConvNewNode
	buffer  []byte
	flushed bool
	mu      sync.Mutex
}

var _ = (fs.FileWriter)((*ConvNewFileHandle)(nil))
var _ = (fs.FileFlusher)((*ConvNewFileHandle)(nil))

func (h *ConvNewFileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Append to buffer - message will be sent on Flush
	h.buffer = append(h.buffer, data...)
	return uint32(len(data)), 0
}

// Flush is called synchronously during close(2), so the caller will block until
// the message is sent. This ensures the conversation is created before close returns.
// Note: Flush may be called multiple times for dup'd file descriptors.
func (h *ConvNewFileHandle) Flush(ctx context.Context) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Only send the message once, even if Flush is called multiple times
	if h.flushed {
		return 0
	}

	cs := h.node.state.Get(h.node.localID)
	if cs == nil {
		return syscall.ENOENT
	}

	message := strings.TrimRight(string(h.buffer), "\n")
	if message == "" {
		return 0 // Don't set flushed for empty buffers - allow retry
	}

	h.flushed = true // Only set when we actually have data to send

	if !cs.Created {
		// First write: create the conversation on the Shelley backend
		result, err := h.node.client.StartConversation(message, cs.Model, cs.Cwd)
		if err != nil {
			log.Printf("StartConversation failed for %s: %v", h.node.localID, err)
			return syscall.EIO
		}
		if err := h.node.state.MarkCreated(h.node.localID, result.ConversationID, result.Slug); err != nil {
			return syscall.EIO
		}
		// Invalidate the parsed message cache since the conversation was just created
		h.node.parsedCache.Invalidate(result.ConversationID)
	} else {
		// Subsequent writes: send message to existing conversation
		// Pass the model from state to ensure we use the same model as the conversation
		if err := h.node.client.SendMessage(cs.ShelleyConversationID, message, cs.Model); err != nil {
			log.Printf("SendMessage failed for conversation %s: %v", cs.ShelleyConversationID, err)
			return syscall.EIO
		}
		// Invalidate the parsed message cache since the conversation was modified
		h.node.parsedCache.Invalidate(cs.ShelleyConversationID)
	}

	return 0
}

func (n *ConvNewNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0222
	// Use conversation creation time if available, otherwise fall back to FS start time
	cs := n.state.Get(n.localID)
	if cs != nil && !cs.CreatedAt.IsZero() {
		setTimestamps(&out.Attr, cs.CreatedAt)
	} else {
		setTimestamps(&out.Attr, n.startTime)
	}
	return 0
}

func (n *ConvNewNode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	return n.Getattr(ctx, f, out)
}


// --- ConvStatusFieldNode: read-only file for conversation status fields ---

type ConvStatusFieldNode struct {
	fs.Inode
	localID   string
	client    shelley.ShelleyClient
	state     *state.Store
	field     string
	startTime time.Time
}

var _ = (fs.NodeOpener)((*ConvStatusFieldNode)(nil))
var _ = (fs.NodeReader)((*ConvStatusFieldNode)(nil))
var _ = (fs.NodeGetattrer)((*ConvStatusFieldNode)(nil))

func (f *ConvStatusFieldNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (f *ConvStatusFieldNode) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	cs := f.state.Get(f.localID)
	if cs == nil {
		return nil, syscall.ENOENT
	}

	var value string
	switch f.field {
	case "fuse_id":
		value = cs.LocalID
	default:
		return nil, syscall.ENOENT
	}

	data := []byte(value + "\n")
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (f *ConvStatusFieldNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	cs := f.state.Get(f.localID)
	if cs != nil && !cs.CreatedAt.IsZero() {
		setTimestamps(&out.Attr, cs.CreatedAt)
	} else {
		setTimestamps(&out.Attr, f.startTime)
	}
	return 0
}

// --- ConvCreatedNode: empty file indicating conversation is created (presence/absence semantics) ---
// The file's mtime is set to the conversation creation time.

type ConvCreatedNode struct {
	fs.Inode
	localID   string
	state     *state.Store
	startTime time.Time
}

var _ = (fs.NodeOpener)((*ConvCreatedNode)(nil))
var _ = (fs.NodeReader)((*ConvCreatedNode)(nil))
var _ = (fs.NodeGetattrer)((*ConvCreatedNode)(nil))

func (f *ConvCreatedNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (f *ConvCreatedNode) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	// Empty file - presence indicates created
	return fuse.ReadResultData(nil), 0
}

func (f *ConvCreatedNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = 0
	cs := f.state.Get(f.localID)
	if cs != nil && !cs.CreatedAt.IsZero() {
		setTimestamps(&out.Attr, cs.CreatedAt)
	} else {
		setTimestamps(&out.Attr, f.startTime)
	}
	return 0
}

// --- CwdSymlinkNode: symlink pointing to the conversation's working directory ---

type CwdSymlinkNode struct {
	fs.Inode
	localID   string
	state     *state.Store
	startTime time.Time
}

var _ = (fs.NodeReadlinker)((*CwdSymlinkNode)(nil))
var _ = (fs.NodeGetattrer)((*CwdSymlinkNode)(nil))

func (c *CwdSymlinkNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	cs := c.state.Get(c.localID)
	if cs == nil || cs.Cwd == "" {
		return nil, syscall.ENOENT
	}
	return []byte(cs.Cwd), 0
}

func (c *CwdSymlinkNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	cs := c.state.Get(c.localID)
	if cs == nil || cs.Cwd == "" {
		return syscall.ENOENT
	}
	out.Mode = syscall.S_IFLNK | 0777
	out.Size = uint64(len(cs.Cwd))
	if !cs.CreatedAt.IsZero() {
		setTimestamps(&out.Attr, cs.CreatedAt)
	} else {
		setTimestamps(&out.Attr, c.startTime)
	}
	return 0
}

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
	localID   string
	client    shelley.ShelleyClient
	state     *state.Store
	kind      queryKind // queryLast or querySince
	person    string    // set for since/{person}/
	startTime time.Time // fallback if conversation has no CreatedAt
}

var _ = (fs.NodeLookuper)((*QueryDirNode)(nil))
var _ = (fs.NodeReaddirer)((*QueryDirNode)(nil))
var _ = (fs.NodeGetattrer)((*QueryDirNode)(nil))

func (q *QueryDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// If this is since/ (no person set), the child is a person directory
	if q.kind == querySince && q.person == "" {
		return q.NewInode(ctx, &QueryDirNode{
			localID: q.localID, client: q.client, state: q.state,
			kind: q.kind, person: name, startTime: q.startTime,
		}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}

	// The child is {N} - a directory containing symlinks to message directories
	n, err := strconv.Atoi(name)
	if err != nil || n <= 0 {
		return nil, syscall.ENOENT
	}

	return q.NewInode(ctx, &QueryResultDirNode{
		localID:   q.localID,
		client:    q.client,
		state:     q.state,
		kind:      q.kind,
		n:         n,
		person:    q.person,
		startTime: q.startTime,
	}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
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
	localID   string
	client    shelley.ShelleyClient
	state     *state.Store
	kind      queryKind // queryLast or querySince
	n         int       // the N in last/{N} or since/{person}/{N}
	person    string    // set for since/{person}/{N}
	startTime time.Time
}

var _ = (fs.NodeLookuper)((*QueryResultDirNode)(nil))
var _ = (fs.NodeReaddirer)((*QueryResultDirNode)(nil))
var _ = (fs.NodeGetattrer)((*QueryResultDirNode)(nil))

// getFilteredMessages returns the messages that match the query.
func (q *QueryResultDirNode) getFilteredMessages() ([]shelley.Message, map[string]string, error) {
	cs := q.state.Get(q.localID)
	if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
		return nil, nil, nil
	}

	convData, err := q.client.GetConversation(cs.ShelleyConversationID)
	if err != nil {
		return nil, nil, err
	}

	msgs, err := shelley.ParseMessages(convData)
	if err != nil {
		return nil, nil, err
	}

	// Build toolMap for slug computation
	msgPtrs := make([]*shelley.Message, len(msgs))
	for i := range msgs {
		msgPtrs[i] = &msgs[i]
	}
	toolMap := shelley.BuildToolNameMap(msgPtrs)

	var filtered []shelley.Message
	switch q.kind {
	case queryLast:
		filtered = shelley.FilterLast(msgs, q.n)
	case querySince:
		filtered = shelley.FilterSince(msgs, q.person, q.n)
	}

	return filtered, toolMap, nil
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
	msgs, toolMap, err := q.getFilteredMessages()
	if err != nil {
		return nil, syscall.EIO
	}
	if msgs == nil {
		return nil, syscall.ENOENT
	}

	// Look for a message matching the name
	for i := range msgs {
		slug := shelley.MessageSlug(&msgs[i], toolMap)
		base := messageFileBase(msgs[i].SequenceID, slug)
		if base == name {
			// Found the message - return a symlink to it
			target := q.symlinkPrefix() + base
			return q.NewInode(ctx, &SymlinkNode{target: target, startTime: q.startTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
		}
	}

	return nil, syscall.ENOENT
}

func (q *QueryResultDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	msgs, toolMap, err := q.getFilteredMessages()
	if err != nil {
		return nil, syscall.EIO
	}

	entries := make([]fuse.DirEntry, 0, len(msgs))
	for i := range msgs {
		slug := shelley.MessageSlug(&msgs[i], toolMap)
		base := messageFileBase(msgs[i].SequenceID, slug)
		entries = append(entries, fuse.DirEntry{Name: base, Mode: syscall.S_IFLNK})
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

// messageFileBase returns the base name for a message file, e.g. "001-user" or "002-bash-tool".
// The slug parameter should be obtained from shelley.MessageSlug() for proper tool naming.
func messageFileBase(seqID int, slug string) string {
	// Sanitize slug: replace any non-alphanumeric characters with hyphens
	sanitized := slugSanitizerRe.ReplaceAllString(strings.ToLower(slug), "-")
	sanitized = strings.Trim(sanitized, "-")
	if sanitized == "" {
		sanitized = "unknown"
	}
	return fmt.Sprintf("%03d-%s", seqID, sanitized)
}

// messageDirRe matches message directory names like "001-user" or "002-agent".
var messageDirRe = regexp.MustCompile(`^(\d+)-[a-z0-9-]+$`)

// parseMessageDirName extracts the sequence number from a message directory name.
// Returns (seqNum, ok).
func parseMessageDirName(name string) (int, bool) {
	m := messageDirRe.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// --- ArchivedNode: presence/absence file for archived status ---
// When present, the conversation is archived. Touch to archive, rm to unarchive.

type ArchivedNode struct {
	fs.Inode
	localID   string
	client    shelley.ShelleyClient
	state     *state.Store
	startTime time.Time
}

var _ = (fs.NodeGetattrer)((*ArchivedNode)(nil))
var _ = (fs.NodeOpener)((*ArchivedNode)(nil))
var _ = (fs.NodeReader)((*ArchivedNode)(nil))
var _ = (fs.NodeSetattrer)((*ArchivedNode)(nil))

func (a *ArchivedNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	cs := a.state.Get(a.localID)
	
	// Default timestamp is CreatedAt or startTime
	timestamp := a.startTime
	if cs != nil && !cs.CreatedAt.IsZero() {
		timestamp = cs.CreatedAt
	}
	
	// ArchivedNode only exists when conversation is archived, so use UpdatedAt
	// as the timestamp (represents when the conversation was last modified/archived)
	if cs != nil && cs.ShelleyConversationID != "" {
		convData, err := a.client.GetConversation(cs.ShelleyConversationID)
		if err == nil {
			var conv shelley.Conversation
			if err := json.Unmarshal(convData, &conv); err == nil && conv.UpdatedAt != "" {
				if updatedTime, err := time.Parse(time.RFC3339, conv.UpdatedAt); err == nil {
					timestamp = updatedTime
				}
			}
		}
	}
	
	setTimestamps(&out.Attr, timestamp)
	return 0
}

func (a *ArchivedNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (a *ArchivedNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	// Empty file - presence is what matters
	return fuse.ReadResultData([]byte{}), 0
}

func (a *ArchivedNode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	// Accept time changes (from touch) silently - just return current attributes
	return a.Getattr(ctx, f, out)
}

// compile-time interface checks
var _ = (fs.InodeEmbedder)((*FS)(nil))
