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

// MyRootNode is a working root node following the go-fuse pattern
type MyRootNode struct {
	fs.Inode
}

func (n *MyRootNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries := []fuse.DirEntry{
		{Name: "test", Mode: fuse.S_IFDIR | 0755},
	}
	return fs.NewListDirStream(entries), 0
}

func (n *MyRootNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if name == "test" {
		out.Mode = fuse.S_IFDIR | 0755
		testNode := &TestNode{}
		return n.NewInode(ctx, testNode, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}
	return nil, syscall.ENOENT
}

func (n *MyRootNode) Getattr(ctx context.Context, file fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	return 0
}

// TestNode is a simple test node  
type TestNode struct {
	fs.Inode
}

func TestWorkingPattern(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "working-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	rootNode := &MyRootNode{}
	
	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout
	
	server, err := fs.Mount(tmpDir, rootNode, opts)
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
