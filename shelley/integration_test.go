package shelley

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestIntegrationWithRealServer tests the client against a real Shelley server
func TestIntegrationWithRealServer(t *testing.T) {
	// Skip if not running in an environment with Shelley server available
	if _, err := exec.LookPath("/usr/local/bin/shelley"); err != nil {
		t.Skip("Shelley binary not found, skipping integration test")
	}

	// Create a temporary directory for the test database
	tmpDir, err := os.MkdirTemp("", "shelley_test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Start a Shelley server with predictable-only mode and clean environment
	cmd := exec.Command("/usr/local/bin/shelley",
		"-db", tmpDir+"/test.db",
		"-predictable-only",
		"serve",
		"-port", "10999",
		"-require-header", "X-Exedev-Userid")
	// Clear environment variables that might interfere with testing
	cmd.Env = append(os.Environ(), "FIREWORKS_API_KEY=", "ANTHROPIC_API_KEY=", "OPENAI_API_KEY=")

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start Shelley server: %v", err)
	}

	// Clean up the process when test completes
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	// Wait for server to start
	if err := waitForServer("http://localhost:10999", 10*time.Second); err != nil {
		t.Fatalf("Server failed to start: %v", err)
	}

	// Create client
	client := NewClient("http://localhost:10999")

	// Test starting a conversation
	result, err := client.StartConversation("Hello, predictable model!", "predictable", tmpDir)
	if err != nil {
		t.Fatalf("Failed to start conversation: %v", err)
	}

	if result.ConversationID == "" {
		t.Error("Expected non-empty conversation ID")
	}

	// Test sending a message
	err = client.SendMessage(result.ConversationID, "How are you?", "predictable")
	if err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}

	// Test getting conversation
	data, err := client.GetConversation(result.ConversationID)
	if err != nil {
		t.Fatalf("Failed to get conversation: %v", err)
	}

	if len(data) == 0 {
		t.Error("Expected conversation data")
	}

	// Test listing conversations
	convData, err := client.ListConversations()
	if err != nil {
		t.Fatalf("Failed to list conversations: %v", err)
	}

	if len(convData) == 0 {
		t.Error("Expected conversation list")
	}
}

// waitForServer waits for a server to respond successfully
func waitForServer(url string, timeout time.Duration) error {
	client := &http.Client{}
	timeoutChan := time.After(timeout)
	tick := time.Tick(100 * time.Millisecond)

	for {
		select {
		case <-timeoutChan:
			return fmt.Errorf("timeout waiting for server at %s", url)
		case <-tick:
			resp, err := client.Get(url)
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				return nil
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
	}
}
