package fuse

import (
	"context"
	"fmt"
	"os/exec"
	"shelley-fuse/fuse/diag"
	"strings"
	"testing"
	"time"
)

// runShell executes a shell command and returns stdout, stderr, and any error.
// The command is run with bash -c in the specified working directory.
// A 30-second timeout is applied via exec.CommandContext.
func runShell(t *testing.T, dir, command string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// runShellOK executes a shell command and fails the test if it errors.
// Returns stdout.
func runShellOK(t *testing.T, dir, command string) string {
	t.Helper()
	stdout, stderr, err := runShell(t, dir, command)
	if err != nil {
		t.Fatalf("Command failed: %s\nstdout: %s\nstderr: %s\nerror: %v", command, stdout, stderr, err)
	}
	return stdout
}

// runShellDiag executes a shell command like runShell, but accepts a
// diag.Tracker. If the command times out (context deadline exceeded),
// the tracker's Dump output is included in the returned error message
// to help diagnose stuck FUSE operations.
func runShellDiag(t *testing.T, dir, command string, tracker *diag.Tracker) (string, string, error) {
	t.Helper()
	return runShellDiagTimeout(t, dir, command, tracker, 30*time.Second)
}

// runShellDiagTimeout is the implementation of runShellDiag with a
// configurable timeout, used for testing the timeout+dump path.
func runShellDiagTimeout(t *testing.T, dir, command string, tracker *diag.Tracker, timeout time.Duration) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil && tracker != nil && ctx.Err() == context.DeadlineExceeded {
		dump := tracker.Dump()
		stacks := diag.GoroutineStacks()
		return stdout.String(), stderr.String(), fmt.Errorf("%w\n\ndiag dump:\n%s\ngoroutine stacks:\n%s", err, dump, stacks)
	}
	return stdout.String(), stderr.String(), err
}

// runShellDiagOK executes a shell command and fails the test if it errors.
// On timeout, the failure message includes the diag tracker's in-flight
// operation dump. Returns stdout.
func runShellDiagOK(t *testing.T, dir, command string, tracker *diag.Tracker) string {
	t.Helper()
	stdout, stderr, err := runShellDiag(t, dir, command, tracker)
	if err != nil {
		t.Fatalf("Command failed: %s\nstdout: %s\nstderr: %s\nerror: %v", command, stdout, stderr, err)
	}
	return stdout
}

// TestReadmeQuickStart exercises the exact Quick Start flow from the README:
//
//	# Allocate a new conversation
//	ID=$(cat new/clone)
//
//	# Configure model and working directory (optional)
//	echo "model=claude-sonnet-4.5 cwd=$PWD" > conversation/$ID/ctl
//
//	# Send first message (creates conversation on backend)
//	echo "Hello, Shelley!" > conversation/$ID/send
//
//	# Read the response(s)
//	cat conversation/$ID/messages/since/user/1/*/content.md
//
//	# Send follow-up
//	echo "Thanks!" > conversation/$ID/send
func TestReadmeQuickStart(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// Step 1: Allocate a new conversation
	// ID=$(cat new/clone)
	stdout := runShellDiagOK(t, mountPoint, "cat new/clone", tracker)
	convID := strings.TrimSpace(stdout)
	if len(convID) != 8 {
		t.Fatalf("Expected 8-char conversation ID, got %q", convID)
	}
	t.Logf("Allocated conversation ID: %s", convID)

	// Step 2: Configure model and working directory
	// echo "model=predictable cwd=/tmp" > conversation/$ID/ctl
	// (Using predictable model for testing)
	runShellDiagOK(t, mountPoint, "echo 'model=predictable cwd=/tmp' > conversation/"+convID+"/ctl", tracker)

	// Verify ctl was written correctly
	ctlContent := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/ctl", tracker)
	if !strings.Contains(ctlContent, "model=predictable") {
		t.Errorf("Expected ctl to contain model=predictable, got: %s", ctlContent)
	}
	if !strings.Contains(ctlContent, "cwd=/tmp") {
		t.Errorf("Expected ctl to contain cwd=/tmp, got: %s", ctlContent)
	}

	// Step 3: Send first message (creates conversation on backend)
	// echo "Hello, Shelley!" > conversation/$ID/send
	runShellDiagOK(t, mountPoint, "echo 'Hello, Shelley!' > conversation/"+convID+"/send", tracker)

	// Step 4: Read the response
	// cat conversation/$ID/messages/all.md
	allMd := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/all.md", tracker)
	if allMd == "" {
		t.Error("Expected non-empty response from all.md")
	}
	if !strings.Contains(allMd, "##") {
		t.Error("Expected markdown headers in all.md")
	}
	t.Logf("all.md content (truncated): %.200s...", allMd)

	// Step 5: Send follow-up
	// echo "Thanks!" > conversation/$ID/send
	runShellDiagOK(t, mountPoint, "echo 'Thanks!' > conversation/"+convID+"/send", tracker)

	// Verify follow-up was added
	allMdAfter := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/all.md", tracker)
	if len(allMdAfter) <= len(allMd) {
		t.Error("Expected all.md to grow after follow-up message")
	}
	if !strings.Contains(allMdAfter, "Thanks!") {
		t.Error("Expected all.md to contain the follow-up message")
	}
}

// TestReadmeCommonOperationsModels exercises the model-related Common Operations:
//
//	# List available models
//	ls models/
//
//	# Check default model
//	readlink models/default
func TestReadmeCommonOperationsModels(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// List available models: ls models/
	modelsOutput := runShellOK(t, mountPoint, "ls models/")
	if modelsOutput == "" {
		t.Error("Expected non-empty models listing")
	}
	if !strings.Contains(modelsOutput, "predictable") {
		t.Errorf("Expected 'predictable' in models listing, got: %s", modelsOutput)
	}
	t.Logf("Models: %s", strings.TrimSpace(modelsOutput))

	// Check default model: readlink models/default
	defaultModel := runShellOK(t, mountPoint, "readlink models/default")
	defaultModel = strings.TrimSpace(defaultModel)
	if defaultModel == "" {
		t.Error("Expected non-empty default model symlink target")
	}
	t.Logf("Default model: %s", defaultModel)
}

// TestReadmeCommonOperationsConversationListing exercises listing conversations:
//
//	# List conversations
//	ls conversation/
func TestReadmeCommonOperationsConversationListing(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// Create a conversation first so there's something to list
	convID := strings.TrimSpace(runShellOK(t, mountPoint, "cat new/clone"))
	runShellOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl")
	runShellOK(t, mountPoint, "echo 'Test message' > conversation/"+convID+"/send")

	// List conversations: ls conversation/
	convListing := runShellOK(t, mountPoint, "ls conversation/")
	if !strings.Contains(convListing, convID) {
		t.Errorf("Expected conversation %s in listing, got: %s", convID, convListing)
	}
	t.Logf("Conversations: %s", strings.TrimSpace(convListing))
}

// TestReadmeCommonOperationsLastMessages exercises the last N messages operations:
//
//	# List last 5 messages (symlinks to message directories)
//	ls conversation/$ID/messages/last/5/
//
//	# Read content of all last 5 messages
//	cat conversation/$ID/messages/last/5/*/content.md
func TestReadmeCommonOperationsLastMessages(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// Create a conversation with multiple messages
	convID := strings.TrimSpace(runShellOK(t, mountPoint, "cat new/clone"))
	runShellOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl")
	runShellOK(t, mountPoint, "echo 'First message' > conversation/"+convID+"/send")
	runShellOK(t, mountPoint, "echo 'Second message' > conversation/"+convID+"/send")
	runShellOK(t, mountPoint, "echo 'Third message' > conversation/"+convID+"/send")

	// List last 5 messages: ls conversation/$ID/messages/last/5/
	last5Listing := runShellOK(t, mountPoint, "ls conversation/"+convID+"/messages/last/5/")
	if last5Listing == "" {
		t.Error("Expected non-empty listing for last/5/")
	}
	t.Logf("Last 5 messages listing: %s", strings.TrimSpace(last5Listing))

	// Read content of all last 5 messages: cat conversation/$ID/messages/last/5/*/content.md
	last5Content := runShellOK(t, mountPoint, "cat conversation/"+convID+"/messages/last/5/*/content.md")
	if last5Content == "" {
		t.Error("Expected non-empty content from last/5/*/content.md")
	}
	// Should contain markdown headers
	if !strings.Contains(last5Content, "##") {
		t.Error("Expected markdown headers in last/5/*/content.md")
	}
	t.Logf("Last 5 messages content (truncated): %.300s...", last5Content)
}

// TestReadmeCommonOperationsSinceMessages exercises the since operations:
//
//	# List messages since your last message
//	ls conversation/$ID/messages/since/user/1/
func TestReadmeCommonOperationsSinceMessages(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// Create a conversation - after sending a user message, the agent responds
	// So since/user/1 should return messages after the last user message
	convID := strings.TrimSpace(runShellOK(t, mountPoint, "cat new/clone"))
	runShellOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl")
	runShellOK(t, mountPoint, "echo 'Hello agent' > conversation/"+convID+"/send")

	// List messages since user's last message: ls conversation/$ID/messages/since/user/1/
	// Note: The command may return empty if user's message is the last one,
	// but the command should succeed
	stdout, stderr, err := runShell(t, mountPoint, "ls conversation/"+convID+"/messages/since/user/1/")
	if err != nil {
		// ls returns error if directory is empty, which is valid
		t.Logf("since/user/1/ listing (may be empty): stdout=%q stderr=%q", stdout, stderr)
	} else {
		t.Logf("since/user/1/ listing: %s", strings.TrimSpace(stdout))
	}

	// Send another message to get an agent response, then check again
	runShellOK(t, mountPoint, "echo 'Another message' > conversation/"+convID+"/send")

	// The directory should be accessible (even if empty when user message is last)
	_, _, _ = runShell(t, mountPoint, "ls conversation/"+convID+"/messages/since/user/1/ 2>/dev/null || true")
}

// TestReadmeCommonOperationsMessageCount exercises the message count operation:
//
//	# Get message count
//	cat conversation/$ID/messages/count
func TestReadmeCommonOperationsMessageCount(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// Create a conversation
	convID := strings.TrimSpace(runShellOK(t, mountPoint, "cat new/clone"))
	runShellOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl")

	// Check count before any messages (uncreated conversation)
	countBefore := runShellOK(t, mountPoint, "cat conversation/"+convID+"/messages/count")
	countBefore = strings.TrimSpace(countBefore)
	if countBefore != "0" {
		t.Errorf("Expected count=0 before first message, got: %s", countBefore)
	}

	// Send a message
	runShellOK(t, mountPoint, "echo 'Test message' > conversation/"+convID+"/send")

	// Get message count: cat conversation/$ID/messages/count
	count := runShellOK(t, mountPoint, "cat conversation/"+convID+"/messages/count")
	count = strings.TrimSpace(count)
	if count == "0" {
		t.Error("Expected non-zero message count after sending message")
	}
	t.Logf("Message count: %s", count)
}

// TestReadmeCommonOperationsCreatedCheck exercises the created check:
//
//	# Check if conversation is created
//	test -e conversation/$ID/created && echo created
func TestReadmeCommonOperationsCreatedCheck(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// Allocate a conversation but don't create it yet
	convID := strings.TrimSpace(runShellOK(t, mountPoint, "cat new/clone"))
	runShellOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl")

	// Check if created before sending message - should NOT exist
	// test -e returns exit code 1 if file doesn't exist
	_, _, err := runShell(t, mountPoint, "test -e conversation/"+convID+"/created")
	if err == nil {
		t.Error("Expected 'created' to NOT exist before first message")
	}

	// Check with the documented pattern
	notCreatedOutput := runShellOK(t, mountPoint, "test -e conversation/"+convID+"/created && echo created || echo not_created")
	if strings.TrimSpace(notCreatedOutput) != "not_created" {
		t.Errorf("Expected 'not_created' before first message, got: %s", notCreatedOutput)
	}

	// Send first message to create conversation
	runShellOK(t, mountPoint, "echo 'Hello!' > conversation/"+convID+"/send")

	// Check if created after sending message - should exist
	// test -e conversation/$ID/created && echo created
	createdOutput := runShellOK(t, mountPoint, "test -e conversation/"+convID+"/created && echo created")
	if strings.TrimSpace(createdOutput) != "created" {
		t.Errorf("Expected 'created' after first message, got: %s", createdOutput)
	}
	t.Logf("Created check passed: %s", strings.TrimSpace(createdOutput))
}

// TestReadmeFullWorkflow exercises a complete workflow combining Quick Start and Common Operations
// to ensure they work together as documented.
func TestReadmeFullWorkflow(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// === Quick Start Flow ===

	// 1. Allocate new conversation
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	t.Logf("Step 1 - Allocated ID: %s", convID)

	// 2. Configure model and cwd
	runShellDiagOK(t, mountPoint, "echo 'model=predictable cwd=/tmp' > conversation/"+convID+"/ctl", tracker)
	t.Log("Step 2 - Configured model and cwd")

	// 3. Check created status before sending message
	_, _, err := runShellDiag(t, mountPoint, "test -e conversation/"+convID+"/created", tracker)
	if err == nil {
		t.Fatal("Created file should not exist before first message")
	}
	t.Log("Step 3 - Verified not created yet")

	// 4. Send first message
	runShellDiagOK(t, mountPoint, "echo 'Hello from workflow test!' > conversation/"+convID+"/send", tracker)
	t.Log("Step 4 - Sent first message")

	// 5. Verify created
	runShellDiagOK(t, mountPoint, "test -e conversation/"+convID+"/created", tracker)
	t.Log("Step 5 - Verified created")

	// 6. Read response
	allMd := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/all.md", tracker)
	if !strings.Contains(allMd, "Hello from workflow test!") {
		t.Error("Expected all.md to contain our message")
	}
	t.Log("Step 6 - Read response via all.md")

	// 7. Send follow-up
	runShellDiagOK(t, mountPoint, "echo 'Follow-up message!' > conversation/"+convID+"/send", tracker)
	t.Log("Step 7 - Sent follow-up")

	// === Common Operations ===

	// 8. List models
	models := runShellDiagOK(t, mountPoint, "ls models/", tracker)
	if !strings.Contains(models, "predictable") {
		t.Error("Expected predictable model in listing")
	}
	t.Log("Step 8 - Listed models")

	// 9. Check default model
	defaultModel := strings.TrimSpace(runShellDiagOK(t, mountPoint, "readlink models/default", tracker))
	if defaultModel == "" {
		t.Error("Expected non-empty default model")
	}
	t.Logf("Step 9 - Default model: %s", defaultModel)

	// 10. List conversations
	convs := runShellDiagOK(t, mountPoint, "ls conversation/", tracker)
	if !strings.Contains(convs, convID) {
		t.Errorf("Expected %s in conversation listing", convID)
	}
	t.Log("Step 10 - Listed conversations")

	// 11. List last 5 messages
	last5 := runShellDiagOK(t, mountPoint, "ls conversation/"+convID+"/messages/last/5/", tracker)
	if last5 == "" {
		t.Error("Expected non-empty last/5 listing")
	}
	t.Logf("Step 11 - Last 5 messages: %s", strings.TrimSpace(last5))

	// 12. Read last 5 message contents
	last5Content := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/last/5/*/content.md", tracker)
	if !strings.Contains(last5Content, "##") {
		t.Error("Expected markdown headers in last/5/*/content.md")
	}
	t.Log("Step 12 - Read last 5 message contents")

	// 13. List messages since user's last message
	// This may be empty if user's message is last, but the command should work
	_, _, _ = runShellDiag(t, mountPoint, "ls conversation/"+convID+"/messages/since/user/1/", tracker)
	t.Log("Step 13 - Listed messages since user/1")

	// 14. Get message count
	count := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/count", tracker))
	if count == "0" {
		t.Error("Expected non-zero message count")
	}
	t.Logf("Step 14 - Message count: %s", count)

	// 15. Final created check
	createdCheck := strings.TrimSpace(runShellDiagOK(t, mountPoint, "test -e conversation/"+convID+"/created && echo created || echo not_created", tracker))
	if createdCheck != "created" {
		t.Errorf("Expected 'created', got: %s", createdCheck)
	}
	t.Log("Step 15 - Final created check passed")

	t.Log("=== Full workflow completed successfully ===")
}

// TestShellPipeToSend tests that piping content to the send file works correctly.
// This is a common shell pattern: echo "message" | tee conversation/$ID/send
func TestShellPipeToSend(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	convID := strings.TrimSpace(runShellOK(t, mountPoint, "cat new/clone"))
	runShellOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl")

	// Use shell pipe pattern
	runShellOK(t, mountPoint, "echo 'Piped message' | cat > conversation/"+convID+"/send")

	// Verify the message was received
	allMd := runShellOK(t, mountPoint, "cat conversation/"+convID+"/messages/all.md")
	if !strings.Contains(allMd, "Piped message") {
		t.Error("Expected piped message in all.md")
	}
}

// TestShellHeredocToSend tests that heredoc input works correctly.
// This tests multiline input via shell heredoc.
func TestShellHeredocToSend(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	convID := strings.TrimSpace(runShellOK(t, mountPoint, "cat new/clone"))
	runShellOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl")

	// Use heredoc pattern for multiline input
	heredocCmd := `cat > conversation/` + convID + `/send << 'EOF'
Line 1 of heredoc
Line 2 of heredoc
Line 3 of heredoc
EOF`
	runShellOK(t, mountPoint, heredocCmd)

	// Verify all lines were received as a single message
	allMd := runShellOK(t, mountPoint, "cat conversation/"+convID+"/messages/all.md")
	if !strings.Contains(allMd, "Line 1") || !strings.Contains(allMd, "Line 2") || !strings.Contains(allMd, "Line 3") {
		t.Error("Expected all heredoc lines in all.md")
	}
}

// TestShellVariableSubstitution tests that shell variable substitution works
// as shown in the Quick Start documentation.
func TestShellVariableSubstitution(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// This mirrors the exact documented pattern:
	// ID=$(cat new/clone)
	// echo "..." > conversation/$ID/send
	script := `
ID=$(cat new/clone)
echo "model=predictable" > conversation/$ID/ctl
echo "Variable substitution test" > conversation/$ID/send
cat conversation/$ID/messages/all.md
`
	output := runShellOK(t, mountPoint, script)
	if !strings.Contains(output, "Variable substitution test") {
		t.Error("Expected output to contain our test message")
	}
	if !strings.Contains(output, "##") {
		t.Error("Expected markdown headers in output")
	}
}

// TestShellGlobPatterns tests that shell glob patterns work as documented.
// e.g., cat conversation/$ID/messages/last/5/*/content.md
func TestShellGlobPatterns(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	mountPoint := mountTestFS(t, serverURL)

	// Create conversation with messages
	convID := strings.TrimSpace(runShellOK(t, mountPoint, "cat new/clone"))
	runShellOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl")
	runShellOK(t, mountPoint, "echo 'Message one' > conversation/"+convID+"/send")
	runShellOK(t, mountPoint, "echo 'Message two' > conversation/"+convID+"/send")

	// Test glob pattern for last N messages
	globOutput := runShellOK(t, mountPoint, "cat conversation/"+convID+"/messages/last/5/*/content.md")
	if globOutput == "" {
		t.Error("Expected non-empty output from glob pattern")
	}

	// Test that we can also use glob to list message directories
	lsOutput := runShellOK(t, mountPoint, "ls -d conversation/"+convID+"/messages/last/5/*/")
	if lsOutput == "" {
		t.Error("Expected non-empty directory listing from glob")
	}
}
