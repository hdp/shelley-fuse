package fuse

import (
	"context"
	"io/ioutil"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// MinimalFS is a minimal filesystem
type MinimalFS struct {
	fs.Inode
}

func (m *MinimalFS) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries := []fuse.DirEntry{
		{Name: "test", Mode: fuse.S_IFDIR | 0755, Ino: 2},
	}
	return fs.NewListDirStream(entries), 0
}

func (m *MinimalFS) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	return 0
}

func (m *MinimalFS) Root() *fs.Inode {
	attr := fs.StableAttr{Mode: fuse.S_IFDIR, Ino: 1}
	return m.NewInode(context.Background(), m, attr)
}

func TestMinimalMount(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "minimal-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	minimalFS := &MinimalFS{}
	
	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout
	
	server, err := fs.Mount(tmpDir, minimalFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer server.Unmount()
	
	entries, err := ioutil.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	
	t.Logf("Found %d entries", len(entries))
	for i, entry := range entries {
		t.Logf("Entry %d: %s", i, entry.Name())
	}
	
	if len(entries) != 1 {
		t.Errorf("Expected 1 entry, got %d", len(entries))
	}
}
