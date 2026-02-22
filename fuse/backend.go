package fuse

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"shelley-fuse/fuse/diag"
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

var backendNotFoundError = regexp.MustCompile(`backend "[^"]+" not found`)

// --- ShelleyDirNode: /shelley/ directory ---

type ShelleyDirNode struct {
	fs.Inode
	state        *state.Store
	clientMgr    *shelley.ClientManager
	cloneTimeout time.Duration
	parsedCache  *ParsedMessageCache
	startTime    time.Time
	diag         *diag.Tracker
}

var _ = (fs.NodeLookuper)((*ShelleyDirNode)(nil))
var _ = (fs.NodeReaddirer)((*ShelleyDirNode)(nil))
var _ = (fs.NodeGetattrer)((*ShelleyDirNode)(nil))

func (s *ShelleyDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(s.diag, "ShelleyDirNode", "Lookup", name).Done()
	setEntryTimeout(out, cacheTTLConversation)

	if name == "backend" {
		return s.NewInode(ctx, &BackendListNode{state: s.state, clientMgr: s.clientMgr, cloneTimeout: s.cloneTimeout, parsedCache: s.parsedCache, startTime: s.startTime, diag: s.diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}
	return nil, syscall.ENOENT
}

func (s *ShelleyDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	defer diag.Track(s.diag, "ShelleyDirNode", "Readdir", "").Done()
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "backend", Mode: fuse.S_IFDIR},
	}), 0
}

func (s *ShelleyDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, s.startTime)
	out.SetTimeout(cacheTTLConversation)
	return 0
}

// --- BackendListNode: /shelley/backend/ directory ---

type BackendListNode struct {
	fs.Inode
	state        *state.Store
	clientMgr    *shelley.ClientManager
	cloneTimeout time.Duration
	parsedCache  *ParsedMessageCache
	startTime    time.Time
	diag         *diag.Tracker
}

var _ = (fs.NodeLookuper)((*BackendListNode)(nil))
var _ = (fs.NodeReaddirer)((*BackendListNode)(nil))
var _ = (fs.NodeGetattrer)((*BackendListNode)(nil))
var _ = (fs.NodeSymlinker)((*BackendListNode)(nil))
var _ = (fs.NodeUnlinker)((*BackendListNode)(nil))
var _ = (fs.NodeMkdirer)((*BackendListNode)(nil))
var _ = (fs.NodeRenamer)((*BackendListNode)(nil))
var _ = (fs.NodeRmdirer)((*BackendListNode)(nil))

// Rmdir removes a backend with the given name.
// Returns EBUSY if the backend is the current default.
// Returns EINVAL for 'default' name (reserved symlink name).
func (b *BackendListNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	defer diag.Track(b.diag, "BackendListNode", "Rmdir", name).Done()

	// "default" is a reserved symlink name
	if name == "default" {
		return syscall.EINVAL
	}

	// Delete the backend from state
	if err := b.state.DeleteBackend(name); err != nil {
		// Map known errors to syscall errors
		if strings.Contains(err.Error(), "cannot delete default backend") {
			return syscall.EBUSY
		}
		if backendNotFoundError.MatchString(err.Error()) {
			return syscall.ENOENT
		}
		log.Printf("Rmdir backend %q: %v", name, err)
		return syscall.EIO
	}

	return 0
}

func (b *BackendListNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(b.diag, "BackendListNode", "Lookup", name).Done()
	// Use zero entry timeout for dynamic directory to allow create/remove operations
	out.SetEntryTimeout(0)

	// "default" is a symlink to the current default backend
	// The name "default" is reserved and never used as an actual backend name
	// It only exists when explicitly set (not when default == "main")
	if name == "default" {
		defaultBackend := b.state.GetDefaultBackend()
		if defaultBackend == state.DefaultBackendName {
			// "default" symlink doesn't exist when it's the implicit default "main"
			return nil, syscall.ENOENT
		}
		return b.NewInode(ctx, &DynamicSymlinkNode{
			getTarget: func() string {
				_ = b.state.Load()
				return b.state.GetDefaultBackend()
			},
			startTime: b.startTime,
		}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	}

	// Check if backend exists
	if b.state.GetBackend(name) != nil {
		return b.NewInode(ctx, &BackendNode{name: name, state: b.state, clientMgr: b.clientMgr, cloneTimeout: b.cloneTimeout, parsedCache: b.parsedCache, startTime: b.startTime, diag: b.diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}

	return nil, syscall.ENOENT
}

func (b *BackendListNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	defer diag.Track(b.diag, "BackendListNode", "Readdir", "").Done()

	backends := b.state.ListBackends()
	entries := make([]fuse.DirEntry, 0, len(backends)+1)

	// "default" is a symlink to the current default backend
	// Only include it if it's been explicitly set (not the default "main")
	if b.state.GetDefaultBackend() != state.DefaultBackendName {
		entries = append(entries, fuse.DirEntry{Name: "default", Mode: syscall.S_IFLNK})
	}

	// Add backend directories
	for _, name := range backends {
		entries = append(entries, fuse.DirEntry{Name: name, Mode: fuse.S_IFDIR})
	}

	return fs.NewListDirStream(entries), 0
}

func (b *BackendListNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, b.startTime)
	out.SetTimeout(cacheTTLConversation)
	return 0
}

// Mkdir creates a new backend with the given name.
// Simple names (no dots) get default URL https://{name}.shelley.exe.xyz.
// Dotted names get empty URL. Reserved name 'default' returns EEXIST.
func (b *BackendListNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(b.diag, "BackendListNode", "Mkdir", name).Done()
	setEntryTimeout(out, cacheTTLConversation)

	// "default" is a reserved symlink name - return EEXIST to indicate it already exists
	if name == "default" {
		return nil, syscall.EEXIST
	}

	// Determine URL based on whether name contains dots
	var url string
	if strings.Contains(name, ".") {
		// Dotted names get empty URL (for custom/backend-specific configurations)
		url = ""
	} else {
		// Simple names get default URL pattern
		url = fmt.Sprintf("https://%s.shelley.exe.xyz", name)
	}

	// Create the backend in state
	if err := b.state.CreateBackend(name, url); err != nil {
		// Map known errors to syscall errors
		if strings.Contains(err.Error(), "reserved") {
			return nil, syscall.EEXIST
		}
		if strings.Contains(err.Error(), "already exists") {
			return nil, syscall.EEXIST
		}
		return nil, syscall.EIO
	}

	// Return the newly created backend directory node
	return b.NewInode(ctx, &BackendNode{name: name, state: b.state, clientMgr: b.clientMgr, cloneTimeout: b.cloneTimeout, parsedCache: b.parsedCache, startTime: b.startTime, diag: b.diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
}

// Symlink creates a symlink within the backend directory.
// Only allows creating a symlink named "default" (EPERM for other names).
// Target must be an existing backend (ENOENT otherwise).
// Returns EEXIST if "default" already set to non-"main" (must remove first).
// Calls SetDefaultBackend on the state store when successful.
func (b *BackendListNode) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(b.diag, "BackendListNode", "Symlink", name).Done()
	setEntryTimeout(out, cacheTTLConversation)

	// Only allow creating a symlink named "default"
	if name != "default" {
		return nil, syscall.EPERM
	}

	// Verify that the target backend exists
	if b.state.GetBackend(target) == nil {
		return nil, syscall.ENOENT
	}

	// If default is already set to something other than "main", return EEXIST
	currentDefault := b.state.GetDefaultBackend()
	if currentDefault != state.DefaultBackendName {
		return nil, syscall.EEXIST
	}

	// Set the default backend in state
	if err := b.state.SetDefaultBackend(target); err != nil {
		return nil, syscall.EIO
	}

	// Return a dynamic symlink node that reads live state
	return b.NewInode(ctx, &DynamicSymlinkNode{
		getTarget: func() string {
			_ = b.state.Load()
			return b.state.GetDefaultBackend()
		},
		startTime: b.startTime,
	}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
}

// Unlink handles removing files/symlinks from the backend directory.
// Only allows removing the "default" symlink.
func (b *BackendListNode) Unlink(ctx context.Context, name string) syscall.Errno {
	defer diag.Track(b.diag, "BackendListNode", "Unlink", name).Done()

	// Only allow removing "default"
	if name != "default" {
		return syscall.EPERM
	}

	// Reset to the default backend name ("main")
	if err := b.state.SetDefaultBackend(state.DefaultBackendName); err != nil {
		return syscall.EIO
	}

	// Invalidate kernel cache for "default" so subsequent operations work
	// Must be done in a goroutine to avoid deadlock - NotifyEntry communicates
	// with the kernel, which is blocked waiting for this Unlink to return.
	go b.NotifyEntry("default")

	return 0
}

// --- DynamicSymlinkNode: symlink with target computed at readlink time ---

type DynamicSymlinkNode struct {
	fs.Inode
	getTarget func() string
	startTime time.Time
}

var _ = (fs.NodeReadlinker)((*DynamicSymlinkNode)(nil))
var _ = (fs.NodeGetattrer)((*DynamicSymlinkNode)(nil))

func (s *DynamicSymlinkNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	return []byte(s.getTarget()), 0
}

func (s *DynamicSymlinkNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFLNK | 0777
	out.Size = uint64(len(s.getTarget()))
	setTimestamps(&out.Attr, s.startTime)
	return 0
}

// --- BackendNode: /shelley/backend/{name}/ directory ---

type BackendNode struct {
	fs.Inode
	name        string
	state       *state.Store
	clientMgr   *shelley.ClientManager
	cloneTimeout time.Duration
	parsedCache  *ParsedMessageCache
	startTime   time.Time
	diag        *diag.Tracker
}



// Rename renames a backend directory. Only supports renaming within the same directory.
// Returns EXDEV for cross-directory rename.
// Returns EINVAL for renaming to or from the reserved name "default".
func (b *BackendListNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	defer diag.Track(b.diag, "BackendListNode", "Rename", fmt.Sprintf("%s -> %s", name, newName)).Done()

	// Cross-directory rename not supported
	targetParent, ok := newParent.(*BackendListNode)
	if !ok || targetParent != b {
		return syscall.EXDEV
	}

	// Cannot rename to or from reserved name "default"
	if name == "default" || newName == "default" {
		return syscall.EINVAL
	}

	// Find the existing backend for the old name
	backend := b.state.GetBackend(name)
	if backend == nil {
		return syscall.ENOENT
	}

	// Check if new name already exists
	if b.state.GetBackend(newName) != nil {
		return syscall.EEXIST
	}

	// Perform the rename in state
	if err := b.state.RenameBackend(name, newName); err != nil {
		// Map known errors to syscall errno
		if strings.Contains(err.Error(), "not found") {
			return syscall.ENOENT
		}
		if strings.Contains(err.Error(), "reserved") {
			return syscall.EINVAL
		}
		return syscall.EIO
	}

	return 0
}
var _ = (fs.NodeLookuper)((*BackendNode)(nil))
var _ = (fs.NodeReaddirer)((*BackendNode)(nil))
var _ = (fs.NodeGetattrer)((*BackendNode)(nil))

func (b *BackendNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(b.diag, "BackendNode", "Lookup", name).Done()
	setEntryTimeout(out, cacheTTLConversation)

	switch name {
	case "url":
		backend := b.state.GetBackend(b.name)
		if backend == nil {
			return nil, syscall.ENOENT
		}
		return b.NewInode(ctx, &BackendURLNode{url: backend.URL, startTime: b.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "connected":
		// Presence file - needs BackendConnectedNode implementation (sf-u12r)
		return nil, syscall.ENOENT
	case "model":
		// Get or create client for this backend
		backend := b.state.GetBackend(b.name)
		if backend == nil || backend.URL == "" {
			return nil, syscall.ENOENT
		}
		client, err := b.clientMgr.EnsureURL(b.name, backend.URL)
		if err != nil {
			return nil, syscall.EIO
		}
		return b.NewInode(ctx, &ModelsDirNode{client: client, state: b.state, startTime: b.startTime, diag: b.diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "conversation":
		// Get or create client for this backend
		backend := b.state.GetBackend(b.name)
		if backend == nil || backend.URL == "" {
			return nil, syscall.ENOENT
		}
		client, err := b.clientMgr.EnsureURL(b.name, backend.URL)
		if err != nil {
			return nil, syscall.EIO
		}
		return b.NewInode(ctx, &ConversationListNode{client: client, state: b.state, cloneTimeout: b.cloneTimeout, startTime: b.startTime, parsedCache: b.parsedCache, diag: b.diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	case "new":
		// Symlink to model/default/new (target doesn't need to exist yet)
		return b.NewInode(ctx, &SymlinkNode{target: "model/default/new", startTime: b.startTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	}
	return nil, syscall.ENOENT
}

func (b *BackendNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	defer diag.Track(b.diag, "BackendNode", "Readdir", "").Done()

	entries := []fuse.DirEntry{
		{Name: "url", Mode: fuse.S_IFREG},
		{Name: "connected", Mode: fuse.S_IFREG}, // presence file (may not exist)
		{Name: "model", Mode: fuse.S_IFDIR},
		{Name: "conversation", Mode: fuse.S_IFDIR},
		{Name: "new", Mode: syscall.S_IFLNK},
	}
	return fs.NewListDirStream(entries), 0
}

func (b *BackendNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, b.startTime)
	out.SetTimeout(cacheTTLConversation)
	return 0
}

// --- BackendURLNode: /shelley/backend/{name}/url file ---

type BackendURLNode struct {
	fs.Inode
	url       string
	startTime time.Time
}

var _ = (fs.NodeOpener)((*BackendURLNode)(nil))
var _ = (fs.NodeReader)((*BackendURLNode)(nil))
var _ = (fs.NodeGetattrer)((*BackendURLNode)(nil))

func (u *BackendURLNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (u *BackendURLNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data := []byte(u.url + "\n")
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (u *BackendURLNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = uint64(len(u.url) + 1) // +1 for newline
	setTimestamps(&out.Attr, u.startTime)
	return 0
}
