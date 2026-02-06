package fuse

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

func testStore(t *testing.T) *state.Store {
	t.Helper()
	s, err := state.NewStore(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func strPtr(s string) *string { return &s }

func TestBasicMount(t *testing.T) {
	mockClient := shelley.NewClient("http://localhost:11002")
	store := testStore(t)
	shelleyFS := NewFS(mockClient, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-basic-test")
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

	entries, err := ioutil.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to read root directory: %v", err)
	}

	expectedEntries := map[string]bool{
		"models":       false,
		"new":          false,
		"conversation": false,
		"README.md":    false,
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
}

// --- Tests for ConversationListNode with server conversations ---

// mockConversationsServer creates a test server that returns mock conversation data
func mockConversationsServer(t *testing.T, conversations []shelley.Conversation) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversations" {
			data, _ := json.Marshal(conversations)
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
}

// mockErrorServer creates a test server that returns errors for the conversations endpoint
func mockErrorServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversations" {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
}

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
func mountTestFSWithServer(t *testing.T, server *httptest.Server, store *state.Store) (string, func()) {
	t.Helper()

	client := shelley.NewClient(server.URL)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Mount failed: %v", err)
	}

	return tmpDir, func() {
		fssrv.Unmount()
		os.RemoveAll(tmpDir)
	}
}

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

// --- Full mount integration test for conversation listing ---

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

// --- Tests for ModelsDirNode ---

// mockModelsServer creates a test server that returns mock model data
func mockModelsServer(t *testing.T, models []shelley.Model) *httptest.Server {
	t.Helper()
	return mockModelsServerWithDefault(t, models, "")
}

// mockModelsServerWithDefault creates a test server that returns mock model data with an optional default model
func mockModelsServerWithDefault(t *testing.T, models []shelley.Model, defaultModel string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			// Simulate the HTML response with embedded model data
			modelsJSON, _ := json.Marshal(models)
			defaultModelJSON := "null"
			if defaultModel != "" {
				defaultModelJSON = fmt.Sprintf("%q", defaultModel)
			}
			fmt.Fprintf(w, `<html><script>window.__SHELLEY_INIT__ = {"models": %s, "default_model": %s};</script></html>`, modelsJSON, defaultModelJSON)
			return
		}
		http.NotFound(w, r)
	}))
}

func TestModelsDirNode_Readdir(t *testing.T) {
	models := []shelley.Model{
		{ID: "model-a", Ready: true},
		{ID: "model-b", Ready: false},
		{ID: "model-c", Ready: true},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	node := &ModelsDirNode{client: client}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
		if entry.Mode != fuse.S_IFDIR {
			t.Errorf("expected directory mode for %q", entry.Name)
		}
	}

	sort.Strings(names)
	expected := []string{"model-a", "model-b", "model-c"}
	sort.Strings(expected)

	if len(names) != len(expected) {
		t.Fatalf("expected %d entries, got %d: %v", len(expected), len(names), names)
	}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("entry %d: expected %q, got %q", i, expected[i], name)
		}
	}
}

func TestModelsDirNode_Lookup(t *testing.T) {
	models := []shelley.Model{
		{ID: "existing-model", Ready: true},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-models-test")
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

	// Lookup existing model
	info, err := os.Stat(filepath.Join(tmpDir, "models", "existing-model"))
	if err != nil {
		t.Fatalf("Lookup for existing model should succeed: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}

	// Lookup nonexistent model
	_, err = os.Stat(filepath.Join(tmpDir, "models", "nonexistent-model"))
	if err == nil {
		t.Error("Lookup for nonexistent model should fail")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected ENOENT, got %v", err)
	}
}

func TestModelNode_Readdir(t *testing.T) {
	model := shelley.Model{ID: "test-model", Ready: true}
	node := &ModelNode{model: model}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var entries []fuse.DirEntry
	for stream.HasNext() {
		entry, _ := stream.Next()
		entries = append(entries, entry)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (id, ready), got %d", len(entries))
	}

	expectedFiles := map[string]bool{"id": false, "ready": false}
	for _, e := range entries {
		if _, ok := expectedFiles[e.Name]; ok {
			expectedFiles[e.Name] = true
			if e.Mode != fuse.S_IFREG {
				t.Errorf("expected file mode for %q", e.Name)
			}
		} else {
			t.Errorf("unexpected entry %q", e.Name)
		}
	}
	for name, found := range expectedFiles {
		if !found {
			t.Errorf("expected entry %q not found", name)
		}
	}
}

func TestModelNode_LookupMounted(t *testing.T) {
	models := []shelley.Model{
		{ID: "my-model-id", Ready: true},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-model-lookup-test")
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

	// Test lookup for "id" via stat
	info, err := os.Stat(filepath.Join(tmpDir, "models", "my-model-id", "id"))
	if err != nil {
		t.Fatalf("Lookup for 'id' failed: %v", err)
	}
	if info.IsDir() {
		t.Error("expected file for 'id', got directory")
	}

	// Test lookup for "ready" via stat
	info, err = os.Stat(filepath.Join(tmpDir, "models", "my-model-id", "ready"))
	if err != nil {
		t.Fatalf("Lookup for 'ready' failed: %v", err)
	}
	if info.IsDir() {
		t.Error("expected file for 'ready', got directory")
	}

	// Test lookup for nonexistent field
	_, err = os.Stat(filepath.Join(tmpDir, "models", "my-model-id", "nonexistent"))
	if err == nil {
		t.Error("Lookup for 'nonexistent' should fail")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected ENOENT for nonexistent field, got %v", err)
	}
}

func TestModelFieldNode_Read(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{"id field", "my-model", "my-model\n"},
		{"ready true", "true", "true\n"},
		{"ready false", "false", "false\n"},
		{"empty value", "", "\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := &ModelFieldNode{value: tc.value}
			dest := make([]byte, 1024)
			result, errno := node.Read(context.Background(), nil, dest, 0)
			if errno != 0 {
				t.Fatalf("Read failed with errno %d", errno)
			}
			data, _ := result.Bytes(nil)
			if string(data) != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, string(data))
			}
		})
	}
}

func TestModelFieldNode_ReadOffset(t *testing.T) {
	node := &ModelFieldNode{value: "hello"}

	// Read from offset 2
	dest := make([]byte, 1024)
	result, errno := node.Read(context.Background(), nil, dest, 2)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ := result.Bytes(nil)
	if string(data) != "llo\n" {
		t.Errorf("expected %q, got %q", "llo\n", string(data))
	}

	// Read from offset beyond content
	result, errno = node.Read(context.Background(), nil, dest, 100)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ = result.Bytes(nil)
	if len(data) != 0 {
		t.Errorf("expected empty result for offset beyond content, got %q", string(data))
	}
}

// --- Tests for ReadmeNode ---

func TestReadmeNode_Read(t *testing.T) {
	node := &ReadmeNode{}
	dest := make([]byte, 8192)
	result, errno := node.Read(context.Background(), nil, dest, 0)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ := result.Bytes(nil)
	if string(data) != readmeContent {
		t.Errorf("README content mismatch: got %d bytes, expected %d bytes", len(data), len(readmeContent))
	}
}

func TestReadmeNode_ReadOffset(t *testing.T) {
	node := &ReadmeNode{}

	// Read from offset 10
	dest := make([]byte, 20)
	result, errno := node.Read(context.Background(), nil, dest, 10)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ := result.Bytes(nil)
	expected := readmeContent[10:30]
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}

	// Read from offset beyond content
	result, errno = node.Read(context.Background(), nil, dest, int64(len(readmeContent)+100))
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ = result.Bytes(nil)
	if len(data) != 0 {
		t.Errorf("expected empty result for offset beyond content, got %q", string(data))
	}
}

func TestReadmeNode_Getattr(t *testing.T) {
	node := &ReadmeNode{}
	var out fuse.AttrOut
	errno := node.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr failed with errno %d", errno)
	}

	// Check mode is read-only (0444)
	expectedMode := uint32(fuse.S_IFREG | 0444)
	if out.Mode != expectedMode {
		t.Errorf("expected mode %o, got %o", expectedMode, out.Mode)
	}

	// Check size matches readmeContent
	if out.Size != uint64(len(readmeContent)) {
		t.Errorf("expected size %d, got %d", len(readmeContent), out.Size)
	}
}

func TestReadmeNode_MountedRead(t *testing.T) {
	mockClient := shelley.NewClient("http://localhost:11002")
	store := testStore(t)
	shelleyFS := NewFS(mockClient, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-readme-test")
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

	// Read README.md content
	readmePath := filepath.Join(tmpDir, "README.md")
	data, err := ioutil.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("Failed to read README.md: %v", err)
	}
	if string(data) != readmeContent {
		t.Errorf("README.md content mismatch: got %d bytes, expected %d bytes", len(data), len(readmeContent))
	}

	// Check file attributes
	info, err := os.Stat(readmePath)
	if err != nil {
		t.Fatalf("Failed to stat README.md: %v", err)
	}
	if info.Size() != int64(len(readmeContent)) {
		t.Errorf("expected size %d, got %d", len(readmeContent), info.Size())
	}
	// Check read-only permission (0444)
	perm := info.Mode().Perm()
	if perm != 0444 {
		t.Errorf("expected permission 0444, got %o", perm)
	}
}

func TestModelsDirNode_MountedReadAndAccess(t *testing.T) {
	models := []shelley.Model{
		{ID: "model-ready", Ready: true},
		{ID: "model-not-ready", Ready: false},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-models-read-test")
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

	// Test reading model-ready/id
	idData, err := ioutil.ReadFile(filepath.Join(tmpDir, "models", "model-ready", "id"))
	if err != nil {
		t.Fatalf("Failed to read model-ready/id: %v", err)
	}
	if strings.TrimSpace(string(idData)) != "model-ready" {
		t.Errorf("expected 'model-ready', got %q", strings.TrimSpace(string(idData)))
	}

	// Test model-ready/ready exists (presence/absence semantics)
	readyPath := filepath.Join(tmpDir, "models", "model-ready", "ready")
	if _, err := os.Stat(readyPath); err != nil {
		t.Errorf("expected model-ready/ready to exist, got error: %v", err)
	}

	// Test model-not-ready/ready does NOT exist (presence/absence semantics)
	notReadyPath := filepath.Join(tmpDir, "models", "model-not-ready", "ready")
	if _, err := os.Stat(notReadyPath); !os.IsNotExist(err) {
		t.Errorf("expected model-not-ready/ready to not exist (ENOENT), got: %v", err)
	}

	// Test listing models directory
	entries, err := ioutil.ReadDir(filepath.Join(tmpDir, "models"))
	if err != nil {
		t.Fatalf("Failed to read models directory: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 models, got %d", len(entries))
	}

	// Test listing model contents
	entries, err = ioutil.ReadDir(filepath.Join(tmpDir, "models", "model-ready"))
	if err != nil {
		t.Fatalf("Failed to read model-ready directory: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 files (id, ready), got %d", len(entries))
	}
}

func TestModelsDirNode_ServerError(t *testing.T) {
	server := mockErrorServer(t)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-models-error-test")
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

	// Reading models directory when server errors should fail
	_, err = ioutil.ReadDir(filepath.Join(tmpDir, "models"))
	if err == nil {
		t.Error("Expected error when reading models directory with server error")
	}
}

func TestModelsDirNode_EmptyModels(t *testing.T) {
	// Server returns empty model list
	server := mockModelsServer(t, []shelley.Model{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	node := &ModelsDirNode{client: client}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	count := 0
	for stream.HasNext() {
		stream.Next()
		count++
	}

	if count != 0 {
		t.Errorf("expected 0 entries for empty model list, got %d", count)
	}
}

// --- Tests for Default Model Symlink ---

func TestModelsDirNode_DefaultSymlink_Readdir(t *testing.T) {
	// When a default model is set, it should appear as a symlink in the directory listing
	models := []shelley.Model{
		{ID: "model-a", Ready: true},
		{ID: "model-b", Ready: false},
	}
	server := mockModelsServerWithDefault(t, models, "model-a")
	defer server.Close()

	client := shelley.NewClient(server.URL)
	node := &ModelsDirNode{client: client}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else if entry.Mode == fuse.S_IFDIR {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 2 directories (model-a, model-b) and 1 symlink (default)
	if len(dirs) != 2 {
		t.Errorf("expected 2 directories, got %d: %v", len(dirs), dirs)
	}
	if len(symlinks) != 1 {
		t.Fatalf("expected 1 symlink, got %d: %v", len(symlinks), symlinks)
	}
	if symlinks[0] != "default" {
		t.Errorf("expected symlink named 'default', got %q", symlinks[0])
	}
}

func TestModelsDirNode_DefaultSymlink_NoDefault_Readdir(t *testing.T) {
	// When no default model is set, the symlink should NOT appear in the listing
	models := []shelley.Model{
		{ID: "model-a", Ready: true},
		{ID: "model-b", Ready: false},
	}
	server := mockModelsServerWithDefault(t, models, "") // No default
	defer server.Close()

	client := shelley.NewClient(server.URL)
	node := &ModelsDirNode{client: client}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var names []string
	hasSymlink := false
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
		if entry.Mode&syscall.S_IFLNK != 0 {
			hasSymlink = true
		}
	}

	// Should have only directories, no symlink
	if hasSymlink {
		t.Error("unexpected symlink in directory listing when no default model is set")
	}
	if len(names) != 2 {
		t.Errorf("expected 2 entries (models only), got %d: %v", len(names), names)
	}
}

func TestModelsDirNode_DefaultSymlink_Lookup(t *testing.T) {
	// Looking up "default" should return a symlink pointing to the default model
	models := []shelley.Model{
		{ID: "claude-3", Ready: true},
		{ID: "gpt-4", Ready: true},
	}
	server := mockModelsServerWithDefault(t, models, "claude-3")
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-default-symlink-test")
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

	// Check that "default" exists and is a symlink
	defaultPath := filepath.Join(tmpDir, "models", "default")
	fi, err := os.Lstat(defaultPath)
	if err != nil {
		t.Fatalf("Failed to lstat default symlink: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected 'default' to be a symlink, got mode %v", fi.Mode())
	}

	// Verify the symlink target
	target, err := os.Readlink(defaultPath)
	if err != nil {
		t.Fatalf("Failed to readlink: %v", err)
	}
	if target != "claude-3" {
		t.Errorf("expected symlink target 'claude-3', got %q", target)
	}
}

func TestModelsDirNode_DefaultSymlink_NoDefault_Lookup(t *testing.T) {
	// Looking up "default" when no default is set should return ENOENT
	models := []shelley.Model{
		{ID: "model-a", Ready: true},
	}
	server := mockModelsServerWithDefault(t, models, "") // No default
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-no-default-test")
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

	// Check that "default" does NOT exist
	defaultPath := filepath.Join(tmpDir, "models", "default")
	_, err = os.Lstat(defaultPath)
	if err == nil {
		t.Error("expected 'default' to not exist when no default model is set")
	} else if !os.IsNotExist(err) {
		t.Errorf("expected ENOENT error, got: %v", err)
	}
}

func TestModelsDirNode_DefaultSymlink_FollowsToModel(t *testing.T) {
	// Following the default symlink should reach the model directory
	models := []shelley.Model{
		{ID: "target-model", Ready: true},
		{ID: "other-model", Ready: false},
	}
	server := mockModelsServerWithDefault(t, models, "target-model")
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-follow-default-test")
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

	// Follow the symlink and read the id file
	idPath := filepath.Join(tmpDir, "models", "default", "id")
	content, err := ioutil.ReadFile(idPath)
	if err != nil {
		t.Fatalf("Failed to read models/default/id: %v", err)
	}
	if strings.TrimSpace(string(content)) != "target-model" {
		t.Errorf("expected id content 'target-model', got %q", strings.TrimSpace(string(content)))
	}

	// Also check the ready file exists (presence/absence semantics)
	readyPath := filepath.Join(tmpDir, "models", "default", "ready")
	if _, err := os.Stat(readyPath); err != nil {
		t.Errorf("expected models/default/ready to exist, got error: %v", err)
	}
}

func TestModelsDirNode_DefaultSymlink_Getattr(t *testing.T) {
	// Verify that the default symlink has correct attributes
	models := []shelley.Model{
		{ID: "test-model", Ready: true},
	}
	server := mockModelsServerWithDefault(t, models, "test-model")
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	startTime := time.Now()
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-default-getattr-test")
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

	defaultPath := filepath.Join(tmpDir, "models", "default")
	fi, err := os.Lstat(defaultPath)
	if err != nil {
		t.Fatalf("Failed to lstat default symlink: %v", err)
	}

	// Verify it's a symlink
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected symlink mode, got %v", fi.Mode())
	}

	// Verify timestamp is reasonable (within a few seconds of startTime)
	mtime := fi.ModTime()
	diff := mtime.Sub(startTime)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("symlink mtime %v differs from startTime %v by %v", mtime, startTime, diff)
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

// --- Tests for timestamp functionality ---

func TestTimestamps_StaticNodesUseStartTime(t *testing.T) {
	// Test that static nodes (models, new, root) use FS start time
	server := mockModelsServer(t, []shelley.Model{{ID: "test-model", Ready: true}})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Get the start time from the FS
	startTime := shelleyFS.StartTime()

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-timestamp-test")
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

	// Test root directory timestamp
	t.Run("RootDirectory", func(t *testing.T) {
		info, err := os.Stat(tmpDir)
		if err != nil {
			t.Fatalf("Failed to stat root: %v", err)
		}
		mtime := info.ModTime()
		// Should be within 1 second of startTime
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Root mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		// Should not be zero (1970)
		if mtime.Unix() == 0 {
			t.Error("Root mtime is zero (1970)")
		}
	})

	// Test models directory timestamp
	t.Run("ModelsDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "models"))
		if err != nil {
			t.Fatalf("Failed to stat models: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Models mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Models mtime is zero (1970)")
		}
	})

	// Test new directory timestamp
	t.Run("NewDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "new"))
		if err != nil {
			t.Fatalf("Failed to stat new: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("New mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("New mtime is zero (1970)")
		}
	})

	// Test model subdirectory timestamp
	t.Run("ModelSubdirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "models", "test-model"))
		if err != nil {
			t.Fatalf("Failed to stat model: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Model mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Model mtime is zero (1970)")
		}
	})

	// Test model file timestamp
	t.Run("ModelFile", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "models", "test-model", "id"))
		if err != nil {
			t.Fatalf("Failed to stat model/id: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Model/id mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Model/id mtime is zero (1970)")
		}
	})

	// Test clone file timestamp
	t.Run("CloneFile", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "new", "clone"))
		if err != nil {
			t.Fatalf("Failed to stat clone: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Clone mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Clone mtime is zero (1970)")
		}
	})

	// Test conversation list directory timestamp
	t.Run("ConversationListDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation"))
		if err != nil {
			t.Fatalf("Failed to stat conversation: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(startTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("Conversation mtime %v differs from startTime %v by %v", mtime, startTime, diff)
		}
		if mtime.Unix() == 0 {
			t.Error("Conversation mtime is zero (1970)")
		}
	})
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

func TestTimestamps_DoNotConstantlyUpdate(t *testing.T) {
	// Test that timestamps don't constantly update to "now"
	server := mockModelsServer(t, []shelley.Model{{ID: "test-model", Ready: true}})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-stable-timestamp-test")
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

	// Stat the models directory twice with a delay
	info1, err := os.Stat(filepath.Join(tmpDir, "models"))
	if err != nil {
		t.Fatalf("Failed to stat models (1): %v", err)
	}
	mtime1 := info1.ModTime()

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	info2, err := os.Stat(filepath.Join(tmpDir, "models"))
	if err != nil {
		t.Fatalf("Failed to stat models (2): %v", err)
	}
	mtime2 := info2.ModTime()

	// Timestamps should be identical (not updating to "now")
	if !mtime1.Equal(mtime2) {
		t.Errorf("Models timestamp changed between stats: %v -> %v", mtime1, mtime2)
	}
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
	modelsInfo, err := os.Stat(filepath.Join(tmpDir, "models"))
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

func TestTimestamps_NeverZero(t *testing.T) {
	// Test that no timestamps are ever zero (1970)
	server := mockModelsServer(t, []shelley.Model{{ID: "test-model", Ready: true}})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Clone a conversation
	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-nonzero-timestamp-test")
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

	// Check various paths - none should have zero timestamp
	// Note: "created" is not checked because it uses presence/absence semantics
	// and only exists when conversation is created on backend
	pathsToCheck := []string{
		tmpDir,                                            // root
		filepath.Join(tmpDir, "models"),                   // models dir
		filepath.Join(tmpDir, "models", "test-model"),     // model dir
		filepath.Join(tmpDir, "models", "test-model", "id"), // model file
		filepath.Join(tmpDir, "new"),                      // new dir
		filepath.Join(tmpDir, "new", "clone"),             // clone file
		filepath.Join(tmpDir, "conversation"),             // conversation list
		filepath.Join(tmpDir, "conversation", convID),     // conversation dir
		filepath.Join(tmpDir, "conversation", convID, "ctl"),
		filepath.Join(tmpDir, "conversation", convID, "send"),
		filepath.Join(tmpDir, "conversation", convID, "fuse_id"),
		// "created" not checked - uses presence/absence semantics
		filepath.Join(tmpDir, "conversation", convID, "messages"),
		filepath.Join(tmpDir, "conversation", convID, "messages", "last"),
		filepath.Join(tmpDir, "conversation", convID, "messages", "since"),
	}

	for _, path := range pathsToCheck {
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("Failed to stat %s: %v", path, err)
			continue
		}
		mtime := info.ModTime()
		if mtime.Unix() == 0 {
			t.Errorf("Path %s has zero mtime (1970)", path)
		}
		// Also check it's a reasonable recent time (within last hour)
		if time.Since(mtime) > time.Hour {
			t.Errorf("Path %s has mtime %v which is more than 1 hour ago", path, mtime)
		}
	}
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

func TestTimestamps_NestedQueryDirsUseConversationTime(t *testing.T) {
	// Test that nested query directories (since/user/) use conversation time
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Wait a bit so we can distinguish times
	time.Sleep(50 * time.Millisecond)

	// Clone a conversation
	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	cs := store.Get(convID)
	convTime := cs.CreatedAt

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-nested-query-timestamp-test")
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

	// Test since/user directory (nested QueryDirNode)
	t.Run("SinceUserDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID, "messages", "since", "user"))
		if err != nil {
			t.Fatalf("Failed to stat since/user: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("since/user mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
	})


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

// TestTimestamps_MessageDirsUseMessageTime verifies that individual message directories
// and their contents (like 0-user/content.md) use the message's CreatedAt timestamp, not the conversation's.
func TestTimestamps_MessageFilesUseMessageTime(t *testing.T) {
	// Create messages with different timestamps
	convID := "test-conv-msg-timestamps"
	msg1Time := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	msg2Time := time.Date(2026, 1, 15, 10, 5, 0, 0, time.UTC) // 5 minutes later
	msg3Time := time.Date(2026, 1, 15, 10, 10, 0, 0, time.UTC) // 10 minutes later

	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("Hello"), CreatedAt: msg1Time.Format(time.RFC3339)},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi there!"), CreatedAt: msg2Time.Format(time.RFC3339)},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr("Thanks"), CreatedAt: msg3Time.Format(time.RFC3339)},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/"+convID {
			data, _ := json.Marshal(struct {
				Messages []shelley.Message `json:"messages"`
			}{Messages: msgs})
			w.Write(data)
			return
		}
		if r.URL.Path == "/api/conversations" {
			data, _ := json.Marshal([]shelley.Conversation{{ConversationID: convID}})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, 0)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-msg-timestamp-test")
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

	// Test message directories and their content.md files
	testCases := []struct {
		name         string
		path         string // relative to messages/
		expectedTime time.Time
	}{
		{"Message1_Dir", "0-user", msg1Time},
		{"Message1_ContentMD", "0-user/content.md", msg1Time},
		{"Message2_Dir", "1-agent", msg2Time},
		{"Message2_ContentMD", "1-agent/content.md", msg2Time},
		{"Message3_Dir", "2-user", msg3Time},
		{"Message3_ContentMD", "2-user/content.md", msg3Time},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tmpDir, "conversation", localID, "messages", tc.path)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("Failed to stat %s: %v", tc.path, err)
			}
			mtime := info.ModTime()
			diff := mtime.Sub(tc.expectedTime)
			if diff < -time.Second || diff > time.Second {
				t.Errorf("Path %s mtime %v differs from expected %v by %v", tc.path, mtime, tc.expectedTime, diff)
			}
		})
	}

	// Also verify that all.json/all.md still use conversation time (not message time)
	t.Run("AllJsonUsesConvTime", func(t *testing.T) {
		cs := store.Get(localID)
		if cs == nil {
			t.Fatal("Conversation not found")
		}
		convTime := cs.CreatedAt

		info, err := os.Stat(filepath.Join(tmpDir, "conversation", localID, "messages", "all.json"))
		if err != nil {
			t.Fatalf("Failed to stat all.json: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("all.json mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		// Verify it's NOT using message time
		if mtime.Equal(msg1Time) || mtime.Equal(msg2Time) || mtime.Equal(msg3Time) {
			t.Errorf("all.json should use conversation time, not message time: mtime=%v", mtime)
		}
	})
}

// --- Model Symlink Tests ---

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

	expectedTarget := "../../models/claude-opus-4"
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

// TestMessagesDirNodeReaddirWithToolCalls verifies that Readdir generates correct
// filenames for tool call and result messages.
func TestMessagesDirNodeReaddirWithToolCalls(t *testing.T) {
	// Create mock server that returns conversation with tool calls
	convID := "test-conv-with-tools"
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content": [{"Type": 5, "ID": "tu_123", "ToolName": "bash"}]}`)},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr(`{"Content": [{"Type": 6, "ToolUseID": "tu_123"}]}`)},
		{MessageID: "m4", ConversationID: convID, SequenceID: 4, Type: "shelley", LLMData: strPtr("Done!")},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Client uses /api/conversation/{id} (singular)
		if r.URL.Path == "/api/conversation/"+convID {
			data, _ := json.Marshal(struct {
				Messages []shelley.Message `json:"messages"`
			}{Messages: msgs})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create and mark conversation as created
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	node := &MessagesDirNode{
		localID:   localID,
		client:    client,
		state:     store,
		startTime: time.Now(),
	}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	// Collect all entries
	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
	}

	// Expected entries:
	// - Static: all.json, all.md, count, last, since
	// - Message directories: 0-user, 1-bash-tool, 2-bash-result, 3-agent (0-indexed)
	expected := []string{
		"all.json", "all.md", "count", "last", "since",
		"0-user",
		"1-bash-tool",
		"2-bash-result",
		"3-agent",
	}

	namesSet := make(map[string]bool)
	for _, name := range names {
		namesSet[name] = true
	}

	for _, exp := range expected {
		if !namesSet[exp] {
			t.Errorf("Expected file %q not found in Readdir results: %v", exp, names)
		}
	}

	// Verify total count
	if len(names) != len(expected) {
		t.Errorf("Expected %d entries, got %d: %v", len(expected), len(names), names)
	}
}

// TestMessagesDirNodeLookupWithToolCalls verifies that Lookup correctly maps
// tool call/result filenames to their messages via a mounted filesystem.
func TestMessagesDirNodeLookupWithToolCalls(t *testing.T) {
	convID := "test-conv-lookup-tools"
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content": [{"Type": 5, "ID": "tu_456", "ToolName": "patch"}]}`)},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr(`{"Content": [{"Type": 6, "ToolUseID": "tu_456"}]}`)},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Client uses /api/conversation/{id} (singular)
		if r.URL.Path == "/api/conversation/"+convID {
			data, _ := json.Marshal(struct {
				Messages []shelley.Message `json:"messages"`
			}{Messages: msgs})
			w.Write(data)
			return
		}
		if r.URL.Path == "/api/conversations" {
			// Return conversation list for adoption
			data, _ := json.Marshal([]shelley.Conversation{{ConversationID: convID}})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-tool-lookup-test")
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

	msgDir := filepath.Join(tmpDir, "conversation", localID, "messages")

	// Test message directories exist (0-indexed)
	testCases := []struct {
		dirname string
		wantOK  bool
	}{
		{"0-user", true},
		{"1-patch-tool", true},
		{"2-patch-result", true},
		// Wrong slug for index 0 (should be user, not agent)
		{"0-agent", false},
		// Wrong slug for index 1 (should be patch-tool, not bash-tool)
		{"1-bash-tool", false},
		// Non-existent index
		{"99-user", false},
	}

	for _, tc := range testCases {
		t.Run(tc.dirname, func(t *testing.T) {
			info, err := os.Stat(filepath.Join(msgDir, tc.dirname))
			gotOK := err == nil
			if gotOK != tc.wantOK {
				if tc.wantOK {
					t.Errorf("Stat(%q): expected success, got error: %v", tc.dirname, err)
				} else {
					t.Errorf("Stat(%q): expected failure, got success", tc.dirname)
				}
			}
			if gotOK && !info.IsDir() {
				t.Errorf("Stat(%q): expected directory, got file", tc.dirname)
			}
		})
	}

	// Test that content.md exists inside message directories
	for _, dirname := range []string{"0-user", "1-patch-tool", "2-patch-result"} {
		t.Run(dirname+"/content.md", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(msgDir, dirname, "content.md"))
			if err != nil {
				t.Errorf("Stat(%q/content.md): expected success, got error: %v", dirname, err)
			}
		})
	}
}

// TestMessagesDirNodeReadToolCallContent verifies that reading a tool call/result
// message directory returns the correct content.
func TestMessagesDirNodeReadToolCallContent(t *testing.T) {
	convID := "test-conv-read-tools"
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 100, Type: "shelley", LLMData: strPtr(`{"Content": [{"Type": 5, "ID": "tu_789", "ToolName": "bash"}]}`)},
		{MessageID: "m2", ConversationID: convID, SequenceID: 101, Type: "user", UserData: strPtr(`{"Content": [{"Type": 6, "ToolUseID": "tu_789"}]}`)},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/"+convID {
			data, _ := json.Marshal(struct {
				Messages []shelley.Message `json:"messages"`
			}{Messages: msgs})
			w.Write(data)
			return
		}
		if r.URL.Path == "/api/conversations" {
			data, _ := json.Marshal([]shelley.Conversation{{ConversationID: convID}})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-tool-read-test")
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

	msgDir := filepath.Join(tmpDir, "conversation", localID, "messages")

	// Verify 99-bash-tool directory exists and has correct field files (0-indexed: seqID 100 → index 99)
	toolDir := filepath.Join(msgDir, "99-bash-tool")
	info, err := os.Stat(toolDir)
	if err != nil {
		t.Fatalf("Failed to stat 99-bash-tool: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("99-bash-tool should be a directory")
	}

	// Check sequence_id field
	seqID, err := ioutil.ReadFile(filepath.Join(toolDir, "sequence_id"))
	if err != nil {
		t.Fatalf("Failed to read sequence_id: %v", err)
	}
	if string(seqID) != "100\n" {
		t.Errorf("Expected sequence_id=100, got %q", string(seqID))
	}

	// Verify 100-bash-result directory exists and has correct field files (0-indexed: seqID 101 → index 100)
	resultDir := filepath.Join(msgDir, "100-bash-result")
	info, err = os.Stat(resultDir)
	if err != nil {
		t.Fatalf("Failed to stat 100-bash-result: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("100-bash-result should be a directory")
	}

	// Check sequence_id field
	seqID, err = ioutil.ReadFile(filepath.Join(resultDir, "sequence_id"))
	if err != nil {
		t.Fatalf("Failed to read sequence_id: %v", err)
	}
	if string(seqID) != "101\n" {
		t.Errorf("Expected sequence_id=101, got %q", string(seqID))
	}
}

// TestCachingReducesFetches verifies that using CachingClient reduces backend fetches
// when reading the same conversation multiple times in quick succession.
func TestCachingReducesFetches(t *testing.T) {
	var fetchCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/server-conv-123" {
			atomic.AddInt32(&fetchCount, 1)
			w.Write([]byte(`{"messages":[{"message_id":"m1","conversation_id":"server-conv-123","sequence_id":1,"type":"user","user_data":"Hello"}]}`))
			return
		}
		if r.URL.Path == "/api/conversations" {
			data, _ := json.Marshal([]shelley.Conversation{{ConversationID: "server-conv-123"}})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	// Use caching client with 5-second TTL
	baseClient := shelley.NewClient(server.URL)
	cachingClient := shelley.NewCachingClient(baseClient, 5*time.Second)
	store := testStore(t)

	// Adopt the server conversation
	localID, _ := store.AdoptWithSlug("server-conv-123", "")

	node := &MessagesDirNode{localID: localID, client: cachingClient, state: store, startTime: time.Now()}

	// First call should fetch from backend
	ctx := context.Background()
	_, errno := node.Readdir(ctx)
	if errno != 0 {
		t.Fatalf("First Readdir failed: %v", errno)
	}
	if atomic.LoadInt32(&fetchCount) != 1 {
		t.Errorf("Expected 1 fetch after first Readdir, got %d", fetchCount)
	}

	// Second call should use cache (within TTL)
	_, errno = node.Readdir(ctx)
	if errno != 0 {
		t.Fatalf("Second Readdir failed: %v", errno)
	}
	if atomic.LoadInt32(&fetchCount) != 1 {
		t.Errorf("Expected still 1 fetch after second Readdir (cached), got %d", fetchCount)
	}

	// Third call should also use cache
	_, errno = node.Readdir(ctx)
	if errno != 0 {
		t.Fatalf("Third Readdir failed: %v", errno)
	}
	if atomic.LoadInt32(&fetchCount) != 1 {
		t.Errorf("Expected still 1 fetch after third Readdir (cached), got %d", fetchCount)
	}
}

// TestCachingInvalidatedByWrite verifies that cache is invalidated when a message is sent.
func TestCachingInvalidatedByWrite(t *testing.T) {
	var fetchCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/server-conv-456" && r.Method == "GET" {
			atomic.AddInt32(&fetchCount, 1)
			w.Write([]byte(`{"messages":[{"message_id":"m1","conversation_id":"server-conv-456","sequence_id":1,"type":"user","user_data":"Hello"}]}`))
			return
		}
		if r.URL.Path == "/api/conversation/server-conv-456/chat" && r.Method == "POST" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/conversations" {
			data, _ := json.Marshal([]shelley.Conversation{{ConversationID: "server-conv-456"}})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	baseClient := shelley.NewClient(server.URL)
	cachingClient := shelley.NewCachingClient(baseClient, 5*time.Second)
	store := testStore(t)

	localID, _ := store.AdoptWithSlug("server-conv-456", "")

	node := &MessagesDirNode{localID: localID, client: cachingClient, state: store, startTime: time.Now()}
	ctx := context.Background()

	// First fetch
	_, _ = node.Readdir(ctx)
	if atomic.LoadInt32(&fetchCount) != 1 {
		t.Errorf("Expected 1 fetch after first Readdir, got %d", fetchCount)
	}

	// Second fetch should use cache
	_, _ = node.Readdir(ctx)
	if atomic.LoadInt32(&fetchCount) != 1 {
		t.Errorf("Expected still 1 fetch after cached Readdir, got %d", fetchCount)
	}

	// Send a message - this should invalidate the cache
	err := cachingClient.SendMessage("server-conv-456", "test message", "")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	// Next fetch should hit backend again (cache invalidated)
	_, _ = node.Readdir(ctx)
	if atomic.LoadInt32(&fetchCount) != 2 {
		t.Errorf("Expected 2 fetches after cache invalidation, got %d", fetchCount)
	}
}

// TestNoCachingWithZeroTTL verifies that caching is disabled when TTL is 0.
func TestNoCachingWithZeroTTL(t *testing.T) {
	var fetchCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/server-conv-789" {
			atomic.AddInt32(&fetchCount, 1)
			w.Write([]byte(`{"messages":[{"message_id":"m1","conversation_id":"server-conv-789","sequence_id":1,"type":"user","user_data":"Hello"}]}`))
			return
		}
		if r.URL.Path == "/api/conversations" {
			data, _ := json.Marshal([]shelley.Conversation{{ConversationID: "server-conv-789"}})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	baseClient := shelley.NewClient(server.URL)
	// Zero TTL = no caching
	cachingClient := shelley.NewCachingClient(baseClient, 0)
	store := testStore(t)

	localID, _ := store.AdoptWithSlug("server-conv-789", "")

	node := &MessagesDirNode{localID: localID, client: cachingClient, state: store, startTime: time.Now()}
	ctx := context.Background()

	// Each call should fetch from backend (no caching)
	for i := 0; i < 3; i++ {
		_, _ = node.Readdir(ctx)
		expectedCount := int32(i + 1)
		if atomic.LoadInt32(&fetchCount) != expectedCount {
			t.Errorf("Call %d: Expected %d fetches (no caching), got %d", i+1, expectedCount, fetchCount)
		}
	}
}

// TestParsedMessageCacheReducesParsing verifies that ParsedMessageCache caches
// parsed messages and toolMap, avoiding repeated parsing on Lookup operations.
func TestParsedMessageCacheReducesParsing(t *testing.T) {
	// Create a conversation with multiple messages including tool calls
	convData := []byte(`{"messages":[
		{"message_id":"m1","sequence_id":1,"type":"user","user_data":"{\"Content\":[{\"Type\":0,\"Text\":\"Hello\"}]}"},
		{"message_id":"m2","sequence_id":2,"type":"shelley","llm_data":"{\"Content\":[{\"Type\":5,\"ID\":\"tool1\",\"ToolName\":\"bash\",\"ToolInput\":{}}]}"},
		{"message_id":"m3","sequence_id":3,"type":"user","user_data":"{\"Content\":[{\"Type\":6,\"ToolUseID\":\"tool1\",\"ToolResult\":[{\"Text\":\"output\"}]}]}"}
	]}`)

	// Test with caching enabled
	cache := NewParsedMessageCache(5 * time.Second)

	// First call should parse
	msgs1, toolMap1, err := cache.GetOrParse("conv-123", convData)
	if err != nil {
		t.Fatalf("GetOrParse failed: %v", err)
	}
	if len(msgs1) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(msgs1))
	}
	if len(toolMap1) != 1 {
		t.Errorf("Expected toolMap with 1 entry, got %d", len(toolMap1))
	}
	if toolMap1["tool1"] != "bash" {
		t.Errorf("Expected toolMap[tool1]=bash, got %q", toolMap1["tool1"])
	}

	// Second call should return cached data (same pointers)
	msgs2, toolMap2, err := cache.GetOrParse("conv-123", convData)
	if err != nil {
		t.Fatalf("Second GetOrParse failed: %v", err)
	}
	// Verify we got the same slice back (cached)
	if &msgs1[0] != &msgs2[0] {
		t.Error("Expected cached messages slice to be returned")
	}
	if toolMap2["tool1"] != "bash" {
		t.Errorf("Expected cached toolMap[tool1]=bash, got %q", toolMap2["tool1"])
	}

	// Invalidate and verify next call re-parses
	cache.Invalidate("conv-123")

	msgs3, _, err := cache.GetOrParse("conv-123", convData)
	if err != nil {
		t.Fatalf("Third GetOrParse failed: %v", err)
	}
	// Should be a new slice after invalidation
	if &msgs1[0] == &msgs3[0] {
		t.Error("Expected fresh parse after invalidation, but got same slice")
	}
}

// TestParsedMessageCacheNilSafe verifies that ParsedMessageCache methods are safe to call on nil.
func TestParsedMessageCacheNilSafe(t *testing.T) {
	var cache *ParsedMessageCache = nil

	convData := []byte(`{"messages":[{"message_id":"m1","sequence_id":1,"type":"user","user_data":"Hello"}]}`)

	// GetOrParse on nil should still work (just parses without caching)
	msgs, toolMap, err := cache.GetOrParse("conv-123", convData)
	if err != nil {
		t.Fatalf("GetOrParse on nil cache failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("Expected 1 message, got %d", len(msgs))
	}
	if toolMap == nil {
		t.Error("Expected non-nil toolMap")
	}

	// Invalidate on nil should not panic
	cache.Invalidate("conv-123") // Should not panic
}

// TestParsedMessageCacheZeroTTL verifies that caching is disabled with zero TTL.
func TestParsedMessageCacheZeroTTL(t *testing.T) {
	cache := NewParsedMessageCache(0)

	convData := []byte(`{"messages":[{"message_id":"m1","sequence_id":1,"type":"user","user_data":"Hello"}]}`)

	// First call
	msgs1, _, err := cache.GetOrParse("conv-123", convData)
	if err != nil {
		t.Fatalf("GetOrParse failed: %v", err)
	}

	// Second call should return a new slice (not cached)
	msgs2, _, err := cache.GetOrParse("conv-123", convData)
	if err != nil {
		t.Fatalf("Second GetOrParse failed: %v", err)
	}

	// With zero TTL, we should get fresh parses each time
	if &msgs1[0] == &msgs2[0] {
		t.Error("Expected fresh parse with zero TTL, but got same slice")
	}
}

// TestMessageDirNodeFields verifies that message directories contain all expected field files.
func TestMessageDirNodeFields(t *testing.T) {
	convID := "test-conv-msg-dir-fields"
	llmData := `{"Content":[{"Type":2,"Text":"Hello from LLM"}]}`
	usageData := `{"input_tokens":100,"output_tokens":50}`
	msgs := []shelley.Message{
		{
			MessageID:      "msg-uuid-123",
			ConversationID: convID,
			SequenceID:     1,
			Type:           "user",
			UserData:       strPtr("Hello"),
			CreatedAt:      "2026-01-15T10:00:00Z",
		},
		{
			MessageID:      "msg-uuid-456",
			ConversationID: convID,
			SequenceID:     2,
			Type:           "shelley",
			LLMData:        strPtr(llmData),
			UsageData:      strPtr(usageData),
			CreatedAt:      "2026-01-15T10:05:00Z",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/"+convID {
			data, _ := json.Marshal(struct {
				Messages []shelley.Message `json:"messages"`
			}{Messages: msgs})
			w.Write(data)
			return
		}
		if r.URL.Path == "/api/conversations" {
			data, _ := json.Marshal([]shelley.Conversation{{ConversationID: convID}})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-msg-dir-fields-test")
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

	msgDir := filepath.Join(tmpDir, "conversation", localID, "messages")

	// Test user message directory (0-user)
	t.Run("UserMessageDir", func(t *testing.T) {
		userDir := filepath.Join(msgDir, "0-user")

		// Verify it's a directory
		info, err := os.Stat(userDir)
		if err != nil {
			t.Fatalf("Failed to stat 0-user: %v", err)
		}
		if !info.IsDir() {
			t.Errorf("0-user should be a directory")
		}

		// Test field files
		tests := []struct {
			field    string
			expected string
		}{
			{"message_id", "msg-uuid-123\n"},
			{"conversation_id", convID + "\n"},
			{"sequence_id", "1\n"},
			{"type", "user\n"},
			{"created_at", "2026-01-15T10:00:00Z\n"},
		}

		for _, tc := range tests {
			data, err := ioutil.ReadFile(filepath.Join(userDir, tc.field))
			if err != nil {
				t.Errorf("Failed to read %s: %v", tc.field, err)
				continue
			}
			if string(data) != tc.expected {
				t.Errorf("%s: expected %q, got %q", tc.field, tc.expected, string(data))
			}
		}

		// Verify content.md exists
		data, err := ioutil.ReadFile(filepath.Join(userDir, "content.md"))
		if err != nil {
			t.Errorf("Failed to read content.md: %v", err)
		}
		if !strings.Contains(string(data), "user") {
			t.Errorf("content.md should contain 'user', got %q", string(data))
		}

		// User message should NOT have llm_data or usage_data
		_, err = os.Stat(filepath.Join(userDir, "llm_data"))
		if !os.IsNotExist(err) {
			t.Errorf("User message should not have llm_data")
		}
		_, err = os.Stat(filepath.Join(userDir, "usage_data"))
		if !os.IsNotExist(err) {
			t.Errorf("User message should not have usage_data")
		}
	})

	// Test agent message directory (1-agent) with llm_data and usage_data
	t.Run("AgentMessageDir", func(t *testing.T) {
		agentDir := filepath.Join(msgDir, "1-agent")

		// Verify field files
		tests := []struct {
			field    string
			expected string
		}{
			{"message_id", "msg-uuid-456\n"},
			{"sequence_id", "2\n"},
			{"type", "shelley\n"},
		}

		for _, tc := range tests {
			data, err := ioutil.ReadFile(filepath.Join(agentDir, tc.field))
			if err != nil {
				t.Errorf("Failed to read %s: %v", tc.field, err)
				continue
			}
			if string(data) != tc.expected {
				t.Errorf("%s: expected %q, got %q", tc.field, tc.expected, string(data))
			}
		}

		// Verify llm_data is a directory (JSON object)
		llmDataPath := filepath.Join(agentDir, "llm_data")
		info, err := os.Stat(llmDataPath)
		if err != nil {
			t.Fatalf("Failed to stat llm_data: %v", err)
		}
		if !info.IsDir() {
			t.Errorf("llm_data should be a directory")
		}

		// Navigate into llm_data/Content/0/Text
		textPath := filepath.Join(llmDataPath, "Content", "0", "Text")
		data, err := ioutil.ReadFile(textPath)
		if err != nil {
			t.Errorf("Failed to read llm_data/Content/0/Text: %v", err)
		} else if strings.TrimSpace(string(data)) != "Hello from LLM" {
			t.Errorf("Expected 'Hello from LLM', got %q", string(data))
		}

		// Verify usage_data is a directory
		usageDataPath := filepath.Join(agentDir, "usage_data")
		info, err = os.Stat(usageDataPath)
		if err != nil {
			t.Fatalf("Failed to stat usage_data: %v", err)
		}
		if !info.IsDir() {
			t.Errorf("usage_data should be a directory")
		}

		// Read usage_data/input_tokens
		data, err = ioutil.ReadFile(filepath.Join(usageDataPath, "input_tokens"))
		if err != nil {
			t.Errorf("Failed to read usage_data/input_tokens: %v", err)
		} else if strings.TrimSpace(string(data)) != "100" {
			t.Errorf("Expected input_tokens=100, got %q", string(data))
		}
	})
}

// TestTimestamps_APIMetadataMapping tests that API metadata (created_at, updated_at)
// is properly mapped to filesystem stat attributes.
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

// TestTimestamps_MessageDirUsesCreatedAt tests that message directories
// use the message's created_at timestamp.
func TestTimestamps_MessageDirUsesCreatedAt(t *testing.T) {
	convID := "conv-msg-time"
	messages := []shelley.Message{
		{
			MessageID:      "msg-1",
			ConversationID: convID,
			SequenceID:     1,
			Type:           "user",
			UserData:       strPtr("Hello"),
			CreatedAt:      "2024-02-20T09:15:30Z",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/"+convID {
			data, _ := json.Marshal(struct {
				Messages []shelley.Message `json:"messages"`
			}{Messages: messages})
			w.Write(data)
			return
		}
		if r.URL.Path == "/api/conversations" {
			data, _ := json.Marshal([]shelley.Conversation{{ConversationID: convID}})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create and mark conversation as created
	localID, _ := store.Clone()
	_ = store.MarkCreated(localID, convID, "test-slug")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-msg-time-test")
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

	// Stat the message directory
	msgDirPath := filepath.Join(tmpDir, "conversation", localID, "messages", "0-user")
	var stat syscall.Stat_t
	if err := syscall.Stat(msgDirPath, &stat); err != nil {
		t.Fatalf("syscall.Stat failed: %v", err)
	}

	expectedTime, _ := time.Parse(time.RFC3339, "2024-02-20T09:15:30Z")
	actualMtime := time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec)
	actualCtime := time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec)

	// Both ctime and mtime should be from message's created_at
	if !actualMtime.Equal(expectedTime) {
		t.Errorf("Message dir mtime = %v, want %v", actualMtime, expectedTime)
	}
	if !actualCtime.Equal(expectedTime) {
		t.Errorf("Message dir ctime = %v, want %v", actualCtime, expectedTime)
	}
}

// TestTimestamps_ConversationUpdatedAtUpdatesOnReadopt tests that when a
// conversation is re-adopted with a newer updated_at, the timestamp is updated.
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

// TestTimestamps_MessagesSubdirUsesConversationMetadata tests that the messages/
// subdirectory uses the conversation's metadata timestamps.
func TestTimestamps_MessagesSubdirUsesConversationMetadata(t *testing.T) {
	convID := "conv-msg-subdir"
	messages := []shelley.Message{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/"+convID {
			data, _ := json.Marshal(struct {
				Messages []shelley.Message `json:"messages"`
			}{Messages: messages})
			w.Write(data)
			return
		}
		if r.URL.Path == "/api/conversations" {
			data, _ := json.Marshal([]shelley.Conversation{{ConversationID: convID}})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create conversation with API metadata
	localID, _ := store.Clone()
	_ = store.MarkCreated(localID, convID, "test-slug")

	// Set API timestamps
	_, _ = store.AdoptWithMetadata(convID, "test-slug", "2024-03-01T10:00:00Z", "2024-03-05T15:00:00Z")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-msg-subdir-test")
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

	// Stat the messages/ directory
	msgsDirPath := filepath.Join(tmpDir, "conversation", localID, "messages")
	var stat syscall.Stat_t
	if err := syscall.Stat(msgsDirPath, &stat); err != nil {
		t.Fatalf("syscall.Stat failed: %v", err)
	}

	expectedCtime, _ := time.Parse(time.RFC3339, "2024-03-01T10:00:00Z")
	expectedMtime, _ := time.Parse(time.RFC3339, "2024-03-05T15:00:00Z")

	actualCtime := time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec)
	actualMtime := time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec)

	if !actualCtime.Equal(expectedCtime) {
		t.Errorf("messages/ ctime = %v, want %v", actualCtime, expectedCtime)
	}
	if !actualMtime.Equal(expectedMtime) {
		t.Errorf("messages/ mtime = %v, want %v", actualMtime, expectedMtime)
	}
}

// TestQueryResultDirNode_LastN verifies that last/{N} returns a directory
// containing symlinks to the last N message directories.
func TestQueryResultDirNode_LastN(t *testing.T) {
	convID := "test-conv-last-n"
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi there!")},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr("How are you?")},
		{MessageID: "m4", ConversationID: convID, SequenceID: 4, Type: "shelley", LLMData: strPtr("I'm great!")},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/"+convID {
			data, _ := json.Marshal(struct {
				Messages []shelley.Message `json:"messages"`
			}{Messages: msgs})
			w.Write(data)
			return
		}
		if r.URL.Path == "/api/conversations" {
			data, _ := json.Marshal([]shelley.Conversation{{ConversationID: convID}})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-last-n-test")
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

	// Test last/2 - should contain symlinks to the last 2 messages
	last2Dir := filepath.Join(tmpDir, "conversation", localID, "messages", "last", "2")
	info, err := os.Stat(last2Dir)
	if err != nil {
		t.Fatalf("Failed to stat last/2: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("last/2 should be a directory")
	}

	entries, err := ioutil.ReadDir(last2Dir)
	if err != nil {
		t.Fatalf("Failed to read last/2: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Expected 2 entries in last/2, got %d", len(entries))
	}

	// Verify the entries are symlinks (0-indexed: seqID 3 → 2-user, seqID 4 → 3-agent)
	expectedNames := []string{"2-user", "3-agent"}
	for i, e := range entries {
		if e.Mode()&os.ModeSymlink == 0 {
			t.Errorf("Expected symlink, got %s with mode %v", e.Name(), e.Mode())
		}
		if e.Name() != expectedNames[i] {
			t.Errorf("Expected name %q, got %q", expectedNames[i], e.Name())
		}
	}

	// Verify symlink targets are correct (../../{message-dir})
	for _, name := range expectedNames {
		target, err := os.Readlink(filepath.Join(last2Dir, name))
		if err != nil {
			t.Errorf("Failed to readlink %s: %v", name, err)
			continue
		}
		expectedTarget := "../../" + name
		if target != expectedTarget {
			t.Errorf("Symlink %s target = %q, want %q", name, target, expectedTarget)
		}
	}

	// Verify we can read through the symlinks
	data, err := ioutil.ReadFile(filepath.Join(last2Dir, "2-user", "type"))
	if err != nil {
		t.Fatalf("Failed to read type through symlink: %v", err)
	}
	if strings.TrimSpace(string(data)) != "user" {
		t.Errorf("Expected type=user, got %q", string(data))
	}

	data, err = ioutil.ReadFile(filepath.Join(last2Dir, "3-agent", "type"))
	if err != nil {
		t.Fatalf("Failed to read type through symlink: %v", err)
	}
	if strings.TrimSpace(string(data)) != "shelley" {
		t.Errorf("Expected type=shelley, got %q", string(data))
	}

	// Test last/3 - should contain 3 messages
	last3Dir := filepath.Join(tmpDir, "conversation", localID, "messages", "last", "3")
	entries, err = ioutil.ReadDir(last3Dir)
	if err != nil {
		t.Fatalf("Failed to read last/3: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("Expected 3 entries in last/3, got %d", len(entries))
	}
}

// TestQueryResultDirNode_SincePersonN verifies that since/{person}/{N} returns
// a directory containing symlinks to messages after the Nth occurrence of that person.
func TestQueryResultDirNode_SincePersonN(t *testing.T) {
	convID := "test-conv-since-n"
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("First")},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr("Response 1")},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr("Second")},
		{MessageID: "m4", ConversationID: convID, SequenceID: 4, Type: "shelley", LLMData: strPtr("Response 2")},
		{MessageID: "m5", ConversationID: convID, SequenceID: 5, Type: "user", UserData: strPtr("Third")},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/"+convID {
			data, _ := json.Marshal(struct {
				Messages []shelley.Message `json:"messages"`
			}{Messages: msgs})
			w.Write(data)
			return
		}
		if r.URL.Path == "/api/conversations" {
			data, _ := json.Marshal([]shelley.Conversation{{ConversationID: convID}})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-since-n-test")
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

	// Test since/user/2 - messages after the 2nd-to-last user message (2-user)
	// Should include: 3-agent, 4-user
	since2Dir := filepath.Join(tmpDir, "conversation", localID, "messages", "since", "user", "2")
	info, err := os.Stat(since2Dir)
	if err != nil {
		t.Fatalf("Failed to stat since/user/2: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("since/user/2 should be a directory")
	}

	entries, err := ioutil.ReadDir(since2Dir)
	if err != nil {
		t.Fatalf("Failed to read since/user/2: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Expected 2 entries in since/user/2, got %d", len(entries))
	}

	expectedNames := []string{"3-agent", "4-user"}
	for i, e := range entries {
		if e.Mode()&os.ModeSymlink == 0 {
			t.Errorf("Expected symlink, got %s with mode %v", e.Name(), e.Mode())
		}
		if e.Name() != expectedNames[i] {
			t.Errorf("Expected name %q, got %q", expectedNames[i], e.Name())
		}
	}

	// Verify symlink targets are correct (../../../{message-dir})
	for _, name := range expectedNames {
		target, err := os.Readlink(filepath.Join(since2Dir, name))
		if err != nil {
			t.Errorf("Failed to readlink %s: %v", name, err)
			continue
		}
		expectedTarget := "../../../" + name
		if target != expectedTarget {
			t.Errorf("Symlink %s target = %q, want %q", name, target, expectedTarget)
		}
	}

	// Test since/user/1 - messages after the last user message (4-user)
	// Should be empty since it's the last message
	since1Dir := filepath.Join(tmpDir, "conversation", localID, "messages", "since", "user", "1")
	entries, err = ioutil.ReadDir(since1Dir)
	if err != nil {
		t.Fatalf("Failed to read since/user/1: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("Expected 0 entries in since/user/1, got %d", len(entries))
	}

	// Verify we can read through symlinks
	data, err := ioutil.ReadFile(filepath.Join(since2Dir, "3-agent", "type"))
	if err != nil {
		t.Fatalf("Failed to read type through symlink: %v", err)
	}
	if strings.TrimSpace(string(data)) != "shelley" {
		t.Errorf("Expected type=shelley, got %q", string(data))
	}
}

// TestConversationAPITimestampFields tests that created_at and updated_at are exposed at the conversation root.
func TestConversationAPITimestampFields(t *testing.T) {
	convID := "test-timestamp-conv-id"
	convSlug := "test-timestamp-slug"
	convCreatedAt := "2024-01-15T10:30:00Z"
	convUpdatedAt := "2024-01-15T11:00:00Z"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversations" {
			slugPtr := &convSlug
			data, _ := json.Marshal([]shelley.Conversation{{
				ConversationID: convID,
				Slug:           slugPtr,
				CreatedAt:      convCreatedAt,
				UpdatedAt:      convUpdatedAt,
			}})
			w.Write(data)
			return
		}
		if r.URL.Path == "/api/conversation/"+convID {
			slugPtr := &convSlug
			data, _ := json.Marshal(shelley.Conversation{
				ConversationID: convID,
				Slug:           slugPtr,
				CreatedAt:      convCreatedAt,
				UpdatedAt:      convUpdatedAt,
			})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversations" {
			w.Write([]byte("[]"))
			return
		}
		http.NotFound(w, r)
	}))
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
