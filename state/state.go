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

// Store manages local conversation state, persisted to a JSON file.
type Store struct {
	Path          string
	Conversations map[string]*ConversationState `json:"conversations"`
	mu            sync.RWMutex
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
		Path:          path,
		Conversations: make(map[string]*ConversationState),
	}
	if err := s.Load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

// Clone allocates a new conversation with a short random hex ID and persists.
func (s *Store) Clone() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := s.generateID()
	if err != nil {
		return "", err
	}
	s.Conversations[id] = &ConversationState{
		LocalID:   id,
		CreatedAt: time.Now(),
	}
	if err := s.saveLocked(); err != nil {
		delete(s.Conversations, id)
		return "", err
	}
	return id, nil
}

// Get returns the state for a conversation, or nil if not found.
func (s *Store) Get(id string) *ConversationState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Conversations[id]
}

// SetModel sets the model display name and internal ID on an unconversed conversation.
// displayName is the user-facing name; internalID is the API model ID.
// Returns an error if the conversation doesn't exist or is already created.
func (s *Store) SetModel(id, displayName, internalID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cs, ok := s.Conversations[id]
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
	s.mu.Lock()
	defer s.mu.Unlock()

	cs, ok := s.Conversations[id]
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
	s.mu.Lock()
	defer s.mu.Unlock()

	cs, ok := s.Conversations[id]
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.Conversations))
	for id := range s.Conversations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// GetByShelleyID returns the local ID for a given Shelley conversation ID, or empty string if not found.
func (s *Store) GetByShelleyID(shelleyID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, cs := range s.Conversations {
		if cs.ShelleyConversationID == shelleyID {
			return cs.LocalID
		}
	}
	return ""
}

// GetBySlug returns the local ID for a given slug, or empty string if not found.
func (s *Store) GetBySlug(slug string) string {
	if slug == "" {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, cs := range s.Conversations {
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
	s.mu.Lock()
	defer s.mu.Unlock()

	cs, ok := s.Conversations[id]
	if !ok {
		return fmt.Errorf("conversation %s not found", id)
	}
	if cs.Created {
		return fmt.Errorf("cannot delete created conversation %s", id)
	}

	delete(s.Conversations, id)
	return s.saveLocked()
}

// ForceDelete removes a conversation from local state regardless of its created status.
// Used when a conversation has been permanently deleted on the server.
func (s *Store) ForceDelete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.Conversations[id]; !ok {
		return fmt.Errorf("conversation %s not found", id)
	}

	delete(s.Conversations, id)
	return s.saveLocked()
}

// ListMappings returns all conversations with their server IDs and slugs.
// Used by FUSE to create symlinks for alternative access paths.
func (s *Store) ListMappings() []ConversationState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]ConversationState, 0, len(s.Conversations))
	for _, cs := range s.Conversations {
		result = append(result, *cs)
	}
	return result
}

// Adopt creates a local conversation entry for an existing Shelley server conversation.
// Returns the new local ID. If the Shelley ID is already tracked locally, returns the existing local ID.
func (s *Store) Adopt(shelleyConversationID string) (string, error) {
	return s.AdoptWithSlug(shelleyConversationID, "")
}

// AdoptWithSlug creates a local conversation entry for an existing Shelley server conversation,
// including the slug. Returns the new local ID. If the Shelley ID is already tracked locally,
// returns the existing local ID and updates the slug if it was previously empty.
func (s *Store) AdoptWithSlug(shelleyConversationID, slug string) (string, error) {
	return s.AdoptWithMetadata(shelleyConversationID, slug, "", "", "", "")
}

// AdoptWithMetadata creates a local conversation entry for an existing Shelley server conversation,
// including metadata from the API. Returns the new local ID. If the Shelley ID is already tracked
// locally, returns the existing local ID and updates metadata if previously empty.
func (s *Store) AdoptWithMetadata(shelleyConversationID, slug, apiCreatedAt, apiUpdatedAt, model, cwd string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if already tracked
	for _, cs := range s.Conversations {
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
	id, err := s.generateID()
	if err != nil {
		return "", err
	}

	s.Conversations[id] = &ConversationState{
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
		delete(s.Conversations, id)
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
	var loaded struct {
		Conversations map[string]*ConversationState `json:"conversations"`
	}
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("failed to parse state file: %w", err)
	}
	if loaded.Conversations != nil {
		s.Conversations = loaded.Conversations
	}
	return nil
}

func (s *Store) saveLocked() error {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}
	data, err := json.MarshalIndent(struct {
		Conversations map[string]*ConversationState `json:"conversations"`
	}{Conversations: s.Conversations}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}
	return os.WriteFile(s.Path, data, 0644)
}

func (s *Store) generateID() (string, error) {
	for i := 0; i < 100; i++ {
		buf := make([]byte, 4)
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("failed to generate random ID: %w", err)
		}
		id := hex.EncodeToString(buf)
		if _, exists := s.Conversations[id]; !exists {
			return id, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique ID after 100 attempts")
}
