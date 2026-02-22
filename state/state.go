package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ConversationState tracks the local and remote state of a conversation.
type ConversationState struct {
	LocalID               string `json:"local_id"`
	ShelleyConversationID string `json:"shelley_conversation_id,omitempty"`
	Slug                  string `json:"slug,omitempty"`
	Model                 string `json:"model,omitempty"`
	// ModelID is the internal API model ID (e.g. "custom-f999b9b0").
	// When set, this is sent to the API instead of Model (the display name).
	// For built-in models where ID == display name, this may be empty.
	ModelID   string    `json:"model_id,omitempty"`
	Cwd       string    `json:"cwd,omitempty"`
	Created   bool      `json:"created"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	// APICreatedAt is the server's created_at timestamp (RFC3339 string).
	// This is the original creation time from the Shelley API.
	APICreatedAt string `json:"api_created_at,omitempty"`
	// APIUpdatedAt is the server's updated_at timestamp (RFC3339 string).
	// This is the last modification time from the Shelley API.
	APIUpdatedAt string `json:"api_updated_at,omitempty"`
}

// EffectiveModelID returns the model ID to use for API calls.
// Returns ModelID if set (for custom models), otherwise falls back to Model.
func (cs *ConversationState) EffectiveModelID() string {
	if cs.ModelID != "" {
		return cs.ModelID
	}
	return cs.Model
}

// BackendState tracks configuration and conversations for a Shelley backend.
type BackendState struct {
	// URL is the backend server URL (for future use with multi-backend support).
	URL string `json:"url,omitempty"`
	// Conversations maps local IDs to conversation state for this backend.
	Conversations map[string]*ConversationState `json:"conversations"`
}

// mainBackendName is the internal name for the auto-created default backend.
// The name "default" is reserved in the FUSE filesystem and is always a symlink
// pointing to the actual default backend name.
const mainBackendName = "main"

// DefaultBackendName is the exported name of the default backend.
const DefaultBackendName = mainBackendName

// Store manages local conversation state, persisted to a JSON file.
type Store struct {
	Path            string
	Backends        map[string]*BackendState `json:"backends"`
	DefaultBackend  string                  `json:"default_backend,omitempty"`
	mu              sync.RWMutex
}

// NewStore creates a new Store. If path is empty, defaults to ~/.shelley-fuse/state.json.
func NewStore(path string) (*Store, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine home directory: %w", err)
		}
		path = filepath.Join(home, ".shelley-fuse", "state.json")
	}
	s := &Store{
		Path:     path,
		Backends: make(map[string]*BackendState),
	}
	if err := s.Load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

// defaultBackend returns the default backend state, creating it if needed.
func (s *Store) defaultBackend() *BackendState {
	b, ok := s.Backends[mainBackendName]
	if !ok {
		b = &BackendState{
			URL:           "",
			Conversations: make(map[string]*ConversationState),
		}
		s.Backends[mainBackendName] = b
	}
	return b
}

// conversations returns the conversation map for the default backend.
// This is a helper for migration and backward compatibility.
func (s *Store) conversations() map[string]*ConversationState {
	return s.defaultBackend().Conversations
}

// conversationsForBackend returns the conversation map for the named backend.
// For the default backend, creates it if it doesn't exist.
// For other backends, returns nil if the backend doesn't exist.
func (s *Store) conversationsForBackend(backend string) map[string]*ConversationState {
	// Special handling for default backend - auto-create like the old code
	if backend == s.getDefaultBackend() {
		return s.defaultBackend().Conversations
	}
	b := s.Backends[backend]
	if b == nil {
		return nil
	}
	return b.Conversations
}

// V1State represents the old state file format (flat conversation map).
type V1State struct {
	Conversations map[string]*ConversationState `json:"conversations"`
}

// migrateFromV1 migrates data from the V1 format to the new backend format.
func (s *Store) migrateFromV1(v1 *V1State) error {
	// Create the default backend if it doesn't exist
	b := s.defaultBackend()
	// Copy all conversations to the default backend
	for id, cs := range v1.Conversations {
		b.Conversations[id] = cs
	}
	return nil
}

// Clone allocates a new conversation with a short random hex ID and persists.
func (s *Store) Clone() (string, error) {
	return s.CloneForBackend(s.GetDefaultBackend())
}

// CloneForBackend allocates a new conversation on the specified backend.
func (s *Store) CloneForBackend(backend string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	convs := s.conversationsForBackend(backend)
	if convs == nil {
		return "", fmt.Errorf("backend %q not found", backend)
	}

	id, err := s.generateIDForBackend(backend)
	if err != nil {
		return "", err
	}
	convs[id] = &ConversationState{
		LocalID:   id,
		CreatedAt: time.Now(),
	}
	if err := s.saveLocked(); err != nil {
		delete(convs, id)
		return "", err
	}
	return id, nil
}

// Get returns the state for a conversation, or nil if not found.
func (s *Store) Get(id string) *ConversationState {
	return s.GetForBackend(s.GetDefaultBackend(), id)
}

// GetForBackend returns the state for a conversation on the specified backend, or nil if not found.
func (s *Store) GetForBackend(backend, id string) *ConversationState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	convs := s.conversationsForBackend(backend)
	if convs == nil {
		return nil
	}
	return convs[id]
}

// SetModel sets the model display name and internal ID on an unconversed conversation.
// displayName is the user-facing name; internalID is the API model ID.
// Returns an error if the conversation doesn't exist or is already created.
func (s *Store) SetModel(id, displayName, internalID string) error {
	return s.SetModelForBackend(s.GetDefaultBackend(), id, displayName, internalID)
}

// SetModelForBackend sets the model on a conversation for the specified backend.
func (s *Store) SetModelForBackend(backend, id, displayName, internalID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	convs := s.conversationsForBackend(backend)
	if convs == nil {
		return fmt.Errorf("backend %q not found", backend)
	}

	cs, ok := convs[id]
	if !ok {
		return fmt.Errorf("conversation %s not found", id)
	}
	if cs.Created {
		return fmt.Errorf("conversation %s already created, ctl is read-only", id)
	}

	cs.Model = displayName
	cs.ModelID = internalID
	return s.saveLocked()
}

// SetCtl sets a key=value pair on an unconversed conversation.
// Returns an error if the conversation doesn't exist or is already created.
func (s *Store) SetCtl(id, key, value string) error {
	return s.SetCtlForBackend(s.GetDefaultBackend(), id, key, value)
}

// SetCtlForBackend sets a key=value pair on a conversation for the specified backend.
func (s *Store) SetCtlForBackend(backend, id, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	convs := s.conversationsForBackend(backend)
	if convs == nil {
		return fmt.Errorf("backend %q not found", backend)
	}

	cs, ok := convs[id]
	if !ok {
		return fmt.Errorf("conversation %s not found", id)
	}
	if cs.Created {
		return fmt.Errorf("conversation %s already created, ctl is read-only", id)
	}

	switch key {
	case "model":
		// For backwards compatibility, SetCtl("model", v) sets both fields to the same value.
		// Prefer SetModel() for proper display name / internal ID separation.
		cs.Model = value
		cs.ModelID = value
	case "cwd":
		cs.Cwd = value
	default:
		return fmt.Errorf("unknown ctl key: %s", key)
	}

	return s.saveLocked()
}

// MarkCreated marks a conversation as created with its Shelley backend ID and slug.
func (s *Store) MarkCreated(id, shelleyConversationID, slug string) error {
	return s.MarkCreatedForBackend(s.GetDefaultBackend(), id, shelleyConversationID, slug)
}

// MarkCreatedForBackend marks a conversation as created for the specified backend.
func (s *Store) MarkCreatedForBackend(backend, id, shelleyConversationID, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	convs := s.conversationsForBackend(backend)
	if convs == nil {
		return fmt.Errorf("backend %q not found", backend)
	}

	cs, ok := convs[id]
	if !ok {
		return fmt.Errorf("conversation %s not found", id)
	}
	cs.Created = true
	cs.ShelleyConversationID = shelleyConversationID
	cs.Slug = slug
	return s.saveLocked()
}

// List returns all known conversation IDs, sorted.
func (s *Store) List() []string {
	return s.ListForBackend(s.GetDefaultBackend())
}

// ListForBackend returns all known conversation IDs for the specified backend, sorted.
func (s *Store) ListForBackend(backend string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	convs := s.conversationsForBackend(backend)
	if convs == nil {
		return nil
	}

	ids := make([]string, 0, len(convs))
	for id := range convs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// GetByShelleyID returns the local ID for a given Shelley conversation ID, or empty string if not found.
func (s *Store) GetByShelleyID(shelleyID string) string {
	return s.GetByShelleyIDForBackend(s.GetDefaultBackend(), shelleyID)
}

// GetByShelleyIDForBackend returns the local ID for a Shelley conversation ID on the specified backend.
func (s *Store) GetByShelleyIDForBackend(backend, shelleyID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	convs := s.conversationsForBackend(backend)
	if convs == nil {
		return ""
	}

	for _, cs := range convs {
		if cs.ShelleyConversationID == shelleyID {
			return cs.LocalID
		}
	}
	return ""
}

// GetBySlug returns the local ID for a given slug, or empty string if not found.
func (s *Store) GetBySlug(slug string) string {
	return s.GetBySlugForBackend(s.GetDefaultBackend(), slug)
}

// GetBySlugForBackend returns the local ID for a slug on the specified backend.
func (s *Store) GetBySlugForBackend(backend, slug string) string {
	if slug == "" {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	convs := s.conversationsForBackend(backend)
	if convs == nil {
		return ""
	}

	for _, cs := range convs {
		if cs.Slug == slug {
			return cs.LocalID
		}
	}
	return ""
}

// Delete removes an unconversed conversation from state.
// Returns an error if the conversation doesn't exist or is already created.
// This is used for cleaning up abandoned clone operations.
func (s *Store) Delete(id string) error {
	return s.DeleteForBackend(s.GetDefaultBackend(), id)
}

// DeleteForBackend removes an unconversed conversation from the specified backend.
func (s *Store) DeleteForBackend(backend, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	convs := s.conversationsForBackend(backend)
	if convs == nil {
		return fmt.Errorf("backend %q not found", backend)
	}

	cs, ok := convs[id]
	if !ok {
		return fmt.Errorf("conversation %s not found", id)
	}
	if cs.Created {
		return fmt.Errorf("cannot delete created conversation %s", id)
	}

	delete(convs, id)
	return s.saveLocked()
}

// ForceDelete removes a conversation from local state regardless of its created status.
// Used when a conversation has been permanently deleted on the server.
func (s *Store) ForceDelete(id string) error {
	return s.ForceDeleteForBackend(s.GetDefaultBackend(), id)
}

// ForceDeleteForBackend removes a conversation from the specified backend regardless of created status.
func (s *Store) ForceDeleteForBackend(backend, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	convs := s.conversationsForBackend(backend)
	if convs == nil {
		return fmt.Errorf("backend %q not found", backend)
	}

	if _, ok := convs[id]; !ok {
		return fmt.Errorf("conversation %s not found", id)
	}

	delete(convs, id)
	return s.saveLocked()
}

// ListMappings returns all conversations with their server IDs and slugs.
// Used by FUSE to create symlinks for alternative access paths.
func (s *Store) ListMappings() []ConversationState {
	return s.ListMappingsForBackend(s.GetDefaultBackend())
}

// ListMappingsForBackend returns all conversations for the specified backend.
func (s *Store) ListMappingsForBackend(backend string) []ConversationState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	convs := s.conversationsForBackend(backend)
	if convs == nil {
		return nil
	}

	result := make([]ConversationState, 0, len(convs))
	for _, cs := range convs {
		result = append(result, *cs)
	}
	return result
}

// Adopt creates a local conversation entry for an existing Shelley server conversation.
// Returns the new local ID. If the Shelley ID is already tracked locally, returns the existing local ID.
func (s *Store) Adopt(shelleyConversationID string) (string, error) {
	return s.AdoptForBackend(s.GetDefaultBackend(), shelleyConversationID)
}

// AdoptForBackend creates a local conversation entry for an existing Shelley conversation on the specified backend.
func (s *Store) AdoptForBackend(backend, shelleyConversationID string) (string, error) {
	return s.AdoptWithSlugForBackend(backend, shelleyConversationID, "")
}

// AdoptWithSlug creates a local conversation entry for an existing Shelley server conversation,
// including the slug. Returns the new local ID. If the Shelley ID is already tracked locally,
// returns the existing local ID and updates the slug if it was previously empty.
func (s *Store) AdoptWithSlug(shelleyConversationID, slug string) (string, error) {
	return s.AdoptWithSlugForBackend(s.GetDefaultBackend(), shelleyConversationID, slug)
}

// AdoptWithSlugForBackend creates a local conversation entry with slug on the specified backend.
func (s *Store) AdoptWithSlugForBackend(backend, shelleyConversationID, slug string) (string, error) {
	return s.AdoptWithMetadataForBackend(backend, shelleyConversationID, slug, "", "", "", "")
}

// AdoptWithMetadata creates a local conversation entry for an existing Shelley server conversation,
// including metadata from the API. Returns the new local ID. If the Shelley ID is already tracked
// locally, returns the existing local ID and updates metadata if previously empty.
func (s *Store) AdoptWithMetadata(shelleyConversationID, slug, apiCreatedAt, apiUpdatedAt, model, cwd string) (string, error) {
	return s.AdoptWithMetadataForBackend(s.GetDefaultBackend(), shelleyConversationID, slug, apiCreatedAt, apiUpdatedAt, model, cwd)
}

// AdoptWithMetadataForBackend creates a local conversation entry with metadata on the specified backend.
func (s *Store) AdoptWithMetadataForBackend(backend, shelleyConversationID, slug, apiCreatedAt, apiUpdatedAt, model, cwd string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	convs := s.conversationsForBackend(backend)
	if convs == nil {
		return "", fmt.Errorf("backend %q not found", backend)
	}

	// Check if already tracked
	for _, cs := range convs {
		if cs.ShelleyConversationID == shelleyConversationID {
			updated := false
			// Update slug if it was previously empty and a new slug is provided
			if slug != "" && cs.Slug == "" {
				cs.Slug = slug
				updated = true
			}
			// Update API timestamps if not already set
			if apiCreatedAt != "" && cs.APICreatedAt == "" {
				cs.APICreatedAt = apiCreatedAt
				updated = true
			}
			if apiUpdatedAt != "" && (cs.APIUpdatedAt == "" || apiUpdatedAt > cs.APIUpdatedAt) {
				cs.APIUpdatedAt = apiUpdatedAt
				updated = true
			}
			if model != "" && cs.Model == "" {
				cs.Model = model
				updated = true
			}
			if cwd != "" && cs.Cwd == "" {
				cs.Cwd = cwd
				updated = true
			}
			if updated {
				_ = s.saveLocked() // Best effort save
			}
			return cs.LocalID, nil
		}
	}

	// Generate a new local ID
	id, err := s.generateIDForBackend(backend)
	if err != nil {
		return "", err
	}

	convs[id] = &ConversationState{
		LocalID:               id,
		ShelleyConversationID: shelleyConversationID,
		Slug:                  slug,
		Model:                 model,
		Cwd:                   cwd,
		Created:               true, // Already exists on server
		CreatedAt:             time.Now(),
		APICreatedAt:          apiCreatedAt,
		APIUpdatedAt:          apiUpdatedAt,
	}

	if err := s.saveLocked(); err != nil {
		delete(convs, id)
		return "", err
	}
	return id, nil
}

// Load reads state from disk. Returns os.ErrNotExist if file doesn't exist.
func (s *Store) Load() error {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return err
	}

	// Try to load as new format (backends map)
	var newFormat struct {
		Backends       map[string]*BackendState `json:"backends"`
		DefaultBackend string                  `json:"default_backend,omitempty"`
	}
	if err := json.Unmarshal(data, &newFormat); err == nil {
		if newFormat.Backends != nil {
			s.Backends = newFormat.Backends
			s.DefaultBackend = newFormat.DefaultBackend
			// Ensure default backend exists
			s.defaultBackend()
			return nil
		}
	}

	// If new format failed, try old format (flat conversations map) and migrate
	var v1 V1State
	if err := json.Unmarshal(data, &v1); err != nil {
		return fmt.Errorf("failed to parse state file: %w", err)
	}

	// Migrate from old format
	if v1.Conversations != nil {
		if err := s.migrateFromV1(&v1); err != nil {
			return fmt.Errorf("failed to migrate from V1 format: %w", err)
		}
		// Save in new format
		if err := s.saveLocked(); err != nil {
			return fmt.Errorf("failed to save migrated state: %w", err)
		}
	}

	return nil
}

func (s *Store) saveLocked() error {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}
	data, err := json.MarshalIndent(struct {
		Backends       map[string]*BackendState `json:"backends"`
		DefaultBackend string                  `json:"default_backend,omitempty"`
	}{Backends: s.Backends, DefaultBackend: s.DefaultBackend}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}
	return os.WriteFile(s.Path, data, 0644)
}

func (s *Store) generateID() (string, error) {
	return s.generateIDForBackend(s.getDefaultBackend())
}

// generateIDForBackend generates a unique 8-char hex ID for the named backend.
func (s *Store) generateIDForBackend(backend string) (string, error) {
	convs := s.conversationsForBackend(backend)
	if convs == nil {
		return "", fmt.Errorf("backend %q not found", backend)
	}
	for i := 0; i < 100; i++ {
		buf := make([]byte, 4)
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("failed to generate random ID: %w", err)
		}
		id := hex.EncodeToString(buf)
		if _, exists := convs[id]; !exists {
			return id, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique ID after 100 attempts")
}

// Reserved backend names that cannot be created by users.
var reservedBackendNames = map[string]bool{
	"default": true,
	"all":     true,
}

// CreateBackend creates a new backend with the given name and URL.
// Returns an error if the name is reserved or already exists.
func (s *Store) CreateBackend(name, url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if reservedBackendNames[name] {
		return fmt.Errorf("backend name %q is reserved", name)
	}

	if _, exists := s.Backends[name]; exists {
		return fmt.Errorf("backend %q already exists", name)
	}

	s.Backends[name] = &BackendState{
		URL:           url,
		Conversations: make(map[string]*ConversationState),
	}
	return s.saveLocked()
}

// GetBackend returns the backend state for the given name, or nil if not found.
func (s *Store) GetBackend(name string) *BackendState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Backends[name]
}

// DeleteBackend removes a backend from state.
// Returns an error if the backend doesn't exist or is the default backend.
func (s *Store) DeleteBackend(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if name == s.getDefaultBackend() {
		return fmt.Errorf("cannot delete default backend %q", name)
	}

	if _, exists := s.Backends[name]; !exists {
		return fmt.Errorf("backend %q not found", name)
	}

	delete(s.Backends, name)
	return s.saveLocked()
}

// RenameBackend renames a backend.
// Returns an error if the old name doesn't exist, new name is reserved, or new name already exists.
// If the renamed backend is the default, updates the default backend reference.
func (s *Store) RenameBackend(oldName, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Backends[oldName]; !exists {
		return fmt.Errorf("backend %q not found", oldName)
	}

	if reservedBackendNames[newName] {
		return fmt.Errorf("backend name %q is reserved", newName)
	}

	if _, exists := s.Backends[newName]; exists {
		return fmt.Errorf("backend %q already exists", newName)
	}

	// Move the backend to the new name
	s.Backends[newName] = s.Backends[oldName]
	delete(s.Backends, oldName)

	// Update default backend if needed
	if s.DefaultBackend == oldName {
		s.DefaultBackend = newName
	}

	return s.saveLocked()
}

// SetDefaultBackend sets the default backend.
// Returns an error if the backend doesn't exist.
func (s *Store) SetDefaultBackend(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Backends[name]; !exists {
		return fmt.Errorf("backend %q not found", name)
	}

	s.DefaultBackend = name
	return s.saveLocked()
}

// GetDefaultBackend returns the name of the default backend.
// If no default is set, returns "default".
func (s *Store) GetDefaultBackend() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getDefaultBackend()
}

// getDefaultBackend returns the default backend name without locking.
func (s *Store) getDefaultBackend() string {
	if s.DefaultBackend == "" {
		return mainBackendName
	}
	return s.DefaultBackend
}

// ListBackends returns all backend names, sorted.
// Ensures the default backend exists before listing.
func (s *Store) ListBackends() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure the default backend exists
	if _, ok := s.Backends[mainBackendName]; !ok {
		s.Backends[mainBackendName] = &BackendState{
			URL:           "",
			Conversations: make(map[string]*ConversationState),
		}
	}

	names := make([]string, 0, len(s.Backends))
	for name := range s.Backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SetBackendURL sets the URL for an existing backend.
// Returns an error if the backend doesn't exist.
func (s *Store) SetBackendURL(name, url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, exists := s.Backends[name]
	if !exists {
		return fmt.Errorf("backend %q not found", name)
	}

	b.URL = url
	return s.saveLocked()
}

// EnsureBackendURL sets the URL for a backend, creating it if it doesn't exist.
// This is useful for initializing the default backend URL on startup.
func (s *Store) EnsureBackendURL(name, url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Backends[name]; !exists {
		s.Backends[name] = &BackendState{
			Conversations: make(map[string]*ConversationState),
		}
	}

	s.Backends[name].URL = url
	return s.saveLocked()
}
