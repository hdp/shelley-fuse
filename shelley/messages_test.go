package shelley

import (
	"encoding/json"
	"strings"
	"testing"
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
