package shelley

// ShelleyClient defines the interface for interacting with the Shelley API.
// Both Client and CachingClient implement this interface.
type ShelleyClient interface {
	// GetConversation retrieves a conversation by its ID.
	GetConversation(conversationID string) ([]byte, error)

	// ListConversations lists all conversations.
	ListConversations() ([]byte, error)

	// ListModels lists available models.
	ListModels() (ModelsResult, error)

	// StartConversation starts a new conversation.
	StartConversation(message, model, cwd string) (StartConversationResult, error)

	// SendMessage sends a message to an existing conversation.
	SendMessage(conversationID, message, model string) error
}

// Verify that Client implements ShelleyClient at compile time.
var _ ShelleyClient = (*Client)(nil)

// Verify that CachingClient implements ShelleyClient at compile time.
var _ ShelleyClient = (*CachingClient)(nil)
