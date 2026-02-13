package fuse

import (
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"testing"

	"shelley-fuse/mockserver"
	"shelley-fuse/shelley"
)

func TestSubagentsDir_Readdir(t *testing.T) {
	parentConv := shelley.Conversation{
		ConversationID: "parent-conv-id",
		Slug:           strPtr("parent-slug"),
		CreatedAt:      "2024-01-01T00:00:00Z",
		UpdatedAt:      "2024-01-01T00:00:00Z",
	}
	child1 := shelley.Conversation{
		ConversationID: "child-conv-1",
		Slug:           strPtr("research-api"),
		CreatedAt:      "2024-01-01T01:00:00Z",
		UpdatedAt:      "2024-01-01T01:00:00Z",
	}
	child2 := shelley.Conversation{
		ConversationID: "child-conv-2",
		Slug:           strPtr("test-runner"),
		CreatedAt:      "2024-01-01T02:00:00Z",
		UpdatedAt:      "2024-01-01T02:00:00Z",
	}

	server := mockserver.New(
		mockserver.WithFullConversation(parentConv, nil),
		mockserver.WithFullConversation(child1, nil),
		mockserver.WithFullConversation(child2, nil),
		mockserver.WithSubagent("parent-conv-id", "child-conv-1"),
		mockserver.WithSubagent("parent-conv-id", "child-conv-2"),
	)
	defer server.Close()

	store := testStore(t)

	// Create parent and mark it as created
	parentID, _ := store.Clone()
	store.MarkCreated(parentID, "parent-conv-id", "parent-slug")

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	subagentsDir := filepath.Join(mountDir, "conversation", parentID, "subagents")

	// Read the subagents directory
	entries, err := os.ReadDir(subagentsDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	// Collect names
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	// Should have entries for 2 children:
	// Each child gets local ID, server ID, and slug symlinks = 6 total
	if len(names) < 4 {
		t.Fatalf("expected at least 4 entries (2 local IDs + 2 server IDs), got %d: %v", len(names), names)
	}

	// Check that server IDs are present
	hasChild1 := false
	hasChild2 := false
	for _, n := range names {
		if n == "child-conv-1" {
			hasChild1 = true
		}
		if n == "child-conv-2" {
			hasChild2 = true
		}
	}
	if !hasChild1 {
		t.Errorf("expected child-conv-1 in entries: %v", names)
	}
	if !hasChild2 {
		t.Errorf("expected child-conv-2 in entries: %v", names)
	}

	// Check slug entries
	hasSlug1 := false
	hasSlug2 := false
	for _, n := range names {
		if n == "research-api" {
			hasSlug1 = true
		}
		if n == "test-runner" {
			hasSlug2 = true
		}
	}
	if !hasSlug1 {
		t.Errorf("expected research-api slug in entries: %v", names)
	}
	if !hasSlug2 {
		t.Errorf("expected test-runner slug in entries: %v", names)
	}

	// All entries should be symlinks
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 {
			t.Errorf("expected %q to be a symlink, got type %v", e.Name(), e.Type())
		}
	}
}

func TestSubagentsDir_SymlinkTarget(t *testing.T) {
	parentConv := shelley.Conversation{
		ConversationID: "parent-conv-id",
		Slug:           strPtr("parent-slug"),
		CreatedAt:      "2024-01-01T00:00:00Z",
		UpdatedAt:      "2024-01-01T00:00:00Z",
	}
	child := shelley.Conversation{
		ConversationID: "child-conv-1",
		Slug:           strPtr("my-subagent"),
		CreatedAt:      "2024-01-01T01:00:00Z",
		UpdatedAt:      "2024-01-01T01:00:00Z",
	}

	server := mockserver.New(
		mockserver.WithFullConversation(parentConv, nil),
		mockserver.WithFullConversation(child, nil),
		mockserver.WithSubagent("parent-conv-id", "child-conv-1"),
	)
	defer server.Close()

	store := testStore(t)

	parentID, _ := store.Clone()
	store.MarkCreated(parentID, "parent-conv-id", "parent-slug")

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	subagentsDir := filepath.Join(mountDir, "conversation", parentID, "subagents")

	// Read the directory first to trigger adoption
	_, err := os.ReadDir(subagentsDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	// Get the child's local ID from state
	childLocalID := store.GetByShelleyID("child-conv-1")
	if childLocalID == "" {
		t.Fatal("child conversation was not adopted into state")
	}

	// Check symlink target for server ID
	target, err := os.Readlink(filepath.Join(subagentsDir, "child-conv-1"))
	if err != nil {
		t.Fatalf("Readlink failed for server ID: %v", err)
	}
	expectedTarget := "../../" + childLocalID
	if target != expectedTarget {
		t.Errorf("server ID symlink target: got %q, want %q", target, expectedTarget)
	}

	// Check symlink target for slug
	target, err = os.Readlink(filepath.Join(subagentsDir, "my-subagent"))
	if err != nil {
		t.Fatalf("Readlink failed for slug: %v", err)
	}
	if target != expectedTarget {
		t.Errorf("slug symlink target: got %q, want %q", target, expectedTarget)
	}

	// Check symlink target for local ID
	target, err = os.Readlink(filepath.Join(subagentsDir, childLocalID))
	if err != nil {
		t.Fatalf("Readlink failed for local ID: %v", err)
	}
	if target != expectedTarget {
		t.Errorf("local ID symlink target: got %q, want %q", target, expectedTarget)
	}
}

func TestSubagentsDir_EmptyWhenNoSubagents(t *testing.T) {
	parentConv := shelley.Conversation{
		ConversationID: "parent-conv-id",
		CreatedAt:      "2024-01-01T00:00:00Z",
		UpdatedAt:      "2024-01-01T00:00:00Z",
	}

	server := mockserver.New(
		mockserver.WithFullConversation(parentConv, nil),
	)
	defer server.Close()

	store := testStore(t)

	parentID, _ := store.Clone()
	store.MarkCreated(parentID, "parent-conv-id", "")

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	subagentsDir := filepath.Join(mountDir, "conversation", parentID, "subagents")

	entries, err := os.ReadDir(subagentsDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("expected 0 entries for conversation with no subagents, got %d", len(entries))
	}
}

func TestSubagentsDir_NotVisibleForUncreatedConversation(t *testing.T) {
	server := mockserver.New()
	defer server.Close()

	store := testStore(t)

	// Create a local conversation but don't mark it as created
	localID, _ := store.Clone()

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	subagentsPath := filepath.Join(mountDir, "conversation", localID, "subagents")

	_, err := os.Stat(subagentsPath)
	if err == nil {
		t.Error("expected subagents to not exist for uncreated conversation")
	}
	if !os.IsNotExist(err) {
		// Check if it's ENOENT
		var pathErr *os.PathError
		if pe, ok := err.(*os.PathError); ok {
			pathErr = pe
		}
		if pathErr != nil && pathErr.Err != syscall.ENOENT {
			t.Errorf("expected ENOENT, got: %v", err)
		}
	}
}

func TestSubagentsDir_LookupBySlug(t *testing.T) {
	parentConv := shelley.Conversation{
		ConversationID: "parent-id",
		CreatedAt:      "2024-01-01T00:00:00Z",
		UpdatedAt:      "2024-01-01T00:00:00Z",
	}
	child := shelley.Conversation{
		ConversationID: "child-id",
		Slug:           strPtr("my-agent"),
		CreatedAt:      "2024-01-01T01:00:00Z",
		UpdatedAt:      "2024-01-01T01:00:00Z",
	}

	server := mockserver.New(
		mockserver.WithFullConversation(parentConv, nil),
		mockserver.WithFullConversation(child, nil),
		mockserver.WithSubagent("parent-id", "child-id"),
	)
	defer server.Close()

	store := testStore(t)

	parentLocalID, _ := store.Clone()
	store.MarkCreated(parentLocalID, "parent-id", "")

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Lookup by slug should work (follows symlink to the conversation directory)
	slugPath := filepath.Join(mountDir, "conversation", parentLocalID, "subagents", "my-agent")

	// Readlink to verify it's a symlink
	target, err := os.Readlink(slugPath)
	if err != nil {
		t.Fatalf("Readlink failed for slug lookup: %v", err)
	}

	// Target should be ../../{childLocalID}
	childLocalID := store.GetByShelleyID("child-id")
	if childLocalID == "" {
		t.Fatal("child was not adopted")
	}
	expected := "../../" + childLocalID
	if target != expected {
		t.Errorf("slug symlink target: got %q, want %q", target, expected)
	}
}

func TestSubagentsDir_FollowSymlinkReachesConversation(t *testing.T) {
	parentConv := shelley.Conversation{
		ConversationID: "parent-id",
		CreatedAt:      "2024-01-01T00:00:00Z",
		UpdatedAt:      "2024-01-01T00:00:00Z",
	}
	child := shelley.Conversation{
		ConversationID: "child-id",
		Slug:           strPtr("worker"),
		CreatedAt:      "2024-01-01T01:00:00Z",
		UpdatedAt:      "2024-01-01T01:00:00Z",
	}

	server := mockserver.New(
		mockserver.WithFullConversation(parentConv, nil),
		mockserver.WithFullConversation(child, nil),
		mockserver.WithSubagent("parent-id", "child-id"),
	)
	defer server.Close()

	store := testStore(t)

	parentLocalID, _ := store.Clone()
	store.MarkCreated(parentLocalID, "parent-id", "")

	mountDir, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Follow the symlink - should reach the child conversation directory
	workerPath := filepath.Join(mountDir, "conversation", parentLocalID, "subagents", "worker")

	// Stat follows symlinks - should succeed and be a directory
	info, err := os.Stat(workerPath)
	if err != nil {
		t.Fatalf("Stat failed (following symlink): %v", err)
	}
	if !info.IsDir() {
		t.Error("expected symlink to resolve to a directory")
	}

	// Should be able to read fuse_id from the resolved conversation
	fuseIDPath := filepath.Join(workerPath, "fuse_id")
	data, err := os.ReadFile(fuseIDPath)
	if err != nil {
		t.Fatalf("ReadFile fuse_id failed: %v", err)
	}

	childLocalID := store.GetByShelleyID("child-id")
	if childLocalID == "" {
		t.Fatal("child was not adopted")
	}

	got := string(data)
	// fuse_id returns localID + newline
	expected := childLocalID + "\n"
	if got != expected {
		t.Errorf("fuse_id: got %q, want %q", got, expected)
	}
}
