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
		baseURL: strings.TrimRight(baseURL, "/"),
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
	ConversationID string  `json:"conversation_id"`
	Slug           *string `json:"slug"`
	Model          *string `json:"model"`
	Cwd            *string `json:"cwd"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	Working        bool    `json:"working"`
}

// StreamResponse represents a streaming response
type StreamResponse struct {
	Messages []Message `json:"messages,omitempty"`
}

// Message represents a message in a conversation
type Message struct {
	MessageID      string  `json:"message_id"`
	ConversationID string  `json:"conversation_id"`
	SequenceID     int     `json:"sequence_id"`
	Type           string  `json:"type"`
	LLMData        *string `json:"llm_data,omitempty"`
	UserData       *string `json:"user_data,omitempty"`
	UsageData      *string `json:"usage_data,omitempty"`
	CreatedAt      string  `json:"created_at"`
}

// Model represents an available model
type Model struct {
	ID               string `json:"id"`
	DisplayName      string `json:"display_name,omitempty"`
	Source           string `json:"source,omitempty"`
	Ready            bool   `json:"ready"`
	MaxContextTokens int    `json:"max_context_tokens,omitempty"`
}

// Name returns the user-facing name for this model.
// It returns DisplayName when available, falling back to ID.
func (m Model) Name() string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return m.ID
}

// ModelsResult holds the result of listing models
type ModelsResult struct {
	Models []Model
}

// FindByName looks up a model by display name first, then by ID.
// Returns nil if no model matches.
func (r *ModelsResult) FindByName(name string) *Model {
	for i := range r.Models {
		if r.Models[i].Name() == name {
			return &r.Models[i]
		}
	}
	for i := range r.Models {
		if r.Models[i].ID == name {
			return &r.Models[i]
		}
	}
	return nil
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

// ListModels lists available models by calling GET /api/models.
func (c *Client) ListModels() (ModelsResult, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/models", nil)
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

	var models []Model
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return ModelsResult{}, fmt.Errorf("failed to decode models response: %w", err)
	}

	return ModelsResult{Models: models}, nil
}

// DefaultModel fetches the default model ID from the server's HTML init data.
// This is separate from ListModels because default_model is only available
// in the HTML page's window.__SHELLEY_INIT__, not in the /api/models endpoint.
func (c *Client) DefaultModel() (string, error) {
	req, err := http.NewRequest("GET", c.baseURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-Exedev-Userid", "1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	re := regexp.MustCompile(`window\.__SHELLEY_INIT__\s*=\s*({.*?});`)
	match := re.FindStringSubmatch(string(body))
	if len(match) > 1 {
		var initData map[string]interface{}
		if err := json.Unmarshal([]byte(match[1]), &initData); err == nil {
			return getString(initData, "default_model"), nil
		}
	}

	return "", nil
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

// IsConversationWorking checks if the agent is currently working on a conversation.
func (c *Client) IsConversationWorking(conversationID string) (bool, error) {
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

	for _, conv := range convs {
		if conv.ConversationID == conversationID {
			return conv.Working, nil
		}
	}

	// Not found in active conversations list
	return false, nil
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

