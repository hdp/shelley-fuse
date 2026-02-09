package jsonfs

import (
	"context"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
)

func TestNewNodeFromJSON_Object(t *testing.T) {
	json := `{"name": "foo", "count": 42, "active": true}`
	node, err := NewNodeFromJSON([]byte(json), nil)
	if err != nil {
		t.Fatalf("NewNodeFromJSON failed: %v", err)
	}

	obj, ok := node.(*objectNode)
	if !ok {
		t.Fatalf("expected *objectNode, got %T", node)
	}

	if len(obj.data) != 3 {
		t.Errorf("expected 3 fields, got %d", len(obj.data))
	}
}

func TestNewNodeFromJSON_Array(t *testing.T) {
	json := `[1, 2, 3]`
	node, err := NewNodeFromJSON([]byte(json), nil)
	if err != nil {
		t.Fatalf("NewNodeFromJSON failed: %v", err)
	}

	arr, ok := node.(*arrayNode)
	if !ok {
		t.Fatalf("expected *arrayNode, got %T", node)
	}

	if len(arr.data) != 3 {
		t.Errorf("expected 3 elements, got %d", len(arr.data))
	}
}

func TestNewNodeFromJSON_InvalidJSON(t *testing.T) {
	_, err := NewNodeFromJSON([]byte(`{invalid`), nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestObjectNode_Readdir(t *testing.T) {
	data := map[string]any{
		"name":   "test",
		"count":  float64(42),
		"nested": map[string]any{"x": float64(1)},
	}
	node := &objectNode{data: data, config: &Config{}}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var entries []fuse.DirEntry
	for stream.HasNext() {
		e, _ := stream.Next()
		entries = append(entries, e)
	}

	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}

	// Check entries are sorted
	expected := []string{"count", "name", "nested"}
	for i, e := range entries {
		if e.Name != expected[i] {
			t.Errorf("entry %d: expected %q, got %q", i, expected[i], e.Name)
		}
	}

	// Check modes: count and name are files, nested is directory
	if entries[0].Mode != fuse.S_IFREG {
		t.Errorf("count should be a file")
	}
	if entries[1].Mode != fuse.S_IFREG {
		t.Errorf("name should be a file")
	}
	if entries[2].Mode != fuse.S_IFDIR {
		t.Errorf("nested should be a directory")
	}
}

func TestArrayNode_Readdir(t *testing.T) {
	data := []any{"a", "b", "c"}
	node := &arrayNode{data: data, config: &Config{}}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	var entries []fuse.DirEntry
	for stream.HasNext() {
		e, _ := stream.Next()
		entries = append(entries, e)
	}

	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}

	// Check numeric indices
	for i, e := range entries {
		expectedName := string('0' + byte(i))
		if e.Name != expectedName {
			t.Errorf("entry %d: expected %q, got %q", i, expectedName, e.Name)
		}
		if e.Mode != fuse.S_IFREG {
			t.Errorf("entry %d should be a file", i)
		}
	}
}

func TestValueNode_Read(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{"string", "hello", "hello\n"},
		{"number", "42", "42\n"},
		{"bool", "true", "true\n"},
		{"null", "null", "null\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &valueNode{content: tt.content, config: &Config{}}
			dest := make([]byte, 100)
			result, errno := node.Read(context.Background(), nil, dest, 0)
			if errno != 0 {
				t.Fatalf("Read failed with errno %d", errno)
			}

			data, _ := result.Bytes(nil)
			if string(data) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, string(data))
			}
		})
	}
}

func TestValueNode_ReadOffset(t *testing.T) {
	node := &valueNode{content: "hello", config: &Config{}}
	dest := make([]byte, 100)

	// Read from offset 2
	result, errno := node.Read(context.Background(), nil, dest, 2)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}

	data, _ := result.Bytes(nil)
	if string(data) != "llo\n" {
		t.Errorf("expected %q, got %q", "llo\n", string(data))
	}
}

func TestValueNode_ReadPastEnd(t *testing.T) {
	node := &valueNode{content: "hi", config: &Config{}}
	dest := make([]byte, 100)

	// Read past end
	result, errno := node.Read(context.Background(), nil, dest, 100)
	if errno != 0 {
		t.Fatalf("Read failed with errno %d", errno)
	}

	data, _ := result.Bytes(nil)
	if len(data) != 0 {
		t.Errorf("expected empty data, got %q", string(data))
	}
}

func TestObjectNode_LookupKeyExists(t *testing.T) {
	data := map[string]any{
		"name":   "test",
		"nested": map[string]any{"x": float64(1)},
	}

	// Test that key exists in data (Lookup requires mounted FS, so test data access)
	if _, ok := data["name"]; !ok {
		t.Error("name key should exist")
	}
	if _, ok := data["nonexistent"]; ok {
		t.Error("nonexistent key should not exist")
	}

	// Test newNode creates correct types
	config := &Config{}
	nameNode := newNode(data["name"], "name", config)
	if _, ok := nameNode.(*valueNode); !ok {
		t.Errorf("name should create valueNode, got %T", nameNode)
	}

	nestedNode := newNode(data["nested"], "nested", config)
	if _, ok := nestedNode.(*objectNode); !ok {
		t.Errorf("nested should create objectNode, got %T", nestedNode)
	}
}

func TestArrayNode_IndexAccess(t *testing.T) {
	data := []any{"a", "b", "c"}
	config := &Config{}

	// Test valid index access
	if len(data) != 3 {
		t.Errorf("expected 3 elements")
	}

	// Test newNode creates correct types for array elements
	for i, v := range data {
		node := newNode(v, "", config)
		if vn, ok := node.(*valueNode); !ok {
			t.Errorf("element %d should create valueNode, got %T", i, node)
		} else if vn.content != v.(string) {
			t.Errorf("element %d content mismatch: expected %q, got %q", i, v, vn.content)
		}
	}
}

func TestStringifyFields_Unpack(t *testing.T) {
	config := &Config{
		StringifyFields: []string{"llm_data"},
	}

	// Create an object with a stringified JSON field
	data := map[string]any{
		"id":       "msg-1",
		"llm_data": `{"content": "hello", "tokens": 5}`,
	}
	node := &objectNode{data: data, config: config}

	// Readdir should show llm_data as directory (unpacked)
	stream, _ := node.Readdir(context.Background())
	var entries []fuse.DirEntry
	for stream.HasNext() {
		e, _ := stream.Next()
		entries = append(entries, e)
	}

	// Find llm_data entry
	var llmDataEntry *fuse.DirEntry
	for i := range entries {
		if entries[i].Name == "llm_data" {
			llmDataEntry = &entries[i]
			break
		}
	}

	if llmDataEntry == nil {
		t.Fatal("llm_data entry not found")
	}

	if llmDataEntry.Mode != fuse.S_IFDIR {
		t.Errorf("llm_data should be a directory when unpacked, got mode %o", llmDataEntry.Mode)
	}
}

func TestStringifyFields_NoUnpackWhenNotConfigured(t *testing.T) {
	config := &Config{} // No stringify fields

	data := map[string]any{
		"llm_data": `{"content": "hello"}`,
	}
	node := &objectNode{data: data, config: config}

	stream, _ := node.Readdir(context.Background())
	var entries []fuse.DirEntry
	for stream.HasNext() {
		e, _ := stream.Next()
		entries = append(entries, e)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	// Should be a file (not unpacked)
	if entries[0].Mode != fuse.S_IFREG {
		t.Errorf("llm_data should be a file when not unpacked, got mode %o", entries[0].Mode)
	}
}

func TestStringifyFields_InvalidJSON(t *testing.T) {
	config := &Config{
		StringifyFields: []string{"bad_json"},
	}

	data := map[string]any{
		"bad_json": `{invalid json}`,
	}
	node := &objectNode{data: data, config: config}

	stream, _ := node.Readdir(context.Background())
	var entries []fuse.DirEntry
	for stream.HasNext() {
		e, _ := stream.Next()
		entries = append(entries, e)
	}

	// Should fall back to file since JSON is invalid
	if entries[0].Mode != fuse.S_IFREG {
		t.Errorf("bad_json should be a file when JSON is invalid, got mode %o", entries[0].Mode)
	}
}

func TestNumberFormatting(t *testing.T) {
	tests := []struct {
		value    float64
		expected string
	}{
		{42, "42"},
		{0, "0"},
		{-1, "-1"},
		{3.14, "3.14"},
		{1000000, "1000000"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			node := newNode(tt.value, "", &Config{})
			vn, ok := node.(*valueNode)
			if !ok {
				t.Fatalf("expected *valueNode, got %T", node)
			}
			if vn.content != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, vn.content)
			}
		})
	}
}

func TestGetattr_Timestamps(t *testing.T) {
	startTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	config := &Config{StartTime: startTime}

	node := &valueNode{content: "test", config: config}
	var out fuse.AttrOut
	errno := node.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr failed with errno %d", errno)
	}

	if out.Attr.Mtime != uint64(startTime.Unix()) {
		t.Errorf("mtime mismatch: expected %d, got %d", startTime.Unix(), out.Attr.Mtime)
	}
}

func TestNestedStructure(t *testing.T) {
	// Test the example from the ticket
	json := `{"name": "foo", "count": 42, "nested": {"x": 1}}`
	node, err := NewNodeFromJSON([]byte(json), nil)
	if err != nil {
		t.Fatalf("NewNodeFromJSON failed: %v", err)
	}

	obj := node.(*objectNode)

	// Check name is a file
	nameNode := newNode(obj.data["name"], "name", nil)
	if _, ok := nameNode.(*valueNode); !ok {
		t.Errorf("name should be a valueNode")
	}

	// Check count is a file
	countNode := newNode(obj.data["count"], "count", nil)
	if _, ok := countNode.(*valueNode); !ok {
		t.Errorf("count should be a valueNode")
	}

	// Check nested is a directory
	nestedNode := newNode(obj.data["nested"], "nested", nil)
	if _, ok := nestedNode.(*objectNode); !ok {
		t.Errorf("nested should be an objectNode")
	}
}

func TestNilConfig(t *testing.T) {
	// Should not panic with nil config
	node, err := NewNodeFromJSON([]byte(`{"a": 1}`), nil)
	if err != nil {
		t.Fatalf("NewNodeFromJSON failed: %v", err)
	}

	obj := node.(*objectNode)
	stream, errno := obj.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	for stream.HasNext() {
		stream.Next()
	}
}

func TestCacheTimeout_Getattr(t *testing.T) {
	cacheTTL := 1 * time.Hour
	config := &Config{
		StartTime:    time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
		CacheTimeout: cacheTTL,
	}

	// Test objectNode Getattr sets cache timeout
	node, err := NewNodeFromJSON([]byte(`{"key": "value"}`), config)
	if err != nil {
		t.Fatal(err)
	}
	obj := node.(*objectNode)
	var out fuse.AttrOut
	if errno := obj.Getattr(context.Background(), nil, &out); errno != 0 {
		t.Fatalf("Getattr failed: %d", errno)
	}
	if got := out.Timeout(); got != cacheTTL {
		t.Errorf("objectNode attr timeout = %v, want %v", got, cacheTTL)
	}

	// Test valueNode Getattr sets cache timeout
	vn := &valueNode{content: "hello", config: config}
	out = fuse.AttrOut{}
	if errno := vn.Getattr(context.Background(), nil, &out); errno != 0 {
		t.Fatalf("Getattr failed: %d", errno)
	}
	if got := out.Timeout(); got != cacheTTL {
		t.Errorf("valueNode attr timeout = %v, want %v", got, cacheTTL)
	}
}

func TestCacheTimeout_Zero(t *testing.T) {
	// With zero CacheTimeout, no per-node timeouts should be set
	config := &Config{
		StartTime: time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
	}

	vn := &valueNode{content: "hello", config: config}
	var out fuse.AttrOut
	if errno := vn.Getattr(context.Background(), nil, &out); errno != 0 {
		t.Fatalf("Getattr failed: %d", errno)
	}
	if got := out.Timeout(); got != 0 {
		t.Errorf("valueNode attr timeout = %v, want 0 (no caching)", got)
	}
}

func TestCacheTimeout_ValueNodeOpen_DirectIO(t *testing.T) {
	// Zero CacheTimeout should use FOPEN_DIRECT_IO
	node := &valueNode{content: "test", config: &Config{}}
	_, flags, errno := node.Open(context.Background(), 0)
	if errno != 0 {
		t.Fatalf("Open failed with errno %d", errno)
	}
	if flags != fuse.FOPEN_DIRECT_IO {
		t.Errorf("expected FOPEN_DIRECT_IO (%d), got %d", fuse.FOPEN_DIRECT_IO, flags)
	}
}

func TestCacheTimeout_ValueNodeOpen_KeepCache(t *testing.T) {
	// Positive CacheTimeout should use FOPEN_KEEP_CACHE
	node := &valueNode{content: "test", config: &Config{CacheTimeout: time.Hour}}
	_, flags, errno := node.Open(context.Background(), 0)
	if errno != 0 {
		t.Fatalf("Open failed with errno %d", errno)
	}
	if flags != fuse.FOPEN_KEEP_CACHE {
		t.Errorf("expected FOPEN_KEEP_CACHE (%d), got %d", fuse.FOPEN_KEEP_CACHE, flags)
	}
}

func TestCacheTimeout_ValueNodeOpen_NilConfig(t *testing.T) {
	// Nil config should use FOPEN_DIRECT_IO
	node := &valueNode{content: "test", config: nil}
	_, flags, errno := node.Open(context.Background(), 0)
	if errno != 0 {
		t.Fatalf("Open failed with errno %d", errno)
	}
	if flags != fuse.FOPEN_DIRECT_IO {
		t.Errorf("expected FOPEN_DIRECT_IO (%d), got %d", fuse.FOPEN_DIRECT_IO, flags)
	}
}
