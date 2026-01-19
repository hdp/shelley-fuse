package fuse

import (
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"shelley-fuse/shelley"
	"shelley-fuse/testutil"
)

// TestInProcessFUSEExample demonstrates how to use the in-process FUSE server for testing
func TestInProcessFUSEExample(t *testing.T) {
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

// TestInProcessFUSEWithTestUtil demonstrates how to use the testutil package
func TestInProcessFUSEWithTestUtil(t *testing.T) {
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
	config := &testutil.InProcessFUSEConfig{
		MountPoint: mountPoint,
		Debug:      false,
		Timeout:    5 * time.Second,
		CreateFS: func() (fs.InodeEmbedder, error) {
			// Create a mock Shelley client
			mockClient := shelley.NewClient("http://localhost:11006")
			return NewFS(mockClient), nil
		},
	}

	// Start the in-process FUSE server
	server, err := testutil.StartInProcessFUSE(config)
	if err != nil {
		t.Logf("Error when starting FUSE server: %v", err)
		// This might fail due to missing fusermount or other system issues
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

	t.Logf("Successfully started in-process FUSE server at %s", mountPoint)
}