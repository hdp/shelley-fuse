// Package jsonfs provides a FUSE filesystem abstraction for exposing JSON data as directories and files.
//
// JSON objects become directories with their keys as entries.
// JSON arrays become directories with numeric indices (0, 1, 2, ...) as entries.
// JSON primitives (string, number, boolean, null) become files containing the value.
//
// Stringified JSON fields (values that are strings containing valid JSON) can be
// automatically unpacked into nested directory structures.
package jsonfs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Config specifies how JSON data should be exposed as a filesystem.
type Config struct {
	// StringifyFields lists field names whose string values should be parsed as JSON
	// and recursively unpacked into directory structures. If empty, no fields are unpacked.
	StringifyFields []string

	// StartTime is used for file/directory timestamps. If zero, time.Now() is used.
	StartTime time.Time

	// CacheTimeout sets the kernel cache timeout for entry/attr lookups.
	// If zero, no per-node timeouts are set (server defaults apply).
	// For immutable JSON data (e.g. message llm_data/usage_data), use a long
	// timeout since the data never changes.
	CacheTimeout time.Duration
}

func (c *Config) startTime() time.Time {
	if c == nil || c.StartTime.IsZero() {
		return time.Now()
	}
	return c.StartTime
}

func (c *Config) cacheTimeout() time.Duration {
	if c == nil {
		return 0
	}
	return c.CacheTimeout
}

func (c *Config) shouldUnpack(fieldName string) bool {
	if c == nil {
		return false
	}
	for _, f := range c.StringifyFields {
		if f == fieldName {
			return true
		}
	}
	return false
}

// NewNode creates a filesystem node from a Go value (typically unmarshaled from JSON).
// The value should be one of: map[string]any, []any, string, float64, bool, or nil.
// Returns an fs.InodeEmbedder that can be used with go-fuse.
func NewNode(value any, config *Config) fs.InodeEmbedder {
	return newNode(value, "", config)
}

// NewNodeFromJSON creates a filesystem node from raw JSON bytes.
// Returns an error if the JSON is invalid.
func NewNodeFromJSON(data []byte, config *Config) (fs.InodeEmbedder, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return NewNode(value, config), nil
}

// newNode creates the appropriate node type based on the value type.
// fieldName is used to check if stringified JSON should be unpacked.
func newNode(value any, fieldName string, config *Config) fs.InodeEmbedder {
	switch v := value.(type) {
	case map[string]any:
		return &objectNode{data: v, config: config}
	case []any:
		return &arrayNode{data: v, config: config}
	case string:
		// Check if this is a stringified JSON field that should be unpacked
		if config.shouldUnpack(fieldName) {
			trimmed := strings.TrimSpace(v)
			if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
				(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
				var parsed any
				if err := json.Unmarshal([]byte(v), &parsed); err == nil {
					return newNode(parsed, "", config)
				}
			}
		}
		return &valueNode{content: v, config: config}
	case float64:
		// Format numbers nicely: integers without decimal, floats with minimal precision
		var str string
		if v == float64(int64(v)) {
			str = strconv.FormatInt(int64(v), 10)
		} else {
			str = strconv.FormatFloat(v, 'g', -1, 64)
		}
		return &valueNode{content: str, config: config}
	case bool:
		return &valueNode{content: strconv.FormatBool(v), config: config}
	case nil:
		return &valueNode{content: "null", config: config}
	default:
		// Fallback: convert to JSON string
		data, _ := json.Marshal(v)
		return &valueNode{content: string(data), config: config}
	}
}

// setEntryCache sets entry and attr cache timeouts on an EntryOut if configured.
// When setting AttrTimeout, it also populates the Attr fields so the kernel
// has valid data to cache (otherwise it would cache zero-valued attrs).
func setEntryCache(out *fuse.EntryOut, child fs.InodeEmbedder, config *Config) {
	timeout := config.cacheTimeout()
	if timeout <= 0 {
		return
	}
	out.SetEntryTimeout(timeout)
	out.SetAttrTimeout(timeout)
	t := config.startTime()
	switch c := child.(type) {
	case *objectNode, *arrayNode:
		out.Attr.Mode = fuse.S_IFDIR | 0755
		setTimestamps(&out.Attr, t)
	case *valueNode:
		out.Attr.Mode = fuse.S_IFREG | 0444
		out.Attr.Size = uint64(len(c.content) + 1)
		setTimestamps(&out.Attr, t)
	default:
		// Unknown node type; set entry timeout only, skip attr
		_ = child
	}
}

// setAttrCache sets the attr cache timeout on an AttrOut if configured.
func setAttrCache(out *fuse.AttrOut, timeout time.Duration) {
	if timeout > 0 {
		out.SetTimeout(timeout)
	}
}

// setTimestamps sets atime, mtime, and ctime on the attribute.
func setTimestamps(attr *fuse.Attr, t time.Time) {
	attr.Atime = uint64(t.Unix())
	attr.Atimensec = uint32(t.Nanosecond())
	attr.Mtime = uint64(t.Unix())
	attr.Mtimensec = uint32(t.Nanosecond())
	attr.Ctime = uint64(t.Unix())
	attr.Ctimensec = uint32(t.Nanosecond())
}

// --- objectNode: JSON object as directory ---

type objectNode struct {
	fs.Inode
	data   map[string]any
	config *Config
}

var _ = (fs.NodeLookuper)((*objectNode)(nil))
var _ = (fs.NodeReaddirer)((*objectNode)(nil))
var _ = (fs.NodeGetattrer)((*objectNode)(nil))
var _ = (fs.NodeOpendirHandler)((*objectNode)(nil))

func (n *objectNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	value, ok := n.data[name]
	if !ok {
		return nil, syscall.ENOENT
	}

	child := newNode(value, name, n.config)
	setEntryCache(out, child, n.config)
	mode := nodeMode(child)
	return n.NewInode(ctx, child, fs.StableAttr{Mode: mode}), 0
}

func (n *objectNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// Sort keys for consistent ordering
	keys := make([]string, 0, len(n.data))
	for k := range n.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	entries := make([]fuse.DirEntry, 0, len(keys))
	for _, k := range keys {
		child := newNode(n.data[k], k, n.config)
		entries = append(entries, fuse.DirEntry{
			Name: k,
			Mode: nodeMode(child),
		})
	}
	return fs.NewListDirStream(entries), 0
}

func (n *objectNode) OpendirHandle(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if n.config.cacheTimeout() > 0 {
		return nil, fuse.FOPEN_CACHE_DIR, 0
	}
	return nil, 0, 0
}

func (n *objectNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, n.config.startTime())
	setAttrCache(out, n.config.cacheTimeout())
	return 0
}

// --- arrayNode: JSON array as directory with numeric indices ---

type arrayNode struct {
	fs.Inode
	data   []any
	config *Config
}

var _ = (fs.NodeLookuper)((*arrayNode)(nil))
var _ = (fs.NodeReaddirer)((*arrayNode)(nil))
var _ = (fs.NodeGetattrer)((*arrayNode)(nil))
var _ = (fs.NodeOpendirHandler)((*arrayNode)(nil))

func (n *arrayNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	idx, err := strconv.Atoi(name)
	if err != nil || idx < 0 || idx >= len(n.data) {
		return nil, syscall.ENOENT
	}

	child := newNode(n.data[idx], "", n.config)
	setEntryCache(out, child, n.config)
	mode := nodeMode(child)
	return n.NewInode(ctx, child, fs.StableAttr{Mode: mode}), 0
}

func (n *arrayNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries := make([]fuse.DirEntry, 0, len(n.data))
	for i, v := range n.data {
		child := newNode(v, "", n.config)
		entries = append(entries, fuse.DirEntry{
			Name: strconv.Itoa(i),
			Mode: nodeMode(child),
		})
	}
	return fs.NewListDirStream(entries), 0
}

func (n *arrayNode) OpendirHandle(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if n.config.cacheTimeout() > 0 {
		return nil, fuse.FOPEN_CACHE_DIR, 0
	}
	return nil, 0, 0
}

func (n *arrayNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, n.config.startTime())
	setAttrCache(out, n.config.cacheTimeout())
	return 0
}

// --- valueNode: JSON primitive as file ---

type valueNode struct {
	fs.Inode
	content string
	config  *Config
}

var _ = (fs.NodeOpener)((*valueNode)(nil))
var _ = (fs.NodeReader)((*valueNode)(nil))
var _ = (fs.NodeGetattrer)((*valueNode)(nil))

func (n *valueNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if n.config.cacheTimeout() > 0 {
		return nil, fuse.FOPEN_KEEP_CACHE, 0
	}
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (n *valueNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data := []byte(n.content + "\n")
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (n *valueNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = uint64(len(n.content) + 1) // +1 for newline
	setTimestamps(&out.Attr, n.config.startTime())
	setAttrCache(out, n.config.cacheTimeout())
	return 0
}

// --- helpers ---

// nodeMode returns the FUSE mode for a node (directory or regular file).
func nodeMode(node fs.InodeEmbedder) uint32 {
	switch node.(type) {
	case *objectNode, *arrayNode:
		return fuse.S_IFDIR
	default:
		return fuse.S_IFREG
	}
}

// readAt returns the portion of data that fits in dest starting at offset off.
func readAt(data, dest []byte, off int64) []byte {
	if off >= int64(len(data)) {
		return nil
	}
	n := copy(dest, data[off:])
	return dest[:n]
}
