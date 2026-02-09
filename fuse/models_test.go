package fuse

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"shelley-fuse/shelley"
)

func TestModelsDirNode_Readdir(t *testing.T) {
	models := []shelley.Model{
		{ID: "model-a", Ready: true},
		{ID: "model-b", Ready: false},
		{ID: "model-c", Ready: true},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	node := &ModelsDirNode{client: client}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
		if entry.Mode != fuse.S_IFDIR {
			t.Errorf("expected directory mode for %q", entry.Name)
		}
	}

	sort.Strings(names)
	expected := []string{"model-a", "model-b", "model-c"}
	sort.Strings(expected)

	if len(names) != len(expected) {
		t.Fatalf("expected %d entries, got %d: %v", len(expected), len(names), names)
	}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("entry %d: expected %q, got %q", i, expected[i], name)
		}
	}
}

func TestModelsDirNode_Lookup(t *testing.T) {
	models := []shelley.Model{
		{ID: "existing-model", Ready: true},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-models-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Lookup existing model
	info, err := os.Stat(filepath.Join(tmpDir, "model", "existing-model"))
	if err != nil {
		t.Fatalf("Lookup for existing model should succeed: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}

	// Lookup nonexistent model
	_, err = os.Stat(filepath.Join(tmpDir, "model", "nonexistent-model"))
	if err == nil {
		t.Error("Lookup for nonexistent model should fail")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected ENOENT, got %v", err)
	}
}

func TestModelNode_Readdir(t *testing.T) {
	model := shelley.Model{ID: "test-model", Ready: true}
	node := &ModelNode{model: model}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var entries []fuse.DirEntry
	for stream.HasNext() {
		entry, _ := stream.Next()
		entries = append(entries, entry)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (id, new, ready), got %d", len(entries))
	}

	expectedModes := map[string]uint32{"id": fuse.S_IFREG, "new": fuse.S_IFDIR, "ready": fuse.S_IFREG}
	found := map[string]bool{}
	for _, e := range entries {
		expMode, ok := expectedModes[e.Name]
		if !ok {
			t.Errorf("unexpected entry %q", e.Name)
			continue
		}
		found[e.Name] = true
		if e.Mode != expMode {
			t.Errorf("entry %q: expected mode %d, got %d", e.Name, expMode, e.Mode)
		}
	}
	for name := range expectedModes {
		if !found[name] {
			t.Errorf("expected entry %q not found", name)
		}
	}
}

func TestModelNode_LookupMounted(t *testing.T) {
	models := []shelley.Model{
		{ID: "my-model-id", Ready: true},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-model-lookup-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Test lookup for "id" via stat
	info, err := os.Stat(filepath.Join(tmpDir, "model", "my-model-id", "id"))
	if err != nil {
		t.Fatalf("Lookup for 'id' failed: %v", err)
	}
	if info.IsDir() {
		t.Error("expected file for 'id', got directory")
	}

	// Test lookup for "ready" via stat
	info, err = os.Stat(filepath.Join(tmpDir, "model", "my-model-id", "ready"))
	if err != nil {
		t.Fatalf("Lookup for 'ready' failed: %v", err)
	}
	if info.IsDir() {
		t.Error("expected file for 'ready', got directory")
	}

	// Test lookup for nonexistent field
	_, err = os.Stat(filepath.Join(tmpDir, "model", "my-model-id", "nonexistent"))
	if err == nil {
		t.Error("Lookup for 'nonexistent' should fail")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected ENOENT for nonexistent field, got %v", err)
	}
}

func TestModelFieldNode_Read(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{"id field", "my-model", "my-model\n"},
		{"ready true", "true", "true\n"},
		{"ready false", "false", "false\n"},
		{"empty value", "", "\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := &ModelFieldNode{value: tc.value}
			dest := make([]byte, 1024)
			result, errno := node.Read(context.Background(), nil, dest, 0)
			if errno != 0 {
				t.Fatalf("Read failed with errno %d", errno)
			}
			data, _ := result.Bytes(nil)
			if string(data) != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, string(data))
			}
		})
	}
}

func TestModelFieldNode_ReadOffset(t *testing.T) {
	node := &ModelFieldNode{value: "hello"}

	// Read from offset 2
	dest := make([]byte, 1024)
	result, errno := node.Read(context.Background(), nil, dest, 2)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ := result.Bytes(nil)
	if string(data) != "llo\n" {
		t.Errorf("expected %q, got %q", "llo\n", string(data))
	}

	// Read from offset beyond content
	result, errno = node.Read(context.Background(), nil, dest, 100)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ = result.Bytes(nil)
	if len(data) != 0 {
		t.Errorf("expected empty result for offset beyond content, got %q", string(data))
	}
}

func TestModelNewDirNode_Readdir(t *testing.T) {
	model := shelley.Model{ID: "test-model", Ready: true}
	node := &ModelNewDirNode{model: model}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var entries []fuse.DirEntry
	for stream.HasNext() {
		entry, _ := stream.Next()
		entries = append(entries, entry)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (clone, start), got %d", len(entries))
	}
	expected := map[string]bool{"clone": false, "start": false}
	for _, e := range entries {
		if _, ok := expected[e.Name]; !ok {
			t.Errorf("unexpected entry %q", e.Name)
		} else {
			expected[e.Name] = true
		}
		if e.Mode != fuse.S_IFREG {
			t.Errorf("expected file mode for %q", e.Name)
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("missing expected entry %q", name)
		}
	}
}

func TestModelNewDirNode_LookupMounted(t *testing.T) {
	models := []shelley.Model{
		{ID: "my-model", Ready: true},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-model-new-lookup-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// new/ directory should exist
	info, err := os.Stat(filepath.Join(tmpDir, "model", "my-model", "new"))
	if err != nil {
		t.Fatalf("Stat for 'new' failed: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory for 'new', got file")
	}

	// new/clone should exist
	info, err = os.Stat(filepath.Join(tmpDir, "model", "my-model", "new", "clone"))
	if err != nil {
		t.Fatalf("Stat for 'new/clone' failed: %v", err)
	}
	if info.IsDir() {
		t.Error("expected file for 'new/clone', got directory")
	}

	// new/start should exist and be executable
	info, err = os.Stat(filepath.Join(tmpDir, "model", "my-model", "new", "start"))
	if err != nil {
		t.Fatalf("Stat for 'new/start' failed: %v", err)
	}
	if info.IsDir() {
		t.Error("expected file for 'new/start', got directory")
	}
	if info.Mode()&0111 == 0 {
		t.Error("expected 'new/start' to be executable")
	}

	// nonexistent should fail
	_, err = os.Stat(filepath.Join(tmpDir, "model", "my-model", "new", "nonexistent"))
	if err == nil {
		t.Error("Stat for 'nonexistent' should fail")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected ENOENT for nonexistent, got %v", err)
	}
}

// --- Tests for ModelCloneNode (mounted) ---

func TestModelCloneNode_ReturnsIDWithModelPreconfigured(t *testing.T) {
	models := []shelley.Model{
		{ID: "my-model", Ready: true},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-model-clone-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Read the model clone to get a new conversation ID
	data, err := os.ReadFile(filepath.Join(tmpDir, "model", "my-model", "new", "clone"))
	if err != nil {
		t.Fatalf("Failed to read model clone: %v", err)
	}

	id := strings.TrimSpace(string(data))
	if len(id) != 8 {
		t.Fatalf("expected 8-character hex ID, got %q", id)
	}

	// Verify the model is preconfigured by reading the ctl file
	ctlData, err := os.ReadFile(filepath.Join(tmpDir, "conversation", id, "ctl"))
	if err != nil {
		t.Fatalf("Failed to read ctl: %v", err)
	}

	ctlContent := strings.TrimSpace(string(ctlData))
	if !strings.Contains(ctlContent, "model=my-model") {
		t.Errorf("expected ctl to contain 'model=my-model', got %q", ctlContent)
	}
}

func TestModelCloneNode_UniqueIDs(t *testing.T) {
	models := []shelley.Model{
		{ID: "my-model", Ready: true},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-model-clone-unique-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Read clone twice, should get different IDs
	data1, err := os.ReadFile(filepath.Join(tmpDir, "model", "my-model", "new", "clone"))
	if err != nil {
		t.Fatalf("First clone read failed: %v", err)
	}
	data2, err := os.ReadFile(filepath.Join(tmpDir, "model", "my-model", "new", "clone"))
	if err != nil {
		t.Fatalf("Second clone read failed: %v", err)
	}

	id1 := strings.TrimSpace(string(data1))
	id2 := strings.TrimSpace(string(data2))
	if id1 == id2 {
		t.Errorf("expected unique IDs, both are %q", id1)
	}
}

func TestModelCloneNode_CustomModelName(t *testing.T) {
	// Test with a model where display name differs from internal ID
	models := []shelley.Model{
		{ID: "custom-abc123", DisplayName: "my-custom-model", Ready: true},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-model-clone-custom-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Access via display name
	data, err := os.ReadFile(filepath.Join(tmpDir, "model", "my-custom-model", "new", "clone"))
	if err != nil {
		t.Fatalf("Failed to read model clone: %v", err)
	}

	id := strings.TrimSpace(string(data))

	// Verify model display name is set in ctl
	ctlData, err := os.ReadFile(filepath.Join(tmpDir, "conversation", id, "ctl"))
	if err != nil {
		t.Fatalf("Failed to read ctl: %v", err)
	}
	ctlContent := strings.TrimSpace(string(ctlData))
	if !strings.Contains(ctlContent, "model=my-custom-model") {
		t.Errorf("expected ctl to contain 'model=my-custom-model', got %q", ctlContent)
	}

	// Verify internal model ID is stored in state
	cs := store.Get(id)
	if cs == nil {
		t.Fatalf("conversation %q not found in state", id)
	}
	if cs.ModelID != "custom-abc123" {
		t.Errorf("expected ModelID 'custom-abc123', got %q", cs.ModelID)
	}
	if cs.Model != "my-custom-model" {
		t.Errorf("expected Model 'my-custom-model', got %q", cs.Model)
	}
}

func TestModelsDirNode_MountedReadAndAccess(t *testing.T) {
	models := []shelley.Model{
		{ID: "model-ready", Ready: true},
		{ID: "model-not-ready", Ready: false},
	}
	server := mockModelsServer(t, models)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-models-read-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Test reading model-ready/id
	idData, err := ioutil.ReadFile(filepath.Join(tmpDir, "model", "model-ready", "id"))
	if err != nil {
		t.Fatalf("Failed to read model-ready/id: %v", err)
	}
	if strings.TrimSpace(string(idData)) != "model-ready" {
		t.Errorf("expected 'model-ready', got %q", strings.TrimSpace(string(idData)))
	}

	// Test model-ready/ready exists (presence/absence semantics)
	readyPath := filepath.Join(tmpDir, "model", "model-ready", "ready")
	if _, err := os.Stat(readyPath); err != nil {
		t.Errorf("expected model-ready/ready to exist, got error: %v", err)
	}

	// Test model-not-ready/ready does NOT exist (presence/absence semantics)
	notReadyPath := filepath.Join(tmpDir, "model", "model-not-ready", "ready")
	if _, err := os.Stat(notReadyPath); !os.IsNotExist(err) {
		t.Errorf("expected model-not-ready/ready to not exist (ENOENT), got: %v", err)
	}

	// Test listing models directory
	entries, err := ioutil.ReadDir(filepath.Join(tmpDir, "model"))
	if err != nil {
		t.Fatalf("Failed to read models directory: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 models, got %d", len(entries))
	}

	// Test listing model contents
	entries, err = ioutil.ReadDir(filepath.Join(tmpDir, "model", "model-ready"))
	if err != nil {
		t.Fatalf("Failed to read model-ready directory: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3 entries (id, new, ready), got %d", len(entries))
	}
}

func TestModelsDirNode_ServerError(t *testing.T) {
	server := mockErrorServer(t)
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-models-error-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Reading models directory when server errors should fail
	_, err = ioutil.ReadDir(filepath.Join(tmpDir, "model"))
	if err == nil {
		t.Error("Expected error when reading models directory with server error")
	}
}

func TestModelsDirNode_EmptyModels(t *testing.T) {
	// Server returns empty model list
	server := mockModelsServer(t, []shelley.Model{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	node := &ModelsDirNode{client: client}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	count := 0
	for stream.HasNext() {
		stream.Next()
		count++
	}

	if count != 0 {
		t.Errorf("expected 0 entries for empty model list, got %d", count)
	}
}

func TestModelsDirNode_DefaultSymlink_Readdir(t *testing.T) {
	// When a default model is set, it should appear as a symlink in the directory listing
	models := []shelley.Model{
		{ID: "model-a", Ready: true},
		{ID: "model-b", Ready: false},
	}
	server := mockModelsServerWithDefault(t, models, "model-a")
	defer server.Close()

	client := shelley.NewClient(server.URL)
	node := &ModelsDirNode{client: client}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var dirs, symlinks []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		if entry.Mode&syscall.S_IFLNK != 0 {
			symlinks = append(symlinks, entry.Name)
		} else if entry.Mode == fuse.S_IFDIR {
			dirs = append(dirs, entry.Name)
		}
	}

	// Should have 2 directories (model-a, model-b) and 1 symlink (default)
	if len(dirs) != 2 {
		t.Errorf("expected 2 directories, got %d: %v", len(dirs), dirs)
	}
	if len(symlinks) != 1 {
		t.Fatalf("expected 1 symlink, got %d: %v", len(symlinks), symlinks)
	}
	if symlinks[0] != "default" {
		t.Errorf("expected symlink named 'default', got %q", symlinks[0])
	}
}

func TestModelsDirNode_DefaultSymlink_NoDefault_Readdir(t *testing.T) {
	// When no default model is set, the symlink should NOT appear in the listing
	models := []shelley.Model{
		{ID: "model-a", Ready: true},
		{ID: "model-b", Ready: false},
	}
	server := mockModelsServerWithDefault(t, models, "") // No default
	defer server.Close()

	client := shelley.NewClient(server.URL)
	node := &ModelsDirNode{client: client}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var names []string
	hasSymlink := false
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
		if entry.Mode&syscall.S_IFLNK != 0 {
			hasSymlink = true
		}
	}

	// Should have only directories, no symlink
	if hasSymlink {
		t.Error("unexpected symlink in directory listing when no default model is set")
	}
	if len(names) != 2 {
		t.Errorf("expected 2 entries (models only), got %d: %v", len(names), names)
	}
}

func TestModelsDirNode_DefaultSymlink_Lookup(t *testing.T) {
	// Looking up "default" should return a symlink pointing to the default model
	models := []shelley.Model{
		{ID: "claude-3", Ready: true},
		{ID: "gpt-4", Ready: true},
	}
	server := mockModelsServerWithDefault(t, models, "claude-3")
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-default-symlink-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Check that "default" exists and is a symlink
	defaultPath := filepath.Join(tmpDir, "model", "default")
	fi, err := os.Lstat(defaultPath)
	if err != nil {
		t.Fatalf("Failed to lstat default symlink: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected 'default' to be a symlink, got mode %v", fi.Mode())
	}

	// Verify the symlink target
	target, err := os.Readlink(defaultPath)
	if err != nil {
		t.Fatalf("Failed to readlink: %v", err)
	}
	if target != "claude-3" {
		t.Errorf("expected symlink target 'claude-3', got %q", target)
	}
}

func TestModelsDirNode_DefaultSymlink_NoDefault_Lookup(t *testing.T) {
	// Looking up "default" when no default is set should return ENOENT
	models := []shelley.Model{
		{ID: "model-a", Ready: true},
	}
	server := mockModelsServerWithDefault(t, models, "") // No default
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-no-default-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Check that "default" does NOT exist
	defaultPath := filepath.Join(tmpDir, "model", "default")
	_, err = os.Lstat(defaultPath)
	if err == nil {
		t.Error("expected 'default' to not exist when no default model is set")
	} else if !os.IsNotExist(err) {
		t.Errorf("expected ENOENT error, got: %v", err)
	}
}

func TestModelsDirNode_DefaultSymlink_FollowsToModel(t *testing.T) {
	// Following the default symlink should reach the model directory
	models := []shelley.Model{
		{ID: "target-model", Ready: true},
		{ID: "other-model", Ready: false},
	}
	server := mockModelsServerWithDefault(t, models, "target-model")
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-follow-default-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Follow the symlink and read the id file
	idPath := filepath.Join(tmpDir, "model", "default", "id")
	content, err := ioutil.ReadFile(idPath)
	if err != nil {
		t.Fatalf("Failed to read model/default/id: %v", err)
	}
	if strings.TrimSpace(string(content)) != "target-model" {
		t.Errorf("expected id content 'target-model', got %q", strings.TrimSpace(string(content)))
	}

	// Also check the ready file exists (presence/absence semantics)
	readyPath := filepath.Join(tmpDir, "model", "default", "ready")
	if _, err := os.Stat(readyPath); err != nil {
		t.Errorf("expected model/default/ready to exist, got error: %v", err)
	}
}

func TestModelsDirNode_DefaultSymlink_Getattr(t *testing.T) {
	// Verify that the default symlink has correct attributes
	models := []shelley.Model{
		{ID: "test-model", Ready: true},
	}
	server := mockModelsServerWithDefault(t, models, "test-model")
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	startTime := time.Now()
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-default-getattr-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	defaultPath := filepath.Join(tmpDir, "model", "default")
	fi, err := os.Lstat(defaultPath)
	if err != nil {
		t.Fatalf("Failed to lstat default symlink: %v", err)
	}

	// Verify it's a symlink
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected symlink mode, got %v", fi.Mode())
	}

	// Verify timestamp is reasonable (within a few seconds of startTime)
	mtime := fi.ModTime()
	diff := mtime.Sub(startTime)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("symlink mtime %v differs from startTime %v by %v", mtime, startTime, diff)
	}
}

// --- Tests for ModelStartNode ---

func TestModelStartNode_Read(t *testing.T) {
	node := &ModelStartNode{model: shelley.Model{ID: "test-model"}, startTime: time.Now()}

	result, errno := node.Read(context.Background(), nil, make([]byte, 4096), 0)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}
	data, _ := result.Bytes(make([]byte, 4096))
	script := string(data)

	if !strings.HasPrefix(script, "#!/bin/sh") {
		t.Error("model start script should begin with #!/bin/sh shebang")
	}
	// The model version uses the sibling clone file
	if !strings.Contains(script, "$DIR/clone") {
		t.Error("model start script should reference $DIR/clone")
	}
	if !strings.Contains(script, "/ctl") {
		t.Error("model start script should write to ctl")
	}
	if !strings.Contains(script, "/send") {
		t.Error("model start script should write to send")
	}
}

func TestModelStartNode_Getattr(t *testing.T) {
	node := &ModelStartNode{model: shelley.Model{ID: "test-model"}, startTime: time.Now()}
	var out fuse.AttrOut
	errno := node.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr failed with errno %d", errno)
	}
	if out.Mode&0111 == 0 {
		t.Error("model start script should be executable")
	}
	if out.Size == 0 {
		t.Error("model start script should have non-zero size")
	}
}

func TestMsgFieldIno(t *testing.T) {
	// Same inputs produce same output (deterministic)
	ino1 := msgFieldIno("conv-abc", 1, "message_id")
	ino2 := msgFieldIno("conv-abc", 1, "message_id")
	if ino1 != ino2 {
		t.Errorf("same inputs should produce same inode: %d != %d", ino1, ino2)
	}
	if ino1 == 0 {
		t.Error("inode should be non-zero")
	}

	// Different field names produce different inodes
	ino3 := msgFieldIno("conv-abc", 1, "type")
	if ino1 == ino3 {
		t.Errorf("different fields should produce different inodes: both %d", ino1)
	}

	// Different sequence IDs produce different inodes
	ino4 := msgFieldIno("conv-abc", 2, "message_id")
	if ino1 == ino4 {
		t.Errorf("different seqIDs should produce different inodes: both %d", ino1)
	}

	// Different conversation IDs produce different inodes
	ino5 := msgFieldIno("conv-xyz", 1, "message_id")
	if ino1 == ino5 {
		t.Errorf("different convIDs should produce different inodes: both %d", ino1)
	}
}
