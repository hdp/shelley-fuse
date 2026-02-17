package shelley

import (
	"testing"
	"time"
)

func TestClientManager_GetClient_NotFound(t *testing.T) {
	cm := NewClientManager(0)
	
	_, err := cm.GetClient("test")
	
	if err == nil {
		t.Errorf("expected error when getting client that doesn't exist")
	}
	if err.Error() != "client for backend \"test\" not found: ensure URL is set first" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestClientManager_EnsureURL_CreatesClient(t *testing.T) {
	cm := NewClientManager(0)
	url := "http://example.com"
	
	client, err := cm.EnsureURL("test", url)
	if err != nil {
		t.Fatalf("EnsureURL failed: %v", err)
	}
	if client == nil {
		t.Fatal("EnsureURL returned nil client")
	}
	
	// Verify it returns the same client on subsequent calls
	client2, err := cm.EnsureURL("test", url)
	if err != nil {
		t.Fatalf("EnsureURL failed second time: %v", err)
	}
	if client != client2 {
		t.Error("Expected same client instance on second call")
	}
}

func TestClientManager_EnsureURL_RecreatesOnURLChange(t *testing.T) {
	cm := NewClientManager(0)
	url1 := "http://example.com"
	url2 := "http://another.example.com"
	
	client1, err := cm.EnsureURL("test", url1)
	if err != nil {
		t.Fatalf("EnsureURL failed: %v", err)
	}
	
	// Change URL - should create new client
	client2, err := cm.EnsureURL("test", url2)
	if err != nil {
		t.Fatalf("EnsureURL failed second time: %v", err)
	}
	if client1 == client2 {
		t.Error("Expected different client instance after URL change")
	}
	
	// Verify it returns the same client for the new URL
	client3, err := cm.EnsureURL("test", url2)
	if err != nil {
		t.Fatalf("EnsureURL failed third time: %v", err)
	}
	if client2 != client3 {
		t.Error("Expected same client instance for unchanged URL")
	}
}

func TestClientManager_EnsureURL_WithCaching(t *testing.T) {
	cm := NewClientManager(3 * time.Second)
	url := "http://example.com"
	
	client, err := cm.EnsureURL("test", url)
	if err != nil {
		t.Fatalf("EnsureURL failed: %v", err)
	}
	
	// Verify client is wrapped with CachingClient
	_, ok := client.(*CachingClient)
	if !ok {
		t.Error("Expected CachingClient when cacheTTL > 0")
	}
}

func TestClientManager_EnsureURL_WithoutCaching(t *testing.T) {
	cm := NewClientManager(0)
	url := "http://example.com"
	
	client, err := cm.EnsureURL("test", url)
	if err != nil {
		t.Fatalf("EnsureURL failed: %v", err)
	}
	
	// Verify client is not wrapped with CachingClient
	_, ok := client.(*CachingClient)
	if ok {
		t.Error("Expected base Client when cacheTTL == 0")
	}
	
	// Verify it's a plain Client
	_, ok = client.(*Client)
	if !ok {
		t.Error("Expected base Client when cacheTTL == 0")
	}
}

func TestClientManager_InvalidateClient(t *testing.T) {
	cm := NewClientManager(0)
	url := "http://example.com"
	
	client1, err := cm.EnsureURL("test", url)
	if err != nil {
		t.Fatalf("EnsureURL failed: %v", err)
	}
	
	// Invalidate the client
	cm.InvalidateClient("test")
	
	// GetClient should not find it now
	_, err = cm.GetClient("test")
	if err == nil {
		t.Error("Expected error after invalidating client")
	}
	
	// EnsureURL should create a new client
	client2, err := cm.EnsureURL("test", url)
	if err != nil {
		t.Fatalf("EnsureURL failed after invalidate: %v", err)
	}
	
	if client1 == client2 {
		t.Error("Expected new client after invalidate")
	}
}

func TestClientManager_GetDefaultClient_NotSet(t *testing.T) {
	cm := NewClientManager(0)
	
	_, err := cm.GetDefaultClient()
	if err == nil {
		t.Error("expected error when no default backend configured")
	}
	if err.Error() != "no default backend configured" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestClientManager_GetDefaultClient_WithDefault(t *testing.T) {
	cm := NewClientManager(0)
	
	// Set default backend name
	cm.SetDefault("main")
	
	// Ensure a client exists for that backend
	url := "http://example.com"
	_, err := cm.EnsureURL("main", url)
	if err != nil {
		t.Fatalf("EnsureURL failed: %v", err)
	}
	
	// Get the default client
	client, err := cm.GetDefaultClient()
	if err != nil {
		t.Fatalf("GetDefaultClient failed: %v", err)
	}
	if client == nil {
		t.Fatal("GetDefaultClient returned nil")
	}
}

func TestClientManager_GetDefaultClient_Invalid(t *testing.T) {
	cm := NewClientManager(0)
	
	// Set default backend name that doesn't exist
	cm.SetDefault("nonexistent")
	
	_, err := cm.GetDefaultClient()
	if err == nil {
		t.Error("expected error when default backend doesn't exist")
	}
}

func TestClientManager_MultipleBackends(t *testing.T) {
	cm := NewClientManager(0)
	
	// Create clients for multiple backends
	client1, err := cm.EnsureURL("backend1", "http://example1.com")
	if err != nil {
		t.Fatalf("EnsureURL failed: %v", err)
	}
	
	client2, err := cm.EnsureURL("backend2", "http://example2.com")
	if err != nil {
		t.Fatalf("EnsureURL failed: %v", err)
	}
	
	if client1 == client2 {
		t.Error("Expected different clients for different backends")
	}
	
	// Verify GetClient returns correct clients
	got1, err := cm.GetClient("backend1")
	if err != nil {
		t.Fatalf("GetClient failed: %v", err)
	}
	if got1 != client1 {
		t.Error("GetClient returned wrong client for backend1")
	}
	
	got2, err := cm.GetClient("backend2")
	if err != nil {
		t.Fatalf("GetClient failed: %v", err)
	}
	if got2 != client2 {
		t.Error("GetClient returned wrong client for backend2")
	}
}

func TestClientManager_ConcurrentAccess(t *testing.T) {
	cm := NewClientManager(0)
	url := "http://example.com"
	
	// Concurrently call EnsureURL multiple times
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := cm.EnsureURL("test", url)
			if err != nil {
				t.Errorf("Concurrent EnsureURL failed: %v", err)
			}
			done <- true
		}()
	}
	
	// Wait for all goroutines to finish
	for i := 0; i < 10; i++ {
		<-done
	}
	
	// All should have succeeded without panics
}
