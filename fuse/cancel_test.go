package fuse

import (
	"os"
	"path/filepath"
	"testing"

	"shelley-fuse/mockserver"
	"shelley-fuse/shelley"
)

// TestCancelFile_Exists tests that the cancel file exists when the agent is working.
func TestCancelFile_Exists(t *testing.T) {
	convID := "test-conv-cancel-exists"
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
	}
	server := mockserver.New(
		mockserver.WithConversation(convID, msgs),
		mockserver.WithConversationWorking(convID, true),
	)
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	cancelPath := filepath.Join(mountPoint, "conversation", localID, "cancel")
	_, err := os.Stat(cancelPath)
	if err != nil {
		t.Fatalf("Expected cancel file to exist, got error: %v", err)
	}
}

// TestCancelFile_NotExists tests that the cancel file does not exist when the agent is not working.
func TestCancelFile_NotExists(t *testing.T) {
	convID := "test-conv-cancel-not-exists"
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi!")},
	}
	server := mockserver.New(
		mockserver.WithConversation(convID, msgs),
		// Working defaults to false
	)
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	cancelPath := filepath.Join(mountPoint, "conversation", localID, "cancel")
	_, err := os.Stat(cancelPath)
	if err == nil {
		t.Error("Expected cancel file to not exist when agent is not working")
	}
	if !os.IsNotExist(err) {
		t.Errorf("Expected ENOENT, got: %v", err)
	}
}

// TestCancelFile_InReaddir tests that cancel appears in directory listing when working=true.
func TestCancelFile_InReaddir(t *testing.T) {
	convID := "test-conv-readdir-cancel"
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
	}
	server := mockserver.New(
		mockserver.WithConversation(convID, msgs),
		mockserver.WithConversationWorking(convID, true),
	)
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	entries, err := os.ReadDir(filepath.Join(mountPoint, "conversation", localID))
	if err != nil {
		t.Fatalf("Failed to read directory: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name() == "cancel" {
			found = true
			if !e.Type().IsRegular() {
				t.Error("cancel should be a regular file")
			}
			break
		}
	}
	if !found {
		t.Error("cancel should appear in directory listing when agent is working")
	}
}

// TestCancelFile_Write tests that writing to cancel calls the cancel endpoint.
func TestCancelFile_Write(t *testing.T) {
	convID := "test-conv-cancel-write"
	msgs := []shelley.Message{
		{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
	}
	server := mockserver.New(
		mockserver.WithConversation(convID, msgs),
		mockserver.WithConversationWorking(convID, true),
	)
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	cancelPath := filepath.Join(mountPoint, "conversation", localID, "cancel")

	// Write to cancel file to trigger cancellation
	err := os.WriteFile(cancelPath, []byte("cancel\n"), 0222)
	if err != nil {
		t.Fatalf("Failed to write to cancel file: %v", err)
	}

	// Verify the mock server received the cancel and set working=false.
	// The "working" file should no longer exist since the cancel endpoint
	// sets working=false in the mock server.
	workingPath := filepath.Join(mountPoint, "conversation", localID, "working")
	_, err = os.Stat(workingPath)
	if err == nil {
		t.Error("Expected working file to disappear after cancellation")
	}
	if !os.IsNotExist(err) {
		t.Errorf("Expected ENOENT for working file after cancel, got: %v", err)
	}
}
