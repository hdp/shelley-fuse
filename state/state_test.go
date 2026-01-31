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
	_ = s.MarkCreated(id, "shelley-123")

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
	if err := s.MarkCreated(id, "shelley-abc"); err != nil {
		t.Fatal(err)
	}

	cs := s.Get(id)
	if !cs.Created {
		t.Error("expected Created=true")
	}
	if cs.ShelleyConversationID != "shelley-abc" {
		t.Errorf("expected ShelleyConversationID=shelley-abc, got %s", cs.ShelleyConversationID)
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
	_ = s1.MarkCreated(id, "shelley-xyz")

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
