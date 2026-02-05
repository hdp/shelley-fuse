package shelley

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Client is a Shelley API client
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Shelley API client
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 2 * time.Minute, // Prevent hanging on unresponsive servers
		},
	}
}

// ChatRequest represents a request to start a conversation or send a message
type ChatRequest struct {
	Message string `json:"message"`
	Model   string `json:"model,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
}

// Conversation represents a conversation response
type Conversation struct {
	ConversationID string `json:"conversation_id"`
	Slug          *string `json:"slug"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// StreamResponse represents a streaming response
type StreamResponse struct {
	Messages []Message `json:"messages,omitempty"`
}

// Message represents a message in a conversation
type Message struct {
	MessageID      string  `json:"message_id"`
	ConversationID  string  `json:"conversation_id"`
	SequenceID      int     `json:"sequence_id"`
	Type           string  `json:"type"`
	LLMData        *string `json:"llm_data,omitempty"`
	UserData       *string `json:"user_data,omitempty"`
	UsageData      *string `json:"usage_data,omitempty"`
	CreatedAt      string  `json:"created_at"`
}

// Model represents an available model
type Model struct {
	ID    string `json:"id"`
	Ready bool   `json:"ready"`
}

// ModelsResult holds the result of listing models
type ModelsResult struct {
	Models       []Model
	DefaultModel string
}

// StartConversationResult holds the response from starting a new conversation
type StartConversationResult struct {
	ConversationID string
	Slug           string
}

// StartConversation starts a new conversation
func (c *Client) StartConversation(message, model, cwd string) (StartConversationResult, error) {
	reqBody := ChatRequest{
		Message: message,
	}
	
	if model != "" {
		reqBody.Model = model
	}
	
	if cwd != "" {
		reqBody.Cwd = cwd
	}
	
	body, err := json.Marshal(reqBody)
	if err != nil {
		return StartConversationResult{}, fmt.Errorf("failed to marshal request: %w", err)
	}
	
	req, err := http.NewRequest("POST", c.baseURL+"/api/conversations/new", bytes.NewBuffer(body))
	if err != nil {
		return StartConversationResult{}, fmt.Errorf("failed to create request: %w", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Shelley-Request", "1")
	req.Header.Set("X-Exedev-Userid", "1")
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return StartConversationResult{}, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return StartConversationResult{}, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}
	
	var result struct {
		ConversationID string  `json:"conversation_id"`
		Slug           *string `json:"slug"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return StartConversationResult{}, fmt.Errorf("failed to decode response: %w", err)
	}
	
	res := StartConversationResult{ConversationID: result.ConversationID}
	if result.Slug != nil {
		res.Slug = *result.Slug
	}
	return res, nil
}

// GetConversation retrieves a conversation
func (c *Client) GetConversation(conversationID string) ([]byte, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/conversation/"+conversationID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	
	req.Header.Set("X-Exedev-Userid", "1")
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}
	
	return io.ReadAll(resp.Body)
}

// SendMessage sends a message to an existing conversation
func (c *Client) SendMessage(conversationID, message, model string) error {
	reqBody := ChatRequest{
		Message: message,
	}
	
	// Only set model if provided (non-empty)
	if model != "" {
		reqBody.Model = model
	}
	
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	
	req, err := http.NewRequest("POST", c.baseURL+"/api/conversation/"+conversationID+"/chat", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Shelley-Request", "1")
	req.Header.Set("X-Exedev-Userid", "1")
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}
	
	return nil
}

// ListModels lists available models by parsing the HTML page to extract window.__SHELLEY_INIT__
func (c *Client) ListModels() (ModelsResult, error) {
	req, err := http.NewRequest("GET", c.baseURL, nil)
	if err != nil {
		return ModelsResult{}, fmt.Errorf("failed to create request: %w", err)
	}
	
	req.Header.Set("X-Exedev-Userid", "1")
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ModelsResult{}, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ModelsResult{}, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}
	
	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ModelsResult{}, fmt.Errorf("failed to read response body: %w", err)
	}
	
	// Parse HTML to extract window.__SHELLEY_INIT__
	content := string(body)
	re := regexp.MustCompile(`window\.__SHELLEY_INIT__\s*=\s*({.*?});`)
	match := re.FindStringSubmatch(content)
	if len(match) > 1 {
		// Try to parse the JSON
		var initData map[string]interface{}
		if err := json.Unmarshal([]byte(match[1]), &initData); err == nil {
			var result ModelsResult
			
			// Extract default_model
			result.DefaultModel = getString(initData, "default_model")
			
			// Extract models list
			if models, ok := initData["models"].([]interface{}); ok {
				for _, m := range models {
					if modelMap, ok := m.(map[string]interface{}); ok {
						model := Model{
							ID:    getString(modelMap, "id"),
							Ready: getBool(modelMap, "ready"),
						}
						result.Models = append(result.Models, model)
					}
				}
			}
			return result, nil
		}
	}
	
	// Fallback to a fixed list of models
	return ModelsResult{
		Models: []Model{
			{ID: "predictable", Ready: true},
			{ID: "qwen3-coder-fireworks", Ready: true},
		},
		DefaultModel: "",
	}, nil
}

// ListConversations lists all conversations
func (c *Client) ListConversations() ([]byte, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/conversations", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	
	req.Header.Set("X-Exedev-Userid", "1")
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}
	
	return io.ReadAll(resp.Body)
}

// ListArchivedConversations lists all archived conversations
func (c *Client) ListArchivedConversations() ([]byte, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/conversations/archived", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Exedev-Userid", "1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// If archived endpoint doesn't exist, return empty list
		if resp.StatusCode == http.StatusNotFound {
			return []byte("[]"), nil
		}
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

// ArchiveConversation archives a conversation
func (c *Client) ArchiveConversation(conversationID string) error {
	req, err := http.NewRequest("POST", c.baseURL+"/api/conversation/"+conversationID+"/archive", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Exedev-Userid", "1")
	req.Header.Set("X-Shelley-Request", "1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// UnarchiveConversation unarchives a conversation
func (c *Client) UnarchiveConversation(conversationID string) error {
	req, err := http.NewRequest("POST", c.baseURL+"/api/conversation/"+conversationID+"/unarchive", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Exedev-Userid", "1")
	req.Header.Set("X-Shelley-Request", "1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// IsConversationArchived checks if a conversation is archived
func (c *Client) IsConversationArchived(conversationID string) (bool, error) {
	// Get conversations list first
	req, err := http.NewRequest("GET", c.baseURL+"/api/conversations", nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Exedev-Userid", "1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var convs []Conversation
	if err := json.NewDecoder(resp.Body).Decode(&convs); err != nil {
		return false, fmt.Errorf("failed to decode response: %w", err)
	}

	// If found in non-archived list, it's not archived
	for _, conv := range convs {
		if conv.ConversationID == conversationID {
			return false, nil
		}
	}

	// Check archived list
	req, err = http.NewRequest("GET", c.baseURL+"/api/conversations/archived", nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Exedev-Userid", "1")

	resp, err = c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// If archived endpoint doesn't exist, conversation is not archived
		return false, nil
	}

	if err := json.NewDecoder(resp.Body).Decode(&convs); err != nil {
		return false, fmt.Errorf("failed to decode response: %w", err)
	}

	for _, conv := range convs {
		if conv.ConversationID == conversationID {
			return true, nil
		}
	}

	return false, nil
}

// Helper function to safely get string from map
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Helper function to safely get bool from map
func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}