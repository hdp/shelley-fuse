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
	if !strings.Contains(md, "## shelley") {
		t.Error("expected markdown to contain '## shelley'")
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
	// Since the 2nd-to-last user message (seq 3: "How are you?")
	result := FilterSince(sampleMessages, "user", 2)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages (seq 3,4,5), got %d", len(result))
	}
	if result[0].SequenceID != 3 {
		t.Errorf("expected first message seq=3, got %d", result[0].SequenceID)
	}
}

func TestFilterSinceLastFromPerson(t *testing.T) {
	// Since the last shelley message (seq 4)
	result := FilterSince(sampleMessages, "shelley", 1)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (seq 4,5), got %d", len(result))
	}
	if result[0].SequenceID != 4 {
		t.Errorf("expected first message seq=4, got %d", result[0].SequenceID)
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
	// 1st (most recent) shelley message
	m := FilterFrom(sampleMessages, "shelley", 1)
	if m == nil {
		t.Fatal("expected a message")
	}
	if *m.LLMData != "I'm doing well." {
		t.Errorf("expected 'I'm doing well.', got %s", *m.LLMData)
	}
}

func TestFilterFromSecond(t *testing.T) {
	// 2nd most recent shelley message
	m := FilterFrom(sampleMessages, "shelley", 2)
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
	m := FilterFrom(sampleMessages, "Shelley", 1)
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
	if !strings.Contains(md, "## shelley") {
		t.Error("expected markdown to contain '## shelley'")
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
	// Falls back to message Type ("user") since tool name can't be determined
	msg := makeToolResultMessage("tu_unknown")
	slug := MessageSlug(msg, map[string]string{})

	// Should fall back to message type, not generic "result"
	if slug != "user" {
		t.Errorf("expected 'user', got %q", slug)
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
	msg := &Message{
		MessageID:      "m1",
		ConversationID: "c1",
		SequenceID:     1,
		Type:           "shelley",
		LLMData:        strPtr("Hello!"),
	}

	slug := MessageSlug(msg, nil)
	if slug != "shelley" {
		t.Errorf("expected 'shelley', got %q", slug)
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
	content := fmt.Sprintf(`{"Content": [{"Type": 5, "ID": %q, "ToolName": %q, "Input": %s}]}`, toolUseID, toolName, input)
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

	// Should have "## tool call" header
	if !strings.Contains(md, "## tool call") {
		t.Error("expected markdown to contain '## tool call'")
	}

	// Should NOT have the raw message type
	if strings.Contains(md, "## shelley") {
		t.Error("tool call should not show '## shelley'")
	}

	// Should show the tool name
	if !strings.Contains(md, "bash") {
		t.Error("expected markdown to contain tool name 'bash'")
	}

	// Should show the input (pretty-printed)
	if !strings.Contains(md, "command") {
		t.Error("expected markdown to contain input field 'command'")
	}
	if !strings.Contains(md, "ls -la") {
		t.Error("expected markdown to contain input value 'ls -la'")
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
	if !strings.Contains(md, "## shelley") {
		t.Error("expected '## shelley' header")
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
	if !strings.Contains(md, "## shelley") {
		t.Error("expected '## shelley' header")
	}
	if !strings.Contains(md, "Hi there!") {
		t.Error("expected shelley content")
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
