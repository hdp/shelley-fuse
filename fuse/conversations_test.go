package fuse

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"shelley-fuse/mockserver"
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

func TestConversationListNode_ReaddirLocalOnly(t *testing.T) {
	// Server returns the conversations we've created locally
	server := mockConversationsServer(t, []shelley.Conversation{
		{ConversationID: "server-id-1"},
		{ConversationID: "server-id-2"},
	})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create some local conversations and mark them as created
	// (uncreated conversations are hidden from listing)
	id1, _ := store.Clone()
	store.MarkCreated(id1, "server-id-1", "")
	id2, _ := store.Clone()
	store.MarkCreated(id2, "server-id-2", "")

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 2 directories (local IDs) and 2 symlinks (server IDs)
	if len(dirs) != 2 {
		t.Fatalf("expected 2 directories, got %d: %v", len(dirs), dirs)
	}
	if len(symlinks) != 2 {
		t.Fatalf("expected 2 symlinks, got %d: %v", len(symlinks), symlinks)
	}

	// Check that our local IDs are in the directories
	sort.Strings(dirs)
	expectedDirs := []string{id1, id2}
	sort.Strings(expectedDirs)
	for i, name := range dirs {
		if name != expectedDirs[i] {
			t.Errorf("dir %d: expected %q, got %q", i, expectedDirs[i], name)
		}
	}
}

func TestConversationListNode_ReaddirServerConversationsAdopted(t *testing.T) {
	// Server returns conversations, no prior local state.
	// Readdir should adopt them all immediately, returning:
	// - 2 directories for local IDs
	// - 2 symlinks for server IDs
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-aaa"},
		{ConversationID: "server-conv-bbb"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Before Readdir, store should be empty
	if len(store.List()) != 0 {
		t.Fatal("expected empty store before Readdir")
	}

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else if entry.Mode&syscall.S_IFDIR != 0 {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 2 directories (adopted server conversations as local IDs)
	if len(dirs) != 2 {
		t.Fatalf("expected 2 directories, got %d: %v", len(dirs), dirs)
	}

	// Should have 2 symlinks (server IDs pointing to local IDs)
	if len(symlinks) != 2 {
		t.Fatalf("expected 2 symlinks, got %d: %v", len(symlinks), symlinks)
	}

	// Directory entries should be local IDs (8-char hex)
	for _, name := range dirs {
		if len(name) != 8 {
			t.Errorf("expected 8-char local ID for directory, got %q", name)
		}
	}

	// Symlink entries should be the server IDs
	serverIDSet := map[string]bool{"server-conv-aaa": true, "server-conv-bbb": true}
	for _, name := range symlinks {
		if !serverIDSet[name] {
			t.Errorf("unexpected symlink %q, expected server IDs", name)
		}
	}

	// Verify the conversations were adopted into local state
	localIDs := store.List()
	if len(localIDs) != 2 {
		t.Fatalf("expected 2 conversations in store after Readdir, got %d", len(localIDs))
	}

	// Verify each adopted conversation has the correct Shelley ID
	shelleyIDs := make(map[string]bool)
	for _, id := range localIDs {
		cs := store.Get(id)
		if cs == nil {
			t.Fatalf("expected conversation state for %s", id)
		}
		if !cs.Created {
			t.Errorf("expected Created=true for adopted conversation %s", id)
		}
		shelleyIDs[cs.ShelleyConversationID] = true
	}
	if !shelleyIDs["server-conv-aaa"] {
		t.Error("server-conv-aaa was not adopted")
	}
	if !shelleyIDs["server-conv-bbb"] {
		t.Error("server-conv-bbb was not adopted")
	}
}

func TestConversationListNode_ReaddirMergedLocalAndServer(t *testing.T) {
	// Server returns some conversations, some overlap with local.
	// Readdir returns:
	// - Directories for created local IDs that are on server (3 total after adoption)
	// - Symlinks for server IDs (3 total)
	// Note: conversations with Shelley IDs not on server are filtered out (stale)
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-111"},
		{ConversationID: "server-conv-222"}, // This one is already tracked locally
		{ConversationID: "server-conv-333"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create a local conversation tracking a server conversation
	localTracked, _ := store.Clone()
	_ = store.MarkCreated(localTracked, "server-conv-222", "") // This tracks server-conv-222

	// Before Readdir: 1 local conversation
	if len(store.List()) != 1 {
		t.Fatalf("expected 1 conversation before Readdir, got %d", len(store.List()))
	}

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else if entry.Mode&syscall.S_IFDIR != 0 {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 3 directories:
	// - localTracked (existing local ID, tracks server-conv-222)
	// - new local ID for server-conv-111 (adopted)
	// - new local ID for server-conv-333 (adopted)
	if len(dirs) != 3 {
		t.Fatalf("expected 3 directories, got %d: %v", len(dirs), dirs)
	}

	// Should have 3 symlinks for server IDs:
	// - server-conv-111 -> its local ID
	// - server-conv-222 -> localTracked
	// - server-conv-333 -> its local ID
	if len(symlinks) != 3 {
		t.Fatalf("expected 3 symlinks, got %d: %v", len(symlinks), symlinks)
	}

	// Directory entries should be local IDs (8-char hex)
	for _, name := range dirs {
		if len(name) != 8 {
			t.Errorf("expected 8-char local ID for directory, got %q", name)
		}
	}

	// Symlink entries should be the server IDs
	serverIDSet := map[string]bool{"server-conv-111": true, "server-conv-222": true, "server-conv-333": true}
	for _, name := range symlinks {
		if !serverIDSet[name] {
			t.Errorf("unexpected symlink %q, expected server IDs", name)
		}
	}

	// Verify localTracked is still present as a directory
	foundLocalTracked := false
	for _, name := range dirs {
		if name == localTracked {
			foundLocalTracked = true
		}
	}
	if !foundLocalTracked {
		t.Errorf("localTracked %s not found in directories", localTracked)
	}

	// After Readdir: should have 3 conversations (1 original + 2 adopted)
	localIDs := store.List()
	if len(localIDs) != 3 {
		t.Fatalf("expected 3 conversations in store after Readdir, got %d", len(localIDs))
	}

	// Verify all server conversations are now tracked
	for _, shelleyID := range []string{"server-conv-111", "server-conv-222", "server-conv-333"} {
		localID := store.GetByShelleyID(shelleyID)
		if localID == "" {
			t.Errorf("server conversation %s should be tracked locally", shelleyID)
		}
	}

	// Verify server-conv-222 is still tracked by localTracked (not duplicated)
	if store.GetByShelleyID("server-conv-222") != localTracked {
		t.Errorf("server-conv-222 should still be tracked by %s", localTracked)
	}
}

func TestConversationListNode_ReaddirServerError(t *testing.T) {
	// Server returns an error - should still show created local conversations
	// When server fails, we show all local entries including symlinks for server IDs
	// (since we can't verify if they're stale)
	server := mockErrorServer(t)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create local conversations and mark them as created
	// (uncreated conversations are hidden from listing)
	id1, _ := store.Clone()
	store.MarkCreated(id1, "server-id-1", "")
	id2, _ := store.Clone()
	store.MarkCreated(id2, "server-id-2", "")

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 2 directories (local IDs) and 2 symlinks (server IDs)
	if len(dirs) != 2 {
		t.Fatalf("expected 2 directories, got %d: %v", len(dirs), dirs)
	}
	if len(symlinks) != 2 {
		t.Fatalf("expected 2 symlinks, got %d: %v", len(symlinks), symlinks)
	}
}

func TestConversationListNode_ReaddirFiltersStaleConversations(t *testing.T) {
	// Test that conversations with a Shelley ID that no longer exists on server
	// are filtered out from Readdir results (prevents broken symlinks)

	// Server returns only conv-active, NOT conv-deleted
	slug := "active-slug"
	server := mockConversationsServer(t, []shelley.Conversation{
		{ConversationID: "conv-active", Slug: &slug},
	})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Adopt conversations that exist on server
	activeLocalID, _ := store.Adopt("conv-active")

	// Adopt a conversation that NO LONGER exists on server (stale)
	staleLocalID, _ := store.Adopt("conv-deleted")

	// Verify both are in the store before Readdir
	if len(store.List()) != 2 {
		t.Fatalf("expected 2 conversations in store, got %d", len(store.List()))
	}

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
	}

	// Should see:
	// - activeLocalID (local ID as directory)
	// - conv-active (symlink to active)
	// - active-slug (symlink, now that AdoptWithSlug updates empty slugs)
	// Should NOT see:
	// - staleLocalID or conv-deleted (filtered out because conv-deleted not on server)

	// Check that stale entries are NOT present
	for _, name := range names {
		if name == staleLocalID {
			t.Errorf("stale local ID %q should not appear in Readdir", staleLocalID)
		}
		if name == "conv-deleted" {
			t.Error("stale server ID 'conv-deleted' should not appear in Readdir")
		}
	}

	// Check that expected entries ARE present
	namesSet := make(map[string]bool)
	for _, name := range names {
		namesSet[name] = true
	}

	// AdoptWithSlug now updates empty slugs, so we should see the slug symlink
	expected := []string{activeLocalID, "conv-active", "active-slug"}
	for _, exp := range expected {
		if !namesSet[exp] {
			t.Errorf("expected entry %q not found in Readdir results: %v", exp, names)
		}
	}

	// Verify total count: 3 entries (1 dir + 2 symlinks for server ID and slug)
	if len(names) != 3 {
		t.Errorf("expected 3 entries, got %d: %v", len(names), names)
	}
}

func TestConversationListNode_ReaddirShowsStaleWhenServerFails(t *testing.T) {
	// When server is unreachable, we should show ALL created local entries
	// including ones with Shelley IDs (since we can't verify them)
	// Note: uncreated conversations are still hidden from listing
	server := mockErrorServer(t)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Adopt a conversation (simulating one that might be stale)
	// Adopt automatically marks it as created
	adoptedLocalID, _ := store.Adopt("conv-possibly-stale")

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
	}

	// When server fails, should see all created entries:
	// - adoptedLocalID (directory)
	// - conv-possibly-stale (symlink)
	namesSet := make(map[string]bool)
	for _, name := range names {
		namesSet[name] = true
	}

	expected := []string{adoptedLocalID, "conv-possibly-stale"}
	for _, exp := range expected {
		if !namesSet[exp] {
			t.Errorf("expected entry %q not found when server fails: %v", exp, names)
		}
	}

	if len(names) != 2 {
		t.Errorf("expected 2 entries when server fails, got %d: %v", len(names), names)
	}
}

// Helper to mount a test filesystem and return mount point and cleanup function

func TestConversationListNode_LookupLocalID(t *testing.T) {
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Lookup by stat'ing the conversation directory
	info, err := os.Stat(filepath.Join(mountPoint, "conversation", localID))
	if err != nil {
		t.Fatalf("Lookup for local ID should succeed: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestConversationListNode_LookupNonexistent(t *testing.T) {
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	store := testStore(t)

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Lookup should fail for nonexistent ID
	_, err := os.Stat(filepath.Join(mountPoint, "conversation", "nonexistent-id"))
	if err == nil {
		t.Error("Lookup for nonexistent ID should fail")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected ENOENT, got %v", err)
	}
}

func TestConversationListNode_LookupServerConversation(t *testing.T) {
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-lookup-test"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	store := testStore(t)

	// Before lookup, store should be empty
	if len(store.List()) != 0 {
		t.Fatal("expected empty store before lookup")
	}

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Lookup the server conversation by its ID
	info, err := os.Stat(filepath.Join(mountPoint, "conversation", "server-conv-lookup-test"))
	if err != nil {
		t.Fatalf("Lookup for server conversation should succeed: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}

	// After lookup, the conversation should be adopted into local state
	ids := store.List()
	if len(ids) != 1 {
		t.Fatalf("expected 1 conversation after adoption, got %d", len(ids))
	}

	// Verify the adopted conversation has the correct Shelley ID
	cs := store.Get(ids[0])
	if cs == nil {
		t.Fatal("expected conversation state")
	}
	if cs.ShelleyConversationID != "server-conv-lookup-test" {
		t.Errorf("expected ShelleyConversationID=server-conv-lookup-test, got %s", cs.ShelleyConversationID)
	}
	if !cs.Created {
		t.Error("expected Created=true for adopted conversation")
	}
}

func TestConversationListNode_LookupServerConversationIdempotent(t *testing.T) {
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-idempotent"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	store := testStore(t)

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Lookup twice
	_, err := os.Stat(filepath.Join(mountPoint, "conversation", "server-conv-idempotent"))
	if err != nil {
		t.Fatalf("first Lookup failed: %v", err)
	}

	_, err = os.Stat(filepath.Join(mountPoint, "conversation", "server-conv-idempotent"))
	if err != nil {
		t.Fatalf("second Lookup failed: %v", err)
	}

	// Should still only have one conversation
	ids := store.List()
	if len(ids) != 1 {
		t.Errorf("expected 1 conversation, got %d", len(ids))
	}
}

func TestConversationListNode_LookupServerError(t *testing.T) {
	server := mockErrorServer(t)
	defer server.Close()

	store := testStore(t)

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Lookup for a non-local ID when server errors should fail
	_, err := os.Stat(filepath.Join(mountPoint, "conversation", "some-server-id"))
	if err == nil {
		t.Error("Lookup should fail when server errors and ID is not local")
	}
}

func TestConversationListNode_LookupLocalTakesPrecedence(t *testing.T) {
	// Server has a conversation, but we also have it tracked locally
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-precedence"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	store := testStore(t)

	// Track the conversation locally first
	localID, _ := store.Clone()
	_ = store.MarkCreated(localID, "server-conv-precedence", "")

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	// Lookup by local ID should work
	info, err := os.Stat(filepath.Join(mountPoint, "conversation", localID))
	if err != nil {
		t.Fatalf("Lookup by local ID should succeed: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}

	// Should still only have one conversation (no duplicate created)
	ids := store.List()
	if len(ids) != 1 {
		t.Errorf("expected 1 conversation, got %d", len(ids))
	}
}


func TestConversationListingMounted(t *testing.T) {
	serverConvs := []shelley.Conversation{
		{ConversationID: "mounted-server-conv-1"},
		{ConversationID: "mounted-server-conv-2"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Server conversations will be adopted during Readdir
	// Note: uncreated local conversations are not shown in listing

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-conv-list-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Read the conversation directory
	entries, err := ioutil.ReadDir(filepath.Join(tmpDir, "conversation"))
	if err != nil {
		t.Fatalf("Failed to read conversation directory: %v", err)
	}

	// Separate directories (local IDs) from symlinks (server IDs)
	var dirs, symlinks []string
	for _, entry := range entries {
		if entry.Mode()&os.ModeSymlink != 0 {
			symlinks = append(symlinks, entry.Name())
		} else if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}

	// Should have 2 directories: 2 adopted server conversations
	// (uncreated local conversations are hidden from listing)
	if len(dirs) != 2 {
		t.Fatalf("expected 2 directories, got %d: %v", len(dirs), dirs)
	}

	// Should have 2 symlinks for server IDs
	if len(symlinks) != 2 {
		t.Fatalf("expected 2 symlinks, got %d: %v", len(symlinks), symlinks)
	}

	// Directory entries should be local IDs (8-char hex)
	for _, name := range dirs {
		if len(name) != 8 {
			t.Errorf("expected 8-char local ID for directory, got %q", name)
		}
	}

	// Symlink entries should be the server IDs
	for _, name := range symlinks {
		if !strings.HasPrefix(name, "mounted-server-conv-") {
			t.Errorf("expected server ID symlink, got %q", name)
		}
	}

	// Verify server conversations were adopted
	for _, shelleyID := range []string{"mounted-server-conv-1", "mounted-server-conv-2"} {
		localID := store.GetByShelleyID(shelleyID)
		if localID == "" {
			t.Errorf("server conversation %s should be tracked locally", shelleyID)
		}
	}
}


func TestConversationListNode_ReaddirUpdatesEmptySlugs(t *testing.T) {
	// This test verifies that AdoptWithSlug correctly updates empty slugs
	// for already-tracked conversations when rediscovered via Readdir.

	// Server returns a conversation with a slug
	slug := "my-conversation-slug"
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-slug-update", Slug: &slug},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Adopt the conversation WITHOUT a slug (simulating old adoption)
	localID, err := store.AdoptWithSlug("server-conv-slug-update", "")
	if err != nil {
		t.Fatalf("AdoptWithSlug failed: %v", err)
	}

	// Verify slug is empty initially
	cs := store.Get(localID)
	if cs.Slug != "" {
		t.Fatalf("Expected empty slug initially, got %q", cs.Slug)
	}

	// Readdir should update the slug for already-tracked conversations
	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	// Collect all entries
	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else if entry.Mode&syscall.S_IFDIR != 0 {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 1 directory (local ID)
	if len(dirs) != 1 {
		t.Fatalf("Expected 1 directory, got %d: %v", len(dirs), dirs)
	}
	if dirs[0] != localID {
		t.Errorf("Expected directory %q, got %q", localID, dirs[0])
	}

	// Should have 2 symlinks: server ID and slug (slug now updated)
	if len(symlinks) != 2 {
		t.Fatalf("Expected 2 symlinks (server ID and slug), got %d: %v", len(symlinks), symlinks)
	}

	// Verify both symlinks are present
	symlinkSet := make(map[string]bool)
	for _, s := range symlinks {
		symlinkSet[s] = true
	}
	if !symlinkSet["server-conv-slug-update"] {
		t.Errorf("Expected server ID symlink 'server-conv-slug-update', got %v", symlinks)
	}
	if !symlinkSet[slug] {
		t.Errorf("Expected slug symlink %q, got %v", slug, symlinks)
	}

	// Verify the state WAS updated with the slug
	cs = store.Get(localID)
	if cs.Slug != slug {
		t.Errorf("State slug should be updated: got %q, want %q", cs.Slug, slug)
	}
}

// TestConversationListNode_ReaddirWithSlugs tests that conversations with slugs
// appear correctly in the directory listing with slug symlinks.
func TestConversationListNode_ReaddirWithSlugs(t *testing.T) {
	// Server returns conversations with slugs
	slug1 := "first-conversation"
	slug2 := "second-conversation"
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-with-slug-1", Slug: &slug1},
		{ConversationID: "server-conv-with-slug-2", Slug: &slug2},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	node := &ConversationListNode{client: client, state: store, cloneTimeout: time.Hour}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	// Collect all entries
	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else if entry.Mode&syscall.S_IFDIR != 0 {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 2 directories (local IDs)
	if len(dirs) != 2 {
		t.Fatalf("Expected 2 directories, got %d: %v", len(dirs), dirs)
	}

	// Should have 4 symlinks: 2 server IDs + 2 slugs
	if len(symlinks) != 4 {
		t.Fatalf("Expected 4 symlinks (2 server IDs + 2 slugs), got %d: %v", len(symlinks), symlinks)
	}

	// Verify both slugs are present as symlinks
	expectedSymlinks := map[string]bool{
		"server-conv-with-slug-1": false,
		"server-conv-with-slug-2": false,
		"first-conversation":      false,
		"second-conversation":     false,
	}
	for _, name := range symlinks {
		if _, ok := expectedSymlinks[name]; ok {
			expectedSymlinks[name] = true
		}
	}
	for name, found := range expectedSymlinks {
		if !found {
			t.Errorf("Expected symlink %q not found", name)
		}
	}

	// Verify slugs were persisted in state
	for _, localID := range dirs {
		cs := store.Get(localID)
		if cs == nil {
			t.Errorf("Missing state for local ID %s", localID)
			continue
		}
		if cs.Slug == "" {
			t.Errorf("Expected non-empty slug for local ID %s", localID)
		}
	}
}


func TestTimestamps_ConversationNodesUseCreatedAt(t *testing.T) {
	// Test that conversation nodes use conversation creation time
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Wait a bit so we can distinguish start time from conversation time
	time.Sleep(10 * time.Millisecond)

	// Clone a conversation - this sets CreatedAt
	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	// Get the conversation creation time
	cs := store.Get(convID)
	if cs == nil {
		t.Fatal("Conversation not found in store")
	}
	convTime := cs.CreatedAt
	if convTime.IsZero() {
		t.Fatal("Conversation CreatedAt is zero")
	}

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-conv-timestamp-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Test conversation directory timestamp
	t.Run("ConversationDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID))
		if err != nil {
			t.Fatalf("Failed to stat conversation dir: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Conversation dir mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Conversation dir mtime is zero (1970)")
		}
	})

	// Test ctl file timestamp
	t.Run("CtlFile", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID, "ctl"))
		if err != nil {
			t.Fatalf("Failed to stat ctl: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Ctl mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Ctl mtime is zero (1970)")
		}
	})

	// Test send file timestamp
	t.Run("SendFile", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID, "send"))
		if err != nil {
			t.Fatalf("Failed to stat send: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Send file mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Send file mtime is zero (1970)")
		}
	})

	// Test fuse_id file timestamp (status fields are now at conversation root)
	t.Run("FuseIdFile", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID, "fuse_id"))
		if err != nil {
			t.Fatalf("Failed to stat fuse_id: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("fuse_id mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("fuse_id mtime is zero (1970)")
		}
	})

	// Test last directory timestamp
	t.Run("LastDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID, "messages", "last"))
		if err != nil {
			t.Fatalf("Failed to stat last: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Last mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Last mtime is zero (1970)")
		}
	})

	// Test since directory timestamp
	t.Run("SinceDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID, "messages", "since"))
		if err != nil {
			t.Fatalf("Failed to stat since: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Since mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Since mtime is zero (1970)")
		}
	})

}


func TestTimestamps_ConversationTimeDiffersFromStartTime(t *testing.T) {
	// Test that conversation time is different from FS start time
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	startTime := shelleyFS.StartTime()

	// Wait a bit so conversation time is clearly different
	time.Sleep(50 * time.Millisecond)

	// Clone a conversation
	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-time-diff-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Get models mtime (should be startTime)
	modelsInfo, err := os.Stat(filepath.Join(tmpDir, "model"))
	if err != nil {
		t.Fatalf("Failed to stat models: %v", err)
	}
	modelsMtime := modelsInfo.ModTime()

	// Get conversation mtime (should be convTime, later than startTime)
	convInfo, err := os.Stat(filepath.Join(tmpDir, "conversation", convID))
	if err != nil {
		t.Fatalf("Failed to stat conversation: %v", err)
	}
	convMtime := convInfo.ModTime()

	// Models should use startTime
	modelsDiff := modelsMtime.Sub(startTime)
	if modelsDiff < -time.Second || modelsDiff > time.Second {
		t.Errorf("Models mtime %v should be close to startTime %v", modelsMtime, startTime)
	}

	// Conversation should be later than startTime
	if !convMtime.After(startTime) {
		t.Errorf("Conversation mtime %v should be after startTime %v", convMtime, startTime)
	}

	// Conversation should be different from models
	if modelsMtime.Equal(convMtime) {
		t.Error("Conversation mtime should differ from models mtime")
	}

	t.Logf("startTime: %v, modelsMtime: %v, convMtime: %v", startTime, modelsMtime, convMtime)
}


func TestTimestamps_SymlinksUseConversationTime(t *testing.T) {
	// Test that symlinks for server IDs use conversation creation time
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-123"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-symlink-timestamp-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// List conversations to trigger adoption
	_, err = ioutil.ReadDir(filepath.Join(tmpDir, "conversation"))
	if err != nil {
		t.Fatalf("Failed to read conversation dir: %v", err)
	}

	// Get the symlink info (use Lstat to not follow)
	symlinkInfo, err := os.Lstat(filepath.Join(tmpDir, "conversation", "server-conv-123"))
	if err != nil {
		t.Fatalf("Failed to lstat symlink: %v", err)
	}

	mtime := symlinkInfo.ModTime()

	// Should not be zero
	if mtime.Unix() == 0 {
		t.Error("Symlink mtime is zero (1970)")
	}

	// Should be a reasonable recent time
	if time.Since(mtime) > time.Hour {
		t.Errorf("Symlink mtime %v is more than 1 hour ago", mtime)
	}
}


func TestTimestamps_MultipleConversationsHaveDifferentTimes(t *testing.T) {
	// Test that different conversations have different creation times
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Clone first conversation
	convID1, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone first: %v", err)
	}

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Clone second conversation
	convID2, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone second: %v", err)
	}

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-multi-conv-timestamp-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Get timestamps for both conversations
	info1, err := os.Stat(filepath.Join(tmpDir, "conversation", convID1))
	if err != nil {
		t.Fatalf("Failed to stat conv1: %v", err)
	}
	mtime1 := info1.ModTime()

	info2, err := os.Stat(filepath.Join(tmpDir, "conversation", convID2))
	if err != nil {
		t.Fatalf("Failed to stat conv2: %v", err)
	}
	mtime2 := info2.ModTime()

	// Second conversation should be later
	if !mtime2.After(mtime1) {
		t.Errorf("Second conversation mtime %v should be after first %v", mtime2, mtime1)
	}

	t.Logf("conv1 mtime: %v, conv2 mtime: %v, diff: %v", mtime1, mtime2, mtime2.Sub(mtime1))
}


func TestTimestamps_StateCreatedAtIsPersisted(t *testing.T) {
	// Test that CreatedAt is persisted to the state file and survives reload
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	// Create store and clone
	store1, err := state.NewStore(statePath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	convID, err := store1.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	cs1 := store1.Get(convID)
	if cs1.CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be set after clone")
	}
	originalTime := cs1.CreatedAt

	// Create new store from same path (simulating restart)
	store2, err := state.NewStore(statePath)
	if err != nil {
		t.Fatalf("Failed to reload store: %v", err)
	}

	cs2 := store2.Get(convID)
	if cs2 == nil {
		t.Fatal("Conversation not found after reload")
	}

	if cs2.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero after reload")
	}

	// Times should be equal (within nanosecond precision loss from JSON)
	diff := cs2.CreatedAt.Sub(originalTime)
	if diff < -time.Microsecond || diff > time.Microsecond {
		t.Errorf("CreatedAt changed after reload: %v -> %v (diff: %v)", originalTime, cs2.CreatedAt, diff)
	}
}

func TestConversationNode_ModelSymlink_NoModel(t *testing.T) {
	// Test that model symlink returns ENOENT when model is not set
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	// Create mount
	client := shelley.NewClient("http://example.com")
	shelleyFS := NewFS(client, store, 0)

	tmpDir := t.TempDir()
	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Try to read model symlink - should fail with ENOENT
	modelPath := filepath.Join(tmpDir, "conversation", convID, "model")
	_, err = os.Lstat(modelPath)
	if err == nil {
		t.Error("Expected error for model symlink when model not set")
	} else if !os.IsNotExist(err) {
		t.Errorf("Expected ENOENT, got: %v", err)
	}
}

func TestConversationNode_ModelSymlink_WithModel(t *testing.T) {
	// Test that model symlink is created and points to correct target
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	// Set model
	err = store.SetCtl(convID, "model", "claude-opus-4")
	if err != nil {
		t.Fatalf("Failed to set model: %v", err)
	}

	// Create mount
	client := shelley.NewClient("http://example.com")
	shelleyFS := NewFS(client, store, 0)

	tmpDir := t.TempDir()
	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	modelPath := filepath.Join(tmpDir, "conversation", convID, "model")

	// Check that symlink exists
	info, err := os.Lstat(modelPath)
	if err != nil {
		t.Fatalf("Failed to lstat model symlink: %v", err)
	}

	// Verify it's a symlink
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("Expected symlink, got mode %v", info.Mode())
	}

	// Read the symlink target
	target, err := os.Readlink(modelPath)
	if err != nil {
		t.Fatalf("Failed to readlink: %v", err)
	}

	expectedTarget := "../../model/claude-opus-4"
	if target != expectedTarget {
		t.Errorf("Expected target %q, got %q", expectedTarget, target)
	}
}

func TestConversationNode_ModelSymlink_InReaddir(t *testing.T) {
	// Test that model symlink appears in Readdir when model is set
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	// Create mount without model set first
	client := shelley.NewClient("http://example.com")
	shelleyFS := NewFS(client, store, 0)

	tmpDir := t.TempDir()
	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	convDir := filepath.Join(tmpDir, "conversation", convID)

	// Read dir without model - should NOT contain model entry
	entries, err := ioutil.ReadDir(convDir)
	if err != nil {
		t.Fatalf("Failed to read dir: %v", err)
	}

	hasModel := false
	for _, e := range entries {
		if e.Name() == "model" {
			hasModel = true
			break
		}
	}
	if hasModel {
		t.Error("model symlink should not appear when model is not set")
	}

	// Now set the model
	err = store.SetCtl(convID, "model", "test-model")
	if err != nil {
		t.Fatalf("Failed to set model: %v", err)
	}

	// Read dir again - should now contain model entry
	entries, err = ioutil.ReadDir(convDir)
	if err != nil {
		t.Fatalf("Failed to read dir after setting model: %v", err)
	}

	hasModel = false
	var modelEntry os.FileInfo
	for _, e := range entries {
		if e.Name() == "model" {
			hasModel = true
			modelEntry = e
			break
		}
	}
	if !hasModel {
		t.Error("model symlink should appear when model is set")
	}

	// Verify it's listed as a symlink
	if modelEntry != nil && modelEntry.Mode()&os.ModeSymlink == 0 {
		t.Errorf("model entry should be a symlink, got mode %v", modelEntry.Mode())
	}
}

func TestConversationNode_ModelSymlink_Timestamp(t *testing.T) {
	// Test that model symlink uses conversation creation time
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	cs := store.Get(convID)
	convTime := cs.CreatedAt

	// Set model
	err = store.SetCtl(convID, "model", "test-model")
	if err != nil {
		t.Fatalf("Failed to set model: %v", err)
	}

	// Create mount
	client := shelley.NewClient("http://example.com")
	shelleyFS := NewFS(client, store, 0)

	tmpDir := t.TempDir()
	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	modelPath := filepath.Join(tmpDir, "conversation", convID, "model")

	info, err := os.Lstat(modelPath)
	if err != nil {
		t.Fatalf("Failed to lstat model symlink: %v", err)
	}

	mtime := info.ModTime()
	diff := mtime.Sub(convTime)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("Model symlink mtime %v differs from conversation time %v by %v", mtime, convTime, diff)
	}
}

func TestTimestamps_APIMetadataMapping(t *testing.T) {
	// Create a mock server with conversations that have API timestamps
	convs := []shelley.Conversation{
		{
			ConversationID: "conv-with-timestamps",
			Slug:           strPtr("test-conv"),
			CreatedAt:      "2024-01-15T10:30:00Z",
			UpdatedAt:      "2024-01-16T14:20:00Z",
		},
	}
	server := mockConversationsServer(t, convs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Mount the filesystem
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-api-metadata-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// List conversations to trigger adoption with metadata
	_, err = ioutil.ReadDir(filepath.Join(tmpDir, "conversation"))
	if err != nil {
		t.Fatalf("Failed to read conversation dir: %v", err)
	}

	// Get the local ID for the adopted conversation
	localID := store.GetByShelleyID("conv-with-timestamps")
	if localID == "" {
		t.Fatal("Conversation was not adopted")
	}

	// Verify the API timestamps were stored
	cs := store.Get(localID)
	if cs == nil {
		t.Fatal("Conversation state not found")
	}
	if cs.APICreatedAt != "2024-01-15T10:30:00Z" {
		t.Errorf("APICreatedAt = %q, want %q", cs.APICreatedAt, "2024-01-15T10:30:00Z")
	}
	if cs.APIUpdatedAt != "2024-01-16T14:20:00Z" {
		t.Errorf("APIUpdatedAt = %q, want %q", cs.APIUpdatedAt, "2024-01-16T14:20:00Z")
	}

	// Stat the conversation directory and verify timestamps
	convPath := filepath.Join(tmpDir, "conversation", localID)
	info, err := os.Stat(convPath)
	if err != nil {
		t.Fatalf("Failed to stat conversation: %v", err)
	}

	// Parse expected times
	expectedMtime, _ := time.Parse(time.RFC3339, "2024-01-16T14:20:00Z")

	// mtime should be the updated_at time
	actualMtime := info.ModTime()
	if !actualMtime.Equal(expectedMtime) {
		t.Errorf("Conversation mtime = %v, want %v (from updated_at)", actualMtime, expectedMtime)
	}
}

// TestTimestamps_APIMetadataMtimeDiffersFromCtime tests that mtime uses updated_at
// while ctime uses created_at when both are available.
func TestTimestamps_APIMetadataMtimeDiffersFromCtime(t *testing.T) {
	// Create a conversation with different created_at and updated_at
	convs := []shelley.Conversation{
		{
			ConversationID: "conv-mtime-ctime",
			Slug:           strPtr("mtime-ctime-test"),
			CreatedAt:      "2024-01-10T08:00:00Z",
			UpdatedAt:      "2024-01-20T16:30:00Z",
		},
	}
	server := mockConversationsServer(t, convs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-mtime-ctime-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Trigger adoption
	_, _ = ioutil.ReadDir(filepath.Join(tmpDir, "conversation"))

	localID := store.GetByShelleyID("conv-mtime-ctime")
	if localID == "" {
		t.Fatal("Conversation was not adopted")
	}

	// Use syscall.Stat to get all timestamps (ctime requires syscall)
	convPath := filepath.Join(tmpDir, "conversation", localID)
	var stat syscall.Stat_t
	if err := syscall.Stat(convPath, &stat); err != nil {
		t.Fatalf("syscall.Stat failed: %v", err)
	}

	expectedCtime, _ := time.Parse(time.RFC3339, "2024-01-10T08:00:00Z")
	expectedMtime, _ := time.Parse(time.RFC3339, "2024-01-20T16:30:00Z")

	actualCtime := time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec)
	actualMtime := time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec)

	if !actualCtime.Equal(expectedCtime) {
		t.Errorf("ctime = %v, want %v (from created_at)", actualCtime, expectedCtime)
	}
	if !actualMtime.Equal(expectedMtime) {
		t.Errorf("mtime = %v, want %v (from updated_at)", actualMtime, expectedMtime)
	}

	// ctime and mtime should be different
	if actualCtime.Equal(actualMtime) {
		t.Error("ctime and mtime should be different when created_at != updated_at")
	}
}

// TestTimestamps_APIMetadataFallbackToLocalTime tests that when API timestamps
// are not available, we fall back to local CreatedAt time.
func TestTimestamps_APIMetadataFallbackToLocalTime(t *testing.T) {
	// Mock server with no conversations (we'll use a locally cloned one)
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	startTime := shelleyFS.StartTime()

	// Wait a bit so clone time is clearly different
	time.Sleep(50 * time.Millisecond)

	// Clone locally (no API timestamps)
	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	cs := store.Get(convID)
	if cs == nil {
		t.Fatal("Conversation state not found")
	}
	localCreatedAt := cs.CreatedAt

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-fallback-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	convPath := filepath.Join(tmpDir, "conversation", convID)
	info, err := os.Stat(convPath)
	if err != nil {
		t.Fatalf("Failed to stat conversation: %v", err)
	}

	// mtime should be the local CreatedAt (since no API timestamps)
	actualMtime := info.ModTime()

	// Allow 1 second tolerance for timing
	diff := actualMtime.Sub(localCreatedAt)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("Conversation mtime %v should be close to local CreatedAt %v", actualMtime, localCreatedAt)
	}

	// Should be different from startTime
	if actualMtime.Sub(startTime) < 40*time.Millisecond {
		t.Errorf("Conversation mtime should be after startTime (waited 50ms before clone)")
	}
}

func TestTimestamps_ConversationUpdatedAtUpdatesOnReadopt(t *testing.T) {
	// First adoption with older timestamps
	store := testStore(t)
	_, err := store.AdoptWithMetadata("conv-update-test", "slug", "2024-01-01T00:00:00Z", "2024-01-05T00:00:00Z")
	if err != nil {
		t.Fatalf("First adoption failed: %v", err)
	}

	// Re-adopt with newer updated_at
	_, err = store.AdoptWithMetadata("conv-update-test", "", "", "2024-01-10T00:00:00Z")
	if err != nil {
		t.Fatalf("Second adoption failed: %v", err)
	}

	localID := store.GetByShelleyID("conv-update-test")
	cs := store.Get(localID)

	// created_at should not change
	if cs.APICreatedAt != "2024-01-01T00:00:00Z" {
		t.Errorf("APICreatedAt should not change, got %q", cs.APICreatedAt)
	}

	// updated_at should be the newer value
	if cs.APIUpdatedAt != "2024-01-10T00:00:00Z" {
		t.Errorf("APIUpdatedAt should be updated to newer value, got %q", cs.APIUpdatedAt)
	}
}


// TestConversationAPITimestampFields tests that created_at and updated_at are exposed at the conversation root.
func TestConversationAPITimestampFields(t *testing.T) {
	convID := "test-timestamp-conv-id"
	convSlug := "test-timestamp-slug"
	convCreatedAt := "2024-01-15T10:30:00Z"
	convUpdatedAt := "2024-01-15T11:00:00Z"

	conv := shelley.Conversation{
		ConversationID: convID,
		Slug:           &convSlug,
		CreatedAt:      convCreatedAt,
		UpdatedAt:      convUpdatedAt,
	}
	rawDetail, _ := json.Marshal(conv)
	server := mockserver.New(mockserver.WithConversationRawDetail(conv, rawDetail))
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()
	store.SetCtl(localID, "model", "claude-3-opus")
	store.MarkCreated(localID, convID, convSlug)

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	convDir := filepath.Join(mountPoint, "conversation", localID)

	// Test created_at field
	data, err := ioutil.ReadFile(filepath.Join(convDir, "created_at"))
	if err != nil {
		t.Fatalf("Failed to read created_at: %v", err)
	}
	if string(data) != convCreatedAt+"\n" {
		t.Errorf("created_at: expected %q, got %q", convCreatedAt+"\n", string(data))
	}

	// Test updated_at field
	data, err = ioutil.ReadFile(filepath.Join(convDir, "updated_at"))
	if err != nil {
		t.Fatalf("Failed to read updated_at: %v", err)
	}
	if string(data) != convUpdatedAt+"\n" {
		t.Errorf("updated_at: expected %q, got %q", convUpdatedAt+"\n", string(data))
	}

	// Verify directory listing includes the timestamp fields
	entries, err := ioutil.ReadDir(convDir)
	if err != nil {
		t.Fatalf("Failed to read conversation dir: %v", err)
	}
	entryMap := make(map[string]bool)
	for _, e := range entries {
		entryMap[e.Name()] = true
	}
	if !entryMap["created_at"] {
		t.Error("created_at should be listed in directory")
	}
	if !entryMap["updated_at"] {
		t.Error("updated_at should be listed in directory")
	}
}

// TestConversationAPITimestampFields_UncreatedConversation tests that timestamp fields don't exist for uncreated conversations.
func TestConversationAPITimestampFields_UncreatedConversation(t *testing.T) {
	server := mockserver.New() // no conversations registered
	defer server.Close()

	store := testStore(t)
	localID, _ := store.Clone()
	// Don't mark as created - this is an uncreated local conversation
	store.SetCtl(localID, "model", "claude-3-haiku")

	mountPoint, cleanup := mountTestFSWithServer(t, server, store)
	defer cleanup()

	convDir := filepath.Join(mountPoint, "conversation", localID)

	// created_at should not exist for uncreated conversation
	_, err := os.Stat(filepath.Join(convDir, "created_at"))
	if err == nil {
		t.Error("created_at should not exist for uncreated conversation")
	}

	// updated_at should not exist for uncreated conversation
	_, err = os.Stat(filepath.Join(convDir, "updated_at"))
	if err == nil {
		t.Error("updated_at should not exist for uncreated conversation")
	}

	// Verify directory listing doesn't include timestamp fields
	entries, err := ioutil.ReadDir(convDir)
	if err != nil {
		t.Fatalf("Failed to read conversation dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() == "created_at" || e.Name() == "updated_at" {
			t.Errorf("%s should not be listed for uncreated conversation", e.Name())
		}
	}
}
