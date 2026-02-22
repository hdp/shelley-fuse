package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func tempStatePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state.json")
}

func TestCloneGeneratesUniqueIDs(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		id, err := s.Clone()
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != 8 {
			t.Errorf("expected 8-char hex ID, got %q", id)
		}
		if seen[id] {
			t.Errorf("duplicate ID: %s", id)
		}
		seen[id] = true
	}
}

func TestCloneCreatesState(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	id, err := s.Clone()
	if err != nil {
		t.Fatal(err)
	}

	cs := s.Get(id)
	if cs == nil {
		t.Fatal("expected conversation state, got nil")
	}
	if cs.LocalID != id {
		t.Errorf("expected LocalID=%s, got %s", id, cs.LocalID)
	}
	if cs.Created {
		t.Error("expected Created=false for new clone")
	}
}

func TestSetCtl(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	id, _ := s.Clone()

	if err := s.SetCtl(id, "model", "predictable"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCtl(id, "cwd", "/tmp"); err != nil {
		t.Fatal(err)
	}

	cs := s.Get(id)
	if cs.Model != "predictable" {
		t.Errorf("expected model=predictable, got %s", cs.Model)
	}
	if cs.Cwd != "/tmp" {
		t.Errorf("expected cwd=/tmp, got %s", cs.Cwd)
	}
}

func TestSetCtlUnknownKey(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	id, _ := s.Clone()
	if err := s.SetCtl(id, "bogus", "val"); err == nil {
		t.Error("expected error for unknown ctl key")
	}
}

func TestSetCtlReadOnlyAfterCreated(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	id, _ := s.Clone()
	_ = s.SetCtl(id, "model", "predictable")
	_ = s.MarkCreated(id, "shelley-123", "")

	if err := s.SetCtl(id, "model", "other"); err == nil {
		t.Error("expected error when setting ctl on created conversation")
	}
}

func TestSetCtlNotFound(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetCtl("nonexistent", "model", "x"); err == nil {
		t.Error("expected error for nonexistent conversation")
	}
}

func TestMarkCreated(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	id, _ := s.Clone()
	if err := s.MarkCreated(id, "shelley-abc", "test-slug"); err != nil {
		t.Fatal(err)
	}

	cs := s.Get(id)
	if !cs.Created {
		t.Error("expected Created=true")
	}
	if cs.ShelleyConversationID != "shelley-abc" {
		t.Errorf("expected ShelleyConversationID=shelley-abc, got %s", cs.ShelleyConversationID)
	}
	if cs.Slug != "test-slug" {
		t.Errorf("expected Slug=test-slug, got %s", cs.Slug)
	}
}

func TestList(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	id1, _ := s.Clone()
	id2, _ := s.Clone()

	ids := s.List()
	if len(ids) != 2 {
		t.Fatalf("expected 2 IDs, got %d", len(ids))
	}
	// List returns sorted
	if ids[0] > ids[1] {
		t.Error("expected sorted IDs")
	}
	found := map[string]bool{id1: false, id2: false}
	for _, id := range ids {
		found[id] = true
	}
	for id, ok := range found {
		if !ok {
			t.Errorf("missing ID %s in list", id)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	id, _ := s1.Clone()
	_ = s1.SetCtl(id, "model", "predictable")
	_ = s1.SetCtl(id, "cwd", "/home/user")
	_ = s1.MarkCreated(id, "shelley-xyz", "xyz-slug")

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	cs := s2.Get(id)
	if cs == nil {
		t.Fatal("expected conversation state after reload, got nil")
	}
	if cs.Model != "predictable" {
		t.Errorf("expected model=predictable, got %s", cs.Model)
	}
	if cs.Cwd != "/home/user" {
		t.Errorf("expected cwd=/home/user, got %s", cs.Cwd)
	}
	if !cs.Created {
		t.Error("expected Created=true after reload")
	}
	if cs.ShelleyConversationID != "shelley-xyz" {
		t.Errorf("expected ShelleyConversationID=shelley-xyz, got %s", cs.ShelleyConversationID)
	}
	if cs.Slug != "xyz-slug" {
		t.Errorf("expected Slug=xyz-slug, got %s", cs.Slug)
	}
}

func TestNewStoreNonexistentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist", "state.json")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.conversations()) != 0 {
		t.Errorf("expected empty conversations, got %d", len(s.conversations()))
	}
}

func TestNewStoreCorruptFile(t *testing.T) {
	path := tempStatePath(t)
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte("not json"), 0644)

	_, err := NewStore(path)
	if err == nil {
		t.Error("expected error for corrupt state file")
	}
}

func TestGetByShelleyID(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Empty store should return empty string
	if got := s.GetByShelleyID("nonexistent"); got != "" {
		t.Errorf("expected empty string for nonexistent ID, got %q", got)
	}

	// Create a conversation and mark it created
	id, _ := s.Clone()
	_ = s.MarkCreated(id, "shelley-abc-123", "")

	// Should find the local ID by Shelley ID
	if got := s.GetByShelleyID("shelley-abc-123"); got != id {
		t.Errorf("expected %q, got %q", id, got)
	}

	// Non-existent Shelley ID should return empty
	if got := s.GetByShelleyID("other-shelley-id"); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestGetByShelleyIDMultipleConversations(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	id1, _ := s.Clone()
	_ = s.MarkCreated(id1, "shelley-111", "")

	id2, _ := s.Clone()
	_ = s.MarkCreated(id2, "shelley-222", "")

	id3, _ := s.Clone()
	// id3 is not created, so no Shelley ID

	if got := s.GetByShelleyID("shelley-111"); got != id1 {
		t.Errorf("expected %q for shelley-111, got %q", id1, got)
	}
	if got := s.GetByShelleyID("shelley-222"); got != id2 {
		t.Errorf("expected %q for shelley-222, got %q", id2, got)
	}
	// id3 has no Shelley ID, so searching for any random ID shouldn't return it
	if got := s.GetByShelleyID(id3); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestAdopt(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Adopt a new server conversation
	localID, err := s.Adopt("server-conv-123")
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}

	// Verify the local ID is an 8-char hex
	if len(localID) != 8 {
		t.Errorf("expected 8-char hex ID, got %q", localID)
	}

	// Verify the state is correct
	cs := s.Get(localID)
	if cs == nil {
		t.Fatal("expected conversation state, got nil")
	}
	if cs.ShelleyConversationID != "server-conv-123" {
		t.Errorf("expected ShelleyConversationID=server-conv-123, got %s", cs.ShelleyConversationID)
	}
	if !cs.Created {
		t.Error("expected Created=true for adopted conversation")
	}
	if cs.LocalID != localID {
		t.Errorf("expected LocalID=%s, got %s", localID, cs.LocalID)
	}
}

func TestAdoptIdempotent(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Adopt the same server conversation twice
	localID1, err := s.Adopt("server-conv-456")
	if err != nil {
		t.Fatalf("first Adopt failed: %v", err)
	}

	localID2, err := s.Adopt("server-conv-456")
	if err != nil {
		t.Fatalf("second Adopt failed: %v", err)
	}

	// Should return the same local ID
	if localID1 != localID2 {
		t.Errorf("expected same local ID, got %q and %q", localID1, localID2)
	}

	// Should only have one conversation
	ids := s.List()
	if len(ids) != 1 {
		t.Errorf("expected 1 conversation, got %d", len(ids))
	}
}

func TestAdoptExistingLocalConversation(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Create a conversation the normal way (clone + mark created)
	localID, _ := s.Clone()
	_ = s.MarkCreated(localID, "server-conv-789", "")

	// Adopt the same server conversation
	adoptedID, err := s.Adopt("server-conv-789")
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}

	// Should return the existing local ID
	if adoptedID != localID {
		t.Errorf("expected existing local ID %q, got %q", localID, adoptedID)
	}

	// Should still only have one conversation
	ids := s.List()
	if len(ids) != 1 {
		t.Errorf("expected 1 conversation, got %d", len(ids))
	}
}

func TestAdoptPersistence(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	localID, err := s1.Adopt("server-persist-test")
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	cs := s2.Get(localID)
	if cs == nil {
		t.Fatal("expected conversation state after reload, got nil")
	}
	if cs.ShelleyConversationID != "server-persist-test" {
		t.Errorf("expected ShelleyConversationID=server-persist-test, got %s", cs.ShelleyConversationID)
	}
	if !cs.Created {
		t.Error("expected Created=true after reload")
	}
}

func TestAdoptWithSlug(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Adopt a new server conversation with a slug
	localID, err := s.AdoptWithSlug("server-conv-with-slug", "my-slug")
	if err != nil {
		t.Fatalf("AdoptWithSlug failed: %v", err)
	}

	// Verify the state is correct
	cs := s.Get(localID)
	if cs == nil {
		t.Fatal("expected conversation state, got nil")
	}
	if cs.ShelleyConversationID != "server-conv-with-slug" {
		t.Errorf("expected ShelleyConversationID=server-conv-with-slug, got %s", cs.ShelleyConversationID)
	}
	if cs.Slug != "my-slug" {
		t.Errorf("expected Slug=my-slug, got %s", cs.Slug)
	}
	if !cs.Created {
		t.Error("expected Created=true for adopted conversation")
	}
}

func TestAdoptWithSlugUpdatesEmptySlug(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// First adopt without slug
	localID1, err := s.AdoptWithSlug("server-conv-update-slug", "")
	if err != nil {
		t.Fatalf("first AdoptWithSlug failed: %v", err)
	}

	cs := s.Get(localID1)
	if cs.Slug != "" {
		t.Errorf("expected empty slug initially, got %s", cs.Slug)
	}

	// Adopt again with a slug - should update the slug
	localID2, err := s.AdoptWithSlug("server-conv-update-slug", "updated-slug")
	if err != nil {
		t.Fatalf("second AdoptWithSlug failed: %v", err)
	}

	// Should return same local ID
	if localID1 != localID2 {
		t.Errorf("expected same local ID, got %q and %q", localID1, localID2)
	}

	// Slug should now be updated
	cs = s.Get(localID1)
	if cs.Slug != "updated-slug" {
		t.Errorf("expected Slug=updated-slug, got %s", cs.Slug)
	}
}

func TestAdoptWithSlugDoesNotOverwriteExistingSlug(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// First adopt with a slug
	localID1, err := s.AdoptWithSlug("server-conv-keep-slug", "original-slug")
	if err != nil {
		t.Fatalf("first AdoptWithSlug failed: %v", err)
	}

	cs := s.Get(localID1)
	if cs.Slug != "original-slug" {
		t.Errorf("expected original-slug, got %s", cs.Slug)
	}

	// Adopt again with a different slug - should NOT update the slug
	localID2, err := s.AdoptWithSlug("server-conv-keep-slug", "new-slug")
	if err != nil {
		t.Fatalf("second AdoptWithSlug failed: %v", err)
	}

	// Should return same local ID
	if localID1 != localID2 {
		t.Errorf("expected same local ID, got %q and %q", localID1, localID2)
	}

	// Slug should still be the original
	cs = s.Get(localID1)
	if cs.Slug != "original-slug" {
		t.Errorf("expected Slug=original-slug, got %s", cs.Slug)
	}
}

func TestAdoptWithSlugUpdatesPersists(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	// Adopt without slug
	localID, err := s1.AdoptWithSlug("server-persist-slug", "")
	if err != nil {
		t.Fatalf("first AdoptWithSlug failed: %v", err)
	}

	// Adopt again with a slug - should update the slug
	_, err = s1.AdoptWithSlug("server-persist-slug", "persisted-slug")
	if err != nil {
		t.Fatalf("second AdoptWithSlug failed: %v", err)
	}

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	// Slug should be persisted
	cs := s2.Get(localID)
	if cs == nil {
		t.Fatal("expected conversation state after reload, got nil")
	}
	if cs.Slug != "persisted-slug" {
		t.Errorf("expected Slug=persisted-slug after reload, got %s", cs.Slug)
	}
}

func TestAdoptWithSlugNoopOnEmptyNewSlug(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Adopt with a slug
	localID, err := s.AdoptWithSlug("server-conv-empty-noop", "has-slug")
	if err != nil {
		t.Fatalf("first AdoptWithSlug failed: %v", err)
	}

	// Adopt again with empty slug - should not change anything
	_, err = s.AdoptWithSlug("server-conv-empty-noop", "")
	if err != nil {
		t.Fatalf("second AdoptWithSlug failed: %v", err)
	}

	cs := s.Get(localID)
	if cs.Slug != "has-slug" {
		t.Errorf("expected Slug=has-slug, got %s", cs.Slug)
	}
}

func TestGetBySlug(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Empty slug returns empty string
	if got := s.GetBySlug(""); got != "" {
		t.Errorf("GetBySlug('') = %q, want empty", got)
	}

	// No conversations yet
	if got := s.GetBySlug("some-slug"); got != "" {
		t.Errorf("GetBySlug('some-slug') = %q, want empty", got)
	}

	// Add a conversation with a slug
	localID, err := s.AdoptWithSlug("server-id-1", "my-slug")
	if err != nil {
		t.Fatal(err)
	}

	// Now it should be found
	if got := s.GetBySlug("my-slug"); got != localID {
		t.Errorf("GetBySlug('my-slug') = %q, want %q", got, localID)
	}

	// Different slug not found
	if got := s.GetBySlug("other-slug"); got != "" {
		t.Errorf("GetBySlug('other-slug') = %q, want empty", got)
	}
}

func TestListMappings(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Empty store
	mappings := s.ListMappings()
	if len(mappings) != 0 {
		t.Errorf("expected 0 mappings, got %d", len(mappings))
	}

	// Add some conversations
	localID1, _ := s.Clone()
	s.MarkCreated(localID1, "server-id-1", "slug-1")

	localID2, _ := s.AdoptWithSlug("server-id-2", "slug-2")

	localID3, _ := s.Clone() // uncreated, no server ID or slug

	mappings = s.ListMappings()
	if len(mappings) != 3 {
		t.Errorf("expected 3 mappings, got %d", len(mappings))
	}

	// Verify we can find all the data
	found := make(map[string]ConversationState)
	for _, m := range mappings {
		found[m.LocalID] = m
	}

	if m, ok := found[localID1]; !ok {
		t.Errorf("missing mapping for %s", localID1)
	} else {
		if m.ShelleyConversationID != "server-id-1" || m.Slug != "slug-1" {
			t.Errorf("wrong mapping for %s: %+v", localID1, m)
		}
	}

	if m, ok := found[localID2]; !ok {
		t.Errorf("missing mapping for %s", localID2)
	} else {
		if m.ShelleyConversationID != "server-id-2" || m.Slug != "slug-2" {
			t.Errorf("wrong mapping for %s: %+v", localID2, m)
		}
	}

	if m, ok := found[localID3]; !ok {
		t.Errorf("missing mapping for %s", localID3)
	} else {
		if m.ShelleyConversationID != "" || m.Slug != "" {
			t.Errorf("unexpected mapping for uncreated %s: %+v", localID3, m)
		}
	}
}

func TestDelete(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Clone a conversation
	id, err := s.Clone()
	if err != nil {
		t.Fatal(err)
	}

	// Verify it exists
	if s.Get(id) == nil {
		t.Fatal("expected conversation to exist")
	}

	// Delete it
	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify it's gone
	if s.Get(id) != nil {
		t.Error("expected conversation to be deleted")
	}
}

func TestDeleteNotFound(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Try to delete non-existent conversation
	if err := s.Delete("nonexistent"); err == nil {
		t.Error("expected error for nonexistent conversation")
	}
}

func TestDeleteRefusesCreatedConversation(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Clone and mark created
	id, _ := s.Clone()
	_ = s.MarkCreated(id, "shelley-123", "slug")

	// Try to delete - should fail
	if err := s.Delete(id); err == nil {
		t.Error("expected error when deleting created conversation")
	}

	// Verify it still exists
	if s.Get(id) == nil {
		t.Error("created conversation should not be deleted")
	}
}

func TestDeletePersistence(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	// Clone two conversations
	id1, _ := s1.Clone()
	id2, _ := s1.Clone()

	// Delete one
	if err := s1.Delete(id1); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Load into fresh store and verify persistence
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if s2.Get(id1) != nil {
		t.Error("deleted conversation should not persist")
	}
	if s2.Get(id2) == nil {
		t.Error("non-deleted conversation should persist")
	}
}

func TestAdoptWithMetadata(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	localID, err := s.AdoptWithMetadata("server-meta-123", "test-slug", "2024-01-15T10:30:00Z", "2024-01-16T14:20:00Z", "", "")
	if err != nil {
		t.Fatalf("AdoptWithMetadata failed: %v", err)
	}

	cs := s.Get(localID)
	if cs == nil {
		t.Fatal("expected conversation state")
	}
	if cs.Slug != "test-slug" {
		t.Errorf("expected Slug=test-slug, got %s", cs.Slug)
	}
	if cs.APICreatedAt != "2024-01-15T10:30:00Z" {
		t.Errorf("expected APICreatedAt=2024-01-15T10:30:00Z, got %s", cs.APICreatedAt)
	}
	if cs.APIUpdatedAt != "2024-01-16T14:20:00Z" {
		t.Errorf("expected APIUpdatedAt=2024-01-16T14:20:00Z, got %s", cs.APIUpdatedAt)
	}
}

func TestAdoptWithMetadataUpdatesTimestamps(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// First adoption without timestamps
	localID, err := s.AdoptWithMetadata("server-meta-update", "slug", "", "", "", "")
	if err != nil {
		t.Fatalf("first AdoptWithMetadata failed: %v", err)
	}

	// Second adoption with timestamps should update them
	_, err = s.AdoptWithMetadata("server-meta-update", "", "2024-01-15T10:30:00Z", "2024-01-16T14:20:00Z", "", "")
	if err != nil {
		t.Fatalf("second AdoptWithMetadata failed: %v", err)
	}

	cs := s.Get(localID)
	if cs.APICreatedAt != "2024-01-15T10:30:00Z" {
		t.Errorf("expected APICreatedAt to be updated, got %s", cs.APICreatedAt)
	}
	if cs.APIUpdatedAt != "2024-01-16T14:20:00Z" {
		t.Errorf("expected APIUpdatedAt to be updated, got %s", cs.APIUpdatedAt)
	}
}

func TestAdoptWithMetadataUpdatesNewerTimestamp(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// First adoption with older timestamp
	localID, err := s.AdoptWithMetadata("server-meta-newer", "slug", "2024-01-15T10:30:00Z", "2024-01-16T14:20:00Z", "", "")
	if err != nil {
		t.Fatalf("first AdoptWithMetadata failed: %v", err)
	}

	// Second adoption with newer updated_at should update
	_, err = s.AdoptWithMetadata("server-meta-newer", "", "", "2024-01-17T09:00:00Z", "", "")
	if err != nil {
		t.Fatalf("second AdoptWithMetadata failed: %v", err)
	}

	cs := s.Get(localID)
	// created_at should not change (already set)
	if cs.APICreatedAt != "2024-01-15T10:30:00Z" {
		t.Errorf("expected APICreatedAt unchanged, got %s", cs.APICreatedAt)
	}
	// updated_at should be newer
	if cs.APIUpdatedAt != "2024-01-17T09:00:00Z" {
		t.Errorf("expected APIUpdatedAt=2024-01-17T09:00:00Z, got %s", cs.APIUpdatedAt)
	}
}

func TestAdoptWithMetadataPersistence(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	localID, err := s1.AdoptWithMetadata("server-meta-persist", "slug", "2024-01-15T10:30:00Z", "2024-01-16T14:20:00Z", "", "")
	if err != nil {
		t.Fatalf("AdoptWithMetadata failed: %v", err)
	}

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	cs := s2.Get(localID)
	if cs == nil {
		t.Fatal("expected conversation state after reload")
	}
	if cs.APICreatedAt != "2024-01-15T10:30:00Z" {
		t.Errorf("expected APICreatedAt persisted, got %s", cs.APICreatedAt)
	}
	if cs.APIUpdatedAt != "2024-01-16T14:20:00Z" {
		t.Errorf("expected APIUpdatedAt persisted, got %s", cs.APIUpdatedAt)
	}
}

func TestSetModel(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	id, _ := s.Clone()

	// Set model with display name and internal ID
	if err := s.SetModel(id, "kimi-2.5-fireworks", "custom-f999b9b0"); err != nil {
		t.Fatal(err)
	}

	cs := s.Get(id)
	if cs.Model != "kimi-2.5-fireworks" {
		t.Errorf("Model = %q, want %q", cs.Model, "kimi-2.5-fireworks")
	}
	if cs.ModelID != "custom-f999b9b0" {
		t.Errorf("ModelID = %q, want %q", cs.ModelID, "custom-f999b9b0")
	}
}

func TestSetModelReadOnlyAfterCreated(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	id, _ := s.Clone()
	_ = s.SetModel(id, "predictable", "predictable")
	_ = s.MarkCreated(id, "shelley-123", "")

	if err := s.SetModel(id, "other", "other"); err == nil {
		t.Error("expected error when setting model on created conversation")
	}
}

func TestSetModelNotFound(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetModel("nonexistent", "x", "x"); err == nil {
		t.Error("expected error for nonexistent conversation")
	}
}

func TestEffectiveModelID(t *testing.T) {
	// When ModelID is set, use it
	cs := &ConversationState{Model: "kimi-2.5-fireworks", ModelID: "custom-f999b9b0"}
	if got := cs.EffectiveModelID(); got != "custom-f999b9b0" {
		t.Errorf("EffectiveModelID() = %q, want %q", got, "custom-f999b9b0")
	}

	// When ModelID is empty, fall back to Model
	cs = &ConversationState{Model: "predictable"}
	if got := cs.EffectiveModelID(); got != "predictable" {
		t.Errorf("EffectiveModelID() = %q, want %q", got, "predictable")
	}

	// Both empty
	cs = &ConversationState{}
	if got := cs.EffectiveModelID(); got != "" {
		t.Errorf("EffectiveModelID() = %q, want empty", got)
	}
}

func TestSetModelPersistence(t *testing.T) {
	path := tempStatePath(t)
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	id, _ := s.Clone()
	if err := s.SetModel(id, "kimi-2.5-fireworks", "custom-abc"); err != nil {
		t.Fatal(err)
	}

	// Reload from disk
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	cs := s2.Get(id)
	if cs == nil {
		t.Fatal("conversation not found after reload")
	}
	if cs.Model != "kimi-2.5-fireworks" {
		t.Errorf("Model = %q, want %q", cs.Model, "kimi-2.5-fireworks")
	}
	if cs.ModelID != "custom-abc" {
		t.Errorf("ModelID = %q, want %q", cs.ModelID, "custom-abc")
	}
}

func TestAdoptWithMetadataModel(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Adopt with a model
	localID, err := s.AdoptWithMetadata("server-model-123", "slug", "2024-01-15T10:30:00Z", "2024-01-16T14:20:00Z", "claude-sonnet-4-5", "")
	if err != nil {
		t.Fatalf("AdoptWithMetadata failed: %v", err)
	}

	cs := s.Get(localID)
	if cs == nil {
		t.Fatal("expected conversation state")
	}
	if cs.Model != "claude-sonnet-4-5" {
		t.Errorf("expected Model=claude-sonnet-4-5, got %s", cs.Model)
	}
}

func TestAdoptWithMetadataModelUpdatesEmpty(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// First adopt without model
	localID, err := s.AdoptWithMetadata("server-model-update", "slug", "", "", "", "")
	if err != nil {
		t.Fatalf("first AdoptWithMetadata failed: %v", err)
	}

	cs := s.Get(localID)
	if cs.Model != "" {
		t.Errorf("expected empty model initially, got %s", cs.Model)
	}

	// Re-adopt with model should update it
	_, err = s.AdoptWithMetadata("server-model-update", "", "", "", "claude-sonnet-4-5", "")
	if err != nil {
		t.Fatalf("second AdoptWithMetadata failed: %v", err)
	}

	cs = s.Get(localID)
	if cs.Model != "claude-sonnet-4-5" {
		t.Errorf("expected Model=claude-sonnet-4-5, got %s", cs.Model)
	}
}

func TestAdoptWithMetadataModelDoesNotOverwrite(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// First adopt with a model
	localID, err := s.AdoptWithMetadata("server-model-keep", "slug", "", "", "claude-sonnet-4-5", "")
	if err != nil {
		t.Fatalf("first AdoptWithMetadata failed: %v", err)
	}

	// Re-adopt with a different model should NOT overwrite
	_, err = s.AdoptWithMetadata("server-model-keep", "", "", "", "gpt-4", "")
	if err != nil {
		t.Fatalf("second AdoptWithMetadata failed: %v", err)
	}

	cs := s.Get(localID)
	if cs.Model != "claude-sonnet-4-5" {
		t.Errorf("expected Model=claude-sonnet-4-5 (original), got %s", cs.Model)
	}
}

func TestAdoptWithMetadataModelPersistence(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	localID, err := s1.AdoptWithMetadata("server-model-persist", "slug", "", "", "claude-sonnet-4-5", "")
	if err != nil {
		t.Fatalf("AdoptWithMetadata failed: %v", err)
	}

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	cs := s2.Get(localID)
	if cs == nil {
		t.Fatal("expected conversation state after reload")
	}
	if cs.Model != "claude-sonnet-4-5" {
		t.Errorf("expected Model persisted as claude-sonnet-4-5, got %s", cs.Model)
	}
}

func TestAdoptWithMetadataCwd(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	localID, err := s.AdoptWithMetadata("server-cwd-123", "slug", "2024-01-15T10:30:00Z", "2024-01-16T14:20:00Z", "", "/home/user/project")
	if err != nil {
		t.Fatalf("AdoptWithMetadata failed: %v", err)
	}

	cs := s.Get(localID)
	if cs == nil {
		t.Fatal("expected conversation state")
	}
	if cs.Cwd != "/home/user/project" {
		t.Errorf("expected Cwd=/home/user/project, got %s", cs.Cwd)
	}
}

func TestAdoptWithMetadataCwdUpdatesEmpty(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// First adopt without cwd
	localID, err := s.AdoptWithMetadata("server-cwd-update", "slug", "", "", "", "")
	if err != nil {
		t.Fatalf("first AdoptWithMetadata failed: %v", err)
	}

	cs := s.Get(localID)
	if cs.Cwd != "" {
		t.Errorf("expected empty cwd initially, got %s", cs.Cwd)
	}

	// Re-adopt with cwd should update it
	_, err = s.AdoptWithMetadata("server-cwd-update", "", "", "", "", "/home/user/project")
	if err != nil {
		t.Fatalf("second AdoptWithMetadata failed: %v", err)
	}

	cs = s.Get(localID)
	if cs.Cwd != "/home/user/project" {
		t.Errorf("expected Cwd=/home/user/project, got %s", cs.Cwd)
	}
}

func TestAdoptWithMetadataCwdDoesNotOverwrite(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// First adopt with a cwd
	localID, err := s.AdoptWithMetadata("server-cwd-keep", "slug", "", "", "", "/home/user/project")
	if err != nil {
		t.Fatalf("first AdoptWithMetadata failed: %v", err)
	}

	// Re-adopt with a different cwd should NOT overwrite
	_, err = s.AdoptWithMetadata("server-cwd-keep", "", "", "", "", "/tmp/other")
	if err != nil {
		t.Fatalf("second AdoptWithMetadata failed: %v", err)
	}

	cs := s.Get(localID)
	if cs.Cwd != "/home/user/project" {
		t.Errorf("expected Cwd=/home/user/project (original), got %s", cs.Cwd)
	}
}

func TestAdoptWithMetadataCwdPersistence(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	localID, err := s1.AdoptWithMetadata("server-cwd-persist", "slug", "", "", "", "/home/user/project")
	if err != nil {
		t.Fatalf("AdoptWithMetadata failed: %v", err)
	}

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	cs := s2.Get(localID)
	if cs == nil {
		t.Fatal("expected conversation state after reload")
	}
	if cs.Cwd != "/home/user/project" {
		t.Errorf("expected Cwd persisted as /home/user/project, got %s", cs.Cwd)
	}
}

func TestForceDelete(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Clone a conversation (uncreated)
	id, err := s.Clone()
	if err != nil {
		t.Fatal(err)
	}

	// Verify it exists
	if s.Get(id) == nil {
		t.Fatal("expected conversation to exist")
	}

	// ForceDelete should work on uncreated conversations
	if err := s.ForceDelete(id); err != nil {
		t.Fatalf("ForceDelete failed: %v", err)
	}

	// Verify it's gone
	if s.Get(id) != nil {
		t.Error("expected conversation to be deleted")
	}
}

func TestForceDeleteCreatedConversation(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Clone and mark created
	id, _ := s.Clone()
	_ = s.MarkCreated(id, "shelley-123", "slug")

	// Regular Delete should refuse
	if err := s.Delete(id); err == nil {
		t.Error("expected regular Delete to fail on created conversation")
	}

	// ForceDelete should succeed on created conversations
	if err := s.ForceDelete(id); err != nil {
		t.Fatalf("ForceDelete failed: %v", err)
	}

	// Verify it's gone
	if s.Get(id) != nil {
		t.Error("expected conversation to be deleted")
	}

	// Verify it doesn't appear in List
	for _, listID := range s.List() {
		if listID == id {
			t.Error("deleted conversation should not appear in List")
		}
	}
}

func TestForceDeleteNotFound(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.ForceDelete("nonexistent"); err == nil {
		t.Error("expected error for nonexistent conversation")
	}
}

func TestForceDeletePersistence(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	// Clone two conversations and mark them both created
	id1, _ := s1.Clone()
	_ = s1.MarkCreated(id1, "shelley-1", "slug-1")
	id2, _ := s1.Clone()
	_ = s1.MarkCreated(id2, "shelley-2", "slug-2")

	// ForceDelete one
	if err := s1.ForceDelete(id1); err != nil {
		t.Fatalf("ForceDelete failed: %v", err)
	}

	// Load into fresh store and verify persistence
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if s2.Get(id1) != nil {
		t.Error("force-deleted conversation should not persist")
	}
	if s2.Get(id2) == nil {
		t.Error("non-deleted conversation should persist")
	}
}

func TestMigrationFromV1(t *testing.T) {
	path := tempStatePath(t)

	// Create a V1 state file
	v1Data := `{
  "conversations": {
    "abc12345": {
      "local_id": "abc12345",
      "shelley_conversation_id": "server-123",
      "slug": "test-slug",
      "model": "predictable",
      "cwd": "/home/user",
      "created": true,
      "created_at": "2024-01-15T10:30:00Z",
      "api_created_at": "2024-01-15T10:30:00Z",
      "api_updated_at": "2024-01-16T14:20:00Z"
    }
  }
}`
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(v1Data), 0644); err != nil {
		t.Fatal(err)
	}

	// Load the store (should trigger migration)
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Verify the data was migrated
	ids := s.List()
	if len(ids) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(ids))
	}

	cs := s.Get(ids[0])
	if cs == nil {
		t.Fatal("expected conversation state")
	}
	if cs.ShelleyConversationID != "server-123" {
		t.Errorf("expected ShelleyConversationID=server-123, got %s", cs.ShelleyConversationID)
	}
	if cs.Slug != "test-slug" {
		t.Errorf("expected Slug=test-slug, got %s", cs.Slug)
	}
	if cs.Model != "predictable" {
		t.Errorf("expected Model=predictable, got %s", cs.Model)
	}
	if cs.Cwd != "/home/user" {
		t.Errorf("expected Cwd=/home/user, got %s", cs.Cwd)
	}
	if !cs.Created {
		t.Error("expected Created=true")
	}

	// Verify the file was rewritten in new format
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var newFormat struct {
		Backends map[string]*BackendState `json:"backends"`
	}
	if err := json.Unmarshal(data, &newFormat); err != nil {
		t.Fatalf("failed to parse new format: %v", err)
	}
	if newFormat.Backends == nil {
		t.Fatal("expected backends map")
	}
	b, ok := newFormat.Backends[mainBackendName]
	if !ok {
		t.Fatalf("expected default backend %q to exist", mainBackendName)
	}
	if b.Conversations == nil {
		t.Fatal("expected conversations map in default backend")
	}
	if len(b.Conversations) != 1 {
		t.Fatalf("expected 1 conversation in default backend, got %d", len(b.Conversations))
	}
}

func TestMigrationFromV1Empty(t *testing.T) {
	path := tempStatePath(t)

	// Create an empty V1 state file
	v1Data := `{
  "conversations": {}
}`
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(v1Data), 0644); err != nil {
		t.Fatal(err)
	}

	// Load the store (should trigger migration)
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Verify the store is empty
	ids := s.List()
	if len(ids) != 0 {
		t.Fatalf("expected 0 conversations, got %d", len(ids))
	}

	// Verify the file was rewritten in new format
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var newFormat struct {
		Backends map[string]*BackendState `json:"backends"`
	}
	if err := json.Unmarshal(data, &newFormat); err != nil {
		t.Fatalf("failed to parse new format: %v", err)
	}
	if newFormat.Backends == nil {
		t.Fatal("expected backends map")
	}
	b, ok := newFormat.Backends[mainBackendName]
	if !ok {
		t.Fatalf("expected default backend %q to exist", mainBackendName)
	}
	if b.Conversations == nil {
		t.Fatal("expected conversations map in default backend")
	}
	if len(b.Conversations) != 0 {
		t.Fatalf("expected 0 conversations in default backend, got %d", len(b.Conversations))
	}
}

func TestNewFormatRoundTrip(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	// Create a conversation
	id, err := s1.Clone()
	if err != nil {
		t.Fatal(err)
	}
	_ = s1.SetModel(id, "predictable", "predictable")
	_ = s1.SetCtl(id, "cwd", "/home/user")
	_ = s1.MarkCreated(id, "shelley-123", "test-slug")

	// Reload into a fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the data is preserved
	cs := s2.Get(id)
	if cs == nil {
		t.Fatal("expected conversation state after reload")
	}
	if cs.Model != "predictable" {
		t.Errorf("expected Model=predictable, got %s", cs.Model)
	}
	if cs.Cwd != "/home/user" {
		t.Errorf("expected Cwd=/home/user, got %s", cs.Cwd)
	}
	if cs.Slug != "test-slug" {
		t.Errorf("expected Slug=test-slug, got %s", cs.Slug)
	}

	// Verify the file is in new format
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var newFormat struct {
		Backends map[string]*BackendState `json:"backends"`
	}
	if err := json.Unmarshal(data, &newFormat); err != nil {
		t.Fatalf("failed to parse new format: %v", err)
	}
	if newFormat.Backends == nil {
		t.Fatal("expected backends map")
	}
	if _, ok := newFormat.Backends[mainBackendName]; !ok {
		t.Fatalf("expected default backend %q to exist", mainBackendName)
	}
}

// Backend CRUD Tests

func TestCreateBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("my-backend", "http://localhost:9999"); err != nil {
		t.Fatalf("CreateBackend failed: %v", err)
	}

	b := s.GetBackend("my-backend")
	if b == nil {
		t.Fatal("expected backend to exist")
	}
	if b.URL != "http://localhost:9999" {
		t.Errorf("expected URL=http://localhost:9999, got %s", b.URL)
	}
	if b.Conversations == nil {
		t.Error("expected Conversations map to be initialized")
	}
}

func TestCreateBackendAlreadyExists(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("my-backend", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("my-backend", "http://other:8080"); err == nil {
		t.Error("expected error when creating backend that already exists")
	}
}

func TestCreateBackendReservedName(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("default", "http://localhost:9999"); err == nil {
		t.Error("expected error when creating backend with reserved name 'default'")
	}

	if err := s.CreateBackend("all", "http://localhost:9999"); err == nil {
		t.Error("expected error when creating backend with reserved name 'all'")
	}
}

func TestCreateBackendPersistence(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := s1.CreateBackend("persisted-backend", "http://localhost:8888"); err != nil {
		t.Fatalf("CreateBackend failed: %v", err)
	}

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	b := s2.GetBackend("persisted-backend")
	if b == nil {
		t.Fatal("expected backend to persist")
	}
	if b.URL != "http://localhost:8888" {
		t.Errorf("expected URL=http://localhost:8888, got %s", b.URL)
	}
}

func TestGetBackendNotFound(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	b := s.GetBackend("nonexistent")
	if b != nil {
		t.Error("expected nil for nonexistent backend")
	}
}

func TestGetBackendDefault(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// The default backend is created lazily when accessed via conversations()
	// The auto-created backend is named "main", not "default" (reserved name)

	// Trigger lazy creation by accessing conversations
	_ = s.conversations()

	b := s.GetBackend("main")
	if b == nil {
		t.Error("expected 'main' backend to exist after lazy creation")
	}
}

func TestDeleteBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("to-delete", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteBackend("to-delete"); err != nil {
		t.Fatalf("DeleteBackend failed: %v", err)
	}

	if s.GetBackend("to-delete") != nil {
		t.Error("expected backend to be deleted")
	}
}

func TestDeleteBackendNotFound(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteBackend("nonexistent"); err == nil {
		t.Error("expected error when deleting nonexistent backend")
	}
}

func TestDeleteBackendRefusesDefault(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Trigger lazy creation of the default backend (named "main")
	_ = s.conversations()

	// Try to delete the default backend (named "main")
	if err := s.DeleteBackend("main"); err == nil {
		t.Error("expected error when deleting default backend")
	}

	// Set a different default and try again
	if err := s.CreateBackend("other", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefaultBackend("other"); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteBackend("other"); err == nil {
		t.Error("expected error when deleting current default backend")
	}
}

func TestDeleteBackendPersistence(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := s1.CreateBackend("persist-delete", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}
	if err := s1.CreateBackend("keep-me", "http://localhost:8888"); err != nil {
		t.Fatal(err)
	}

	if err := s1.DeleteBackend("persist-delete"); err != nil {
		t.Fatalf("DeleteBackend failed: %v", err)
	}

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if s2.GetBackend("persist-delete") != nil {
		t.Error("deleted backend should not persist")
	}
	if s2.GetBackend("keep-me") == nil {
		t.Error("non-deleted backend should persist")
	}
}

func TestRenameBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("old-name", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	if err := s.RenameBackend("old-name", "new-name"); err != nil {
		t.Fatalf("RenameBackend failed: %v", err)
	}

	if s.GetBackend("old-name") != nil {
		t.Error("old name should not exist")
	}
	b := s.GetBackend("new-name")
	if b == nil {
		t.Fatal("new name should exist")
	}
	if b.URL != "http://localhost:9999" {
		t.Errorf("URL should be preserved, got %s", b.URL)
	}
}

func TestRenameBackendNotFound(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RenameBackend("nonexistent", "new-name"); err == nil {
		t.Error("expected error when renaming nonexistent backend")
	}
}

func TestRenameBackendReservedName(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("my-backend", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	if err := s.RenameBackend("my-backend", "default"); err == nil {
		t.Error("expected error when renaming to reserved name 'default'")
	}

	if err := s.RenameBackend("my-backend", "all"); err == nil {
		t.Error("expected error when renaming to reserved name 'all'")
	}
}

func TestRenameBackendAlreadyExists(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("backend1", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBackend("backend2", "http://localhost:8888"); err != nil {
		t.Fatal(err)
	}

	if err := s.RenameBackend("backend1", "backend2"); err == nil {
		t.Error("expected error when renaming to existing name")
	}
}

func TestRenameBackendUpdatesDefault(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Create a backend and make it default
	if err := s.CreateBackend("my-default", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefaultBackend("my-default"); err != nil {
		t.Fatal(err)
	}

	// Rename it
	if err := s.RenameBackend("my-default", "renamed-default"); err != nil {
		t.Fatalf("RenameBackend failed: %v", err)
	}

	// Default should be updated
	if s.GetDefaultBackend() != "renamed-default" {
		t.Errorf("expected default=renamed-default, got %s", s.GetDefaultBackend())
	}
}

func TestRenameBackendPersistence(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := s1.CreateBackend("persist-rename", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	if err := s1.RenameBackend("persist-rename", "after-rename"); err != nil {
		t.Fatalf("RenameBackend failed: %v", err)
	}

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if s2.GetBackend("persist-rename") != nil {
		t.Error("old name should not exist")
	}
	if s2.GetBackend("after-rename") == nil {
		t.Error("new name should exist")
	}
}

func TestSetDefaultBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Default should initially be "main" (the auto-created backend name)
	if s.GetDefaultBackend() != "main" {
		t.Errorf("expected initial default='main', got %s", s.GetDefaultBackend())
	}

	// Create a new backend and make it default
	if err := s.CreateBackend("new-default", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	if err := s.SetDefaultBackend("new-default"); err != nil {
		t.Fatalf("SetDefaultBackend failed: %v", err)
	}

	if s.GetDefaultBackend() != "new-default" {
		t.Errorf("expected default='new-default', got %s", s.GetDefaultBackend())
	}
}

func TestSetDefaultBackendNotFound(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetDefaultBackend("nonexistent"); err == nil {
		t.Error("expected error when setting nonexistent backend as default")
	}
}

func TestSetDefaultBackendPersistence(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := s1.CreateBackend("persist-default", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	if err := s1.SetDefaultBackend("persist-default"); err != nil {
		t.Fatalf("SetDefaultBackend failed: %v", err)
	}

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if s2.GetDefaultBackend() != "persist-default" {
		t.Errorf("expected default='persist-default', got %s", s2.GetDefaultBackend())
	}
}

func TestListBackends(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// ListBackends ensures the default backend exists, so we should see "main"
	backends := s.ListBackends()
	if len(backends) != 1 {
		t.Errorf("expected 1 backend (the default), got %d", len(backends))
	}
	if backends[0] != "main" {
		t.Errorf("expected 'main', got %s", backends[0])
	}

	// Create some backends
	if err := s.CreateBackend("alpha", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBackend("beta", "http://localhost:8888"); err != nil {
		t.Fatal(err)
	}

	backends = s.ListBackends()
	if len(backends) != 3 {
		t.Errorf("expected 3 backends, got %d", len(backends))
	}

	// Should be sorted
	if backends[0] != "alpha" || backends[1] != "beta" || backends[2] != "main" {
		t.Errorf("expected sorted backends [alpha, beta, main], got %v", backends)
	}
}

func TestSetBackendURL(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("my-backend", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	if err := s.SetBackendURL("my-backend", "http://newhost:8080"); err != nil {
		t.Fatalf("SetBackendURL failed: %v", err)
	}

	b := s.GetBackend("my-backend")
	if b.URL != "http://newhost:8080" {
		t.Errorf("expected URL=http://newhost:8080, got %s", b.URL)
	}
}

func TestSetBackendURLNotFound(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetBackendURL("nonexistent", "http://localhost:9999"); err == nil {
		t.Error("expected error when setting URL for nonexistent backend")
	}
}

func TestSetBackendURLPersistence(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := s1.CreateBackend("url-persist", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	if err := s1.SetBackendURL("url-persist", "http://newhost:8080"); err != nil {
		t.Fatalf("SetBackendURL failed: %v", err)
	}

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	b := s2.GetBackend("url-persist")
	if b == nil {
		t.Fatal("expected backend to persist")
	}
	if b.URL != "http://newhost:8080" {
		t.Errorf("expected URL=http://newhost:8080, got %s", b.URL)
	}
}

func TestBackendURLPersistenceOnCreate(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := s1.CreateBackend("url-test", "http://custom-url:1234"); err != nil {
		t.Fatal(err)
	}

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	b := s2.GetBackend("url-test")
	if b == nil {
		t.Fatal("expected backend to exist")
	}
	if b.URL != "http://custom-url:1234" {
		t.Errorf("expected URL=http://custom-url:1234, got %s", b.URL)
	}
}

func TestDefaultBackendURLPersistence(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	// Create a default backend with a URL by setting it explicitly
	// First trigger lazy creation
	_ = s1.conversations()
	// Now set the URL on the "main" backend
	if err := s1.SetBackendURL("main", "http://explicit-main:9999"); err != nil {
		t.Fatalf("SetBackendURL failed: %v", err)
	}

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	b := s2.GetBackend("main")
	if b == nil {
		t.Fatal("expected 'main' backend to exist")
	}
	if b.URL != "http://explicit-main:9999" {
		t.Errorf("expected URL=http://explicit-main:9999, got %s", b.URL)
	}
}

// Tests for *ForBackend methods

func TestCloneForBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Create a backend
	if err := s.CreateBackend("test-backend", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	// Clone on the specific backend
	id, err := s.CloneForBackend("test-backend")
	if err != nil {
		t.Fatalf("CloneForBackend failed: %v", err)
	}

	// Verify the conversation exists on the backend
	cs := s.GetForBackend("test-backend", id)
	if cs == nil {
		t.Fatal("expected conversation on test-backend")
	}

	// Verify it doesn't exist on the default backend
	if s.Get(id) != nil {
		t.Error("conversation should not exist on default backend")
	}
}

func TestCloneForBackendNonexistentBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CloneForBackend("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent backend")
	}
}

func TestGetForBackendNonexistentBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	cs := s.GetForBackend("nonexistent", "some-id")
	if cs != nil {
		t.Error("expected nil for nonexistent backend")
	}
}

func TestListForBackendIsolation(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Create a backend
	if err := s.CreateBackend("backend-a", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	// Clone on default backend
	defaultID, _ := s.Clone()

	// Clone on backend-a
	backendAID, _ := s.CloneForBackend("backend-a")

	// Verify List returns only default conversations
	defaultList := s.List()
	if len(defaultList) != 1 || defaultList[0] != defaultID {
		t.Errorf("List() should only have default conversations, got %v", defaultList)
	}

	// Verify ListForBackend returns only backend-a conversations
	backendAList := s.ListForBackend("backend-a")
	if len(backendAList) != 1 || backendAList[0] != backendAID {
		t.Errorf("ListForBackend(backend-a) should only have backend-a conversations, got %v", backendAList)
	}

	// Verify ListForBackend returns empty for empty backend
	s.CreateBackend("backend-b", "http://localhost:8888")
	backendBList := s.ListForBackend("backend-b")
	if len(backendBList) != 0 {
		t.Errorf("ListForBackend(backend-b) should be empty, got %v", backendBList)
	}
}

func TestListForBackendNonexistentBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	list := s.ListForBackend("nonexistent")
	if list != nil {
		t.Errorf("expected nil for nonexistent backend, got %v", list)
	}
}

func TestConversationIsolationBetweenBackends(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Create two backends
	if err := s.CreateBackend("backend-a", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBackend("backend-b", "http://localhost:8888"); err != nil {
		t.Fatal(err)
	}

	// Create conversations on each backend
	idA, _ := s.CloneForBackend("backend-a")
	idB, _ := s.CloneForBackend("backend-b")
	idDefault, _ := s.Clone()

	// Set model on each
	s.SetModelForBackend("backend-a", idA, "model-a", "model-a")
	s.SetModelForBackend("backend-b", idB, "model-b", "model-b")
	s.SetModel(idDefault, "model-default", "model-default")

	// Mark each as created
	s.MarkCreatedForBackend("backend-a", idA, "shelley-a", "slug-a")
	s.MarkCreatedForBackend("backend-b", idB, "shelley-b", "slug-b")
	s.MarkCreated(idDefault, "shelley-default", "slug-default")

	// Verify isolation: GetForBackend
	if cs := s.GetForBackend("backend-a", idA); cs == nil || cs.Model != "model-a" {
		t.Error("backend-a conversation not found or wrong model")
	}
	if cs := s.GetForBackend("backend-a", idB); cs != nil {
		t.Error("backend-b conversation should not be visible on backend-a")
	}
	if cs := s.GetForBackend("backend-b", idA); cs != nil {
		t.Error("backend-a conversation should not be visible on backend-b")
	}

	// Verify isolation: GetByShelleyIDForBackend
	if got := s.GetByShelleyIDForBackend("backend-a", "shelley-a"); got != idA {
		t.Errorf("GetByShelleyIDForBackend(backend-a, shelley-a) = %q, want %q", got, idA)
	}
	if got := s.GetByShelleyIDForBackend("backend-a", "shelley-b"); got != "" {
		t.Errorf("GetByShelleyIDForBackend(backend-a, shelley-b) = %q, want empty", got)
	}

	// Verify isolation: GetBySlugForBackend
	if got := s.GetBySlugForBackend("backend-b", "slug-b"); got != idB {
		t.Errorf("GetBySlugForBackend(backend-b, slug-b) = %q, want %q", got, idB)
	}
	if got := s.GetBySlugForBackend("backend-b", "slug-a"); got != "" {
		t.Errorf("GetBySlugForBackend(backend-b, slug-a) = %q, want empty", got)
	}
}

func TestSetModelForBackendNonexistentBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	err = s.SetModelForBackend("nonexistent", "some-id", "model", "model")
	if err == nil {
		t.Error("expected error for nonexistent backend")
	}
}

func TestSetCtlForBackendNonexistentBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	err = s.SetCtlForBackend("nonexistent", "some-id", "model", "value")
	if err == nil {
		t.Error("expected error for nonexistent backend")
	}
}

func TestMarkCreatedForBackendNonexistentBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	err = s.MarkCreatedForBackend("nonexistent", "some-id", "shelley-123", "slug")
	if err == nil {
		t.Error("expected error for nonexistent backend")
	}
}

func TestDeleteForBackendNonexistentBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	err = s.DeleteForBackend("nonexistent", "some-id")
	if err == nil {
		t.Error("expected error for nonexistent backend")
	}
}

func TestForceDeleteForBackendNonexistentBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	err = s.ForceDeleteForBackend("nonexistent", "some-id")
	if err == nil {
		t.Error("expected error for nonexistent backend")
	}
}

func TestAdoptForBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("adopt-backend", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	localID, err := s.AdoptForBackend("adopt-backend", "server-conv-123")
	if err != nil {
		t.Fatalf("AdoptForBackend failed: %v", err)
	}

	cs := s.GetForBackend("adopt-backend", localID)
	if cs == nil {
		t.Fatal("expected conversation on adopt-backend")
	}
	if cs.ShelleyConversationID != "server-conv-123" {
		t.Errorf("expected ShelleyConversationID=server-conv-123, got %s", cs.ShelleyConversationID)
	}

	// Should not be visible on default backend
	if s.Get(localID) != nil {
		t.Error("conversation should not exist on default backend")
	}
}

func TestAdoptForBackendNonexistentBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AdoptForBackend("nonexistent", "server-conv-123")
	if err == nil {
		t.Error("expected error for nonexistent backend")
	}
}

func TestAdoptWithSlugForBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("slug-backend", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	localID, err := s.AdoptWithSlugForBackend("slug-backend", "server-conv-456", "my-slug")
	if err != nil {
		t.Fatalf("AdoptWithSlugForBackend failed: %v", err)
	}

	cs := s.GetForBackend("slug-backend", localID)
	if cs == nil {
		t.Fatal("expected conversation")
	}
	if cs.Slug != "my-slug" {
		t.Errorf("expected Slug=my-slug, got %s", cs.Slug)
	}
}

func TestAdoptWithMetadataForBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("meta-backend", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	localID, err := s.AdoptWithMetadataForBackend("meta-backend", "server-789", "test-slug", "2024-01-15T10:30:00Z", "2024-01-16T14:20:00Z", "claude-sonnet-4-5", "/home/user")
	if err != nil {
		t.Fatalf("AdoptWithMetadataForBackend failed: %v", err)
	}

	cs := s.GetForBackend("meta-backend", localID)
	if cs == nil {
		t.Fatal("expected conversation")
	}
	if cs.Slug != "test-slug" {
		t.Errorf("expected Slug=test-slug, got %s", cs.Slug)
	}
	if cs.APICreatedAt != "2024-01-15T10:30:00Z" {
		t.Errorf("expected APICreatedAt, got %s", cs.APICreatedAt)
	}
	if cs.Model != "claude-sonnet-4-5" {
		t.Errorf("expected Model=claude-sonnet-4-5, got %s", cs.Model)
	}
	if cs.Cwd != "/home/user" {
		t.Errorf("expected Cwd=/home/user, got %s", cs.Cwd)
	}
}

func TestAdoptWithMetadataForBackendNonexistentBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AdoptWithMetadataForBackend("nonexistent", "server-789", "slug", "", "", "", "")
	if err == nil {
		t.Error("expected error for nonexistent backend")
	}
}

func TestDeleteForBackendIsolation(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("delete-backend", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	// Create conversations on both backends with same ID (collision)
	idDefault, _ := s.Clone()
	idOther, _ := s.CloneForBackend("delete-backend")

	// Delete from other backend shouldn't affect default
	if err := s.DeleteForBackend("delete-backend", idOther); err != nil {
		t.Fatalf("DeleteForBackend failed: %v", err)
	}

	// Default conversation should still exist
	if s.Get(idDefault) == nil {
		t.Error("default conversation should still exist")
	}

	// Other backend conversation should be gone
	if s.GetForBackend("delete-backend", idOther) != nil {
		t.Error("other backend conversation should be deleted")
	}
}

func TestForceDeleteForBackendIsolation(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("force-del-backend", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	// Create and mark created on both
	idDefault, _ := s.Clone()
	s.MarkCreated(idDefault, "shelley-default", "slug-default")

	idOther, _ := s.CloneForBackend("force-del-backend")
	s.MarkCreatedForBackend("force-del-backend", idOther, "shelley-other", "slug-other")

	// ForceDelete from other backend
	if err := s.ForceDeleteForBackend("force-del-backend", idOther); err != nil {
		t.Fatalf("ForceDeleteForBackend failed: %v", err)
	}

	// Default conversation should still exist
	if s.Get(idDefault) == nil {
		t.Error("default conversation should still exist")
	}
}

func TestListMappingsForBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("mapping-backend", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	// Create on default
	defaultID, _ := s.Clone()
	s.MarkCreated(defaultID, "shelley-default", "slug-default")

	// Create on other backend
	otherID, _ := s.CloneForBackend("mapping-backend")
	s.MarkCreatedForBackend("mapping-backend", otherID, "shelley-other", "slug-other")

	// Verify mappings are isolated
	defaultMappings := s.ListMappings()
	if len(defaultMappings) != 1 {
		t.Errorf("expected 1 mapping on default, got %d", len(defaultMappings))
	}

	otherMappings := s.ListMappingsForBackend("mapping-backend")
	if len(otherMappings) != 1 {
		t.Errorf("expected 1 mapping on other backend, got %d", len(otherMappings))
	}

	// Verify content
	found := false
	for _, m := range otherMappings {
		if m.LocalID == otherID {
			found = true
			if m.ShelleyConversationID != "shelley-other" {
				t.Errorf("wrong ShelleyConversationID: %s", m.ShelleyConversationID)
			}
		}
	}
	if !found {
		t.Error("otherID not found in mappings")
	}
}

func TestListMappingsForBackendNonexistentBackend(t *testing.T) {
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	mappings := s.ListMappingsForBackend("nonexistent")
	if mappings != nil {
		t.Errorf("expected nil for nonexistent backend, got %v", mappings)
	}
}

func TestForBackendMethodsDefaultBackend(t *testing.T) {
	// Verify that methods without ForBackend delegate to default backend
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Create on default backend
	id, _ := s.Clone()

	// Verify Get == GetForBackend(default)
	if s.Get(id) != s.GetForBackend(s.GetDefaultBackend(), id) {
		t.Error("Get should delegate to GetForBackend with default backend")
	}

	// Verify List == ListForBackend(default)
	defaultList := s.List()
	forBackendList := s.ListForBackend(s.GetDefaultBackend())
	if len(defaultList) != len(forBackendList) {
		t.Error("List should delegate to ListForBackend with default backend")
	}

	// Verify ListMappings == ListMappingsForBackend(default)
	defaultMappings := s.ListMappings()
	forBackendMappings := s.ListMappingsForBackend(s.GetDefaultBackend())
	if len(defaultMappings) != len(forBackendMappings) {
		t.Error("ListMappings should delegate to ListMappingsForBackend with default backend")
	}
}

func TestForBackendPersistence(t *testing.T) {
	path := tempStatePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	// Create backends
	if err := s1.CreateBackend("persist-a", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	// Create conversations on different backends
	idA, _ := s1.CloneForBackend("persist-a")
	s1.SetModelForBackend("persist-a", idA, "model-a", "model-a")
	s1.MarkCreatedForBackend("persist-a", idA, "shelley-a", "slug-a")

	idDefault, _ := s1.Clone()
	s1.SetModel(idDefault, "model-default", "model-default")

	// Load into fresh store
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	// Verify backend-a conversation persisted
	csA := s2.GetForBackend("persist-a", idA)
	if csA == nil {
		t.Fatal("expected conversation on persist-a after reload")
	}
	if csA.Model != "model-a" {
		t.Errorf("expected Model=model-a, got %s", csA.Model)
	}
	if csA.ShelleyConversationID != "shelley-a" {
		t.Errorf("expected ShelleyConversationID=shelley-a, got %s", csA.ShelleyConversationID)
	}

	// Verify default conversation persisted
	csDefault := s2.Get(idDefault)
	if csDefault == nil {
		t.Fatal("expected conversation on default after reload")
	}
	if csDefault.Model != "model-default" {
		t.Errorf("expected Model=model-default, got %s", csDefault.Model)
	}
}

func TestSameIDDifferentBackends(t *testing.T) {
	// Verify that the same local ID can exist in different backends
	// (they're isolated by backend)
	s, err := NewStore(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateBackend("backend-x", "http://localhost:9999"); err != nil {
		t.Fatal(err)
	}

	// Create many conversations on each backend until we get a collision
	// (this is probabilistic but with 8 hex chars we should hit one eventually)
	defaultIDs := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, _ := s.Clone()
		defaultIDs[id] = true
	}

	backendXIDs := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, _ := s.CloneForBackend("backend-x")
		backendXIDs[id] = true
	}

	// Mark all as created with different server IDs
	for id := range defaultIDs {
		s.MarkCreated(id, "default-"+id, "default-slug-"+id)
	}
	for id := range backendXIDs {
		s.MarkCreatedForBackend("backend-x", id, "x-"+id, "x-slug-"+id)
	}

	// Verify isolation: each backend's conversations are independent
	for id := range defaultIDs {
		cs := s.Get(id)
		if cs == nil {
			t.Errorf("default backend missing ID %s", id)
		} else if cs.ShelleyConversationID != "default-"+id {
			t.Errorf("wrong server ID for default backend %s: %s", id, cs.ShelleyConversationID)
		}
		// Should not be visible on backend-x
		if s.GetForBackend("backend-x", id) != nil {
			// If the ID exists on both backends, verify they're different conversations
			csX := s.GetForBackend("backend-x", id)
			if csX.ShelleyConversationID == cs.ShelleyConversationID {
				t.Errorf("same ID %s has same server ID on both backends", id)
			}
		}
	}

	for id := range backendXIDs {
		cs := s.GetForBackend("backend-x", id)
		if cs == nil {
			t.Errorf("backend-x missing ID %s", id)
		} else if cs.ShelleyConversationID != "x-"+id {
			t.Errorf("wrong server ID for backend-x %s: %s", id, cs.ShelleyConversationID)
		}
	}
}