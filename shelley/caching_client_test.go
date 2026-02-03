package shelley

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestCachingClient_GetConversation_CachesResult verifies that repeated calls
// to GetConversation return cached results without hitting the backend.
func TestCachingClient_GetConversation_CachesResult(t *testing.T) {
	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		if r.URL.Path == "/api/conversation/conv-123" {
			w.Write([]byte(`{"messages":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	// First call should hit the backend
	_, err := caching.GetConversation("conv-123")
	if err != nil {
		t.Fatalf("First GetConversation failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("Expected 1 backend call, got %d", callCount)
	}

	// Second call should use cache
	_, err = caching.GetConversation("conv-123")
	if err != nil {
		t.Fatalf("Second GetConversation failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("Expected still 1 backend call after cache hit, got %d", callCount)
	}

	// Third call should also use cache
	_, err = caching.GetConversation("conv-123")
	if err != nil {
		t.Fatalf("Third GetConversation failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("Expected still 1 backend call after second cache hit, got %d", callCount)
	}
}

// TestCachingClient_GetConversation_CacheExpires verifies that cache entries expire.
func TestCachingClient_GetConversation_CacheExpires(t *testing.T) {
	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		if r.URL.Path == "/api/conversation/conv-123" {
			w.Write([]byte(`{"messages":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	// Use very short TTL for testing
	caching := NewCachingClient(client, 50*time.Millisecond)

	// First call should hit the backend
	_, err := caching.GetConversation("conv-123")
	if err != nil {
		t.Fatalf("First GetConversation failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("Expected 1 backend call, got %d", callCount)
	}

	// Wait for cache to expire
	time.Sleep(100 * time.Millisecond)

	// Second call should hit backend again (cache expired)
	_, err = caching.GetConversation("conv-123")
	if err != nil {
		t.Fatalf("Second GetConversation failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 2 {
		t.Fatalf("Expected 2 backend calls after cache expiry, got %d", callCount)
	}
}

// TestCachingClient_GetConversation_DifferentConversations verifies that
// different conversations have separate cache entries.
func TestCachingClient_GetConversation_DifferentConversations(t *testing.T) {
	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		if r.URL.Path == "/api/conversation/conv-1" {
			w.Write([]byte(`{"messages":[{"id":"1"}]}`))
			return
		}
		if r.URL.Path == "/api/conversation/conv-2" {
			w.Write([]byte(`{"messages":[{"id":"2"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	// Fetch conv-1
	_, err := caching.GetConversation("conv-1")
	if err != nil {
		t.Fatalf("GetConversation(conv-1) failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("Expected 1 call, got %d", callCount)
	}

	// Fetch conv-2 (should be a separate backend call)
	_, err = caching.GetConversation("conv-2")
	if err != nil {
		t.Fatalf("GetConversation(conv-2) failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 2 {
		t.Fatalf("Expected 2 calls, got %d", callCount)
	}

	// Fetch conv-1 again (should use cache)
	_, err = caching.GetConversation("conv-1")
	if err != nil {
		t.Fatalf("GetConversation(conv-1) second call failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 2 {
		t.Fatalf("Expected still 2 calls after cache hit, got %d", callCount)
	}

	// Fetch conv-2 again (should use cache)
	_, err = caching.GetConversation("conv-2")
	if err != nil {
		t.Fatalf("GetConversation(conv-2) second call failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 2 {
		t.Fatalf("Expected still 2 calls after second cache hit, got %d", callCount)
	}
}

// TestCachingClient_SendMessage_InvalidatesCache verifies that sending a message
// invalidates the cache for that conversation.
func TestCachingClient_SendMessage_InvalidatesCache(t *testing.T) {
	var getCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/conv-123" && r.Method == "GET" {
			atomic.AddInt32(&getCount, 1)
			w.Write([]byte(`{"messages":[]}`))
			return
		}
		if r.URL.Path == "/api/conversation/conv-123/chat" && r.Method == "POST" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	// First call populates cache
	_, err := caching.GetConversation("conv-123")
	if err != nil {
		t.Fatalf("First GetConversation failed: %v", err)
	}
	if atomic.LoadInt32(&getCount) != 1 {
		t.Fatalf("Expected 1 GET call, got %d", getCount)
	}

	// Second call uses cache
	_, err = caching.GetConversation("conv-123")
	if err != nil {
		t.Fatalf("Second GetConversation failed: %v", err)
	}
	if atomic.LoadInt32(&getCount) != 1 {
		t.Fatalf("Expected still 1 GET call, got %d", getCount)
	}

	// Send message invalidates cache
	err = caching.SendMessage("conv-123", "hello", "")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	// Next GetConversation should hit backend again
	_, err = caching.GetConversation("conv-123")
	if err != nil {
		t.Fatalf("Third GetConversation failed: %v", err)
	}
	if atomic.LoadInt32(&getCount) != 2 {
		t.Fatalf("Expected 2 GET calls after cache invalidation, got %d", getCount)
	}
}

// TestCachingClient_SendMessage_DoesNotInvalidateOtherConversations verifies that
// sending a message only invalidates the cache for that specific conversation.
func TestCachingClient_SendMessage_DoesNotInvalidateOtherConversations(t *testing.T) {
	var conv1GetCount, conv2GetCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/conv-1" && r.Method == "GET" {
			atomic.AddInt32(&conv1GetCount, 1)
			w.Write([]byte(`{"messages":[]}`))
			return
		}
		if r.URL.Path == "/api/conversation/conv-2" && r.Method == "GET" {
			atomic.AddInt32(&conv2GetCount, 1)
			w.Write([]byte(`{"messages":[]}`))
			return
		}
		if r.URL.Path == "/api/conversation/conv-1/chat" && r.Method == "POST" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	// Populate cache for both conversations
	_, _ = caching.GetConversation("conv-1")
	_, _ = caching.GetConversation("conv-2")
	if atomic.LoadInt32(&conv1GetCount) != 1 || atomic.LoadInt32(&conv2GetCount) != 1 {
		t.Fatalf("Expected 1 call each, got conv1=%d conv2=%d", conv1GetCount, conv2GetCount)
	}

	// Send message to conv-1 (should only invalidate conv-1's cache)
	_ = caching.SendMessage("conv-1", "hello", "")

	// conv-2 should still use cache
	_, _ = caching.GetConversation("conv-2")
	if atomic.LoadInt32(&conv2GetCount) != 1 {
		t.Fatalf("Expected conv2 to still have 1 call (cached), got %d", conv2GetCount)
	}

	// conv-1 should hit backend (cache was invalidated)
	_, _ = caching.GetConversation("conv-1")
	if atomic.LoadInt32(&conv1GetCount) != 2 {
		t.Fatalf("Expected conv1 to have 2 calls (invalidated), got %d", conv1GetCount)
	}
}

// TestCachingClient_ListConversations_CachesResult verifies caching of ListConversations.
func TestCachingClient_ListConversations_CachesResult(t *testing.T) {
	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversations" {
			atomic.AddInt32(&callCount, 1)
			data, _ := json.Marshal([]Conversation{{ConversationID: "conv-1"}})
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	// First call hits backend
	_, err := caching.ListConversations()
	if err != nil {
		t.Fatalf("First ListConversations failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("Expected 1 call, got %d", callCount)
	}

	// Second call uses cache
	_, err = caching.ListConversations()
	if err != nil {
		t.Fatalf("Second ListConversations failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("Expected still 1 call, got %d", callCount)
	}
}

// TestCachingClient_StartConversation_InvalidatesListCache verifies that
// starting a conversation invalidates the ListConversations cache.
func TestCachingClient_StartConversation_InvalidatesListCache(t *testing.T) {
	var listCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversations" && r.Method == "GET" {
			atomic.AddInt32(&listCount, 1)
			data, _ := json.Marshal([]Conversation{})
			w.Write(data)
			return
		}
		if r.URL.Path == "/api/conversations/new" && r.Method == "POST" {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"conversation_id":"new-conv"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	// Populate list cache
	_, _ = caching.ListConversations()
	if atomic.LoadInt32(&listCount) != 1 {
		t.Fatalf("Expected 1 list call, got %d", listCount)
	}

	// Second call uses cache
	_, _ = caching.ListConversations()
	if atomic.LoadInt32(&listCount) != 1 {
		t.Fatalf("Expected still 1 list call, got %d", listCount)
	}

	// Start a new conversation (should invalidate list cache)
	_, err := caching.StartConversation("hello", "", "")
	if err != nil {
		t.Fatalf("StartConversation failed: %v", err)
	}

	// List should hit backend again
	_, _ = caching.ListConversations()
	if atomic.LoadInt32(&listCount) != 2 {
		t.Fatalf("Expected 2 list calls after invalidation, got %d", listCount)
	}
}

// TestCachingClient_ListModels_CachesResult verifies caching of ListModels.
func TestCachingClient_ListModels_CachesResult(t *testing.T) {
	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			atomic.AddInt32(&callCount, 1)
			// Return HTML with SHELLEY_INIT containing models
			w.Write([]byte(`<script>window.__SHELLEY_INIT__ = {"models":[{"id":"test-model","ready":true}],"default_model":"test-model"};</script>`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	// First call hits backend
	result, err := caching.ListModels()
	if err != nil {
		t.Fatalf("First ListModels failed: %v", err)
	}
	if len(result.Models) != 1 || result.Models[0].ID != "test-model" {
		t.Fatalf("Unexpected result: %+v", result)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("Expected 1 call, got %d", callCount)
	}

	// Second call uses cache
	_, err = caching.ListModels()
	if err != nil {
		t.Fatalf("Second ListModels failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("Expected still 1 call, got %d", callCount)
	}
}

// TestCachingClient_ZeroTTL_DisablesCaching verifies that TTL of 0 disables caching.
func TestCachingClient_ZeroTTL_DisablesCaching(t *testing.T) {
	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		if r.URL.Path == "/api/conversation/conv-123" {
			w.Write([]byte(`{"messages":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	// Zero TTL should disable caching
	caching := NewCachingClient(client, 0)

	// First call
	_, err := caching.GetConversation("conv-123")
	if err != nil {
		t.Fatalf("First call failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("Expected 1 call, got %d", callCount)
	}

	// Second call should also hit backend (no caching)
	_, err = caching.GetConversation("conv-123")
	if err != nil {
		t.Fatalf("Second call failed: %v", err)
	}
	if atomic.LoadInt32(&callCount) != 2 {
		t.Fatalf("Expected 2 calls (caching disabled), got %d", callCount)
	}
}

// TestCachingClient_InvalidateConversation verifies manual cache invalidation.
func TestCachingClient_InvalidateConversation(t *testing.T) {
	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		if r.URL.Path == "/api/conversation/conv-123" {
			w.Write([]byte(`{"messages":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	// Populate cache
	_, _ = caching.GetConversation("conv-123")
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("Expected 1 call, got %d", callCount)
	}

	// Verify cache is working
	_, _ = caching.GetConversation("conv-123")
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("Expected still 1 call, got %d", callCount)
	}

	// Manually invalidate
	caching.InvalidateConversation("conv-123")

	// Should hit backend again
	_, _ = caching.GetConversation("conv-123")
	if atomic.LoadInt32(&callCount) != 2 {
		t.Fatalf("Expected 2 calls after invalidation, got %d", callCount)
	}
}

// TestCachingClient_InvalidateAll verifies that InvalidateAll clears all caches.
func TestCachingClient_InvalidateAll(t *testing.T) {
	var convCount, listCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/conv-123" {
			atomic.AddInt32(&convCount, 1)
			w.Write([]byte(`{"messages":[]}`))
			return
		}
		if r.URL.Path == "/api/conversations" {
			atomic.AddInt32(&listCount, 1)
			w.Write([]byte(`[]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	// Populate caches
	_, _ = caching.GetConversation("conv-123")
	_, _ = caching.ListConversations()
	if atomic.LoadInt32(&convCount) != 1 || atomic.LoadInt32(&listCount) != 1 {
		t.Fatalf("Expected 1 call each, got conv=%d list=%d", convCount, listCount)
	}

	// Verify caches are working
	_, _ = caching.GetConversation("conv-123")
	_, _ = caching.ListConversations()
	if atomic.LoadInt32(&convCount) != 1 || atomic.LoadInt32(&listCount) != 1 {
		t.Fatalf("Expected still 1 call each, got conv=%d list=%d", convCount, listCount)
	}

	// Invalidate all
	caching.InvalidateAll()

	// Should hit backend again for both
	_, _ = caching.GetConversation("conv-123")
	_, _ = caching.ListConversations()
	if atomic.LoadInt32(&convCount) != 2 || atomic.LoadInt32(&listCount) != 2 {
		t.Fatalf("Expected 2 calls each after invalidation, got conv=%d list=%d", convCount, listCount)
	}
}

// TestCachingClient_ConcurrentAccess verifies thread-safety of the caching client.
func TestCachingClient_ConcurrentAccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/conv-123" {
			w.Write([]byte(`{"messages":[]}`))
			return
		}
		if r.URL.Path == "/api/conversation/conv-123/chat" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	// Run many goroutines concurrently reading and writing
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_, _ = caching.GetConversation("conv-123")
				if j%10 == 0 {
					_ = caching.SendMessage("conv-123", "hello", "")
				}
			}
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
	// If we get here without panics/races, the test passes
}
