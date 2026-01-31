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
		return *m.UserData
	}
	if m.LLMData != nil {
		return *m.LLMData
	}
	return ""
}
