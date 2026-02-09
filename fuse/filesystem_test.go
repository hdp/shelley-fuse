package fuse

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"shelley-fuse/shelley"
)

func TestBasicMount(t *testing.T) {
	mockClient := shelley.NewClient("http://localhost:11002")
	store := testStore(t)
	shelleyFS := NewFS(mockClient, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-basic-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	entries, err := ioutil.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to read root directory: %v", err)
	}

	expectedEntries := map[string]bool{
		"model":        false,
		"new":          false,
		"conversation": false,
		"README.md":    false,
	}

	for _, entry := range entries {
		if _, exists := expectedEntries[entry.Name()]; exists {
			expectedEntries[entry.Name()] = true
		}
	}

	for name, found := range expectedEntries {
		if !found {
			t.Errorf("Expected entry '%s' not found in root directory", name)
		}
	}
}

func TestNewIsSymlinkToModelsDefaultNew(t *testing.T) {
	client := shelley.NewClient("http://localhost:11002")
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-new-symlink-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	newPath := filepath.Join(tmpDir, "new")

	// Verify /new is a symlink
	info, err := os.Lstat(newPath)
	if err != nil {
		t.Fatalf("Lstat /new failed: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("/new should be a symlink, got mode %v", info.Mode())
	}

	// Verify symlink target
	target, err := os.Readlink(newPath)
	if err != nil {
		t.Fatalf("Readlink /new failed: %v", err)
	}
	if target != "model/default/new" {
		t.Errorf("/new symlink target = %q, want %q", target, "model/default/new")
	}
}


// --- Tests for ReadmeNode ---

func TestReadmeNode_Read(t *testing.T) {
	node := &ReadmeNode{}
	dest := make([]byte, 8192)
	result, errno := node.Read(context.Background(), nil, dest, 0)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ := result.Bytes(nil)
	if string(data) != readmeContent {
		t.Errorf("README content mismatch: got %d bytes, expected %d bytes", len(data), len(readmeContent))
	}
}

func TestReadmeNode_ReadOffset(t *testing.T) {
	node := &ReadmeNode{}

	// Read from offset 10
	dest := make([]byte, 20)
	result, errno := node.Read(context.Background(), nil, dest, 10)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ := result.Bytes(nil)
	expected := readmeContent[10:30]
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}

	// Read from offset beyond content
	result, errno = node.Read(context.Background(), nil, dest, int64(len(readmeContent)+100))
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ = result.Bytes(nil)
	if len(data) != 0 {
		t.Errorf("expected empty result for offset beyond content, got %q", string(data))
	}
}

func TestReadmeNode_Getattr(t *testing.T) {
	node := &ReadmeNode{}
	var out fuse.AttrOut
	errno := node.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr failed with errno %d", errno)
	}

	// Check mode is read-only (0444)
	expectedMode := uint32(fuse.S_IFREG | 0444)
	if out.Mode != expectedMode {
		t.Errorf("expected mode %o, got %o", expectedMode, out.Mode)
	}

	// Check size matches readmeContent
	if out.Size != uint64(len(readmeContent)) {
		t.Errorf("expected size %d, got %d", len(readmeContent), out.Size)
	}
}

func TestReadmeNode_MountedRead(t *testing.T) {
	mockClient := shelley.NewClient("http://localhost:11002")
	store := testStore(t)
	shelleyFS := NewFS(mockClient, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-readme-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Read README.md content
	readmePath := filepath.Join(tmpDir, "README.md")
	data, err := ioutil.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("Failed to read README.md: %v", err)
	}
	if string(data) != readmeContent {
		t.Errorf("README.md content mismatch: got %d bytes, expected %d bytes", len(data), len(readmeContent))
	}

	// Check file attributes
	info, err := os.Stat(readmePath)
	if err != nil {
		t.Fatalf("Failed to stat README.md: %v", err)
	}
	if info.Size() != int64(len(readmeContent)) {
		t.Errorf("expected size %d, got %d", len(readmeContent), info.Size())
	}
	// Check read-only permission (0444)
	perm := info.Mode().Perm()
	if perm != 0444 {
		t.Errorf("expected permission 0444, got %o", perm)
	}
}


// --- Tests for timestamp functionality ---

func TestTimestamps_StaticNodesUseStartTime(t *testing.T) {
	// Test that static nodes (models, new, root) use FS start time
	server := mockModelsServer(t, []shelley.Model{{ID: "test-model", Ready: true}})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Get the start time from the FS
	startTime := shelleyFS.StartTime()

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-timestamp-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Test root directory timestamp
	t.Run("RootDirectory", func(t *testing.T) {
		info, err := os.Stat(tmpDir)
		if err != nil {
			t.Fatalf("Failed to stat root: %v", err)
		}
		mtime := info.ModTime()
		// Should be within 1 second of startTime
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Root mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		// Should not be zero (1970)
		if mtime.Unix() == 0 {
			t.Error("Root mtime is zero (1970)")
		}
	})

	// Test models directory timestamp
	t.Run("ModelsDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "model"))
		if err != nil {
			t.Fatalf("Failed to stat models: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Models mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Models mtime is zero (1970)")
		}
	})

	// Test new symlink timestamp
	t.Run("NewSymlink", func(t *testing.T) {
		info, err := os.Lstat(filepath.Join(tmpDir, "new"))
		if err != nil {
			t.Fatalf("Failed to lstat new: %v", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("/new should be a symlink")
		}
		mtime := info.ModTime()
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("New symlink mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("New symlink mtime is zero (1970)")
		}
	})

	// Test model subdirectory timestamp
	t.Run("ModelSubdirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "model", "test-model"))
		if err != nil {
			t.Fatalf("Failed to stat model: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Model mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Model mtime is zero (1970)")
		}
	})

	// Test model file timestamp
	t.Run("ModelFile", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "model", "test-model", "id"))
		if err != nil {
			t.Fatalf("Failed to stat model/id: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Model/id mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Model/id mtime is zero (1970)")
		}
	})

	// Test model clone file timestamp
	t.Run("ModelCloneFile", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "model", "test-model", "new", "clone"))
		if err != nil {
			t.Fatalf("Failed to stat model/test-model/new/clone: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("ModelClone mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("ModelClone mtime is zero (1970)")
		}
	})

	// Test conversation list directory timestamp
	t.Run("ConversationListDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation"))
		if err != nil {
			t.Fatalf("Failed to stat conversation: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Conversation mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Conversation mtime is zero (1970)")
		}
	})
}


func TestTimestamps_DoNotConstantlyUpdate(t *testing.T) {
	// Test that timestamps don't constantly update to "now"
	server := mockModelsServer(t, []shelley.Model{{ID: "test-model", Ready: true}})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-stable-timestamp-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Stat the models directory twice with a delay
	info1, err := os.Stat(filepath.Join(tmpDir, "model"))
	if err != nil {
		t.Fatalf("Failed to stat models (1): %v", err)
	}
	mtime1 := info1.ModTime()

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	info2, err := os.Stat(filepath.Join(tmpDir, "model"))
	if err != nil {
		t.Fatalf("Failed to stat models (2): %v", err)
	}
	mtime2 := info2.ModTime()

	// Timestamps should be identical (not updating to "now")
	if !mtime1.Equal(mtime2) {
		t.Errorf("Models timestamp changed between stats: %v -> %v", mtime1, mtime2)
	}
}


func TestTimestamps_NeverZero(t *testing.T) {
	// Test that no timestamps are ever zero (1970)
	server := mockModelsServer(t, []shelley.Model{{ID: "test-model", Ready: true}})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Clone a conversation
	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-nonzero-timestamp-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Check various paths - none should have zero timestamp
	// Note: "created" is not checked because it uses presence/absence semantics
	// and only exists when conversation is created on backend
	// Paths checked via os.Stat (follows symlinks)
	statPaths := []string{
		tmpDir,                         // root
		filepath.Join(tmpDir, "model"), // models dir
		filepath.Join(tmpDir, "model", "test-model"),                 // model dir
		filepath.Join(tmpDir, "model", "test-model", "id"),           // model file
		filepath.Join(tmpDir, "model", "test-model", "new", "clone"), // model clone file
		filepath.Join(tmpDir, "conversation"),                        // conversation list
		filepath.Join(tmpDir, "conversation", convID),                // conversation dir
		filepath.Join(tmpDir, "conversation", convID, "ctl"),
		filepath.Join(tmpDir, "conversation", convID, "send"),
		filepath.Join(tmpDir, "conversation", convID, "fuse_id"),
		// "created" not checked - uses presence/absence semantics
		filepath.Join(tmpDir, "conversation", convID, "messages"),
		filepath.Join(tmpDir, "conversation", convID, "messages", "last"),
		filepath.Join(tmpDir, "conversation", convID, "messages", "since"),
	}

	for _, path := range statPaths {
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("Failed to stat %s: %v", path, err)
			continue
		}
		mtime := info.ModTime()
		if mtime.Unix() == 0 {
			t.Errorf("Path %s has zero mtime (1970)", path)
		}
		// Also check it's a reasonable recent time (within last hour)
		if time.Since(mtime) > time.Hour {
			t.Errorf("Path %s has mtime %v which is more than 1 hour ago", path, mtime)
		}
	}

	// /new is a symlink — check via Lstat
	info, err := os.Lstat(filepath.Join(tmpDir, "new"))
	if err != nil {
		t.Errorf("Failed to lstat /new: %v", err)
	} else {
		mtime := info.ModTime()
		if mtime.Unix() == 0 {
			t.Errorf("/new symlink has zero mtime (1970)")
		}
		if time.Since(mtime) > time.Hour {
			t.Errorf("/new symlink has mtime %v which is more than 1 hour ago", mtime)
		}
	}
}

