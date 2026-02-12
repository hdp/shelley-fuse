package fuse

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"shelley-fuse/mockserver"
	"shelley-fuse/shelley"
)

func TestMessagesDirNodeReaddirWithToolCalls(t *testing.T) {
	// Create mock server that returns conversation with tool calls
	convID := "test-conv-with-tools"
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content": [{"Type": 5, "ID": "tu_123", "ToolName": "bash"}]}`)},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr(`{"Content": [{"Type": 6, "ToolUseID": "tu_123"}]}`)},
		{MessageID: "m4", ConversationID: convID, SequenceID: 4, Type: "shelley", LLMData: strPtr("Done!")},
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create and mark conversation as created
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	node := &MessagesDirNode{
		localID:   localID,
		client:    client,
		state:     store,
		startTime: time.Now(),
	}

	stream, errno := node.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir failed with errno %d", errno)
	}

	// Collect all entries
	var names []string
	for stream.HasNext() {
		entry, _ := stream.Next()
		names = append(names, entry.Name)
	}

	// Expected entries:
	// - Static: all.json, all.md, count, last, since
	// - Message directories: 0-user, 1-bash-tool, 2-bash-result, 3-agent (0-indexed)
	expected := []string{
		"all.json", "all.md", "count", "last", "since",
		"0-user",
		"1-bash-tool",
		"2-bash-result",
		"3-agent",
	}

	namesSet := make(map[string]bool)
	for _, name := range names {
		namesSet[name] = true
	}

	for _, exp := range expected {
		if !namesSet[exp] {
			t.Errorf("Expected file %q not found in Readdir results: %v", exp, names)
		}
	}

	// Verify total count
	if len(names) != len(expected) {
		t.Errorf("Expected %d entries, got %d: %v", len(expected), len(names), names)
	}
}

// TestMessagesDirNodeLookupWithToolCalls verifies that Lookup correctly maps
// tool call/result filenames to their messages via a mounted filesystem.
func TestMessagesDirNodeLookupWithToolCalls(t *testing.T) {
	convID := "test-conv-lookup-tools"
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content": [{"Type": 5, "ID": "tu_456", "ToolName": "patch"}]}`)},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr(`{"Content": [{"Type": 6, "ToolUseID": "tu_456"}]}`)},
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-tool-lookup-test")
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

	msgDir := filepath.Join(tmpDir, "conversation", localID, "messages")

	// Test message directories exist (0-indexed)
	testCases := []struct {
		dirname string
		wantOK  bool
	}{
		{"0-user", true},
		{"1-patch-tool", true},
		{"2-patch-result", true},
		// Wrong slug for index 0 (should be user, not agent)
		{"0-agent", false},
		// Wrong slug for index 1 (should be patch-tool, not bash-tool)
		{"1-bash-tool", false},
		// Non-existent index
		{"99-user", false},
	}

	for _, tc := range testCases {
		t.Run(tc.dirname, func(t *testing.T) {
			info, err := os.Stat(filepath.Join(msgDir, tc.dirname))
			gotOK := err == nil
			if gotOK != tc.wantOK {
				if tc.wantOK {
					t.Errorf("Stat(%q): expected success, got error: %v", tc.dirname, err)
				} else {
					t.Errorf("Stat(%q): expected failure, got success", tc.dirname)
				}
			}
			if gotOK && !info.IsDir() {
				t.Errorf("Stat(%q): expected directory, got file", tc.dirname)
			}
		})
	}

	// Test that content.md exists inside message directories
	for _, dirname := range []string{"0-user", "1-patch-tool", "2-patch-result"} {
		t.Run(dirname+"/content.md", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(msgDir, dirname, "content.md"))
			if err != nil {
				t.Errorf("Stat(%q/content.md): expected success, got error: %v", dirname, err)
			}
		})
	}
}

// TestMessagesDirNodeReadToolCallContent verifies that reading a tool call/result
// message directory returns the correct content.
func TestMessagesDirNodeReadToolCallContent(t *testing.T) {
	convID := "test-conv-read-tools"
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 100, Type: "shelley", LLMData: strPtr(`{"Content": [{"Type": 5, "ID": "tu_789", "ToolName": "bash"}]}`)},
		{MessageID: "m2", ConversationID: convID, SequenceID: 101, Type: "user", UserData: strPtr(`{"Content": [{"Type": 6, "ToolUseID": "tu_789"}]}`)},
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-tool-read-test")
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

	msgDir := filepath.Join(tmpDir, "conversation", localID, "messages")

	// Verify 099-bash-tool directory exists and has correct field files (0-indexed: seqID 100 → index 99)
	// With maxSeqID=101, width=3 (len("100")=3), so 99 is zero-padded to 099
	toolDir := filepath.Join(msgDir, "099-bash-tool")
	info, err := os.Stat(toolDir)
	if err != nil {
		t.Fatalf("Failed to stat 099-bash-tool: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("099-bash-tool should be a directory")
	}

	// Check sequence_id field
	seqID, err := ioutil.ReadFile(filepath.Join(toolDir, "sequence_id"))
	if err != nil {
		t.Fatalf("Failed to read sequence_id: %v", err)
	}
	if string(seqID) != "100\n" {
		t.Errorf("Expected sequence_id=100, got %q", string(seqID))
	}

	// Verify 100-bash-result directory exists and has correct field files (0-indexed: seqID 101 → index 100)
	resultDir := filepath.Join(msgDir, "100-bash-result")
	info, err = os.Stat(resultDir)
	if err != nil {
		t.Fatalf("Failed to stat 100-bash-result: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("100-bash-result should be a directory")
	}

	// Check sequence_id field
	seqID, err = ioutil.ReadFile(filepath.Join(resultDir, "sequence_id"))
	if err != nil {
		t.Fatalf("Failed to read sequence_id: %v", err)
	}
	if string(seqID) != "101\n" {
		t.Errorf("Expected sequence_id=101, got %q", string(seqID))
	}
}

func TestMessageDirNodeFields(t *testing.T) {
	convID := "test-conv-msg-dir-fields"
	llmData := `{"Content":[{"Type":2,"Text":"Hello from LLM"}]}`
	usageData := `{"input_tokens":100,"output_tokens":50}`
	msgs := []shelley.Message{
		{
			MessageID:      "msg-uuid-123",
			ConversationID: convID,
			SequenceID:     1,
			Type:           "user",
			UserData:       strPtr("Hello"),
			CreatedAt:      "2026-01-15T10:00:00Z",
		},
		{
			MessageID:      "msg-uuid-456",
			ConversationID: convID,
			SequenceID:     2,
			Type:           "shelley",
			LLMData:        strPtr(llmData),
			UsageData:      strPtr(usageData),
			CreatedAt:      "2026-01-15T10:05:00Z",
		},
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-msg-dir-fields-test")
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

	msgDir := filepath.Join(tmpDir, "conversation", localID, "messages")

	// Test user message directory (0-user)
	t.Run("UserMessageDir", func(t *testing.T) {
		userDir := filepath.Join(msgDir, "0-user")

		// Verify it's a directory
		info, err := os.Stat(userDir)
		if err != nil {
			t.Fatalf("Failed to stat 0-user: %v", err)
		}
		if !info.IsDir() {
			t.Errorf("0-user should be a directory")
		}

		// Test field files
		tests := []struct {
			field    string
			expected string
		}{
			{"message_id", "msg-uuid-123\n"},
			{"conversation_id", convID + "\n"},
			{"sequence_id", "1\n"},
			{"type", "user\n"},
			{"created_at", "2026-01-15T10:00:00Z\n"},
		}

		for _, tc := range tests {
			data, err := ioutil.ReadFile(filepath.Join(userDir, tc.field))
			if err != nil {
				t.Errorf("Failed to read %s: %v", tc.field, err)
				continue
			}
			if string(data) != tc.expected {
				t.Errorf("%s: expected %q, got %q", tc.field, tc.expected, string(data))
			}
		}

		// Verify content.md exists
		data, err := ioutil.ReadFile(filepath.Join(userDir, "content.md"))
		if err != nil {
			t.Errorf("Failed to read content.md: %v", err)
		}
		if !strings.Contains(string(data), "user") {
			t.Errorf("content.md should contain 'user', got %q", string(data))
		}

		// User message should NOT have llm_data or usage_data
		_, err = os.Stat(filepath.Join(userDir, "llm_data"))
		if !os.IsNotExist(err) {
			t.Errorf("User message should not have llm_data")
		}
		_, err = os.Stat(filepath.Join(userDir, "usage_data"))
		if !os.IsNotExist(err) {
			t.Errorf("User message should not have usage_data")
		}
	})

	// Test agent message directory (1-agent) with llm_data and usage_data
	t.Run("AgentMessageDir", func(t *testing.T) {
		agentDir := filepath.Join(msgDir, "1-agent")

		// Verify field files
		tests := []struct {
			field    string
			expected string
		}{
			{"message_id", "msg-uuid-456\n"},
			{"sequence_id", "2\n"},
			{"type", "shelley\n"},
		}

		for _, tc := range tests {
			data, err := ioutil.ReadFile(filepath.Join(agentDir, tc.field))
			if err != nil {
				t.Errorf("Failed to read %s: %v", tc.field, err)
				continue
			}
			if string(data) != tc.expected {
				t.Errorf("%s: expected %q, got %q", tc.field, tc.expected, string(data))
			}
		}

		// Verify llm_data is a directory (JSON object)
		llmDataPath := filepath.Join(agentDir, "llm_data")
		info, err := os.Stat(llmDataPath)
		if err != nil {
			t.Fatalf("Failed to stat llm_data: %v", err)
		}
		if !info.IsDir() {
			t.Errorf("llm_data should be a directory")
		}

		// Navigate into llm_data/Content/0/Text
		textPath := filepath.Join(llmDataPath, "Content", "0", "Text")
		data, err := ioutil.ReadFile(textPath)
		if err != nil {
			t.Errorf("Failed to read llm_data/Content/0/Text: %v", err)
		} else if strings.TrimSpace(string(data)) != "Hello from LLM" {
			t.Errorf("Expected 'Hello from LLM', got %q", string(data))
		}

		// Verify usage_data is a directory
		usageDataPath := filepath.Join(agentDir, "usage_data")
		info, err = os.Stat(usageDataPath)
		if err != nil {
			t.Fatalf("Failed to stat usage_data: %v", err)
		}
		if !info.IsDir() {
			t.Errorf("usage_data should be a directory")
		}

		// Read usage_data/input_tokens
		data, err = ioutil.ReadFile(filepath.Join(usageDataPath, "input_tokens"))
		if err != nil {
			t.Errorf("Failed to read usage_data/input_tokens: %v", err)
		} else if strings.TrimSpace(string(data)) != "100" {
			t.Errorf("Expected input_tokens=100, got %q", string(data))
		}
	})
}

// TestMessageFieldStableInodes verifies that message field nodes use stable,
// deterministic inode numbers derived from (conversationID, sequenceID, fieldName).
// This allows the kernel to recognize the same logical file across lookups.
func TestMessageFieldStableInodes(t *testing.T) {
	convID := "test-conv-stable-ino"
	msgs := []shelley.Message{
		{
			MessageID:      "msg-uuid-001",
			ConversationID: convID,
			SequenceID:     1,
			Type:           "user",
			UserData:       strPtr("Hello"),
			CreatedAt:      "2026-01-15T10:00:00Z",
		},
		{
			MessageID:      "msg-uuid-002",
			ConversationID: convID,
			SequenceID:     2,
			Type:           "shelley",
			LLMData:        strPtr(`{"Content":[{"Type":2,"Text":"Hi"}]}`),
			CreatedAt:      "2026-01-15T10:01:00Z",
		},
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-stable-ino-test")
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

	msgDir := filepath.Join(tmpDir, "conversation", localID, "messages")

	// Stat each field file twice and verify the inode is stable across lookups.
	userDir := filepath.Join(msgDir, "0-user")
	agentDir := filepath.Join(msgDir, "1-agent")

	fields := []string{"message_id", "conversation_id", "sequence_id", "type", "created_at", "content.md"}

	// Collect inodes from first stat pass
	userInodes := make(map[string]uint64)
	agentInodes := make(map[string]uint64)

	for _, field := range fields {
		info, err := os.Stat(filepath.Join(userDir, field))
		if err != nil {
			t.Fatalf("Stat %s/0-user/%s: %v", localID, field, err)
		}
		userInodes[field] = statIno(info)

		info, err = os.Stat(filepath.Join(agentDir, field))
		if err != nil {
			t.Fatalf("Stat %s/1-agent/%s: %v", localID, field, err)
		}
		agentInodes[field] = statIno(info)
	}

	// Also check llm_data directory inode for agent
	info, err := os.Stat(filepath.Join(agentDir, "llm_data"))
	if err != nil {
		t.Fatalf("Stat llm_data: %v", err)
	}
	agentLLMIno := statIno(info)

	// Verify all inode numbers are non-zero (stable, not auto-assigned 0)
	for field, ino := range userInodes {
		if ino == 0 {
			t.Errorf("user/%s inode should be non-zero", field)
		}
	}
	for field, ino := range agentInodes {
		if ino == 0 {
			t.Errorf("agent/%s inode should be non-zero", field)
		}
	}
	if agentLLMIno == 0 {
		t.Errorf("agent/llm_data inode should be non-zero")
	}

	// Verify same field in different messages gets different inodes
	for _, field := range fields {
		if userInodes[field] == agentInodes[field] {
			t.Errorf("%s: user and agent inodes should differ, both are %d", field, userInodes[field])
		}
	}

	// Verify different fields within the same message get different inodes
	seenUser := make(map[uint64]string)
	for field, ino := range userInodes {
		if prev, ok := seenUser[ino]; ok {
			t.Errorf("inode collision in user message: %s and %s both have ino %d", prev, field, ino)
		}
		seenUser[ino] = field
	}

	// Verify message directory itself has a stable inode
	userDirInfo, err := os.Stat(userDir)
	if err != nil {
		t.Fatalf("Stat user dir: %v", err)
	}
	userDirIno := statIno(userDirInfo)
	if userDirIno == 0 {
		t.Errorf("user message dir inode should be non-zero")
	}

	agentDirInfo, err := os.Stat(agentDir)
	if err != nil {
		t.Fatalf("Stat agent dir: %v", err)
	}
	agentDirIno := statIno(agentDirInfo)
	if agentDirIno == 0 {
		t.Errorf("agent message dir inode should be non-zero")
	}
	if userDirIno == agentDirIno {
		t.Errorf("user and agent message dir inodes should differ")
	}
}

func TestTimestamps_MessageDirUsesCreatedAt(t *testing.T) {
	convID := "conv-msg-time"
	messages := []shelley.Message{
		{
			MessageID:      "msg-1",
			ConversationID: convID,
			SequenceID:     1,
			Type:           "user",
			UserData:       strPtr("Hello"),
			CreatedAt:      "2024-02-20T09:15:30Z",
		},
	}

	server := mockserver.New(mockserver.WithConversation(convID, messages))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create and mark conversation as created
	localID, _ := store.Clone()
	_ = store.MarkCreated(localID, convID, "test-slug")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-msg-time-test")
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

	// Stat the message directory
	msgDirPath := filepath.Join(tmpDir, "conversation", localID, "messages", "0-user")
	var stat syscall.Stat_t
	if err := syscall.Stat(msgDirPath, &stat); err != nil {
		t.Fatalf("syscall.Stat failed: %v", err)
	}

	expectedTime, _ := time.Parse(time.RFC3339, "2024-02-20T09:15:30Z")
	actualMtime := time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec)
	actualCtime := time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec)

	// Both ctime and mtime should be from message's created_at
	if !actualMtime.Equal(expectedTime) {
		t.Errorf("Message dir mtime = %v, want %v", actualMtime, expectedTime)
	}
	if !actualCtime.Equal(expectedTime) {
		t.Errorf("Message dir ctime = %v, want %v", actualCtime, expectedTime)
	}
}

func TestTimestamps_MessagesSubdirUsesConversationMetadata(t *testing.T) {
	convID := "conv-msg-subdir"
	messages := []shelley.Message{}

	server := mockserver.New(mockserver.WithConversation(convID, messages))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	// Create conversation with API metadata
	localID, _ := store.Clone()
	_ = store.MarkCreated(localID, convID, "test-slug")

	// Set API timestamps
	_, _ = store.AdoptWithMetadata(convID, "test-slug", "2024-03-01T10:00:00Z", "2024-03-05T15:00:00Z", "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-msg-subdir-test")
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

	// Stat the messages/ directory
	msgsDirPath := filepath.Join(tmpDir, "conversation", localID, "messages")
	var stat syscall.Stat_t
	if err := syscall.Stat(msgsDirPath, &stat); err != nil {
		t.Fatalf("syscall.Stat failed: %v", err)
	}

	expectedCtime, _ := time.Parse(time.RFC3339, "2024-03-01T10:00:00Z")
	expectedMtime, _ := time.Parse(time.RFC3339, "2024-03-05T15:00:00Z")

	actualCtime := time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec)
	actualMtime := time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec)

	if !actualCtime.Equal(expectedCtime) {
		t.Errorf("messages/ ctime = %v, want %v", actualCtime, expectedCtime)
	}
	if !actualMtime.Equal(expectedMtime) {
		t.Errorf("messages/ mtime = %v, want %v", actualMtime, expectedMtime)
	}
}

func TestTimestamps_MessageFilesUseMessageTime(t *testing.T) {
	// Create messages with different timestamps
	convID := "test-conv-msg-timestamps"
	msg1Time := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	msg2Time := time.Date(2026, 1, 15, 10, 5, 0, 0, time.UTC)  // 5 minutes later
	msg3Time := time.Date(2026, 1, 15, 10, 10, 0, 0, time.UTC) // 10 minutes later

	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("Hello"), CreatedAt: msg1Time.Format(time.RFC3339)},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi there!"), CreatedAt: msg2Time.Format(time.RFC3339)},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr("Thanks"), CreatedAt: msg3Time.Format(time.RFC3339)},
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, 0)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-msg-timestamp-test")
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

	// Test message directories and their content.md files
	testCases := []struct {
		name         string
		path         string // relative to messages/
		expectedTime time.Time
	}{
		{"Message1_Dir", "0-user", msg1Time},
		{"Message1_ContentMD", "0-user/content.md", msg1Time},
		{"Message2_Dir", "1-agent", msg2Time},
		{"Message2_ContentMD", "1-agent/content.md", msg2Time},
		{"Message3_Dir", "2-user", msg3Time},
		{"Message3_ContentMD", "2-user/content.md", msg3Time},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tmpDir, "conversation", localID, "messages", tc.path)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("Failed to stat %s: %v", tc.path, err)
			}
			mtime := info.ModTime()
			diff := mtime.Sub(tc.expectedTime)
			if diff < -time.Second || diff > time.Second {
				t.Errorf("Path %s mtime %v differs from expected %v by %v", tc.path, mtime, tc.expectedTime, diff)
			}
		})
	}

	// Also verify that all.json/all.md still use conversation time (not message time)
	t.Run("AllJsonUsesConvTime", func(t *testing.T) {
		cs := store.Get(localID)
		if cs == nil {
			t.Fatal("Conversation not found")
		}
		convTime := cs.CreatedAt

		info, err := os.Stat(filepath.Join(tmpDir, "conversation", localID, "messages", "all.json"))
		if err != nil {
			t.Fatalf("Failed to stat all.json: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("all.json mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
		// Verify it's NOT using message time
		if mtime.Equal(msg1Time) || mtime.Equal(msg2Time) || mtime.Equal(msg3Time) {
			t.Errorf("all.json should use conversation time, not message time: mtime=%v", mtime)
		}
	})
}

