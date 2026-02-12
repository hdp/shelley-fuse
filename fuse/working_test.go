package fuse

import (
	"os"
	"path/filepath"
	"testing"

	"shelley-fuse/mockserver"
	"shelley-fuse/shelley"
)

// TestWorkingFile_Exists tests that the working file exists when the agent is working.
func TestWorkingFile_Exists(t *testing.T) {
	convID := "test-conv-working"
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

	workingPath := filepath.Join(mountPoint, "conversation", localID, "working")
	_, err := os.Stat(workingPath)
	if err != nil {
		t.Fatalf("Expected working file to exist, got error: %v", err)
	}
}

// TestWorkingFile_NotExists tests that the working file does not exist when the agent is not working.
func TestWorkingFile_NotExists(t *testing.T) {
	convID := "test-conv-not-working"
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

	workingPath := filepath.Join(mountPoint, "conversation", localID, "working")
	_, err := os.Stat(workingPath)
	if err == nil {
		t.Error("Expected working file to not exist when agent is not working")
	}
	if !os.IsNotExist(err) {
		t.Errorf("Expected ENOENT, got: %v", err)
	}
}

// TestWorkingFile_InReaddir tests that working appears in directory listing when working=true.
func TestWorkingFile_InReaddir(t *testing.T) {
	convID := "test-conv-readdir-working"
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
		if e.Name() == "working" {
			found = true
			if !e.Type().IsRegular() {
				t.Error("working should be a regular file")
			}
			break
		}
	}
	if !found {
		t.Error("working should appear in directory listing when agent is working")
	}
}
