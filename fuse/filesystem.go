package fuse

import (
	"context"
	"strings"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"shelley-fuse/shelley"
)

// FS represents the Shelley FUSE filesystem
type FS struct {
	fs.Inode
	defaultClient *shelley.Client
	clients       map[string]*shelley.Client // host:port -> client
}

// NewFS creates a new Shelley FUSE filesystem
func NewFS(defaultClient *shelley.Client) *FS {
	return &FS{
		defaultClient: defaultClient,
		clients:       make(map[string]*shelley.Client),
	}
}

// Root returns the root node of the filesystem
func (f *FS) Root() *fs.Inode {
	attr := fs.StableAttr{Mode: fuse.S_IFDIR, Ino: 0}
	return f.NewInode(context.Background(), &RootNode{fs: f}, attr)
}

// RootNode represents the root directory of the filesystem
type RootNode struct {
	fs.Inode
	fs *FS
}

// Lookup handles directory lookups
func (r *RootNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// Handle special names: "default" and host:port formats
	if name == "default" {
		if r.fs.defaultClient == nil {
			return nil, syscall.EINVAL
		}
		out.Mode = fuse.S_IFDIR | 0755
	attr := fs.StableAttr{Mode: fuse.S_IFDIR, Ino: 3}
	return r.NewInode(ctx, &HostNode{client: r.fs.defaultClient}, attr), 0
	}
	
	// Check if name is in host:port format
	if strings.Contains(name, ":") {
		// Create client for this host if it doesn't exist
		client, exists := r.fs.clients[name]
		if !exists {
			client = shelley.NewClient("http://" + name)
			r.fs.clients[name] = client
		}
			out.Mode = fuse.S_IFDIR | 0755
			attr := fs.StableAttr{Mode: fuse.S_IFDIR, Ino: 2}
			return r.NewInode(ctx, &HostNode{client: client}, attr), 0
	}
	
	// Fall back to default client for other names
	if r.fs.defaultClient == nil {
		return nil, syscall.EINVAL
	}
	out.Mode = fuse.S_IFDIR | 0755
	attr := fs.StableAttr{Mode: fuse.S_IFDIR, Ino: 1}
	return r.NewInode(ctx, &HostNode{client: r.fs.defaultClient}, attr), 0
}

// Readdir reads the root directory contents
func (r *RootNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries := []fuse.DirEntry{
		{Name: "default", Mode: fuse.S_IFDIR | 0755},
	}
	// In a real implementation, we might list known hosts
	return fs.NewListDirStream(entries), 0
}

// ModelsNode represents the models file
type ModelsNode struct {
	fs.Inode
	client *shelley.Client
}

// Read reads the models file
func (m *ModelsNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data, err := m.client.ListModels()
	if err != nil {
		return nil, syscall.EIO
	}
	
	result := readAt(data, dest, off)
	return fuse.ReadResultData(result), 0
}

// Getattr sets file attributes for models
func (m *ModelsNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	data, err := m.client.ListModels()
	if err != nil {
		return syscall.EIO
	}
	out.Size = uint64(len(data))
	out.Mode = 0444 // read-only
	return 0
}

// ModelDirNode represents the model directory
type ModelDirNode struct {
	fs.Inode
	client *shelley.Client
}

// Lookup handles model directory lookups
func (m *ModelDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// This would normally validate the model exists
	// For now, we'll allow any model name
	return m.NewInode(ctx, &ModelNode{name: name, client: m.client}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
}

// Readdir reads the model directory contents
func (m *ModelDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// In a real implementation, we'd list available models
	// For now, return empty list
	return fs.NewListDirStream([]fuse.DirEntry{}), 0
}

// ModelNode represents a specific model directory
type ModelNode struct {
	fs.Inode
	name   string
	client *shelley.Client
}

// Lookup handles model subdirectory lookups
func (m *ModelNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	switch name {
	case "new":
		return m.NewInode(ctx, &ModelNewDirNode{model: m.name, client: m.client}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}
	return nil, syscall.ENOENT
}

// Readdir reads the model subdirectory contents
func (m *ModelNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries := []fuse.DirEntry{
		{Name: "new", Mode: fuse.S_IFDIR | 0755},
	}
	return fs.NewListDirStream(entries), 0
}

// ModelNewDirNode represents the new conversation directory under a model
type ModelNewDirNode struct {
	fs.Inode
	model  string
	client *shelley.Client
}

// Lookup handles new conversation directory lookups
func (m *ModelNewDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// The name is the cwd
	return m.NewInode(ctx, &NewConversationNode{model: m.model, cwd: name, client: m.client}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
}

// Readdir reads the new conversation directory contents
func (m *ModelNewDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// This directory is meant to be used by creating files, not listing them
	return fs.NewListDirStream([]fuse.DirEntry{}), 0
}

// NewConversationNode represents a new conversation file
type NewConversationNode struct {
	fs.Inode
	model  string
	cwd    string
	client *shelley.Client
}

// Write handles writing to the new conversation file to create a conversation
func (n *NewConversationNode) Write(ctx context.Context, f fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	message := string(data)
	if off > 0 {
		// Append to existing message
		// In a real implementation, we'd need to track the partial message
		// For simplicity, we'll just use the new data
		message = message[int(off):]
	}
	
	_, err := n.client.StartConversation(message, n.model, n.cwd)
	if err != nil {
		return 0, syscall.EIO
	}
	
	// In a real implementation, we might want to store the conversation ID
	// somewhere accessible. For now, we'll just return success.
	return uint32(len(data)), 0
}

// Read handles reading from the new conversation file to get the conversation ID
func (n *NewConversationNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	// This is a write-only file in the typical usage
	// But we could return the conversation ID if one was created
	result := readAt([]byte(""), dest, off)
	return fuse.ReadResultData(result), 0
}

// Getattr sets file attributes
func (n *NewConversationNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0666 // read-write
	return 0
}

// ConversationDirNode represents the conversation directory
type ConversationDirNode struct {
	fs.Inode
	client *shelley.Client
}

// Lookup handles conversation directory lookups
func (c *ConversationDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return c.NewInode(ctx, &ConversationNode{conversationID: name, client: c.client}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
}

// Readdir reads the conversation directory contents
func (c *ConversationDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// In a real implementation, we'd list existing conversations
	// For now, return empty list
	return fs.NewListDirStream([]fuse.DirEntry{}), 0
}

// ConversationNode represents a specific conversation file
type ConversationNode struct {
	fs.Inode
	conversationID string
	model         string
	client        *shelley.Client
}

// Read reads the conversation content
func (c *ConversationNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data, err := c.client.GetConversation(c.conversationID)
	if err != nil {
		return nil, syscall.EIO
	}
	
	result := readAt(data, dest, off)
	return fuse.ReadResultData(result), 0
}

// Write handles writing to the conversation file to send messages
func (c *ConversationNode) Write(ctx context.Context, f fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	message := string(data)
	if off > 0 {
		// Append to existing message
		message = message[int(off):]
	}
	
	// Trim any trailing newlines that might be added by shell redirection
	message = strings.TrimRight(message, "\n")
	
	if message == "" {
		return uint32(len(data)), 0
	}
	
	if err := c.client.SendMessage(c.conversationID, message, ""); err != nil {
		return 0, syscall.EIO
	}
	
	return uint32(len(data)), 0
}

// Getattr sets file attributes for conversation
func (c *ConversationNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0666 // read-write
	return 0
}

// readAt is a helper function to read data at a specific offset
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
// HostNode represents a specific Shelley host
type HostNode struct {
	fs.Inode
	client *shelley.Client
}

// Lookup handles host directory lookups
func (h *HostNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	switch name {
	case "models":
		out.Mode = fuse.S_IFREG | 0444
		attr := fs.StableAttr{Mode: fuse.S_IFREG, Ino: 4}
		return h.NewInode(ctx, &ModelsNode{client: h.client}, attr), 0
	case "model":
		out.Mode = fuse.S_IFDIR | 0755
		attr := fs.StableAttr{Mode: fuse.S_IFDIR, Ino: 5}
		return h.NewInode(ctx, &ModelDirNode{client: h.client}, attr), 0
	case "new":
		out.Mode = fuse.S_IFDIR | 0755
		attr := fs.StableAttr{Mode: fuse.S_IFDIR, Ino: 6}
		return h.NewInode(ctx, &NewDirNode{client: h.client}, attr), 0
	case "conversations":
		out.Mode = fuse.S_IFDIR | 0755
		attr := fs.StableAttr{Mode: fuse.S_IFDIR, Ino: 7}
		return h.NewInode(ctx, &ConversationsDirNode{client: h.client}, attr), 0
	}
	return nil, syscall.ENOENT
}// Readdir reads the host directory contents
func (h *HostNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries := []fuse.DirEntry{
		{Name: "models", Mode: fuse.S_IFREG | 0444},
		{Name: "model", Mode: fuse.S_IFDIR | 0755},
		{Name: "new", Mode: fuse.S_IFDIR | 0755},
		{Name: "conversations", Mode: fuse.S_IFDIR | 0755},
	}
	return fs.NewListDirStream(entries), 0
}
// NewDirNode represents the new conversation directory
type NewDirNode struct {
	fs.Inode
	client *shelley.Client
}

// Lookup handles new directory lookups
func (n *NewDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// The name is the cwd
	return n.NewInode(ctx, &NewConversationNode{model: "", cwd: name, client: n.client}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
}

// Readdir reads the new directory contents
func (n *NewDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// This directory is meant to be used by creating files, not listing them
	return fs.NewListDirStream([]fuse.DirEntry{}), 0
}

// ConversationsDirNode represents the conversations directory
type ConversationsDirNode struct {
	fs.Inode
	client *shelley.Client
}

// Lookup handles conversations directory lookups
func (c *ConversationsDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return c.NewInode(ctx, &ConversationNode{conversationID: name, client: c.client}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
}

// Readdir reads the conversations directory contents
func (c *ConversationsDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// In a real implementation, we'd list existing conversations
	// For now, return empty list
	return fs.NewListDirStream([]fuse.DirEntry{}), 0
}