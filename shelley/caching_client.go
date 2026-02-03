package shelley

import (
	"sync"
	"time"
)

// CachingClient wraps a Client and adds caching for read operations.
// Cache entries are invalidated on writes to the corresponding conversation.
// A cacheTTL of 0 disables caching entirely.
type CachingClient struct {
	client   *Client
	cacheTTL time.Duration

	mu sync.RWMutex

	// Per-conversation cache for GetConversation results
	conversationCache map[string]*cacheEntry

	// Global caches
	conversationsListCache *cacheEntry
	modelsCache            *cacheEntry
}

// cacheEntry holds cached data with an expiration time.
type cacheEntry struct {
	data      []byte
	result    *ModelsResult // for models cache
	expiresAt time.Time
}

// NewCachingClient creates a new CachingClient wrapping the given client.
// A cacheTTL of 0 disables caching.
func NewCachingClient(client *Client, cacheTTL time.Duration) *CachingClient {
	return &CachingClient{
		client:            client,
		cacheTTL:          cacheTTL,
		conversationCache: make(map[string]*cacheEntry),
	}
}

// isValid returns true if the cache entry exists and hasn't expired.
func (e *cacheEntry) isValid() bool {
	return e != nil && time.Now().Before(e.expiresAt)
}

// GetConversation retrieves a conversation, using cache if available.
func (c *CachingClient) GetConversation(conversationID string) ([]byte, error) {
	// Check cache first (if caching is enabled)
	if c.cacheTTL > 0 {
		c.mu.RLock()
		entry := c.conversationCache[conversationID]
		c.mu.RUnlock()

		if entry.isValid() {
			// Return a copy to prevent mutation
			result := make([]byte, len(entry.data))
			copy(result, entry.data)
			return result, nil
		}
	}

	// Fetch from backend
	data, err := c.client.GetConversation(conversationID)
	if err != nil {
		return nil, err
	}

	// Store in cache (if caching is enabled)
	if c.cacheTTL > 0 {
		c.mu.Lock()
		c.conversationCache[conversationID] = &cacheEntry{
			data:      data,
			expiresAt: time.Now().Add(c.cacheTTL),
		}
		c.mu.Unlock()
	}

	return data, nil
}

// ListConversations lists all conversations, using cache if available.
func (c *CachingClient) ListConversations() ([]byte, error) {
	// Check cache first (if caching is enabled)
	if c.cacheTTL > 0 {
		c.mu.RLock()
		entry := c.conversationsListCache
		c.mu.RUnlock()

		if entry.isValid() {
			// Return a copy to prevent mutation
			result := make([]byte, len(entry.data))
			copy(result, entry.data)
			return result, nil
		}
	}

	// Fetch from backend
	data, err := c.client.ListConversations()
	if err != nil {
		return nil, err
	}

	// Store in cache (if caching is enabled)
	if c.cacheTTL > 0 {
		c.mu.Lock()
		c.conversationsListCache = &cacheEntry{
			data:      data,
			expiresAt: time.Now().Add(c.cacheTTL),
		}
		c.mu.Unlock()
	}

	return data, nil
}

// ListModels lists available models, using cache if available.
func (c *CachingClient) ListModels() (ModelsResult, error) {
	// Check cache first (if caching is enabled)
	if c.cacheTTL > 0 {
		c.mu.RLock()
		entry := c.modelsCache
		c.mu.RUnlock()

		if entry.isValid() && entry.result != nil {
			return *entry.result, nil
		}
	}

	// Fetch from backend
	result, err := c.client.ListModels()
	if err != nil {
		return ModelsResult{}, err
	}

	// Store in cache (if caching is enabled)
	if c.cacheTTL > 0 {
		c.mu.Lock()
		c.modelsCache = &cacheEntry{
			result:    &result,
			expiresAt: time.Now().Add(c.cacheTTL),
		}
		c.mu.Unlock()
	}

	return result, nil
}

// StartConversation starts a new conversation and invalidates the conversations list cache.
func (c *CachingClient) StartConversation(message, model, cwd string) (StartConversationResult, error) {
	result, err := c.client.StartConversation(message, model, cwd)
	if err != nil {
		return result, err
	}

	// Invalidate conversations list cache since a new conversation was created
	if c.cacheTTL > 0 {
		c.mu.Lock()
		c.conversationsListCache = nil
		c.mu.Unlock()
	}

	return result, nil
}

// SendMessage sends a message to an existing conversation and invalidates that conversation's cache.
func (c *CachingClient) SendMessage(conversationID, message, model string) error {
	err := c.client.SendMessage(conversationID, message, model)
	if err != nil {
		return err
	}

	// Invalidate this conversation's cache since it was modified
	if c.cacheTTL > 0 {
		c.mu.Lock()
		delete(c.conversationCache, conversationID)
		c.mu.Unlock()
	}

	return nil
}

// InvalidateConversation manually invalidates the cache for a specific conversation.
// This can be used when external writes are detected.
func (c *CachingClient) InvalidateConversation(conversationID string) {
	if c.cacheTTL > 0 {
		c.mu.Lock()
		delete(c.conversationCache, conversationID)
		c.mu.Unlock()
	}
}

// InvalidateAll clears all caches.
func (c *CachingClient) InvalidateAll() {
	if c.cacheTTL > 0 {
		c.mu.Lock()
		c.conversationCache = make(map[string]*cacheEntry)
		c.conversationsListCache = nil
		c.modelsCache = nil
		c.mu.Unlock()
	}
}
