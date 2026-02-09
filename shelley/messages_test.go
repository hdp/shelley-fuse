package shelley

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func strPtr(s string) *string { return &s }

var sampleMessages = []Message{
	{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
	{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi there!")},
	{MessageID: "m3", ConversationID: "c1", SequenceID: 3, Type: "user", UserData: strPtr("How are you?")},
	{MessageID: "m4", ConversationID: "c1", SequenceID: 4, Type: "shelley", LLMData: strPtr("I'm doing well.")},
	{MessageID: "m5", ConversationID: "c1", SequenceID: 5, Type: "user", UserData: strPtr("Great")},
}

func sampleConversationJSON() []byte {
	data, _ := json.Marshal(struct {
		Messages []Message `json:"messages"`
	}{Messages: sampleMessages})
	return data
}

func TestParseMessages(t *testing.T) {
	msgs, err := ParseMessages(sampleConversationJSON())
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}
	if msgs[0].Type != "user" {
		t.Errorf("expected first message type=user, got %s", msgs[0].Type)
	}
}

func TestParseMessagesInvalid(t *testing.T) {
	_, err := ParseMessages([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestFormatJSON(t *testing.T) {
	data, err := FormatJSON(sampleMessages[:2])
	if err != nil {
		t.Fatal(err)
	}
	var result []Message
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 messages in JSON, got %d", len(result))
	}
}

func TestFormatMarkdown(t *testing.T) {
	md := string(FormatMarkdown(sampleMessages[:2]))
	if !strings.Contains(md, "## user") {
		t.Error("expected markdown to contain '## user'")
	}
	if !strings.Contains(md, "Hello") {
		t.Error("expected markdown to contain 'Hello'")
	}
	if !strings.Contains(md, "## agent") {
		t.Error("expected markdown to contain '## agent'")
	}
	if !strings.Contains(md, "Hi there!") {
		t.Error("expected markdown to contain 'Hi there!'")
	}
}

func TestGetMessage(t *testing.T) {
	m := GetMessage(sampleMessages, 3)
	if m == nil {
		t.Fatal("expected message at sequence 3")
	}
	if *m.UserData != "How are you?" {
		t.Errorf("expected 'How are you?', got %s", *m.UserData)
	}
}

func TestGetMessageNotFound(t *testing.T) {
	m := GetMessage(sampleMessages, 99)
	if m != nil {
		t.Error("expected nil for nonexistent sequence")
	}
}

func TestFilterLast(t *testing.T) {
	result := FilterLast(sampleMessages, 2)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
	if result[0].SequenceID != 4 {
		t.Errorf("expected seq 4, got %d", result[0].SequenceID)
	}
	if result[1].SequenceID != 5 {
		t.Errorf("expected seq 5, got %d", result[1].SequenceID)
	}
}

func TestFilterLastMoreThanAvailable(t *testing.T) {
	result := FilterLast(sampleMessages, 100)
	if len(result) != 5 {
		t.Errorf("expected all 5, got %d", len(result))
	}
}

func TestFilterLastZero(t *testing.T) {
	result := FilterLast(sampleMessages, 0)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestFilterSince(t *testing.T) {
	// Messages AFTER the 2nd-to-last user message (seq 3: "How are you?")
	// seq 3 is excluded, should return seq 4 and 5
	result := FilterSince(sampleMessages, "user", 2)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (seq 4,5), got %d", len(result))
	}
	if result[0].SequenceID != 4 {
		t.Errorf("expected first message seq=4, got %d", result[0].SequenceID)
	}
}

func TestFilterSinceLastFromPerson(t *testing.T) {
	// Messages AFTER the last agent message (seq 4)
	// seq 4 is excluded, should return only seq 5
	result := FilterSince(sampleMessages, "agent", 1)
	if len(result) != 1 {
		t.Fatalf("expected 1 message (seq 5), got %d", len(result))
	}
	if result[0].SequenceID != 5 {
		t.Errorf("expected first message seq=5, got %d", result[0].SequenceID)
	}
}

func TestFilterSinceNotFound(t *testing.T) {
	result := FilterSince(sampleMessages, "nobody", 1)
	if result != nil {
		t.Error("expected nil for unknown person")
	}
}

func TestFilterSinceNTooLarge(t *testing.T) {
	result := FilterSince(sampleMessages, "user", 100)
	if result != nil {
		t.Error("expected nil when n exceeds occurrences")
	}
}

func TestFilterFrom(t *testing.T) {
	// 1st (most recent) agent message
	m := FilterFrom(sampleMessages, "agent", 1)
	if m == nil {
		t.Fatal("expected a message")
	}
	if *m.LLMData != "I'm doing well." {
		t.Errorf("expected 'I'm doing well.', got %s", *m.LLMData)
	}
}

func TestFilterFromSecond(t *testing.T) {
	// 2nd most recent agent message
	m := FilterFrom(sampleMessages, "agent", 2)
	if m == nil {
		t.Fatal("expected a message")
	}
	if *m.LLMData != "Hi there!" {
		t.Errorf("expected 'Hi there!', got %s", *m.LLMData)
	}
}

func TestFilterFromNotFound(t *testing.T) {
	m := FilterFrom(sampleMessages, "nobody", 1)
	if m != nil {
		t.Error("expected nil for unknown person")
	}
}

func TestFilterFromCaseInsensitive(t *testing.T) {
	m := FilterFrom(sampleMessages, "Agent", 1)
	if m == nil {
		t.Fatal("expected case-insensitive match")
	}
}

func TestFilterSinceCaseInsensitive(t *testing.T) {
	result := FilterSince(sampleMessages, "User", 1)
	if result == nil {
		t.Fatal("expected case-insensitive match")
	}
}
func TestExtractTextContentPlain(t *testing.T) {
	content := extractTextContent("Hello, world!")
	if content != "Hello, world!" {
		t.Errorf("Expected plain text to pass through, got %q", content)
	}
}

func TestExtractTextContentEmpty(t *testing.T) {
	content := extractTextContent("")
	if content != "" {
		t.Errorf("Expected empty string to remain empty, got %q", content)
	}
}

func TestExtractTextContentJSONWithContent(t *testing.T) {
	jsonStr := `{"Content": "Hi there! How can I help?"}`
	content := extractTextContent(jsonStr)
	if content != "Hi there! How can I help?" {
		t.Errorf("Expected to extract content from JSON, got %q", content)
	}
}

func TestExtractTextContentJSONWithLowercaseContent(t *testing.T) {
	jsonStr := `{"content": "Hi there! How can I help?"}`
	content := extractTextContent(jsonStr)
	if content != "Hi there! How can I help?" {
		t.Errorf("Expected to extract content from lowercase JSON, got %q", content)
	}
}

func TestExtractTextContentJSONWithContentArray(t *testing.T) {
	jsonStr := `{"Content": [{"Text": "Hi there!"}, {"Text": " How can I help?"}]}`
	content := extractTextContent(jsonStr)
	if content != "Hi there! How can I help?" {
		t.Errorf("Expected to extract from content array, got %q", content)
	}
}

func TestExtractTextContentJSONArray(t *testing.T) {
	jsonStr := `[{"Content": "First part"}, {"Content": " second part"}]`
	content := extractTextContent(jsonStr)
	if content != "First part second part" {
		t.Errorf("Expected to extract from JSON array, got %q", content)
	}
}

func TestExtractTextContentMalformedJSON(t *testing.T) {
	jsonStr := `{"Content": "incomplete`
	content := extractTextContent(jsonStr)
	if content != jsonStr {
		t.Errorf("Expected malformed JSON to pass through, got %q", content)
	}
}

func TestExtractTextContentJSONWithoutContent(t *testing.T) {
	jsonStr := `{"other": "value"}`
	content := extractTextContent(jsonStr)
	// Should return indented JSON for readability
	expected := "{\n  \"other\": \"value\"\n}"
	if content != expected {
		t.Errorf("Expected JSON without content to return indented JSON, got %q", content)
	}
}

func TestFormatMarkdownWithJSON(t *testing.T) {
	messages := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "shelley", LLMData: strPtr(`{"Content": "Hi there! How can I help?"}`)},
	}

	md := string(FormatMarkdown(messages))
	if !strings.Contains(md, "## user") {
		t.Error("expected markdown to contain '## user'")
	}
	if !strings.Contains(md, "Hello") {
		t.Error("expected markdown to contain 'Hello'")
	}
	if !strings.Contains(md, "## agent") {
		t.Error("expected markdown to contain '## agent'")
	}
	if !strings.Contains(md, "Hi there! How can I help?") {
		t.Error("expected markdown to contain extracted text 'Hi there! How can I help?'")
	}
	// Should NOT contain the raw JSON
	if strings.Contains(md, `{"Content": "Hi there! How can I help?"}`) {
		t.Error("markdown should not contain raw JSON")
	}
}

func TestFormatMarkdownMixedContent(t *testing.T) {
	messages := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "shelley", LLMData: strPtr("Plain text response")},
		{MessageID: "m3", ConversationID: "c1", SequenceID: 3, Type: "user", UserData: strPtr(`{"Content": "User message with JSON"}`)},
		{MessageID: "m4", ConversationID: "c1", SequenceID: 4, Type: "shelley", LLMData: strPtr(`{"Content": [{"Text": "Complex "}, {"Text": "response"}]}`)},
	}

	md := string(FormatMarkdown(messages))

	// Check that plain text passes through
	if !strings.Contains(md, "Plain text response") {
		t.Error("expected plain text to pass through")
	}

	// Check that user JSON is also extracted
	if !strings.Contains(md, "User message with JSON") {
		t.Error("expected user JSON to be extracted")
	}

	// Check that complex JSON is extracted
	if !strings.Contains(md, "Complex response") {
		t.Error("expected complex JSON to be extracted")
	}

	// Should NOT contain any raw JSON
	if strings.Contains(md, `{"Content":`) {
		t.Error("markdown should not contain raw JSON")
	}
}

func TestMessageContentNil(t *testing.T) {
	msg := Message{MessageID: "m1", SequenceID: 1, Type: "user"}
	content := messageContent(msg)
	if content != "" {
		t.Errorf("expected empty content for nil UserData/LLMData, got %q", content)
	}
}

func TestMessageContentEmptyString(t *testing.T) {
	msg := Message{MessageID: "m1", SequenceID: 1, Type: "user", UserData: strPtr("")}
	content := messageContent(msg)
	if content != "" {
		t.Errorf("expected empty content for empty string, got %q", content)
	}
}

// Test data for tool messages
func makeToolUseMessage(toolUseID, toolName string) *Message {
	// Note: The Shelley API uses 'ID' field for tool use identifier in tool_use messages
	content := fmt.Sprintf(`{"Content": [{"Type": 5, "ID": %q, "ToolName": %q}]}`, toolUseID, toolName)
	return &Message{
		MessageID:      "m-tool-use",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(content),
	}
}

func makeToolResultMessage(toolUseID string) *Message {
	content := fmt.Sprintf(`{"Content": [{"Type": 6, "ToolUseID": %q}]}`, toolUseID)
	return &Message{
		MessageID:      "m-tool-result",
		ConversationID: "c1",
		SequenceID:     2,
		Type:           "user",
		UserData:       strPtr(content),
	}
}

func TestBuildToolNameMap(t *testing.T) {
	messages := []*Message{
		makeToolUseMessage("tu_123", "bash"),
		makeToolResultMessage("tu_123"),
		makeToolUseMessage("tu_456", "patch"),
	}

	toolMap := BuildToolNameMap(messages)

	if len(toolMap) != 2 {
		t.Fatalf("expected 2 entries in tool map, got %d", len(toolMap))
	}

	if toolMap["tu_123"] != "bash" {
		t.Errorf("expected toolMap['tu_123']='bash', got %q", toolMap["tu_123"])
	}

	if toolMap["tu_456"] != "patch" {
		t.Errorf("expected toolMap['tu_456']='patch', got %q", toolMap["tu_456"])
	}
}

func TestBuildToolNameMapEmpty(t *testing.T) {
	toolMap := BuildToolNameMap([]*Message{})
	if len(toolMap) != 0 {
		t.Errorf("expected empty tool map, got %d entries", len(toolMap))
	}
}

func TestBuildToolNameMapNilMessages(t *testing.T) {
	toolMap := BuildToolNameMap([]*Message{nil, nil})
	if len(toolMap) != 0 {
		t.Errorf("expected empty tool map for nil messages, got %d entries", len(toolMap))
	}
}

func TestMessageSlugToolUse(t *testing.T) {
	msg := makeToolUseMessage("tu_123", "bash")
	slug := MessageSlug(msg, nil)

	if slug != "bash-tool" {
		t.Errorf("expected 'bash-tool', got %q", slug)
	}
}

func TestMessageSlugToolUsePatch(t *testing.T) {
	msg := makeToolUseMessage("tu_456", "Patch")
	slug := MessageSlug(msg, nil)

	// Should be lowercased
	if slug != "patch-tool" {
		t.Errorf("expected 'patch-tool', got %q", slug)
	}
}

func TestMessageSlugToolResult(t *testing.T) {
	messages := []*Message{
		makeToolUseMessage("tu_123", "bash"),
		makeToolResultMessage("tu_123"),
	}

	toolMap := BuildToolNameMap(messages)
	slug := MessageSlug(messages[1], toolMap)

	if slug != "bash-result" {
		t.Errorf("expected 'bash-result', got %q", slug)
	}
}

func TestMessageSlugToolResultUnknown(t *testing.T) {
	// Tool result with no matching tool_use in the map
	// Returns generic "tool-result" to avoid misidentifying as "user"
	msg := makeToolResultMessage("tu_unknown")
	slug := MessageSlug(msg, map[string]string{})

	// Should return generic "tool-result", NOT "user" (which would break FilterSince)
	if slug != "tool-result" {
		t.Errorf("expected 'tool-result', got %q", slug)
	}
}

func TestMessageSlugToolResultWithDirectToolName(t *testing.T) {
	// Tool result with ToolName populated directly (fallback when toolMap lookup fails)
	content := `{"Content": [{"Type": 6, "ToolUseID": "tu_xyz", "ToolName": "patch"}]}`
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "user",
		UserData:       strPtr(content),
	}
	// Empty toolMap - will use ToolName from the content item itself
	slug := MessageSlug(msg, map[string]string{})

	if slug != "patch-result" {
		t.Errorf("expected 'patch-result', got %q", slug)
	}
}

func TestMessageSlugRegularUser(t *testing.T) {
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "user",
		UserData:       strPtr("Hello!"),
	}

	slug := MessageSlug(msg, nil)
	if slug != "user" {
		t.Errorf("expected 'user', got %q", slug)
	}
}

func TestMessageSlugRegularShelley(t *testing.T) {
	// Internal Type is "shelley" but user-facing slug should be "agent"
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr("Hello!"),
	}

	slug := MessageSlug(msg, nil)
	if slug != "agent" {
		t.Errorf("expected 'agent', got %q", slug)
	}
}

func TestMessageSlugRegularAssistant(t *testing.T) {
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "Assistant",
		LLMData:        strPtr("Hello!"),
	}

	slug := MessageSlug(msg, nil)
	// Should be lowercased
	if slug != "assistant" {
		t.Errorf("expected 'assistant', got %q", slug)
	}
}

func TestMessageSlugNilMessage(t *testing.T) {
	slug := MessageSlug(nil, nil)
	if slug != "unknown" {
		t.Errorf("expected 'unknown', got %q", slug)
	}
}

func TestMessageSlugEmptyContent(t *testing.T) {
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "system",
	}

	slug := MessageSlug(msg, nil)
	if slug != "system" {
		t.Errorf("expected 'system', got %q", slug)
	}
}

func TestMessageSlugInvalidJSON(t *testing.T) {
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "user",
		UserData:       strPtr("not valid json"),
	}

	slug := MessageSlug(msg, nil)
	// Should fall back to message type
	if slug != "user" {
		t.Errorf("expected 'user', got %q", slug)
	}
}

// Tests for FormatMarkdown with tool calls and tool results

func makeToolUseMessageWithInput(toolUseID, toolName, input string) *Message {
	// Note: The Shelley API uses 'ID' field for tool use identifier in tool_use messages
	content := fmt.Sprintf(`{"Content": [{"Type": 5, "ID": %q, "ToolName": %q, "ToolInput": %s}]}`, toolUseID, toolName, input)
	return &Message{
		MessageID:      "m-tool-use",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(content),
	}
}

func makeToolResultMessageWithOutput(toolUseID, outputText string) *Message {
	content := fmt.Sprintf(`{"Content": [{"Type": 6, "ToolUseID": %q, "ToolResult": [{"Text": %q}]}]}`, toolUseID, outputText)
	return &Message{
		MessageID:      "m-tool-result",
		ConversationID: "c1",
		SequenceID:     2,
		Type:           "user",
		UserData:       strPtr(content),
	}
}

func TestFormatMarkdownToolCall(t *testing.T) {
	msg := makeToolUseMessageWithInput("tu_123", "bash", `{"command": "ls -la"}`)
	messages := []Message{*msg}

	md := string(FormatMarkdown(messages))

	// Header should include tool name: "## tool call: bash"
	if !strings.Contains(md, "## tool call: bash") {
		t.Errorf("expected header '## tool call: bash', got: %s", md)
	}

	// Should NOT have the raw message type
	if strings.Contains(md, "## agent") {
		t.Error("tool call should not show '## agent'")
	}

	// Body should show the input arguments as key: value format
	if !strings.Contains(md, "command: ls -la") {
		t.Errorf("expected 'command: ls -la' in markdown body, got:\n%s", md)
	}

	// Body should NOT show just the tool name without arguments
	// (This was the bug - body showed "bash" instead of arguments)
	lines := strings.Split(md, "\n")
	for i, line := range lines {
		// Skip header line
		if strings.HasPrefix(line, "## ") {
			continue
		}
		// Body line should not be just "bash" (the old buggy behavior)
		if strings.TrimSpace(line) == "bash" {
			t.Errorf("line %d should not be just 'bash' - body should show arguments, not tool name", i)
		}
	}
}

func TestFormatMarkdownToolCallEmptyInput(t *testing.T) {
	// Test that tool call with empty input shows empty body, not tool name
	content := `{"Content": [{"Type": 5, "ID": "tu_456", "ToolName": "think"}]}`
	msg := &Message{
		MessageID:      "m-tool-use",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(content),
	}

	md := string(FormatMarkdown([]Message{*msg}))

	// Header should include tool name
	if !strings.Contains(md, "## tool call: think") {
		t.Errorf("expected header '## tool call: think', got: %s", md)
	}

	// Body should be empty (no arguments to show)
	// Old bug: body would show "think\n\n" instead of being empty
	lines := strings.Split(md, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			continue
		}
		if strings.TrimSpace(line) == "think" {
			t.Errorf("body should not contain just 'think' - empty input means empty body")
		}
	}
}

func TestFormatMarkdownToolResult(t *testing.T) {
	msg := makeToolResultMessageWithOutput("tu_123", "file1.txt\nfile2.txt\n")
	messages := []Message{*msg}

	md := string(FormatMarkdown(messages))

	// Should have "## tool result" header
	if !strings.Contains(md, "## tool result") {
		t.Error("expected markdown to contain '## tool result'")
	}

	// Should NOT have the raw message type
	if strings.Contains(md, "## user") {
		t.Error("tool result should not show '## user'")
	}

	// Should show the output text
	if !strings.Contains(md, "file1.txt") {
		t.Error("expected markdown to contain output text")
	}
}

func TestFormatMarkdownMixedToolAndRegular(t *testing.T) {
	messages := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "user", UserData: strPtr("Run ls")},
		*makeToolUseMessageWithInput("tu_123", "bash", `{"command": "ls"}`),
		*makeToolResultMessageWithOutput("tu_123", "output.txt"),
		{MessageID: "m4", ConversationID: "c1", SequenceID: 4, Type: "shelley", LLMData: strPtr("Here are the files.")},
	}

	md := string(FormatMarkdown(messages))

	// Check all headers are present
	if !strings.Contains(md, "## user") {
		t.Error("expected '## user' header")
	}
	if !strings.Contains(md, "## tool call") {
		t.Error("expected '## tool call' header")
	}
	if !strings.Contains(md, "## tool result") {
		t.Error("expected '## tool result' header")
	}
	if !strings.Contains(md, "## agent") {
		t.Error("expected '## agent' header")
	}

	// Verify content
	if !strings.Contains(md, "Run ls") {
		t.Error("expected user message content")
	}
	if !strings.Contains(md, "output.txt") {
		t.Error("expected tool result output")
	}
	if !strings.Contains(md, "Here are the files.") {
		t.Error("expected shelley message content")
	}
}

func TestFormatMarkdownToolResultMultipleTexts(t *testing.T) {
	// Tool result with multiple text entries
	content := `{"Content": [{"Type": 6, "ToolUseID": "tu_123", "ToolResult": [{"Text": "line1\n"}, {"Text": "line2\n"}]}]}`
	msg := Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "user",
		UserData:       strPtr(content),
	}

	md := string(FormatMarkdown([]Message{msg}))

	if !strings.Contains(md, "line1") {
		t.Error("expected first text")
	}
	if !strings.Contains(md, "line2") {
		t.Error("expected second text")
	}
}

func TestFormatMarkdownRegularMessagesUnchanged(t *testing.T) {
	// Verify regular messages still work as before
	messages := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi there!")},
	}

	md := string(FormatMarkdown(messages))

	if !strings.Contains(md, "## user") {
		t.Error("expected '## user' header")
	}
	if !strings.Contains(md, "Hello") {
		t.Error("expected user content")
	}
	if !strings.Contains(md, "## agent") {
		t.Error("expected '## agent' header")
	}
	if !strings.Contains(md, "Hi there!") {
		t.Error("expected agent content")
	}
}

func TestParseMessageTime(t *testing.T) {
	tests := []struct {
		name     string
		msg      *Message
		wantZero bool
		wantTime time.Time
	}{
		{
			name:     "nil message",
			msg:      nil,
			wantZero: true,
		},
		{
			name:     "empty CreatedAt",
			msg:      &Message{MessageID: "m1", CreatedAt: ""},
			wantZero: true,
		},
		{
			name:     "invalid CreatedAt",
			msg:      &Message{MessageID: "m1", CreatedAt: "not-a-timestamp"},
			wantZero: true,
		},
		{
			name:     "valid RFC3339 timestamp",
			msg:      &Message{MessageID: "m1", CreatedAt: "2026-01-15T10:30:00Z"},
			wantZero: false,
			wantTime: time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC),
		},
		{
			name:     "valid RFC3339 with timezone",
			msg:      &Message{MessageID: "m1", CreatedAt: "2026-01-15T10:30:00-05:00"},
			wantZero: false,
			wantTime: time.Date(2026, 1, 15, 10, 30, 0, 0, time.FixedZone("", -5*60*60)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMessageTime(tt.msg)
			if tt.wantZero {
				if !got.IsZero() {
					t.Errorf("ParseMessageTime() = %v, want zero time", got)
				}
			} else {
				if !got.Equal(tt.wantTime) {
					t.Errorf("ParseMessageTime() = %v, want %v", got, tt.wantTime)
				}
			}
		})
	}
}

// Tests for slug-based filtering (FilterSince and FilterFrom should use MessageSlug)

func TestFilterSinceSlugBased(t *testing.T) {
	// Create messages including tool use and result
	// Tool use has Type="shelley", tool result has Type="user" but slug should be "bash-result"
	messages := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi!")},
		{MessageID: "m3", ConversationID: "c1", SequenceID: 3, Type: "user", UserData: strPtr("Run ls")},
		// Tool use (Type=shelley, slug=bash-tool)
		{MessageID: "m4", ConversationID: "c1", SequenceID: 4, Type: "shelley", LLMData: strPtr(`{"Content": [{"Type": 5, "ID": "tu_123", "ToolName": "bash"}]}`)},
		// Tool result - Type=user but slug=bash-result, should NOT match "user"
		{MessageID: "m5", ConversationID: "c1", SequenceID: 5, Type: "user", UserData: strPtr(`{"Content": [{"Type": 6, "ToolUseID": "tu_123"}]}`)},
		{MessageID: "m6", ConversationID: "c1", SequenceID: 6, Type: "shelley", LLMData: strPtr("Done")},
		{MessageID: "m7", ConversationID: "c1", SequenceID: 7, Type: "user", UserData: strPtr("Goodbye")},
	}

	// Test: since/user/1 should skip tool result (m5) and find m7 (the last real user message)
	// Since it's the LAST user message, and we exclude the reference, result should be empty
	result := FilterSince(messages, "user", 1)
	if len(result) != 0 {
		t.Fatalf("since/user/1: expected 0 messages (nothing after seq 7), got %d", len(result))
	}

	// Test: since/user/2 should skip tool result (m5) and find m3 (the 2nd-to-last real user message)
	// Excludes m3 itself, returns m4,m5,m6,m7 = 4 messages
	result = FilterSince(messages, "user", 2)
	if len(result) != 4 {
		t.Fatalf("since/user/2: expected 4 messages (seq 4-7), got %d", len(result))
	}
	if result[0].SequenceID != 4 {
		t.Errorf("since/user/2: expected first message seq=4, got %d", result[0].SequenceID)
	}

	// Test: since/bash-result/1 should find the tool result (m5)
	// Excludes m5 itself, returns m6,m7 = 2 messages
	result = FilterSince(messages, "bash-result", 1)
	if len(result) != 2 {
		t.Fatalf("since/bash-result/1: expected 2 messages (seq 6-7), got %d", len(result))
	}
	if result[0].SequenceID != 6 {
		t.Errorf("since/bash-result/1: expected first message seq=6, got %d", result[0].SequenceID)
	}

	// Test: since/bash-tool/1 should find the tool use (m4)
	// Excludes m4 itself, returns m5,m6,m7 = 3 messages
	result = FilterSince(messages, "bash-tool", 1)
	if len(result) != 3 {
		t.Fatalf("since/bash-tool/1: expected 3 messages (seq 5-7), got %d", len(result))
	}
	if result[0].SequenceID != 5 {
		t.Errorf("since/bash-tool/1: expected first message seq=5, got %d", result[0].SequenceID)
	}
}

func TestFilterFromSlugBased(t *testing.T) {
	// Same message set as TestFilterSinceSlugBased
	messages := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi!")},
		{MessageID: "m3", ConversationID: "c1", SequenceID: 3, Type: "user", UserData: strPtr("Run ls")},
		// Tool use (Type=shelley, slug=bash-tool)
		{MessageID: "m4", ConversationID: "c1", SequenceID: 4, Type: "shelley", LLMData: strPtr(`{"Content": [{"Type": 5, "ID": "tu_123", "ToolName": "bash"}]}`)},
		// Tool result - Type=user but slug=bash-result
		{MessageID: "m5", ConversationID: "c1", SequenceID: 5, Type: "user", UserData: strPtr(`{"Content": [{"Type": 6, "ToolUseID": "tu_123"}]}`)},
		{MessageID: "m6", ConversationID: "c1", SequenceID: 6, Type: "shelley", LLMData: strPtr("Done")},
		{MessageID: "m7", ConversationID: "c1", SequenceID: 7, Type: "user", UserData: strPtr("Goodbye")},
	}

	// Test: from/user/1 should skip tool result and find m7
	m := FilterFrom(messages, "user", 1)
	if m == nil {
		t.Fatal("from/user/1: expected a message")
	}
	if m.SequenceID != 7 {
		t.Errorf("from/user/1: expected seq=7, got %d", m.SequenceID)
	}

	// Test: from/user/2 should skip tool result and find m3
	m = FilterFrom(messages, "user", 2)
	if m == nil {
		t.Fatal("from/user/2: expected a message")
	}
	if m.SequenceID != 3 {
		t.Errorf("from/user/2: expected seq=3, got %d", m.SequenceID)
	}

	// Test: from/bash-result/1 should find the tool result (m5)
	m = FilterFrom(messages, "bash-result", 1)
	if m == nil {
		t.Fatal("from/bash-result/1: expected a message")
	}
	if m.SequenceID != 5 {
		t.Errorf("from/bash-result/1: expected seq=5, got %d", m.SequenceID)
	}

	// Test: from/bash-tool/1 should find the tool use (m4)
	m = FilterFrom(messages, "bash-tool", 1)
	if m == nil {
		t.Fatal("from/bash-tool/1: expected a message")
	}
	if m.SequenceID != 4 {
		t.Errorf("from/bash-tool/1: expected seq=4, got %d", m.SequenceID)
	}
}

// ============================================================================
// Comprehensive test cases for FilterSince edge cases
// These tests investigate why '## user' might appear at the start of since/user/1.md
// ============================================================================

func TestFilterSince_ConsecutiveUserMessages(t *testing.T) {
	// Scenario: user, user, shelley, user
	// since/user/1 should find seq 4 (last user) and return nothing after it
	msgs := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "user", UserData: strPtr("First user msg")},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "user", UserData: strPtr("Second user msg")},
		{MessageID: "m3", ConversationID: "c1", SequenceID: 3, Type: "shelley", LLMData: strPtr("Response")},
		{MessageID: "m4", ConversationID: "c1", SequenceID: 4, Type: "user", UserData: strPtr("Third user msg")},
	}

	result := FilterSince(msgs, "user", 1)
	t.Logf("FilterSince(user, 1) returned %d messages", len(result))
	for i, m := range result {
		t.Logf("  [%d] seq=%d type=%s", i, m.SequenceID, m.Type)
	}

	// Last user message is seq 4, so result should be empty
	if len(result) != 0 {
		t.Errorf("Expected 0 messages after last user msg, got %d", len(result))
	}

	// Now test since/user/2 - should find seq 2 and return seq 3, 4
	result2 := FilterSince(msgs, "user", 2)
	t.Logf("FilterSince(user, 2) returned %d messages", len(result2))
	for i, m := range result2 {
		t.Logf("  [%d] seq=%d type=%s", i, m.SequenceID, m.Type)
	}

	if len(result2) != 2 {
		t.Errorf("Expected 2 messages after 2nd-to-last user, got %d", len(result2))
	}

	// Check what FormatMarkdown would produce
	md := string(FormatMarkdown(result2))
	t.Logf("FormatMarkdown output:\n%s", md)

	// First header should be agent, not user
	if strings.HasPrefix(md, "## user") {
		t.Errorf("FOUND BUG: Markdown starts with '## user' but should start with '## agent'")
		t.Logf("Full markdown:\n%s", md)
	}
}

func TestFilterSince_ToolCallsMixedIn(t *testing.T) {
	// Scenario: user, bash-tool, bash-result, shelley, user
	// Tool calls and results should NOT count as "user" messages

	// Create tool use JSON using Shelley API format (Type=5 for tool_use)
	toolUseJSON := `{"Content": [{"Type": 5, "ID": "tool123", "ToolName": "bash", "ToolInput": {"command": "ls"}}]}`
	// Create tool result JSON using Shelley API format (Type=6 for tool_result)
	toolResultJSON := `{"Content": [{"Type": 6, "ToolUseID": "tool123", "ToolResult": [{"Text": "file1.txt\nfile2.txt"}]}]}`

	msgs := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "user", UserData: strPtr("Run ls")},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "shelley", LLMData: strPtr(toolUseJSON)},
		{MessageID: "m3", ConversationID: "c1", SequenceID: 3, Type: "user", UserData: strPtr(toolResultJSON)},
		{MessageID: "m4", ConversationID: "c1", SequenceID: 4, Type: "shelley", LLMData: strPtr("Here are your files")},
		{MessageID: "m5", ConversationID: "c1", SequenceID: 5, Type: "user", UserData: strPtr("Thanks!")},
	}

	// First, verify MessageSlug correctly identifies each message
	toolMap := buildToolMapFromSlice(msgs)
	t.Logf("Tool map: %v", toolMap)

	for _, m := range msgs {
		slug := MessageSlug(&m, toolMap)
		t.Logf("seq=%d type=%s slug=%s", m.SequenceID, m.Type, slug)
	}

	// Verify seq 3 (tool result) is NOT identified as "user"
	slug3 := MessageSlug(&msgs[2], toolMap)
	if slug3 == "user" {
		t.Errorf("FOUND BUG: Tool result (seq 3) has slug 'user', should be 'bash-result'")
	}

	// since/user/1 should find seq 5 (last actual user msg) and return nothing
	result := FilterSince(msgs, "user", 1)
	t.Logf("FilterSince(user, 1) returned %d messages", len(result))
	for i, m := range result {
		slug := MessageSlug(&m, toolMap)
		t.Logf("  [%d] seq=%d type=%s slug=%s", i, m.SequenceID, m.Type, slug)
	}

	if len(result) != 0 {
		t.Errorf("Expected 0 messages after last user msg, got %d", len(result))
	}

	// since/user/2 should find seq 1 and return seq 2,3,4,5
	result2 := FilterSince(msgs, "user", 2)
	t.Logf("FilterSince(user, 2) returned %d messages", len(result2))

	// Check what FormatMarkdown would produce
	md := string(FormatMarkdown(result2))
	t.Logf("FormatMarkdown output:\n%s", md)

	// First header should NOT be "## user"
	if strings.HasPrefix(md, "## user") {
		t.Errorf("FOUND BUG: Markdown starts with '## user'")
	}
}

func TestFilterSince_ToolResultMisidentifiedAsUser(t *testing.T) {
	// Scenario: Tool result with Type="user" but content is tool_result
	// This tests the MessageSlug function specifically

	// Use Shelley API format (Type=5 for tool_use, Type=6 for tool_result)
	toolUseJSON := `{"Content": [{"Type": 5, "ID": "tool456", "ToolName": "bash", "ToolInput": {"command": "pwd"}}]}`
	toolResultJSON := `{"Content": [{"Type": 6, "ToolUseID": "tool456", "ToolResult": [{"Text": "/home/user"}]}]}`

	msgs := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "user", UserData: strPtr("What directory?")},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "shelley", LLMData: strPtr(toolUseJSON)},
		{MessageID: "m3", ConversationID: "c1", SequenceID: 3, Type: "user", UserData: strPtr(toolResultJSON)},
	}

	toolMap := buildToolMapFromSlice(msgs)

	// Test each message's slug
	slug1 := MessageSlug(&msgs[0], toolMap)
	slug2 := MessageSlug(&msgs[1], toolMap)
	slug3 := MessageSlug(&msgs[2], toolMap)

	t.Logf("seq=1 (user msg): slug=%s (expected: user)", slug1)
	t.Logf("seq=2 (tool call): slug=%s (expected: bash-tool)", slug2)
	t.Logf("seq=3 (tool result): slug=%s (expected: bash-result)", slug3)

	if slug1 != "user" {
		t.Errorf("Expected slug 'user' for seq 1, got %s", slug1)
	}
	if slug2 != "bash-tool" {
		t.Errorf("Expected slug 'bash-tool' for seq 2, got %s", slug2)
	}
	if slug3 != "bash-result" {
		t.Errorf("FOUND BUG: Expected slug 'bash-result' for seq 3, got %s", slug3)
	}

	// since/user/1 should find seq 1 and return seq 2, 3
	result := FilterSince(msgs, "user", 1)
	t.Logf("FilterSince(user, 1) returned %d messages", len(result))

	if len(result) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(result))
	}

	// Check markdown - should NOT start with "## user"
	md := string(FormatMarkdown(result))
	t.Logf("FormatMarkdown output:\n%s", md)

	if strings.HasPrefix(md, "## user") {
		t.Errorf("FOUND BUG: Markdown starts with '## user' but should start with tool call header")
	}
}

func TestFilterSince_OnlyUserMessages(t *testing.T) {
	// Edge case: conversation with only user messages
	msgs := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "user", UserData: strPtr("Anyone there?")},
		{MessageID: "m3", ConversationID: "c1", SequenceID: 3, Type: "user", UserData: strPtr("Hello???")},
	}

	// since/user/1 finds seq 3, returns empty
	result := FilterSince(msgs, "user", 1)
	if len(result) != 0 {
		t.Errorf("Expected 0 messages, got %d", len(result))
	}

	// since/user/2 finds seq 2, returns seq 3
	result2 := FilterSince(msgs, "user", 2)
	if len(result2) != 1 {
		t.Errorf("Expected 1 message, got %d", len(result2))
	}

	md := string(FormatMarkdown(result2))
	t.Logf("FormatMarkdown output:\n%s", md)

	// This SHOULD start with "## user" because only user messages exist after seq 2
	if !strings.HasPrefix(md, "## user") {
		t.Errorf("Expected markdown to start with '## user' for user-only conversation")
	}
}

func TestFilterSince_EmptyResultWhenReferenceIsLast(t *testing.T) {
	// Edge case: the reference message is the last message
	msgs := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "shelley", LLMData: strPtr("Hi")},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "user", UserData: strPtr("Hello")},
	}

	result := FilterSince(msgs, "user", 1)
	if len(result) != 0 {
		t.Errorf("Expected 0 messages when reference is last, got %d", len(result))
	}

	// FormatMarkdown on empty should return empty
	md := string(FormatMarkdown(result))
	if md != "" {
		t.Errorf("Expected empty markdown for empty result, got: %s", md)
	}
}

func TestFilterSince_RealWorldScenario(t *testing.T) {
	// Simulate a real conversation flow that might trigger the bug
	// user asks question -> shelley responds with tool call -> tool result -> shelley final answer -> user follow-up

	// Use Shelley API format (Type=5 for tool_use, Type=6 for tool_result)
	toolUseJSON := `{"Content": [{"Type": 5, "ID": "toolu_abc", "ToolName": "bash", "ToolInput": {"command": "echo hello"}}]}`
	toolResultJSON := `{"Content": [{"Type": 6, "ToolUseID": "toolu_abc", "ToolResult": [{"Text": "hello"}]}]}`

	msgs := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "user", UserData: strPtr("Run echo hello")},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "shelley", LLMData: strPtr(toolUseJSON)},
		{MessageID: "m3", ConversationID: "c1", SequenceID: 3, Type: "user", UserData: strPtr(toolResultJSON)},
		{MessageID: "m4", ConversationID: "c1", SequenceID: 4, Type: "shelley", LLMData: strPtr("The command output 'hello'")},
		{MessageID: "m5", ConversationID: "c1", SequenceID: 5, Type: "user", UserData: strPtr("Great, thanks!")},
	}

	toolMap := buildToolMapFromSlice(msgs)

	// Log all slugs for debugging
	t.Log("Message slugs:")
	for _, m := range msgs {
		slug := MessageSlug(&m, toolMap)
		t.Logf("  seq=%d type=%s slug=%s", m.SequenceID, m.Type, slug)
	}

	// Test since/user/1 - should find seq 5 and return empty
	result1 := FilterSince(msgs, "user", 1)
	t.Logf("since/user/1: %d messages", len(result1))
	if len(result1) != 0 {
		md := string(FormatMarkdown(result1))
		t.Logf("Unexpected non-empty result:\n%s", md)
		if strings.HasPrefix(md, "## user") {
			t.Errorf("FOUND BUG: since/user/1 shows '## user' header")
		}
	}

	// Test since/user/2 - should find seq 1 and return seq 2,3,4,5
	result2 := FilterSince(msgs, "user", 2)
	t.Logf("since/user/2: %d messages", len(result2))

	md2 := string(FormatMarkdown(result2))
	t.Logf("since/user/2 markdown:\n%s", md2)

	// The first header should be the tool call, not "user"
	if strings.HasPrefix(md2, "## user") {
		t.Errorf("FOUND BUG: since/user/2 starts with '## user' header")
	}
}

// Test that verifies the format header generation for tool results
func TestFormatMarkdown_ToolResultHeader(t *testing.T) {
	// A tool result message that has Type="user" but content is tool_result
	// Use Shelley API format (Type=5 for tool_use, Type=6 for tool_result)
	toolUseJSON := `{"Content": [{"Type": 5, "ID": "tool789", "ToolName": "bash", "ToolInput": {"command": "ls"}}]}`
	toolResultJSON := `{"Content": [{"Type": 6, "ToolUseID": "tool789", "ToolResult": [{"Text": "output"}]}]}`

	msgs := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "shelley", LLMData: strPtr(toolUseJSON)},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "user", UserData: strPtr(toolResultJSON)},
	}

	md := string(FormatMarkdown(msgs))
	t.Logf("FormatMarkdown output:\n%s", md)

	// The tool result should have header "## tool result: bash", NOT "## user"
	if strings.Contains(md, "## user") {
		t.Errorf("FOUND BUG: FormatMarkdown shows '## user' for tool result message")
	}
	if !strings.Contains(md, "## tool result") {
		t.Errorf("Expected '## tool result' header for tool result message")
	}
}

func TestFormatMarkdownMultipleContentItems(t *testing.T) {
	// Real-world scenario: message with text explanation followed by multiple tool calls
	content := `{"Content": [
		{"Type": 2, "Text": "I'll read the necessary files and check the ticket system."},
		{"Type": 5, "ToolName": "bash", "ToolInput": {"command": "cat ~/.skills/supervisor-agent/SKILL.md"}},
		{"Type": 5, "ToolName": "bash", "ToolInput": {"command": "tk help"}},
		{"Type": 5, "ToolName": "bash", "ToolInput": {"command": "tk ls"}}
	]}`
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(content),
	}

	md := string(FormatMarkdown([]Message{*msg}))
	t.Logf("Markdown output:\n%s", md)

	// Header should be tool call with first tool name
	if !strings.Contains(md, "## tool call: bash") {
		t.Errorf("expected header '## tool call: bash', got:\n%s", md)
	}

	// Body should contain the text explanation
	if !strings.Contains(md, "I'll read the necessary files") {
		t.Errorf("expected body to contain text explanation, got:\n%s", md)
	}

	// Body should contain ALL tool call arguments, not just the first one
	if !strings.Contains(md, "cat ~/.skills/supervisor-agent/SKILL.md") {
		t.Errorf("expected body to contain first tool call argument, got:\n%s", md)
	}
	if !strings.Contains(md, "tk help") {
		t.Errorf("expected body to contain second tool call argument, got:\n%s", md)
	}
	if !strings.Contains(md, "tk ls") {
		t.Errorf("expected body to contain third tool call argument, got:\n%s", md)
	}
}

// ============================================================================
// Tests for improved tool call argument formatting (sf-2jt7)
// ============================================================================

func TestFormatToolCallContent_SingleSimpleField(t *testing.T) {
	// Single field with string value should format as "key: value"
	msg := makeToolUseMessageWithInput("tu_001", "bash", `{"command": "ls -la"}`)
	messages := []Message{*msg}

	md := string(FormatMarkdown(messages))

	// Should contain "command: ls -la", not JSON
	if !strings.Contains(md, "command: ls -la") {
		t.Errorf("expected 'command: ls -la', got:\n%s", md)
	}

	// Should NOT contain JSON braces in the body (header can have "tool call:")
	lines := strings.Split(md, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			continue
		}
		if strings.Contains(line, "{") || strings.Contains(line, "}") {
			t.Errorf("unexpected JSON in body: %s", line)
		}
	}
}

func TestFormatToolCallContent_MultipleSimpleFields(t *testing.T) {
	// Multiple fields should format as multiple key: value lines
	content := `{"Content": [{"Type": 5, "ID": "tu_002", "ToolName": "patch", "ToolInput": {"path": "test.txt", "operation": "replace", "newText": "hello"}}]}`
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(content),
	}

	md := string(FormatMarkdown([]Message{*msg}))

	// Should contain key-value pairs
	if !strings.Contains(md, "path: test.txt") {
		t.Errorf("expected 'path: test.txt', got:\n%s", md)
	}
	if !strings.Contains(md, "operation: replace") {
		t.Errorf("expected 'operation: replace', got:\n%s", md)
	}
	if !strings.Contains(md, "newText: hello") {
		t.Errorf("expected 'newText: hello', got:\n%s", md)
	}
}

func TestFormatToolCallContent_NumberAndBooleanValues(t *testing.T) {
	// Test numeric and boolean values format correctly
	content := `{"Content": [{"Type": 5, "ID": "tu_003", "ToolName": "test", "ToolInput": {"count": 42, "enabled": true, "ratio": 3.14}}]}`
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(content),
	}

	md := string(FormatMarkdown([]Message{*msg}))

	if !strings.Contains(md, "count: 42") {
		t.Errorf("expected 'count: 42', got:\n%s", md)
	}
	if !strings.Contains(md, "enabled: true") {
		t.Errorf("expected 'enabled: true', got:\n%s", md)
	}
	if !strings.Contains(md, "ratio: 3.14") {
		t.Errorf("expected 'ratio: 3.14', got:\n%s", md)
	}
}

func TestFormatToolCallContent_NestedObject(t *testing.T) {
	// Nested objects should fall back to JSON
	content := `{"Content": [{"Type": 5, "ID": "tu_004", "ToolName": "complex", "ToolInput": {"options": {"verbose": true, "debug": false}}}]}`
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(content),
	}

	md := string(FormatMarkdown([]Message{*msg}))

	// Should contain JSON structure for nested object
	if !strings.Contains(md, "options") {
		t.Errorf("expected 'options', got:\n%s", md)
	}
	// JSON should be pretty-printed (contains indentation)
	if !strings.Contains(md, "  ") {
		t.Errorf("expected pretty-printed JSON with indentation, got:\n%s", md)
	}
}

func TestFormatToolCallContent_ArrayValue(t *testing.T) {
	// Arrays in values should trigger JSON fallback
	content := `{"Content": [{"Type": 5, "ID": "tu_005", "ToolName": "multi", "ToolInput": {"files": ["a.txt", "b.txt"]}}]}`
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(content),
	}

	md := string(FormatMarkdown([]Message{*msg}))

	// Should contain JSON array structure
	if !strings.Contains(md, "files") {
		t.Errorf("expected 'files', got:\n%s", md)
	}
	if !strings.Contains(md, "[") || !strings.Contains(md, "]") {
		t.Errorf("expected array brackets in output, got:\n%s", md)
	}
}

func TestFormatToolCallContent_EmptyInput(t *testing.T) {
	// Empty input should return empty body
	content := `{"Content": [{"Type": 5, "ID": "tu_006", "ToolName": "think"}]}`
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(content),
	}

	md := string(FormatMarkdown([]Message{*msg}))

	// Header should be present
	if !strings.Contains(md, "## tool call: think") {
		t.Errorf("expected '## tool call: think', got:\n%s", md)
	}
}

func TestFormatToolCallContent_NullValue(t *testing.T) {
	// Null values should be handled
	content := `{"Content": [{"Type": 5, "ID": "tu_007", "ToolName": "test", "ToolInput": {"value": null}}]}`
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(content),
	}

	md := string(FormatMarkdown([]Message{*msg}))

	if !strings.Contains(md, "value: null") {
		t.Errorf("expected 'value: null', got:\n%s", md)
	}
}

func TestFormatToolCallContent_MultilineString(t *testing.T) {
	// Multiline strings should preserve newlines
	content := `{"Content": [{"Type": 5, "ID": "tu_008", "ToolName": "bash", "ToolInput": {"command": "echo 'line1'\necho 'line2'"}}]}`
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(content),
	}

	md := string(FormatMarkdown([]Message{*msg}))

	// Should contain the command with embedded newline
	if !strings.Contains(md, "command: echo 'line1'\necho 'line2'") {
		t.Errorf("expected multiline command, got:\n%s", md)
	}
}

func TestFormatToolCallContent_KeyOrder(t *testing.T) {
	// Keys should be sorted for consistent output
	content := `{"Content": [{"Type": 5, "ID": "tu_009", "ToolName": "patch", "ToolInput": {"z_last": "c", "a_first": "a", "m_middle": "b"}}]}`
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(content),
	}

	md := string(FormatMarkdown([]Message{*msg}))

	// Keys should appear in sorted order: a_first, m_middle, z_last
	aIdx := strings.Index(md, "a_first:")
	mIdx := strings.Index(md, "m_middle:")
	zIdx := strings.Index(md, "z_last:")

	if aIdx == -1 || mIdx == -1 || zIdx == -1 {
		t.Errorf("expected all keys present, got:\n%s", md)
	}
	if !(aIdx < mIdx && mIdx < zIdx) {
		t.Errorf("expected keys in sorted order, got:\n%s", md)
	}
}

// ============================================================================
// Tests for tool result command formatting (sf-7ydd)
// ============================================================================

func TestFormatToolResultContent_WithCommand(t *testing.T) {
	// Create a tool use message with a bash command
	toolUseContent := `{"Content": [{"Type": 5, "ID": "tu_cmd_001", "ToolName": "bash", "ToolInput": {"command": "ls -la /tmp"}}]}`
	toolUseMsg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(toolUseContent),
	}

	// Create a tool result message referencing that tool use
	toolResultContent := `{"Content": [{"Type": 6, "ToolUseID": "tu_cmd_001", "ToolResult": [{"Text": "file1.txt\nfile2.txt\n"}]}]}`
	toolResultMsg := &Message{
		MessageID:      "m2",
		ConversationID: "c1",
		SequenceID:     2,
		Type:           "user",
		UserData:       strPtr(toolResultContent),
	}

	messages := []Message{*toolUseMsg, *toolResultMsg}
	md := string(FormatMarkdown(messages))

	t.Logf("Markdown output:\n%s", md)

	// Should have tool result header
	if !strings.Contains(md, "## tool result: bash") {
		t.Errorf("expected '## tool result: bash' header")
	}

	// Should have command subheader
	if !strings.Contains(md, "### command: ls -la /tmp") {
		t.Errorf("expected '### command: ls -la /tmp' subheader, got:\n%s", md)
	}

	// Should have output in code block
	if !strings.Contains(md, "```\nfile1.txt\nfile2.txt\n```") {
		t.Errorf("expected output in code block, got:\n%s", md)
	}
}

func TestFormatToolResultContent_MultipleResults(t *testing.T) {
	// Create tool use messages for multiple commands
	toolUse1 := `{"Content": [{"Type": 5, "ID": "tu_multi_001", "ToolName": "bash", "ToolInput": {"command": "echo hello"}}]}`
	toolUse2 := `{"Content": [{"Type": 5, "ID": "tu_multi_002", "ToolName": "bash", "ToolInput": {"command": "echo world"}}]}`

	// Create tool result message with multiple results
	toolResultContent := `{"Content": [
		{"Type": 6, "ToolUseID": "tu_multi_001", "ToolResult": [{"Text": "hello\n"}]},
		{"Type": 6, "ToolUseID": "tu_multi_002", "ToolResult": [{"Text": "world\n"}]}
	]}`

	messages := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "shelley", LLMData: strPtr(toolUse1)},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "shelley", LLMData: strPtr(toolUse2)},
		{MessageID: "m3", ConversationID: "c1", SequenceID: 3, Type: "user", UserData: strPtr(toolResultContent)},
	}

	md := string(FormatMarkdown(messages))
	t.Logf("Markdown output:\n%s", md)

	// Should have both commands
	if !strings.Contains(md, "### command: echo hello") {
		t.Errorf("expected '### command: echo hello' subheader")
	}
	if !strings.Contains(md, "### command: echo world") {
		t.Errorf("expected '### command: echo world' subheader")
	}

	// Should have both outputs in code blocks
	if !strings.Contains(md, "```\nhello\n```") {
		t.Errorf("expected 'hello' output in code block")
	}
	if !strings.Contains(md, "```\nworld\n```") {
		t.Errorf("expected 'world' output in code block")
	}
}

func TestFormatToolResultContent_NoCorrespondingToolUse(t *testing.T) {
	// Tool result without a corresponding tool use should still work
	toolResultContent := `{"Content": [{"Type": 6, "ToolUseID": "tu_orphan", "ToolResult": [{"Text": "orphan output\n"}]}]}`

	messages := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "user", UserData: strPtr(toolResultContent)},
	}

	md := string(FormatMarkdown(messages))
	t.Logf("Markdown output:\n%s", md)

	// Should still have the output in a code block (no command subheader)
	if !strings.Contains(md, "```\norphan output\n```") {
		t.Errorf("expected output in code block even without command, got:\n%s", md)
	}

	// Should NOT have a command subheader since there's no matching tool use
	if strings.Contains(md, "### command:") {
		t.Errorf("should not have command subheader for orphan tool result")
	}
}

func TestFormatToolResultContent_NonBashTool(t *testing.T) {
	// Test with a non-bash tool (e.g., patch)
	toolUseContent := `{"Content": [{"Type": 5, "ID": "tu_patch_001", "ToolName": "patch", "ToolInput": {"path": "test.txt", "operation": "replace", "oldText": "foo", "newText": "bar"}}]}`
	toolUseMsg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr(toolUseContent),
	}

	toolResultContent := `{"Content": [{"Type": 6, "ToolUseID": "tu_patch_001", "ToolResult": [{"Text": "patches applied"}]}]}`
	toolResultMsg := &Message{
		MessageID:      "m2",
		ConversationID: "c1",
		SequenceID:     2,
		Type:           "user",
		UserData:       strPtr(toolResultContent),
	}

	messages := []Message{*toolUseMsg, *toolResultMsg}
	md := string(FormatMarkdown(messages))

	t.Logf("Markdown output:\n%s", md)

	// Should have tool result header
	if !strings.Contains(md, "## tool result: patch") {
		t.Errorf("expected '## tool result: patch' header")
	}

	// Should have a command subheader with key=value format for non-bash tools
	if !strings.Contains(md, "### command:") {
		t.Errorf("expected '### command:' subheader for non-bash tool")
	}

	// Should include the parameters in some form
	if !strings.Contains(md, "newText=bar") || !strings.Contains(md, "oldText=foo") {
		t.Errorf("expected tool parameters in command, got:\n%s", md)
	}
}

func TestBuildToolCallMap(t *testing.T) {
	messages := []*Message{
		makeToolUseMessageWithInput("tu_map_001", "bash", `{"command": "ls -la"}`),
		makeToolResultMessageWithOutput("tu_map_001", "output"),
		makeToolUseMessageWithInput("tu_map_002", "patch", `{"path": "test.txt"}`),
	}

	toolCallMap := BuildToolCallMap(messages)

	if len(toolCallMap) != 2 {
		t.Fatalf("expected 2 entries in tool call map, got %d", len(toolCallMap))
	}

	if info, ok := toolCallMap["tu_map_001"]; !ok {
		t.Errorf("expected tu_map_001 in map")
	} else {
		if info.Name != "bash" {
			t.Errorf("expected Name='bash', got %q", info.Name)
		}
		if !strings.Contains(string(info.Input), "ls -la") {
			t.Errorf("expected Input to contain 'ls -la', got %s", string(info.Input))
		}
	}

	if info, ok := toolCallMap["tu_map_002"]; !ok {
		t.Errorf("expected tu_map_002 in map")
	} else {
		if info.Name != "patch" {
			t.Errorf("expected Name='patch', got %q", info.Name)
		}
	}
}

// Test for multiple tool results with different commands (sf-7ydd)
func TestFormatMarkdownMultipleToolResults(t *testing.T) {
	// Create messages with tool calls and corresponding results
	toolUse1JSON := `{"Content": [{"Type": 5, "ID": "tu_001", "ToolName": "bash", "ToolInput": {"command": "tk help"}}]}`
	toolUse2JSON := `{"Content": [{"Type": 5, "ID": "tu_002", "ToolName": "bash", "ToolInput": {"command": "cat /foo"}}]}`

	// Tool result message with multiple results
	toolResultJSON := `{"Content": [
		{"Type": 6, "ToolUseID": "tu_001", "ToolResult": [{"Text": "tk - minimal ticket system...\n"}]},
		{"Type": 6, "ToolUseID": "tu_002", "ToolResult": [{"Text": "some file contents\n"}]}
	]}`

	msgs := []Message{
		{MessageID: "m1", ConversationID: "c1", SequenceID: 1, Type: "shelley", LLMData: strPtr(toolUse1JSON)},
		{MessageID: "m2", ConversationID: "c1", SequenceID: 2, Type: "shelley", LLMData: strPtr(toolUse2JSON)},
		{MessageID: "m3", ConversationID: "c1", SequenceID: 3, Type: "user", UserData: strPtr(toolResultJSON)},
	}

	md := string(FormatMarkdown(msgs))
	t.Logf("Markdown output:\n%s", md)

	// Should show command headers for each result
	if !strings.Contains(md, "### command: tk help") {
		t.Errorf("expected '### command: tk help', got:\n%s", md)
	}
	if !strings.Contains(md, "### command: cat /foo") {
		t.Errorf("expected '### command: cat /foo', got:\n%s", md)
	}

	// Should contain the output text in code blocks
	if !strings.Contains(md, "tk - minimal ticket system...") {
		t.Errorf("expected 'tk - minimal ticket system...', got:\n%s", md)
	}
	if !strings.Contains(md, "some file contents") {
		t.Errorf("expected 'some file contents', got:\n%s", md)
	}

	// Verify the expected format structure (command header followed by code block)
	// The output should look like the expected format from the ticket
	expectedPatterns := []string{
		"## tool result: bash",
		"### command: tk help",
		"```",
		"tk - minimal ticket system...",
		"### command: cat /foo",
		"```",
		"some file contents",
	}

	for _, pattern := range expectedPatterns {
		if !strings.Contains(md, pattern) {
			t.Errorf("expected pattern %q not found in:\n%s", pattern, md)
		}
	}
}
