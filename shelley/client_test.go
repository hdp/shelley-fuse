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
func TestModelName(t *testing.T) {
	tests := []struct {
		name     string
		model    Model
		wantName string
	}{
		{"display name set", Model{ID: "custom-abc123", DisplayName: "my-model"}, "my-model"},
		{"display name empty", Model{ID: "predictable"}, "predictable"},
		{"id and display same", Model{ID: "claude-sonnet", DisplayName: "claude-sonnet"}, "claude-sonnet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.model.Name(); got != tt.wantName {
				t.Errorf("Name() = %q, want %q", got, tt.wantName)
			}
		})
	}
}

func TestModelsResultFindByName(t *testing.T) {
	result := &ModelsResult{
		Models: []Model{
			{ID: "predictable", Ready: true},
			{ID: "custom-f999b9b0", DisplayName: "kimi-2.5-fireworks", Ready: true},
			{ID: "claude-sonnet", DisplayName: "claude-sonnet", Ready: true},
		},
		DefaultModel: "predictable",
	}

	// Find by display name
	m := result.FindByName("kimi-2.5-fireworks")
	if m == nil || m.ID != "custom-f999b9b0" {
		t.Errorf("FindByName(kimi-2.5-fireworks) = %v, want custom-f999b9b0", m)
	}

	// Find built-in model by ID (which is also its display name)
	m = result.FindByName("predictable")
	if m == nil || m.ID != "predictable" {
		t.Errorf("FindByName(predictable) = %v, want predictable", m)
	}

	// Find by internal ID (fallback)
	m = result.FindByName("custom-f999b9b0")
	if m == nil || m.ID != "custom-f999b9b0" {
		t.Errorf("FindByName(custom-f999b9b0) = %v, want custom-f999b9b0", m)
	}

	// Display name takes priority over ID match
	// If a model's display name matches, it should be returned even if another model's ID matches
	m = result.FindByName("claude-sonnet")
	if m == nil || m.ID != "claude-sonnet" {
		t.Errorf("FindByName(claude-sonnet) = %v, want claude-sonnet", m)
	}

	// Not found
	m = result.FindByName("nonexistent")
	if m != nil {
		t.Errorf("FindByName(nonexistent) = %v, want nil", m)
	}
}

func TestModelsResultDefaultModelName(t *testing.T) {
	// Default model is a custom model with display name
	result := &ModelsResult{
		Models: []Model{
			{ID: "predictable", Ready: true},
			{ID: "custom-abc", DisplayName: "my-custom", Ready: true},
		},
		DefaultModel: "custom-abc",
	}
	if got := result.DefaultModelName(); got != "my-custom" {
		t.Errorf("DefaultModelName() = %q, want %q", got, "my-custom")
	}

	// Default model is a built-in (no display name)
	result.DefaultModel = "predictable"
	if got := result.DefaultModelName(); got != "predictable" {
		t.Errorf("DefaultModelName() = %q, want %q", got, "predictable")
	}

	// No default model
	result.DefaultModel = ""
	if got := result.DefaultModelName(); got != "" {
		t.Errorf("DefaultModelName() = %q, want %q", got, "")
	}

	// Default model ID not found in list
	result.DefaultModel = "nonexistent"
	if got := result.DefaultModelName(); got != "" {
		t.Errorf("DefaultModelName() = %q, want %q", got, "")
	}
}

func TestListModelsDisplayName(t *testing.T) {
	// Create a test server that returns model data with display_name
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head>
			<script>window.__SHELLEY_INIT__={"models":[{"id":"predictable","ready":true},{"id":"custom-abc123","display_name":"kimi-2.5-fireworks","ready":true}],"default_model":"custom-abc123"};</script>
		</head></html>`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	result, err := client.ListModels()
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}

	if len(result.Models) != 2 {
		t.Fatalf("Expected 2 models, got %d", len(result.Models))
	}

	// Built-in model: no display name, Name() returns ID
	if result.Models[0].ID != "predictable" {
		t.Errorf("Models[0].ID = %q, want predictable", result.Models[0].ID)
	}
	if result.Models[0].DisplayName != "" {
		t.Errorf("Models[0].DisplayName = %q, want empty", result.Models[0].DisplayName)
	}
	if result.Models[0].Name() != "predictable" {
		t.Errorf("Models[0].Name() = %q, want predictable", result.Models[0].Name())
	}

	// Custom model: has display name
	if result.Models[1].ID != "custom-abc123" {
		t.Errorf("Models[1].ID = %q, want custom-abc123", result.Models[1].ID)
	}
	if result.Models[1].DisplayName != "kimi-2.5-fireworks" {
		t.Errorf("Models[1].DisplayName = %q, want kimi-2.5-fireworks", result.Models[1].DisplayName)
	}
	if result.Models[1].Name() != "kimi-2.5-fireworks" {
		t.Errorf("Models[1].Name() = %q, want kimi-2.5-fireworks", result.Models[1].Name())
	}

	// Default model resolves to display name
	if result.DefaultModel != "custom-abc123" {
		t.Errorf("DefaultModel = %q, want custom-abc123", result.DefaultModel)
	}
	if got := result.DefaultModelName(); got != "kimi-2.5-fireworks" {
		t.Errorf("DefaultModelName() = %q, want kimi-2.5-fireworks", got)
	}
}
