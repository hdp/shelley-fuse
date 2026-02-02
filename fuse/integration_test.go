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
	"sort"
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

// startShelleyServer starts a predictable-only Shelley server on a random free port.
// Returns the server URL and a cleanup function.
func startShelleyServer(t *testing.T) string {
	t.Helper()

	// Find a free port by binding and releasing
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")

	cmd := exec.Command("/usr/local/bin/shelley",
		"-db", dbPath,
		"-predictable-only",
		"serve",
		"-port", fmt.Sprintf("%d", port),
		"-require-header", "X-Exedev-Userid")
	cmd.Env = append(os.Environ(),
		"FIREWORKS_API_KEY=",
		"ANTHROPIC_API_KEY=",
		"OPENAI_API_KEY=",
	)

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start shelley server: %v", err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	// Wait for server to be ready
	serverURL := fmt.Sprintf("http://localhost:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(serverURL)
		if err == nil {
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

// mountTestFS mounts a shelley-fuse filesystem for testing.
func mountTestFS(t *testing.T, serverURL string) string {
	mountPoint, _ := mountTestFSWithStore(t, serverURL, time.Hour)
	return mountPoint
}

// mountTestFSWithStore mounts a shelley-fuse filesystem for testing and returns both
// the mount point and the state store. The cloneTimeout parameter controls how long
// unconversed IDs are kept before cleanup.
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
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(mountPoint, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	t.Cleanup(func() {
		fssrv.Unmount()
	})

	return mountPoint, store
}

// TestPlan9Flow is the main end-to-end test exercising the Plan 9-style API.
// It starts a real Shelley server, mounts a FUSE filesystem, and exercises
// the full clone → ctl → new → read cycle across all query paths.
func TestPlan9Flow(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// 1. Verify root directory listing
	t.Run("RootDirectory", func(t *testing.T) {
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
	})

	// 2. Read models directory - verify directory structure
	t.Run("ReadModelsDir", func(t *testing.T) {
		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "models"))
		if err != nil {
			t.Fatalf("Failed to read models directory: %v", err)
		}
		
		// Should have at least one model
		if len(entries) == 0 {
			t.Fatal("Expected at least one model in /models directory")
		}
		
		// Check that 'predictable' model exists
		foundPredictable := false
		for _, e := range entries {
			if e.Name() == "predictable" {
				foundPredictable = true
				if !e.IsDir() {
					t.Error("Expected 'predictable' to be a directory")
				}
				break
			}
		}
		if !foundPredictable {
			t.Error("Expected 'predictable' model in /models directory")
		}
	})

	// 2b. Read model fields
	t.Run("ReadModelFields", func(t *testing.T) {
		// Read the id field
		idData, err := ioutil.ReadFile(filepath.Join(mountPoint, "models", "predictable", "id"))
		if err != nil {
			t.Fatalf("Failed to read models/predictable/id: %v", err)
		}
		if strings.TrimSpace(string(idData)) != "predictable" {
			t.Errorf("Expected id='predictable', got %q", strings.TrimSpace(string(idData)))
		}
		
		// Read the ready field
		readyData, err := ioutil.ReadFile(filepath.Join(mountPoint, "models", "predictable", "ready"))
		if err != nil {
			t.Fatalf("Failed to read models/predictable/ready: %v", err)
		}
		if strings.TrimSpace(string(readyData)) != "true" {
			t.Errorf("Expected ready='true', got %q", strings.TrimSpace(string(readyData)))
		}
	})

	// 2c. List model directory contents
	t.Run("ListModelContents", func(t *testing.T) {
		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "models", "predictable"))
		if err != nil {
			t.Fatalf("Failed to read models/predictable directory: %v", err)
		}
		
		expectedFiles := map[string]bool{"id": false, "ready": false}
		for _, e := range entries {
			if _, ok := expectedFiles[e.Name()]; ok {
				expectedFiles[e.Name()] = true
				if e.IsDir() {
					t.Errorf("Expected %q to be a file, not a directory", e.Name())
				}
			}
		}
		for name, found := range expectedFiles {
			if !found {
				t.Errorf("Expected file %q in models/predictable directory", name)
			}
		}
	})

	// 3. Clone a new conversation
	var convID string
	t.Run("Clone", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
		if err != nil {
			t.Fatalf("Failed to read new/clone: %v", err)
		}
		convID = strings.TrimSpace(string(data))
		if len(convID) != 8 {
			t.Fatalf("Expected 8-char hex ID, got %q", convID)
		}
		t.Logf("Cloned conversation ID: %s", convID)
	})

	// 4. Write ctl to configure the conversation
	t.Run("WriteCtl", func(t *testing.T) {
		ctlPath := filepath.Join(mountPoint, "conversation", convID, "ctl")
		err := ioutil.WriteFile(ctlPath, []byte("model=predictable cwd=/tmp"), 0644)
		if err != nil {
			t.Fatalf("Failed to write ctl: %v", err)
		}
	})

	// 5. Read ctl to verify configuration
	t.Run("ReadCtl", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "ctl"))
		if err != nil {
			t.Fatalf("Failed to read ctl: %v", err)
		}
		content := strings.TrimSpace(string(data))
		if !strings.Contains(content, "model=predictable") {
			t.Errorf("Expected 'model=predictable' in ctl, got %q", content)
		}
		if !strings.Contains(content, "cwd=/tmp") {
			t.Errorf("Expected 'cwd=/tmp' in ctl, got %q", content)
		}
	})

	// 6. Read created before conversation is created
	t.Run("StatusBeforeCreate", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "created"))
		if err != nil {
			t.Fatalf("Failed to read created: %v", err)
		}
		content := strings.TrimSpace(string(data))
		if content != "false" {
			t.Errorf("Expected created=false before first message, got %q", content)
		}
	})

	// 7. Write first message to create the conversation on the backend
	t.Run("WriteFirstMessage", func(t *testing.T) {
		newPath := filepath.Join(mountPoint, "conversation", convID, "new")
		err := ioutil.WriteFile(newPath, []byte("Hello shelley, this is a test"), 0644)
		if err != nil {
			t.Fatalf("Failed to write first message: %v", err)
		}
	})

	// 8. Read created after conversation is created
	t.Run("StatusAfterCreate", func(t *testing.T) {
		// Check created status
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "created"))
		if err != nil {
			t.Fatalf("Failed to read created: %v", err)
		}
		if strings.TrimSpace(string(data)) != "true" {
			t.Errorf("Expected created=true, got %q", string(data))
		}

		// Check id exists (was shelley_id in status/)
		data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "id"))
		if err != nil {
			t.Fatalf("Failed to read id: %v", err)
		}
		if strings.TrimSpace(string(data)) == "" {
			t.Error("Expected non-empty id")
		}
	})

	// 8b. Read the id file (should return Shelley conversation ID)
	var shelleyConvID string
	t.Run("ReadConversationID", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "id"))
		if err != nil {
			t.Fatalf("Failed to read id: %v", err)
		}
		shelleyConvID = strings.TrimSpace(string(data))
		if shelleyConvID == "" {
			t.Error("Expected non-empty Shelley conversation ID")
		}
		t.Logf("Shelley conversation ID: %s", shelleyConvID)
	})

	// 8c. Read the slug file (returns ENOENT if slug is empty, which is expected for predictable model)
	t.Run("ReadConversationSlug", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "slug"))
		if err != nil {
			// ENOENT is expected when slug is empty (e.g., predictable model)
			if os.IsNotExist(err) {
				t.Logf("Slug file returned ENOENT (empty slug, expected for predictable model)")
				return
			}
			t.Fatalf("Failed to read slug: %v", err)
		}
		slug := strings.TrimSpace(string(data))
		t.Logf("Conversation slug: %q", slug)
	})

	// 8d. Verify id file returns ENOENT for uncreated conversation
	t.Run("IDNotAvailableBeforeCreate", func(t *testing.T) {
		// Clone a new conversation but don't create it
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
		if err != nil {
			t.Fatalf("Failed to clone: %v", err)
		}
		uncreatedID := strings.TrimSpace(string(data))

		// Reading id should fail with ENOENT
		_, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", uncreatedID, "id"))
		if err == nil {
			t.Error("Expected error reading id for uncreated conversation")
		}

		// Reading slug should also fail with ENOENT
		_, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", uncreatedID, "slug"))
		if err == nil {
			t.Error("Expected error reading slug for uncreated conversation")
		}
	})

	// 9. Verify ctl is read-only after creation
	t.Run("CtlReadOnlyAfterCreate", func(t *testing.T) {
		ctlPath := filepath.Join(mountPoint, "conversation", convID, "ctl")
		err := ioutil.WriteFile(ctlPath, []byte("model=other"), 0644)
		if err == nil {
			t.Error("Expected error writing to ctl after conversation created")
		}
	})

	// 10. Read all.json
	t.Run("ReadAllJSON", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "all.json"))
		if err != nil {
			t.Fatalf("Failed to read all.json: %v", err)
		}
		var msgs []shelley.Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			t.Fatalf("Failed to parse all.json: %v", err)
		}
		if len(msgs) == 0 {
			t.Error("Expected at least one message in all.json")
		}
		t.Logf("all.json contains %d messages", len(msgs))
	})

	// 11. Read all.md
	t.Run("ReadAllMD", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "all.md"))
		if err != nil {
			t.Fatalf("Failed to read all.md: %v", err)
		}
		content := string(data)
		if !strings.Contains(content, "##") {
			t.Error("Expected markdown headers in all.md")
		}
		t.Logf("all.md:\n%s", content)
	})

	// 12. Send a follow-up message (may fail with predictable model, non-fatal)
	t.Run("WriteSecondMessage", func(t *testing.T) {
		newPath := filepath.Join(mountPoint, "conversation", convID, "new")
		err := ioutil.WriteFile(newPath, []byte("Tell me more"), 0644)
		if err != nil {
			t.Logf("Second message write failed (expected with predictable model): %v", err)
		}
	})

	// 13. Read specific message by sequence number (JSON)
	t.Run("ReadMessageBySeqJSON", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "1.json"))
		if err != nil {
			t.Fatalf("Failed to read 1.json: %v", err)
		}
		var msgs []shelley.Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			t.Fatalf("Failed to parse 1.json: %v", err)
		}
		if len(msgs) != 1 {
			t.Errorf("Expected 1 message, got %d", len(msgs))
		}
	})

	// 14. Read specific message by sequence number (Markdown)
	t.Run("ReadMessageBySeqMD", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "1.md"))
		if err != nil {
			t.Fatalf("Failed to read 1.md: %v", err)
		}
		if !strings.Contains(string(data), "##") {
			t.Error("Expected markdown header in 1.md")
		}
	})

	// 15. Read last/N.json
	t.Run("ReadLastJSON", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "last", "2.json"))
		if err != nil {
			t.Fatalf("Failed to read last/2.json: %v", err)
		}
		var msgs []shelley.Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			t.Fatalf("Failed to parse last/2.json: %v", err)
		}
		if len(msgs) != 2 {
			t.Errorf("Expected 2 messages from last/2.json, got %d", len(msgs))
		}
	})

	// 16. Read last/N.md
	t.Run("ReadLastMD", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "last", "2.md"))
		if err != nil {
			t.Fatalf("Failed to read last/2.md: %v", err)
		}
		if !strings.Contains(string(data), "##") {
			t.Error("Expected markdown headers in last/2.md")
		}
	})

	// 17. Read since/user/1.json (messages since last user message)
	t.Run("ReadSinceJSON", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "since", "user", "1.json"))
		if err != nil {
			t.Fatalf("Failed to read since/user/1.json: %v", err)
		}
		var msgs []shelley.Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			t.Fatalf("Failed to parse since/user/1.json: %v", err)
		}
		if len(msgs) == 0 {
			t.Error("Expected at least one message from since/user/1.json")
		}
	})

	// 18. Read since/user/1.md
	t.Run("ReadSinceMD", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "since", "user", "1.md"))
		if err != nil {
			t.Fatalf("Failed to read since/user/1.md: %v", err)
		}
		if !strings.Contains(string(data), "##") {
			t.Error("Expected markdown headers in since/user/1.md")
		}
	})

	// 19. Read from/user/1.json (most recent user message)
	t.Run("ReadFromJSON", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "from", "user", "1.json"))
		if err != nil {
			t.Fatalf("Failed to read from/user/1.json: %v", err)
		}
		var msgs []shelley.Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			t.Fatalf("Failed to parse from/user/1.json: %v", err)
		}
		if len(msgs) != 1 {
			t.Errorf("Expected 1 message, got %d", len(msgs))
		}
	})

	// 20. Read from/user/1.md
	t.Run("ReadFromMD", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "from", "user", "1.md"))
		if err != nil {
			t.Fatalf("Failed to read from/user/1.md: %v", err)
		}
		if !strings.Contains(string(data), "##") {
			t.Error("Expected markdown header in from/user/1.md")
		}
	})

	// 21. Verify conversation appears in conversation directory listing
	t.Run("ConversationListing", func(t *testing.T) {
		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
		if err != nil {
			t.Fatalf("Failed to read conversation dir: %v", err)
		}
		found := false
		for _, e := range entries {
			if e.Name() == convID {
				found = true
				if !e.IsDir() {
					t.Errorf("Conversation %s should be a directory", convID)
				}
			}
		}
		if !found {
			t.Errorf("Conversation %s not found in listing", convID)
		}
	})

	// 22. Clone returns different ID each time
	t.Run("CloneUnique", func(t *testing.T) {
		data1, _ := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
		data2, _ := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
		id1 := strings.TrimSpace(string(data1))
		id2 := strings.TrimSpace(string(data2))
		if id1 == id2 {
			t.Errorf("Expected different IDs from clone, got %q both times", id1)
		}
	})

	// 23. Verify conversation directory contains id and slug files
	t.Run("ConversationDirContentsIncludeIDAndSlug", func(t *testing.T) {
		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation", convID))
		if err != nil {
			t.Fatalf("Failed to read conversation directory: %v", err)
		}

		expectedFiles := map[string]bool{
			"ctl":      false,
			"new":      false,
			"id":       false,
			"slug":     false,
			"all.json": false,
			"all.md":   false,
		}
		expectedDirs := map[string]bool{
			"last":  false,
			"since": false,
			"from":  false,
		}

		for _, e := range entries {
			if _, ok := expectedFiles[e.Name()]; ok {
				expectedFiles[e.Name()] = true
				if e.IsDir() {
					t.Errorf("Expected %q to be a file, not a directory", e.Name())
				}
			}
			if _, ok := expectedDirs[e.Name()]; ok {
				expectedDirs[e.Name()] = true
				if !e.IsDir() {
					t.Errorf("Expected %q to be a directory", e.Name())
				}
			}
		}

		for name, found := range expectedFiles {
			if !found {
				t.Errorf("Expected file %q in conversation directory", name)
			}
		}
		for name, found := range expectedDirs {
			if !found {
				t.Errorf("Expected directory %q in conversation directory", name)
			}
		}
	})

	// 24. Verify status fields are at conversation root (flattened from status/)
	t.Run("StatusFieldsAtRoot", func(t *testing.T) {
		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation", convID))
		if err != nil {
			t.Fatalf("Failed to read conversation directory: %v", err)
		}

		// Status files that should be at conversation root
		statusFiles := map[string]bool{
			"fuse_id":       false,
			"created":       false,
			"created_at":    false,
			"message_count": false,
		}

		// cwd should be a symlink at root
		cwdFound := false

		for _, e := range entries {
			if e.Name() == "cwd" {
				cwdFound = true
				if e.Mode()&os.ModeSymlink == 0 {
					t.Errorf("Expected 'cwd' to be a symlink, got mode %v", e.Mode())
				}
			} else if _, ok := statusFiles[e.Name()]; ok {
				statusFiles[e.Name()] = true
				if e.IsDir() {
					t.Errorf("Expected %q to be a file, not a directory", e.Name())
				}
			}
		}

		for name, found := range statusFiles {
			if !found {
				t.Errorf("Expected file %q in conversation directory", name)
			}
		}
		if !cwdFound {
			t.Errorf("Expected symlink 'cwd' in conversation directory")
		}
	})

	// 25. Verify status field values (now at conversation root)
	t.Run("StatusFieldValues", func(t *testing.T) {
		convDir := filepath.Join(mountPoint, "conversation", convID)

		// Check fuse_id (was local_id in status/)
		data, err := ioutil.ReadFile(filepath.Join(convDir, "fuse_id"))
		if err != nil {
			t.Fatalf("Failed to read fuse_id: %v", err)
		}
		if strings.TrimSpace(string(data)) != convID {
			t.Errorf("Expected fuse_id=%q, got %q", convID, strings.TrimSpace(string(data)))
		}

		// Check id matches what we got from id file (was shelley_id in status/)
		data, err = ioutil.ReadFile(filepath.Join(convDir, "id"))
		if err != nil {
			t.Fatalf("Failed to read id: %v", err)
		}
		if strings.TrimSpace(string(data)) != shelleyConvID {
			t.Errorf("Expected id=%q, got %q", shelleyConvID, strings.TrimSpace(string(data)))
		}

		// Check model symlink (was status/model file, now symlink at root)
		modelTarget, err := os.Readlink(filepath.Join(convDir, "model"))
		if err != nil {
			t.Fatalf("Failed to readlink model: %v", err)
		}
		if !strings.Contains(modelTarget, "predictable") {
			t.Errorf("Expected model symlink to contain 'predictable', got %q", modelTarget)
		}

		// Check cwd symlink (was in status/, now at root)
		cwdTarget, err := os.Readlink(filepath.Join(convDir, "cwd"))
		if err != nil {
			t.Fatalf("Failed to readlink cwd: %v", err)
		}
		if cwdTarget != "/tmp" {
			t.Errorf("Expected cwd symlink target=/tmp, got %q", cwdTarget)
		}

		// Check created (should be "true" after conversation is created)
		data, err = ioutil.ReadFile(filepath.Join(convDir, "created"))
		if err != nil {
			t.Fatalf("Failed to read created: %v", err)
		}
		if strings.TrimSpace(string(data)) != "true" {
			t.Errorf("Expected created=true, got %q", strings.TrimSpace(string(data)))
		}

		// Check created_at is a valid RFC3339 timestamp
		data, err = ioutil.ReadFile(filepath.Join(convDir, "created_at"))
		if err != nil {
			t.Fatalf("Failed to read created_at: %v", err)
		}
		createdAtStr := strings.TrimSpace(string(data))
		if _, err := time.Parse(time.RFC3339, createdAtStr); err != nil {
			t.Errorf("Expected created_at to be RFC3339 formatted, got %q: %v", createdAtStr, err)
		}

		// Check message_count is a non-zero number
		data, err = ioutil.ReadFile(filepath.Join(convDir, "message_count"))
		if err != nil {
			t.Fatalf("Failed to read message_count: %v", err)
		}
		msgCount := strings.TrimSpace(string(data))
		if msgCount == "0" {
			t.Errorf("Expected non-zero message_count, got %q", msgCount)
		}
		t.Logf("message_count = %s", msgCount)
	})
}

// TestServerConversationListing tests that conversations from the server
// are automatically adopted with local IDs when listing the conversation directory.
func TestServerConversationListing(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)

	// Create a conversation directly via the API (not through FUSE)
	// This simulates a conversation that exists on the server but isn't tracked locally
	client := shelley.NewClient(serverURL)
	result, err := client.StartConversation("Hello from API", "predictable", t.TempDir())
	if err != nil {
		t.Fatalf("Failed to create server conversation: %v", err)
	}
	serverConvID := result.ConversationID
	t.Logf("Created server conversation: %s", serverConvID)

	// Mount the filesystem with a fresh state store
	mountPoint := mountTestFS(t, serverURL)

	var adoptedLocalID string

	// 1. Verify server conversations are immediately adopted with local IDs
	// Directory listing should show: local ID (directory) + server ID (symlink) + slug (symlink if exists)
	t.Run("ServerConversationAdoptedImmediately", func(t *testing.T) {
		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
		if err != nil {
			t.Fatalf("Failed to read conversation dir: %v", err)
		}

		// Find the directory entry (local ID) vs symlink entries
		var dirs, symlinks []os.FileInfo
		for _, e := range entries {
			if e.Mode()&os.ModeSymlink != 0 {
				symlinks = append(symlinks, e)
			} else if e.IsDir() {
				dirs = append(dirs, e)
			}
		}

		// Should have exactly 1 directory (the local ID)
		if len(dirs) != 1 {
			t.Fatalf("Expected 1 directory (local ID), got %d: %v", len(dirs), entryNames(dirs))
		}

		// The directory should be a local ID (8-char hex)
		adoptedLocalID = dirs[0].Name()
		if len(adoptedLocalID) != 8 {
			t.Errorf("Expected 8-char local ID, got %q", adoptedLocalID)
		}
		t.Logf("Server conversation adopted as local ID: %s", adoptedLocalID)

		// Should have at least 1 symlink (the server ID)
		if len(symlinks) < 1 {
			t.Fatalf("Expected at least 1 symlink (server ID), got %d", len(symlinks))
		}

		// One of the symlinks should be the server ID
		foundServerID := false
		for _, s := range symlinks {
			if s.Name() == serverConvID {
				foundServerID = true
				break
			}
		}
		if !foundServerID {
			t.Errorf("Expected symlink for server ID %q, symlinks: %v", serverConvID, entryNames(symlinks))
		}
		t.Logf("Found %d symlinks: %v", len(symlinks), entryNames(symlinks))
	})

	// 2. Verify we can access via server ID symlink
	// The symlink should point to the local ID directory
	t.Run("AccessViaServerID", func(t *testing.T) {
		serverIDPath := filepath.Join(mountPoint, "conversation", serverConvID)

		// Verify it's a symlink
		linkInfo, err := os.Lstat(serverIDPath)
		if err != nil {
			t.Fatalf("Failed to lstat server ID path: %v", err)
		}
		if linkInfo.Mode()&os.ModeSymlink == 0 {
			t.Errorf("Expected symlink for server ID, got mode %v", linkInfo.Mode())
		}

		// Read the symlink target
		target, err := os.Readlink(serverIDPath)
		if err != nil {
			t.Fatalf("Failed to readlink: %v", err)
		}
		if target != adoptedLocalID {
			t.Errorf("Symlink target = %q, want %q", target, adoptedLocalID)
		}
		t.Logf("Server ID symlink %s -> %s", serverConvID, target)

		// Verify we can follow the symlink and access content
		info, err := os.Stat(serverIDPath)
		if err != nil {
			t.Fatalf("Failed to stat (follow symlink) server ID path: %v", err)
		}
		if !info.IsDir() {
			t.Error("expected directory after following symlink")
		}
	})

	// 3. Verify we can access via local ID and read content
	t.Run("AccessViaLocalID", func(t *testing.T) {
		if adoptedLocalID == "" {
			t.Skip("No adopted local ID from previous test")
		}

		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", adoptedLocalID, "all.json"))
		if err != nil {
			t.Fatalf("Failed to read all.json via local ID: %v", err)
		}

		var msgs []shelley.Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			t.Fatalf("Failed to parse all.json: %v", err)
		}
		if len(msgs) == 0 {
			t.Error("Expected at least one message")
		}
		t.Logf("Server conversation has %d messages", len(msgs))
	})

	// 4. Verify the id file returns the server conversation ID
	t.Run("VerifyIDFile", func(t *testing.T) {
		if adoptedLocalID == "" {
			t.Skip("No adopted local ID from previous test")
		}

		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", adoptedLocalID, "id"))
		if err != nil {
			t.Fatalf("Failed to read id file: %v", err)
		}

		idContent := strings.TrimSpace(string(data))
		if idContent != serverConvID {
			t.Errorf("Expected id file to contain %q, got %q", serverConvID, idContent)
		}
	})

	// 5. Verify listing is stable (no duplicates on re-read)
	t.Run("StableListing", func(t *testing.T) {
		entries1, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
		if err != nil {
			t.Fatalf("Failed to read conversation dir (1st): %v", err)
		}

		entries2, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
		if err != nil {
			t.Fatalf("Failed to read conversation dir (2nd): %v", err)
		}

		if len(entries1) != len(entries2) {
			t.Errorf("Listing should be stable: got %d then %d entries", len(entries1), len(entries2))
		}

		names1 := entryNames(entries1)
		names2 := entryNames(entries2)
		sort.Strings(names1)
		sort.Strings(names2)
		for i := range names1 {
			if names1[i] != names2[i] {
				t.Errorf("Listing mismatch: %v vs %v", names1, names2)
				break
			}
		}
	})
}

// entryNames extracts names from directory entries for logging
func entryNames(entries []os.FileInfo) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}

// TestSymlinkAccess tests that symlinks for server IDs and slugs work correctly.
// Users should be able to access conversations through symlinks just like directories.
func TestSymlinkAccess(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// Create a conversation through the normal flow to get both local ID and server ID
	var localID, serverID string

	t.Run("Setup", func(t *testing.T) {
		// Clone and create a conversation
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
		if err != nil {
			t.Fatalf("Failed to clone: %v", err)
		}
		localID = strings.TrimSpace(string(data))

		// Configure and send first message
		err = ioutil.WriteFile(filepath.Join(mountPoint, "conversation", localID, "ctl"), []byte("model=predictable"), 0644)
		if err != nil {
			t.Fatalf("Failed to write ctl: %v", err)
		}
		err = ioutil.WriteFile(filepath.Join(mountPoint, "conversation", localID, "new"), []byte("Hello symlink test"), 0644)
		if err != nil {
			t.Fatalf("Failed to send message: %v", err)
		}

		// Get the server ID
		data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", localID, "id"))
		if err != nil {
			t.Fatalf("Failed to read server ID: %v", err)
		}
		serverID = strings.TrimSpace(string(data))
		t.Logf("Created conversation: local=%s, server=%s", localID, serverID)
	})

	t.Run("SymlinkAppearsInListing", func(t *testing.T) {
		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
		if err != nil {
			t.Fatalf("Failed to read conversation dir: %v", err)
		}

		// Find the server ID symlink
		var foundSymlink bool
		for _, e := range entries {
			if e.Name() == serverID && e.Mode()&os.ModeSymlink != 0 {
				foundSymlink = true
				break
			}
		}
		if !foundSymlink {
			t.Errorf("Server ID symlink %q not found in listing", serverID)
		}
	})

	t.Run("ReadlinkReturnsLocalID", func(t *testing.T) {
		target, err := os.Readlink(filepath.Join(mountPoint, "conversation", serverID))
		if err != nil {
			t.Fatalf("Readlink failed: %v", err)
		}
		if target != localID {
			t.Errorf("Symlink target = %q, want %q", target, localID)
		}
	})

	t.Run("StatFollowsSymlink", func(t *testing.T) {
		// os.Stat follows symlinks, should return directory info
		info, err := os.Stat(filepath.Join(mountPoint, "conversation", serverID))
		if err != nil {
			t.Fatalf("Stat failed: %v", err)
		}
		if !info.IsDir() {
			t.Errorf("Expected directory after following symlink, got mode %v", info.Mode())
		}
	})

	t.Run("LstatReturnsSymlink", func(t *testing.T) {
		// os.Lstat does NOT follow symlinks
		info, err := os.Lstat(filepath.Join(mountPoint, "conversation", serverID))
		if err != nil {
			t.Fatalf("Lstat failed: %v", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("Expected symlink, got mode %v", info.Mode())
		}
	})

	t.Run("ReadFileThroughSymlink", func(t *testing.T) {
		// Read all.json through the symlink
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", serverID, "all.json"))
		if err != nil {
			t.Fatalf("Failed to read all.json through symlink: %v", err)
		}

		var msgs []map[string]interface{}
		if err := json.Unmarshal(data, &msgs); err != nil {
			t.Fatalf("Failed to parse JSON: %v", err)
		}
		if len(msgs) == 0 {
			t.Error("Expected messages in conversation")
		}
	})

	t.Run("ReadStatusThroughSymlink", func(t *testing.T) {
		// Read fuse_id through symlink (was status/local_id)
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", serverID, "fuse_id"))
		if err != nil {
			t.Fatalf("Failed to read fuse_id through symlink: %v", err)
		}
		if strings.TrimSpace(string(data)) != localID {
			t.Errorf("fuse_id = %q, want %s", string(data), localID)
		}

		// Read id through symlink (was status/shelley_id)
		data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", serverID, "id"))
		if err != nil {
			t.Fatalf("Failed to read id through symlink: %v", err)
		}
		if strings.TrimSpace(string(data)) != serverID {
			t.Errorf("id = %q, want %s", string(data), serverID)
		}
	})

	t.Run("ListDirThroughSymlink", func(t *testing.T) {
		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation", serverID))
		if err != nil {
			t.Fatalf("Failed to list dir through symlink: %v", err)
		}

		expected := map[string]bool{"ctl": false, "new": false, "fuse_id": false, "all.json": false}
		for _, e := range entries {
			if _, ok := expected[e.Name()]; ok {
				expected[e.Name()] = true
			}
		}
		for name, found := range expected {
			if !found {
				t.Errorf("Expected %q in directory listing through symlink", name)
			}
		}
	})

	t.Run("AccessNestedPathThroughSymlink", func(t *testing.T) {
		// Access last/2.json through symlink
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", serverID, "last", "2.json"))
		if err != nil {
			t.Fatalf("Failed to read last/2.json through symlink: %v", err)
		}

		var msgs []map[string]interface{}
		if err := json.Unmarshal(data, &msgs); err != nil {
			t.Fatalf("Failed to parse JSON: %v", err)
		}
		if len(msgs) != 2 {
			t.Errorf("Expected 2 messages, got %d", len(msgs))
		}
	})

	t.Run("SymlinkAndDirectoryBothWork", func(t *testing.T) {
		// Read the same file via local ID and server ID, should be identical
		dataViaLocal, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", localID, "all.json"))
		if err != nil {
			t.Fatalf("Failed to read via local ID: %v", err)
		}

		dataViaServer, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", serverID, "all.json"))
		if err != nil {
			t.Fatalf("Failed to read via server ID: %v", err)
		}

		if string(dataViaLocal) != string(dataViaServer) {
			t.Errorf("Content differs between local ID and server ID access")
		}
	})

	t.Run("WriteThroughSymlink", func(t *testing.T) {
		// Create a new conversation to test write-through
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
		if err != nil {
			t.Fatalf("Failed to clone: %v", err)
		}
		newLocalID := strings.TrimSpace(string(data))

		// Configure via symlink would not work yet (no server ID), so configure directly
		err = ioutil.WriteFile(filepath.Join(mountPoint, "conversation", newLocalID, "ctl"), []byte("model=predictable"), 0644)
		if err != nil {
			t.Fatalf("Failed to write ctl: %v", err)
		}

		// Send first message to create conversation
		err = ioutil.WriteFile(filepath.Join(mountPoint, "conversation", newLocalID, "new"), []byte("First message"), 0644)
		if err != nil {
			t.Fatalf("Failed to send first message: %v", err)
		}

		// Get server ID
		data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", newLocalID, "id"))
		if err != nil {
			t.Fatalf("Failed to read server ID: %v", err)
		}
		newServerID := strings.TrimSpace(string(data))

		// Note: The predictable model doesn't support follow-up messages,
		// but we can verify the symlink exists and is accessible
		target, err := os.Readlink(filepath.Join(mountPoint, "conversation", newServerID))
		if err != nil {
			t.Fatalf("Readlink failed: %v", err)
		}
		if target != newLocalID {
			t.Errorf("Symlink target = %q, want %q", target, newLocalID)
		}

		// Reading via symlink should work
		dataViaSymlink, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", newServerID, "all.json"))
		if err != nil {
			t.Fatalf("Failed to read via symlink: %v", err)
		}

		var msgs []map[string]interface{}
		if err := json.Unmarshal(dataViaSymlink, &msgs); err != nil {
			t.Fatalf("Failed to parse JSON: %v", err)
		}
		if len(msgs) < 2 {
			t.Errorf("Expected at least 2 messages, got %d", len(msgs))
		}
	})
}

// TestSymlinkEdgeCases tests edge cases in symlink handling.
func TestSymlinkEdgeCases(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	t.Run("NonexistentSymlinkReturnsENOENT", func(t *testing.T) {
		_, err := os.Stat(filepath.Join(mountPoint, "conversation", "nonexistent-id"))
		if err == nil {
			t.Error("Expected error for nonexistent ID")
		}
		if !os.IsNotExist(err) {
			t.Errorf("Expected ENOENT, got %v", err)
		}
	})

	t.Run("LocalIDTakesPriorityOverSymlink", func(t *testing.T) {
		// Clone creates a local ID that's always a directory
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
		if err != nil {
			t.Fatalf("Failed to clone: %v", err)
		}
		localID := strings.TrimSpace(string(data))

		// The local ID should be a directory, not a symlink
		info, err := os.Lstat(filepath.Join(mountPoint, "conversation", localID))
		if err != nil {
			t.Fatalf("Lstat failed: %v", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("Local ID should be a directory, not a symlink")
		}
		if !info.IsDir() {
			t.Errorf("Local ID should be a directory, got mode %v", info.Mode())
		}
	})

	t.Run("MultipleConversationsHaveDistinctSymlinks", func(t *testing.T) {
		// Create two conversations
		var ids []string
		for i := 0; i < 2; i++ {
			data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
			if err != nil {
				t.Fatalf("Failed to clone: %v", err)
			}
			localID := strings.TrimSpace(string(data))

			err = ioutil.WriteFile(filepath.Join(mountPoint, "conversation", localID, "ctl"), []byte("model=predictable"), 0644)
			if err != nil {
				t.Fatalf("Failed to write ctl: %v", err)
			}
			err = ioutil.WriteFile(filepath.Join(mountPoint, "conversation", localID, "new"), []byte(fmt.Sprintf("Message %d", i)), 0644)
			if err != nil {
				t.Fatalf("Failed to send message: %v", err)
			}

			data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", localID, "id"))
			if err != nil {
				t.Fatalf("Failed to read server ID: %v", err)
			}
			serverID := strings.TrimSpace(string(data))
			ids = append(ids, localID, serverID)
		}

		// List conversations
		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
		if err != nil {
			t.Fatalf("Failed to read conversation dir: %v", err)
		}

		// Count directories and symlinks
		var dirs, symlinks int
		for _, e := range entries {
			if e.Mode()&os.ModeSymlink != 0 {
				symlinks++
			} else if e.IsDir() {
				dirs++
			}
		}

		// Should have at least 2 directories (local IDs) and 2 symlinks (server IDs)
		// (might have more from earlier tests)
		if dirs < 2 {
			t.Errorf("Expected at least 2 directories, got %d", dirs)
		}
		if symlinks < 2 {
			t.Errorf("Expected at least 2 symlinks, got %d", symlinks)
		}
		t.Logf("Found %d directories, %d symlinks", dirs, symlinks)
	})
}

// TestStatusFields tests the status fields (now at conversation root).
func TestStatusFields(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// Clone a conversation but don't create it
	var uncreatedID string
	t.Run("UncreatedConversation", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
		if err != nil {
			t.Fatalf("Failed to clone: %v", err)
		}
		uncreatedID = strings.TrimSpace(string(data))
		t.Logf("Uncreated conversation ID: %s", uncreatedID)

		// Conversation directory should be listable with status fields
		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation", uncreatedID))
		if err != nil {
			t.Fatalf("Failed to read conversation directory: %v", err)
		}
		if len(entries) == 0 {
			t.Error("Expected conversation directory to contain files")
		}

		// Check fuse_id is set (was local_id in status/)
		data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", uncreatedID, "fuse_id"))
		if err != nil {
			t.Fatalf("Failed to read fuse_id: %v", err)
		}
		if strings.TrimSpace(string(data)) != uncreatedID {
			t.Errorf("Expected fuse_id=%q, got %q", uncreatedID, strings.TrimSpace(string(data)))
		}

		// Check created is "false"
		data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", uncreatedID, "created"))
		if err != nil {
			t.Fatalf("Failed to read created: %v", err)
		}
		if strings.TrimSpace(string(data)) != "false" {
			t.Errorf("Expected created=false, got %q", strings.TrimSpace(string(data)))
		}

		// Check id returns ENOENT for uncreated conversation
		_, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", uncreatedID, "id"))
		if err == nil {
			t.Error("Expected ENOENT reading id for uncreated conversation")
		}

		// Check message_count is 0 for uncreated conversation
		data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", uncreatedID, "message_count"))
		if err != nil {
			t.Fatalf("Failed to read message_count: %v", err)
		}
		if strings.TrimSpace(string(data)) != "0" {
			t.Errorf("Expected message_count=0 for uncreated conversation, got %q", strings.TrimSpace(string(data)))
		}
	})

	// Verify status fields work through symlinks
	t.Run("StatusViaSymlink", func(t *testing.T) {
		// Create a conversation through the normal flow
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
		if err != nil {
			t.Fatalf("Failed to clone: %v", err)
		}
		localID := strings.TrimSpace(string(data))

		err = ioutil.WriteFile(filepath.Join(mountPoint, "conversation", localID, "ctl"), []byte("model=predictable"), 0644)
		if err != nil {
			t.Fatalf("Failed to write ctl: %v", err)
		}
		err = ioutil.WriteFile(filepath.Join(mountPoint, "conversation", localID, "new"), []byte("Test message"), 0644)
		if err != nil {
			t.Fatalf("Failed to send message: %v", err)
		}

		// Get the server ID (now via id file at conversation root)
		data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", localID, "id"))
		if err != nil {
			t.Fatalf("Failed to read id: %v", err)
		}
		serverID := strings.TrimSpace(string(data))
		if serverID == "" {
			t.Fatal("Expected non-empty id")
		}

		// Access status fields via symlink
		data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", serverID, "fuse_id"))
		if err != nil {
			t.Fatalf("Failed to read fuse_id via symlink: %v", err)
		}
		if strings.TrimSpace(string(data)) != localID {
			t.Errorf("Expected fuse_id=%q via symlink, got %q", localID, strings.TrimSpace(string(data)))
		}

		// Verify message_count > 0
		data, err = ioutil.ReadFile(filepath.Join(mountPoint, "conversation", serverID, "message_count"))
		if err != nil {
			t.Fatalf("Failed to read message_count via symlink: %v", err)
		}
		if strings.TrimSpace(string(data)) == "0" {
			t.Errorf("Expected message_count > 0 via symlink")
		}
	})
}

// TestSymlinkWithSlug tests symlinks for conversation slugs.
func TestSymlinkWithSlug(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)

	// Create a conversation with a slug directly via API
	client := shelley.NewClient(serverURL)
	result, err := client.StartConversation("Test message for slug symlink", "predictable", t.TempDir())
	if err != nil {
		t.Fatalf("Failed to create conversation: %v", err)
	}
	serverID := result.ConversationID
	slug := result.Slug
	t.Logf("Created conversation: serverID=%s, slug=%s", serverID, slug)

	// Mount filesystem
	mountPoint := mountTestFS(t, serverURL)

	// Trigger adoption by listing
	_, err = ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
	if err != nil {
		t.Fatalf("Failed to list conversations: %v", err)
	}

	t.Run("SlugSymlinkAppears", func(t *testing.T) {
		if slug == "" {
			t.Skip("No slug for this conversation")
		}

		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
		if err != nil {
			t.Fatalf("Failed to read conversation dir: %v", err)
		}

		var foundSlug bool
		for _, e := range entries {
			if e.Name() == slug && e.Mode()&os.ModeSymlink != 0 {
				foundSlug = true
				break
			}
		}
		if !foundSlug {
			t.Errorf("Slug symlink %q not found in listing", slug)
			t.Logf("Entries: %v", entryNames(entries))
		}
	})

	t.Run("AccessViaSlugSymlink", func(t *testing.T) {
		if slug == "" {
			t.Skip("No slug for this conversation")
		}

		// Read content through slug symlink
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", slug, "all.json"))
		if err != nil {
			t.Fatalf("Failed to read all.json via slug: %v", err)
		}

		var msgs []map[string]interface{}
		if err := json.Unmarshal(data, &msgs); err != nil {
			t.Fatalf("Failed to parse JSON: %v", err)
		}
		if len(msgs) == 0 {
			t.Error("Expected messages")
		}
	})

	t.Run("SlugAndServerIDPointToSameLocalID", func(t *testing.T) {
		if slug == "" {
			t.Skip("No slug for this conversation")
		}

		targetViaSlug, err := os.Readlink(filepath.Join(mountPoint, "conversation", slug))
		if err != nil {
			t.Fatalf("Readlink via slug failed: %v", err)
		}

		targetViaServerID, err := os.Readlink(filepath.Join(mountPoint, "conversation", serverID))
		if err != nil {
			t.Fatalf("Readlink via server ID failed: %v", err)
		}

		if targetViaSlug != targetViaServerID {
			t.Errorf("Slug and server ID symlinks point to different targets: %s vs %s", targetViaSlug, targetViaServerID)
		}
		t.Logf("Both slug and server ID symlinks point to: %s", targetViaSlug)
	})
}


// TestUnconversedIDsHiddenFromListing verifies that cloned but uncreated conversations
// are hidden from directory listings but still accessible via Lookup.
func TestUnconversedIDsHiddenFromListing(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint, store := mountTestFSWithStore(t, serverURL, time.Hour)

	// Clone a new conversation
	var convID string
	t.Run("Clone", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
		if err != nil {
			t.Fatalf("Failed to read new/clone: %v", err)
		}
		convID = strings.TrimSpace(string(data))
		if len(convID) != 8 {
			t.Fatalf("Expected 8-char hex ID, got %q", convID)
		}
		t.Logf("Cloned conversation ID: %s", convID)
	})

	// Verify the conversation exists in state but is NOT created
	t.Run("VerifyStateUncreated", func(t *testing.T) {
		cs := store.Get(convID)
		if cs == nil {
			t.Fatal("Expected conversation in state")
		}
		if cs.Created {
			t.Error("Expected Created=false for cloned conversation")
		}
	})

	// Verify the conversation does NOT appear in ls /conversation
	t.Run("NotInListing", func(t *testing.T) {
		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
		if err != nil {
			t.Fatalf("Failed to read conversation directory: %v", err)
		}
		for _, e := range entries {
			if e.Name() == convID {
				t.Errorf("Uncreated conversation %s should NOT appear in listing", convID)
			}
		}
		t.Logf("Verified: conversation %s not in listing (entries: %d)", convID, len(entries))
	})

	// Verify the conversation IS accessible via Lookup (can access its files)
	t.Run("AccessibleViaLookup", func(t *testing.T) {
		// Should be able to read ctl
		_, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "ctl"))
		if err != nil {
			t.Fatalf("Failed to read ctl for uncreated conversation: %v", err)
		}

		// Should be able to stat the directory
		info, err := os.Stat(filepath.Join(mountPoint, "conversation", convID))
		if err != nil {
			t.Fatalf("Failed to stat uncreated conversation: %v", err)
		}
		if !info.IsDir() {
			t.Error("Expected conversation to be a directory")
		}
	})

	// Configure and create the conversation
	t.Run("WriteCtlAndNew", func(t *testing.T) {
		ctlPath := filepath.Join(mountPoint, "conversation", convID, "ctl")
		err := ioutil.WriteFile(ctlPath, []byte("model=predictable"), 0644)
		if err != nil {
			t.Fatalf("Failed to write ctl: %v", err)
		}

		newPath := filepath.Join(mountPoint, "conversation", convID, "new")
		err = ioutil.WriteFile(newPath, []byte("Hello from test"), 0644)
		if err != nil {
			t.Fatalf("Failed to write first message: %v", err)
		}
	})

	// Verify the conversation now appears in listing
	t.Run("NowInListing", func(t *testing.T) {
		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
		if err != nil {
			t.Fatalf("Failed to read conversation directory: %v", err)
		}
		found := false
		for _, e := range entries {
			if e.Name() == convID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Created conversation %s should appear in listing", convID)
			t.Logf("Entries: %v", entryNames(entries))
		}
	})

	// Verify state is now marked as created
	t.Run("VerifyStateCreated", func(t *testing.T) {
		cs := store.Get(convID)
		if cs == nil {
			t.Fatal("Expected conversation in state")
		}
		if !cs.Created {
			t.Error("Expected Created=true after writing message")
		}
	})
}

// TestUnconversedIDsCleanup verifies that unconversed IDs are cleaned up after the timeout.
func TestUnconversedIDsCleanup(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	// Use a very short timeout for testing
	mountPoint, store := mountTestFSWithStore(t, serverURL, 100*time.Millisecond)

	// Clone a new conversation
	var convID string
	t.Run("Clone", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
		if err != nil {
			t.Fatalf("Failed to read new/clone: %v", err)
		}
		convID = strings.TrimSpace(string(data))
		t.Logf("Cloned conversation ID: %s", convID)
	})

	// Verify the conversation exists in state
	t.Run("ExistsInState", func(t *testing.T) {
		cs := store.Get(convID)
		if cs == nil {
			t.Fatal("Expected conversation in state")
		}
	})

	// Wait for timeout to expire
	t.Run("WaitForTimeout", func(t *testing.T) {
		time.Sleep(150 * time.Millisecond)
	})

	// Trigger cleanup by reading the conversation directory
	t.Run("TriggerCleanup", func(t *testing.T) {
		_, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
		if err != nil {
			t.Fatalf("Failed to read conversation directory: %v", err)
		}
	})

	// Verify the conversation is gone from state
	t.Run("CleanedUpFromState", func(t *testing.T) {
		cs := store.Get(convID)
		if cs != nil {
			t.Errorf("Expected conversation %s to be cleaned up from state", convID)
		}
	})
}

// TestUnconversedIDsNotCleanedUpBeforeTimeout verifies that unconversed IDs are not
// cleaned up before the timeout expires.
func TestUnconversedIDsNotCleanedUpBeforeTimeout(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	// Use a longer timeout
	mountPoint, store := mountTestFSWithStore(t, serverURL, time.Hour)

	// Clone a new conversation
	var convID string
	t.Run("Clone", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
		if err != nil {
			t.Fatalf("Failed to read new/clone: %v", err)
		}
		convID = strings.TrimSpace(string(data))
		t.Logf("Cloned conversation ID: %s", convID)
	})

	// Trigger Readdir multiple times
	t.Run("MultipleReaddirs", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			_, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
			if err != nil {
				t.Fatalf("Failed to read conversation directory: %v", err)
			}
		}
	})

	// Verify the conversation still exists in state (not cleaned up)
	t.Run("StillInState", func(t *testing.T) {
		cs := store.Get(convID)
		if cs == nil {
			t.Errorf("Conversation %s should not be cleaned up before timeout", convID)
		}
	})
}

// TestCreatedConversationNotCleanedUp verifies that created conversations are never
// cleaned up, even with a short timeout.
func TestCreatedConversationNotCleanedUp(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	// Use a very short timeout
	mountPoint, store := mountTestFSWithStore(t, serverURL, 100*time.Millisecond)

	// Clone and create a conversation
	var convID string
	t.Run("CloneAndCreate", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "new", "clone"))
		if err != nil {
			t.Fatalf("Failed to read new/clone: %v", err)
		}
		convID = strings.TrimSpace(string(data))

		// Configure and create
		ctlPath := filepath.Join(mountPoint, "conversation", convID, "ctl")
		err = ioutil.WriteFile(ctlPath, []byte("model=predictable"), 0644)
		if err != nil {
			t.Fatalf("Failed to write ctl: %v", err)
		}

		newPath := filepath.Join(mountPoint, "conversation", convID, "new")
		err = ioutil.WriteFile(newPath, []byte("Hello"), 0644)
		if err != nil {
			t.Fatalf("Failed to write message: %v", err)
		}
		t.Logf("Created conversation ID: %s", convID)
	})

	// Wait for timeout to expire
	t.Run("WaitForTimeout", func(t *testing.T) {
		time.Sleep(150 * time.Millisecond)
	})

	// Trigger cleanup attempt
	t.Run("TriggerCleanup", func(t *testing.T) {
		_, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
		if err != nil {
			t.Fatalf("Failed to read conversation directory: %v", err)
		}
	})

	// Verify the created conversation still exists
	t.Run("StillInState", func(t *testing.T) {
		cs := store.Get(convID)
		if cs == nil {
			t.Fatal("Created conversation should not be cleaned up")
		}
		if !cs.Created {
			t.Error("Expected conversation to still be marked as created")
		}
	})

	// Verify it still appears in listing
	t.Run("StillInListing", func(t *testing.T) {
		entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation"))
		if err != nil {
			t.Fatalf("Failed to read conversation directory: %v", err)
		}
		found := false
		for _, e := range entries {
			if e.Name() == convID {
				found = true
				break
			}
		}
		if !found {
			t.Error("Created conversation should still appear in listing")
		}
	})
}
