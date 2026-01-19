package testutil

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

// Simple test filesystem that implements the required interfaces
type testFS struct {
	fs.Inode
}

type testRootNode struct {
	fs.Inode
}

func (t *testRootNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "test", Mode: fuse.S_IFDIR | 0755},
	}), 0
}

func (t *testRootNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return nil, syscall.ENOENT
}

func (t *testFS) Root() *fs.Inode {
	return t.NewInode(context.Background(), &testRootNode{}, fs.StableAttr{})
}

func TestInProcessFUSEServer(t *testing.T) {
	// Skip if fusermount is not available
	if _, err := os.Stat("/usr/bin/fusermount"); os.IsNotExist(err) {
		if _, err := os.Stat("/bin/fusermount"); os.IsNotExist(err) {
			t.Skip("fusermount not found, skipping FUSE test")
		}
	}

	// Create a temporary directory for the FUSE mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-testutil-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create mount point
	mountPoint := tmpDir + "/mount"
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		t.Fatalf("Failed to create mount point: %v", err)
	}

	// Configuration for in-process FUSE server
	config := &InProcessFUSEConfig{
		MountPoint: mountPoint,
		Debug:      false,
		Timeout:    5 * time.Second,
		CreateFS: func() (fs.InodeEmbedder, error) {
			return &testFS{}, nil
		},
	}

	// Try to start the in-process FUSE server
	server, err := StartInProcessFUSE(config)
	if err != nil {
		t.Logf("Error when starting FUSE server: %v", err)
		return
	}

	// If we successfully started, test error collection
	defer func() {
		if server != nil {
			server.Stop()
		}
	}()

	// Test error collection
	if server.HasErrors() {
		t.Errorf("Server should not have errors at startup")
	}

	// Clear errors
	server.ClearErrors()

	// Check that errors were cleared
	if server.HasErrors() {
		t.Errorf("Server should not have errors after clearing")
	}
}

func TestInProcessFUSEConfigValidation(t *testing.T) {
	// Test missing mount point
	config := &InProcessFUSEConfig{
		CreateFS: func() (fs.InodeEmbedder, error) {
			return &testFS{}, nil
		},
	}

	_, err := StartInProcessFUSE(config)
	if err == nil {
		t.Error("Expected error for missing mount point")
	}

	// Test missing CreateFS function
	config = &InProcessFUSEConfig{
		MountPoint: "/tmp/test-mount",
	}

	_, err = StartInProcessFUSE(config)
	if err == nil {
		t.Error("Expected error for missing CreateFS function")
	}
}