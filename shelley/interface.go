package shelley

// ShelleyClient defines the interface for interacting with the Shelley API.
// Both Client and CachingClient implement this interface.
type ShelleyClient interface {
	// GetConversation retrieves a conversation by its ID.
	GetConversation(conversationID string) ([]byte, error)

	// ListConversations lists all conversations.
	ListConversations() ([]byte, error)

	// ListArchivedConversations lists all archived conversations.
	ListArchivedConversations() ([]byte, error)

	// ListModels lists available models.
	ListModels() (ModelsResult, error)

	// DefaultModel returns the default model ID.
	DefaultModel() (string, error)

	// StartConversation starts a new conversation.
	StartConversation(message, model, cwd string) (StartConversationResult, error)

	// SendMessage sends a message to an existing conversation.
	SendMessage(conversationID, message, model string) error

	// ArchiveConversation archives a conversation.
	ArchiveConversation(conversationID string) error

	// UnarchiveConversation unarchives a conversation.
	UnarchiveConversation(conversationID string) error

	// CancelConversation cancels an in-progress agent loop for a conversation.
	CancelConversation(conversationID string) error

	// DeleteConversation permanently deletes a conversation.
	DeleteConversation(conversationID string) error

	// IsConversationArchived checks if a conversation is archived.
	IsConversationArchived(conversationID string) (bool, error)

	// IsConversationWorking checks if the agent is currently working on a conversation.
	IsConversationWorking(conversationID string) (bool, error)

	// ListSubagents lists child conversations (subagents) for a conversation.
	ListSubagents(conversationID string) ([]byte, error)

	// ContinueConversation creates a new conversation from an existing one with a summary.
	ContinueConversation(sourceConversationID, model, cwd string) (ContinueConversationResult, error)
}

// Verify that Client implements ShelleyClient at compile time.
var _ ShelleyClient = (*Client)(nil)

// Verify that CachingClient implements ShelleyClient at compile time.
var _ ShelleyClient = (*CachingClient)(nil)
