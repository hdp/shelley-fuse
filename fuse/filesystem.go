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
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

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
}

// NewFS creates a new Shelley FUSE filesystem.
// cloneTimeout specifies how long to wait before cleaning up unconversed clone IDs.
func NewFS(client shelley.ShelleyClient, store *state.Store, cloneTimeout time.Duration) *FS {
	return &FS{client: client, state: store, cloneTimeout: cloneTimeout, startTime: time.Now()}
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
		return f.NewInode(ctx, &ConversationListNode{client: f.client, state: f.state, cloneTimeout: f.cloneTimeout, startTime: f.startTime}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
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
        001-user.json    → specific message (named by slug)
        100-bash-tool.md → tool call (## tool call header)
        101-bash-result.md → tool result (## tool result header)
        last/{N}.md      → last N messages
        since/{slug}/{N}.md → messages since Nth matching {slug}

` + "```" + `

## Common Operations

` + "```" + `bash
# List available models
ls models/

# Check default model
readlink models/default

# List conversations
ls conversation/

# Read last 5 messages
cat conversation/$ID/messages/last/5.md

# Read messages since your last message
cat conversation/$ID/messages/since/user/1.md

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
		value := "false"
		if m.model.Ready {
			value = "true"
		}
		return m.NewInode(ctx, &ModelFieldNode{value: value, startTime: m.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}
	return nil, syscall.ENOENT
}

func (m *ModelNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "id", Mode: fuse.S_IFREG},
		{Name: "ready", Mode: fuse.S_IFREG},
	}), 0
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
}

var _ = (fs.NodeLookuper)((*ConversationListNode)(nil))
var _ = (fs.NodeReaddirer)((*ConversationListNode)(nil))
var _ = (fs.NodeGetattrer)((*ConversationListNode)(nil))

func (c *ConversationListNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// First check if it's a known local ID (the common case after Readdir adoption)
	cs := c.state.Get(name)
	if cs != nil {
		return c.NewInode(ctx, &ConversationNode{
			localID:   name,
			client:    c.client,
			state:     c.state,
			startTime: c.startTime,
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
				// Return symlink to the local ID - use newly adopted conversation's time
				localCS := c.state.Get(localID)
				symlinkTime := c.startTime
				if localCS != nil && !localCS.CreatedAt.IsZero() {
					symlinkTime = localCS.CreatedAt
				}
				return c.NewInode(ctx, &SymlinkNode{target: localID, startTime: symlinkTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
			}
			// Also check by slug for not-yet-adopted conversations
			if conv.Slug != nil && *conv.Slug == name {
				localID, err := c.state.AdoptWithSlug(conv.ConversationID, *conv.Slug)
				if err != nil {
					return nil, syscall.EIO
				}
				localCS := c.state.Get(localID)
				symlinkTime := c.startTime
				if localCS != nil && !localCS.CreatedAt.IsZero() {
					symlinkTime = localCS.CreatedAt
				}
				return c.NewInode(ctx, &SymlinkNode{target: localID, startTime: symlinkTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
			}
		}
	}

	return nil, syscall.ENOENT
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
			// AdoptWithSlug handles the case where a conversation is not yet tracked locally
			// Errors are non-fatal; worst case the conversation won't appear
			// in this listing but will be adopted on next Lookup
			_, _ = c.state.AdoptWithSlug(conv.ConversationID, slug)
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

func (c *ConversationListNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, c.startTime)
	return 0
}

// --- ConversationNode: /conversation/{id}/ directory ---

type ConversationNode struct {
	fs.Inode
	localID   string
	client    shelley.ShelleyClient
	state     *state.Store
	startTime time.Time // FS start time, used as fallback
}

var _ = (fs.NodeLookuper)((*ConversationNode)(nil))
var _ = (fs.NodeReaddirer)((*ConversationNode)(nil))
var _ = (fs.NodeGetattrer)((*ConversationNode)(nil))

// getConversationTime returns the appropriate timestamp for this conversation.
// Uses conversation CreatedAt if available, otherwise falls back to FS start time.
func (c *ConversationNode) getConversationTime() time.Time {
	cs := c.state.Get(c.localID)
	if cs != nil && !cs.CreatedAt.IsZero() {
		return cs.CreatedAt
	}
	return c.startTime
}

func (c *ConversationNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	switch name {
	case "ctl":
		return c.NewInode(ctx, &CtlNode{localID: c.localID, state: c.state, startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "new":
		return c.NewInode(ctx, &ConvNewNode{localID: c.localID, client: c.client, state: c.state, startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "messages":
		return c.NewInode(ctx, &MessagesDirNode{localID: c.localID, client: c.client, state: c.state, startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "id":
		return c.NewInode(ctx, &ConvMetaFieldNode{localID: c.localID, state: c.state, field: "id", startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "slug":
		return c.NewInode(ctx, &ConvMetaFieldNode{localID: c.localID, state: c.state, field: "slug", startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "fuse_id":
		return c.NewInode(ctx, &ConvStatusFieldNode{localID: c.localID, client: c.client, state: c.state, field: "fuse_id", startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "created":
		return c.NewInode(ctx, &ConvStatusFieldNode{localID: c.localID, client: c.client, state: c.state, field: "created", startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "created_at":
		return c.NewInode(ctx, &ConvStatusFieldNode{localID: c.localID, client: c.client, state: c.state, field: "created_at", startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
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
	}

	return nil, syscall.ENOENT
}

func (c *ConversationNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries := []fuse.DirEntry{
		{Name: "ctl", Mode: fuse.S_IFREG},
		{Name: "new", Mode: fuse.S_IFREG},
		{Name: "messages", Mode: fuse.S_IFDIR},
		{Name: "id", Mode: fuse.S_IFREG},
		{Name: "slug", Mode: fuse.S_IFREG},
		{Name: "fuse_id", Mode: fuse.S_IFREG},
		{Name: "created", Mode: fuse.S_IFREG},
		{Name: "created_at", Mode: fuse.S_IFREG},
	}

	// Include model and cwd symlinks only if set
	cs := c.state.Get(c.localID)
	if cs != nil && cs.Model != "" {
		entries = append(entries, fuse.DirEntry{Name: "model", Mode: syscall.S_IFLNK})
	}
	if cs != nil && cs.Cwd != "" {
		entries = append(entries, fuse.DirEntry{Name: "cwd", Mode: syscall.S_IFLNK})
	}

	return fs.NewListDirStream(entries), 0
}

func (c *ConversationNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, c.getConversationTime())
	return 0
}

// --- MessagesDirNode: /conversation/{id}/messages/ directory ---

type MessagesDirNode struct {
	fs.Inode
	localID   string
	client    shelley.ShelleyClient
	state     *state.Store
	startTime time.Time
}

var _ = (fs.NodeLookuper)((*MessagesDirNode)(nil))
var _ = (fs.NodeReaddirer)((*MessagesDirNode)(nil))
var _ = (fs.NodeGetattrer)((*MessagesDirNode)(nil))

func (m *MessagesDirNode) getConversationTime() time.Time {
	cs := m.state.Get(m.localID)
	if cs != nil && !cs.CreatedAt.IsZero() {
		return cs.CreatedAt
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

	// Named message files: {NNN}-{slug}.json, {NNN}-{slug}.md
	// We need to verify the filename matches the expected slug for that message
	if seqNum, fmt, ok := parseNamedMessageFile(name); ok {
		// Fetch conversation and verify the filename matches the computed slug
		cs := m.state.Get(m.localID)
		if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
			return nil, syscall.ENOENT
		}

		convData, err := m.client.GetConversation(cs.ShelleyConversationID)
		if err != nil {
			return nil, syscall.EIO
		}

		msgs, err := shelley.ParseMessages(convData)
		if err != nil {
			return nil, syscall.EIO
		}

		// Find the message by sequence number
		msg := shelley.GetMessage(msgs, seqNum)
		if msg == nil {
			return nil, syscall.ENOENT
		}

		// Build tool map and compute expected slug
		msgPtrs := make([]*shelley.Message, len(msgs))
		for i := range msgs {
			msgPtrs[i] = &msgs[i]
		}
		toolMap := shelley.BuildToolNameMap(msgPtrs)
		expectedSlug := shelley.MessageSlug(msg, toolMap)
		expectedBase := messageFileBase(seqNum, expectedSlug)

		// Verify the filename matches the expected slug
		base := strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".md")
		if base != expectedBase {
			return nil, syscall.ENOENT
		}

		return m.NewInode(ctx, &ConvContentNode{
			localID: m.localID, client: m.client, state: m.state,
			query: contentQuery{kind: queryBySeq, seqNum: seqNum, format: fmt}, startTime: m.startTime,
			messageTime: shelley.ParseMessageTime(msg),
		}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
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

	// List individual messages with named files (001-user.json, 100-bash-tool.md, ...)
	cs := m.state.Get(m.localID)
	if cs != nil && cs.Created && cs.ShelleyConversationID != "" {
		convData, err := m.client.GetConversation(cs.ShelleyConversationID)
		if err == nil {
			msgs, err := shelley.ParseMessages(convData)
			if err == nil {
				// Build tool name map for proper tool_result naming
				msgPtrs := make([]*shelley.Message, len(msgs))
				for i := range msgs {
					msgPtrs[i] = &msgs[i]
				}
				toolMap := shelley.BuildToolNameMap(msgPtrs)

				for i := range msgs {
					slug := shelley.MessageSlug(&msgs[i], toolMap)
					base := messageFileBase(msgs[i].SequenceID, slug)
					entries = append(entries, fuse.DirEntry{Name: base + ".json", Mode: fuse.S_IFREG})
					entries = append(entries, fuse.DirEntry{Name: base + ".md", Mode: fuse.S_IFREG})
				}
			}
		}
	}

	return fs.NewListDirStream(entries), 0
}

func (m *MessagesDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, m.getConversationTime())
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
	localID   string
	client    shelley.ShelleyClient
	state     *state.Store
	startTime time.Time // fallback if conversation has no CreatedAt
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
	} else {
		// Subsequent writes: send message to existing conversation
		// Pass the model from state to ensure we use the same model as the conversation
		if err := h.node.client.SendMessage(cs.ShelleyConversationID, message, cs.Model); err != nil {
			log.Printf("SendMessage failed for conversation %s: %v", cs.ShelleyConversationID, err)
			return syscall.EIO
		}
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
	case "created":
		if cs.Created {
			value = "true"
		} else {
			value = "false"
		}
	case "created_at":
		if !cs.CreatedAt.IsZero() {
			value = cs.CreatedAt.Format(time.RFC3339)
		}
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

// --- ConvMetaFieldNode: read-only file for conversation metadata (id or slug) ---

type ConvMetaFieldNode struct {
	fs.Inode
	localID   string
	state     *state.Store
	field     string    // "id" or "slug"
	startTime time.Time // fallback if conversation has no CreatedAt
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
		if cs.Slug == "" {
			return nil, syscall.ENOENT
		}
		value = cs.Slug
	default:
		return nil, syscall.ENOENT
	}

	data := []byte(value + "\n")
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (m *ConvMetaFieldNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	// Use conversation creation time if available, otherwise fall back to FS start time
	cs := m.state.Get(m.localID)
	if cs != nil && !cs.CreatedAt.IsZero() {
		setTimestamps(&out.Attr, cs.CreatedAt)
	} else {
		setTimestamps(&out.Attr, m.startTime)
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
		startTime: q.startTime,
	}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
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

// namedMessageFileRe matches named message files like "001-user.json" or "002-shelley.md".
var namedMessageFileRe = regexp.MustCompile(`^(\d+)-[a-z0-9-]+\.(json|md)$`)

// parseNamedMessageFile extracts the sequence number and format from a named message filename.
// Returns (seqNum, format, ok).
func parseNamedMessageFile(name string) (int, contentFormat, bool) {
	m := namedMessageFileRe.FindStringSubmatch(name)
	if m == nil {
		return 0, 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, 0, false
	}
	var format contentFormat
	switch m[2] {
	case "json":
		format = formatJSON
	case "md":
		format = formatMD
	}
	return n, format, true
}

// compile-time interface checks
var _ = (fs.InodeEmbedder)((*FS)(nil))
