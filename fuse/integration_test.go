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
	if err := ioutil.WriteFile(filepath.Join(mountPoint, "conversation", localID, "new"),
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

	readyData, err := ioutil.ReadFile(filepath.Join(mountPoint, "models", "predictable", "ready"))
	if err != nil {
		t.Fatalf("Failed to read models/predictable/ready: %v", err)
	}
	if strings.TrimSpace(string(readyData)) != "true" {
		t.Errorf("Expected ready='true', got %q", string(readyData))
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

	// Status before create
	data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "created"))
	if err != nil {
		t.Fatalf("Failed to read created: %v", err)
	}
	if strings.TrimSpace(string(data)) != "false" {
		t.Errorf("Expected created=false before first message")
	}

	// id should not exist before create
	if _, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "id")); err == nil {
		t.Error("Expected error reading id before conversation created")
	}

	// Write first message
	if err := ioutil.WriteFile(filepath.Join(mountPoint, "conversation", convID, "new"),
		[]byte("Hello shelley, this is a test"), 0644); err != nil {
		t.Fatalf("Failed to write first message: %v", err)
	}

	// Status after create
	data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "created"))
	if err != nil {
		t.Fatalf("Failed to read created: %v", err)
	}
	if strings.TrimSpace(string(data)) != "true" {
		t.Errorf("Expected created=true after first message")
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

	// Read specific message via named file
	// Note: The first message is typically a system message (001-system.json),
	// so the user message is at sequence 2 (002-user.json)
	data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "messages", "002-user.json"))
	if err != nil {
		t.Fatalf("Failed to read 002-user.json: %v", err)
	}
	if err := json.Unmarshal(data, &msgs); err != nil || len(msgs) != 1 {
		t.Errorf("Expected 1 message in 002-user.json")
	}

	// Read last/2
	data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "messages", "last", "2.json"))
	if err != nil {
		t.Fatalf("Failed to read last/2.json: %v", err)
	}
	if err := json.Unmarshal(data, &msgs); err != nil || len(msgs) != 2 {
		t.Errorf("Expected 2 messages in last/2.json, got %d", len(msgs))
	}

	// Read since/user/1
	data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "messages", "since", "user", "1.json"))
	if err != nil {
		t.Fatalf("Failed to read since/user/1.json: %v", err)
	}
	if err := json.Unmarshal(data, &msgs); err != nil || len(msgs) == 0 {
		t.Error("Expected messages in since/user/1.json")
	}

	// Read from/user/1
	data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "messages", "from", "user", "1.json"))
	if err != nil {
		t.Fatalf("Failed to read from/user/1.json: %v", err)
	}
	if err := json.Unmarshal(data, &msgs); err != nil || len(msgs) != 1 {
		t.Errorf("Expected 1 message in from/user/1.json")
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

	expectedFiles := map[string]bool{
		"ctl": false, "new": false, "id": false, "slug": false,
		"fuse_id": false, "created": false, "created_at": false, "message_count": false,
	}
	expectedDirs := map[string]bool{"messages": false}

	for _, e := range entries {
		if _, ok := expectedFiles[e.Name()]; ok {
			expectedFiles[e.Name()] = true
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

	data, _ = ioutil.ReadFile(filepath.Join(convDir, "created"))
	if strings.TrimSpace(string(data)) != "true" {
		t.Errorf("created should be true")
	}

	data, _ = ioutil.ReadFile(filepath.Join(convDir, "created_at"))
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data))); err != nil {
		t.Errorf("created_at not RFC3339: %v", err)
	}

	data, _ = ioutil.ReadFile(filepath.Join(convDir, "message_count"))
	if strings.TrimSpace(string(data)) == "0" {
		t.Errorf("message_count should be > 0")
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
	expected := map[string]bool{"ctl": false, "new": false, "messages": false}
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

	// Nested path through symlink
	data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", serverID, "messages", "last", "2.json"))
	if err != nil {
		t.Fatalf("Failed to read last/2.json through symlink: %v", err)
	}
	if err := json.Unmarshal(data, &msgs); err != nil || len(msgs) != 2 {
		t.Errorf("Expected 2 messages")
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

	// created is false
	data, _ = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", uncreatedID, "created"))
	if strings.TrimSpace(string(data)) != "false" {
		t.Errorf("created should be false")
	}

	// id returns ENOENT
	_, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", uncreatedID, "id"))
	if err == nil {
		t.Error("Expected ENOENT for id")
	}

	// message_count is 0
	data, _ = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", uncreatedID, "message_count"))
	if strings.TrimSpace(string(data)) != "0" {
		t.Errorf("message_count should be 0")
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
	ioutil.WriteFile(filepath.Join(mountPoint, "conversation", convID, "new"), []byte("Hello"), 0644)

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

// TestMultilineWriteToNew verifies that multiline messages written to /new
// are buffered and sent as a single message when the file is closed, not
// split on newlines. This tests the fix for sf-bksa.
func TestMultilineWriteToNew(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	localID, _ := createConversation(t, mountPoint, "Initial message")

	// Write a multiline message using explicit open/write/close
	newPath := filepath.Join(mountPoint, "conversation", localID, "new")
	f, err := os.OpenFile(newPath, os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to open new: %v", err)
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
