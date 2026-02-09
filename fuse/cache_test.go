package fuse

import (
	"context"
	"testing"
	"time"

	"shelley-fuse/mockserver"
	"shelley-fuse/shelley"
)

func TestCachingReducesFetches(t *testing.T) {
	hello := "Hello"
	server := mockserver.New(mockserver.WithConversation("server-conv-123", []shelley.Message{
		{MessageID: "m1", ConversationID: "server-conv-123", SequenceID: 1, Type: "user", UserData: &hello},
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
	if server.FetchCount() != 1 {
		t.Errorf("Expected 1 fetch after first Readdir, got %d", server.FetchCount())
	}

	// Second call should use cache (within TTL)
	_, errno = node.Readdir(ctx)
	if errno != 0 {
		t.Fatalf("Second Readdir failed: %v", errno)
	}
	if server.FetchCount() != 1 {
		t.Errorf("Expected still 1 fetch after second Readdir (cached), got %d", server.FetchCount())
	}

	// Third call should also use cache
	_, errno = node.Readdir(ctx)
	if errno != 0 {
		t.Fatalf("Third Readdir failed: %v", errno)
	}
	if server.FetchCount() != 1 {
		t.Errorf("Expected still 1 fetch after third Readdir (cached), got %d", server.FetchCount())
	}
}

// TestCachingInvalidatedByWrite verifies that cache is invalidated when a message is sent.
func TestCachingInvalidatedByWrite(t *testing.T) {
	hello := "Hello"
	server := mockserver.New(mockserver.WithConversation("server-conv-456", []shelley.Message{
		{MessageID: "m1", ConversationID: "server-conv-456", SequenceID: 1, Type: "user", UserData: &hello},
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
	if server.FetchCount() != 1 {
		t.Errorf("Expected 1 fetch after first Readdir, got %d", server.FetchCount())
	}

	// Second fetch should use cache
	_, _ = node.Readdir(ctx)
	if server.FetchCount() != 1 {
		t.Errorf("Expected still 1 fetch after cached Readdir, got %d", server.FetchCount())
	}

	// Send a message - this should invalidate the cache
	err := cachingClient.SendMessage("server-conv-456", "test message", "")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	// Next fetch should hit backend again (cache invalidated)
	_, _ = node.Readdir(ctx)
	if server.FetchCount() != 2 {
		t.Errorf("Expected 2 fetches after cache invalidation, got %d", server.FetchCount())
	}
}

// TestNoCachingWithZeroTTL verifies that caching is disabled when TTL is 0.
func TestNoCachingWithZeroTTL(t *testing.T) {
	hello := "Hello"
	server := mockserver.New(mockserver.WithConversation("server-conv-789", []shelley.Message{
		{MessageID: "m1", ConversationID: "server-conv-789", SequenceID: 1, Type: "user", UserData: &hello},
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
		if server.FetchCount() != expectedCount {
			t.Errorf("Call %d: Expected %d fetches (no caching), got %d", i+1, expectedCount, server.FetchCount())
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

	cache := NewParsedMessageCache()

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

	// Second call with same data should return cached data (same pointers)
	msgs2, toolMap2, err := cache.GetOrParse("conv-123", convData)
	if err != nil {
		t.Fatalf("Second GetOrParse failed: %v", err)
	}
	if &msgs1[0] != &msgs2[0] {
		t.Error("Expected cached messages slice to be returned for same data")
	}
	if toolMap2["tool1"] != "bash" {
		t.Errorf("Expected cached toolMap[tool1]=bash, got %q", toolMap2["tool1"])
	}

	// Call with different data should re-parse (content-addressed)
	newData := []byte(`{"messages":[
		{"message_id":"m1","sequence_id":1,"type":"user","user_data":"{\"Content\":[{\"Type\":0,\"Text\":\"Hello\"}]}"},
		{"message_id":"m2","sequence_id":2,"type":"shelley","llm_data":"{\"Content\":[{\"Type\":5,\"ID\":\"tool1\",\"ToolName\":\"bash\",\"ToolInput\":{}}]}"},
		{"message_id":"m3","sequence_id":3,"type":"user","user_data":"{\"Content\":[{\"Type\":6,\"ToolUseID\":\"tool1\",\"ToolResult\":[{\"Text\":\"output\"}]}"},
		{"message_id":"m4","sequence_id":4,"type":"shelley","llm_data":"{\"Content\":[{\"Type\":2,\"Text\":\"Done\"}]}"}
	]}`)
	msgs3, _, err := cache.GetOrParse("conv-123", newData)
	if err != nil {
		t.Fatalf("Third GetOrParse failed: %v", err)
	}
	if len(msgs3) != 4 {
		t.Errorf("Expected 4 messages after new data, got %d", len(msgs3))
	}
	if &msgs1[0] == &msgs3[0] {
		t.Error("Expected fresh parse for new data, but got same slice")
	}

	// Invalidate and verify next call re-parses even with same data
	cache.Invalidate("conv-123")
	msgs4, _, err := cache.GetOrParse("conv-123", newData)
	if err != nil {
		t.Fatalf("Fourth GetOrParse failed: %v", err)
	}
	if &msgs3[0] == &msgs4[0] {
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

// TestParsedMessageCacheContentAddressed verifies that the cache is keyed by data content.
func TestParsedMessageCacheContentAddressed(t *testing.T) {
	cache := NewParsedMessageCache()

	convData := []byte(`{"messages":[{"message_id":"m1","sequence_id":1,"type":"user","user_data":"Hello"}]}`)

	// First call
	msgs1, _, err := cache.GetOrParse("conv-123", convData)
	if err != nil {
		t.Fatalf("GetOrParse failed: %v", err)
	}

	// Second call with same data should return cached slice
	msgs2, _, err := cache.GetOrParse("conv-123", convData)
	if err != nil {
		t.Fatalf("Second GetOrParse failed: %v", err)
	}
	if &msgs1[0] != &msgs2[0] {
		t.Error("Expected cached result for same data, but got different slice")
	}

	// Third call with different data should re-parse
	newData := []byte(`{"messages":[{"message_id":"m1","sequence_id":1,"type":"user","user_data":"Hello"},{"message_id":"m2","sequence_id":2,"type":"shelley","llm_data":"{}"}]}`)
	msgs3, _, err := cache.GetOrParse("conv-123", newData)
	if err != nil {
		t.Fatalf("Third GetOrParse failed: %v", err)
	}
	if len(msgs3) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(msgs3))
	}
	if &msgs1[0] == &msgs3[0] {
		t.Error("Expected fresh parse for different data, but got same slice")
	}
}

// TestParsedMessageCacheConsistencyAcrossCallers verifies that multiple callers
// sharing a ParsedMessageCache see the same snapshot, preventing the bug where
// messages/ and messages/last/ could return different data.
func TestParsedMessageCacheConsistencyAcrossCallers(t *testing.T) {
	cache := NewParsedMessageCache()

	// Simulate CachingClient returning the same bytes to two callers
	convData := []byte(`{"messages":[
		{"message_id":"m1","sequence_id":1,"type":"user","user_data":"{\"Content\":[{\"Type\":0,\"Text\":\"Hello\"}]}"},
		{"message_id":"m2","sequence_id":2,"type":"shelley","llm_data":"{\"Content\":[{\"Type\":2,\"Text\":\"Hi\"}]}"}
	]}`)

	// Caller 1: MessagesDirNode.Readdir
	msgs1, toolMap1, err := cache.GetOrParse("conv-1", convData)
	if err != nil {
		t.Fatalf("Caller 1 GetOrParse failed: %v", err)
	}

	// Caller 2: QueryResultDirNode.getFilteredMessages (last/)
	msgs2, toolMap2, err := cache.GetOrParse("conv-1", convData)
	if err != nil {
		t.Fatalf("Caller 2 GetOrParse failed: %v", err)
	}

	// Both callers must see the exact same data
	if len(msgs1) != len(msgs2) {
		t.Errorf("Inconsistent message counts: caller1=%d, caller2=%d", len(msgs1), len(msgs2))
	}
	if &msgs1[0] != &msgs2[0] {
		t.Error("Expected same slice from shared cache")
	}
	_ = toolMap1
	_ = toolMap2

	// Now simulate CachingClient returning NEW data (cache expired upstream)
	newData := []byte(`{"messages":[
		{"message_id":"m1","sequence_id":1,"type":"user","user_data":"{\"Content\":[{\"Type\":0,\"Text\":\"Hello\"}]}"},
		{"message_id":"m2","sequence_id":2,"type":"shelley","llm_data":"{\"Content\":[{\"Type\":2,\"Text\":\"Hi\"}]}"},
		{"message_id":"m3","sequence_id":3,"type":"user","user_data":"{\"Content\":[{\"Type\":0,\"Text\":\"More\"}]}"}
	]}`)

	// Both callers get the new data and must see consistent results
	msgs3, _, err := cache.GetOrParse("conv-1", newData)
	if err != nil {
		t.Fatalf("Caller 1 with new data failed: %v", err)
	}
	msgs4, _, err := cache.GetOrParse("conv-1", newData)
	if err != nil {
		t.Fatalf("Caller 2 with new data failed: %v", err)
	}

	if len(msgs3) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(msgs3))
	}
	if len(msgs3) != len(msgs4) {
		t.Errorf("Inconsistent after update: caller1=%d, caller2=%d", len(msgs3), len(msgs4))
	}
	if &msgs3[0] != &msgs4[0] {
		t.Error("Expected same slice from shared cache after update")
	}
}
