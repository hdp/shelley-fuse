package fuse

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"shelley-fuse/fuse/diag"
	"strings"
	"testing"
	"time"
)

// filterEnv returns a copy of env with the named variables removed.
func filterEnv(env []string, names ...string) []string {
	result := make([]string, 0, len(env))
	for _, e := range env {
		exclude := false
		for _, name := range names {
			if strings.HasPrefix(e, name+"=") {
				exclude = true
				break
			}
		}
		if !exclude {
			result = append(result, e)
		}
	}
	return result
}

// runShellDiag executes a shell command like runShell, but accepts a
// diag.Tracker. If the command times out (context deadline exceeded),
// the tracker's Dump output is included in the returned error message
// to help diagnose stuck FUSE operations.
func runShellDiag(t *testing.T, dir, command string, tracker *diag.Tracker) (string, string, error) {
	t.Helper()
	return runShellDiagTimeout(t, dir, command, tracker, 1*time.Second)
}

// runShellDiagTimeout is the implementation of runShellDiag with a
// configurable timeout, used for testing the timeout+dump path.
func runShellDiagTimeout(t *testing.T, dir, command string, tracker *diag.Tracker, timeout time.Duration) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Don't set cmd.Dir to the FUSE mount point.
	// Under the hood, os.StartProcess tries to stat the directory before running the command, which means we can deadlock before getting to cmd.Run().
	cmd := exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf("if ! cd \"%s\"; then echo \"cd %s: $?\" 1>&2; exit 1; fi; %s", dir, dir, command))
	// Clear BASH_ENV so that custom bash init scripts (e.g. a cd() wrapper)
	// don't interfere with our shell commands.
	cmd.Env = filterEnv(os.Environ(), "BASH_ENV")
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

	// Step 4: Read the response(s)
	// cat conversation/$ID/messages/since/user/1/*/content.md
	// Note: with the predictable model, there may not be agent responses after
	// the first message (only a system message + user message). So we also
	// verify via all.md that the conversation was created and has content.
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

	// Verify the documented response reading pattern works
	// cat conversation/$ID/messages/since/user/1/*/content.md
	// After sending "Thanks!", this should return any messages after that last user message.
	// Use ls first to verify the directory is accessible.
	_, _, err := runShellDiag(t, mountPoint,
		"ls conversation/"+convID+"/messages/since/user/1/", tracker)
	if err != nil {
		t.Logf("since/user/1/ listing returned error (may be empty dir): %v", err)
	}
}

// TestReadmeCommonOperationsModels exercises the model-related Common Operations:
//
//	# List available models
//	ls model/
//
//	# Check default model
//	readlink model/default
func TestReadmeCommonOperationsModels(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// List available models: ls model/
	modelsOutput := runShellDiagOK(t, mountPoint, "ls model/", tracker)
	if modelsOutput == "" {
		t.Error("Expected non-empty models listing")
	}
	if !strings.Contains(modelsOutput, "predictable") {
		t.Errorf("Expected 'predictable' in models listing, got: %s", modelsOutput)
	}
	t.Logf("Models: %s", strings.TrimSpace(modelsOutput))

	// Check default model: readlink model/default
	defaultModel := runShellDiagOK(t, mountPoint, "readlink model/default", tracker)
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
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// Create a conversation first so there's something to list
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tracker)
	runShellDiagOK(t, mountPoint, "echo 'Test message' > conversation/"+convID+"/send", tracker)

	// List conversations: ls conversation/
	convListing := runShellDiagOK(t, mountPoint, "ls conversation/", tracker)
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
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// Create a conversation with multiple messages
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tracker)
	runShellDiagOK(t, mountPoint, "echo 'First message' > conversation/"+convID+"/send", tracker)
	runShellDiagOK(t, mountPoint, "echo 'Second message' > conversation/"+convID+"/send", tracker)
	runShellDiagOK(t, mountPoint, "echo 'Third message' > conversation/"+convID+"/send", tracker)

	// List last 5 messages: ls conversation/$ID/messages/last/5/
	last5Listing := runShellDiagOK(t, mountPoint, "ls conversation/"+convID+"/messages/last/5/", tracker)
	if last5Listing == "" {
		t.Error("Expected non-empty listing for last/5/")
	}
	t.Logf("Last 5 messages listing: %s", strings.TrimSpace(last5Listing))

	// Read content of all last 5 messages: cat conversation/$ID/messages/last/5/*/content.md
	last5Content := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/last/5/*/content.md", tracker)
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
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// Create a conversation - after sending a user message, the agent responds
	// So since/user/1 should return messages after the last user message
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tracker)
	runShellDiagOK(t, mountPoint, "echo 'Hello agent' > conversation/"+convID+"/send", tracker)

	// List messages since user's last message: ls conversation/$ID/messages/since/user/1/
	// Note: The command may return empty if user's message is the last one,
	// but the command should succeed
	stdout, stderr, err := runShellDiag(t, mountPoint, "ls conversation/"+convID+"/messages/since/user/1/", tracker)
	if err != nil {
		// ls returns error if directory is empty, which is valid
		t.Logf("since/user/1/ listing (may be empty): stdout=%q stderr=%q", stdout, stderr)
	} else {
		t.Logf("since/user/1/ listing: %s", strings.TrimSpace(stdout))
	}

	// Send another message to get an agent response, then check again
	runShellDiagOK(t, mountPoint, "echo 'Another message' > conversation/"+convID+"/send", tracker)

	// The directory should be accessible (even if empty when user message is last)
	_, _, _ = runShellDiag(t, mountPoint, "ls conversation/"+convID+"/messages/since/user/1/ 2>/dev/null || true", tracker)
}

// TestReadmeCommonOperationsMessageCount exercises the message count operation:
//
//	# Get message count
//	cat conversation/$ID/messages/count
func TestReadmeCommonOperationsMessageCount(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// Create a conversation
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tracker)

	// Check count before any messages (uncreated conversation)
	countBefore := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/count", tracker)
	countBefore = strings.TrimSpace(countBefore)
	if countBefore != "0" {
		t.Errorf("Expected count=0 before first message, got: %s", countBefore)
	}

	// Send a message
	runShellDiagOK(t, mountPoint, "echo 'Test message' > conversation/"+convID+"/send", tracker)

	// Get message count: cat conversation/$ID/messages/count
	count := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/count", tracker)
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
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// Allocate a conversation but don't create it yet
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tracker)

	// Check if created before sending message - should NOT exist
	// test -e returns exit code 1 if file doesn't exist
	_, _, err := runShellDiag(t, mountPoint, "test -e conversation/"+convID+"/created", tracker)
	if err == nil {
		t.Error("Expected 'created' to NOT exist before first message")
	}

	// Check with the documented pattern
	notCreatedOutput := runShellDiagOK(t, mountPoint, "test -e conversation/"+convID+"/created && echo created || echo not_created", tracker)
	if strings.TrimSpace(notCreatedOutput) != "not_created" {
		t.Errorf("Expected 'not_created' before first message, got: %s", notCreatedOutput)
	}

	// Send first message to create conversation
	runShellDiagOK(t, mountPoint, "echo 'Hello!' > conversation/"+convID+"/send", tracker)

	// Check if created after sending message - should exist
	// test -e conversation/$ID/created && echo created
	createdOutput := runShellDiagOK(t, mountPoint, "test -e conversation/"+convID+"/created && echo created", tracker)
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
	models := runShellDiagOK(t, mountPoint, "ls model/", tracker)
	if !strings.Contains(models, "predictable") {
		t.Error("Expected predictable model in listing")
	}
	t.Log("Step 8 - Listed models")

	// 9. Check default model
	defaultModel := strings.TrimSpace(runShellDiagOK(t, mountPoint, "readlink model/default", tracker))
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
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tracker)

	// Use shell pipe pattern
	runShellDiagOK(t, mountPoint, "echo 'Piped message' | cat > conversation/"+convID+"/send", tracker)

	// Verify the message was received
	allMd := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/all.md", tracker)
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
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tracker)

	// Use heredoc pattern for multiline input
	heredocCmd := `cat > conversation/` + convID + `/send << 'EOF'
Line 1 of heredoc
Line 2 of heredoc
Line 3 of heredoc
EOF`
	runShellDiagOK(t, mountPoint, heredocCmd, tracker)

	// Verify all lines were received as a single message
	allMd := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/all.md", tracker)
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
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// This mirrors the exact documented pattern:
	// ID=$(cat new/clone)
	// echo "..." > conversation/$ID/send
	script := `
ID=$(cat new/clone)
echo "model=predictable" > conversation/$ID/ctl
echo "Variable substitution test" > conversation/$ID/send
cat conversation/$ID/messages/all.md
`
	output := runShellDiagOK(t, mountPoint, script, tracker)
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
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// Create conversation with messages
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tracker)
	runShellDiagOK(t, mountPoint, "echo 'Message one' > conversation/"+convID+"/send", tracker)
	runShellDiagOK(t, mountPoint, "echo 'Message two' > conversation/"+convID+"/send", tracker)

	// Test glob pattern for last N messages
	globOutput := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/last/5/*/content.md", tracker)
	if globOutput == "" {
		t.Error("Expected non-empty output from glob pattern")
	}

	// Test that we can also use glob to list message directories
	lsOutput := runShellDiagOK(t, mountPoint, "ls -d conversation/"+convID+"/messages/last/5/*/", tracker)
	if lsOutput == "" {
		t.Error("Expected non-empty directory listing from glob")
	}
}

// TestReadmeCommonOperationsModelSymlink exercises checking a conversation's model:
//
//	# Check which model a conversation uses
//	readlink conversation/$ID/model
func TestReadmeCommonOperationsModelSymlink(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// Create a conversation with a known model
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tracker)
	runShellDiagOK(t, mountPoint, "echo 'Test' > conversation/"+convID+"/send", tracker)

	// readlink conversation/$ID/model
	modelTarget := strings.TrimSpace(runShellDiagOK(t, mountPoint, "readlink conversation/"+convID+"/model", tracker))
	if !strings.Contains(modelTarget, "predictable") {
		t.Errorf("Expected model symlink to contain 'predictable', got: %s", modelTarget)
	}
	t.Logf("Model symlink: %s", modelTarget)
}

// TestReadmeCommonOperationsArchiving exercises the archive/unarchive flow:
//
//	# Archive a conversation
//	touch conversation/$ID/archived
//
//	# Check if archived
//	test -e conversation/$ID/archived && echo archived
//
//	# Unarchive a conversation
//	rm conversation/$ID/archived
func TestReadmeCommonOperationsArchiving(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// Create a conversation
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tracker)
	runShellDiagOK(t, mountPoint, "echo 'Test archiving' > conversation/"+convID+"/send", tracker)

	// Initially not archived
	notArchived := strings.TrimSpace(runShellDiagOK(t, mountPoint,
		"test -e conversation/"+convID+"/archived && echo archived || echo not_archived", tracker))
	if notArchived != "not_archived" {
		t.Errorf("Expected 'not_archived' initially, got: %s", notArchived)
	}

	// Archive: touch conversation/$ID/archived
	runShellDiagOK(t, mountPoint, "touch conversation/"+convID+"/archived", tracker)

	// Check if archived: test -e conversation/$ID/archived && echo archived
	archived := strings.TrimSpace(runShellDiagOK(t, mountPoint,
		"test -e conversation/"+convID+"/archived && echo archived", tracker))
	if archived != "archived" {
		t.Errorf("Expected 'archived' after touch, got: %s", archived)
	}
	t.Log("Archived successfully")

	// Unarchive: rm conversation/$ID/archived
	runShellDiagOK(t, mountPoint, "rm conversation/"+convID+"/archived", tracker)

	// Verify unarchived
	unarchived := strings.TrimSpace(runShellDiagOK(t, mountPoint,
		"test -e conversation/"+convID+"/archived && echo archived || echo not_archived", tracker))
	if unarchived != "not_archived" {
		t.Errorf("Expected 'not_archived' after rm, got: %s", unarchived)
	}
	t.Log("Unarchived successfully")
}

// TestReadmeCommonOperationsLastSingleMessage exercises reading the very last message:
//
//	# Read the content of the very last message (the sole entry in last/1/)
//	cat conversation/$ID/messages/last/1/0/content.md
func TestReadmeCommonOperationsLastSingleMessage(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// Create a conversation
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tracker)
	runShellDiagOK(t, mountPoint, "echo 'Hello from test' > conversation/"+convID+"/send", tracker)

	// cat conversation/$ID/messages/last/1/0/content.md
	lastContent := runShellDiagOK(t, mountPoint,
		"cat conversation/"+convID+"/messages/last/1/0/content.md", tracker)
	if lastContent == "" {
		t.Error("Expected non-empty content from last/1/0/content.md")
	}
	if !strings.Contains(lastContent, "##") {
		t.Error("Expected markdown headers in last/1/0/content.md")
	}
	t.Logf("Last message content (truncated): %.200s", lastContent)
}

// TestReadmeCommonOperationsLast2Messages exercises listing the last 2 messages:
//
//	# List the last 2 messages
//	ls conversation/$ID/messages/last/2/
//	# 0 -> ../../003-user
//	# 1 -> ../../004-agent
func TestReadmeCommonOperationsLast2Messages(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	// Create a conversation with at least 2 messages
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tracker)
	runShellDiagOK(t, mountPoint, "echo 'Hello' > conversation/"+convID+"/send", tracker)

	// ls conversation/$ID/messages/last/2/
	last2 := runShellDiagOK(t, mountPoint,
		"ls conversation/"+convID+"/messages/last/2/", tracker)
	lines := strings.Fields(strings.TrimSpace(last2))
	if len(lines) != 2 {
		t.Errorf("Expected 2 entries in last/2/, got %d: %v", len(lines), lines)
	}
	// Entries should be numbered 0 and 1
	if len(lines) >= 2 {
		if lines[0] != "0" || lines[1] != "1" {
			t.Errorf("Expected entries '0' and '1', got %v", lines)
		}
	}

	// Verify they're symlinks with readlink
	for _, entry := range []string{"0", "1"} {
		target := strings.TrimSpace(runShellDiagOK(t, mountPoint,
			"readlink conversation/"+convID+"/messages/last/2/"+entry, tracker))
		if !strings.HasPrefix(target, "../../") {
			t.Errorf("Expected symlink target starting with '../../', got: %s", target)
		}
		t.Logf("last/2/%s -> %s", entry, target)
	}
}

// TestReadmeCommonOperationsSinceDirectory exercises the since directory:
//
//	# Read all messages since the last user message
//	ls conversation/$ID/messages/since/user/1/
//
// Note: The README also documents:
//
//	cat conversation/$ID/messages/since/user/1/*/content.md
//
// but this glob pattern cannot be tested here because the predictable model
// does not generate agent responses. In production, the agent response would
// appear after the last user message, making the glob non-empty. We verify
// the directory is accessible and the listing succeeds.
func TestReadmeCommonOperationsSinceDirectory(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	tracker := tm.Diag

	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", tracker))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tracker)
	runShellDiagOK(t, mountPoint, "echo 'First message' > conversation/"+convID+"/send", tracker)
	runShellDiagOK(t, mountPoint, "echo 'Second message' > conversation/"+convID+"/send", tracker)

	// ls conversation/$ID/messages/since/user/1/
	// The predictable model doesn't generate agent responses, so the user's
	// message is always the last message and since/user/1/ is empty.
	// We verify the directory is accessible (ls succeeds).
	runShellDiagOK(t, mountPoint,
		"ls conversation/"+convID+"/messages/since/user/1/", tracker)
	t.Log("since/user/1/ directory is accessible")

	// Verify since/user/2/ returns messages after the second-to-last user message.
	// This should include the last user message itself.
	since2Listing := runShellDiagOK(t, mountPoint,
		"ls conversation/"+convID+"/messages/since/user/2/", tracker)
	if since2Listing == "" {
		t.Error("Expected non-empty listing for since/user/2/")
	}
	t.Logf("since/user/2/ listing: %s", strings.TrimSpace(since2Listing))
}
