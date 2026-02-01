package shelley

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStartConversation(t *testing.T) {
	// Create a test server that captures requests
	var capturedRequest *http.Request
	var capturedBody []byte
	
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRequest = r
		capturedBody, _ = io.ReadAll(r.Body)
		
		// Return a mock response with both conversation_id and slug
		slug := "test-slug"
		response := map[string]interface{}{"conversation_id": "test-conversation-id", "slug": slug}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	
	// Create client with test server URL
	client := NewClient(server.URL)
	
	// Test starting a conversation
	result, err := client.StartConversation("Hello, world!", "test-model", "/test/cwd")
	if err != nil {
		t.Fatalf("StartConversation failed: %v", err)
	}
	
	if result.ConversationID != "test-conversation-id" {
		t.Errorf("Expected conversation ID 'test-conversation-id', got '%s'", result.ConversationID)
	}
	if result.Slug != "test-slug" {
		t.Errorf("Expected slug 'test-slug', got '%s'", result.Slug)
	}
	
	// Verify the request
	if capturedRequest.Method != "POST" {
		t.Errorf("Expected POST request, got %s", capturedRequest.Method)
	}
	
	if capturedRequest.URL.Path != "/api/conversations/new" {
		t.Errorf("Expected path '/api/conversations/new', got '%s'", capturedRequest.URL.Path)
	}
	
	// Check headers
	if capturedRequest.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Expected Content-Type header 'application/json', got '%s'", capturedRequest.Header.Get("Content-Type"))
	}
	
	if capturedRequest.Header.Get("X-Shelley-Request") != "1" {
		t.Errorf("Expected X-Shelley-Request header '1', got '%s'", capturedRequest.Header.Get("X-Shelley-Request"))
	}
	
	if capturedRequest.Header.Get("X-Exedev-Userid") != "1" {
		t.Errorf("Expected X-Exedev-Userid header '1', got '%s'", capturedRequest.Header.Get("X-Exedev-Userid"))
	}
	
	// Check request body
	var reqBody ChatRequest
	if err := json.Unmarshal(capturedBody, &reqBody); err != nil {
		t.Fatalf("Failed to unmarshal request body: %v", err)
	}
	
	if reqBody.Message != "Hello, world!" {
		t.Errorf("Expected message 'Hello, world!', got '%s'", reqBody.Message)
	}
	
	if reqBody.Model != "test-model" {
		t.Errorf("Expected model 'test-model', got '%s'", reqBody.Model)
	}
	
	if reqBody.Cwd != "/test/cwd" {
		t.Errorf("Expected cwd '/test/cwd', got '%s'", reqBody.Cwd)
	}
}

func TestGetConversation(t *testing.T) {
	// Create a test server that returns a mock conversation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		
		if r.URL.Path != "/api/conversation/test-conversation-id" {
			t.Errorf("Expected path '/api/conversation/test-conversation-id', got '%s'", r.URL.Path)
		}
		
		if r.Header.Get("X-Exedev-Userid") != "1" {
			t.Errorf("Expected X-Exedev-Userid header '1', got '%s'", r.Header.Get("X-Exedev-Userid"))
		}
		
		// Return mock conversation data
		mockData := []byte(`{"messages":[{"message_id":"1","type":"user","content":"Hello"}]}`)
		w.Write(mockData)
	}))
	defer server.Close()
	
	// Create client with test server URL
	client := NewClient(server.URL)
	
	// Test getting a conversation
	data, err := client.GetConversation("test-conversation-id")
	if err != nil {
		t.Fatalf("GetConversation failed: %v", err)
	}
	
	expectedData := `{"messages":[{"message_id":"1","type":"user","content":"Hello"}]}`
	if string(data) != expectedData {
		t.Errorf("Expected '%s', got '%s'", expectedData, string(data))
	}
}

func TestSendMessage(t *testing.T) {
	// Create a test server that captures requests
	var capturedRequest *http.Request
	var capturedBody []byte
	
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRequest = r
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	
	// Create client with test server URL
	client := NewClient(server.URL)
	
	// Test sending a message
	err := client.SendMessage("test-conversation-id", "Hello, assistant!", "predictable")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}
	
	// Verify the request
	if capturedRequest.Method != "POST" {
		t.Errorf("Expected POST request, got %s", capturedRequest.Method)
	}
	
	if capturedRequest.URL.Path != "/api/conversation/test-conversation-id/chat" {
		t.Errorf("Expected path '/api/conversation/test-conversation-id/chat', got '%s'", capturedRequest.URL.Path)
	}
	
	// Check headers
	if capturedRequest.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Expected Content-Type header 'application/json', got '%s'", capturedRequest.Header.Get("Content-Type"))
	}
	
	if capturedRequest.Header.Get("X-Shelley-Request") != "1" {
		t.Errorf("Expected X-Shelley-Request header '1', got '%s'", capturedRequest.Header.Get("X-Shelley-Request"))
	}
	
	if capturedRequest.Header.Get("X-Exedev-Userid") != "1" {
		t.Errorf("Expected X-Exedev-Userid header '1', got '%s'", capturedRequest.Header.Get("X-Exedev-Userid"))
	}
	
	// Check request body
	var reqBody ChatRequest
	if err := json.Unmarshal(capturedBody, &reqBody); err != nil {
		t.Fatalf("Failed to unmarshal request body: %v", err)
	}
	
	if reqBody.Message != "Hello, assistant!" {
		t.Errorf("Expected message 'Hello, assistant!', got '%s'", reqBody.Message)
	}
	
	if reqBody.Model != "predictable" {
		t.Errorf("Expected model 'predictable', got '%s'", reqBody.Model)
	}
}

func TestSendMessageStatusCreated(t *testing.T) {
	// Test that SendMessage also accepts HTTP 201 Created (like StartConversation)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"status": "created"}`))
	}))
	defer server.Close()
	
	client := NewClient(server.URL)
	
	err := client.SendMessage("test-conversation-id", "Hello, assistant!", "predictable")
	if err != nil {
		t.Fatalf("SendMessage with StatusCreated failed: %v", err)
	}
}

func TestListConversations(t *testing.T) {
	// Create a test server that returns mock conversations
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		
		if r.URL.Path != "/api/conversations" {
			t.Errorf("Expected path '/api/conversations', got '%s'", r.URL.Path)
		}
		
		if r.Header.Get("X-Exedev-Userid") != "1" {
			t.Errorf("Expected X-Exedev-Userid header '1', got '%s'", r.Header.Get("X-Exedev-Userid"))
		}
		
		// Return mock conversations data
		mockData := []byte(`[{"conversation_id":"1","slug":"test"}]`)
		w.Write(mockData)
	}))
	defer server.Close()
	
	// Create client with test server URL
	client := NewClient(server.URL)
	
	// Test listing conversations
	data, err := client.ListConversations()
	if err != nil {
		t.Fatalf("ListConversations failed: %v", err)
	}
	
	expectedData := `[{"conversation_id":"1","slug":"test"}]`
	if string(data) != expectedData {
		t.Errorf("Expected '%s', got '%s'", expectedData, string(data))
	}
}