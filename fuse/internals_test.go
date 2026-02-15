package fuse

// Internal FUSE tests
//
// These tests verify behavior that cannot be tested via shell commands:
// - Inode number stability
// - Timestamp precision (nanoseconds)
// - Direct node API behavior
// - Internal hash functions

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"shelley-fuse/mockserver"
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

// =============================================================================
// Inode Stability Tests
// =============================================================================

// =============================================================================
// Timestamp Precision Tests
// =============================================================================

func TestTimestamps_MessageDirUsesCreatedAt(t *testing.T) {
	convID := "conv-msg-time"
	messages := []shelley.Message{
		{
			MessageID:      "msg-1",
			ConversationID: convID,
			SequenceID:     1,
			Type:           "user",
			UserData:       strPtr("Hello"),
			CreatedAt:      "2024-02-20T09:15:30Z",
		},
	}

	server := mockserver.New(mockserver.WithConversation(convID, messages))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create and mark conversation as created
	localID, _ := store.Clone()
	_ = store.MarkCreated(localID, convID, "test-slug")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-msg-time-test")
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

	// Stat the message directory
	msgDirPath := filepath.Join(tmpDir, "conversation", localID, "messages", "0-user")
	var stat syscall.Stat_t
	if err := syscall.Stat(msgDirPath, &stat); err != nil {
		t.Fatalf("syscall.Stat failed: %v", err)
	}

	expectedTime, _ := time.Parse(time.RFC3339, "2024-02-20T09:15:30Z")
	actualMtime := time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec)
	actualCtime := time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec)

	// Both ctime and mtime should be from message's created_at
	if !actualMtime.Equal(expectedTime) {
		t.Errorf("Message dir mtime = %v, want %v", actualMtime, expectedTime)
	}
	if !actualCtime.Equal(expectedTime) {
		t.Errorf("Message dir ctime = %v, want %v", actualCtime, expectedTime)
	}
}

func TestTimestamps_MessagesSubdirUsesConversationMetadata(t *testing.T) {
	convID := "conv-msg-subdir"
	messages := []shelley.Message{}

	server := mockserver.New(mockserver.WithConversation(convID, messages))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create conversation with API metadata
	localID, _ := store.Clone()
	_ = store.MarkCreated(localID, convID, "test-slug")

	// Set API timestamps
	_, _ = store.AdoptWithMetadata(convID, "test-slug", "2024-03-01T10:00:00Z", "2024-03-05T15:00:00Z", "", "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-msg-subdir-test")
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

	// Stat the messages/ directory
	msgsDirPath := filepath.Join(tmpDir, "conversation", localID, "messages")
	var stat syscall.Stat_t
	if err := syscall.Stat(msgsDirPath, &stat); err != nil {
		t.Fatalf("syscall.Stat failed: %v", err)
	}

	expectedCtime, _ := time.Parse(time.RFC3339, "2024-03-01T10:00:00Z")
	expectedMtime, _ := time.Parse(time.RFC3339, "2024-03-05T15:00:00Z")

	actualCtime := time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec)
	actualMtime := time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec)

	if !actualCtime.Equal(expectedCtime) {
		t.Errorf("messages/ ctime = %v, want %v", actualCtime, expectedCtime)
	}
	if !actualMtime.Equal(expectedMtime) {
		t.Errorf("messages/ mtime = %v, want %v", actualMtime, expectedMtime)
	}
}

func TestTimestamps_MessageFilesUseMessageTime(t *testing.T) {
	// Create messages with different timestamps
	convID := "test-conv-msg-timestamps"
	msg1Time := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	msg2Time := time.Date(2026, 1, 15, 10, 5, 0, 0, time.UTC)  // 5 minutes later
	msg3Time := time.Date(2026, 1, 15, 10, 10, 0, 0, time.UTC) // 10 minutes later

	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("Hello"), CreatedAt: msg1Time.Format(time.RFC3339)},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi there!"), CreatedAt: msg2Time.Format(time.RFC3339)},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr("Thanks"), CreatedAt: msg3Time.Format(time.RFC3339)},
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, 0)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-msg-timestamp-test")
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

	// Test message directories and their content.md files
	testCases := []struct {
		name         string
		path         string // relative to messages/
		expectedTime time.Time
	}{
		{"Message1_Dir", "0-user", msg1Time},
		{"Message1_ContentMD", "0-user/content.md", msg1Time},
		{"Message2_Dir", "1-agent", msg2Time},
		{"Message2_ContentMD", "1-agent/content.md", msg2Time},
		{"Message3_Dir", "2-user", msg3Time},
		{"Message3_ContentMD", "2-user/content.md", msg3Time},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tmpDir, "conversation", localID, "messages", tc.path)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("Failed to stat %s: %v", tc.path, err)
			}
			mtime := info.ModTime()
			diff := mtime.Sub(tc.expectedTime)
			if diff < -time.Second || diff > time.Second {
				t.Errorf("Path %s mtime %v differs from expected %v by %v", tc.path, mtime, tc.expectedTime, diff)
			}
		})
	}

	// Also verify that all.json/all.md still use conversation time (not message time)
	t.Run("AllJsonUsesConvTime", func(t *testing.T) {
		cs := store.Get(localID)
		if cs == nil {
			t.Fatal("Conversation not found")
		}
		convTime := cs.CreatedAt

		info, err := os.Stat(filepath.Join(tmpDir, "conversation", localID, "messages", "all.json"))
		if err != nil {
			t.Fatalf("Failed to stat all.json: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("all.json mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		// Verify it's NOT using message time
		if mtime.Equal(msg1Time) || mtime.Equal(msg2Time) || mtime.Equal(msg3Time) {
			t.Errorf("all.json should use conversation time, not message time: mtime=%v", mtime)
		}
	})
}

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

// =============================================================================
// Direct Node API Tests
// =============================================================================

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

func TestModelsDirNode_Readdir(t *testing.T) {
	models := []shelley.Model{
		{ID: "model-a", Ready: true},
		{ID: "model-b", Ready: false},
		{ID: "model-c", Ready: true},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	node := &ModelsDirNode{client: client}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
		if entry.Mode != fuse.S_IFDIR {
			t.Errorf("expected directory mode for %q", entry.Name)
		}
	}

	sort.Strings(names)
	expected := []string{"model-a", "model-b", "model-c"}
	sort.Strings(expected)

	if len(names) != len(expected) {
		t.Fatalf("expected %d entries, got %d: %v", len(expected), len(names), names)
	}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("entry %d: expected %q, got %q", i, expected[i], name)
		}
	}
}

func TestModelNode_Readdir(t *testing.T) {
	model := shelley.Model{ID: "test-model", Ready: true}
	node := &ModelNode{model: model}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var entries []fuse.DirEntry
	for stream.HasNext() {
		entry, _ := stream.Next()
		entries = append(entries, entry)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (id, new, ready), got %d", len(entries))
	}

	expectedModes := map[string]uint32{"id": fuse.S_IFREG, "new": fuse.S_IFDIR, "ready": fuse.S_IFREG}
	found := map[string]bool{}
	for _, e := range entries {
		expMode, ok := expectedModes[e.Name]
		if !ok {
			t.Errorf("unexpected entry %q", e.Name)
			continue
		}
		found[e.Name] = true
		if e.Mode != expMode {
			t.Errorf("entry %q: expected mode %d, got %d", e.Name, expMode, e.Mode)
		}
	}
	for name := range expectedModes {
		if !found[name] {
			t.Errorf("expected entry %q not found", name)
		}
	}
}

func TestModelFieldNode_Read(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{"id field", "my-model", "my-model\n"},
		{"ready true", "true", "true\n"},
		{"ready false", "false", "false\n"},
		{"empty value", "", "\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := &ModelFieldNode{value: tc.value}
			dest := make([]byte, 1024)
			result, errno := node.Read(context.Background(), nil, dest, 0)
			if errno != 0 {
				t.Fatalf("Read failed with errno %d", errno)
			}
			data, _ := result.Bytes(nil)
			if string(data) != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, string(data))
			}
		})
	}
}

func TestModelFieldNode_ReadOffset(t *testing.T) {
	node := &ModelFieldNode{value: "hello"}

	// Read from offset 2
	dest := make([]byte, 1024)
	result, errno := node.Read(context.Background(), nil, dest, 2)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ := result.Bytes(nil)
	if string(data) != "llo\n" {
		t.Errorf("expected %q, got %q", "llo\n", string(data))
	}

	// Read from offset beyond content
	result, errno = node.Read(context.Background(), nil, dest, 100)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ = result.Bytes(nil)
	if len(data) != 0 {
		t.Errorf("expected empty result for offset beyond content, got %q", string(data))
	}
}

func TestModelNewDirNode_Readdir(t *testing.T) {
	model := shelley.Model{ID: "test-model", Ready: true}
	node := &ModelNewDirNode{model: model}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var entries []fuse.DirEntry
	for stream.HasNext() {
		entry, _ := stream.Next()
		entries = append(entries, entry)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (clone, start), got %d", len(entries))
	}
	expected := map[string]bool{"clone": false, "start": false}
	for _, e := range entries {
		if _, ok := expected[e.Name]; !ok {
			t.Errorf("unexpected entry %q", e.Name)
		} else {
			expected[e.Name] = true
		}
		if e.Mode != fuse.S_IFREG {
			t.Errorf("expected file mode for %q", e.Name)
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("missing expected entry %q", name)
		}
	}
}

func TestModelsDirNode_EmptyModels(t *testing.T) {
	// Server returns empty model list
	server := mockModelsServer(t, []shelley.Model{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	node := &ModelsDirNode{client: client}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	count := 0
	for stream.HasNext() {
		stream.Next()
		count++
	}

	if count != 0 {
		t.Errorf("expected 0 entries for empty model list, got %d", count)
	}
}

func TestModelsDirNode_DefaultSymlink_Readdir(t *testing.T) {
	// When a default model is set, it should appear as a symlink in the directory listing
	models := []shelley.Model{
		{ID: "model-a", Ready: true},
		{ID: "model-b", Ready: false},
	}
	server := mockModelsServerWithDefault(t, models, "model-a")
	defer server.Close()

	client := shelley.NewClient(server.URL)
	node := &ModelsDirNode{client: client}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else if entry.Mode == fuse.S_IFDIR {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 2 directories (model-a, model-b) and 1 symlink (default)
	if len(dirs) != 2 {
		t.Errorf("expected 2 directories, got %d: %v", len(dirs), dirs)
	}
	if len(symlinks) != 1 {
		t.Fatalf("expected 1 symlink, got %d: %v", len(symlinks), symlinks)
	}
	if symlinks[0] != "default" {
		t.Errorf("expected symlink named 'default', got %q", symlinks[0])
	}
}

func TestModelsDirNode_DefaultSymlink_NoDefault_Readdir(t *testing.T) {
	// When no default model is set, the symlink should NOT appear in the listing
	models := []shelley.Model{
		{ID: "model-a", Ready: true},
		{ID: "model-b", Ready: false},
	}
	server := mockModelsServerWithDefault(t, models, "") // No default
	defer server.Close()

	client := shelley.NewClient(server.URL)
	node := &ModelsDirNode{client: client}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var names []string
	hasSymlink := false
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
		if entry.Mode&syscall.S_IFLNK != 0 {
			hasSymlink = true
		}
	}

	// Should have only directories, no symlink
	if hasSymlink {
		t.Error("unexpected symlink in directory listing when no default model is set")
	}
	if len(names) != 2 {
		t.Errorf("expected 2 entries (models only), got %d: %v", len(names), names)
	}
}

func TestModelStartNode_Read(t *testing.T) {
	node := &ModelStartNode{model: shelley.Model{ID: "test-model"}, startTime: time.Now()}

	result, errno := node.Read(context.Background(), nil, make([]byte, 4096), 0)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ := result.Bytes(make([]byte, 4096))
	script := string(data)

	if !strings.HasPrefix(script, "#!/bin/sh") {
		t.Error("model start script should begin with #!/bin/sh shebang")
	}
	// The model version uses the sibling clone file
	if !strings.Contains(script, "$DIR/clone") {
		t.Error("model start script should reference $DIR/clone")
	}
	if !strings.Contains(script, "/ctl") {
		t.Error("model start script should write to ctl")
	}
	if !strings.Contains(script, "/send") {
		t.Error("model start script should write to send")
	}
}

func TestModelStartNode_Getattr(t *testing.T) {
	node := &ModelStartNode{model: shelley.Model{ID: "test-model"}, startTime: time.Now()}
	var out fuse.AttrOut
	errno := node.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr failed with errno %d", errno)
	}
	if out.Mode&0111 == 0 {
		t.Error("model start script should be executable")
	}
	if out.Size == 0 {
		t.Error("model start script should have non-zero size")
	}
}

func TestMessagesDirNodeReaddirWithToolCalls(t *testing.T) {
	// Create mock server that returns conversation with tool calls
	convID := "test-conv-with-tools"
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content": [{"Type": 5, "ID": "tu_123", "ToolName": "bash"}]}`)},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr(`{"Content": [{"Type": 6, "ToolUseID": "tu_123"}]}`)},
		{MessageID: "m4", ConversationID: convID, SequenceID: 4, Type: "shelley", LLMData: strPtr("Done!")},
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create and mark conversation as created
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	node := &MessagesDirNode{
		localID:   localID,
		client:    client,
		state:     store,
		startTime: time.Now(),
	}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	// Collect all entries
	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
	}

	// Expected entries:
	// - Static: all.json, all.md, count, last, since
	// - Message directories: 0-user, 1-bash-tool, 2-bash-result, 3-agent (0-indexed)
	expected := []string{
		"all.json", "all.md", "count", "last", "since",
		"0-user",
		"1-bash-tool",
		"2-bash-result",
		"3-agent",
	}

	namesSet := make(map[string]bool)
	for _, name := range names {
		namesSet[name] = true
	}

	for _, exp := range expected {
		if !namesSet[exp] {
			t.Errorf("Expected file %q not found in Readdir results: %v", exp, names)
		}
	}

	// Verify total count
	if len(names) != len(expected) {
		t.Errorf("Expected %d entries, got %d: %v", len(expected), len(names), names)
	}
}

// TestMessageFieldStableInodes verifies that message field nodes use stable,
// deterministic inode numbers derived from (conversationID, sequenceID, fieldName).
// This allows the kernel to recognize the same logical file across lookups.
func TestMessageFieldStableInodes(t *testing.T) {
	convID := "test-conv-stable-ino"
	msgs := []shelley.Message{
		{
			MessageID:      "msg-uuid-001",
			ConversationID: convID,
			SequenceID:     1,
			Type:           "user",
			UserData:       strPtr("Hello"),
			CreatedAt:      "2026-01-15T10:00:00Z",
		},
		{
			MessageID:      "msg-uuid-002",
			ConversationID: convID,
			SequenceID:     2,
			Type:           "shelley",
			LLMData:        strPtr(`{"Content":[{"Type":2,"Text":"Hi"}]}`),
			CreatedAt:      "2026-01-15T10:01:00Z",
		},
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-stable-ino-test")
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

	msgDir := filepath.Join(tmpDir, "conversation", localID, "messages")

	// Stat each field file twice and verify the inode is stable across lookups.
	userDir := filepath.Join(msgDir, "0-user")
	agentDir := filepath.Join(msgDir, "1-agent")

	fields := []string{"message_id", "conversation_id", "sequence_id", "type", "created_at", "content.md"}

	// Collect inodes from first stat pass
	userInodes := make(map[string]uint64)
	agentInodes := make(map[string]uint64)

	for _, field := range fields {
		info, err := os.Stat(filepath.Join(userDir, field))
		if err != nil {
			t.Fatalf("Stat %s/0-user/%s: %v", localID, field, err)
		}
		userInodes[field] = statIno(info)

		info, err = os.Stat(filepath.Join(agentDir, field))
		if err != nil {
			t.Fatalf("Stat %s/1-agent/%s: %v", localID, field, err)
		}
		agentInodes[field] = statIno(info)
	}

	// Also check llm_data directory inode for agent
	info, err := os.Stat(filepath.Join(agentDir, "llm_data"))
	if err != nil {
		t.Fatalf("Stat llm_data: %v", err)
	}
	agentLLMIno := statIno(info)

	// Verify all inode numbers are non-zero (stable, not auto-assigned 0)
	for field, ino := range userInodes {
		if ino == 0 {
			t.Errorf("user/%s inode should be non-zero", field)
		}
	}
	for field, ino := range agentInodes {
		if ino == 0 {
			t.Errorf("agent/%s inode should be non-zero", field)
		}
	}
	if agentLLMIno == 0 {
		t.Errorf("agent/llm_data inode should be non-zero")
	}

	// Verify same field in different messages gets different inodes
	for _, field := range fields {
		if userInodes[field] == agentInodes[field] {
			t.Errorf("%s: user and agent inodes should differ, both are %d", field, userInodes[field])
		}
	}

	// Verify different fields within the same message get different inodes
	seenUser := make(map[uint64]string)
	for field, ino := range userInodes {
		if prev, ok := seenUser[ino]; ok {
			t.Errorf("inode collision in user message: %s and %s both have ino %d", prev, field, ino)
		}
		seenUser[ino] = field
	}

	// Verify message directory itself has a stable inode
	userDirInfo, err := os.Stat(userDir)
	if err != nil {
		t.Fatalf("Stat user dir: %v", err)
	}
	userDirIno := statIno(userDirInfo)
	if userDirIno == 0 {
		t.Errorf("user message dir inode should be non-zero")
	}

	agentDirInfo, err := os.Stat(agentDir)
	if err != nil {
		t.Fatalf("Stat agent dir: %v", err)
	}
	agentDirIno := statIno(agentDirInfo)
	if agentDirIno == 0 {
		t.Errorf("agent message dir inode should be non-zero")
	}
	if userDirIno == agentDirIno {
		t.Errorf("user and agent message dir inodes should differ")
	}
}

// =============================================================================
// Internal Function Tests
// =============================================================================

func TestMsgFieldIno(t *testing.T) {
	// Same inputs produce same output (deterministic)
	ino1 := msgFieldIno("conv-abc", 1, "message_id")
	ino2 := msgFieldIno("conv-abc", 1, "message_id")
	if ino1 != ino2 {
		t.Errorf("same inputs should produce same inode: %d != %d", ino1, ino2)
	}
	if ino1 == 0 {
		t.Error("inode should be non-zero")
	}

	// Different field names produce different inodes
	ino3 := msgFieldIno("conv-abc", 1, "type")
	if ino1 == ino3 {
		t.Errorf("different fields should produce different inodes: both %d", ino1)
	}

	// Different sequence IDs produce different inodes
	ino4 := msgFieldIno("conv-abc", 2, "message_id")
	if ino1 == ino4 {
		t.Errorf("different seqIDs should produce different inodes: both %d", ino1)
	}

	// Different conversation IDs produce different inodes
	ino5 := msgFieldIno("conv-xyz", 1, "message_id")
	if ino1 == ino5 {
		t.Errorf("different convIDs should produce different inodes: both %d", ino1)
	}
}
func TestConversationListNode_ReaddirLocalOnly(t *testing.T) {
	// Server returns the conversations we've created locally
	server := mockConversationsServer(t, []shelley.Conversation{
		{ConversationID: "server-id-1"},
		{ConversationID: "server-id-2"},
	})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create some local conversations and mark them as created
	// (uncreated conversations are hidden from listing)
	id1, _ := store.Clone()
	store.MarkCreated(id1, "server-id-1", "")
	id2, _ := store.Clone()
	store.MarkCreated(id2, "server-id-2", "")

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else if entry.Name != "last" {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 2 directories (local IDs) and 2 symlinks (server IDs)
	if len(dirs) != 2 {
		t.Fatalf("expected 2 directories, got %d: %v", len(dirs), dirs)
	}
	if len(symlinks) != 2 {
		t.Fatalf("expected 2 symlinks, got %d: %v", len(symlinks), symlinks)
	}

	// Check that our local IDs are in the directories
	sort.Strings(dirs)
	expectedDirs := []string{id1, id2}
	sort.Strings(expectedDirs)
	for i, name := range dirs {
		if name != expectedDirs[i] {
			t.Errorf("dir %d: expected %q, got %q", i, expectedDirs[i], name)
		}
	}
}

func TestConversationListNode_ReaddirServerConversationsAdopted(t *testing.T) {
	// Server returns conversations, no prior local state.
	// Readdir should adopt them all immediately, returning:
	// - 2 directories for local IDs
	// - 2 symlinks for server IDs
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-aaa"},
		{ConversationID: "server-conv-bbb"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Before Readdir, store should be empty
	if len(store.List()) != 0 {
		t.Fatal("expected empty store before Readdir")
	}

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else if entry.Mode&syscall.S_IFDIR != 0 && entry.Name != "last" {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 2 directories (adopted server conversations as local IDs)
	if len(dirs) != 2 {
		t.Fatalf("expected 2 directories, got %d: %v", len(dirs), dirs)
	}

	// Should have 2 symlinks (server IDs pointing to local IDs)
	if len(symlinks) != 2 {
		t.Fatalf("expected 2 symlinks, got %d: %v", len(symlinks), symlinks)
	}

	// Directory entries should be local IDs (8-char hex)
	for _, name := range dirs {
		if len(name) != 8 {
			t.Errorf("expected 8-char local ID for directory, got %q", name)
		}
	}

	// Symlink entries should be the server IDs
	serverIDSet := map[string]bool{"server-conv-aaa": true, "server-conv-bbb": true}
	for _, name := range symlinks {
		if !serverIDSet[name] {
			t.Errorf("unexpected symlink %q, expected server IDs", name)
		}
	}

	// Verify the conversations were adopted into local state
	localIDs := store.List()
	if len(localIDs) != 2 {
		t.Fatalf("expected 2 conversations in store after Readdir, got %d", len(localIDs))
	}

	// Verify each adopted conversation has the correct Shelley ID
	shelleyIDs := make(map[string]bool)
	for _, id := range localIDs {
		cs := store.Get(id)
		if cs == nil {
			t.Fatalf("expected conversation state for %s", id)
		}
		if !cs.Created {
			t.Errorf("expected Created=true for adopted conversation %s", id)
		}
		shelleyIDs[cs.ShelleyConversationID] = true
	}
	if !shelleyIDs["server-conv-aaa"] {
		t.Error("server-conv-aaa was not adopted")
	}
	if !shelleyIDs["server-conv-bbb"] {
		t.Error("server-conv-bbb was not adopted")
	}
}

func TestConversationListNode_ReaddirMergedLocalAndServer(t *testing.T) {
	// Server returns some conversations, some overlap with local.
	// Readdir returns:
	// - Directories for created local IDs that are on server (3 total after adoption)
	// - Symlinks for server IDs (3 total)
	// Note: conversations with Shelley IDs not on server are filtered out (stale)
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-111"},
		{ConversationID: "server-conv-222"}, // This one is already tracked locally
		{ConversationID: "server-conv-333"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create a local conversation tracking a server conversation
	localTracked, _ := store.Clone()
	_ = store.MarkCreated(localTracked, "server-conv-222", "") // This tracks server-conv-222

	// Before Readdir: 1 local conversation
	if len(store.List()) != 1 {
		t.Fatalf("expected 1 conversation before Readdir, got %d", len(store.List()))
	}

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else if entry.Mode&syscall.S_IFDIR != 0 && entry.Name != "last" {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 3 directories:
	// - localTracked (existing local ID, tracks server-conv-222)
	// - new local ID for server-conv-111 (adopted)
	// - new local ID for server-conv-333 (adopted)
	if len(dirs) != 3 {
		t.Fatalf("expected 3 directories, got %d: %v", len(dirs), dirs)
	}

	// Should have 3 symlinks for server IDs:
	// - server-conv-111 -> its local ID
	// - server-conv-222 -> localTracked
	// - server-conv-333 -> its local ID
	if len(symlinks) != 3 {
		t.Fatalf("expected 3 symlinks, got %d: %v", len(symlinks), symlinks)
	}

	// Directory entries should be local IDs (8-char hex)
	for _, name := range dirs {
		if len(name) != 8 {
			t.Errorf("expected 8-char local ID for directory, got %q", name)
		}
	}

	// Symlink entries should be the server IDs
	serverIDSet := map[string]bool{"server-conv-111": true, "server-conv-222": true, "server-conv-333": true}
	for _, name := range symlinks {
		if !serverIDSet[name] {
			t.Errorf("unexpected symlink %q, expected server IDs", name)
		}
	}

	// Verify localTracked is still present as a directory
	foundLocalTracked := false
	for _, name := range dirs {
		if name == localTracked {
			foundLocalTracked = true
		}
	}
	if !foundLocalTracked {
		t.Errorf("localTracked %s not found in directories", localTracked)
	}

	// After Readdir: should have 3 conversations (1 original + 2 adopted)
	localIDs := store.List()
	if len(localIDs) != 3 {
		t.Fatalf("expected 3 conversations in store after Readdir, got %d", len(localIDs))
	}

	// Verify all server conversations are now tracked
	for _, shelleyID := range []string{"server-conv-111", "server-conv-222", "server-conv-333"} {
		localID := store.GetByShelleyID(shelleyID)
		if localID == "" {
			t.Errorf("server conversation %s should be tracked locally", shelleyID)
		}
	}

	// Verify server-conv-222 is still tracked by localTracked (not duplicated)
	if store.GetByShelleyID("server-conv-222") != localTracked {
		t.Errorf("server-conv-222 should still be tracked by %s", localTracked)
	}
}

func TestConversationListNode_ReaddirServerError(t *testing.T) {
	// Server returns an error - should still show created local conversations
	// When server fails, we show all local entries including symlinks for server IDs
	// (since we can't verify if they're stale)
	server := mockErrorServer(t)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create local conversations and mark them as created
	// (uncreated conversations are hidden from listing)
	id1, _ := store.Clone()
	store.MarkCreated(id1, "server-id-1", "")
	id2, _ := store.Clone()
	store.MarkCreated(id2, "server-id-2", "")

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else if entry.Name != "last" {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 2 directories (local IDs) and 2 symlinks (server IDs)
	if len(dirs) != 2 {
		t.Fatalf("expected 2 directories, got %d: %v", len(dirs), dirs)
	}
	if len(symlinks) != 2 {
		t.Fatalf("expected 2 symlinks, got %d: %v", len(symlinks), symlinks)
	}
}

func TestConversationListNode_ReaddirFiltersStaleConversations(t *testing.T) {
	// Test that conversations with a Shelley ID that no longer exists on server
	// are filtered out from Readdir results (prevents broken symlinks)

	// Server returns only conv-active, NOT conv-deleted
	slug := "active-slug"
	server := mockConversationsServer(t, []shelley.Conversation{
		{ConversationID: "conv-active", Slug: &slug},
	})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Adopt conversations that exist on server
	activeLocalID, _ := store.Adopt("conv-active")

	// Adopt a conversation that NO LONGER exists on server (stale)
	staleLocalID, _ := store.Adopt("conv-deleted")

	// Verify both are in the store before Readdir
	if len(store.List()) != 2 {
		t.Fatalf("expected 2 conversations in store, got %d", len(store.List()))
	}

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
	}

	// Should see:
	// - activeLocalID (local ID as directory)
	// - conv-active (symlink to active)
	// - active-slug (symlink, now that AdoptWithSlug updates empty slugs)
	// Should NOT see:
	// - staleLocalID or conv-deleted (filtered out because conv-deleted not on server)

	// Check that stale entries are NOT present
	for _, name := range names {
		if name == staleLocalID {
			t.Errorf("stale local ID %q should not appear in Readdir", staleLocalID)
		}
		if name == "conv-deleted" {
			t.Error("stale server ID 'conv-deleted' should not appear in Readdir")
		}
	}

	// Check that expected entries ARE present
	namesSet := make(map[string]bool)
	for _, name := range names {
		namesSet[name] = true
	}

	// AdoptWithSlug now updates empty slugs, so we should see the slug symlink
	expected := []string{activeLocalID, "conv-active", "active-slug"}
	for _, exp := range expected {
		if !namesSet[exp] {
			t.Errorf("expected entry %q not found in Readdir results: %v", exp, names)
		}
	}

	// Verify total count: 4 entries (1 dir + 2 symlinks for server ID and slug + "last" dir)
	if len(names) != 4 {
		t.Errorf("expected 4 entries, got %d: %v", len(names), names)
	}
}

func TestConversationListNode_ReaddirShowsStaleWhenServerFails(t *testing.T) {
	// When server is unreachable, we should show ALL created local entries
	// including ones with Shelley IDs (since we can't verify them)
	// Note: uncreated conversations are still hidden from listing
	server := mockErrorServer(t)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Adopt a conversation (simulating one that might be stale)
	// Adopt automatically marks it as created
	adoptedLocalID, _ := store.Adopt("conv-possibly-stale")

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
	}

	// When server fails, should see all created entries:
	// - adoptedLocalID (directory)
	// - conv-possibly-stale (symlink)
	namesSet := make(map[string]bool)
	for _, name := range names {
		namesSet[name] = true
	}

	expected := []string{adoptedLocalID, "conv-possibly-stale"}
	for _, exp := range expected {
		if !namesSet[exp] {
			t.Errorf("expected entry %q not found when server fails: %v", exp, names)
		}
	}

	if len(names) != 3 {
		t.Errorf("expected 3 entries when server fails, got %d: %v", len(names), names)
	}
}

// Helper to mount a test filesystem and return mount point and cleanup function

func TestConversationListNode_LookupLocalID(t *testing.T) {
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Lookup by stat'ing the conversation directory
	info, err := os.Stat(filepath.Join(mountPoint, "conversation", localID))
	if err != nil {
		t.Fatalf("Lookup for local ID should succeed: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestConversationListNode_LookupNonexistent(t *testing.T) {
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	store := testStore(t)

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Lookup should fail for nonexistent ID
	_, err := os.Stat(filepath.Join(mountPoint, "conversation", "nonexistent-id"))
	if err == nil {
		t.Error("Lookup for nonexistent ID should fail")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected ENOENT, got %v", err)
	}
}

func TestConversationListNode_LookupServerConversation(t *testing.T) {
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-lookup-test"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	store := testStore(t)

	// Before lookup, store should be empty
	if len(store.List()) != 0 {
		t.Fatal("expected empty store before lookup")
	}

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Lookup the server conversation by its ID
	info, err := os.Stat(filepath.Join(mountPoint, "conversation", "server-conv-lookup-test"))
	if err != nil {
		t.Fatalf("Lookup for server conversation should succeed: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}

	// After lookup, the conversation should be adopted into local state
	ids := store.List()
	if len(ids) != 1 {
		t.Fatalf("expected 1 conversation after adoption, got %d", len(ids))
	}

	// Verify the adopted conversation has the correct Shelley ID
	cs := store.Get(ids[0])
	if cs == nil {
		t.Fatal("expected conversation state")
	}
	if cs.ShelleyConversationID != "server-conv-lookup-test" {
		t.Errorf("expected ShelleyConversationID=server-conv-lookup-test, got %s", cs.ShelleyConversationID)
	}
	if !cs.Created {
		t.Error("expected Created=true for adopted conversation")
	}
}

func TestConversationListNode_LookupServerConversationIdempotent(t *testing.T) {
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-idempotent"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	store := testStore(t)

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Lookup twice
	_, err := os.Stat(filepath.Join(mountPoint, "conversation", "server-conv-idempotent"))
	if err != nil {
		t.Fatalf("first Lookup failed: %v", err)
	}

	_, err = os.Stat(filepath.Join(mountPoint, "conversation", "server-conv-idempotent"))
	if err != nil {
		t.Fatalf("second Lookup failed: %v", err)
	}

	// Should still only have one conversation
	ids := store.List()
	if len(ids) != 1 {
		t.Errorf("expected 1 conversation, got %d", len(ids))
	}
}

func TestConversationListNode_LookupServerError(t *testing.T) {
	server := mockErrorServer(t)
	defer server.Close()

	store := testStore(t)

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Lookup for a non-local ID when server errors should fail
	_, err := os.Stat(filepath.Join(mountPoint, "conversation", "some-server-id"))
	if err == nil {
		t.Error("Lookup should fail when server errors and ID is not local")
	}
}

func TestConversationListNode_LookupLocalTakesPrecedence(t *testing.T) {
	// Server has a conversation, but we also have it tracked locally
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-precedence"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	store := testStore(t)

	// Track the conversation locally first
	localID, _ := store.Clone()
	_ = store.MarkCreated(localID, "server-conv-precedence", "")

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Lookup by local ID should work
	info, err := os.Stat(filepath.Join(mountPoint, "conversation", localID))
	if err != nil {
		t.Fatalf("Lookup by local ID should succeed: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}

	// Should still only have one conversation (no duplicate created)
	ids := store.List()
	if len(ids) != 1 {
		t.Errorf("expected 1 conversation, got %d", len(ids))
	}
}


func TestConversationListingMounted(t *testing.T) {
	serverConvs := []shelley.Conversation{
		{ConversationID: "mounted-server-conv-1"},
		{ConversationID: "mounted-server-conv-2"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Server conversations will be adopted during Readdir
	// Note: uncreated local conversations are not shown in listing

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-conv-list-test")
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

	// Read the conversation directory
	entries, err := ioutil.ReadDir(filepath.Join(tmpDir, "conversation"))
	if err != nil {
		t.Fatalf("Failed to read conversation directory: %v", err)
	}

	// Separate directories (local IDs) from symlinks (server IDs)
	var dirs, symlinks []string
	for _, entry := range entries {
		if entry.Mode()&os.ModeSymlink != 0 {
			symlinks = append(symlinks, entry.Name())
		} else if entry.IsDir() && entry.Name() != "last" {
			dirs = append(dirs, entry.Name())
		}
	}

	// Should have 2 directories: 2 adopted server conversations
	// (uncreated local conversations are hidden from listing)
	if len(dirs) != 2 {
		t.Fatalf("expected 2 directories, got %d: %v", len(dirs), dirs)
	}

	// Should have 2 symlinks for server IDs
	if len(symlinks) != 2 {
		t.Fatalf("expected 2 symlinks, got %d: %v", len(symlinks), symlinks)
	}

	// Directory entries should be local IDs (8-char hex)
	for _, name := range dirs {
		if len(name) != 8 {
			t.Errorf("expected 8-char local ID for directory, got %q", name)
		}
	}

	// Symlink entries should be the server IDs
	for _, name := range symlinks {
		if !strings.HasPrefix(name, "mounted-server-conv-") {
			t.Errorf("expected server ID symlink, got %q", name)
		}
	}

	// Verify server conversations were adopted
	for _, shelleyID := range []string{"mounted-server-conv-1", "mounted-server-conv-2"} {
		localID := store.GetByShelleyID(shelleyID)
		if localID == "" {
			t.Errorf("server conversation %s should be tracked locally", shelleyID)
		}
	}
}


func TestConversationListNode_ReaddirUpdatesEmptySlugs(t *testing.T) {
	// This test verifies that AdoptWithSlug correctly updates empty slugs
	// for already-tracked conversations when rediscovered via Readdir.

	// Server returns a conversation with a slug
	slug := "my-conversation-slug"
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-slug-update", Slug: &slug},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Adopt the conversation WITHOUT a slug (simulating old adoption)
	localID, err := store.AdoptWithSlug("server-conv-slug-update", "")
	if err != nil {
		t.Fatalf("AdoptWithSlug failed: %v", err)
	}

	// Verify slug is empty initially
	cs := store.Get(localID)
	if cs.Slug != "" {
		t.Fatalf("Expected empty slug initially, got %q", cs.Slug)
	}

	// Readdir should update the slug for already-tracked conversations
	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	// Collect all entries
	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else if entry.Mode&syscall.S_IFDIR != 0 && entry.Name != "last" {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 1 directory (local ID)
	if len(dirs) != 1 {
		t.Fatalf("Expected 1 directory, got %d: %v", len(dirs), dirs)
	}
	if dirs[0] != localID {
		t.Errorf("Expected directory %q, got %q", localID, dirs[0])
	}

	// Should have 2 symlinks: server ID and slug (slug now updated)
	if len(symlinks) != 2 {
		t.Fatalf("Expected 2 symlinks (server ID and slug), got %d: %v", len(symlinks), symlinks)
	}

	// Verify both symlinks are present
	symlinkSet := make(map[string]bool)
	for _, s := range symlinks {
		symlinkSet[s] = true
	}
	if !symlinkSet["server-conv-slug-update"] {
		t.Errorf("Expected server ID symlink 'server-conv-slug-update', got %v", symlinks)
	}
	if !symlinkSet[slug] {
		t.Errorf("Expected slug symlink %q, got %v", slug, symlinks)
	}

	// Verify the state WAS updated with the slug
	cs = store.Get(localID)
	if cs.Slug != slug {
		t.Errorf("State slug should be updated: got %q, want %q", cs.Slug, slug)
	}
}

// TestConversationListNode_ReaddirWithSlugs tests that conversations with slugs
// appear correctly in the directory listing with slug symlinks.
func TestConversationListNode_ReaddirWithSlugs(t *testing.T) {
	// Server returns conversations with slugs
	slug1 := "first-conversation"
	slug2 := "second-conversation"
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-with-slug-1", Slug: &slug1},
		{ConversationID: "server-conv-with-slug-2", Slug: &slug2},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	// Collect all entries
	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else if entry.Mode&syscall.S_IFDIR != 0 && entry.Name != "last" {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 2 directories (local IDs)
	if len(dirs) != 2 {
		t.Fatalf("Expected 2 directories, got %d: %v", len(dirs), dirs)
	}

	// Should have 4 symlinks: 2 server IDs + 2 slugs
	if len(symlinks) != 4 {
		t.Fatalf("Expected 4 symlinks (2 server IDs + 2 slugs), got %d: %v", len(symlinks), symlinks)
	}

	// Verify both slugs are present as symlinks
	expectedSymlinks := map[string]bool{
		"server-conv-with-slug-1": false,
		"server-conv-with-slug-2": false,
		"first-conversation":      false,
		"second-conversation":     false,
	}
	for _, name := range symlinks {
		if _, ok := expectedSymlinks[name]; ok {
			expectedSymlinks[name] = true
		}
	}
	for name, found := range expectedSymlinks {
		if !found {
			t.Errorf("Expected symlink %q not found", name)
		}
	}

	// Verify slugs were persisted in state
	for _, localID := range dirs {
		cs := store.Get(localID)
		if cs == nil {
			t.Errorf("Missing state for local ID %s", localID)
			continue
		}
		if cs.Slug == "" {
			t.Errorf("Expected non-empty slug for local ID %s", localID)
		}
	}
}


func TestTimestamps_ConversationNodesUseCreatedAt(t *testing.T) {
	// Test that conversation nodes use conversation creation time
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Wait a bit so we can distinguish start time from conversation time
	time.Sleep(10 * time.Millisecond)

	// Clone a conversation - this sets CreatedAt
	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	// Get the conversation creation time
	cs := store.Get(convID)
	if cs == nil {
		t.Fatal("Conversation not found in store")
	}
	convTime := cs.CreatedAt
	if convTime.IsZero() {
		t.Fatal("Conversation CreatedAt is zero")
	}

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-conv-timestamp-test")
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

	// Test conversation directory timestamp
	t.Run("ConversationDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID))
		if err != nil {
			t.Fatalf("Failed to stat conversation dir: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Conversation dir mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Conversation dir mtime is zero (1970)")
		}
	})

	// Test ctl file timestamp
	t.Run("CtlFile", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID, "ctl"))
		if err != nil {
			t.Fatalf("Failed to stat ctl: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Ctl mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Ctl mtime is zero (1970)")
		}
	})

	// Test send file timestamp
	t.Run("SendFile", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID, "send"))
		if err != nil {
			t.Fatalf("Failed to stat send: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Send file mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Send file mtime is zero (1970)")
		}
	})

	// Test fuse_id file timestamp (status fields are now at conversation root)
	t.Run("FuseIdFile", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID, "fuse_id"))
		if err != nil {
			t.Fatalf("Failed to stat fuse_id: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("fuse_id mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("fuse_id mtime is zero (1970)")
		}
	})

	// Test last directory timestamp
	t.Run("LastDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID, "messages", "last"))
		if err != nil {
			t.Fatalf("Failed to stat last: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Last mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Last mtime is zero (1970)")
		}
	})

	// Test since directory timestamp
	t.Run("SinceDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID, "messages", "since"))
		if err != nil {
			t.Fatalf("Failed to stat since: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Since mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Since mtime is zero (1970)")
		}
	})

}


func TestTimestamps_ConversationTimeDiffersFromStartTime(t *testing.T) {
	// Test that conversation time is different from FS start time
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	startTime := shelleyFS.StartTime()

	// Wait a bit so conversation time is clearly different
	time.Sleep(50 * time.Millisecond)

	// Clone a conversation
	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-time-diff-test")
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

	// Get models mtime (should be startTime)
	modelsInfo, err := os.Stat(filepath.Join(tmpDir, "model"))
	if err != nil {
		t.Fatalf("Failed to stat models: %v", err)
	}
	modelsMtime := modelsInfo.ModTime()

	// Get conversation mtime (should be convTime, later than startTime)
	convInfo, err := os.Stat(filepath.Join(tmpDir, "conversation", convID))
	if err != nil {
		t.Fatalf("Failed to stat conversation: %v", err)
	}
	convMtime := convInfo.ModTime()

	// Models should use startTime
	modelsDiff := modelsMtime.Sub(startTime)
	if modelsDiff < -time.Second || modelsDiff > time.Second {
		t.Errorf("Models mtime %v should be close to startTime %v", modelsMtime, startTime)
	}

	// Conversation should be later than startTime
	if !convMtime.After(startTime) {
		t.Errorf("Conversation mtime %v should be after startTime %v", convMtime, startTime)
	}

	// Conversation should be different from models
	if modelsMtime.Equal(convMtime) {
		t.Error("Conversation mtime should differ from models mtime")
	}

	t.Logf("startTime: %v, modelsMtime: %v, convMtime: %v", startTime, modelsMtime, convMtime)
}


func TestTimestamps_SymlinksUseConversationTime(t *testing.T) {
	// Test that symlinks for server IDs use conversation creation time
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-123"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-symlink-timestamp-test")
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

	// List conversations to trigger adoption
	_, err = ioutil.ReadDir(filepath.Join(tmpDir, "conversation"))
	if err != nil {
		t.Fatalf("Failed to read conversation dir: %v", err)
	}

	// Get the symlink info (use Lstat to not follow)
	symlinkInfo, err := os.Lstat(filepath.Join(tmpDir, "conversation", "server-conv-123"))
	if err != nil {
		t.Fatalf("Failed to lstat symlink: %v", err)
	}

	mtime := symlinkInfo.ModTime()

	// Should not be zero
	if mtime.Unix() == 0 {
		t.Error("Symlink mtime is zero (1970)")
	}

	// Should be a reasonable recent time
	if time.Since(mtime) > time.Hour {
		t.Errorf("Symlink mtime %v is more than 1 hour ago", mtime)
	}
}


func TestTimestamps_MultipleConversationsHaveDifferentTimes(t *testing.T) {
	// Test that different conversations have different creation times
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Clone first conversation
	convID1, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone first: %v", err)
	}

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Clone second conversation
	convID2, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone second: %v", err)
	}

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-multi-conv-timestamp-test")
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

	// Get timestamps for both conversations
	info1, err := os.Stat(filepath.Join(tmpDir, "conversation", convID1))
	if err != nil {
		t.Fatalf("Failed to stat conv1: %v", err)
	}
	mtime1 := info1.ModTime()

	info2, err := os.Stat(filepath.Join(tmpDir, "conversation", convID2))
	if err != nil {
		t.Fatalf("Failed to stat conv2: %v", err)
	}
	mtime2 := info2.ModTime()

	// Second conversation should be later
	if !mtime2.After(mtime1) {
		t.Errorf("Second conversation mtime %v should be after first %v", mtime2, mtime1)
	}

	t.Logf("conv1 mtime: %v, conv2 mtime: %v, diff: %v", mtime1, mtime2, mtime2.Sub(mtime1))
}


func TestTimestamps_StateCreatedAtIsPersisted(t *testing.T) {
	// Test that CreatedAt is persisted to the state file and survives reload
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	// Create store and clone
	store1, err := state.NewStore(statePath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	convID, err := store1.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	cs1 := store1.Get(convID)
	if cs1.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be set after clone")
	}
	originalTime := cs1.CreatedAt

	// Create new store from same path (simulating restart)
	store2, err := state.NewStore(statePath)
	if err != nil {
		t.Fatalf("Failed to reload store: %v", err)
	}

	cs2 := store2.Get(convID)
	if cs2 == nil {
		t.Fatal("Conversation not found after reload")
	}

	if cs2.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero after reload")
	}

	// Times should be equal (within nanosecond precision loss from JSON)
	diff := cs2.CreatedAt.Sub(originalTime)
	if diff < -time.Microsecond || diff > time.Microsecond {
		t.Errorf("CreatedAt changed after reload: %v -> %v (diff: %v)", originalTime, cs2.CreatedAt, diff)
	}
}

func TestConversationNode_ModelSymlink_NoModel(t *testing.T) {
	// Test that model symlink returns ENOENT when model is not set
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	// Create mount
	client := shelley.NewClient("http://example.com")
	shelleyFS := NewFS(client, store, 0)

	tmpDir := t.TempDir()
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

	// Try to read model symlink - should fail with ENOENT
	modelPath := filepath.Join(tmpDir, "conversation", convID, "model")
	_, err = os.Lstat(modelPath)
	if err == nil {
		t.Error("Expected error for model symlink when model not set")
	} else if !os.IsNotExist(err) {
		t.Errorf("Expected ENOENT, got: %v", err)
	}
}

func TestConversationNode_ModelSymlink_WithModel(t *testing.T) {
	// Test that model symlink is created and points to correct target
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	// Set model
	err = store.SetCtl(convID, "model", "claude-opus-4")
	if err != nil {
		t.Fatalf("Failed to set model: %v", err)
	}

	// Create mount
	client := shelley.NewClient("http://example.com")
	shelleyFS := NewFS(client, store, 0)

	tmpDir := t.TempDir()
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

	modelPath := filepath.Join(tmpDir, "conversation", convID, "model")

	// Check that symlink exists
	info, err := os.Lstat(modelPath)
	if err != nil {
		t.Fatalf("Failed to lstat model symlink: %v", err)
	}

	// Verify it's a symlink
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("Expected symlink, got mode %v", info.Mode())
	}

	// Read the symlink target
	target, err := os.Readlink(modelPath)
	if err != nil {
		t.Fatalf("Failed to readlink: %v", err)
	}

	expectedTarget := "../../model/claude-opus-4"
	if target != expectedTarget {
		t.Errorf("Expected target %q, got %q", expectedTarget, target)
	}
}

func TestConversationNode_ModelSymlink_InReaddir(t *testing.T) {
	// Test that model symlink appears in Readdir when model is set
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	// Create mount without model set first
	client := shelley.NewClient("http://example.com")
	shelleyFS := NewFS(client, store, 0)

	tmpDir := t.TempDir()
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

	convDir := filepath.Join(tmpDir, "conversation", convID)

	// Read dir without model - should NOT contain model entry
	entries, err := ioutil.ReadDir(convDir)
	if err != nil {
		t.Fatalf("Failed to read dir: %v", err)
	}

	hasModel := false
	for _, e := range entries {
		if e.Name() == "model" {
			hasModel = true
			break
		}
	}
	if hasModel {
		t.Error("model symlink should not appear when model is not set")
	}

	// Now set the model
	err = store.SetCtl(convID, "model", "test-model")
	if err != nil {
		t.Fatalf("Failed to set model: %v", err)
	}

	// Read dir again - should now contain model entry
	entries, err = ioutil.ReadDir(convDir)
	if err != nil {
		t.Fatalf("Failed to read dir after setting model: %v", err)
	}

	hasModel = false
	var modelEntry os.FileInfo
	for _, e := range entries {
		if e.Name() == "model" {
			hasModel = true
			modelEntry = e
			break
		}
	}
	if !hasModel {
		t.Error("model symlink should appear when model is set")
	}

	// Verify it's listed as a symlink
	if modelEntry != nil && modelEntry.Mode()&os.ModeSymlink == 0 {
		t.Errorf("model entry should be a symlink, got mode %v", modelEntry.Mode())
	}
}

func TestConversationNode_ModelSymlink_Timestamp(t *testing.T) {
	// Test that model symlink uses conversation creation time
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	cs := store.Get(convID)
	convTime := cs.CreatedAt

	// Set model
	err = store.SetCtl(convID, "model", "test-model")
	if err != nil {
		t.Fatalf("Failed to set model: %v", err)
	}

	// Create mount
	client := shelley.NewClient("http://example.com")
	shelleyFS := NewFS(client, store, 0)

	tmpDir := t.TempDir()
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

	modelPath := filepath.Join(tmpDir, "conversation", convID, "model")

	info, err := os.Lstat(modelPath)
	if err != nil {
		t.Fatalf("Failed to lstat model symlink: %v", err)
	}

	mtime := info.ModTime()
	diff := mtime.Sub(convTime)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("Model symlink mtime %v differs from conversation time %v by %v", mtime, convTime, diff)
	}
}

func TestTimestamps_APIMetadataMapping(t *testing.T) {
	// Create a mock server with conversations that have API timestamps
	convs := []shelley.Conversation{
		{
			ConversationID: "conv-with-timestamps",
			Slug:           strPtr("test-conv"),
			CreatedAt:      "2024-01-15T10:30:00Z",
			UpdatedAt:      "2024-01-16T14:20:00Z",
		},
	}
	server := mockConversationsServer(t, convs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Mount the filesystem
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-api-metadata-test")
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

	// List conversations to trigger adoption with metadata
	_, err = ioutil.ReadDir(filepath.Join(tmpDir, "conversation"))
	if err != nil {
		t.Fatalf("Failed to read conversation dir: %v", err)
	}

	// Get the local ID for the adopted conversation
	localID := store.GetByShelleyID("conv-with-timestamps")
	if localID == "" {
		t.Fatal("Conversation was not adopted")
	}

	// Verify the API timestamps were stored
	cs := store.Get(localID)
	if cs == nil {
		t.Fatal("Conversation state not found")
	}
	if cs.APICreatedAt != "2024-01-15T10:30:00Z" {
		t.Errorf("APICreatedAt = %q, want %q", cs.APICreatedAt, "2024-01-15T10:30:00Z")
	}
	if cs.APIUpdatedAt != "2024-01-16T14:20:00Z" {
		t.Errorf("APIUpdatedAt = %q, want %q", cs.APIUpdatedAt, "2024-01-16T14:20:00Z")
	}

	// Stat the conversation directory and verify timestamps
	convPath := filepath.Join(tmpDir, "conversation", localID)
	info, err := os.Stat(convPath)
	if err != nil {
		t.Fatalf("Failed to stat conversation: %v", err)
	}

	// Parse expected times
	expectedMtime, _ := time.Parse(time.RFC3339, "2024-01-16T14:20:00Z")

	// mtime should be the updated_at time
	actualMtime := info.ModTime()
	if !actualMtime.Equal(expectedMtime) {
		t.Errorf("Conversation mtime = %v, want %v (from updated_at)", actualMtime, expectedMtime)
	}
}

// TestTimestamps_APIMetadataMtimeDiffersFromCtime tests that mtime uses updated_at
// while ctime uses created_at when both are available.
func TestTimestamps_APIMetadataMtimeDiffersFromCtime(t *testing.T) {
	// Create a conversation with different created_at and updated_at
	convs := []shelley.Conversation{
		{
			ConversationID: "conv-mtime-ctime",
			Slug:           strPtr("mtime-ctime-test"),
			CreatedAt:      "2024-01-10T08:00:00Z",
			UpdatedAt:      "2024-01-20T16:30:00Z",
		},
	}
	server := mockConversationsServer(t, convs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-mtime-ctime-test")
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

	// Trigger adoption
	_, _ = ioutil.ReadDir(filepath.Join(tmpDir, "conversation"))

	localID := store.GetByShelleyID("conv-mtime-ctime")
	if localID == "" {
		t.Fatal("Conversation was not adopted")
	}

	// Use syscall.Stat to get all timestamps (ctime requires syscall)
	convPath := filepath.Join(tmpDir, "conversation", localID)
	var stat syscall.Stat_t
	if err := syscall.Stat(convPath, &stat); err != nil {
		t.Fatalf("syscall.Stat failed: %v", err)
	}

	expectedCtime, _ := time.Parse(time.RFC3339, "2024-01-10T08:00:00Z")
	expectedMtime, _ := time.Parse(time.RFC3339, "2024-01-20T16:30:00Z")

	actualCtime := time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec)
	actualMtime := time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec)

	if !actualCtime.Equal(expectedCtime) {
		t.Errorf("ctime = %v, want %v (from created_at)", actualCtime, expectedCtime)
	}
	if !actualMtime.Equal(expectedMtime) {
		t.Errorf("mtime = %v, want %v (from updated_at)", actualMtime, expectedMtime)
	}

	// ctime and mtime should be different
	if actualCtime.Equal(actualMtime) {
		t.Error("ctime and mtime should be different when created_at != updated_at")
	}
}

// TestTimestamps_APIMetadataFallbackToLocalTime tests that when API timestamps
// are not available, we fall back to local CreatedAt time.
func TestTimestamps_APIMetadataFallbackToLocalTime(t *testing.T) {
	// Mock server with no conversations (we'll use a locally cloned one)
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	startTime := shelleyFS.StartTime()

	// Wait a bit so clone time is clearly different
	time.Sleep(50 * time.Millisecond)

	// Clone locally (no API timestamps)
	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	cs := store.Get(convID)
	if cs == nil {
		t.Fatal("Conversation state not found")
	}
	localCreatedAt := cs.CreatedAt

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-fallback-test")
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

	convPath := filepath.Join(tmpDir, "conversation", convID)
	info, err := os.Stat(convPath)
	if err != nil {
		t.Fatalf("Failed to stat conversation: %v", err)
	}

	// mtime should be the local CreatedAt (since no API timestamps)
	actualMtime := info.ModTime()

	// Allow 1 second tolerance for timing
	diff := actualMtime.Sub(localCreatedAt)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("Conversation mtime %v should be close to local CreatedAt %v", actualMtime, localCreatedAt)
	}

	// Should be different from startTime
	if actualMtime.Sub(startTime) < 40*time.Millisecond {
		t.Errorf("Conversation mtime should be after startTime (waited 50ms before clone)")
	}
}

func TestTimestamps_ConversationUpdatedAtUpdatesOnReadopt(t *testing.T) {
	// First adoption with older timestamps
	store := testStore(t)
	_, err := store.AdoptWithMetadata("conv-update-test", "slug", "2024-01-01T00:00:00Z", "2024-01-05T00:00:00Z", "", "")
	if err != nil {
		t.Fatalf("First adoption failed: %v", err)
	}

	// Re-adopt with newer updated_at
	_, err = store.AdoptWithMetadata("conv-update-test", "", "", "2024-01-10T00:00:00Z", "", "")
	if err != nil {
		t.Fatalf("Second adoption failed: %v", err)
	}

	localID := store.GetByShelleyID("conv-update-test")
	cs := store.Get(localID)

	// created_at should not change
	if cs.APICreatedAt != "2024-01-01T00:00:00Z" {
		t.Errorf("APICreatedAt should not change, got %q", cs.APICreatedAt)
	}

	// updated_at should be the newer value
	if cs.APIUpdatedAt != "2024-01-10T00:00:00Z" {
		t.Errorf("APIUpdatedAt should be updated to newer value, got %q", cs.APIUpdatedAt)
	}
}


// TestConversationAPITimestampFields tests that created_at and updated_at are exposed at the conversation root.
func TestConversationAPITimestampFields(t *testing.T) {
	convID := "test-timestamp-conv-id"
	convSlug := "test-timestamp-slug"
	convCreatedAt := "2024-01-15T10:30:00Z"
	convUpdatedAt := "2024-01-15T11:00:00Z"

	conv := shelley.Conversation{
		ConversationID: convID,
		Slug:           &convSlug,
		CreatedAt:      convCreatedAt,
		UpdatedAt:      convUpdatedAt,
	}
	rawDetail, _ := json.Marshal(conv)
	server := mockserver.New(mockserver.WithConversationRawDetail(conv, rawDetail))
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()
	store.SetCtl(localID, "model", "claude-3-opus")
	store.MarkCreated(localID, convID, convSlug)

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	convDir := filepath.Join(mountPoint, "conversation", localID)

	// Test created_at field
	data, err := ioutil.ReadFile(filepath.Join(convDir, "created_at"))
	if err != nil {
		t.Fatalf("Failed to read created_at: %v", err)
	}
	if string(data) != convCreatedAt+"\n" {
		t.Errorf("created_at: expected %q, got %q", convCreatedAt+"\n", string(data))
	}

	// Test updated_at field
	data, err = ioutil.ReadFile(filepath.Join(convDir, "updated_at"))
	if err != nil {
		t.Fatalf("Failed to read updated_at: %v", err)
	}
	if string(data) != convUpdatedAt+"\n" {
		t.Errorf("updated_at: expected %q, got %q", convUpdatedAt+"\n", string(data))
	}

	// Verify directory listing includes the timestamp fields
	entries, err := ioutil.ReadDir(convDir)
	if err != nil {
		t.Fatalf("Failed to read conversation dir: %v", err)
	}
	entryMap := make(map[string]bool)
	for _, e := range entries {
		entryMap[e.Name()] = true
	}
	if !entryMap["created_at"] {
		t.Error("created_at should be listed in directory")
	}
	if !entryMap["updated_at"] {
		t.Error("updated_at should be listed in directory")
	}
}

// TestConversationAPITimestampFields_UncreatedConversation tests that timestamp fields don't exist for uncreated conversations.
func TestConversationAPITimestampFields_UncreatedConversation(t *testing.T) {
	server := mockserver.New() // no conversations registered
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()
	// Don't mark as created - this is an uncreated local conversation
	store.SetCtl(localID, "model", "claude-3-haiku")

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	convDir := filepath.Join(mountPoint, "conversation", localID)

	// created_at should not exist for uncreated conversation
	_, err := os.Stat(filepath.Join(convDir, "created_at"))
	if err == nil {
		t.Error("created_at should not exist for uncreated conversation")
	}

	// updated_at should not exist for uncreated conversation
	_, err = os.Stat(filepath.Join(convDir, "updated_at"))
	if err == nil {
		t.Error("updated_at should not exist for uncreated conversation")
	}

	// Verify directory listing doesn't include timestamp fields
	entries, err := ioutil.ReadDir(convDir)
	if err != nil {
		t.Fatalf("Failed to read conversation dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() == "created_at" || e.Name() == "updated_at" {
			t.Errorf("%s should not be listed for uncreated conversation", e.Name())
		}
	}
}

func TestAdoptedConversation_ModelSymlink(t *testing.T) {
	// Test that conversations adopted from the server with a model field
	// get a working model symlink.
	modelName := "claude-sonnet-4-5"
	convs := []shelley.Conversation{
		{
			ConversationID: "conv-with-model",
			Slug:           strPtr("test-model-slug"),
			Model:          strPtr(modelName),
			CreatedAt:      "2024-06-01T10:00:00Z",
			UpdatedAt:      "2024-06-01T11:00:00Z",
		},
	}

	server := mockConversationsServer(t, convs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, 0)

	tmpDir := t.TempDir()
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

	// Trigger adoption by reading the conversation list directory
	entries, err := ioutil.ReadDir(filepath.Join(tmpDir, "conversation"))
	if err != nil {
		t.Fatalf("Failed to read conversation dir: %v", err)
	}

	// Find the local ID (the directory entry, not symlinks)
	var localID string
	for _, e := range entries {
		if e.IsDir() {
			localID = e.Name()
			break
		}
	}
	if localID == "" {
		t.Fatal("No conversation directory found after adoption")
	}

	// Check that the model symlink exists
	modelPath := filepath.Join(tmpDir, "conversation", localID, "model")
	info, err := os.Lstat(modelPath)
	if err != nil {
		t.Fatalf("Failed to lstat model symlink: %v", err)
	}

	// Verify it's a symlink
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("Expected symlink, got mode %v", info.Mode())
	}

	// Read the symlink target
	target, err := os.Readlink(modelPath)
	if err != nil {
		t.Fatalf("Failed to readlink: %v", err)
	}

	expectedTarget := "../../model/" + modelName
	if target != expectedTarget {
		t.Errorf("Expected target %q, got %q", expectedTarget, target)
	}
}

func TestAdoptedConversation_NoModel(t *testing.T) {
	// Test that conversations adopted without a model field don't have a model symlink.
	convs := []shelley.Conversation{
		{
			ConversationID: "conv-no-model",
			Slug:           strPtr("test-no-model-slug"),
			CreatedAt:      "2024-06-01T10:00:00Z",
			UpdatedAt:      "2024-06-01T11:00:00Z",
		},
	}

	server := mockConversationsServer(t, convs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, 0)

	tmpDir := t.TempDir()
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

	// Trigger adoption
	entries, err := ioutil.ReadDir(filepath.Join(tmpDir, "conversation"))
	if err != nil {
		t.Fatalf("Failed to read conversation dir: %v", err)
	}

	var localID string
	for _, e := range entries {
		if e.IsDir() {
			localID = e.Name()
			break
		}
	}
	if localID == "" {
		t.Fatal("No conversation directory found after adoption")
	}

	// Model symlink should NOT exist
	modelPath := filepath.Join(tmpDir, "conversation", localID, "model")
	_, err = os.Lstat(modelPath)
	if err == nil {
		t.Error("Expected model symlink to not exist when no model is set")
	} else if !os.IsNotExist(err) {
		t.Errorf("Expected ENOENT, got: %v", err)
	}
}

func TestContinueNode_NotPresentForUncreatedConversation(t *testing.T) {
	server := mockserver.New()
	defer server.Close()

	store := testStore(t)
	// Clone creates an uncreated conversation
	id, err := store.Clone()
	if err != nil {
		t.Fatal(err)
	}

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// "continue" should not exist for uncreated conversations
	_, err = os.Stat(filepath.Join(mountDir, "conversation", id, "continue"))
	if err == nil {
		t.Error("Expected 'continue' to not exist for uncreated conversation")
	} else if !os.IsNotExist(err) {
		t.Errorf("Expected ENOENT, got: %v", err)
	}
}

func TestContinueNode_PresentForCreatedConversation(t *testing.T) {
	conv := shelley.Conversation{ConversationID: "server-conv-1"}
	server := mockserver.New(
		mockserver.WithFullConversation(conv, nil),
	)
	defer server.Close()

	store := testStore(t)
	// Clone and mark created to simulate a conversation that exists on the backend
	id, err := store.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCreated(id, "server-conv-1", ""); err != nil {
		t.Fatal(err)
	}

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// "continue" should exist for created conversations
	info, err := os.Stat(filepath.Join(mountDir, "conversation", id, "continue"))
	if err != nil {
		t.Fatalf("Expected 'continue' to exist: %v", err)
	}
	if info.IsDir() {
		t.Error("Expected 'continue' to be a file, not a directory")
	}
	if info.Mode().Perm() != 0444 {
		t.Errorf("Expected mode 0444, got %o", info.Mode().Perm())
	}
}

func TestContinueNode_ReturnsNewConversationID(t *testing.T) {
	conv := shelley.Conversation{ConversationID: "server-conv-1"}
	server := mockserver.New(
		mockserver.WithFullConversation(conv, nil),
	)
	defer server.Close()

	store := testStore(t)
	id, err := store.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCreated(id, "server-conv-1", ""); err != nil {
		t.Fatal(err)
	}

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Read "continue" to trigger the continue API call
	data, err := os.ReadFile(filepath.Join(mountDir, "conversation", id, "continue"))
	if err != nil {
		t.Fatalf("Failed to read continue: %v", err)
	}

	newID := strings.TrimSpace(string(data))
	if len(newID) != 8 {
		t.Fatalf("expected 8-character hex local ID, got %q", newID)
	}

	// The new conversation should be adopted in local state
	cs := store.Get(newID)
	if cs == nil {
		t.Fatal("expected new conversation to exist in state")
	}
	if !cs.Created {
		t.Error("expected new conversation to be marked as created")
	}
	if !strings.HasPrefix(cs.ShelleyConversationID, "continued-server-conv-1-") {
		t.Errorf("expected server ID to start with 'continued-server-conv-1-', got %q", cs.ShelleyConversationID)
	}

	// The new conversation directory should be accessible
	_, err = os.Stat(filepath.Join(mountDir, "conversation", newID))
	if err != nil {
		t.Fatalf("Expected new conversation directory to exist: %v", err)
	}
}

func TestContinueNode_UniqueIDs(t *testing.T) {
	conv := shelley.Conversation{ConversationID: "server-conv-1"}
	server := mockserver.New(
		mockserver.WithFullConversation(conv, nil),
	)
	defer server.Close()

	store := testStore(t)
	id, err := store.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCreated(id, "server-conv-1", ""); err != nil {
		t.Fatal(err)
	}

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Read continue twice, should get different IDs
	data1, err := os.ReadFile(filepath.Join(mountDir, "conversation", id, "continue"))
	if err != nil {
		t.Fatalf("First continue read failed: %v", err)
	}
	data2, err := os.ReadFile(filepath.Join(mountDir, "conversation", id, "continue"))
	if err != nil {
		t.Fatalf("Second continue read failed: %v", err)
	}

	id1 := strings.TrimSpace(string(data1))
	id2 := strings.TrimSpace(string(data2))
	if id1 == id2 {
		t.Errorf("expected unique IDs, both are %q", id1)
	}
}

func TestContinueNode_InReaddir(t *testing.T) {
	conv := shelley.Conversation{ConversationID: "server-conv-1"}
	server := mockserver.New(
		mockserver.WithFullConversation(conv, nil),
	)
	defer server.Close()

	store := testStore(t)
	id, err := store.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCreated(id, "server-conv-1", ""); err != nil {
		t.Fatal(err)
	}

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	entries, err := os.ReadDir(filepath.Join(mountDir, "conversation", id))
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.Name() == "continue" {
			found = true
			if e.IsDir() {
				t.Error("Expected 'continue' to be a file, not a directory")
			}
			break
		}
	}
	if !found {
		t.Error("Expected 'continue' in directory listing")
	}
}

func TestContinueNode_ServerError(t *testing.T) {
	conv := shelley.Conversation{ConversationID: "server-conv-1"}
	// Use a custom continue handler that returns an error
	server := mockserver.New(
		mockserver.WithFullConversation(conv, nil),
		mockserver.WithContinueHandler(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal error"))
		}),
	)
	defer server.Close()

	store := testStore(t)
	id, err := store.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCreated(id, "server-conv-1", ""); err != nil {
		t.Fatal(err)
	}

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Reading continue should fail when server returns an error
	_, err = os.ReadFile(filepath.Join(mountDir, "conversation", id, "continue"))
	if err == nil {
		t.Error("Expected error when server returns 500")
	}
}

func TestConversationListNode_Rmdir_CreatedConversation(t *testing.T) {
	conv := shelley.Conversation{ConversationID: "server-conv-1"}
	server := mockserver.New(
		mockserver.WithFullConversation(conv, nil),
	)
	defer server.Close()

	store := testStore(t)
	id, err := store.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCreated(id, "server-conv-1", "test-slug"); err != nil {
		t.Fatal(err)
	}

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	convPath := filepath.Join(mountDir, "conversation", id)

	// Verify the conversation directory exists
	if _, err := os.Stat(convPath); err != nil {
		t.Fatalf("expected conversation directory to exist: %v", err)
	}

	// rmdir should remove the conversation
	if err := syscall.Rmdir(convPath); err != nil {
		t.Fatalf("Rmdir failed: %v", err)
	}

	// Verify conversation is gone from state
	if store.Get(id) != nil {
		t.Error("expected conversation to be removed from state")
	}
}

func TestConversationListNode_Rmdir_UncreatedConversation(t *testing.T) {
	server := mockserver.New()
	defer server.Close()

	store := testStore(t)
	id, err := store.Clone()
	if err != nil {
		t.Fatal(err)
	}

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	convPath := filepath.Join(mountDir, "conversation", id)

	// rmdir on uncreated conversation should work (local cleanup only)
	if err := syscall.Rmdir(convPath); err != nil {
		t.Fatalf("Rmdir failed on uncreated conversation: %v", err)
	}

	// Verify conversation is gone from state
	if store.Get(id) != nil {
		t.Error("expected conversation to be removed from state")
	}
}

func TestConversationListNode_Rmdir_NonexistentConversation(t *testing.T) {
	server := mockserver.New()
	defer server.Close()

	store := testStore(t)

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	convPath := filepath.Join(mountDir, "conversation", "nonexistent-id")

	// rmdir on nonexistent conversation should return ENOENT
	err := syscall.Rmdir(convPath)
	if err != syscall.ENOENT {
		t.Errorf("expected ENOENT, got %v", err)
	}
}

func TestConversationListNode_Rmdir_ServerError(t *testing.T) {
	// Server that returns 500 for delete requests
	conv := shelley.Conversation{ConversationID: "server-conv-err"}
	server := mockserver.New(
		mockserver.WithFullConversation(conv, nil),
		mockserver.WithErrorMode(0), // not in error mode by default
	)
	defer server.Close()

	store := testStore(t)
	id, err := store.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCreated(id, "server-conv-err", ""); err != nil {
		t.Fatal(err)
	}

	// Close the server to simulate network errors
	server.Close()

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	convPath := filepath.Join(mountDir, "conversation", id)

	// rmdir should fail with EIO when server is down
	err = syscall.Rmdir(convPath)
	if err == nil {
		t.Error("expected error when server is down")
	}

	// Conversation should still be in state since server delete failed
	if store.Get(id) == nil {
		t.Error("conversation should still exist in state after server error")
	}
}

func TestConversationListNode_Rmdir_ConversationDisappearsFromReaddir(t *testing.T) {
	conv := shelley.Conversation{ConversationID: "server-conv-rmdir"}
	server := mockserver.New(
		mockserver.WithFullConversation(conv, nil),
	)
	defer server.Close()

	store := testStore(t)
	id, err := store.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCreated(id, "server-conv-rmdir", ""); err != nil {
		t.Fatal(err)
	}

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	convDir := filepath.Join(mountDir, "conversation")

	// Before delete: should appear in listing
	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.Name() == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected conversation in listing before delete")
	}

	// Delete the conversation
	convPath := filepath.Join(convDir, id)
	if err := syscall.Rmdir(convPath); err != nil {
		t.Fatalf("Rmdir failed: %v", err)
	}

	// After delete: should not appear in listing
	entries, err = os.ReadDir(convDir)
	if err != nil {
		t.Fatalf("ReadDir after delete failed: %v", err)
	}

	for _, e := range entries {
		if e.Name() == id {
			t.Error("expected conversation to disappear from listing after delete")
		}
	}
}

// --- Tests for ConversationLastDirNode ---

func TestLastDir_Lookup_MostRecent(t *testing.T) {
	// Create 3 conversations with different created_at timestamps
	conv1 := shelley.Conversation{
		ConversationID: "conv-oldest",
		CreatedAt:      "2024-01-01T00:00:00Z",
	}
	conv2 := shelley.Conversation{
		ConversationID: "conv-middle",
		CreatedAt:      "2024-06-01T00:00:00Z",
	}
	conv3 := shelley.Conversation{
		ConversationID: "conv-newest",
		CreatedAt:      "2024-12-01T00:00:00Z",
	}

	server := mockserver.New(
		mockserver.WithFullConversation(conv1, nil),
		mockserver.WithFullConversation(conv2, nil),
		mockserver.WithFullConversation(conv3, nil),
	)
	defer server.Close()

	store := testStore(t)
	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// last/1 should be a symlink pointing to the most recent conversation
	lastPath := filepath.Join(mountDir, "conversation", "last", "1")
	target, err := os.Readlink(lastPath)
	if err != nil {
		t.Fatalf("Readlink last/1 failed: %v", err)
	}

	// The target should be ../{localID} where localID maps to conv-newest
	localID := store.GetByShelleyID("conv-newest")
	if localID == "" {
		t.Fatal("conv-newest not adopted")
	}
	expected := "../" + localID
	if target != expected {
		t.Errorf("last/1 target = %q, want %q", target, expected)
	}
}

func TestLastDir_Lookup_SecondMostRecent(t *testing.T) {
	conv1 := shelley.Conversation{
		ConversationID: "conv-oldest",
		CreatedAt:      "2024-01-01T00:00:00Z",
	}
	conv2 := shelley.Conversation{
		ConversationID: "conv-middle",
		CreatedAt:      "2024-06-01T00:00:00Z",
	}
	conv3 := shelley.Conversation{
		ConversationID: "conv-newest",
		CreatedAt:      "2024-12-01T00:00:00Z",
	}

	server := mockserver.New(
		mockserver.WithFullConversation(conv1, nil),
		mockserver.WithFullConversation(conv2, nil),
		mockserver.WithFullConversation(conv3, nil),
	)
	defer server.Close()

	store := testStore(t)
	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// last/2 should point to the second most recent
	target, err := os.Readlink(filepath.Join(mountDir, "conversation", "last", "2"))
	if err != nil {
		t.Fatalf("Readlink last/2 failed: %v", err)
	}

	localID := store.GetByShelleyID("conv-middle")
	if localID == "" {
		t.Fatal("conv-middle not adopted")
	}
	if target != "../"+localID {
		t.Errorf("last/2 target = %q, want %q", target, "../"+localID)
	}

	// last/3 should point to the oldest
	target, err = os.Readlink(filepath.Join(mountDir, "conversation", "last", "3"))
	if err != nil {
		t.Fatalf("Readlink last/3 failed: %v", err)
	}

	localID = store.GetByShelleyID("conv-oldest")
	if localID == "" {
		t.Fatal("conv-oldest not adopted")
	}
	if target != "../"+localID {
		t.Errorf("last/3 target = %q, want %q", target, "../"+localID)
	}
}

func TestLastDir_Lookup_OutOfRange_ReturnsENOENT(t *testing.T) {
	conv := shelley.Conversation{
		ConversationID: "conv-1",
		CreatedAt:      "2024-01-01T00:00:00Z",
	}

	server := mockserver.New(
		mockserver.WithFullConversation(conv, nil),
	)
	defer server.Close()

	store := testStore(t)
	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// last/2 should return ENOENT when only 1 conversation exists
	_, err := os.Readlink(filepath.Join(mountDir, "conversation", "last", "2"))
	if err == nil {
		t.Error("Expected error for last/2 with only 1 conversation")
	} else if !os.IsNotExist(err) {
		t.Errorf("Expected ENOENT, got: %v", err)
	}
}

func TestLastDir_Lookup_Zero_ReturnsENOENT(t *testing.T) {
	conv := shelley.Conversation{
		ConversationID: "conv-1",
		CreatedAt:      "2024-01-01T00:00:00Z",
	}

	server := mockserver.New(
		mockserver.WithFullConversation(conv, nil),
	)
	defer server.Close()

	store := testStore(t)
	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// last/0 is invalid (1-indexed)
	_, err := os.Readlink(filepath.Join(mountDir, "conversation", "last", "0"))
	if err == nil {
		t.Error("Expected error for last/0")
	} else if !os.IsNotExist(err) {
		t.Errorf("Expected ENOENT, got: %v", err)
	}
}

func TestLastDir_Lookup_NonNumeric_ReturnsENOENT(t *testing.T) {
	conv := shelley.Conversation{
		ConversationID: "conv-1",
		CreatedAt:      "2024-01-01T00:00:00Z",
	}

	server := mockserver.New(
		mockserver.WithFullConversation(conv, nil),
	)
	defer server.Close()

	store := testStore(t)
	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// last/abc is invalid
	_, err := os.Readlink(filepath.Join(mountDir, "conversation", "last", "abc"))
	if err == nil {
		t.Error("Expected error for last/abc")
	} else if !os.IsNotExist(err) {
		t.Errorf("Expected ENOENT, got: %v", err)
	}
}

func TestLastDir_Readdir_ListsEntries(t *testing.T) {
	conv1 := shelley.Conversation{
		ConversationID: "conv-a",
		CreatedAt:      "2024-01-01T00:00:00Z",
	}
	conv2 := shelley.Conversation{
		ConversationID: "conv-b",
		CreatedAt:      "2024-06-01T00:00:00Z",
	}
	conv3 := shelley.Conversation{
		ConversationID: "conv-c",
		CreatedAt:      "2024-12-01T00:00:00Z",
	}

	server := mockserver.New(
		mockserver.WithFullConversation(conv1, nil),
		mockserver.WithFullConversation(conv2, nil),
		mockserver.WithFullConversation(conv3, nil),
	)
	defer server.Close()

	store := testStore(t)
	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	entries, err := os.ReadDir(filepath.Join(mountDir, "conversation", "last"))
	if err != nil {
		t.Fatalf("ReadDir last/ failed: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("Expected 3 entries, got %d", len(entries))
	}

	// Entries should be "1", "2", "3"
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name()] = true
	}
	for _, expected := range []string{"1", "2", "3"} {
		if !names[expected] {
			t.Errorf("Expected entry %q in readdir", expected)
		}
	}
}

func TestLastDir_Readdir_Empty(t *testing.T) {
	// No conversations on the server
	server := mockserver.New()
	defer server.Close()

	store := testStore(t)
	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	entries, err := os.ReadDir(filepath.Join(mountDir, "conversation", "last"))
	if err != nil {
		t.Fatalf("ReadDir last/ failed: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("Expected 0 entries for empty server, got %d", len(entries))
	}
}

func TestLastDir_AppearsInConversationListReaddir(t *testing.T) {
	server := mockserver.New()
	defer server.Close()

	store := testStore(t)
	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	entries, err := os.ReadDir(filepath.Join(mountDir, "conversation"))
	if err != nil {
		t.Fatalf("ReadDir conversation/ failed: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.Name() == "last" {
			found = true
			if !e.IsDir() {
				t.Error("Expected 'last' to be a directory")
			}
			break
		}
	}
	if !found {
		t.Error("Expected 'last' in conversation/ readdir")
	}
}

func TestLastDir_Stat_IsDirectory(t *testing.T) {
	server := mockserver.New()
	defer server.Close()

	store := testStore(t)
	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	info, err := os.Stat(filepath.Join(mountDir, "conversation", "last"))
	if err != nil {
		t.Fatalf("Stat conversation/last failed: %v", err)
	}
	if !info.IsDir() {
		t.Error("Expected last to be a directory")
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("Expected mode 0755, got %o", info.Mode().Perm())
	}
}

func TestLastDir_SymlinkResolvesToConversation(t *testing.T) {
	conv := shelley.Conversation{
		ConversationID: "conv-resolve",
		CreatedAt:      "2024-01-15T10:30:00Z",
	}

	server := mockserver.New(
		mockserver.WithFullConversation(conv, nil),
	)
	defer server.Close()

	store := testStore(t)
	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Stat (follow symlink) should resolve to the conversation directory
	info, err := os.Stat(filepath.Join(mountDir, "conversation", "last", "1"))
	if err != nil {
		t.Fatalf("Stat last/1 failed: %v", err)
	}
	if !info.IsDir() {
		t.Error("Expected last/1 to resolve to a directory")
	}

	// Should be able to read files inside the resolved conversation
	data, err := os.ReadFile(filepath.Join(mountDir, "conversation", "last", "1", "fuse_id"))
	if err != nil {
		t.Fatalf("Failed to read fuse_id through last/1: %v", err)
	}

	localID := store.GetByShelleyID("conv-resolve")
	if localID == "" {
		t.Fatal("conv-resolve not adopted")
	}
	if strings.TrimSpace(string(data)) != localID {
		t.Errorf("fuse_id = %q, want %q", strings.TrimSpace(string(data)), localID)
	}
}
func TestTimestamps_NestedQueryDirsUseConversationTime(t *testing.T) {
	// Test that nested query directories (since/user/) use conversation time
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Wait a bit so we can distinguish times
	time.Sleep(50 * time.Millisecond)

	// Clone a conversation
	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	cs := store.Get(convID)
	convTime := cs.CreatedAt

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-nested-query-timestamp-test")
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

	// Test since/user directory (nested QueryDirNode)
	t.Run("SinceUserDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID, "messages", "since", "user"))
		if err != nil {
			t.Fatalf("Failed to stat since/user: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("since/user mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
	})

}

func TestQueryResultDirNode_LastN(t *testing.T) {
	convID := "test-conv-last-n"
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi there!")},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr("How are you?")},
		{MessageID: "m4", ConversationID: convID, SequenceID: 4, Type: "shelley", LLMData: strPtr("I'm great!")},
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-last-n-test")
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

	// Test last/2 - should contain symlinks to the last 2 messages
	last2Dir := filepath.Join(tmpDir, "conversation", localID, "messages", "last", "2")
	info, err := os.Stat(last2Dir)
	if err != nil {
		t.Fatalf("Failed to stat last/2: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("last/2 should be a directory")
	}

	entries, err := ioutil.ReadDir(last2Dir)
	if err != nil {
		t.Fatalf("Failed to read last/2: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Expected 2 entries in last/2, got %d", len(entries))
	}

	// Verify the entries are ordinal symlinks: 0, 1 (0 = oldest, 1 = newest of last 2)
	// last/2 should have entries "0" and "1"
	// 0 → ../../2-user (seqID 3, 0-indexed as 2)
	// 1 → ../../3-agent (seqID 4, 0-indexed as 3)
	expectedOrdinals := []string{"0", "1"}
	expectedTargets := []string{"../../2-user", "../../3-agent"}
	for i, e := range entries {
		if e.Mode()&os.ModeSymlink == 0 {
			t.Errorf("Expected symlink, got %s with mode %v", e.Name(), e.Mode())
		}
		if e.Name() != expectedOrdinals[i] {
			t.Errorf("Expected name %q, got %q", expectedOrdinals[i], e.Name())
		}
	}

	// Verify symlink targets are correct (../../{message-dir})
	for i, ordinal := range expectedOrdinals {
		target, err := os.Readlink(filepath.Join(last2Dir, ordinal))
		if err != nil {
			t.Errorf("Failed to readlink %s: %v", ordinal, err)
			continue
		}
		if target != expectedTargets[i] {
			t.Errorf("Symlink %s target = %q, want %q", ordinal, target, expectedTargets[i])
		}
	}

	// Verify we can read through the symlinks
	data, err := ioutil.ReadFile(filepath.Join(last2Dir, "0", "type"))
	if err != nil {
		t.Fatalf("Failed to read type through symlink 0: %v", err)
	}
	if strings.TrimSpace(string(data)) != "user" {
		t.Errorf("Expected type=user for ordinal 0, got %q", string(data))
	}

	data, err = ioutil.ReadFile(filepath.Join(last2Dir, "1", "type"))
	if err != nil {
		t.Fatalf("Failed to read type through symlink 1: %v", err)
	}
	if strings.TrimSpace(string(data)) != "shelley" {
		t.Errorf("Expected type=shelley for ordinal 1, got %q", string(data))
	}

	// Test last/3 - should contain 3 messages with ordinals 0, 1, 2
	last3Dir := filepath.Join(tmpDir, "conversation", localID, "messages", "last", "3")
	entries, err = ioutil.ReadDir(last3Dir)
	if err != nil {
		t.Fatalf("Failed to read last/3: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("Expected 3 entries in last/3, got %d", len(entries))
	}
	// Verify ordinals
	for i, e := range entries {
		expected := strconv.Itoa(i)
		if e.Name() != expected {
			t.Errorf("Expected ordinal %q in last/3, got %q", expected, e.Name())
		}
	}
}

// TestQueryResultDirNode_SincePersonN verifies that since/{person}/{N} returns
// a directory containing symlinks to messages after the Nth occurrence of that person.
func TestQueryResultDirNode_SincePersonN(t *testing.T) {
	convID := "test-conv-since-n"
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("First")},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr("Response 1")},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr("Second")},
		{MessageID: "m4", ConversationID: convID, SequenceID: 4, Type: "shelley", LLMData: strPtr("Response 2")},
		{MessageID: "m5", ConversationID: convID, SequenceID: 5, Type: "user", UserData: strPtr("Third")},
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-since-n-test")
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

	// Test since/user/2 - messages after the 2nd-to-last user message (2-user)
	// Should include: 3-agent, 4-user
	since2Dir := filepath.Join(tmpDir, "conversation", localID, "messages", "since", "user", "2")
	info, err := os.Stat(since2Dir)
	if err != nil {
		t.Fatalf("Failed to stat since/user/2: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("since/user/2 should be a directory")
	}

	entries, err := ioutil.ReadDir(since2Dir)
	if err != nil {
		t.Fatalf("Failed to read since/user/2: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Expected 2 entries in since/user/2, got %d", len(entries))
	}

	expectedNames := []string{"3-agent", "4-user"}
	for i, e := range entries {
		if e.Mode()&os.ModeSymlink == 0 {
			t.Errorf("Expected symlink, got %s with mode %v", e.Name(), e.Mode())
		}
		if e.Name() != expectedNames[i] {
			t.Errorf("Expected name %q, got %q", expectedNames[i], e.Name())
		}
	}

	// Verify symlink targets are correct (../../../{message-dir})
	for _, name := range expectedNames {
		target, err := os.Readlink(filepath.Join(since2Dir, name))
		if err != nil {
			t.Errorf("Failed to readlink %s: %v", name, err)
			continue
		}
		expectedTarget := "../../../" + name
		if target != expectedTarget {
			t.Errorf("Symlink %s target = %q, want %q", name, target, expectedTarget)
		}
	}

	// Test since/user/1 - messages after the last user message (4-user)
	// Should be empty since it's the last message
	since1Dir := filepath.Join(tmpDir, "conversation", localID, "messages", "since", "user", "1")
	entries, err = ioutil.ReadDir(since1Dir)
	if err != nil {
		t.Fatalf("Failed to read since/user/1: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("Expected 0 entries in since/user/1, got %d", len(entries))
	}

	// Verify we can read through symlinks
	data, err := ioutil.ReadFile(filepath.Join(since2Dir, "3-agent", "type"))
	if err != nil {
		t.Fatalf("Failed to read type through symlink: %v", err)
	}
	if strings.TrimSpace(string(data)) != "shelley" {
		t.Errorf("Expected type=shelley, got %q", string(data))
	}
}

func TestSinceDirLsDoesNotMakeExcessiveAPICalls(t *testing.T) {
	convID := "conv-since-perf"
	numMessages := 100

	// Build a conversation: 1 user message, then (numMessages-1) agent replies
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr(`{"Content":[{"Type":2,"Text":"Hello"}]}`)},
	}
	for i := 2; i <= numMessages; i++ {
		msgs = append(msgs, shelley.Message{
			MessageID:      fmt.Sprintf("m%d", i),
			ConversationID: convID,
			SequenceID:     i,
			Type:           "shelley",
			LLMData:        strPtr(fmt.Sprintf(`{"Content":[{"Type":2,"Text":"Reply %d"}]}`, i)),
		})
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	// Use CachingClient like production
	baseClient := shelley.NewClient(server.URL)
	cachingClient := shelley.NewCachingClient(baseClient, 5*time.Second)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(cachingClient, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-since-perf-test")
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

	// Reset counter after mount
	server.ResetFetchCount()

	// ls -l since/user/1/ — should list (numMessages-1) agent messages
	sincePath := filepath.Join(tmpDir, "conversation", localID, "messages", "since", "user", "1")
	entries, err := ioutil.ReadDir(sincePath)
	if err != nil {
		t.Fatalf("ReadDir on since/user/1/ failed: %v", err)
	}
	expected := numMessages - 1
	if len(entries) != expected {
		t.Fatalf("Expected %d entries in since/user/1/, got %d", expected, len(entries))
	}

	// The key assertion: API calls should be bounded, not O(N).
	// With proper caching, we expect at most a small constant number of fetches
	// (1 for the conversation data, possibly a couple more for cache misses).
	// Without the fix, this would be ~100+ fetches.
	fetches := server.FetchCount()
	t.Logf("GetConversation calls for ls -l since/user/1/ with %d entries: %d", expected, fetches)
	if fetches > 5 {
		t.Errorf("Too many GetConversation calls: %d (expected <= 5 with caching)", fetches)
	}
}

// TestSinceDirPerformance verifies that ReadDir on messages/ and
// since/{person}/{N}/ complete in reasonable absolute time for a 100-message
// conversation. This replaced a ratio-based test that became flaky after
// immutable-node caching made messages/ dramatically faster (kernel-cached
// Lstat results) while since/ still does per-entry FUSE roundtrips. Both
// paths are now orders of magnitude faster than the original O(N²) bug
// (~6s for messages/, ~35s for since/ with 150 messages). The absolute
// thresholds here are generous guards against gross regressions, not
// precision benchmarks.
func TestSinceDirPerformance(t *testing.T) {
	convID := "conv-perf-regression"
	numMessages := 100

	// Build a conversation: 1 user message followed by (numMessages-1) agent replies.
	// This ensures since/user/1/ returns (numMessages-1) entries.
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr(`{"Content":[{"Type":2,"Text":"Hello"}]}`)},
	}
	for i := 2; i <= numMessages; i++ {
		msgs = append(msgs, shelley.Message{
			MessageID:      fmt.Sprintf("m%d", i),
			ConversationID: convID,
			SequenceID:     i,
			Type:           "shelley",
			LLMData:        strPtr(fmt.Sprintf(`{"Content":[{"Type":2,"Text":"Reply %d"}]}`, i)),
		})
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	baseClient := shelley.NewClient(server.URL)
	cachingClient := shelley.NewCachingClient(baseClient, 5*time.Second)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(cachingClient, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-perf-regression")
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

	messagesPath := filepath.Join(tmpDir, "conversation", localID, "messages")
	sincePath := filepath.Join(tmpDir, "conversation", localID, "messages", "since", "user", "1")

	// Warm up caches
	ioutil.ReadDir(messagesPath)
	ioutil.ReadDir(sincePath)

	const iterations = 5

	// Measure messages/ ReadDir
	var messagesTotal time.Duration
	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, err := ioutil.ReadDir(messagesPath)
		if err != nil {
			t.Fatalf("ReadDir messages/ failed: %v", err)
		}
		messagesTotal += time.Since(start)
	}

	// Measure since/user/1/ ReadDir
	var sinceTotal time.Duration
	for i := 0; i < iterations; i++ {
		start := time.Now()
		entries, err := ioutil.ReadDir(sincePath)
		if err != nil {
			t.Fatalf("ReadDir since/user/1/ failed: %v", err)
		}
		if len(entries) != numMessages-1 {
			t.Fatalf("Expected %d entries, got %d", numMessages-1, len(entries))
		}
		sinceTotal += time.Since(start)
	}

	messagesAvg := messagesTotal / time.Duration(iterations)
	sinceAvg := sinceTotal / time.Duration(iterations)

	t.Logf("messages/ avg: %v, since/user/1/ avg: %v", messagesAvg, sinceAvg)

	// Absolute thresholds: guard against gross regressions. The original bug
	// had ~6s for messages/ and ~35s for since/ with 150 messages. Current
	// performance is ~1-2ms and ~7-15ms respectively. A 500ms threshold per
	// path gives ~30x headroom and will only trip on a real regression.
	const maxAcceptable = 500 * time.Millisecond
	if messagesAvg > maxAcceptable {
		t.Errorf("messages/ avg %v exceeds %v threshold", messagesAvg, maxAcceptable)
	}
	if sinceAvg > maxAcceptable {
		t.Errorf("since/user/1/ avg %v exceeds %v threshold", sinceAvg, maxAcceptable)
	}
}
