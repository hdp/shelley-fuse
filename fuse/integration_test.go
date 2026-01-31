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

	shelleyFS := NewFS(client, store)

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

	return mountPoint
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

	// 2. Read models file
	t.Run("ReadModels", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "models"))
		if err != nil {
			t.Fatalf("Failed to read models: %v", err)
		}
		if !strings.Contains(string(data), "predictable") {
			t.Error("Expected 'predictable' in models output")
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

	// 6. Read status.json before conversation is created
	t.Run("StatusBeforeCreate", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "status.json"))
		if err != nil {
			t.Fatalf("Failed to read status.json: %v", err)
		}
		var status map[string]interface{}
		if err := json.Unmarshal(data, &status); err != nil {
			t.Fatalf("Failed to parse status.json: %v", err)
		}
		if status["created"] != false {
			t.Errorf("Expected created=false before first message, got %v", status["created"])
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

	// 8. Read status.json after conversation is created
	t.Run("StatusAfterCreate", func(t *testing.T) {
		data, err := ioutil.ReadFile(filepath.Join(mountPoint, "conversation", convID, "status.json"))
		if err != nil {
			t.Fatalf("Failed to read status.json: %v", err)
		}
		var status map[string]interface{}
		if err := json.Unmarshal(data, &status); err != nil {
			t.Fatalf("Failed to parse status.json: %v", err)
		}
		if status["created"] != true {
			t.Errorf("Expected created=true, got %v", status["created"])
		}
		if _, ok := status["shelley_conversation_id"]; !ok {
			t.Error("Expected shelley_conversation_id in status")
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
}
