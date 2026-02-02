package shelley

import (
	"encoding/json"
	"fmt"
	"strings"
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
// Tool calls are formatted with "## tool call" header, tool results with "## tool result".
// Regular messages use their Type field as the header (e.g., "## user", "## shelley").
func FormatMarkdown(messages []Message) []byte {
	// Build tool name map for looking up tool names in tool results
	msgPtrs := make([]*Message, len(messages))
	for i := range messages {
		msgPtrs[i] = &messages[i]
	}
	toolMap := BuildToolNameMap(msgPtrs)

	var b strings.Builder
	for _, m := range messages {
		header, content := formatMessageMarkdown(&m, toolMap)
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
// Returns (header, content) where header is "tool call", "tool result", or the message type.
func formatMessageMarkdown(m *Message, toolMap map[string]string) (string, string) {
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
			// Check for tool_use or tool_result content
			for _, item := range content.Content {
				switch item.Type {
				case ContentTypeToolUse:
					return "tool call", formatToolCallContent(item)
				case ContentTypeToolResult:
					return "tool result", formatToolResultContent(item)
				}
			}
		}
	}

	// Regular message - use type as header and extract text content
	return m.Type, messageContent(*m)
}

// formatToolCallContent formats the body of a tool call message.
// Shows the tool name and pretty-printed input.
func formatToolCallContent(item ContentItem) string {
	var b strings.Builder
	b.WriteString(item.ToolName)
	b.WriteString("\n\n")

	if len(item.Input) > 0 {
		// Pretty-print the input JSON
		var parsed interface{}
		if err := json.Unmarshal(item.Input, &parsed); err == nil {
			if pretty, err := json.MarshalIndent(parsed, "", "  "); err == nil {
				b.Write(pretty)
			} else {
				b.Write(item.Input)
			}
		} else {
			b.Write(item.Input)
		}
	}

	return b.String()
}

// formatToolResultContent formats the body of a tool result message.
// Extracts and concatenates text from the ToolResult array.
func formatToolResultContent(item ContentItem) string {
	var parts []string
	for _, result := range item.ToolResult {
		if result.Text != "" {
			parts = append(parts, result.Text)
		}
	}
	return strings.Join(parts, "")
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

// FilterSince returns messages since the nth-to-last message from the given person.
// Person matching is case-insensitive against the message Type field.
// n=1 means since the last message from that person, n=2 means since the second-to-last, etc.
func FilterSince(messages []Message, person string, n int) []Message {
	if n <= 0 {
		return nil
	}
	person = strings.ToLower(person)

	// Find the nth-to-last message from this person
	count := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if matchPerson(messages[i].Type, person) {
			count++
			if count == n {
				return messages[i:]
			}
		}
	}
	return nil
}

// FilterFrom returns the nth message from the given person (1-based, counting from the end).
// Person matching is case-insensitive against the message Type field.
// n=1 means the most recent message from that person.
func FilterFrom(messages []Message, person string, n int) *Message {
	if n <= 0 {
		return nil
	}
	person = strings.ToLower(person)

	count := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if matchPerson(messages[i].Type, person) {
			count++
			if count == n {
				return &messages[i]
			}
		}
	}
	return nil
}

func matchPerson(msgType, person string) bool {
	return strings.ToLower(msgType) == person
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
	ContentTypeText       = 0
	ContentTypeToolUse    = 5
	ContentTypeToolResult = 6
)

// ContentItem represents a single content item in a message's Content array.
// This is used for parsing the JSON content to detect tool calls and results.
type ContentItem struct {
	Type       int             `json:"Type"`
	ToolName   string          `json:"ToolName,omitempty"`
	ToolUseID  string          `json:"ToolUseID,omitempty"`
	Input      json.RawMessage `json:"Input,omitempty"`
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

// BuildToolNameMap iterates through all messages and builds a map from ToolUseID to ToolName.
// This enables looking up the tool name for tool_result messages.
func BuildToolNameMap(messages []*Message) map[string]string {
	toolMap := make(map[string]string)
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
			if item.Type == ContentTypeToolUse && item.ToolUseID != "" && item.ToolName != "" {
				toolMap[item.ToolUseID] = item.ToolName
			}
		}
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
				case ContentTypeToolResult:
					if item.ToolUseID != "" && toolMap != nil {
						if toolName, ok := toolMap[item.ToolUseID]; ok {
							return strings.ToLower(toolName) + "-result"
						}
					}
					// If we can't find the tool name, fall back to generic result
					return "tool-result"
				}
			}
		}
	}

	// Fall back to lowercased message type
	return strings.ToLower(msg.Type)
}
