package fuse

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"shelley-fuse/fuse/diag"
	"shelley-fuse/jsonfs"
	"shelley-fuse/metadata"
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
type ParsedMessageCache struct {
	mu      sync.RWMutex
	entries map[string]*parsedCacheEntry
}

type parsedCacheEntry struct {
	messages []shelley.Message
	toolMap  map[string]string
	maxSeqID int    // highest SequenceID (cached to avoid O(N) recomputation)
	checksum uint64 // FNV-1a hash of the raw data used to produce this entry
	rawData  []byte // reference to the raw data slice for fast identity checks
}

// NewParsedMessageCache creates a new content-addressed parse cache.
func NewParsedMessageCache() *ParsedMessageCache {
	return &ParsedMessageCache{
		entries: make(map[string]*parsedCacheEntry),
	}
}

// dataChecksum computes a fast FNV-1a hash of the raw data.
func dataChecksum(data []byte) uint64 {
	// FNV-1a 64-bit
	const offset64 = 14695981039346656037
	const prime64 = 1099511628211
	h := uint64(offset64)
	for _, b := range data {
		h ^= uint64(b)
		h *= prime64
	}
	return h
}

// ParseResult holds the result of parsing conversation data.
type ParseResult struct {
	Messages []shelley.Message
	ToolMap  map[string]string
	MaxSeqID int
}

// GetOrParse returns cached messages and toolMap for a conversation, or parses the data and caches it.
// The rawData is the JSON response from GetConversation. The cache returns the previously parsed
// result only if rawData has the same content; otherwise it re-parses and caches.
// It first checks if rawData is the exact same slice (pointer identity) for O(1) cache hits
// when the CachingClient returns the same cached bytes, then falls back to FNV checksum comparison.
func (c *ParsedMessageCache) GetOrParse(conversationID string, rawData []byte) ([]shelley.Message, map[string]string, error) {
	r, err := c.GetOrParseResult(conversationID, rawData)
	if err != nil {
		return nil, nil, err
	}
	return r.Messages, r.ToolMap, nil
}

// GetOrParseResult is like GetOrParse but returns the full ParseResult including MaxSeqID.
func (c *ParsedMessageCache) GetOrParseResult(conversationID string, rawData []byte) (*ParseResult, error) {
	if c != nil {
		c.mu.RLock()
		entry := c.entries[conversationID]
		c.mu.RUnlock()

		if entry != nil {
			// Fast path: pointer identity check. When CachingClient returns
			// the same cached slice, this avoids computing the checksum entirely.
			if len(rawData) == len(entry.rawData) && len(rawData) > 0 &&
				&rawData[0] == &entry.rawData[0] {
				return &ParseResult{Messages: entry.messages, ToolMap: entry.toolMap, MaxSeqID: entry.maxSeqID}, nil
			}
			// Slow path: content-addressed comparison via checksum
			if entry.checksum == dataChecksum(rawData) {
				return &ParseResult{Messages: entry.messages, ToolMap: entry.toolMap, MaxSeqID: entry.maxSeqID}, nil
			}
		}
	}

	// Parse the conversation data
	msgs, err := shelley.ParseMessages(rawData)
	if err != nil {
		return nil, err
	}

	// Build the tool name map
	msgPtrs := make([]*shelley.Message, len(msgs))
	for i := range msgs {
		msgPtrs[i] = &msgs[i]
	}
	toolMap := shelley.BuildToolNameMap(msgPtrs)
	maxSeq := maxSeqIDFromMessages(msgs)

	// Cache the result
	if c != nil {
		c.mu.Lock()
		c.entries[conversationID] = &parsedCacheEntry{
			messages: msgs,
			toolMap:  toolMap,
			maxSeqID: maxSeq,
			checksum: dataChecksum(rawData),
			rawData:  rawData,
		}
		c.mu.Unlock()
	}

	return &ParseResult{Messages: msgs, ToolMap: toolMap, MaxSeqID: maxSeq}, nil
}

// Invalidate removes the cached entry for a conversation.
// Safe to call on nil receiver.
func (c *ParsedMessageCache) Invalidate(conversationID string) {
	if c != nil {
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

// --- ModelsDirNode: /model/ directory listing available models ---

type ModelsDirNode struct {
	fs.Inode
	client    shelley.ShelleyClient
	state     *state.Store
	startTime time.Time
	diag      *diag.Tracker
}

var _ = (fs.NodeLookuper)((*ModelsDirNode)(nil))
var _ = (fs.NodeReaddirer)((*ModelsDirNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelsDirNode)(nil))

func (m *ModelsDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(m.diag, "ModelsDirNode", "Lookup", name)()
	result, err := m.client.ListModels()
	if err != nil {
		return nil, syscall.EIO
	}

	setEntryTimeout(out, cacheTTLModels)

	// Handle "default" symlink — target uses display name
	if name == "default" {
		defName := result.DefaultModelName()
		if defName == "" {
			return nil, syscall.ENOENT
		}
		return m.NewInode(ctx, &SymlinkNode{target: defName, startTime: m.startTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	}

	// Primary lookup: match by display name
	for _, model := range result.Models {
		if model.Name() == name {
			return m.NewInode(ctx, &ModelNode{model: model, client: m.client, state: m.state, startTime: m.startTime, diag: m.diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
		}
	}
	// Fallback: match by internal ID — return symlink to display name
	for _, model := range result.Models {
		if model.ID != model.Name() && model.ID == name {
			return m.NewInode(ctx, &SymlinkNode{target: model.Name(), startTime: m.startTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
		}
	}
	return nil, syscall.ENOENT
}

func (m *ModelsDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	defer diag.Track(m.diag, "ModelsDirNode", "Readdir", "")()
	result, err := m.client.ListModels()
	if err != nil {
		return nil, syscall.EIO
	}

	// Capacity for models + optional default symlink + ID alias symlinks
	entries := make([]fuse.DirEntry, 0, len(result.Models)*2+1)

	// Add "default" symlink if default model is set
	if result.DefaultModel != "" {
		entries = append(entries, fuse.DirEntry{Name: "default", Mode: syscall.S_IFLNK})
	}

	// Add model directories using display names, plus ID symlinks where they differ
	for _, model := range result.Models {
		entries = append(entries, fuse.DirEntry{Name: model.Name(), Mode: fuse.S_IFDIR})
		if model.ID != model.Name() {
			entries = append(entries, fuse.DirEntry{Name: model.ID, Mode: syscall.S_IFLNK})
		}
	}
	return fs.NewListDirStream(entries), 0
}

func (m *ModelsDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, m.startTime)
	out.SetTimeout(cacheTTLModels)
	return 0
}

// --- ModelNode: /model/{model-id}/ directory for a single model ---

type ModelNode struct {
	fs.Inode
	model     shelley.Model
	client    shelley.ShelleyClient
	state     *state.Store
	startTime time.Time
	diag      *diag.Tracker
}

var _ = (fs.NodeLookuper)((*ModelNode)(nil))
var _ = (fs.NodeReaddirer)((*ModelNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelNode)(nil))

func (m *ModelNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	setEntryTimeout(out, cacheTTLModels)
	switch name {
	case "id":
		return m.NewInode(ctx, &ModelFieldNode{value: m.model.ID, startTime: m.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "ready":
		// Presence/absence semantics: file exists only when model is ready
		if !m.model.Ready {
			return nil, syscall.ENOENT
		}
		return m.NewInode(ctx, &ModelReadyNode{startTime: m.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "new":
		return m.NewInode(ctx, &ModelNewDirNode{model: m.model, state: m.state, startTime: m.startTime, diag: m.diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}
	return nil, syscall.ENOENT
}

func (m *ModelNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries := []fuse.DirEntry{
		{Name: "id", Mode: fuse.S_IFREG},
		{Name: "new", Mode: fuse.S_IFDIR},
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
	out.SetTimeout(cacheTTLModels)
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
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

func (m *ModelFieldNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data := []byte(m.value + "\n")
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (m *ModelFieldNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = uint64(len(m.value) + 1)
	setTimestamps(&out.Attr, m.startTime)
	out.SetTimeout(cacheTTLModels)
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
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

func (m *ModelReadyNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	// Empty file - presence indicates ready
	return fuse.ReadResultData(nil), 0
}

func (m *ModelReadyNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = 0
	setTimestamps(&out.Attr, m.startTime)
	out.SetTimeout(cacheTTLModels)
	return 0
}

// --- ModelNewDirNode: /model/{model-id}/new/ directory containing clone ---

type ModelNewDirNode struct {
	fs.Inode
	model     shelley.Model
	state     *state.Store
	startTime time.Time
	diag      *diag.Tracker
}

var _ = (fs.NodeLookuper)((*ModelNewDirNode)(nil))
var _ = (fs.NodeReaddirer)((*ModelNewDirNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelNewDirNode)(nil))

func (n *ModelNewDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	setEntryTimeout(out, cacheTTLModels)
	switch name {
	case "clone":
		return n.NewInode(ctx, &ModelCloneNode{model: n.model, state: n.state, startTime: n.startTime, diag: n.diag}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "start":
		return n.NewInode(ctx, &ModelStartNode{model: n.model, startTime: n.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}
	return nil, syscall.ENOENT
}

func (n *ModelNewDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "clone", Mode: fuse.S_IFREG},
		{Name: "start", Mode: fuse.S_IFREG},
	}), 0
}

func (n *ModelNewDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, n.startTime)
	out.SetTimeout(cacheTTLModels)
	return 0
}

// --- ModelCloneNode: /model/{model-id}/new/clone — clones with model preconfigured ---

type ModelCloneNode struct {
	fs.Inode
	model     shelley.Model
	state     *state.Store
	startTime time.Time
	diag      *diag.Tracker
}

var _ = (fs.NodeOpener)((*ModelCloneNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelCloneNode)(nil))

func (c *ModelCloneNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	defer diag.Track(c.diag, "ModelCloneNode", "Open", c.model.Name())()
	id, err := c.state.Clone()
	if err != nil {
		return nil, 0, syscall.EIO
	}
	// Preconfigure the model on the new conversation
	if err := c.state.SetModel(id, c.model.Name(), c.model.ID); err != nil {
		return nil, 0, syscall.EIO
	}
	return &CloneFileHandle{id: id, diag: c.diag}, fuse.FOPEN_DIRECT_IO, 0
}

func (c *ModelCloneNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	setTimestamps(&out.Attr, c.startTime)
	out.SetTimeout(cacheTTLModels)
	return 0
}

// --- CloneFileHandle: shared file handle for clone nodes ---

type CloneFileHandle struct {
	id   string
	diag *diag.Tracker
}

var _ = (fs.FileReader)((*CloneFileHandle)(nil))

func (h *CloneFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	defer diag.Track(h.diag, "CloneFileHandle", "Read", h.id)()
	data := []byte(h.id + "\n")
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

// --- ModelStartNode: /model/{model}/new/start — executable shell script that creates a conversation ---

// modelStartScriptTemplate is the shell script for /model/{model}/new/start.
// It reads a message from stdin, clones a new conversation using the model-specific
// clone file, sets cwd to the caller's working directory, sends the message,
// and prints the conversation ID.
const modelStartScriptTemplate = `#!/bin/sh
set -e
DIR="$(cd "$(dirname "$0")" && pwd)"
MOUNT="$(cd "$DIR/../../.." && pwd)"
MSG="$(cat)"
[ -z "$MSG" ] && { echo "error: no message provided on stdin" >&2; exit 1; }
ID="$(cat "$DIR/clone")"
printf 'cwd=%s\n' "$PWD" > "$MOUNT/conversation/$ID/ctl"
printf '%s' "$MSG" > "$MOUNT/conversation/$ID/send"
echo "$ID"
`

type ModelStartNode struct {
	fs.Inode
	model     shelley.Model
	startTime time.Time
}

var _ = (fs.NodeOpener)((*ModelStartNode)(nil))
var _ = (fs.NodeReader)((*ModelStartNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelStartNode)(nil))

func (n *ModelStartNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (n *ModelStartNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data := []byte(modelStartScriptTemplate)
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (n *ModelStartNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0555
	out.Size = uint64(len(modelStartScriptTemplate))
	setTimestamps(&out.Attr, n.startTime)
	return 0
}

// --- ConversationListNode: /conversation/ directory ---

type ConversationListNode struct {
	fs.Inode
	client       shelley.ShelleyClient
	state        *state.Store
	cloneTimeout time.Duration
	startTime    time.Time
	parsedCache  *ParsedMessageCache
	diag         *diag.Tracker
}

var _ = (fs.NodeLookuper)((*ConversationListNode)(nil))
var _ = (fs.NodeReaddirer)((*ConversationListNode)(nil))
var _ = (fs.NodeGetattrer)((*ConversationListNode)(nil))

func (c *ConversationListNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(c.diag, "ConversationListNode", "Lookup", name)()
	setEntryTimeout(out, cacheTTLConversation)
	// First check if it's a known local ID (the common case after Readdir adoption)
	cs := c.state.Get(name)
	if cs != nil {
		return c.NewInode(ctx, &ConversationNode{
			localID:     name,
			client:      c.client,
			state:       c.state,
			startTime:   c.startTime,
			parsedCache: c.parsedCache,
			diag:        c.diag,
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
	defer diag.Track(c.diag, "ConversationListNode", "Readdir", "")()
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
	out.SetTimeout(cacheTTLConversation)
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
	diag        *diag.Tracker
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
	defer diag.Track(c.diag, "ConversationNode", "Lookup", c.localID+"/"+name)()
	setEntryTimeout(out, cacheTTLConversation)
	// Special files with custom behavior
	switch name {
	case "ctl":
		return c.NewInode(ctx, &CtlNode{localID: c.localID, client: c.client, state: c.state, startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "send":
		return c.NewInode(ctx, &ConvSendNode{localID: c.localID, client: c.client, state: c.state, startTime: c.startTime, parsedCache: c.parsedCache, diag: c.diag}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "messages":
		return c.NewInode(ctx, &MessagesDirNode{localID: c.localID, client: c.client, state: c.state, startTime: c.startTime, parsedCache: c.parsedCache, diag: c.diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "fuse_id":
		return c.NewInode(ctx, &ConvStatusFieldNode{localID: c.localID, client: c.client, state: c.state, field: "fuse_id", startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "created":
		// Presence/absence semantics: file exists only when conversation is created on backend.
		// Once created, it never disappears → long positive timeout.
		// Before creation, short negative timeout so we notice quickly.
		cs := c.state.Get(c.localID)
		if cs == nil || !cs.Created {
			out.SetEntryTimeout(negTimeout)
			return nil, syscall.ENOENT
		}
		out.SetEntryTimeout(immutableEntryTimeout)
		return c.NewInode(ctx, &ConvCreatedNode{localID: c.localID, state: c.state, startTime: c.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "model":
		// Set once via ctl, never changes after → long positive timeout.
		// Before set, short negative timeout so we notice the ctl write.
		cs := c.state.Get(c.localID)
		if cs == nil || cs.Model == "" {
			out.SetEntryTimeout(negTimeout)
			return nil, syscall.ENOENT
		}
		out.SetEntryTimeout(immutableEntryTimeout)
		target := "../../model/" + cs.Model
		return c.NewInode(ctx, &SymlinkNode{target: target, startTime: c.getConversationTime()}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	case "cwd":
		// Set once via ctl, never changes after → long positive timeout.
		// Before set, short negative timeout so we notice the ctl write.
		cs := c.state.Get(c.localID)
		if cs == nil || cs.Cwd == "" {
			out.SetEntryTimeout(negTimeout)
			return nil, syscall.ENOENT
		}
		out.SetEntryTimeout(immutableEntryTimeout)
		return c.NewInode(ctx, &CwdSymlinkNode{
			localID:   c.localID,
			state:     c.state,
			startTime: c.startTime,
		}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	case "archived":
		// Presence/absence semantics: file exists only when conversation is archived.
		// Can appear and disappear (archive/unarchive) → short timeouts both ways.
		cs := c.state.Get(c.localID)
		if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
			out.SetEntryTimeout(negTimeout)
			return nil, syscall.ENOENT
		}
		archived, err := c.client.IsConversationArchived(cs.ShelleyConversationID)
		if err != nil || !archived {
			out.SetEntryTimeout(volatileEntryTimeout)
			return nil, syscall.ENOENT
		}
		out.SetEntryTimeout(volatileEntryTimeout)
		return c.NewInode(ctx, &ArchivedNode{
			localID:   c.localID,
			client:    c.client,
			state:     c.state,
			startTime: c.startTime,
		}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "waiting_for_input":
		// Symlink to messages/{NNN}-agent/llm_data/EndOfTurn when conversation is waiting for user input.
		// Changes with every message → no entry caching (0 timeout, the default).
		cs := c.state.Get(c.localID)
		if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
			return nil, syscall.ENOENT
		}

		// Fetch and parse the conversation messages
		convData, err := c.client.GetConversation(cs.ShelleyConversationID)
		if err != nil {
			return nil, syscall.EIO
		}

		result, err := c.parsedCache.GetOrParseResult(cs.ShelleyConversationID, convData)
		if err != nil {
			return nil, syscall.EIO
		}

		// Analyze conversation to determine if waiting for input
		status := AnalyzeWaitingForInput(result.Messages, result.ToolMap)
		if !status.Waiting {
			return nil, syscall.ENOENT
		}

		// Construct the symlink target: messages/{NNN}-agent/llm_data/EndOfTurn
		// The directory name uses 0-based indexing (seqID-1)
		agentDirName := messageFileBase(status.LastAgentSeqID, status.LastAgentSlug, result.MaxSeqID)
		target := fmt.Sprintf("messages/%s/llm_data/EndOfTurn", agentDirName)

		return c.NewInode(ctx, &SymlinkNode{target: target, startTime: c.getConversationTime()}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
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

	config := &jsonfs.Config{
		StartTime:    c.getConversationTime(),
		CacheTimeout: 10 * time.Second, // conversation metadata is semi-stable
	}
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
	defer diag.Track(c.diag, "ConversationNode", "Readdir", c.localID)()
	// Special files always present
	entries := []fuse.DirEntry{
		{Name: "ctl", Mode: fuse.S_IFREG},
		{Name: "send", Mode: fuse.S_IFREG},
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

	// Include waiting_for_input symlink only when conversation is waiting for user input
	if cs != nil && cs.Created && cs.ShelleyConversationID != "" {
		convData, err := c.client.GetConversation(cs.ShelleyConversationID)
		if err == nil {
			msgs, toolMap, err := c.parsedCache.GetOrParse(cs.ShelleyConversationID, convData)
			if err == nil {
				status := AnalyzeWaitingForInput(msgs, toolMap)
				if status.Waiting {
					entries = append(entries, fuse.DirEntry{Name: "waiting_for_input", Mode: syscall.S_IFLNK})
				}
			}
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
	out.SetTimeout(cacheTTLConversation)
	return 0
}

// Create handles creating files in the conversation directory.
// Only "archived" can be created, which archives the conversation.
func (c *ConversationNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	defer diag.Track(c.diag, "ConversationNode", "Create", c.localID+"/"+name)()
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
	defer diag.Track(c.diag, "ConversationNode", "Unlink", c.localID+"/"+name)()
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
	diag        *diag.Tracker
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
	defer diag.Track(m.diag, "MessagesDirNode", "Lookup", m.localID+"/"+name)()
	switch name {
	case "last":
		ino := stableIno("query-dir", m.localID, "last")
		return m.NewInode(ctx, &QueryDirNode{localID: m.localID, client: m.client, state: m.state, kind: queryLast, startTime: m.startTime, parsedCache: m.parsedCache, diag: m.diag}, fs.StableAttr{Mode: fuse.S_IFDIR, Ino: ino}), 0
	case "since":
		ino := stableIno("query-dir", m.localID, "since")
		return m.NewInode(ctx, &QueryDirNode{localID: m.localID, client: m.client, state: m.state, kind: querySince, startTime: m.startTime, parsedCache: m.parsedCache, diag: m.diag}, fs.StableAttr{Mode: fuse.S_IFDIR, Ino: ino}), 0
	case "count":
		return m.NewInode(ctx, &MessageCountNode{localID: m.localID, client: m.client, state: m.state, startTime: m.startTime, parsedCache: m.parsedCache}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}

	// all.json, all.md
	format, ok := parseFormat(name)
	if ok {
		base := strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".md")
		if base == "all" {
			return m.NewInode(ctx, &ConvContentNode{
				localID: m.localID, client: m.client, state: m.state,
				query: contentQuery{kind: queryAll, format: format}, startTime: m.startTime,
				parsedCache: m.parsedCache, diag: m.diag,
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
		result, err := m.parsedCache.GetOrParseResult(cs.ShelleyConversationID, convData)
		if err != nil {
			return nil, syscall.EIO
		}

		// Find the message by sequence number
		msg := shelley.GetMessage(result.Messages, seqNum)
		if msg == nil {
			return nil, syscall.ENOENT
		}

		// Compute expected slug using the cached toolMap
		expectedSlug := shelley.MessageSlug(msg, result.ToolMap)
		expectedName := messageFileBase(seqNum, expectedSlug, result.MaxSeqID)

		// Verify the directory name matches the expected slug
		if name != expectedName {
			return nil, syscall.ENOENT
		}

		node := &MessageDirNode{
			message:   *msg,
			toolMap:   result.ToolMap,
			startTime: m.startTime,
		}
		// Message directories are immutable once created — cache aggressively.
		// Populate attrs in EntryOut so the kernel has valid data to cache.
		out.SetEntryTimeout(cacheTTLImmutable)
		out.SetAttrTimeout(cacheTTLImmutable)
		out.Attr.Mode = fuse.S_IFDIR | 0755
		node.messageTimestamps().ApplyWithFallback(&out.Attr, m.startTime)
		ino := stableIno("msg-dir", msg.ConversationID, strconv.Itoa(msg.SequenceID))
		return m.NewInode(ctx, node, fs.StableAttr{Mode: fuse.S_IFDIR, Ino: ino}), 0
	}

	return nil, syscall.ENOENT
}

func (m *MessagesDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	defer diag.Track(m.diag, "MessagesDirNode", "Readdir", m.localID)()
	entries := []fuse.DirEntry{
		{Name: "all.json", Mode: fuse.S_IFREG},
		{Name: "all.md", Mode: fuse.S_IFREG},
		{Name: "count", Mode: fuse.S_IFREG},
		{Name: "last", Mode: fuse.S_IFDIR},
		{Name: "since", Mode: fuse.S_IFDIR},
	}

	// List individual messages as directories (0-user/, 1-agent/, ...)
	cs := m.state.Get(m.localID)
	if cs != nil && cs.Created && cs.ShelleyConversationID != "" {
		convData, err := m.client.GetConversation(cs.ShelleyConversationID)
		if err == nil {
			// Use the parsed message cache for efficiency
			result, err := m.parsedCache.GetOrParseResult(cs.ShelleyConversationID, convData)
			if err == nil {
				for i := range result.Messages {
					slug := shelley.MessageSlug(&result.Messages[i], result.ToolMap)
					base := messageFileBase(result.Messages[i].SequenceID, slug, result.MaxSeqID)
					ino := stableIno("msg-dir", result.Messages[i].ConversationID, strconv.Itoa(result.Messages[i].SequenceID))
					entries = append(entries, fuse.DirEntry{Name: base, Mode: fuse.S_IFDIR, Ino: ino})
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
var _ = (fs.NodeOpendirHandler)((*MessageDirNode)(nil))

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

// setImmutableFieldAttrs populates the EntryOut with immutable cache timeouts
// and file attrs for a MessageFieldNode, so the kernel has valid data to cache.
func setImmutableFieldAttrs(out *fuse.EntryOut, value string, noNewline bool, t time.Time) {
	out.SetEntryTimeout(cacheTTLImmutable)
	out.SetAttrTimeout(cacheTTLImmutable)
	out.Attr.Mode = fuse.S_IFREG | 0444
	size := len(value)
	if !noNewline {
		size++
	}
	out.Attr.Size = uint64(size)
	setTimestamps(&out.Attr, t)
}

// setImmutableDirAttrs populates the EntryOut with immutable cache timeouts
// and directory attrs, so the kernel has valid data to cache.
func setImmutableDirAttrs(out *fuse.EntryOut, t time.Time) {
	out.SetEntryTimeout(cacheTTLImmutable)
	out.SetAttrTimeout(cacheTTLImmutable)
	out.Attr.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, t)
}

// msgFieldIno computes a stable inode number for a message field node.
// This allows the kernel to recognize the same logical file across lookups
// and reuse cached data, even after the inode is forgotten and re-discovered.
func msgFieldIno(conversationID string, sequenceID int, fieldName string) uint64 {
	return stableIno("msg-field", conversationID, strconv.Itoa(sequenceID), fieldName)
}

func (m *MessageDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	t := m.messageTime()
	convID := m.message.ConversationID
	seqID := m.message.SequenceID

	// Helper to create and return an immutable field node with cached attrs
	// and a stable inode number derived from (conversationID, sequenceID, fieldName).
	fieldNode := func(value string) (*fs.Inode, syscall.Errno) {
		setImmutableFieldAttrs(out, value, false, t)
		ino := msgFieldIno(convID, seqID, name)
		return m.NewInode(ctx, &MessageFieldNode{value: value, startTime: t}, fs.StableAttr{Mode: fuse.S_IFREG, Ino: ino}), 0
	}

	switch name {
	case "message_id":
		return fieldNode(m.message.MessageID)
	case "conversation_id":
		return fieldNode(m.message.ConversationID)
	case "sequence_id":
		return fieldNode(strconv.Itoa(m.message.SequenceID))
	case "type":
		return fieldNode(m.message.Type)
	case "created_at":
		return fieldNode(m.message.CreatedAt)
	case "llm_data":
		if m.message.LLMData == nil || *m.message.LLMData == "" {
			return nil, syscall.ENOENT
		}
		ino := msgFieldIno(convID, seqID, name)
		config := &jsonfs.Config{StartTime: t, CacheTimeout: cacheTTLImmutable}
		node, err := jsonfs.NewNodeFromJSON([]byte(*m.message.LLMData), config)
		if err != nil {
			// If JSON parsing fails, return as a file
			setImmutableFieldAttrs(out, *m.message.LLMData, false, t)
			return m.NewInode(ctx, &MessageFieldNode{value: *m.message.LLMData, startTime: t}, fs.StableAttr{Mode: fuse.S_IFREG, Ino: ino}), 0
		}
		setImmutableDirAttrs(out, t)
		return m.NewInode(ctx, node, fs.StableAttr{Mode: fuse.S_IFDIR, Ino: ino}), 0
	case "usage_data":
		if m.message.UsageData == nil || *m.message.UsageData == "" {
			return nil, syscall.ENOENT
		}
		ino := msgFieldIno(convID, seqID, name)
		config := &jsonfs.Config{StartTime: t, CacheTimeout: cacheTTLImmutable}
		node, err := jsonfs.NewNodeFromJSON([]byte(*m.message.UsageData), config)
		if err != nil {
			// If JSON parsing fails, return as a file
			setImmutableFieldAttrs(out, *m.message.UsageData, false, t)
			return m.NewInode(ctx, &MessageFieldNode{value: *m.message.UsageData, startTime: t}, fs.StableAttr{Mode: fuse.S_IFREG, Ino: ino}), 0
		}
		setImmutableDirAttrs(out, t)
		return m.NewInode(ctx, node, fs.StableAttr{Mode: fuse.S_IFDIR, Ino: ino}), 0
	case "content.md":
		// Generate markdown rendering of this single message
		content := string(shelley.FormatMarkdown([]shelley.Message{m.message}))
		setImmutableFieldAttrs(out, content, true, t)
		ino := msgFieldIno(convID, seqID, name)
		return m.NewInode(ctx, &MessageFieldNode{value: content, startTime: t, noNewline: true}, fs.StableAttr{Mode: fuse.S_IFREG, Ino: ino}), 0
	}
	return nil, syscall.ENOENT
}

func (m *MessageDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	convID := m.message.ConversationID
	seqID := m.message.SequenceID
	fieldIno := func(name string) uint64 {
		return msgFieldIno(convID, seqID, name)
	}

	entries := []fuse.DirEntry{
		{Name: "message_id", Mode: fuse.S_IFREG, Ino: fieldIno("message_id")},
		{Name: "conversation_id", Mode: fuse.S_IFREG, Ino: fieldIno("conversation_id")},
		{Name: "sequence_id", Mode: fuse.S_IFREG, Ino: fieldIno("sequence_id")},
		{Name: "type", Mode: fuse.S_IFREG, Ino: fieldIno("type")},
		{Name: "created_at", Mode: fuse.S_IFREG, Ino: fieldIno("created_at")},
		{Name: "content.md", Mode: fuse.S_IFREG, Ino: fieldIno("content.md")},
	}
	// Only include llm_data if present
	if m.message.LLMData != nil && *m.message.LLMData != "" {
		// Check if it's valid JSON object/array
		trimmed := strings.TrimSpace(*m.message.LLMData)
		if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
			(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			entries = append(entries, fuse.DirEntry{Name: "llm_data", Mode: fuse.S_IFDIR, Ino: fieldIno("llm_data")})
		} else {
			entries = append(entries, fuse.DirEntry{Name: "llm_data", Mode: fuse.S_IFREG, Ino: fieldIno("llm_data")})
		}
	}
	// Only include usage_data if present
	if m.message.UsageData != nil && *m.message.UsageData != "" {
		trimmed := strings.TrimSpace(*m.message.UsageData)
		if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
			(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			entries = append(entries, fuse.DirEntry{Name: "usage_data", Mode: fuse.S_IFDIR, Ino: fieldIno("usage_data")})
		} else {
			entries = append(entries, fuse.DirEntry{Name: "usage_data", Mode: fuse.S_IFREG, Ino: fieldIno("usage_data")})
		}
	}
	return fs.NewListDirStream(entries), 0
}

func (m *MessageDirNode) OpendirHandle(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_CACHE_DIR, 0
}

func (m *MessageDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	m.messageTimestamps().ApplyWithFallback(&out.Attr, m.startTime)
	out.SetTimeout(cacheTTLImmutable)
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
	return nil, fuse.FOPEN_KEEP_CACHE, 0
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
	out.SetTimeout(cacheTTLImmutable)
	return 0
}

// --- MessageCountNode: /conversation/{id}/messages/count ---

type MessageCountNode struct {
	fs.Inode
	localID     string
	client      shelley.ShelleyClient
	state       *state.Store
	startTime   time.Time
	parsedCache *ParsedMessageCache
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
			msgs, _, err := m.parsedCache.GetOrParse(cs.ShelleyConversationID, convData)
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
	client    shelley.ShelleyClient
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
		if k == "model" {
			// Resolve model name to display name + internal ID.
			// Users write display names (e.g. "kimi-2.5-fireworks");
			// we store both the display name and internal ID.
			result, err := c.client.ListModels()
			if err != nil {
				log.Printf("CtlNode.Write: ListModels failed: %v", err)
				return 0, syscall.EIO
			}
			model := result.FindByName(v)
			if model == nil {
				return 0, syscall.EINVAL
			}
			if err := c.state.SetModel(c.localID, model.Name(), model.ID); err != nil {
				return 0, syscall.EINVAL
			}
		} else {
			if err := c.state.SetCtl(c.localID, k, v); err != nil {
				return 0, syscall.EINVAL
			}
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

// --- ConvSendNode: write message, creates conversation if needed ---

type ConvSendNode struct {
	fs.Inode
	localID     string
	client      shelley.ShelleyClient
	state       *state.Store
	startTime   time.Time // fallback if conversation has no CreatedAt
	parsedCache *ParsedMessageCache
	diag        *diag.Tracker
}

var _ = (fs.NodeOpener)((*ConvSendNode)(nil))
var _ = (fs.NodeGetattrer)((*ConvSendNode)(nil))
var _ = (fs.NodeSetattrer)((*ConvSendNode)(nil))

func (n *ConvSendNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return &ConvSendFileHandle{
		node: n,
	}, fuse.FOPEN_DIRECT_IO, 0
}

// ConvSendFileHandle buffers writes and sends the message on Flush (close)
type ConvSendFileHandle struct {
	node    *ConvSendNode
	buffer  []byte
	flushed bool
	mu      sync.Mutex
}

var _ = (fs.FileWriter)((*ConvSendFileHandle)(nil))
var _ = (fs.FileFlusher)((*ConvSendFileHandle)(nil))

func (h *ConvSendFileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Append to buffer - message will be sent on Flush
	h.buffer = append(h.buffer, data...)
	return uint32(len(data)), 0
}

// Flush is called synchronously during close(2), so the caller will block until
// the message is sent. This ensures the conversation is created before close returns.
// Note: Flush may be called multiple times for dup'd file descriptors.
func (h *ConvSendFileHandle) Flush(ctx context.Context) syscall.Errno {
	defer diag.Track(h.node.diag, "ConvSendFileHandle", "Flush", h.node.localID)()
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
		result, err := h.node.client.StartConversation(message, cs.EffectiveModelID(), cs.Cwd)
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
		// Pass the internal model ID to ensure we use the correct API identifier
		if err := h.node.client.SendMessage(cs.ShelleyConversationID, message, cs.EffectiveModelID()); err != nil {
			log.Printf("SendMessage failed for conversation %s: %v", cs.ShelleyConversationID, err)
			return syscall.EIO
		}
		// Invalidate the parsed message cache since the conversation was modified
		h.node.parsedCache.Invalidate(cs.ShelleyConversationID)
	}

	return 0
}

func (n *ConvSendNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
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

func (n *ConvSendNode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
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

// --- WaitingForInputStatus: analyzes conversation to determine if waiting for user input ---

// WaitingForInputStatus represents the result of analyzing whether a conversation
// is waiting for user input.
type WaitingForInputStatus struct {
	// Waiting is true if the conversation is waiting for user input
	Waiting bool
	// LastAgentIndex is the 0-based index of the last agent message in the messages slice.
	// Only valid if Waiting is true.
	LastAgentIndex int
	// LastAgentSeqID is the sequence ID of the last agent message.
	// Only valid if Waiting is true.
	LastAgentSeqID int
	// LastAgentSlug is the slug of the last agent message (e.g., "agent" or "bash-tool").
	// Only valid if Waiting is true.
	LastAgentSlug string
}

// AnalyzeWaitingForInput determines if a conversation is waiting for user input.
//
// A conversation is waiting for input when:
// - The last content-bearing message (excluding gitinfo) is from agent
// - All tool calls have matching tool results (no pending tool calls)
// - gitinfo messages may follow (ignored for status purposes)
// - No user messages follow the agent
//
// The function returns the status including the index of the last agent message
// for constructing the symlink target.
func AnalyzeWaitingForInput(messages []shelley.Message, toolMap map[string]string) WaitingForInputStatus {
	if len(messages) == 0 {
		return WaitingForInputStatus{Waiting: false}
	}

	// Track pending tool calls (tool calls without matching results)
	pendingToolCalls := make(map[string]bool)

	// Track the last agent message index and slug
	lastAgentIdx := -1
	lastAgentSeqID := 0
	lastAgentSlug := ""

	for i, msg := range messages {
		slug := shelley.MessageSlug(&msg, toolMap)

		// Skip gitinfo messages for status purposes
		if isGitInfoMessage(&msg, slug) {
			continue
		}

		// Check if this is an agent message
		if isAgentMessage(&msg, slug) {
			lastAgentIdx = i
			lastAgentSeqID = msg.SequenceID
			lastAgentSlug = slug

			// Check for tool calls in this agent message
			toolUseIDs := extractToolUseIDs(&msg)
			for _, id := range toolUseIDs {
				pendingToolCalls[id] = true
			}
			continue
		}

		// Check if this is a tool result message
		if isToolResultMessage(&msg, slug) {
			// Mark the corresponding tool call as completed
			toolResultIDs := extractToolResultIDs(&msg)
			for _, id := range toolResultIDs {
				delete(pendingToolCalls, id)
			}
			continue
		}

		// This is a user message (not agent, not tool result, not gitinfo)
		// If there's a user message after the last agent, we're not waiting for input
		// (We'll check this at the end by comparing indices)
	}

	// No agent messages found
	if lastAgentIdx == -1 {
		return WaitingForInputStatus{Waiting: false}
	}

	// Check if there are any pending tool calls
	if len(pendingToolCalls) > 0 {
		return WaitingForInputStatus{Waiting: false}
	}

	// Check if there's a user message after the last agent message
	// (ignoring gitinfo and tool results that complete pending calls)
	for i := lastAgentIdx + 1; i < len(messages); i++ {
		msg := &messages[i]
		slug := shelley.MessageSlug(msg, toolMap)

		// Skip gitinfo messages
		if isGitInfoMessage(msg, slug) {
			continue
		}

		// Tool results are OK (they complete previous tool calls)
		if isToolResultMessage(msg, slug) {
			continue
		}

		// Any other message type after agent means not waiting for input
		// This includes user messages and unexpected message types
		return WaitingForInputStatus{Waiting: false}
	}

	// All conditions met: waiting for input
	return WaitingForInputStatus{
		Waiting:        true,
		LastAgentIndex: lastAgentIdx,
		LastAgentSeqID: lastAgentSeqID,
		LastAgentSlug:  lastAgentSlug,
	}
}

// isAgentMessage returns true if the message is from the agent (LLM).
// Note: Agent messages have Type="shelley". The slug varies based on content:
// - "agent" for text-only responses
// - "{tool}-tool" for messages containing tool calls (e.g., "bash-tool")
func isAgentMessage(msg *shelley.Message, slug string) bool {
	return strings.ToLower(msg.Type) == "shelley"
}

// isGitInfoMessage returns true if the message is a gitinfo message.
// gitinfo messages are ignored for determining conversation status.
func isGitInfoMessage(msg *shelley.Message, slug string) bool {
	// gitinfo messages typically have Type="gitinfo" or similar
	lowerType := strings.ToLower(msg.Type)
	return lowerType == "gitinfo" || lowerType == "git_info" || lowerType == "git-info"
}

// isToolResultMessage returns true if the message contains tool results.
func isToolResultMessage(msg *shelley.Message, slug string) bool {
	// Tool result messages have slugs ending in "-result"
	return strings.HasSuffix(slug, "-result")
}

// extractToolUseIDs extracts all tool use IDs from an agent message.
func extractToolUseIDs(msg *shelley.Message) []string {
	var ids []string

	// Parse content from LLMData
	var data string
	if msg.LLMData != nil {
		data = *msg.LLMData
	}
	if data == "" {
		return ids
	}

	var content shelley.MessageContent
	if err := json.Unmarshal([]byte(data), &content); err != nil {
		return ids
	}

	for _, item := range content.Content {
		if item.Type == shelley.ContentTypeToolUse {
			// Tool use ID can be in either ID or ToolUseID field
			if item.ID != "" {
				ids = append(ids, item.ID)
			}
		}
	}

	return ids
}

// extractToolResultIDs extracts all tool use IDs referenced by tool results in a message.
func extractToolResultIDs(msg *shelley.Message) []string {
	var ids []string

	// Parse content from UserData (tool results are typically in user messages)
	var data string
	if msg.UserData != nil {
		data = *msg.UserData
	}
	if data == "" {
		return ids
	}

	var content shelley.MessageContent
	if err := json.Unmarshal([]byte(data), &content); err != nil {
		return ids
	}

	for _, item := range content.Content {
		if item.Type == shelley.ContentTypeToolResult && item.ToolUseID != "" {
			ids = append(ids, item.ToolUseID)
		}
	}

	return ids
}

// stableIno computes a deterministic inode number from the given key parts.
// This allows go-fuse to reuse existing inodes across repeated Lookup calls
// for the same path, preserving any cached state on the node.
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
