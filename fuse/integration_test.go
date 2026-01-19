package fuse

import (
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hanwen/go-fuse/v2/fs"
	"shelley-fuse/shelley"
	"shelley-fuse/testhelper"
)

// TestIntegrationWithUnixTools tests the FUSE filesystem using standard Unix tools
func TestIntegrationWithUnixTools(t *testing.T) {
	// Skip if fusermount is not available
	if _, err := exec.LookPath("fusermount"); err != nil {
		t.Skip("fusermount not found, skipping integration test")
	}

	// Start a test Shelley server
	server, err := testhelper.StartTestServer(11003, "")
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer server.Stop()

	// Create a temporary directory for the FUSE mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-integration-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Build the FUSE binary
	if err := buildFUSE(); err != nil {
		t.Fatalf("Failed to build FUSE binary: %v", err)
	}

	// Create mount point
	mountPoint := filepath.Join(tmpDir, "mount")
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		t.Fatalf("Failed to create mount point: %v", err)
	}

	// Start FUSE filesystem in-process for better error reporting
	fuseMount, err := testhelper.StartFUSEInProcess(mountPoint, "http://localhost:11003", func(serverURL string) (fs.InodeEmbedder, error) {
		client := shelley.NewClient(serverURL)
		return NewFS(client), nil
	})
	if err != nil {
		t.Fatalf("Failed to start FUSE: %v", err)
	}

	// Clean up
	defer func() {
		fuseMount.Stop()
	}()

	// Test 1: List root directory
	t.Run("ListRootDirectory", func(t *testing.T) {
		entries, err := ioutil.ReadDir(mountPoint)
		if err != nil {
			t.Fatalf("Failed to read root directory: %v", err)
		}

		if len(entries) == 0 {
			t.Error("Expected at least one entry in root directory")
		}

		foundModels := false
		foundNew := false
		for _, entry := range entries {
			if entry.Name() == "models" && !entry.IsDir() {
				foundModels = true
			}
			if entry.Name() == "new" && entry.IsDir() {
				foundNew = true
			}
		}

		if !foundModels {
			t.Error("Expected 'models' file in root")
		}
		if !foundNew {
			t.Error("Expected 'new' directory in root")
		}
	})

	// Test 2: List root directory entries
	t.Run("ListRootEntries", func(t *testing.T) {
		entries, err := ioutil.ReadDir(mountPoint)
		if err != nil {
			t.Fatalf("Failed to read root directory: %v", err)
		}

		expectedEntries := map[string]bool{
			"models": false,
			"new":    false,
			"model":  false,
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
	})

	// Test 3: Read models file
	t.Run("ReadModelsFile", func(t *testing.T) {
		modelsPath := filepath.Join(mountPoint, "models")
		
		// Check if it exists and is a file
		stat, err := os.Stat(modelsPath)
		if err != nil {
			t.Fatalf("Failed to stat models file: %v", err)
		}
		
		if stat.IsDir() {
			t.Error("models should be a file, not a directory")
		}

		// Read the models file
		content, err := ioutil.ReadFile(modelsPath)
		if err != nil {
			t.Fatalf("Failed to read models file: %v", err)
		}

		// Should contain JSON with models
		if len(content) == 0 {
			t.Error("Expected non-empty models content")
		}

		contentStr := string(content)
		if !strings.Contains(contentStr, "predictable") {
			t.Error("Expected 'predictable' model in models content")
		}
	})

	// Test 4: Create a new conversation using .first
	t.Run("CreateNewConversation", func(t *testing.T) {
		// First check if the directory exists
		newDirPath := filepath.Join(mountPoint, "new", "test_cwd")
		stat, err := os.Stat(newDirPath)
		if err != nil {
			t.Logf("Directory %s does not exist: %v", newDirPath, err)
		} else if !stat.IsDir() {
			t.Errorf("Path %s is not a directory", newDirPath)
		} else {
			t.Logf("Directory %s exists", newDirPath)
			
			// List directory contents
			entries, err := ioutil.ReadDir(newDirPath)
			if err != nil {
				t.Logf("Failed to read directory: %v", err)
			} else {
				t.Logf("Directory contents:")
				for _, entry := range entries {
					t.Logf("  %s (mode: %o)", entry.Name(), entry.Mode())
				}
			}
		}
		
		newConvPath := filepath.Join(mountPoint, "new", "test_cwd", ".first")
		
		// Write to the .first file to start conversation
		message := "Hello from integration test!"
		err = ioutil.WriteFile(newConvPath, []byte(message), 0666)
		if err != nil {
			t.Fatalf("Failed to write to .first file: %v", err)
		}

		// The write should succeed, indicating the conversation was created
		t.Log("Successfully created new conversation")

		// Test reading .dir to get conversation directory
		dirPath := filepath.Join(mountPoint, "new", "test_cwd", ".dir")
		dirData, err := ioutil.ReadFile(dirPath)
		if err != nil {
			t.Fatalf("Failed to read .dir file: %v", err)
		}

		// Should contain the conversation directory path
		if len(dirData) == 0 {
			t.Error("Expected non-empty .dir content")
		}
		t.Logf("Conversation directory: %s", string(dirData))
	})

	// Test 5: Test conversation directory operations
	t.Run("TestConversationDirectory", func(t *testing.T) {
		// First create a conversation
		newConvPath := filepath.Join(mountPoint, "new", "test_cwd2", ".first")
		message := "Help me write a Go program"
		err := ioutil.WriteFile(newConvPath, []byte(message), 0666)
		if err != nil {
			t.Fatalf("Failed to write to .first file: %v", err)
		}

		// Get the conversation directory
		dirPath := filepath.Join(mountPoint, "new", "test_cwd2", ".dir")
		dirData, err := ioutil.ReadFile(dirPath)
		if err != nil {
			t.Fatalf("Failed to read .dir file: %v", err)
		}

		convDir := strings.TrimSpace(string(dirData))
		if convDir == "" {
			t.Fatal("Empty conversation directory from .dir")
		}

		// Parse conversation ID from the path (last component)
		parts := strings.Split(convDir, "/")
		if len(parts) == 0 {
			t.Fatal("Invalid conversation directory path")
		}
		convID := parts[len(parts)-1]
		_ = convID // convID is not used yet but might be useful later

		// Test reading the 'all' file
		allPath := filepath.Join(mountPoint, convDir, "all")
		_, err = ioutil.ReadFile(allPath)
		if err != nil {
			t.Fatalf("Failed to read 'all' file: %v", err)
		}

		// Test reading the 'last-response' file
		lrPath := filepath.Join(mountPoint, convDir, "last-response")
		_, err = ioutil.ReadFile(lrPath)
		if err != nil {
			t.Fatalf("Failed to read 'last-response' file: %v", err)
		}

		// Test writing to the 'all' file (sending a follow-up message)
		followUp := "Add error handling"
		err = ioutil.WriteFile(allPath, []byte(followUp), 0666)
		if err != nil {
			t.Fatalf("Failed to write to 'all' file: %v", err)
		}

		t.Log("Successfully tested conversation directory operations")
	})
}



// buildFUSE builds the shelley-fuse binary
func buildFUSE() error {
	// Build from project root
	cmd := exec.Command("go", "build", "-o", "bin/shelley-fuse", "./cmd/shelley-fuse")
	cmd.Dir = "/home/exedev/shelley-fuse" // Use absolute path to project root
	return cmd.Run()
}