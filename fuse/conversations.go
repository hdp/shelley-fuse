package fuse

import (
	"context"
	"encoding/json"
	"log"
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
var _ = (fs.NodeRmdirer)((*ConversationListNode)(nil))

func (c *ConversationListNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(c.diag, "ConversationListNode", "Lookup", name).Done()
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

// derefStr safely dereferences a *string, returning "" for nil.
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// lookupInConversationList searches for a conversation by ID or slug in the given list.
// If found, it adopts the conversation locally and returns a symlink to the local ID.
func (c *ConversationListNode) lookupInConversationList(ctx context.Context, name string, convs []shelley.Conversation) (*fs.Inode, syscall.Errno) {
	for _, conv := range convs {
		if conv.ConversationID == name {
			// Adopt this server conversation locally with API metadata
			slug := derefStr(conv.Slug)
			localID, err := c.state.AdoptWithMetadata(name, slug, conv.CreatedAt, conv.UpdatedAt, derefStr(conv.Model), derefStr(conv.Cwd))
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
			localID, err := c.state.AdoptWithMetadata(conv.ConversationID, *conv.Slug, conv.CreatedAt, conv.UpdatedAt, derefStr(conv.Model), derefStr(conv.Cwd))
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
	defer diag.Track(c.diag, "ConversationListNode", "Readdir", "").Done()
	// Adopt any server conversations that aren't tracked locally, and update
	// slugs for already-tracked conversations (slugs are always provided immediately).
	serverConvs, err := c.fetchServerConversations()

	// Build a set of valid server conversation IDs for filtering stale entries
	validServerIDs := make(map[string]bool)
	serverFetchSucceeded := err == nil

	if serverFetchSucceeded {
		for _, conv := range serverConvs {
			validServerIDs[conv.ConversationID] = true
			// AdoptWithMetadata handles the case where a conversation is not yet tracked locally
			// and also updates API timestamps. Errors are non-fatal; worst case the conversation
			// won't appear in this listing but will be adopted on next Lookup
			_, _ = c.state.AdoptWithMetadata(conv.ConversationID, derefStr(conv.Slug), conv.CreatedAt, conv.UpdatedAt, derefStr(conv.Model), derefStr(conv.Cwd))
		}
	}

	// Also fetch archived conversations to prevent them from being filtered
	// as stale. Archived conversations are valid — they just live on a
	// different server endpoint (/api/conversations/archived).
	// However, we track them separately so they can be excluded from the
	// directory listing while remaining accessible via direct Lookup.
	archivedServerIDs := make(map[string]bool)
	archivedConvs, archivedErr := c.fetchArchivedConversations()
	if archivedErr == nil {
		for _, conv := range archivedConvs {
			validServerIDs[conv.ConversationID] = true
			archivedServerIDs[conv.ConversationID] = true
			_, _ = c.state.AdoptWithMetadata(conv.ConversationID, derefStr(conv.Slug), conv.CreatedAt, conv.UpdatedAt, derefStr(conv.Model), derefStr(conv.Cwd))
		}
	}

	// Note: if fetchServerConversations fails, we still return local entries.
	// This is intentional - local state should always be accessible.
	// If fetchArchivedConversations fails, archived conversations may be
	// filtered as stale, but they remain accessible via direct Lookup.

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
		} else if validServerIDs[cs.ShelleyConversationID] && !archivedServerIDs[cs.ShelleyConversationID] {
			// Has server ID, still exists on server, and is not archived
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

// Rmdir handles `rmdir conversation/{id}` to permanently delete a conversation.
// Only works on local IDs (not server IDs or slugs, which are symlinks).
func (c *ConversationListNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	defer diag.Track(c.diag, "ConversationListNode", "Rmdir", name).Done()

	cs := c.state.Get(name)
	if cs == nil {
		return syscall.ENOENT
	}

	if !cs.Created || cs.ShelleyConversationID == "" {
		// Not yet created on the backend — just clean up local state
		_ = c.state.ForceDelete(name)
		return 0
	}

	// Delete from the server
	if err := c.client.DeleteConversation(cs.ShelleyConversationID); err != nil {
		log.Printf("DeleteConversation failed for %s (%s): %v", name, cs.ShelleyConversationID, err)
		return syscall.EIO
	}

	// Invalidate the parsed message cache
	c.parsedCache.Invalidate(cs.ShelleyConversationID)

	// Remove from local state
	if err := c.state.ForceDelete(name); err != nil {
		log.Printf("ForceDelete failed for %s: %v", name, err)
		// Server delete succeeded, so don't return error — state will be cleaned up on next Readdir
	}

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
	defer diag.Track(c.diag, "ConversationNode", "Lookup", c.localID+"/"+name).Done()
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
	case "continue":
		cs := c.state.Get(c.localID)
		if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
			out.SetEntryTimeout(negTimeout)
			return nil, syscall.ENOENT
		}
		return c.NewInode(ctx, &ContinueNode{
			localID:   c.localID,
			client:    c.client,
			state:     c.state,
			startTime: c.startTime,
			diag:      c.diag,
		}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "subagents":
		cs := c.state.Get(c.localID)
		if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
			out.SetEntryTimeout(negTimeout)
			return nil, syscall.ENOENT
		}
		return c.NewInode(ctx, &SubagentsDirNode{
			localID:   c.localID,
			client:    c.client,
			state:     c.state,
			startTime: c.startTime,
			diag:      c.diag,
		}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "working":
		// Presence/absence semantics: file exists only when agent is working.
		// Can appear and disappear rapidly → short timeouts both ways.
		cs := c.state.Get(c.localID)
		if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
			out.SetEntryTimeout(volatileEntryTimeout)
			return nil, syscall.ENOENT
		}

		working, err := c.client.IsConversationWorking(cs.ShelleyConversationID)
		if err != nil || !working {
			out.SetEntryTimeout(volatileEntryTimeout)
			return nil, syscall.ENOENT
		}
		out.SetEntryTimeout(volatileEntryTimeout)
		return c.NewInode(ctx, &WorkingNode{startTime: c.getConversationTime()}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
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
	defer diag.Track(c.diag, "ConversationNode", "Readdir", c.localID).Done()
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

	// Include working file only when the agent is currently working
	if cs != nil && cs.Created && cs.ShelleyConversationID != "" {
		working, err := c.client.IsConversationWorking(cs.ShelleyConversationID)
		if err == nil && working {
			entries = append(entries, fuse.DirEntry{Name: "working", Mode: fuse.S_IFREG})
		}
	}

	// Include subagents directory and continue file for created conversations
	if cs != nil && cs.Created && cs.ShelleyConversationID != "" {
		entries = append(entries, fuse.DirEntry{Name: "continue", Mode: fuse.S_IFREG})
		entries = append(entries, fuse.DirEntry{Name: "subagents", Mode: fuse.S_IFDIR})
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
	defer diag.Track(c.diag, "ConversationNode", "Create", c.localID+"/"+name).Done()
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
	defer diag.Track(c.diag, "ConversationNode", "Unlink", c.localID+"/"+name).Done()
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
	op := diag.Track(h.node.diag, "ConvSendFileHandle", "Flush", h.node.localID)
	defer op.Done()
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
		op.SetPhase("HTTP POST StartConversation")
		result, err := h.node.client.StartConversation(message, cs.EffectiveModelID(), cs.Cwd)
		if err != nil {
			log.Printf("StartConversation failed for %s: %v", h.node.localID, err)
			return syscall.EIO
		}
		op.SetPhase("MarkCreated")
		if err := h.node.state.MarkCreated(h.node.localID, result.ConversationID, result.Slug); err != nil {
			return syscall.EIO
		}
		// Invalidate the parsed message cache since the conversation was just created
		h.node.parsedCache.Invalidate(result.ConversationID)
	} else {
		// Subsequent writes: send message to existing conversation
		// Pass the internal model ID to ensure we use the correct API identifier
		op.SetPhase("HTTP POST SendMessage")
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

// --- WorkingNode: empty presence file indicating agent is working ---

type WorkingNode struct {
	fs.Inode
	startTime time.Time
}

var _ = (fs.NodeOpener)((*WorkingNode)(nil))
var _ = (fs.NodeReader)((*WorkingNode)(nil))
var _ = (fs.NodeGetattrer)((*WorkingNode)(nil))

func (w *WorkingNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (w *WorkingNode) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	return fuse.ReadResultData(nil), 0
}

func (w *WorkingNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = 0
	setTimestamps(&out.Attr, w.startTime)
	return 0
}

// --- SubagentsDirNode: /conversation/{id}/subagents/ directory ---
// Lists child conversations (subagents) as symlinks pointing to ../../{localID}.

type SubagentsDirNode struct {
	fs.Inode
	localID   string
	client    shelley.ShelleyClient
	state     *state.Store
	startTime time.Time
	diag      *diag.Tracker
}

var _ = (fs.NodeLookuper)((*SubagentsDirNode)(nil))
var _ = (fs.NodeReaddirer)((*SubagentsDirNode)(nil))
var _ = (fs.NodeGetattrer)((*SubagentsDirNode)(nil))

// fetchSubagents retrieves and adopts subagent conversations for this conversation.
func (n *SubagentsDirNode) fetchSubagents() ([]shelley.Conversation, error) {
	cs := n.state.Get(n.localID)
	if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
		return nil, nil
	}

	data, err := n.client.ListSubagents(cs.ShelleyConversationID)
	if err != nil {
		return nil, err
	}

	var convs []shelley.Conversation
	if err := json.Unmarshal(data, &convs); err != nil {
		return nil, err
	}

	// Adopt each subagent into local state
	for _, conv := range convs {
		_, _ = n.state.AdoptWithMetadata(
			conv.ConversationID,
			derefStr(conv.Slug),
			conv.CreatedAt,
			conv.UpdatedAt,
			derefStr(conv.Model),
			derefStr(conv.Cwd),
		)
	}

	return convs, nil
}

func (n *SubagentsDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(n.diag, "SubagentsDirNode", "Lookup", n.localID+"/subagents/"+name).Done()
	setEntryTimeout(out, cacheTTLConversation)

	convs, err := n.fetchSubagents()
	if err != nil {
		return nil, syscall.EIO
	}

	for _, conv := range convs {
		localID := n.state.GetByShelleyID(conv.ConversationID)
		if localID == "" {
			continue
		}

		if name == localID || name == conv.ConversationID || (conv.Slug != nil && name == *conv.Slug) {
			target := "../../" + localID
			return n.NewInode(ctx, &SymlinkNode{target: target, startTime: n.startTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
		}
	}

	return nil, syscall.ENOENT
}

func (n *SubagentsDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	defer diag.Track(n.diag, "SubagentsDirNode", "Readdir", n.localID+"/subagents").Done()

	convs, err := n.fetchSubagents()
	if err != nil {
		return fs.NewListDirStream(nil), 0
	}

	usedNames := make(map[string]bool)
	var entries []fuse.DirEntry

	for _, conv := range convs {
		localID := n.state.GetByShelleyID(conv.ConversationID)
		if localID == "" {
			continue
		}

		// Add symlink for local ID
		if !usedNames[localID] {
			entries = append(entries, fuse.DirEntry{Name: localID, Mode: syscall.S_IFLNK})
			usedNames[localID] = true
		}

		// Add symlink for server ID if it doesn't conflict
		if !usedNames[conv.ConversationID] {
			entries = append(entries, fuse.DirEntry{Name: conv.ConversationID, Mode: syscall.S_IFLNK})
			usedNames[conv.ConversationID] = true
		}

		// Add symlink for slug if valid and doesn't conflict
		if conv.Slug != nil && *conv.Slug != "" && isValidFilename(*conv.Slug) && !usedNames[*conv.Slug] {
			entries = append(entries, fuse.DirEntry{Name: *conv.Slug, Mode: syscall.S_IFLNK})
			usedNames[*conv.Slug] = true
		}
	}

	return fs.NewListDirStream(entries), 0
}

func (n *SubagentsDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, n.startTime)
	out.SetTimeout(cacheTTLConversation)
	return 0
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

// --- ContinueNode: /conversation/{id}/continue — creates a new conversation from an existing one ---
// Reading this file calls POST /api/conversations/continue on the Shelley server,
// creates a new local conversation entry, and returns the new local ID.

type ContinueNode struct {
	fs.Inode
	localID   string
	client    shelley.ShelleyClient
	state     *state.Store
	startTime time.Time
	diag      *diag.Tracker
}

var _ = (fs.NodeOpener)((*ContinueNode)(nil))
var _ = (fs.NodeGetattrer)((*ContinueNode)(nil))

func (c *ContinueNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	defer diag.Track(c.diag, "ContinueNode", "Open", c.localID).Done()
	cs := c.state.Get(c.localID)
	if cs == nil || !cs.Created || cs.ShelleyConversationID == "" {
		return nil, 0, syscall.ENOENT
	}

	result, err := c.client.ContinueConversation(cs.ShelleyConversationID, "", "")
	if err != nil {
		log.Printf("ContinueConversation failed for %s: %v", c.localID, err)
		return nil, 0, syscall.EIO
	}

	// Adopt the new conversation into local state
	newLocalID, err := c.state.AdoptWithMetadata(result.ConversationID, "", "", "", "", "")
	if err != nil {
		log.Printf("AdoptWithMetadata failed for continued conversation %s: %v", result.ConversationID, err)
		return nil, 0, syscall.EIO
	}

	return &CloneFileHandle{id: newLocalID, diag: c.diag}, fuse.FOPEN_DIRECT_IO, 0
}

func (c *ContinueNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	cs := c.state.Get(c.localID)
	if cs != nil && !cs.CreatedAt.IsZero() {
		setTimestamps(&out.Attr, cs.CreatedAt)
	} else {
		setTimestamps(&out.Attr, c.startTime)
	}
	return 0
}

