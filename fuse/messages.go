package fuse

import (
	"context"
	"strconv"
	"strings"
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
	defer diag.Track(m.diag, "MessagesDirNode", "Lookup", m.localID+"/"+name).Done()
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
	defer diag.Track(m.diag, "MessagesDirNode", "Readdir", m.localID).Done()
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
var _ = (fs.NodeGetattrer)((*MessageCountNode)(nil))

// messageCountData computes the count string for this conversation.
func (m *MessageCountNode) messageCountData() []byte {
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
	return []byte(value + "\n")
}

func (m *MessageCountNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	// Compute content at open time so the file handle reports accurate size.
	data := m.messageCountData()
	cs := m.state.Get(m.localID)
	var ts time.Time
	if cs != nil && !cs.CreatedAt.IsZero() {
		ts = cs.CreatedAt
	} else {
		ts = m.startTime
	}
	return &messageCountFileHandle{content: data, ts: ts}, fuse.FOPEN_DIRECT_IO, 0
}

func (m *MessageCountNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	// If called on an open file handle, delegate to it for accurate size.
	if fga, ok := f.(fs.FileGetattrer); ok {
		return fga.Getattr(ctx, out)
	}
	out.Mode = fuse.S_IFREG | 0444
	// Without an open handle we don't know the exact size; report 0.
	// DIRECT_IO ensures the kernel still issues a read.
	cs := m.state.Get(m.localID)
	if cs != nil && !cs.CreatedAt.IsZero() {
		setTimestamps(&out.Attr, cs.CreatedAt)
	} else {
		setTimestamps(&out.Attr, m.startTime)
	}
	return 0
}

// messageCountFileHandle caches the count content computed at Open time.
type messageCountFileHandle struct {
	content []byte
	ts      time.Time
}

var _ = (fs.FileReader)((*messageCountFileHandle)(nil))
var _ = (fs.FileGetattrer)((*messageCountFileHandle)(nil))

func (h *messageCountFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	return fuse.ReadResultData(readAt(h.content, dest, off)), 0
}

func (h *messageCountFileHandle) Getattr(ctx context.Context, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = uint64(len(h.content))
	setTimestamps(&out.Attr, h.ts)
	return 0
}

