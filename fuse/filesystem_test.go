package fuse

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
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

func TestConversationListNode_ReaddirServerOnly(t *testing.T) {
	// Server returns conversations, no local state
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-aaa"},
		{ConversationID: "server-conv-bbb"},
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

	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
	}

	sort.Strings(names)
	expected := []string{"server-conv-aaa", "server-conv-bbb"}
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

func TestConversationListNode_ReaddirMergedLocalAndServer(t *testing.T) {
	// Server returns some conversations, some overlap with local
	serverConvs := []shelley.Conversation{
		{ConversationID: "server-conv-111"},
		{ConversationID: "server-conv-222"}, // This one is tracked locally
		{ConversationID: "server-conv-333"},
	}
	server := mockConversationsServer(t, serverConvs)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create local conversations - one unrelated, one tracking a server conversation
	localOnly, _ := store.Clone()
	localTracked, _ := store.Clone()
	_ = store.MarkCreated(localTracked, "server-conv-222") // This tracks server-conv-222

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

	// Should have:
	// - localOnly (local ID)
	// - localTracked (local ID, tracks server-conv-222)
	// - server-conv-111 (server only)
	// - server-conv-333 (server only)
	// server-conv-222 should NOT appear separately because it's tracked by localTracked

	sort.Strings(names)
	expected := []string{localOnly, localTracked, "server-conv-111", "server-conv-333"}
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
	_ = store.MarkCreated(localID, "server-conv-precedence")

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

	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
		if !entry.IsDir() {
			t.Errorf("entry %q should be a directory", entry.Name())
		}
	}

	sort.Strings(names)
	expected := []string{localID, "mounted-server-conv-1", "mounted-server-conv-2"}
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
