package state

import (
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
	if len(s.Conversations) != 0 {
		t.Errorf("expected empty conversations, got %d", len(s.Conversations))
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
