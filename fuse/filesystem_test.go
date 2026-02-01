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

func TestBasicMount(t *testing.T) {
	mockClient := shelley.NewClient("http://localhost:11002")
	store := testStore(t)
	shelleyFS := NewFS(mockClient, store)

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
	// Server returns empty list - only local conversations should appear
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create some local conversations
	id1, _ := store.Clone()
	id2, _ := store.Clone()

	node := &ConversationListNode{client: client, state: store}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
	}

	sort.Strings(names)
	expected := []string{id1, id2}
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

	node := &ConversationListNode{client: client, state: store}
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
	// - Directories for all local IDs (4 total after adoption)
	// - Symlinks for server IDs (3 total)
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-111"},
		{ConversationID: "server-conv-222"}, // This one is already tracked locally
		{ConversationID: "server-conv-333"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create local conversations - one unrelated, one tracking a server conversation
	localOnly, _ := store.Clone()
	localTracked, _ := store.Clone()
	_ = store.MarkCreated(localTracked, "server-conv-222", "") // This tracks server-conv-222

	// Before Readdir: 2 local conversations
	if len(store.List()) != 2 {
		t.Fatalf("expected 2 conversations before Readdir, got %d", len(store.List()))
	}

	node := &ConversationListNode{client: client, state: store}
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

	// Should have 4 directories:
	// - localOnly (existing local ID, no server ID)
	// - localTracked (existing local ID, tracks server-conv-222)
	// - new local ID for server-conv-111 (adopted)
	// - new local ID for server-conv-333 (adopted)
	if len(dirs) != 4 {
		t.Fatalf("expected 4 directories, got %d: %v", len(dirs), dirs)
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

	// Verify original local IDs are still present as directories
	foundLocalOnly := false
	foundLocalTracked := false
	for _, name := range dirs {
		if name == localOnly {
			foundLocalOnly = true
		}
		if name == localTracked {
			foundLocalTracked = true
		}
	}
	if !foundLocalOnly {
		t.Errorf("localOnly %s not found in directories", localOnly)
	}
	if !foundLocalTracked {
		t.Errorf("localTracked %s not found in directories", localTracked)
	}

	// After Readdir: should have 4 conversations (2 original + 2 adopted)
	localIDs := store.List()
	if len(localIDs) != 4 {
		t.Fatalf("expected 4 conversations in store after Readdir, got %d", len(localIDs))
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
	// Server returns an error - should still show local conversations
	server := mockErrorServer(t)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create local conversations
	id1, _ := store.Clone()
	id2, _ := store.Clone()

	node := &ConversationListNode{client: client, state: store}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
	}

	sort.Strings(names)
	expected := []string{id1, id2}
	sort.Strings(expected)

	if len(names) != len(expected) {
		t.Fatalf("expected %d entries (local only due to server error), got %d: %v", len(expected), len(names), names)
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

	// Create a local-only conversation (no Shelley ID - should always appear)
	localOnly, _ := store.Clone()

	// Adopt conversations that exist on server
	activeLocalID, _ := store.Adopt("conv-active")

	// Adopt a conversation that NO LONGER exists on server (stale)
	staleLocalID, _ := store.Adopt("conv-deleted")

	// Verify all 3 are in the store before Readdir
	if len(store.List()) != 3 {
		t.Fatalf("expected 3 conversations in store, got %d", len(store.List()))
	}

	node := &ConversationListNode{client: client, state: store}
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
	// - localOnly (local ID as directory)
	// - activeLocalID (local ID as directory)
	// - conv-active (symlink to active)
	// - active-slug (symlink to active)
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

	expected := []string{localOnly, activeLocalID, "conv-active", "active-slug"}
	for _, exp := range expected {
		if !namesSet[exp] {
			t.Errorf("expected entry %q not found in Readdir results: %v", exp, names)
		}
	}

	// Verify total count: 4 entries (2 dirs + 2 symlinks for active)
	if len(names) != 4 {
		t.Errorf("expected 4 entries, got %d: %v", len(names), names)
	}
}

func TestConversationListNode_ReaddirShowsStaleWhenServerFails(t *testing.T) {
	// When server is unreachable, we should show ALL local entries
	// including ones with Shelley IDs (since we can't verify them)
	server := mockErrorServer(t)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create a local-only conversation
	localOnly, _ := store.Clone()

	// Adopt a conversation (simulating one that might be stale)
	adoptedLocalID, _ := store.Adopt("conv-possibly-stale")

	node := &ConversationListNode{client: client, state: store}
	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
	}

	// When server fails, should see all entries:
	// - localOnly (directory)
	// - adoptedLocalID (directory)
	// - conv-possibly-stale (symlink)
	namesSet := make(map[string]bool)
	for _, name := range names {
		namesSet[name] = true
	}

	expected := []string{localOnly, adoptedLocalID, "conv-possibly-stale"}
	for _, exp := range expected {
		if !namesSet[exp] {
			t.Errorf("expected entry %q not found when server fails: %v", exp, names)
		}
	}

	if len(names) != 3 {
		t.Errorf("expected 3 entries when server fails, got %d: %v", len(names), names)
	}
}

// Helper to mount a test filesystem and return mount point and cleanup function
func mountTestFSWithServer(t *testing.T, server *httptest.Server, store *state.Store) (string, func()) {
	t.Helper()

	client := shelley.NewClient(server.URL)
	shelleyFS := NewFS(client, store)

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

	// Create a local conversation
	localID, _ := store.Clone()

	shelleyFS := NewFS(client, store)

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

	// Should have 3 directories: 1 original local + 2 adopted server conversations
	if len(dirs) != 3 {
		t.Fatalf("expected 3 directories, got %d: %v", len(dirs), dirs)
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

	// Verify original local ID is present as a directory
	foundLocal := false
	for _, name := range dirs {
		if name == localID {
			foundLocal = true
			break
		}
	}
	if !foundLocal {
		t.Errorf("original local ID %s not found in listing", localID)
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
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			// Simulate the HTML response with embedded model data
			modelsJSON, _ := json.Marshal(models)
			fmt.Fprintf(w, `<html><script>window.__SHELLEY_INIT__ = {"models": %s};</script></html>`, modelsJSON)
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
	shelleyFS := NewFS(client, store)

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
	shelleyFS := NewFS(client, store)

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

func TestModelsDirNode_MountedReadAndAccess(t *testing.T) {
	models := []shelley.Model{
		{ID: "model-ready", Ready: true},
		{ID: "model-not-ready", Ready: false},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store)

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

	// Test reading model-ready/ready
	readyData, err := ioutil.ReadFile(filepath.Join(tmpDir, "models", "model-ready", "ready"))
	if err != nil {
		t.Fatalf("Failed to read model-ready/ready: %v", err)
	}
	if strings.TrimSpace(string(readyData)) != "true" {
		t.Errorf("expected 'true', got %q", strings.TrimSpace(string(readyData)))
	}

	// Test reading model-not-ready/ready
	readyData, err = ioutil.ReadFile(filepath.Join(tmpDir, "models", "model-not-ready", "ready"))
	if err != nil {
		t.Fatalf("Failed to read model-not-ready/ready: %v", err)
	}
	if strings.TrimSpace(string(readyData)) != "false" {
		t.Errorf("expected 'false', got %q", strings.TrimSpace(string(readyData)))
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
	shelleyFS := NewFS(client, store)

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

func TestConversationListNode_ReaddirUpdatesEmptySlugs(t *testing.T) {
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

	// Readdir should update the slug
	node := &ConversationListNode{client: client, state: store}
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

	// Should have 2 symlinks: server ID and slug
	if len(symlinks) != 2 {
		t.Fatalf("Expected 2 symlinks (server ID + slug), got %d: %v", len(symlinks), symlinks)
	}

	// Verify slug symlink is present
	foundSlug := false
	for _, name := range symlinks {
		if name == slug {
			foundSlug = true
			break
		}
	}
	if !foundSlug {
		t.Errorf("Slug symlink %q not found in listing, symlinks: %v", slug, symlinks)
	}

	// Verify the state was updated with the slug
	cs = store.Get(localID)
	if cs.Slug != slug {
		t.Errorf("State slug not updated: got %q, want %q", cs.Slug, slug)
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

	node := &ConversationListNode{client: client, state: store}
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
