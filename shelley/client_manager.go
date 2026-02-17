package shelley

import (
	"fmt"
	"sync"
	"time"
)

// ClientManager holds multiple ShelleyClient instances, one per backend.
// Clients are lazily created on first access. URL changes are detected and clients
// are recreated when invalidated.
type ClientManager struct {
	mu          sync.RWMutex
	cacheTTL    time.Duration
	backends    map[string]*managedClient
	defaultName string
}

// managedClient holds a ShelleyClient and the URL it was created with.
// Used to detect URL changes for client invalidation.
type managedClient struct {
	client ShelleyClient
	url    string
}

// NewClientManager creates a new ClientManager.
// cacheTTL is the duration to use for caching; 0 disables caching.
func NewClientManager(cacheTTL time.Duration) *ClientManager {
	return &ClientManager{
		cacheTTL: cacheTTL,
		backends: make(map[string]*managedClient),
	}
}

// GetClient returns the ShelleyClient for the given backend name.
// Creates the client on first access if it doesn't exist.
// Returns an error if there's no URL configured for this backend.
func (cm *ClientManager) GetClient(backendName string) (ShelleyClient, error) {
	// Acquire read lock first for fast path
	cm.mu.RLock()
	mc, exists := cm.backends[backendName]
	cm.mu.RUnlock()

	if exists {
		return mc.client, nil
	}

	// Slow path: need to create client
	// Upgrade to write lock
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Check if another goroutine created it while we were waiting for the lock
	if mc, exists := cm.backends[backendName]; exists {
		return mc.client, nil
	}

	// Backend not found in manager - this means no URL has been configured
	// Callers are expected to call EnsureURL first.
	return nil, fmt.Errorf("client for backend %q not found: ensure URL is set first", backendName)
}

// EnsureURL ensures a client exists for the given backend with the specified URL.
// Creates a new client if needed, or recreates it if the URL has changed.
// Returns the client (possibly wrapped with CachingClient if cacheTTL > 0).
func (cm *ClientManager) EnsureURL(backendName, url string) (ShelleyClient, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	mc, exists := cm.backends[backendName]

	// If client exists and URL hasn't changed, return it
	if exists && mc.url == url {
		return mc.client, nil
	}

	// Create new client
	baseClient := NewClient(url)
	var client ShelleyClient
	if cm.cacheTTL > 0 {
		client = NewCachingClient(baseClient, cm.cacheTTL)
	} else {
		client = baseClient
	}

	cm.backends[backendName] = &managedClient{
		client: client,
		url:    url,
	}

	return client, nil
}

// InvalidateClient removes the client for the given backend name.
// The next call to GetClient or EnsureURL will create a new client.
func (cm *ClientManager) InvalidateClient(backendName string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	delete(cm.backends, backendName)
}

// GetDefaultClient returns the client for the default backend.
// Returns an error if there's no default client configured.
func (cm *ClientManager) GetDefaultClient() (ShelleyClient, error) {
	cm.mu.RLock()
	defaultName := cm.defaultName
	cm.mu.RUnlock()

	if defaultName == "" {
		return nil, fmt.Errorf("no default backend configured")
	}

	return cm.GetClient(defaultName)
}

// SetDefault sets the default backend name.
func (cm *ClientManager) SetDefault(name string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.defaultName = name
}
