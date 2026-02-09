package shelley

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ParseMessages extracts the messages array from a conversation JSON response.
func ParseMessages(data []byte) ([]Message, error) {
	var resp struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse conversation: %w", err)
	}
	return resp.Messages, nil
}

// FormatJSON marshals messages to indented JSON.
func FormatJSON(messages []Message) ([]byte, error) {
	return json.MarshalIndent(messages, "", "  ")
}

// FormatMarkdown formats messages as Markdown.
// Tool calls are formatted with "## tool call: <name>" header, tool results with "## tool result: <name>".
// Regular messages use their Type field as the header (e.g., "## user", "## agent").
func FormatMarkdown(messages []Message) []byte {
	// Build tool call map for looking up tool names and inputs in tool results
	msgPtrs := make([]*Message, len(messages))
	for i := range messages {
		msgPtrs[i] = &messages[i]
	}
	toolCallMap := BuildToolCallMap(msgPtrs)

	var b strings.Builder
	for _, m := range messages {
		header, content := formatMessageMarkdown(&m, toolCallMap)
		b.WriteString("## ")
		b.WriteString(header)
		b.WriteString("\n\n")
		if content != "" {
			b.WriteString(content)
			b.WriteString("\n\n")
		}
	}
	return []byte(b.String())
}

// formatMessageMarkdown returns the header and content for a message's markdown representation.
// Returns (header, content) where header includes tool name for tool calls (e.g., "tool call: bash")
// and tool results (e.g., "tool result: bash"), or the message type for regular messages.
//
// Messages may contain multiple content items (text + multiple tool calls). This function
// processes ALL content items and combines them into a single markdown output.
func formatMessageMarkdown(m *Message, toolCallMap map[string]ToolCallInfo) (string, string) {
	if m == nil {
		return "unknown", ""
	}

	// Parse content from either LLMData or UserData
	var data string
	if m.LLMData != nil {
		data = *m.LLMData
	} else if m.UserData != nil {
		data = *m.UserData
	}

	if data != "" {
		var content MessageContent
		if err := json.Unmarshal([]byte(data), &content); err == nil {
			// Determine header and build content from ALL items
			header, body := formatAllContentItems(content.Content, toolCallMap)
			if header != "" {
				return header, body
			}
		}
	}

	// Regular message - use type as header and extract text content
	// Map internal "shelley" type to user-facing "agent" for consistency
	header := m.Type
	if strings.ToLower(header) == "shelley" {
		header = "agent"
	}
	return header, messageContent(*m)
}

// formatAllContentItems processes all content items in a message and returns
// an appropriate header and combined body content.
// The header is determined by the primary content type (tool call, tool result, or message type).
// The body includes all text content and all tool call arguments.
func formatAllContentItems(items []ContentItem, toolCallMap map[string]ToolCallInfo) (string, string) {
	if len(items) == 0 {
		return "", ""
	}

	var parts []string
	var header string
	var toolNames []string
	var isToolResult bool

	for _, item := range items {
		switch item.Type {
		case ContentTypeText:
			if item.Text != "" {
				parts = append(parts, item.Text)
			}
		case ContentTypeToolUse:
			if item.ToolName != "" {
				toolNames = append(toolNames, item.ToolName)
			}
			if formatted := formatToolCallContent(item); formatted != "" {
				parts = append(parts, formatted)
			}
		case ContentTypeToolResult:
			isToolResult = true
			if item.ToolUseID != "" && toolCallMap != nil {
				if info, ok := toolCallMap[item.ToolUseID]; ok {
					toolNames = append(toolNames, info.Name)
				}
			}
			if formatted := formatToolResultContent(item, toolCallMap); formatted != "" {
				parts = append(parts, formatted)
			}
		}
	}

	// Determine header based on content types found
	if isToolResult {
		if len(toolNames) > 0 {
			header = "tool result: " + toolNames[0]
		} else {
			header = "tool result"
		}
	} else if len(toolNames) > 0 {
		header = "tool call: " + toolNames[0]
	}

	return header, strings.Join(parts, "\n\n")
}

// formatToolCallContent formats the body of a tool call message.
// Shows only the input arguments (tool name is in the header).
//
// Formatting rules:
// - Single-field object with simple string value: "key: value"
// - Multi-field object with simple values: "key1: value1\nkey2: value2"
// - Complex nested values: fall back to pretty-printed JSON
func formatToolCallContent(item ContentItem) string {
	if len(item.Input) == 0 {
		return ""
	}

	// Parse the input JSON
	var parsed interface{}
	if err := json.Unmarshal(item.Input, &parsed); err != nil {
		return string(item.Input)
	}

	// Check if it's an object with simple values
	if obj, ok := parsed.(map[string]interface{}); ok {
		return formatToolInputObject(obj)
	}

	// Fall back to pretty-printed JSON for other cases (arrays, etc.)
	if pretty, err := json.MarshalIndent(parsed, "", "  "); err == nil {
		return string(pretty)
	}
	return string(item.Input)
}

// formatToolInputObject formats a tool input object as readable key-value pairs.
// Returns formatted string or falls back to JSON for complex nested values.
func formatToolInputObject(obj map[string]interface{}) string {
	if len(obj) == 0 {
		return ""
	}

	// Check if all values are simple (string, number, bool, null)
	allSimple := true
	for _, v := range obj {
		if !isSimpleValue(v) {
			allSimple = false
			break
		}
	}

	if !allSimple {
		// Fall back to pretty-printed JSON for complex nested structures
		if pretty, err := json.MarshalIndent(obj, "", "  "); err == nil {
			return string(pretty)
		}
		return fmt.Sprintf("%v", obj)
	}

	// Format as key-value pairs
	// For consistent ordering, sort the keys
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		v := obj[k]
		parts = append(parts, fmt.Sprintf("%s: %s", k, formatSimpleValue(v)))
	}
	return strings.Join(parts, "\n")
}

// isSimpleValue returns true if the value is a simple type (string, number, bool, null).
func isSimpleValue(v interface{}) bool {
	switch v.(type) {
	case string, float64, bool, nil:
		return true
	default:
		return false
	}
}

// formatSimpleValue formats a simple value for display.
func formatSimpleValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		// Check if it's an integer
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		return fmt.Sprintf("%t", val)
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%v", val)
	}
}

// formatToolResultContent formats the body of a tool result message.
// Extracts text from the ToolResult array and formats with command header if available.
// Format:
//
//	### command: <command>
//	```
//	<output>
//	```
func formatToolResultContent(item ContentItem, toolCallMap map[string]ToolCallInfo) string {
	// Extract the output text
	var outputParts []string
	for _, result := range item.ToolResult {
		if result.Text != "" {
			outputParts = append(outputParts, result.Text)
		}
	}
	output := strings.Join(outputParts, "")
	if output == "" {
		return ""
	}

	// Try to get the command from the tool call map
	var command string
	if item.ToolUseID != "" && toolCallMap != nil {
		if info, ok := toolCallMap[item.ToolUseID]; ok && len(info.Input) > 0 {
			command = extractCommandFromInput(info.Input)
		}
	}

	// Format with command header if we have a command
	if command != "" {
		var b strings.Builder
		b.WriteString("### command: ")
		b.WriteString(command)
		b.WriteString("\n\n```\n")
		b.WriteString(output)
		// Ensure output ends with newline before closing fence
		if !strings.HasSuffix(output, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```")
		return b.String()
	}

	// No command available, just return the output in a code block
	var b strings.Builder
	b.WriteString("```\n")
	b.WriteString(output)
	if !strings.HasSuffix(output, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```")
	return b.String()
}

// extractCommandFromInput extracts a command string from tool input JSON.
// For bash tools, this extracts the "command" field.
// For other tools, it returns a formatted representation of the input.
func extractCommandFromInput(input json.RawMessage) string {
	var parsed map[string]interface{}
	if err := json.Unmarshal(input, &parsed); err != nil {
		return ""
	}

	// For bash commands, extract the "command" field
	if cmd, ok := parsed["command"].(string); ok {
		return cmd
	}

	// For other tools, format the input as key=value pairs
	if len(parsed) == 0 {
		return ""
	}

	keys := make([]string, 0, len(parsed))
	for k := range parsed {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		v := parsed[k]
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, " ")
}

// GetMessage returns the message at 1-based sequence number, or nil if out of range.
func GetMessage(messages []Message, seqNum int) *Message {
	for i := range messages {
		if messages[i].SequenceID == seqNum {
			return &messages[i]
		}
	}
	return nil
}

// FilterLast returns the last n messages.
func FilterLast(messages []Message, n int) []Message {
	if n >= len(messages) {
		return messages
	}
	if n <= 0 {
		return nil
	}
	return messages[len(messages)-n:]
}

// GetNthLast returns the nth-to-last message (1-based).
// n=1 returns the last message, n=2 returns the second-to-last, etc.
// Returns nil if n is out of range or <= 0.
func GetNthLast(messages []Message, n int) *Message {
	if n <= 0 || n > len(messages) {
		return nil
	}
	return &messages[len(messages)-n]
}

// GetNthSince returns the nth message after the reference message from person (1-based).
// n=1 returns the first message after the last message from person.
// n=2 returns the second message after the last message from person.
// Returns nil if person is not found or n is out of range.
func GetNthSince(messages []Message, person string, n int) *Message {
	return GetNthSinceWithToolMap(messages, person, n, nil)
}

// GetNthSinceWithToolMap is like GetNthSince but accepts a pre-built tool name map.
// If toolMap is nil, it builds one from the messages.
func GetNthSinceWithToolMap(messages []Message, person string, n int, toolMap map[string]string) *Message {
	if n <= 0 {
		return nil
	}
	person = strings.ToLower(person)

	if toolMap == nil {
		toolMap = buildToolMapFromSlice(messages)
	}

	// Find the last message from this person
	refIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		slug := MessageSlug(&messages[i], toolMap)
		if slug == person {
			refIdx = i
			break
		}
	}
	if refIdx == -1 {
		return nil
	}

	// Get the nth message after the reference
	targetIdx := refIdx + n
	if targetIdx >= len(messages) {
		return nil
	}
	return &messages[targetIdx]
}

// FilterSince returns messages after the nth-to-last message from the given person.
// The referenced message itself is NOT included in the result.
// Person matching is case-insensitive against the message slug (computed by MessageSlug).
// This means "user" matches actual user messages but not tool results (which have slug like "bash-result").
// n=1 means messages after the last message from that person, n=2 means after the second-to-last, etc.
func FilterSince(messages []Message, person string, n int) []Message {
	return FilterSinceWithToolMap(messages, person, n, nil)
}

// FilterSinceWithToolMap is like FilterSince but accepts a pre-built tool name map.
// If toolMap is nil, it builds one from the messages.
func FilterSinceWithToolMap(messages []Message, person string, n int, toolMap map[string]string) []Message {
	if n <= 0 {
		return nil
	}
	person = strings.ToLower(person)

	if toolMap == nil {
		toolMap = buildToolMapFromSlice(messages)
	}

	// Find the nth-to-last message from this person
	count := 0
	for i := len(messages) - 1; i >= 0; i-- {
		slug := MessageSlug(&messages[i], toolMap)
		if slug == person {
			count++
			if count == n {
				// Return messages AFTER index i (excluding the reference message)
				return messages[i+1:]
			}
		}
	}
	return nil
}

// FilterFrom returns the nth message from the given person (1-based, counting from the end).
// Person matching is case-insensitive against the message slug (computed by MessageSlug).
// This means "user" matches actual user messages but not tool results (which have slug like "bash-result").
// n=1 means the most recent message from that person.
func FilterFrom(messages []Message, person string, n int) *Message {
	return FilterFromWithToolMap(messages, person, n, nil)
}

// FilterFromWithToolMap is like FilterFrom but accepts a pre-built tool name map.
// If toolMap is nil, it builds one from the messages.
func FilterFromWithToolMap(messages []Message, person string, n int, toolMap map[string]string) *Message {
	if n <= 0 {
		return nil
	}
	person = strings.ToLower(person)

	if toolMap == nil {
		toolMap = buildToolMapFromSlice(messages)
	}

	count := 0
	for i := len(messages) - 1; i >= 0; i-- {
		slug := MessageSlug(&messages[i], toolMap)
		if slug == person {
			count++
			if count == n {
				return &messages[i]
			}
		}
	}
	return nil
}

// buildToolMapFromSlice builds a tool name map from a slice of Message values.
// This is a convenience wrapper around BuildToolNameMap for use with []Message.
func buildToolMapFromSlice(messages []Message) map[string]string {
	msgPtrs := make([]*Message, len(messages))
	for i := range messages {
		msgPtrs[i] = &messages[i]
	}
	return BuildToolNameMap(msgPtrs)
}

func messageContent(m Message) string {
	if m.UserData != nil {
		text := extractTextContent(*m.UserData)
		if text != "" {
			return text
		}
		// Fall back to raw data for non-empty but unextractable content
		if *m.UserData != "" {
			return *m.UserData
		}
	}
	if m.LLMData != nil {
		text := extractTextContent(*m.LLMData)
		if text != "" {
			return text
		}
		// Fall back to raw data for non-empty but unextractable content
		if *m.LLMData != "" {
			return *m.LLMData
		}
	}
	return ""
}

// extractTextContent extracts human-readable text from message data
// which may be plain text or JSON with a "Content" field
func extractTextContent(data string) string {
	if data == "" {
		return ""
	}

	// Check if it's JSON - if so, try to parse and extract content
	trimmed := strings.TrimSpace(data)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return extractFromJSON(data)
	}

	// Plain text, return as-is
	return data
}

// extractFromJSON attempts to parse JSON and extract text content
func extractFromJSON(jsonStr string) string {
	var content interface{}
	if err := json.Unmarshal([]byte(jsonStr), &content); err != nil {
		// Not valid JSON, return as-is
		return jsonStr
	}

	// Handle different JSON structures
	switch c := content.(type) {
	case []interface{}:
		return extractFromArray(c)
	case map[string]interface{}:
		return extractFromMap(c)
	default:
		// Unknown structure, return the original JSON string
		return jsonStr
	}
}

// extractFromArray handles arrays of content objects
func extractFromArray(arr []interface{}) string {
	var parts []string
	for _, item := range arr {
		if obj, ok := item.(map[string]interface{}); ok {
			parts = append(parts, extractFromMap(obj))
		} else {
			parts = append(parts, fmt.Sprintf("%v", item))
		}
	}
	return strings.Join(parts, "")
}

// extractFromMap extracts text from a content object map
func extractFromMap(obj map[string]interface{}) string {
	// Look for Content field first (capitalized, as seen in some API responses)
	if content, ok := obj["Content"]; ok {
		return extractFromContentField(content)
	}

	// Look for content field (lowercase)
	if content, ok := obj["content"]; ok {
		return extractFromContentField(content)
	}

	// If no content field found, return as indented JSON for readability
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", obj)
	}
	return string(data)
}

// extractFromContentField extracts text from a Content field
func extractFromContentField(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var parts []string
		for _, item := range c {
			if obj, ok := item.(map[string]interface{}); ok {
				if text, ok := obj["Text"].(string); ok {
					parts = append(parts, text)
				}
			} else {
				parts = append(parts, fmt.Sprintf("%v", item))
			}
		}
		return strings.Join(parts, "")
	case map[string]interface{}:
		if text, ok := c["Text"].(string); ok {
			return text
		}
		return fmt.Sprintf("%v", c)
	default:
		return fmt.Sprintf("%v", content)
	}
}

// ContentType represents the type of a content item in a message.
// These values match the Shelley API content types.
const (
	ContentTypeText       = 2 // Text content with explanation
	ContentTypeToolUse    = 5 // Tool call (tool_use)
	ContentTypeToolResult = 6 // Tool result (tool_result)
)

// ContentItem represents a single content item in a message's Content array.
// This is used for parsing the JSON content to detect tool calls and results.
type ContentItem struct {
	Type       int              `json:"Type"`
	Text       string           `json:"Text,omitempty"` // Text content for Type 2
	ID         string           `json:"ID,omitempty"`   // Tool use ID for tool_use (Type 5)
	ToolName   string           `json:"ToolName,omitempty"`
	ToolUseID  string           `json:"ToolUseID,omitempty"` // References tool use ID in tool_result (Type 6)
	Input      json.RawMessage  `json:"ToolInput,omitempty"`
	ToolResult []ToolResultItem `json:"ToolResult,omitempty"`
}

// ToolResultItem represents an item in the ToolResult array of a tool_result content.
type ToolResultItem struct {
	Text string `json:"Text"`
}

// MessageContent represents the parsed content from LLMData or UserData.
type MessageContent struct {
	Content []ContentItem `json:"Content"`
}

// ToolCallInfo contains information about a tool call, including its name and input.
type ToolCallInfo struct {
	Name  string
	Input json.RawMessage
}

// BuildToolCallMap iterates through all messages and builds a map from ToolUseID to ToolCallInfo.
// This enables looking up both the tool name and input for tool_result messages.
func BuildToolCallMap(messages []*Message) map[string]ToolCallInfo {
	toolMap := make(map[string]ToolCallInfo)
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		// Parse content from either LLMData or UserData
		var data string
		if msg.LLMData != nil {
			data = *msg.LLMData
		} else if msg.UserData != nil {
			data = *msg.UserData
		}
		if data == "" {
			continue
		}

		var content MessageContent
		if err := json.Unmarshal([]byte(data), &content); err != nil {
			continue
		}

		for _, item := range content.Content {
			if item.Type == ContentTypeToolUse && item.ToolName != "" {
				info := ToolCallInfo{Name: item.ToolName, Input: item.Input}
				// The Shelley API uses 'ID' field for tool use identifier in tool_use messages,
				// but 'ToolUseID' in tool_result messages to reference it.
				// We check both for compatibility.
				if item.ID != "" {
					toolMap[item.ID] = info
				}
				if item.ToolUseID != "" {
					toolMap[item.ToolUseID] = info
				}
			}
		}
	}
	return toolMap
}

// BuildToolNameMap iterates through all messages and builds a map from ToolUseID to ToolName.
// This enables looking up the tool name for tool_result messages.
// Deprecated: Use BuildToolCallMap for richer information including tool input.
func BuildToolNameMap(messages []*Message) map[string]string {
	toolCallMap := BuildToolCallMap(messages)
	toolMap := make(map[string]string, len(toolCallMap))
	for id, info := range toolCallMap {
		toolMap[id] = info.Name
	}
	return toolMap
}

// MessageSlug determines the slug for a message based on its content.
// For tool_use messages: returns "{toolname}-tool" (e.g., "bash-tool")
// For tool_result messages: returns "{toolname}-result" (e.g., "bash-result")
// For regular messages: returns lowercased Type field (e.g., "user", "assistant")
//
// The toolMap parameter should be built using BuildToolNameMap() to enable
// looking up tool names for tool_result messages.
func MessageSlug(msg *Message, toolMap map[string]string) string {
	if msg == nil {
		return "unknown"
	}

	// Parse content from either LLMData or UserData
	var data string
	if msg.LLMData != nil {
		data = *msg.LLMData
	} else if msg.UserData != nil {
		data = *msg.UserData
	}

	if data != "" {
		var content MessageContent
		if err := json.Unmarshal([]byte(data), &content); err == nil {
			// Check for tool_use or tool_result content
			for _, item := range content.Content {
				switch item.Type {
				case ContentTypeToolUse:
					if item.ToolName != "" {
						return strings.ToLower(item.ToolName) + "-tool"
					}
					// ToolName empty is unexpected - fall through to msg.Type
				case ContentTypeToolResult:
					// Look up the tool name from the toolMap using ToolUseID
					if item.ToolUseID != "" && toolMap != nil {
						if toolName, ok := toolMap[item.ToolUseID]; ok {
							return strings.ToLower(toolName) + "-result"
						}
					}
					// Fallback: check if ToolName is populated directly on the item
					if item.ToolName != "" {
						return strings.ToLower(item.ToolName) + "-result"
					}
					// Tool name not found - return generic "tool-result" to avoid
					// misidentifying as "user" (which would break FilterSince)
					return "tool-result"
				}
			}
		}
	}

	// Fall back to lowercased message type
	// Map internal "shelley" type to user-facing "agent" for consistency
	slug := strings.ToLower(msg.Type)
	if slug == "shelley" {
		return "agent"
	}
	return slug
}

// ParseMessageTime parses the CreatedAt field of a message into a time.Time.
// Returns the zero time if parsing fails or the field is empty.
func ParseMessageTime(m *Message) time.Time {
	if m == nil || m.CreatedAt == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, m.CreatedAt)
	if err != nil {
		return time.Time{}
	}
	return t
}
