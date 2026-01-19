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
	"shelley-fuse/shelley"
)

func TestBasicMount(t *testing.T) {
	// Create a proper client with a safe URL (not the real instance)
	mockClient := shelley.NewClient("http://localhost:11002")
	
	// Create the FUSE filesystem
	shelleyFS := NewFS(mockClient)
	
	// Create a temporary directory for mounting
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-basic-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	// Set up FUSE server options
	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout
	
	// Mount the filesystem
	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()
	
	// Test if we can list the root directory
	entries, err := ioutil.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to read root directory: %v", err)
	}
	
	t.Logf("Found %d entries in root directory", len(entries))
	for i, entry := range entries {
		t.Logf("Entry %d: %s (dir: %v, mode: %o)", i, entry.Name(), entry.IsDir(), entry.Mode())
	}
	
	// Check for expected entries
	expectedEntries := map[string]bool{
		"models": false,
		"new":    false,
		"model":  false,
	}
	
	for _, entry := range entries {
		if _, exists := expectedEntries[entry.Name()]; exists {
			expectedEntries[entry.Name()] = true
			t.Logf("Found expected entry '%s'", entry.Name())
		}
	}
	
	for name, found := range expectedEntries {
		if !found {
			t.Errorf("Expected entry '%s' not found in root directory", name)
		}
	}
}

// Test that we can create a simple mount and unmount
type TestFS struct {
	fs.Inode
}

type TestRootNode struct {
	fs.Inode
}

func (t *TestRootNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "test", Mode: fuse.S_IFDIR | 0755},
	}), 0
}

func (t *TestRootNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return nil, syscall.ENOENT
}

func (t *TestFS) Root() *fs.Inode {
	return t.NewInode(context.Background(), &TestRootNode{}, fs.StableAttr{})
}

func TestSimpleMount(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "simple-fuse-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	testFS := &TestFS{}
	
	// Mount
	fssrv, err := fs.Mount(tmpDir, testFS, &fs.Options{})
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()
	
	// Test listing
	entries, err := ioutil.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to read directory: %v", err)
	}
	
	t.Logf("Simple mount test: found %d entries", len(entries))
}
// TestInProcessFUSE demonstrates how to use the in-process FUSE server for testing
func TestInProcessFUSE(t *testing.T) {
	// Skip if fusermount is not available
	if _, err := os.Stat("/usr/bin/fusermount"); os.IsNotExist(err) {
		if _, err := os.Stat("/bin/fusermount"); os.IsNotExist(err) {
			t.Skip("fusermount not found, skipping FUSE test")
		}
	}

	// Create a temporary directory for the FUSE mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-inprocess-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create mount point
	mountPoint := tmpDir + "/mount"
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		t.Fatalf("Failed to create mount point: %v", err)
	}

	// Create a mock Shelley client
	mockClient := shelley.NewClient("http://localhost:11005")
	
	// Create the FUSE filesystem
	shelleyFS := NewFS(mockClient)
	
	// Set up FUSE server options
	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	// Mount the filesystem in-process
	fssrv, err := fs.Mount(mountPoint, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// At this point, we have a fully functional in-process FUSE server
	// that we can test directly
	t.Logf("Successfully mounted in-process FUSE filesystem at %s", mountPoint)
}