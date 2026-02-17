package fuse

import (
	"context"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"shelley-fuse/fuse/diag"
	"shelley-fuse/state"
)

// --- ShelleyDirNode: /shelley/ directory ---

type ShelleyDirNode struct {
	fs.Inode
	state     *state.Store
	startTime time.Time
	diag      *diag.Tracker
}

var _ = (fs.NodeLookuper)((*ShelleyDirNode)(nil))
var _ = (fs.NodeReaddirer)((*ShelleyDirNode)(nil))
var _ = (fs.NodeGetattrer)((*ShelleyDirNode)(nil))

func (s *ShelleyDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(s.diag, "ShelleyDirNode", "Lookup", name).Done()
	setEntryTimeout(out, cacheTTLConversation)

	if name == "backend" {
		return s.NewInode(ctx, &BackendListNode{state: s.state, startTime: s.startTime, diag: s.diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
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
	state     *state.Store
	startTime time.Time
	diag      *diag.Tracker
}

var _ = (fs.NodeLookuper)((*BackendListNode)(nil))
var _ = (fs.NodeReaddirer)((*BackendListNode)(nil))
var _ = (fs.NodeGetattrer)((*BackendListNode)(nil))

func (b *BackendListNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(b.diag, "BackendListNode", "Lookup", name).Done()
	setEntryTimeout(out, cacheTTLConversation)

	// "default" is always a symlink to the current default backend
	// The name "default" is reserved and never used as an actual backend name
	if name == "default" {
		defaultBackend := b.state.GetDefaultBackend()
		return b.NewInode(ctx, &SymlinkNode{target: defaultBackend, startTime: b.startTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	}

	// Check if backend exists
	if b.state.GetBackend(name) != nil {
		return b.NewInode(ctx, &BackendNode{name: name, state: b.state, startTime: b.startTime, diag: b.diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}

	return nil, syscall.ENOENT
}

func (b *BackendListNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	defer diag.Track(b.diag, "BackendListNode", "Readdir", "").Done()

	backends := b.state.ListBackends()
	entries := make([]fuse.DirEntry, 0, len(backends)+1)

	// "default" is always a symlink to the current default backend
	// The name "default" is reserved and never used as an actual backend name
	entries = append(entries, fuse.DirEntry{Name: "default", Mode: syscall.S_IFLNK})

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

// --- BackendNode: /shelley/backend/{name}/ directory ---

type BackendNode struct {
	fs.Inode
	name      string
	state     *state.Store
	startTime time.Time
	diag      *diag.Tracker
}

var _ = (fs.NodeReaddirer)((*BackendNode)(nil))
var _ = (fs.NodeGetattrer)((*BackendNode)(nil))

func (b *BackendNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	defer diag.Track(b.diag, "BackendNode", "Readdir", "").Done()
	// For now, backend directories are empty placeholders.
	// URL configuration and other backend-specific files will be added later.
	return fs.NewListDirStream(nil), 0
}

func (b *BackendNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, b.startTime)
	out.SetTimeout(cacheTTLConversation)
	return 0
}
