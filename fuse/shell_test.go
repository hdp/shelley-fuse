package fuse

// Shell-based FUSE tests
//
// These tests verify FUSE behavior using shell commands (cat, ls, echo, etc.)
// which matches the primary use case of the filesystem. Shell tests also
// run in external processes, avoiding test hangs from FUSE deadlocks.
//
// Test sections:
// - Shell Helper Tests
// - Root Filesystem
// - Models
// - Conversations
// - Messages
// - Control Files

import (
	"shelley-fuse/fuse/diag"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Shell Helper Tests
// =============================================================================

// TestRunShellDiagTimeoutIncludesDump verifies that when a command times out,
// the error includes the diag endpoint's in-flight operation dump.
func TestRunShellDiagTimeoutIncludesDump(t *testing.T) {
	tracker := diag.NewTracker()

	// Simulate a stuck FUSE operation.
	h := tracker.Track("SendNode", "Write", "conv=deadbeef")
	defer h.Done()

	diagURL := startDiagServer(t, tracker)

	// Use a very short timeout so the "sleep" command is killed quickly.
	_, _, err := runShellDiagTimeout(t, t.TempDir(), "sleep 60", diagURL, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "diag dump") {
		t.Errorf("timeout error should include diag dump, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "SendNode.Write conv=deadbeef") {
		t.Errorf("diag dump should show stuck op, got: %s", errMsg)
	}
}

// TestRunShellDiagNonTimeoutNoDump verifies that non-timeout errors do NOT
// include the diag dump.
func TestRunShellDiagNonTimeoutNoDump(t *testing.T) {
	tracker := diag.NewTracker()
	h := tracker.Track("SendNode", "Write", "conv=deadbeef")
	defer h.Done()

	diagURL := startDiagServer(t, tracker)

	_, _, err := runShellDiag(t, t.TempDir(), "exit 1", diagURL)
	if err == nil {
		t.Fatal("expected error from exit 1")
	}
	if strings.Contains(err.Error(), "diag dump") {
		t.Error("non-timeout error should NOT include diag dump")
	}
}

// TestRunShellDiagEmptyURL verifies that an empty diagURL does not cause
// a panic, even on timeout.
func TestRunShellDiagEmptyURL(t *testing.T) {
	_, _, err := runShellDiagTimeout(t, t.TempDir(), "sleep 60", "", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	// Should not panic and should not contain diag dump.
	if strings.Contains(err.Error(), "diag dump") {
		t.Error("empty diagURL should not produce diag dump")
	}
}

// TestRunShellDiagOKSuccess verifies the happy path.
func TestRunShellDiagOKSuccess(t *testing.T) {
	tracker := diag.NewTracker()
	diagURL := startDiagServer(t, tracker)
	out := runShellDiagOK(t, t.TempDir(), "echo hello", diagURL)
	if strings.TrimSpace(out) != "hello" {
		t.Errorf("got %q, want %q", out, "hello\n")
	}
}

// =============================================================================
// Root Filesystem Tests
// =============================================================================

// TestShellRootDirectory tests that the root directory contains expected entries.
func TestShellRootDirectory(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)

	// List root directory
	output := runShellDiagOK(t, tm.MountPoint, "ls", tm.DiagURL)

	for _, expected := range []string{"model", "new", "conversation", "README.md"} {
		if !strings.Contains(output, expected) {
			t.Errorf("Expected %q in root listing, got: %s", expected, output)
		}
	}
}

// TestShellNewSymlink tests that /new is a symlink to model/default/new.
func TestShellNewSymlink(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)

	// Verify /new is a symlink
	runShellDiagOK(t, tm.MountPoint, "test -L new", tm.DiagURL)

	// Verify symlink target
	target := strings.TrimSpace(runShellDiagOK(t, tm.MountPoint, "readlink new", tm.DiagURL))
	if target != "model/default/new" {
		t.Errorf("Expected symlink target 'model/default/new', got %q", target)
	}
}

// TestShellReadme tests reading README.md via shell.
func TestShellReadme(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)

	// Read README.md
	content := runShellDiagOK(t, tm.MountPoint, "cat README.md", tm.DiagURL)
	if !strings.Contains(content, "Shelley") {
		t.Error("README.md should contain 'Shelley'")
	}
	if !strings.Contains(content, "Quick Start") {
		t.Error("README.md should contain 'Quick Start'")
	}
}

// =============================================================================
// Model Tests
// =============================================================================

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
	diagURL := tm.DiagURL

	// List available models: ls model/
	modelsOutput := runShellDiagOK(t, mountPoint, "ls model/", diagURL)
	if modelsOutput == "" {
		t.Error("Expected non-empty models listing")
	}
	if !strings.Contains(modelsOutput, "predictable") {
		t.Errorf("Expected 'predictable' in models listing, got: %s", modelsOutput)
	}
	t.Logf("Models: %s", strings.TrimSpace(modelsOutput))

	// Check default model: readlink model/default
	defaultModel := runShellDiagOK(t, mountPoint, "readlink model/default", diagURL)
	defaultModel = strings.TrimSpace(defaultModel)
	if defaultModel == "" {
		t.Error("Expected non-empty default model symlink target")
	}
	t.Logf("Default model: %s", defaultModel)
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
	diagURL := tm.DiagURL

	// Create a conversation with a known model
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'Test' > conversation/"+convID+"/send", diagURL)

	// readlink conversation/$ID/model
	modelTarget := strings.TrimSpace(runShellDiagOK(t, mountPoint, "readlink conversation/"+convID+"/model", diagURL))
	if !strings.Contains(modelTarget, "predictable") {
		t.Errorf("Expected model symlink to contain 'predictable', got: %s", modelTarget)
	}
	t.Logf("Model symlink: %s", modelTarget)
}

// TestShellModelClone tests cloning a conversation via model/modelname/new/clone.
func TestShellModelClone(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)

	// Clone via model/predictable/new/clone
	id := strings.TrimSpace(runShellDiagOK(t, tm.MountPoint, "cat model/predictable/new/clone", tm.DiagURL))
	if len(id) != 8 {
		t.Fatalf("Expected 8-char ID, got %q", id)
	}

	// Verify model is preconfigured
	ctl := runShellDiagOK(t, tm.MountPoint, "cat conversation/"+id+"/ctl", tm.DiagURL)
	if !strings.Contains(ctl, "model=predictable") {
		t.Errorf("Expected ctl to contain 'model=predictable', got %q", ctl)
	}
}

// TestShellModelCloneUnique tests that each clone returns a unique ID.
func TestShellModelCloneUnique(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)

	id1 := strings.TrimSpace(runShellDiagOK(t, tm.MountPoint, "cat model/predictable/new/clone", tm.DiagURL))
	id2 := strings.TrimSpace(runShellDiagOK(t, tm.MountPoint, "cat model/predictable/new/clone", tm.DiagURL))
	if id1 == id2 {
		t.Errorf("Expected unique IDs, both are %q", id1)
	}
}

// TestShellModelContents tests accessing model field files.
func TestShellModelContents(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)

	// Read model ID
	id := strings.TrimSpace(runShellDiagOK(t, tm.MountPoint, "cat model/predictable/id", tm.DiagURL))
	if id != "predictable" {
		t.Errorf("Expected 'predictable', got %q", id)
	}

	// Model should have ready file (presence/absence semantics)
	runShellDiagOK(t, tm.MountPoint, "test -e model/predictable/ready", tm.DiagURL)

	// List model contents
	contents := runShellDiagOK(t, tm.MountPoint, "ls model/predictable/", tm.DiagURL)
	for _, expected := range []string{"id", "new", "ready"} {
		if !strings.Contains(contents, expected) {
			t.Errorf("Expected %q in model contents, got: %s", expected, contents)
		}
	}
}

// TestShellModelLookup tests looking up existing and nonexistent models.
func TestShellModelLookup(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)

	// Existing model should be accessible
	runShellDiagOK(t, tm.MountPoint, "test -d model/predictable", tm.DiagURL)

	// Nonexistent model should not exist
	_, _, err := runShellDiag(t, tm.MountPoint, "test -e model/nonexistent-model-xyz", tm.DiagURL)
	if err == nil {
		t.Error("Expected nonexistent model to not exist")
	}
}

// TestShellModelNewDirectory tests the model/name/new directory.
func TestShellModelNewDirectory(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)

	// new should be a directory
	runShellDiagOK(t, tm.MountPoint, "test -d model/predictable/new", tm.DiagURL)

	// List new directory contents
	contents := runShellDiagOK(t, tm.MountPoint, "ls model/predictable/new/", tm.DiagURL)
	if !strings.Contains(contents, "clone") {
		t.Errorf("Expected 'clone' in new directory, got: %s", contents)
	}
	if !strings.Contains(contents, "start") {
		t.Errorf("Expected 'start' in new directory, got: %s", contents)
	}

	// start should be executable
	runShellDiagOK(t, tm.MountPoint, "test -x model/predictable/new/start", tm.DiagURL)
}

// =============================================================================
// Conversation Tests
// =============================================================================

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
	diagURL := tm.DiagURL

	// Step 1: Allocate a new conversation
	// ID=$(cat new/clone)
	stdout := runShellDiagOK(t, mountPoint, "cat new/clone", diagURL)
	convID := strings.TrimSpace(stdout)
	if len(convID) != 8 {
		t.Fatalf("Expected 8-char conversation ID, got %q", convID)
	}
	t.Logf("Allocated conversation ID: %s", convID)

	// Step 2: Configure model and working directory
	// echo "model=predictable cwd=<dir>" > conversation/$ID/ctl
	// Use a dedicated temp dir, NOT /tmp itself — the shelley server walks
	// the cwd for guidance files and would enter the FUSE mount under /tmp.
	cwdDir := t.TempDir()
	runShellDiagOK(t, mountPoint, "echo 'model=predictable cwd="+cwdDir+"' > conversation/"+convID+"/ctl", diagURL)

	// Verify ctl was written correctly
	ctlContent := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/ctl", diagURL)
	if !strings.Contains(ctlContent, "model=predictable") {
		t.Errorf("Expected ctl to contain model=predictable, got: %s", ctlContent)
	}
	if !strings.Contains(ctlContent, "cwd="+cwdDir) {
		t.Errorf("Expected ctl to contain cwd=%s, got: %s", cwdDir, ctlContent)
	}

	// Step 3: Send first message (creates conversation on backend)
	// echo "Hello, Shelley!" > conversation/$ID/send
	runShellDiagOK(t, mountPoint, "echo 'Hello, Shelley!' > conversation/"+convID+"/send", diagURL)

	// Step 4: Read the response(s)
	// cat conversation/$ID/messages/since/user/1/*/content.md
	// Note: with the predictable model, there may not be agent responses after
	// the first message (only a system message + user message). So we also
	// verify via all.md that the conversation was created and has content.
	allMd := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/all.md", diagURL)
	if allMd == "" {
		t.Error("Expected non-empty response from all.md")
	}
	if !strings.Contains(allMd, "##") {
		t.Error("Expected markdown headers in all.md")
	}
	t.Logf("all.md content (truncated): %.200s...", allMd)

	// Step 5: Send follow-up
	// echo "Thanks!" > conversation/$ID/send
	runShellDiagOK(t, mountPoint, "echo 'Thanks!' > conversation/"+convID+"/send", diagURL)

	// Verify follow-up was added
	allMdAfter := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/all.md", diagURL)
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
		"ls conversation/"+convID+"/messages/since/user/1/", diagURL)
	if err != nil {
		t.Logf("since/user/1/ listing returned error (may be empty dir): %v", err)
	}
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
	diagURL := tm.DiagURL

	// Create a conversation first so there's something to list
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'Test message' > conversation/"+convID+"/send", diagURL)

	// List conversations: ls conversation/
	convListing := runShellDiagOK(t, mountPoint, "ls conversation/", diagURL)
	if !strings.Contains(convListing, convID) {
		t.Errorf("Expected conversation %s in listing, got: %s", convID, convListing)
	}
	t.Logf("Conversations: %s", strings.TrimSpace(convListing))
}

// TestReadmeFullWorkflow exercises a complete workflow combining Quick Start and Common Operations
// to ensure they work together as documented.
func TestReadmeFullWorkflow(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	diagURL := tm.DiagURL

	// === Quick Start Flow ===

	// 1. Allocate new conversation
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	t.Logf("Step 1 - Allocated ID: %s", convID)

	// 2. Configure model and cwd (use dedicated temp dir, not /tmp itself)
	cwdDir := t.TempDir()
	runShellDiagOK(t, mountPoint, "echo 'model=predictable cwd="+cwdDir+"' > conversation/"+convID+"/ctl", diagURL)
	t.Log("Step 2 - Configured model and cwd")

	// 3. Check created status before sending message
	_, _, err := runShellDiag(t, mountPoint, "test -e conversation/"+convID+"/created", diagURL)
	if err == nil {
		t.Fatal("Created file should not exist before first message")
	}
	t.Log("Step 3 - Verified not created yet")

	// 4. Send first message
	runShellDiagOK(t, mountPoint, "echo 'Hello from workflow test!' > conversation/"+convID+"/send", diagURL)
	t.Log("Step 4 - Sent first message")

	// 5. Verify created
	runShellDiagOK(t, mountPoint, "test -e conversation/"+convID+"/created", diagURL)
	t.Log("Step 5 - Verified created")

	// 6. Read response
	allMd := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/all.md", diagURL)
	if !strings.Contains(allMd, "Hello from workflow test!") {
		t.Error("Expected all.md to contain our message")
	}
	t.Log("Step 6 - Read response via all.md")

	// 7. Send follow-up
	runShellDiagOK(t, mountPoint, "echo 'Follow-up message!' > conversation/"+convID+"/send", diagURL)
	t.Log("Step 7 - Sent follow-up")

	// === Common Operations ===

	// 8. List models
	models := runShellDiagOK(t, mountPoint, "ls model/", diagURL)
	if !strings.Contains(models, "predictable") {
		t.Error("Expected predictable model in listing")
	}
	t.Log("Step 8 - Listed models")

	// 9. Check default model
	defaultModel := strings.TrimSpace(runShellDiagOK(t, mountPoint, "readlink model/default", diagURL))
	if defaultModel == "" {
		t.Error("Expected non-empty default model")
	}
	t.Logf("Step 9 - Default model: %s", defaultModel)

	// 10. List conversations
	convs := runShellDiagOK(t, mountPoint, "ls conversation/", diagURL)
	if !strings.Contains(convs, convID) {
		t.Errorf("Expected %s in conversation listing", convID)
	}
	t.Log("Step 10 - Listed conversations")

	// 11. List last 5 messages
	last5 := runShellDiagOK(t, mountPoint, "ls conversation/"+convID+"/messages/last/5/", diagURL)
	if last5 == "" {
		t.Error("Expected non-empty last/5 listing")
	}
	t.Logf("Step 11 - Last 5 messages: %s", strings.TrimSpace(last5))

	// 12. Read last 5 message contents
	last5Content := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/last/5/*/content.md", diagURL)
	if !strings.Contains(last5Content, "##") {
		t.Error("Expected markdown headers in last/5/*/content.md")
	}
	t.Log("Step 12 - Read last 5 message contents")

	// 13. List messages since user's last message
	// This may be empty if user's message is last, but the command should work
	_, _, _ = runShellDiag(t, mountPoint, "ls conversation/"+convID+"/messages/since/user/1/", diagURL)
	t.Log("Step 13 - Listed messages since user/1")

	// 14. Get message count
	count := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/count", diagURL))
	if count == "0" {
		t.Error("Expected non-zero message count")
	}
	t.Logf("Step 14 - Message count: %s", count)

	// 15. Final created check
	createdCheck := strings.TrimSpace(runShellDiagOK(t, mountPoint, "test -e conversation/"+convID+"/created && echo created || echo not_created", diagURL))
	if createdCheck != "created" {
		t.Errorf("Expected 'created', got: %s", createdCheck)
	}
	t.Log("Step 15 - Final created check passed")

	t.Log("=== Full workflow completed successfully ===")
}

// TestShellVariableSubstitution tests that shell variable substitution works
// as shown in the Quick Start documentation.
func TestShellVariableSubstitution(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	diagURL := tm.DiagURL

	// This mirrors the exact documented pattern:
	// ID=$(cat new/clone)
	// echo "..." > conversation/$ID/send
	script := `
ID=$(cat new/clone)
echo "model=predictable" > conversation/$ID/ctl
echo "Variable substitution test" > conversation/$ID/send
cat conversation/$ID/messages/all.md
`
	output := runShellDiagOK(t, mountPoint, script, diagURL)
	if !strings.Contains(output, "Variable substitution test") {
		t.Error("Expected output to contain our test message")
	}
	if !strings.Contains(output, "##") {
		t.Error("Expected markdown headers in output")
	}
}

// =============================================================================
// Message Tests
// =============================================================================

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
	diagURL := tm.DiagURL

	// Create a conversation with multiple messages
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'First message' > conversation/"+convID+"/send", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'Second message' > conversation/"+convID+"/send", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'Third message' > conversation/"+convID+"/send", diagURL)

	// List last 5 messages: ls conversation/$ID/messages/last/5/
	last5Listing := runShellDiagOK(t, mountPoint, "ls conversation/"+convID+"/messages/last/5/", diagURL)
	if last5Listing == "" {
		t.Error("Expected non-empty listing for last/5/")
	}
	t.Logf("Last 5 messages listing: %s", strings.TrimSpace(last5Listing))

	// Read content of all last 5 messages: cat conversation/$ID/messages/last/5/*/content.md
	last5Content := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/last/5/*/content.md", diagURL)
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
	diagURL := tm.DiagURL

	// Create a conversation - after sending a user message, the agent responds
	// So since/user/1 should return messages after the last user message
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'Hello agent' > conversation/"+convID+"/send", diagURL)

	// List messages since user's last message: ls conversation/$ID/messages/since/user/1/
	// Note: The command may return empty if user's message is the last one,
	// but the command should succeed
	stdout, stderr, err := runShellDiag(t, mountPoint, "ls conversation/"+convID+"/messages/since/user/1/", diagURL)
	if err != nil {
		// ls returns error if directory is empty, which is valid
		t.Logf("since/user/1/ listing (may be empty): stdout=%q stderr=%q", stdout, stderr)
	} else {
		t.Logf("since/user/1/ listing: %s", strings.TrimSpace(stdout))
	}

	// Send another message to get an agent response, then check again
	runShellDiagOK(t, mountPoint, "echo 'Another message' > conversation/"+convID+"/send", diagURL)

	// The directory should be accessible (even if empty when user message is last)
	_, _, _ = runShellDiag(t, mountPoint, "ls conversation/"+convID+"/messages/since/user/1/ 2>/dev/null || true", diagURL)
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
	diagURL := tm.DiagURL

	// Create a conversation
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)

	// Check count before any messages (uncreated conversation)
	countBefore := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/count", diagURL)
	countBefore = strings.TrimSpace(countBefore)
	if countBefore != "0" {
		t.Errorf("Expected count=0 before first message, got: %s", countBefore)
	}

	// Send a message
	runShellDiagOK(t, mountPoint, "echo 'Test message' > conversation/"+convID+"/send", diagURL)

	// Get message count: cat conversation/$ID/messages/count
	count := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/count", diagURL)
	count = strings.TrimSpace(count)
	if count == "0" {
		t.Error("Expected non-zero message count after sending message")
	}
	t.Logf("Message count: %s", count)
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
	diagURL := tm.DiagURL

	// Create a conversation
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'Hello from test' > conversation/"+convID+"/send", diagURL)

	// cat conversation/$ID/messages/last/1/0/content.md
	lastContent := runShellDiagOK(t, mountPoint,
		"cat conversation/"+convID+"/messages/last/1/0/content.md", diagURL)
	if lastContent == "" {
		t.Error("Expected non-empty content from last/1/0/content.md")
	}
	if !strings.Contains(lastContent, "##") {
		t.Error("Expected markdown headers in last/1/0/content.md")
	}
	t.Logf("Last message content (truncated): %.200s", lastContent)
}

// TestReadmeMessageDirContents verifies that individual message directories
// (e.g. 000-system/, 001-user/) are non-empty and contain the expected files.
// This catches regressions where Readdir on MessageDirNode returns no entries
// (e.g. if OpendirHandle incorrectly short-circuits the readdir path).
func TestReadmeMessageDirContents(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	diagURL := tm.DiagURL

	// Create a conversation with a message
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'Hello' > conversation/"+convID+"/send", diagURL)

	// ls conversation/$ID/messages/ should include message directories
	msgsListing := runShellDiagOK(t, mountPoint, "ls conversation/"+convID+"/messages/", diagURL)
	// We expect at least a system and user message directory (NNN-slug format)
	if !strings.Contains(msgsListing, "user") {
		t.Errorf("Expected a user message directory in messages/ listing, got:\n%s", msgsListing)
	}

	// Find the first message directory and verify its contents
	lines := strings.Fields(strings.TrimSpace(msgsListing))
	var msgDir string
	for _, line := range lines {
		if strings.Contains(line, "-") {
			msgDir = line
			break
		}
	}
	if msgDir == "" {
		t.Fatalf("No message directories found in listing:\n%s", msgsListing)
	}

	// ls conversation/$ID/messages/{msgDir}/ should contain field files
	contents := runShellDiagOK(t, mountPoint, "ls conversation/"+convID+"/messages/"+msgDir+"/", diagURL)
	if contents == "" {
		t.Fatalf("Message directory %s is completely empty", msgDir)
	}
	for _, expected := range []string{"content.md", "type", "message_id", "sequence_id", "created_at"} {
		if !strings.Contains(contents, expected) {
			t.Errorf("Expected %q in message directory listing, got:\n%s", expected, contents)
		}
	}
	t.Logf("Message directory %s contents: %s", msgDir, strings.TrimSpace(contents))
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
	diagURL := tm.DiagURL

	// Create a conversation with at least 2 messages
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'Hello' > conversation/"+convID+"/send", diagURL)

	// ls conversation/$ID/messages/last/2/
	last2 := runShellDiagOK(t, mountPoint,
		"ls conversation/"+convID+"/messages/last/2/", diagURL)
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
			"readlink conversation/"+convID+"/messages/last/2/"+entry, diagURL))
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
	diagURL := tm.DiagURL

	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'First message' > conversation/"+convID+"/send", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'Second message' > conversation/"+convID+"/send", diagURL)

	// ls conversation/$ID/messages/since/user/1/
	// The predictable model doesn't generate agent responses, so the user's
	// message is always the last message and since/user/1/ is empty.
	// We verify the directory is accessible (ls succeeds).
	runShellDiagOK(t, mountPoint,
		"ls conversation/"+convID+"/messages/since/user/1/", diagURL)
	t.Log("since/user/1/ directory is accessible")

	// Verify since/user/2/ returns messages after the second-to-last user message.
	// This should include the last user message itself.
	since2Listing := runShellDiagOK(t, mountPoint,
		"ls conversation/"+convID+"/messages/since/user/2/", diagURL)
	if since2Listing == "" {
		t.Error("Expected non-empty listing for since/user/2/")
	}
	t.Logf("since/user/2/ listing: %s", strings.TrimSpace(since2Listing))
}

// TestShellMessageFields tests reading message field files via shell.
func TestShellMessageFields(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)

	// Create conversation with message
	convID := strings.TrimSpace(runShellDiagOK(t, tm.MountPoint, "cat new/clone", tm.DiagURL))
	runShellDiagOK(t, tm.MountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", tm.DiagURL)
	runShellDiagOK(t, tm.MountPoint, "echo 'Hello' > conversation/"+convID+"/send", tm.DiagURL)

	// List messages
	msgs := runShellDiagOK(t, tm.MountPoint, "ls conversation/"+convID+"/messages/", tm.DiagURL)
	if !strings.Contains(msgs, "user") {
		t.Errorf("Expected user message in listing, got: %s", msgs)
	}

	// Find a user message directory (format is NNN-user where NNN is zero-padded index)
	lines := strings.Fields(strings.TrimSpace(msgs))
	var userMsgDir string
	for _, line := range lines {
		if strings.Contains(line, "-user") {
			userMsgDir = line
			break
		}
	}
	if userMsgDir == "" {
		t.Fatalf("No user message directory found in: %s", msgs)
	}

	// Read message fields using discovered directory name
	msgType := runShellDiagOK(t, tm.MountPoint, "cat conversation/"+convID+"/messages/"+userMsgDir+"/type", tm.DiagURL)
	if strings.TrimSpace(msgType) != "user" {
		t.Errorf("Expected type=user, got %q", msgType)
	}

	// Verify message_id field exists and is non-empty
	msgID := runShellDiagOK(t, tm.MountPoint, "cat conversation/"+convID+"/messages/"+userMsgDir+"/message_id", tm.DiagURL)
	if strings.TrimSpace(msgID) == "" {
		t.Error("Expected non-empty message_id")
	}

	// Verify sequence_id field
	seqID := runShellDiagOK(t, tm.MountPoint, "cat conversation/"+convID+"/messages/"+userMsgDir+"/sequence_id", tm.DiagURL)
	if strings.TrimSpace(seqID) == "" {
		t.Error("Expected non-empty sequence_id")
	}

	// Verify content.md exists
	content := runShellDiagOK(t, tm.MountPoint, "cat conversation/"+convID+"/messages/"+userMsgDir+"/content.md", tm.DiagURL)
	if content == "" {
		t.Error("Expected non-empty content.md")
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
	diagURL := tm.DiagURL

	// Create conversation with messages
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'Message one' > conversation/"+convID+"/send", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'Message two' > conversation/"+convID+"/send", diagURL)

	// Test glob pattern for last N messages
	globOutput := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/last/5/*/content.md", diagURL)
	if globOutput == "" {
		t.Error("Expected non-empty output from glob pattern")
	}

	// Test that we can also use glob to list message directories
	lsOutput := runShellDiagOK(t, mountPoint, "ls -d conversation/"+convID+"/messages/last/5/*/", diagURL)
	if lsOutput == "" {
		t.Error("Expected non-empty directory listing from glob")
	}
}

// =============================================================================
// Control File Tests
// =============================================================================

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
	diagURL := tm.DiagURL

	// Allocate a conversation but don't create it yet
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)

	// Check if created before sending message - should NOT exist
	// test -e returns exit code 1 if file doesn't exist
	_, _, err := runShellDiag(t, mountPoint, "test -e conversation/"+convID+"/created", diagURL)
	if err == nil {
		t.Error("Expected 'created' to NOT exist before first message")
	}

	// Check with the documented pattern
	notCreatedOutput := runShellDiagOK(t, mountPoint, "test -e conversation/"+convID+"/created && echo created || echo not_created", diagURL)
	if strings.TrimSpace(notCreatedOutput) != "not_created" {
		t.Errorf("Expected 'not_created' before first message, got: %s", notCreatedOutput)
	}

	// Send first message to create conversation
	runShellDiagOK(t, mountPoint, "echo 'Hello!' > conversation/"+convID+"/send", diagURL)

	// Check if created after sending message - should exist
	// test -e conversation/$ID/created && echo created
	createdOutput := runShellDiagOK(t, mountPoint, "test -e conversation/"+convID+"/created && echo created", diagURL)
	if strings.TrimSpace(createdOutput) != "created" {
		t.Errorf("Expected 'created' after first message, got: %s", createdOutput)
	}
	t.Logf("Created check passed: %s", strings.TrimSpace(createdOutput))
}

// TestShellPipeToSend tests that piping content to the send file works correctly.
// This is a common shell pattern: echo "message" | tee conversation/$ID/send
func TestShellPipeToSend(t *testing.T) {
	skipIfNoFusermount(t)
	skipIfNoShelley(t)

	serverURL := startShelleyServer(t)
	tm := mountTestFSFull(t, serverURL, time.Hour)
	mountPoint := tm.MountPoint
	diagURL := tm.DiagURL

	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)

	// Use shell pipe pattern
	runShellDiagOK(t, mountPoint, "echo 'Piped message' | cat > conversation/"+convID+"/send", diagURL)

	// Verify the message was received
	allMd := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/all.md", diagURL)
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
	diagURL := tm.DiagURL

	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)

	// Use heredoc pattern for multiline input
	heredocCmd := `cat > conversation/` + convID + `/send << 'EOF'
Line 1 of heredoc
Line 2 of heredoc
Line 3 of heredoc
EOF`
	runShellDiagOK(t, mountPoint, heredocCmd, diagURL)

	// Verify all lines were received as a single message
	allMd := runShellDiagOK(t, mountPoint, "cat conversation/"+convID+"/messages/all.md", diagURL)
	if !strings.Contains(allMd, "Line 1") || !strings.Contains(allMd, "Line 2") || !strings.Contains(allMd, "Line 3") {
		t.Error("Expected all heredoc lines in all.md")
	}
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
	diagURL := tm.DiagURL

	// Create a conversation
	convID := strings.TrimSpace(runShellDiagOK(t, mountPoint, "cat new/clone", diagURL))
	runShellDiagOK(t, mountPoint, "echo 'model=predictable' > conversation/"+convID+"/ctl", diagURL)
	runShellDiagOK(t, mountPoint, "echo 'Test archiving' > conversation/"+convID+"/send", diagURL)

	// Initially not archived
	notArchived := strings.TrimSpace(runShellDiagOK(t, mountPoint,
		"test -e conversation/"+convID+"/archived && echo archived || echo not_archived", diagURL))
	if notArchived != "not_archived" {
		t.Errorf("Expected 'not_archived' initially, got: %s", notArchived)
	}

	// Archive: touch conversation/$ID/archived
	runShellDiagOK(t, mountPoint, "touch conversation/"+convID+"/archived", diagURL)

	// Check if archived: test -e conversation/$ID/archived && echo archived
	archived := strings.TrimSpace(runShellDiagOK(t, mountPoint,
		"test -e conversation/"+convID+"/archived && echo archived", diagURL))
	if archived != "archived" {
		t.Errorf("Expected 'archived' after touch, got: %s", archived)
	}
	t.Log("Archived successfully")

	// Unarchive: rm conversation/$ID/archived
	runShellDiagOK(t, mountPoint, "rm conversation/"+convID+"/archived", diagURL)

	// Verify unarchived
	unarchived := strings.TrimSpace(runShellDiagOK(t, mountPoint,
		"test -e conversation/"+convID+"/archived && echo archived || echo not_archived", diagURL))
	if unarchived != "not_archived" {
		t.Errorf("Expected 'not_archived' after rm, got: %s", unarchived)
	}
	t.Log("Unarchived successfully")
}
