package fuse

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

func skipIfNoFusermount(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("fusermount"); err != nil {
		t.Skip("fusermount not found, skipping FUSE test")
	}
}

func skipIfNoShelley(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/usr/local/bin/shelley"); os.IsNotExist(err) {
		t.Skip("/usr/local/bin/shelley not found, skipping integration test")
	}
}

func startShelleyServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")

	cmd := exec.Command("/usr/local/bin/shelley",
		"-db", dbPath, "-predictable-only", "serve",
		"-port", fmt.Sprintf("%d", port),
		"-require-header", "X-Exedev-Userid")
	cmd.Env = append(os.Environ(),
		"FIREWORKS_API_KEY=", "ANTHROPIC_API_KEY=", "OPENAI_API_KEY=")

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start shelley server: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	serverURL := fmt.Sprintf("http://localhost:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		if resp, err := client.Get(serverURL); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return serverURL
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Shelley server failed to start on port %d", port)
	return ""
}

func mountTestFS(t *testing.T, serverURL string) string {
	mp, _ := mountTestFSWithStore(t, serverURL, time.Hour)
	return mp
}

func mountTestFSWithStore(t *testing.T, serverURL string, cloneTimeout time.Duration) (string, *state.Store) {
	t.Helper()
	tmpDir := t.TempDir()
	mountPoint := filepath.Join(tmpDir, "mount")
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		t.Fatalf("Failed to create mount point: %v", err)
	}

	client := shelley.NewClient(serverURL)
	store, err := state.NewStore(filepath.Join(tmpDir, "state.json"))
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	shelleyFS := NewFS(client, store, cloneTimeout)
	opts := &fs.Options{}
	zero := time.Duration(0)
	opts.EntryTimeout, opts.AttrTimeout, opts.NegativeTimeout = &zero, &zero, &zero

	fssrv, err := fs.Mount(mountPoint, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() { fssrv.Unmount(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			exec.Command("fusermount", "-u", mountPoint).Run()
			<-done
		}
	})
	return mountPoint, store
}

// createConversation is a helper that clones, configures, and creates a conversation.
// Returns (localID, serverID).
func createConversation(t *testing.T, mountPoint, message string) (string, string) {
	t.Helper()
	data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}
	localID := strings.TrimSpace(string(data))

	if err := ioutil.WriteFile(filepath.Join(mountPoint, "conversation", localID, "ctl"),
		[]byte("model=predictable"), 0644); err != nil {
		t.Fatalf("Failed to write ctl: %v", err)
	}
	if err := ioutil.WriteFile(filepath.Join(mountPoint, "conversation", localID, "send"),
		[]byte(message), 0644); err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}

	data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", localID, "id"))
	if err != nil {
		t.Fatalf("Failed to read server ID: %v", err)
	}
	return localID, strings.TrimSpace(string(data))
}

func entryNames(entries []os.FileInfo) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}

// TestRootAndModels tests the root directory and models directory structure.
func TestRootAndModels(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// Root directory
	entries, err := ioutil.ReadDir(mountPoint)
	if err != nil {
		t.Fatalf("Failed to read root directory: %v", err)
	}
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name()] = true
	}
	for _, expected := range []string{"models", "new", "conversation"} {
		if !names[expected] {
			t.Errorf("Expected entry %q in root directory", expected)
		}
	}

	// Models directory
	entries, err = ioutil.ReadDir(filepath.Join(mountPoint, "models"))
	if err != nil {
		t.Fatalf("Failed to read models directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("Expected at least one model")
	}

	var foundPredictable bool
	for _, e := range entries {
		if e.Name() == "predictable" && e.IsDir() {
			foundPredictable = true
		}
	}
	if !foundPredictable {
		t.Error("Expected 'predictable' model directory")
	}

	// Model fields
	idData, err := ioutil.ReadFile(filepath.Join(mountPoint, "models", "predictable", "id"))
	if err != nil {
		t.Fatalf("Failed to read models/predictable/id: %v", err)
	}
	if strings.TrimSpace(string(idData)) != "predictable" {
		t.Errorf("Expected id='predictable', got %q", string(idData))
	}

	// Check ready file exists (presence/absence semantics)
	readyPath := filepath.Join(mountPoint, "models", "predictable", "ready")
	if _, err := os.Stat(readyPath); err != nil {
		t.Errorf("Expected models/predictable/ready to exist: %v", err)
	}

	// Model directory listing
	entries, err = ioutil.ReadDir(filepath.Join(mountPoint, "models", "predictable"))
	if err != nil {
		t.Fatalf("Failed to read models/predictable: %v", err)
	}
	expectedFiles := map[string]bool{"id": false, "ready": false}
	for _, e := range entries {
		if _, ok := expectedFiles[e.Name()]; ok {
			expectedFiles[e.Name()] = true
		}
	}
	for name, found := range expectedFiles {
		if !found {
			t.Errorf("Expected file %q in models/predictable", name)
		}
	}
}

// TestCloneUnique verifies clone returns different IDs each time.
func TestCloneUnique(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	data1, _ := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
	data2, _ := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
	id1 := strings.TrimSpace(string(data1))
	id2 := strings.TrimSpace(string(data2))
	if id1 == id2 {
		t.Errorf("Expected different IDs from clone, got %q both times", id1)
	}
	if len(id1) != 8 || len(id2) != 8 {
		t.Errorf("Expected 8-char IDs, got %q and %q", id1, id2)
	}
}

// TestConversationFlow tests the full clone → configure → create → read cycle.
func TestConversationFlow(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// Clone
	data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}
	convID := strings.TrimSpace(string(data))
	if len(convID) != 8 {
		t.Fatalf("Expected 8-char hex ID, got %q", convID)
	}

	// Write and read ctl
	ctlPath := filepath.Join(mountPoint, "conversation", convID, "ctl")
	if err := ioutil.WriteFile(ctlPath, []byte("model=predictable cwd=/tmp"), 0644); err != nil {
		t.Fatalf("Failed to write ctl: %v", err)
	}
	data, err = ioutil.ReadFile(ctlPath)
	if err != nil {
		t.Fatalf("Failed to read ctl: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "model=predictable") || !strings.Contains(content, "cwd=/tmp") {
		t.Errorf("ctl content mismatch: %q", content)
	}

	// Status before create - "created" file should NOT exist (presence/absence semantics)
	createdPath := filepath.Join(mountPoint, "conversation", convID, "created")
	if _, err := os.Stat(createdPath); !os.IsNotExist(err) {
		t.Errorf("Expected 'created' to not exist before first message, got: %v", err)
	}

	// id should not exist before create
	if _, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "id")); err == nil {
		t.Error("Expected error reading id before conversation created")
	}

	// Write first message
	if err := ioutil.WriteFile(filepath.Join(mountPoint, "conversation", convID, "send"),
		[]byte("Hello shelley, this is a test"), 0644); err != nil {
		t.Fatalf("Failed to write first message: %v", err)
	}

	// Status after create - "created" file should exist (presence/absence semantics)
	if _, err := os.Stat(createdPath); err != nil {
		t.Errorf("Expected 'created' to exist after first message: %v", err)
	}

	// Read server ID
	data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "id"))
	if err != nil {
		t.Fatalf("Failed to read id: %v", err)
	}
	shelleyConvID := strings.TrimSpace(string(data))
	if shelleyConvID == "" {
		t.Error("Expected non-empty Shelley conversation ID")
	}

	// ctl should be read-only after creation
	if err := ioutil.WriteFile(ctlPath, []byte("model=other"), 0644); err == nil {
		t.Error("Expected error writing to ctl after conversation created")
	}

	// Read all.json
	data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "messages", "all.json"))
	if err != nil {
		t.Fatalf("Failed to read all.json: %v", err)
	}
	var msgs []shelley.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		t.Fatalf("Failed to parse all.json: %v", err)
	}
	if len(msgs) == 0 {
		t.Error("Expected at least one message")
	}

	// Read all.md
	data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "messages", "all.md"))
	if err != nil {
		t.Fatalf("Failed to read all.md: %v", err)
	}
	if !strings.Contains(string(data), "##") {
		t.Error("Expected markdown headers in all.md")
	}

	// Read specific message via named directory
	// Note: The first message is typically a system message (0-system/),
	// so the user message is at index 1 (1-user/). Directories are 0-indexed.
	userMsgDir := filepath.Join(mountPoint, "conversation", convID, "messages", "1-user")
	info, err := os.Stat(userMsgDir)
	if err != nil {
		t.Fatalf("Failed to stat 1-user: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("Expected 1-user to be a directory")
	}

	// Verify the message directory contents
	data, err = ioutil.ReadFile(filepath.Join(userMsgDir, "type"))
	if err != nil {
		t.Fatalf("Failed to read 1-user/type: %v", err)
	}
	if strings.TrimSpace(string(data)) != "user" {
		t.Errorf("Expected type=user, got %q", string(data))
	}

	// Verify content.md exists
	data, err = ioutil.ReadFile(filepath.Join(userMsgDir, "content.md"))
	if err != nil {
		t.Fatalf("Failed to read 1-user/content.md: %v", err)
	}
	if !strings.Contains(string(data), "##") {
		t.Error("Expected markdown headers in content.md")
	}

	// Read last/2 - now a directory with symlinks to message directories
	last2Dir := filepath.Join(mountPoint, "conversation", convID, "messages", "last", "2")
	last2Entries, err := ioutil.ReadDir(last2Dir)
	if err != nil {
		t.Fatalf("Failed to read last/2: %v", err)
	}
	if len(last2Entries) != 2 {
		t.Errorf("Expected 2 entries in last/2, got %d", len(last2Entries))
	}
	// Verify the entries are symlinks to message directories
	for _, e := range last2Entries {
		if e.Mode()&os.ModeSymlink == 0 {
			t.Errorf("Expected symlink in last/2, got %s with mode %v", e.Name(), e.Mode())
		}
		// Verify we can read through the symlink
		data, err = ioutil.ReadFile(filepath.Join(last2Dir, e.Name(), "type"))
		if err != nil {
			t.Errorf("Failed to read type through symlink: %v", err)
		}
	}

	// Read since/user/1 - now a directory with symlinks
	// Since the user's message is the last one in this conversation, we expect an empty directory
	since1Dir := filepath.Join(mountPoint, "conversation", convID, "messages", "since", "user", "1")
	since1Entries, err := ioutil.ReadDir(since1Dir)
	if err != nil {
		t.Fatalf("Failed to read since/user/1: %v", err)
	}
	// With the corrected behavior, since/user/1 excludes the reference message
	// Since the last user message is the final message, result should be empty
	if len(since1Entries) != 0 {
		t.Errorf("Expected 0 entries in since/user/1 (last user msg is final), got %d", len(since1Entries))
	}


	// Verify conversation in listing
	entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
	if err != nil {
		t.Fatalf("Failed to read conversation dir: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Name() == convID && e.IsDir() {
			found = true
		}
	}
	if !found {
		t.Errorf("Conversation %s not found in listing", convID)
	}
}

// TestConversationDirectoryStructure tests the directory contents and status fields.
func TestConversationDirectoryStructure(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)
	localID, serverID := createConversation(t, mountPoint, "Test message")

	entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation", localID))
	if err != nil {
		t.Fatalf("Failed to read conversation directory: %v", err)
	}

	// Required files for all conversations
	expectedFiles := map[string]bool{
		"ctl": false, "send": false, "fuse_id": false,
	}
	// Files that appear only when conversation is created on backend
	createdFiles := map[string]bool{
		"id": false, "created": false,
	}
	// API timestamp fields (may or may not be present depending on server)
	apiTimestampFiles := map[string]bool{"created_at": false, "updated_at": false}
	// Optional files (slug may not be set)
	optionalFiles := map[string]bool{"slug": false}
	expectedDirs := map[string]bool{"messages": false}

	for _, e := range entries {
		if _, ok := expectedFiles[e.Name()]; ok {
			expectedFiles[e.Name()] = true
		}
		if _, ok := createdFiles[e.Name()]; ok {
			createdFiles[e.Name()] = true
		}
		if _, ok := optionalFiles[e.Name()]; ok {
			optionalFiles[e.Name()] = true
		}
		if _, ok := apiTimestampFiles[e.Name()]; ok {
			apiTimestampFiles[e.Name()] = true
		}
		if _, ok := expectedDirs[e.Name()]; ok {
			expectedDirs[e.Name()] = true
		}
	}

	for name, found := range expectedFiles {
		if !found {
			t.Errorf("Expected file %q", name)
		}
	}
	for name, found := range createdFiles {
		if !found {
			t.Errorf("Expected file %q (for created conversation)", name)
		}
	}
	// optionalFiles and apiTimestampFiles are not checked - they may or may not be present
	_ = optionalFiles
	_ = apiTimestampFiles
	for name, found := range expectedDirs {
		if !found {
			t.Errorf("Expected directory %q", name)
		}
	}

	// Verify status field values
	convDir := filepath.Join(mountPoint, "conversation", localID)

	data, _ := ioutil.ReadFile(filepath.Join(convDir, "fuse_id"))
	if strings.TrimSpace(string(data)) != localID {
		t.Errorf("fuse_id mismatch")
	}

	data, _ = ioutil.ReadFile(filepath.Join(convDir, "id"))
	if strings.TrimSpace(string(data)) != serverID {
		t.Errorf("id mismatch")
	}

	// "created" file should exist (presence/absence semantics) with proper mtime
	createdPath := filepath.Join(convDir, "created")
	createdInfo, err := os.Stat(createdPath)
	if err != nil {
		t.Errorf("created file should exist: %v", err)
	} else {
		// Verify mtime is reasonable (within last hour)
		if time.Since(createdInfo.ModTime()) > time.Hour {
			t.Errorf("created mtime should be recent, got %v", createdInfo.ModTime())
		}
	}

	data, _ = ioutil.ReadFile(filepath.Join(convDir, "messages", "count"))
	if strings.TrimSpace(string(data)) == "0" {
		t.Errorf("messages/count should be > 0")
	}

	// Symlinks
	modelTarget, err := os.Readlink(filepath.Join(convDir, "model"))
	if err != nil || !strings.Contains(modelTarget, "predictable") {
		t.Errorf("model symlink incorrect: %v", err)
	}
}

// TestServerConversationAdoption tests that server conversations are adopted with local IDs.
func TestServerConversationAdoption(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)

	// Create conversation directly via API
	client := shelley.NewClient(serverURL)
	result, err := client.StartConversation("Hello from API", "predictable", t.TempDir())
	if err != nil {
		t.Fatalf("Failed to create server conversation: %v", err)
	}
	serverConvID := result.ConversationID

	// Mount fresh filesystem
	mountPoint := mountTestFS(t, serverURL)

	// List triggers adoption
	entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
	if err != nil {
		t.Fatalf("Failed to read conversation dir: %v", err)
	}

	var dirs, symlinks []os.FileInfo
	for _, e := range entries {
		if e.Mode()&os.ModeSymlink != 0 {
			symlinks = append(symlinks, e)
		} else if e.IsDir() {
			dirs = append(dirs, e)
		}
	}

	if len(dirs) != 1 {
		t.Fatalf("Expected 1 directory (local ID), got %d", len(dirs))
	}
	adoptedLocalID := dirs[0].Name()
	if len(adoptedLocalID) != 8 {
		t.Errorf("Expected 8-char local ID, got %q", adoptedLocalID)
	}

	// Server ID symlink should exist
	var foundServerID bool
	for _, s := range symlinks {
		if s.Name() == serverConvID {
			foundServerID = true
		}
	}
	if !foundServerID {
		t.Errorf("Expected symlink for server ID %q", serverConvID)
	}

	// Symlink target should be local ID
	target, err := os.Readlink(filepath.Join(mountPoint, "conversation", serverConvID))
	if err != nil {
		t.Fatalf("Readlink failed: %v", err)
	}
	if target != adoptedLocalID {
		t.Errorf("Symlink target = %q, want %q", target, adoptedLocalID)
	}

	// Can read via local ID
	data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", adoptedLocalID, "messages", "all.json"))
	if err != nil {
		t.Fatalf("Failed to read via local ID: %v", err)
	}
	var msgs []shelley.Message
	if err := json.Unmarshal(data, &msgs); err != nil || len(msgs) == 0 {
		t.Error("Expected messages")
	}

	// id file returns server ID
	data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", adoptedLocalID, "id"))
	if err != nil {
		t.Fatalf("Failed to read id: %v", err)
	}
	if strings.TrimSpace(string(data)) != serverConvID {
		t.Errorf("id mismatch")
	}

	// Listing should be stable (no duplicates, same local ID)
	entries2, _ := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
	var dirs2 []os.FileInfo
	for _, e := range entries2 {
		if e.IsDir() && e.Mode()&os.ModeSymlink == 0 {
			dirs2 = append(dirs2, e)
		}
	}
	if len(dirs2) != 1 || dirs2[0].Name() != adoptedLocalID {
		t.Errorf("Listing not stable: expected single dir %s, got %v", adoptedLocalID, entryNames(dirs2))
	}
}

// TestSymlinkAccess tests accessing conversations through symlinks.
func TestSymlinkAccess(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)
	localID, serverID := createConversation(t, mountPoint, "Hello symlink test")

	// Symlink appears in listing
	entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
	if err != nil {
		t.Fatalf("Failed to read conversation dir: %v", err)
	}
	var foundSymlink bool
	for _, e := range entries {
		if e.Name() == serverID && e.Mode()&os.ModeSymlink != 0 {
			foundSymlink = true
		}
	}
	if !foundSymlink {
		t.Errorf("Server ID symlink not found")
	}

	// Readlink returns local ID
	target, err := os.Readlink(filepath.Join(mountPoint, "conversation", serverID))
	if err != nil || target != localID {
		t.Errorf("Readlink: got %q, want %q", target, localID)
	}

	// Stat follows symlink
	info, err := os.Stat(filepath.Join(mountPoint, "conversation", serverID))
	if err != nil || !info.IsDir() {
		t.Error("Stat should follow symlink to directory")
	}

	// Lstat returns symlink
	info, err = os.Lstat(filepath.Join(mountPoint, "conversation", serverID))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("Lstat should return symlink")
	}

	// Read file through symlink
	data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", serverID, "messages", "all.json"))
	if err != nil {
		t.Fatalf("Failed to read through symlink: %v", err)
	}
	var msgs []map[string]interface{}
	if err := json.Unmarshal(data, &msgs); err != nil || len(msgs) == 0 {
		t.Error("Expected messages")
	}

	// Read status through symlink
	data, _ = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", serverID, "fuse_id"))
	if strings.TrimSpace(string(data)) != localID {
		t.Errorf("fuse_id via symlink mismatch")
	}

	// List dir through symlink
	entries, err = ioutil.ReadDir(filepath.Join(mountPoint, "conversation", serverID))
	if err != nil {
		t.Fatalf("Failed to list through symlink: %v", err)
	}
	expected := map[string]bool{"ctl": false, "send": false, "messages": false}
	for _, e := range entries {
		if _, ok := expected[e.Name()]; ok {
			expected[e.Name()] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("Expected %q through symlink", name)
		}
	}

	// Nested path through symlink - last/2 is now a directory
	last2Dir := filepath.Join(mountPoint, "conversation", serverID, "messages", "last", "2")
	last2Entries, err := ioutil.ReadDir(last2Dir)
	if err != nil {
		t.Fatalf("Failed to read last/2 through symlink: %v", err)
	}
	if len(last2Entries) != 2 {
		t.Errorf("Expected 2 entries in last/2, got %d", len(last2Entries))
	}

	// Both paths return same content
	dataViaLocal, _ := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", localID, "messages", "all.json"))
	dataViaServer, _ := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", serverID, "messages", "all.json"))
	if string(dataViaLocal) != string(dataViaServer) {
		t.Error("Content differs between local and server ID access")
	}
}

// TestSymlinkEdgeCases tests edge cases in symlink handling.
func TestSymlinkEdgeCases(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// Nonexistent ID returns ENOENT
	_, err := os.Stat(filepath.Join(mountPoint, "conversation", "nonexistent-id"))
	if !os.IsNotExist(err) {
		t.Errorf("Expected ENOENT for nonexistent ID, got %v", err)
	}

	// Local ID is directory, not symlink
	data, _ := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
	localID := strings.TrimSpace(string(data))
	info, err := os.Lstat(filepath.Join(mountPoint, "conversation", localID))
	if err != nil {
		t.Fatalf("Lstat failed: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Error("Local ID should be directory, not symlink")
	}

	// Multiple conversations have distinct symlinks
	localID1, serverID1 := createConversation(t, mountPoint, "Message 1")
	localID2, serverID2 := createConversation(t, mountPoint, "Message 2")

	entries, _ := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
	var dirs, symlinks int
	for _, e := range entries {
		if e.Mode()&os.ModeSymlink != 0 {
			symlinks++
		} else if e.IsDir() {
			dirs++
		}
	}
	if dirs < 2 {
		t.Errorf("Expected at least 2 directories, got %d", dirs)
	}
	if symlinks < 2 {
		t.Errorf("Expected at least 2 symlinks, got %d", symlinks)
	}

	// Verify distinct
	if localID1 == localID2 || serverID1 == serverID2 {
		t.Error("IDs should be distinct")
	}
}

// TestUncreatedConversationStatus tests status fields for uncreated conversations.
func TestUncreatedConversationStatus(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}
	uncreatedID := strings.TrimSpace(string(data))

	// Directory is listable
	entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation", uncreatedID))
	if err != nil {
		t.Fatalf("Failed to read directory: %v", err)
	}
	if len(entries) == 0 {
		t.Error("Expected files in conversation directory")
	}

	// fuse_id is set
	data, _ = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", uncreatedID, "fuse_id"))
	if strings.TrimSpace(string(data)) != uncreatedID {
		t.Errorf("fuse_id mismatch")
	}

	// created file should NOT exist (presence/absence semantics)
	createdPath := filepath.Join(mountPoint, "conversation", uncreatedID, "created")
	if _, err := os.Stat(createdPath); !os.IsNotExist(err) {
		t.Errorf("'created' should not exist for uncreated conversation, got: %v", err)
	}

	// id returns ENOENT
	_, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", uncreatedID, "id"))
	if err == nil {
		t.Error("Expected ENOENT for id")
	}

	// messages/count is 0
	data, _ = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", uncreatedID, "messages", "count"))
	if strings.TrimSpace(string(data)) != "0" {
		t.Errorf("messages/count should be 0")
	}
}

// TestSlugSymlink tests symlinks for conversation slugs.
func TestSlugSymlink(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)

	// Create conversation with slug via API
	client := shelley.NewClient(serverURL)
	result, err := client.StartConversation("Test for slug", "predictable", t.TempDir())
	if err != nil {
		t.Fatalf("Failed to create conversation: %v", err)
	}
	serverID := result.ConversationID
	slug := result.Slug

	mountPoint := mountTestFS(t, serverURL)

	// Trigger adoption
	ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))

	if slug == "" {
		t.Skip("No slug for this conversation")
	}

	// Slug symlink appears
	entries, _ := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
	var foundSlug bool
	for _, e := range entries {
		if e.Name() == slug && e.Mode()&os.ModeSymlink != 0 {
			foundSlug = true
		}
	}
	if !foundSlug {
		t.Errorf("Slug symlink %q not found", slug)
	}

	// Can read via slug
	data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", slug, "messages", "all.json"))
	if err != nil {
		t.Fatalf("Failed to read via slug: %v", err)
	}
	var msgs []map[string]interface{}
	if err := json.Unmarshal(data, &msgs); err != nil || len(msgs) == 0 {
		t.Error("Expected messages")
	}

	// Slug and server ID point to same local ID
	targetSlug, _ := os.Readlink(filepath.Join(mountPoint, "conversation", slug))
	targetServer, _ := os.Readlink(filepath.Join(mountPoint, "conversation", serverID))
	if targetSlug != targetServer {
		t.Errorf("Symlink targets differ: %s vs %s", targetSlug, targetServer)
	}
}

// TestUnconversedIDsHiddenFromListing verifies uncreated conversations are hidden from listings.
func TestUnconversedIDsHiddenFromListing(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint, store := mountTestFSWithStore(t, serverURL, time.Hour)

	// Clone
	data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}
	convID := strings.TrimSpace(string(data))

	// Exists in state but not created
	cs := store.Get(convID)
	if cs == nil || cs.Created {
		t.Fatalf("Expected uncreated conversation in state")
	}

	// Not in listing
	entries, _ := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
	for _, e := range entries {
		if e.Name() == convID {
			t.Errorf("Uncreated conversation should NOT appear in listing")
		}
	}

	// But accessible via lookup
	_, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "ctl"))
	if err != nil {
		t.Fatalf("Failed to access uncreated conversation: %v", err)
	}

	// Create it
	ioutil.WriteFile(filepath.Join(mountPoint, "conversation", convID, "ctl"), []byte("model=predictable"), 0644)
	ioutil.WriteFile(filepath.Join(mountPoint, "conversation", convID, "send"), []byte("Hello"), 0644)

	// Now in listing
	entries, _ = ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
	var found bool
	for _, e := range entries {
		if e.Name() == convID {
			found = true
		}
	}
	if !found {
		t.Errorf("Created conversation should appear in listing")
	}

	// State updated
	cs = store.Get(convID)
	if cs == nil || !cs.Created {
		t.Error("Expected Created=true in state")
	}
}

// TestUnconversedIDsCleanup verifies cleanup after timeout.
func TestUnconversedIDsCleanup(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint, store := mountTestFSWithStore(t, serverURL, 100*time.Millisecond)

	data, _ := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
	convID := strings.TrimSpace(string(data))

	if store.Get(convID) == nil {
		t.Fatal("Expected conversation in state")
	}

	time.Sleep(150 * time.Millisecond)
	ioutil.ReadDir(filepath.Join(mountPoint, "conversation")) // trigger cleanup

	if store.Get(convID) != nil {
		t.Errorf("Conversation should be cleaned up")
	}
}

// TestUnconversedIDsNotCleanedUpBeforeTimeout verifies no premature cleanup.
func TestUnconversedIDsNotCleanedUpBeforeTimeout(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint, store := mountTestFSWithStore(t, serverURL, time.Hour)

	data, _ := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
	convID := strings.TrimSpace(string(data))

	for i := 0; i < 3; i++ {
		ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
	}

	if store.Get(convID) == nil {
		t.Error("Conversation should not be cleaned up before timeout")
	}
}

// TestCreatedConversationNotCleanedUp verifies created conversations are never cleaned up.
func TestCreatedConversationNotCleanedUp(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint, store := mountTestFSWithStore(t, serverURL, 100*time.Millisecond)

	localID, _ := createConversation(t, mountPoint, "Hello")

	time.Sleep(150 * time.Millisecond)
	ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))

	cs := store.Get(localID)
	if cs == nil || !cs.Created {
		t.Error("Created conversation should not be cleaned up")
	}

	entries, _ := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
	var found bool
	for _, e := range entries {
		if e.Name() == localID {
			found = true
		}
	}
	if !found {
		t.Error("Created conversation should still appear in listing")
	}
}

// TestMultilineWriteToSend verifies that multiline messages written to /send
// are buffered and sent as a single message when the file is closed, not
// split on newlines. This tests the fix for sf-bksa.
func TestMultilineWriteToSend(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	localID, _ := createConversation(t, mountPoint, "Initial message")

	// Write a multiline message using explicit open/write/close
	sendPath := filepath.Join(mountPoint, "conversation", localID, "send")
	f, err := os.OpenFile(sendPath, os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to open send: %v", err)
	}

	// Write in multiple chunks, simulating how a shell might pipe data
	chunks := []string{
		"Line 1\n",
		"Line 2\n",
		"Line 3",
	}
	for _, chunk := range chunks {
		if _, err := f.Write([]byte(chunk)); err != nil {
			f.Close()
			t.Fatalf("Failed to write chunk: %v", err)
		}
	}

	if err := f.Close(); err != nil {
		t.Fatalf("Failed to close: %v", err)
	}

	// Read the messages and verify the multiline message was sent as one
	data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", localID, "messages", "all.json"))
	if err != nil {
		t.Fatalf("Failed to read all.json: %v", err)
	}

	var msgs []shelley.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		t.Fatalf("Failed to parse all.json: %v", err)
	}

	// Find user messages (Type == "user")
	var userMsgs []shelley.Message
	for _, m := range msgs {
		if m.Type == "user" {
			userMsgs = append(userMsgs, m)
		}
	}

	// Should have exactly 2 user messages: initial + multiline
	if len(userMsgs) != 2 {
		t.Errorf("Expected 2 user messages, got %d", len(userMsgs))
	}

	// The second user message should contain all three lines
	// Message content is stored in LLMData as JSON; check it contains the text
	if len(userMsgs) >= 2 {
		var content string
		if userMsgs[1].LLMData != nil {
			content = *userMsgs[1].LLMData
		}
		if !strings.Contains(content, "Line 1") ||
			!strings.Contains(content, "Line 2") ||
			!strings.Contains(content, "Line 3") {
			t.Errorf("Expected multiline message with all 3 lines, got: %q", content)
		}
	}
}

// TestShellRedirectToSend tests the shell redirect pattern where bash does:
//   1. open file -> fd 3
//   2. dup2(3, 1) -> fd 1 now points to file
//   3. close(3) -> triggers Flush with EMPTY buffer (no data written yet!)
//   4. echo writes to fd 1 -> data goes in buffer
//   5. process exits, fd 1 closed -> triggers Flush again
//
// The bug (sf-bksa): flushed flag was set on step 3, so step 5 was a no-op.
// The fix: only set flushed when there's actual data to send.
func TestShellRedirectToSend(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	localID, _ := createConversation(t, mountPoint, "Initial message")

	sendPath := filepath.Join(mountPoint, "conversation", localID, "send")

	// Simulate shell redirect behavior:
	// 1. Open the file (fd 3)
	fd1, err := syscall.Open(sendPath, syscall.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to open send: %v", err)
	}

	// 2. Dup to a new fd (simulating dup2 to redirect stdout)
	fd2, err := syscall.Dup(fd1)
	if err != nil {
		syscall.Close(fd1)
		t.Fatalf("Failed to dup: %v", err)
	}

	// 3. Close the original fd - this triggers Flush with EMPTY buffer!
	//    The bug was that this set flushed=true even though nothing was written.
	if err := syscall.Close(fd1); err != nil {
		syscall.Close(fd2)
		t.Fatalf("Failed to close fd1: %v", err)
	}

	// 4. Write data to the dup'd fd (like echo would)
	msg := "Shell redirect test message"
	if _, err := syscall.Write(fd2, []byte(msg)); err != nil {
		syscall.Close(fd2)
		t.Fatalf("Failed to write: %v", err)
	}

	// 5. Close the dup'd fd - this should trigger Flush with the actual data
	if err := syscall.Close(fd2); err != nil {
		t.Fatalf("Failed to close fd2: %v", err)
	}

	// Verify the message was sent
	data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", localID, "messages", "all.json"))
	if err != nil {
		t.Fatalf("Failed to read all.json: %v", err)
	}

	var msgs []shelley.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		t.Fatalf("Failed to parse all.json: %v", err)
	}

	// Find user messages
	var userMsgs []shelley.Message
	for _, m := range msgs {
		if m.Type == "user" {
			userMsgs = append(userMsgs, m)
		}
	}

	// Should have exactly 2 user messages: initial + shell redirect message
	if len(userMsgs) != 2 {
		t.Fatalf("Expected 2 user messages, got %d (shell redirect message not sent!)", len(userMsgs))
	}

	// Verify the second message contains our text
	var content string
	if userMsgs[1].LLMData != nil {
		content = *userMsgs[1].LLMData
	}
	if !strings.Contains(content, "Shell redirect test") {
		t.Errorf("Expected shell redirect message, got: %q", content)
	}
}

// TestArchivedFile tests the archived file presence/absence semantics.
// Creating the file archives the conversation, removing it unarchives.
func TestArchivedFile(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)
	localID, _ := createConversation(t, mountPoint, "Test archiving")

	archivedPath := filepath.Join(mountPoint, "conversation", localID, "archived")

	// Initially, archived should NOT exist
	_, err := os.Stat(archivedPath)
	if !os.IsNotExist(err) {
		t.Errorf("Expected archived to not exist initially, got err: %v", err)
	}

	// Touch (create) the archived file to archive the conversation
	f, err := os.Create(archivedPath)
	if err != nil {
		t.Fatalf("Failed to create archived file: %v", err)
	}
	f.Close()

	// Now archived should exist
	_, err = os.Stat(archivedPath)
	if err != nil {
		t.Errorf("Expected archived to exist after creation, got err: %v", err)
	}

	// Archived should appear in directory listing
	entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation", localID))
	if err != nil {
		t.Fatalf("Failed to read conversation directory: %v", err)
	}
	var foundArchived bool
	for _, e := range entries {
		if e.Name() == "archived" {
			foundArchived = true
		}
	}
	if !foundArchived {
		t.Error("Expected 'archived' in directory listing after archiving")
	}

	// Remove the archived file to unarchive
	err = os.Remove(archivedPath)
	if err != nil {
		t.Fatalf("Failed to remove archived file: %v", err)
	}

	// After removal, archived should NOT exist
	_, err = os.Stat(archivedPath)
	if !os.IsNotExist(err) {
		t.Errorf("Expected archived to not exist after removal, got err: %v", err)
	}
}

// TestArchivedFileOnlyAllowsArchived verifies that only 'archived' can be created/removed.
func TestArchivedFileOnlyAllowsArchived(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)
	localID, _ := createConversation(t, mountPoint, "Test archiving restrictions")

	// Try to create a random file - should fail
	randomPath := filepath.Join(mountPoint, "conversation", localID, "randomfile")
	_, err := os.Create(randomPath)
	if err == nil {
		t.Error("Expected error creating random file in conversation directory")
	}
}

// TestArchivedFileRemoveWhenNotArchived verifies removing archived when not archived returns error.
func TestArchivedFileRemoveWhenNotArchived(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)
	localID, _ := createConversation(t, mountPoint, "Test remove when not archived")

	archivedPath := filepath.Join(mountPoint, "conversation", localID, "archived")

	// Try to remove archived when not archived - should fail with ENOENT
	err := os.Remove(archivedPath)
	if err == nil {
		t.Error("Expected error removing archived file when not archived")
	}
	if !os.IsNotExist(err) {
		t.Errorf("Expected ENOENT error, got: %v", err)
	}
}

// TestArchivedFileTouchSetattr verifies that touch (setting times) on archived file succeeds.
func TestArchivedFileTouchSetattr(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)
	localID, _ := createConversation(t, mountPoint, "Test touch archived")

	archivedPath := filepath.Join(mountPoint, "conversation", localID, "archived")

	// First archive the conversation by creating the file
	f, err := os.Create(archivedPath)
	if err != nil {
		t.Fatalf("Failed to create archived file: %v", err)
	}
	f.Close()

	// Verify it exists
	_, err = os.Stat(archivedPath)
	if err != nil {
		t.Fatalf("Expected archived to exist after creation, got err: %v", err)
	}

	// Now touch (set times) should succeed - this was previously failing with ENOTSUP
	newTime := time.Now()
	err = os.Chtimes(archivedPath, newTime, newTime)
	if err != nil {
		t.Errorf("Touch (Chtimes) on archived file should succeed, got error: %v", err)
	}
}

// TestArchivedFileTimestamp verifies that the archived file uses UpdatedAt as its timestamp.
func TestArchivedFileTimestamp(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)
	localID, _ := createConversation(t, mountPoint, "Test archived timestamp")

	archivedPath := filepath.Join(mountPoint, "conversation", localID, "archived")

	// Archive the conversation by creating the file
	f, err := os.Create(archivedPath)
	if err != nil {
		t.Fatalf("Failed to create archived file: %v", err)
	}
	f.Close()

	// Get the file info to check the timestamp
	info, err := os.Stat(archivedPath)
	if err != nil {
		t.Fatalf("Failed to stat archived file: %v", err)
	}

	// The ModTime should be non-zero and reasonably recent (within last hour)
	// This verifies that the timestamp is being set from conversation.UpdatedAt
	modTime := info.ModTime()
	if modTime.IsZero() {
		t.Error("Expected non-zero modification time on archived file")
	}

	now := time.Now()
	if modTime.After(now) {
		t.Errorf("ModTime %v is in the future (now: %v)", modTime, now)
	}

	// ModTime should be within last hour (reasonable for a just-created conversation)
	oneHourAgo := now.Add(-1 * time.Hour)
	if modTime.Before(oneHourAgo) {
		t.Errorf("ModTime %v is too old (more than 1 hour ago, now: %v)", modTime, now)
	}

	t.Logf("Archived file timestamp: %v", modTime)
}
