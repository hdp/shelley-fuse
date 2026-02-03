package shelley

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
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

// TestCachingClient_Singleflight_CoalescesConcurrentSameConversation verifies that
// singleflight coalesces multiple concurrent requests for the same conversation
// into a single HTTP call.
func TestCachingClient_Singleflight_CoalescesConcurrentSameConversation(t *testing.T) {
	var callCount int32
	var mu sync.Mutex
	var inFlight int32
	var maxInFlight int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversation/conv-123" {
			// Track concurrent requests
			mu.Lock()
			atomic.AddInt32(&callCount, 1)
			current := atomic.AddInt32(&inFlight, 1)
			if current > atomic.LoadInt32(&maxInFlight) {
				atomic.StoreInt32(&maxInFlight, current)
			}
			mu.Unlock()

			// Simulate slow backend
			time.Sleep(50 * time.Millisecond)

			atomic.AddInt32(&inFlight, -1)
			w.Write([]byte(`{"messages":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	// Spawn 10 goroutines all requesting the same conversation simultaneously
	const numGoroutines = 10
	var wg sync.WaitGroup
	results := make([][]byte, numGoroutines)
	errors := make([]error, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i], errors[i] = caching.GetConversation("conv-123")
		}()
	}
	wg.Wait()

	// Verify all goroutines got results without error
	for i := 0; i < numGoroutines; i++ {
		if errors[i] != nil {
			t.Errorf("Goroutine %d got error: %v", i, errors[i])
		}
		if len(results[i]) == 0 {
			t.Errorf("Goroutine %d got empty result", i)
		}
	}

	// Verify only 1 HTTP call was made (not 10)
	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("Expected 1 HTTP call due to singleflight, got %d", count)
	}

	// Verify only 1 request was in flight at a time
	if max := atomic.LoadInt32(&maxInFlight); max != 1 {
		t.Errorf("Expected max 1 concurrent request, got %d", max)
	}
}

// TestCachingClient_Singleflight_DifferentConversationsNotBlocked verifies that
// requests for different conversations can proceed independently without blocking
// each other.
func TestCachingClient_Singleflight_DifferentConversationsNotBlocked(t *testing.T) {
	var convAStarted, convBStarted, convBFinished int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/conversation/conv-A":
			atomic.StoreInt32(&convAStarted, 1)
			// Slow response for conversation A
			time.Sleep(200 * time.Millisecond)
			w.Write([]byte(`{"messages":[{"id":"A"}]}`))
		case "/api/conversation/conv-B":
			atomic.StoreInt32(&convBStarted, 1)
			// Fast response for conversation B
			w.Write([]byte(`{"messages":[{"id":"B"}]}`))
			atomic.StoreInt32(&convBFinished, 1)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	var wg sync.WaitGroup
	var convAResult, convBResult []byte
	var convAErr, convBErr error

	// Start slow request for conversation A
	wg.Add(1)
	go func() {
		defer wg.Done()
		convAResult, convAErr = caching.GetConversation("conv-A")
	}()

	// Wait for A to start
	for atomic.LoadInt32(&convAStarted) == 0 {
		time.Sleep(5 * time.Millisecond)
	}

	// Now request conversation B while A is still in progress
	wg.Add(1)
	go func() {
		defer wg.Done()
		convBResult, convBErr = caching.GetConversation("conv-B")
	}()

	// Wait for B to complete (should be fast)
	for atomic.LoadInt32(&convBFinished) == 0 {
		time.Sleep(5 * time.Millisecond)
	}

	// At this point, B should have finished while A is still in progress
	// (A has 200ms delay, B has none)
	wg.Wait()

	// Verify both completed successfully
	if convAErr != nil {
		t.Errorf("Conversation A failed: %v", convAErr)
	}
	if convBErr != nil {
		t.Errorf("Conversation B failed: %v", convBErr)
	}
	if len(convAResult) == 0 {
		t.Error("Conversation A got empty result")
	}
	if len(convBResult) == 0 {
		t.Error("Conversation B got empty result")
	}

	// Verify B started and finished (proving it wasn't blocked by A)
	if atomic.LoadInt32(&convBStarted) != 1 {
		t.Error("Conversation B never started")
	}
	if atomic.LoadInt32(&convBFinished) != 1 {
		t.Error("Conversation B never finished")
	}
}

// TestCachingClient_Singleflight_ReadDirPlusAndFlushDontBlock simulates the
// original FUSE deadlock scenario with concurrent ListConversations and
// StartConversation calls.
func TestCachingClient_Singleflight_ReadDirPlusAndFlushDontBlock(t *testing.T) {
	var listCallCount, startCallCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/conversations":
			atomic.AddInt32(&listCallCount, 1)
			// Simulate slow ListConversations (like ReadDirPlus)
			time.Sleep(50 * time.Millisecond)
			w.Write([]byte(`[]`))
		case "/api/conversations/new":
			atomic.AddInt32(&startCallCount, 1)
			// StartConversation (like Flush)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"conversation_id":"new-conv","slug":"test"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	const numReaders = 5
	const numWriters = 5

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Track completion
	var readersCompleted, writersCompleted int32

	// Simulate multiple ReadDirPlus operations (calling ListConversations)
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := caching.ListConversations()
			if err != nil {
				t.Errorf("ListConversations failed: %v", err)
			}
			atomic.AddInt32(&readersCompleted, 1)
		}()
	}

	// Simulate multiple Flush operations (calling StartConversation)
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := caching.StartConversation("test message", "model", "/tmp")
			if err != nil {
				t.Errorf("StartConversation failed: %v", err)
			}
			atomic.AddInt32(&writersCompleted, 1)
		}()
	}

	// Use a timeout to detect deadlocks
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All operations completed successfully
	case <-time.After(5 * time.Second):
		t.Fatal("Deadlock detected: operations did not complete within timeout")
	}

	// Verify all operations completed
	if r := atomic.LoadInt32(&readersCompleted); r != numReaders {
		t.Errorf("Expected %d readers completed, got %d", numReaders, r)
	}
	if w := atomic.LoadInt32(&writersCompleted); w != numWriters {
		t.Errorf("Expected %d writers completed, got %d", numWriters, w)
	}

	// Verify singleflight coalesced the ListConversations calls
	// (should be 1 call, not 5, since they're concurrent on the same key)
	if count := atomic.LoadInt32(&listCallCount); count != 1 {
		t.Errorf("Expected 1 ListConversations call (coalesced), got %d", count)
	}

	// Each StartConversation should make its own call (no caching for writes)
	if count := atomic.LoadInt32(&startCallCount); count != numWriters {
		t.Errorf("Expected %d StartConversation calls, got %d", numWriters, count)
	}
}

// TestCachingClient_Singleflight_ListConversationsCoalesced verifies that
// concurrent ListConversations calls are coalesced into a single HTTP call.
func TestCachingClient_Singleflight_ListConversationsCoalesced(t *testing.T) {
	var callCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversations" {
			atomic.AddInt32(&callCount, 1)
			// Simulate slow backend
			time.Sleep(50 * time.Millisecond)
			w.Write([]byte(`[]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	const numGoroutines = 10
	var wg sync.WaitGroup
	errors := make([]error, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, errors[i] = caching.ListConversations()
		}()
	}
	wg.Wait()

	// Verify all succeeded
	for i, err := range errors {
		if err != nil {
			t.Errorf("Goroutine %d failed: %v", i, err)
		}
	}

	// Verify only 1 HTTP call was made
	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("Expected 1 HTTP call due to singleflight, got %d", count)
	}
}

// TestCachingClient_Singleflight_ListModelsCoalesced verifies that
// concurrent ListModels calls are coalesced into a single HTTP call.
func TestCachingClient_Singleflight_ListModelsCoalesced(t *testing.T) {
	var callCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			atomic.AddInt32(&callCount, 1)
			// Simulate slow backend
			time.Sleep(50 * time.Millisecond)
			// Return HTML with embedded JSON (like real Shelley server)
			w.Write([]byte(`<html><script>window.__SHELLEY_INIT__ = {"models":[{"id":"test","ready":true}],"default_model":"test"};</script></html>`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	caching := NewCachingClient(client, 5*time.Second)

	const numGoroutines = 10
	var wg sync.WaitGroup
	errors := make([]error, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, errors[i] = caching.ListModels()
		}()
	}
	wg.Wait()

	// Verify all succeeded
	for i, err := range errors {
		if err != nil {
			t.Errorf("Goroutine %d failed: %v", i, err)
		}
	}

	// Verify only 1 HTTP call was made
	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("Expected 1 HTTP call due to singleflight, got %d", count)
	}
}
