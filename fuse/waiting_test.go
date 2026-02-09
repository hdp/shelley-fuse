package fuse

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"shelley-fuse/mockserver"
	"shelley-fuse/shelley"
)

func TestAnalyzeWaitingForInput_EmptyConversation(t *testing.T) {
	status := AnalyzeWaitingForInput([]shelley.Message{}, nil)
	if status.Waiting {
		t.Error("Empty conversation should not be waiting for input")
	}
}

func TestAnalyzeWaitingForInput_OnlyUserMessage(t *testing.T) {
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
	}
	status := AnalyzeWaitingForInput(msgs, nil)
	if status.Waiting {
		t.Error("Conversation with only user message should not be waiting for input")
	}
}

func TestAnalyzeWaitingForInput_SimpleAgentResponse(t *testing.T) {
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi there!")},
	}
	status := AnalyzeWaitingForInput(msgs, nil)
	if !status.Waiting {
		t.Error("Conversation ending with agent message should be waiting for input")
	}
	if status.LastAgentIndex != 1 {
		t.Errorf("LastAgentIndex: expected 1, got %d", status.LastAgentIndex)
	}
	if status.LastAgentSeqID != 2 {
		t.Errorf("LastAgentSeqID: expected 2, got %d", status.LastAgentSeqID)
	}
}

func TestAnalyzeWaitingForInput_UserMessageAfterAgent(t *testing.T) {
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi!")},
		{MessageID: "m3", SequenceID: 3, Type: "user", UserData: strPtr("Follow up")},
	}
	status := AnalyzeWaitingForInput(msgs, nil)
	if status.Waiting {
		t.Error("Conversation with user message after agent should not be waiting for input")
	}
}

func TestAnalyzeWaitingForInput_CompletedToolCall(t *testing.T) {
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content":[{"Type":5,"ID":"tu_123","ToolName":"bash"}]}`)},
		{MessageID: "m3", SequenceID: 3, Type: "user", UserData: strPtr(`{"Content":[{"Type":6,"ToolUseID":"tu_123"}]}`)},
		{MessageID: "m4", SequenceID: 4, Type: "shelley", LLMData: strPtr("Done!")},
	}
	toolMap := shelley.BuildToolNameMap([]*shelley.Message{&msgs[0], &msgs[1], &msgs[2], &msgs[3]})
	status := AnalyzeWaitingForInput(msgs, toolMap)
	if !status.Waiting {
		t.Error("Conversation with completed tool call should be waiting for input")
	}
	if status.LastAgentIndex != 3 {
		t.Errorf("LastAgentIndex: expected 3, got %d", status.LastAgentIndex)
	}
}

func TestAnalyzeWaitingForInput_PendingToolCall(t *testing.T) {
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content":[{"Type":5,"ID":"tu_pending","ToolName":"bash"}]}`)},
	}
	toolMap := shelley.BuildToolNameMap([]*shelley.Message{&msgs[0], &msgs[1]})
	status := AnalyzeWaitingForInput(msgs, toolMap)
	if status.Waiting {
		t.Error("Conversation with pending tool call should not be waiting for input")
	}
}

func TestAnalyzeWaitingForInput_GitInfoAfterAgent(t *testing.T) {
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi!")},
		{MessageID: "m3", SequenceID: 3, Type: "gitinfo", UserData: strPtr("git info data")},
	}
	status := AnalyzeWaitingForInput(msgs, nil)
	if !status.Waiting {
		t.Error("Conversation with gitinfo after agent should be waiting for input")
	}
	if status.LastAgentIndex != 1 {
		t.Errorf("LastAgentIndex: expected 1, got %d", status.LastAgentIndex)
	}
}

func TestAnalyzeWaitingForInput_MultipleToolCalls(t *testing.T) {
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content":[{"Type":5,"ID":"tu_1","ToolName":"bash"},{"Type":5,"ID":"tu_2","ToolName":"patch"}]}`)},
		{MessageID: "m3", SequenceID: 3, Type: "user", UserData: strPtr(`{"Content":[{"Type":6,"ToolUseID":"tu_1"}]}`)},
		{MessageID: "m4", SequenceID: 4, Type: "user", UserData: strPtr(`{"Content":[{"Type":6,"ToolUseID":"tu_2"}]}`)},
		{MessageID: "m5", SequenceID: 5, Type: "shelley", LLMData: strPtr("All done!")},
	}
	toolMap := shelley.BuildToolNameMap([]*shelley.Message{&msgs[0], &msgs[1], &msgs[2], &msgs[3], &msgs[4]})
	status := AnalyzeWaitingForInput(msgs, toolMap)
	if !status.Waiting {
		t.Error("Conversation with all tool calls completed should be waiting for input")
	}
}

func TestAnalyzeWaitingForInput_PartialToolCallCompletion(t *testing.T) {
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content":[{"Type":5,"ID":"tu_1","ToolName":"bash"},{"Type":5,"ID":"tu_2","ToolName":"patch"}]}`)},
		{MessageID: "m3", SequenceID: 3, Type: "user", UserData: strPtr(`{"Content":[{"Type":6,"ToolUseID":"tu_1"}]}`)},
		// tu_2 has no result yet
	}
	toolMap := shelley.BuildToolNameMap([]*shelley.Message{&msgs[0], &msgs[1], &msgs[2]})
	status := AnalyzeWaitingForInput(msgs, toolMap)
	if status.Waiting {
		t.Error("Conversation with incomplete tool calls should not be waiting for input")
	}
}

// TestWaitingForInputSymlink_Exists tests that the symlink exists when waiting for input.
func TestWaitingForInputSymlink_Exists(t *testing.T) {
	convID := "test-conv-waiting"
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content":[{"Type":2,"Text":"Hi!"}],"EndOfTurn":true}`)},
	}
	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	symlinkPath := filepath.Join(mountPoint, "conversation", localID, "waiting_for_input")
	target, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Expected symlink to exist, got error: %v", err)
	}
	expectedTarget := "messages/1-agent/llm_data/EndOfTurn"
	if target != expectedTarget {
		t.Errorf("Symlink target: expected %q, got %q", expectedTarget, target)
	}
}

// TestWaitingForInputSymlink_NotExistsAfterUserMessage tests symlink absence after user message.
func TestWaitingForInputSymlink_NotExistsAfterUserMessage(t *testing.T) {
	convID := "test-conv-not-waiting"
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi!")},
		{MessageID: "m3", SequenceID: 3, Type: "user", UserData: strPtr("Follow up")},
	}
	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	symlinkPath := filepath.Join(mountPoint, "conversation", localID, "waiting_for_input")
	_, err := os.Readlink(symlinkPath)
	if err == nil {
		t.Error("Expected symlink to not exist when user message follows agent")
	}
}

// TestWaitingForInputSymlink_NotExistsWithPendingToolCall tests symlink absence with pending tool.
func TestWaitingForInputSymlink_NotExistsWithPendingToolCall(t *testing.T) {
	convID := "test-conv-pending-tool"
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content":[{"Type":5,"ID":"tu_pending","ToolName":"bash"}]}`)},
	}
	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	symlinkPath := filepath.Join(mountPoint, "conversation", localID, "waiting_for_input")
	_, err := os.Readlink(symlinkPath)
	if err == nil {
		t.Error("Expected symlink to not exist with pending tool call")
	}
}

// TestWaitingForInputSymlink_InReaddir tests that symlink appears in directory listing when appropriate.
func TestWaitingForInputSymlink_InReaddir(t *testing.T) {
	convID := "test-conv-readdir-waiting"
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi!")},
	}
	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	entries, err := ioutil.ReadDir(filepath.Join(mountPoint, "conversation", localID))
	if err != nil {
		t.Fatalf("Failed to read directory: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name() == "waiting_for_input" {
			found = true
			if e.Mode()&os.ModeSymlink == 0 {
				t.Error("waiting_for_input should be a symlink")
			}
			break
		}
	}
	if !found {
		t.Error("waiting_for_input should appear in directory listing when waiting")
	}
}

// TestAnalyzeWaitingForInput_ToolCallCompletedNoFollowUp tests the case where
// agent makes a tool call, tool completes, but there's no follow-up text response.
// This should show Waiting=true because the agent's last action was completed.
func TestAnalyzeWaitingForInput_ToolCallCompletedNoFollowUp(t *testing.T) {
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Run ls")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content":[{"Type":5,"ID":"tu_ls","ToolName":"bash"}]}`)},
		{MessageID: "m3", SequenceID: 3, Type: "user", UserData: strPtr(`{"Content":[{"Type":6,"ToolUseID":"tu_ls","ToolResult":[{"Text":"file1.txt\nfile2.txt"}]}]}`)},
		// No follow-up agent message - the tool result is the last message
	}
	toolMap := shelley.BuildToolNameMap([]*shelley.Message{&msgs[0], &msgs[1], &msgs[2]})
	status := AnalyzeWaitingForInput(msgs, toolMap)

	// This should be waiting=true because:
	// - Last agent message (m2) made a tool call
	// - Tool call was completed (m3 has the result)
	// - No pending tool calls remain
	// - No user messages after the agent's tool call was satisfied
	if !status.Waiting {
		t.Error("Conversation with completed tool call (no follow-up) should be waiting for input")
	}
	if status.LastAgentIndex != 1 {
		t.Errorf("LastAgentIndex: expected 1, got %d", status.LastAgentIndex)
	}
	if status.LastAgentSeqID != 2 {
		t.Errorf("LastAgentSeqID: expected 2, got %d", status.LastAgentSeqID)
	}
	// The slug should be "bash-tool" since the agent message contains a tool call
	if status.LastAgentSlug != "bash-tool" {
		t.Errorf("LastAgentSlug: expected 'bash-tool', got %q", status.LastAgentSlug)
	}
}

// TestWaitingForInputSymlink_ToolCallCompletedNoFollowUp tests symlink with tool call slug.
func TestWaitingForInputSymlink_ToolCallCompletedNoFollowUp(t *testing.T) {
	convID := "test-conv-tool-completed"
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Run ls")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content":[{"Type":5,"ID":"tu_ls","ToolName":"bash"}]}`)},
		{MessageID: "m3", SequenceID: 3, Type: "user", UserData: strPtr(`{"Content":[{"Type":6,"ToolUseID":"tu_ls","ToolResult":[{"Text":"output"}]}]}`)},
	}
	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	symlinkPath := filepath.Join(mountPoint, "conversation", localID, "waiting_for_input")
	target, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Expected symlink to exist, got error: %v", err)
	}
	// Target should use "bash-tool" since the last agent message contains a tool call
	expectedTarget := "messages/1-bash-tool/llm_data/EndOfTurn"
	if target != expectedTarget {
		t.Errorf("Symlink target: expected %q, got %q", expectedTarget, target)
	}
}
