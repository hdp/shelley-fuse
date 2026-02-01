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
func FormatMarkdown(messages []Message) []byte {
	var b strings.Builder
	for _, m := range messages {
		b.WriteString("## ")
		b.WriteString(m.Type)
		b.WriteString("\n\n")
		content := messageContent(m)
		if content != "" {
			b.WriteString(content)
			b.WriteString("\n\n")
		}
	}
	return []byte(b.String())
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
		return extractTextContent(*m.UserData)
	}
	if m.LLMData != nil {
		return extractTextContent(*m.LLMData)
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

	// If no content field found, return the whole object as string
	return fmt.Sprintf("%v", obj)
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
