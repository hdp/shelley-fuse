package shelley

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// CachingClient wraps a Client and adds caching for read operations.
// Cache entries are invalidated on writes to the corresponding conversation.
// A cacheTTL of 0 disables caching entirely.
//
// Uses singleflight to coalesce duplicate requests, preventing thundering herd
// on cache miss without holding locks during HTTP calls.
type CachingClient struct {
	client   *Client
	cacheTTL time.Duration

	mu sync.RWMutex

	// Singleflight for coalescing duplicate requests
	sf singleflight.Group

	// Per-conversation cache for GetConversation results
	conversationCache map[string]*cacheEntry

	// Global caches
	conversationsListCache *cacheEntry
	archivedListCache      *cacheEntry
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
// Uses singleflight to coalesce duplicate requests without holding locks during HTTP calls.
// The returned byte slice must not be modified by callers.
func (c *CachingClient) GetConversation(conversationID string) ([]byte, error) {
	// Fast path: check cache with read lock
	if c.cacheTTL > 0 {
		c.mu.RLock()
		entry := c.conversationCache[conversationID]
		c.mu.RUnlock()

		if entry.isValid() {
			// Return cached slice directly — callers must not mutate.
			// Returning the same slice enables downstream caches
			// (e.g. ParsedMessageCache) to use pointer identity for
			// fast cache-hit detection.
			return entry.data, nil
		}
	}

	// Slow path: use singleflight to coalesce duplicate requests
	// This ensures only one HTTP call is made even if multiple goroutines
	// experience a cache miss simultaneously, without holding locks during HTTP.
	result, err, _ := c.sf.Do("conversation:"+conversationID, func() (interface{}, error) {
		data, err := c.client.GetConversation(conversationID)
		if err != nil {
			return nil, err
		}

		if c.cacheTTL > 0 {
			c.mu.Lock()
			c.conversationCache[conversationID] = &cacheEntry{
				data:      data,
				expiresAt: time.Now().Add(c.cacheTTL),
			}
			c.mu.Unlock()
		}

		return data, nil
	})

	if err != nil {
		return nil, err
	}
	return result.([]byte), nil
}

// ListConversations lists all conversations, using cache if available.
// Uses singleflight to coalesce duplicate requests without holding locks during HTTP calls.
// The returned byte slice must not be modified by callers.
func (c *CachingClient) ListConversations() ([]byte, error) {
	// Fast path: check cache with read lock
	if c.cacheTTL > 0 {
		c.mu.RLock()
		entry := c.conversationsListCache
		c.mu.RUnlock()

		if entry.isValid() {
			return entry.data, nil
		}
	}

	// Slow path: use singleflight to coalesce duplicate requests
	// This ensures only one HTTP call is made even if multiple goroutines
	// experience a cache miss simultaneously, without holding locks during HTTP.
	result, err, _ := c.sf.Do("conversations:list", func() (interface{}, error) {
		data, err := c.client.ListConversations()
		if err != nil {
			return nil, err
		}

		if c.cacheTTL > 0 {
			c.mu.Lock()
			c.conversationsListCache = &cacheEntry{
				data:      data,
				expiresAt: time.Now().Add(c.cacheTTL),
			}
			c.mu.Unlock()
		}

		return data, nil
	})

	if err != nil {
		return nil, err
	}
	return result.([]byte), nil
}

// ListArchivedConversations lists all archived conversations, using cache if available.
// Uses singleflight to coalesce duplicate requests without holding locks during HTTP calls.
// The returned byte slice must not be modified by callers.
func (c *CachingClient) ListArchivedConversations() ([]byte, error) {
	// Fast path: check cache with read lock
	if c.cacheTTL > 0 {
		c.mu.RLock()
		entry := c.archivedListCache
		c.mu.RUnlock()

		if entry.isValid() {
			return entry.data, nil
		}
	}

	// Slow path: use singleflight to coalesce duplicate requests
	result, err, _ := c.sf.Do("conversations:archived", func() (interface{}, error) {
		data, err := c.client.ListArchivedConversations()
		if err != nil {
			return nil, err
		}

		if c.cacheTTL > 0 {
			c.mu.Lock()
			c.archivedListCache = &cacheEntry{
				data:      data,
				expiresAt: time.Now().Add(c.cacheTTL),
			}
			c.mu.Unlock()
		}

		return data, nil
	})

	if err != nil {
		return nil, err
	}
	return result.([]byte), nil
}

// ListModels lists available models, using cache if available.
// Uses singleflight to coalesce duplicate requests without holding locks during HTTP calls.
func (c *CachingClient) ListModels() (ModelsResult, error) {
	// Fast path: check cache with read lock
	if c.cacheTTL > 0 {
		c.mu.RLock()
		entry := c.modelsCache
		c.mu.RUnlock()

		if entry.isValid() && entry.result != nil {
			return *entry.result, nil
		}
	}

	// Slow path: use singleflight to coalesce duplicate requests
	// This ensures only one HTTP call is made even if multiple goroutines
	// experience a cache miss simultaneously, without holding locks during HTTP.
	result, err, _ := c.sf.Do("models:list", func() (interface{}, error) {
		modelsResult, err := c.client.ListModels()
		if err != nil {
			return ModelsResult{}, err
		}

		if c.cacheTTL > 0 {
			c.mu.Lock()
			c.modelsCache = &cacheEntry{
				result:    &modelsResult,
				expiresAt: time.Now().Add(c.cacheTTL),
			}
			c.mu.Unlock()
		}

		return modelsResult, nil
	})

	if err != nil {
		return ModelsResult{}, err
	}
	return result.(ModelsResult), nil
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
		c.archivedListCache = nil
		c.modelsCache = nil
		c.mu.Unlock()
	}
}

// ArchiveConversation archives a conversation and invalidates the conversations list cache.
func (c *CachingClient) ArchiveConversation(conversationID string) error {
	err := c.client.ArchiveConversation(conversationID)
	if err != nil {
		return err
	}

	// Invalidate both list caches since conversation moved between lists
	if c.cacheTTL > 0 {
		c.mu.Lock()
		c.conversationsListCache = nil
		c.archivedListCache = nil
		c.mu.Unlock()
	}

	return nil
}

// UnarchiveConversation unarchives a conversation and invalidates the conversations list cache.
func (c *CachingClient) UnarchiveConversation(conversationID string) error {
	err := c.client.UnarchiveConversation(conversationID)
	if err != nil {
		return err
	}

	// Invalidate both list caches since conversation moved between lists
	if c.cacheTTL > 0 {
		c.mu.Lock()
		c.conversationsListCache = nil
		c.archivedListCache = nil
		c.mu.Unlock()
	}

	return nil
}

// IsConversationArchived checks if a conversation is archived.
func (c *CachingClient) IsConversationArchived(conversationID string) (bool, error) {
	// Don't cache this - it's a read that checks both endpoints
	return c.client.IsConversationArchived(conversationID)
}
