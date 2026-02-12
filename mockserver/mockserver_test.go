package mockserver

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"shelley-fuse/shelley"
)

func TestNew_ServesModelsAPI(t *testing.T) {
	s := New(
		WithModels([]shelley.Model{{ID: "test-model", Ready: true}}),
		WithDefaultModel("test-model"),
	)
	defer s.Close()

	client := shelley.NewClient(s.URL)
	result, err := client.ListModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 1 || result.Models[0].ID != "test-model" {
		t.Errorf("unexpected models: %+v", result.Models)
	}
}

func TestNew_ServesDefaultModel(t *testing.T) {
	s := New(
		WithModels([]shelley.Model{{ID: "test-model", Ready: true}}),
		WithDefaultModel("test-model"),
	)
	defer s.Close()

	client := shelley.NewClient(s.URL)
	defModel, err := client.DefaultModel()
	if err != nil {
		t.Fatal(err)
	}
	if defModel != "test-model" {
		t.Errorf("unexpected default: %s", defModel)
	}
}

func TestNew_ServesConversations(t *testing.T) {
	msgs := []shelley.Message{{MessageID: "m1", Type: "user"}}
	s := New(WithConversation("conv-1", msgs))
	defer s.Close()

	// List conversations
	resp, err := http.Get(s.URL + "/api/conversations")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var convs []shelley.Conversation
	json.NewDecoder(resp.Body).Decode(&convs)
	if len(convs) != 1 || convs[0].ConversationID != "conv-1" {
		t.Errorf("unexpected conversations: %+v", convs)
	}

	// Get conversation detail
	resp2, err := http.Get(s.URL + "/api/conversation/conv-1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	body, _ := io.ReadAll(resp2.Body)
	var detail struct {
		Messages []shelley.Message `json:"messages"`
	}
	json.Unmarshal(body, &detail)
	if len(detail.Messages) != 1 || detail.Messages[0].MessageID != "m1" {
		t.Errorf("unexpected messages: %+v", detail.Messages)
	}

	if s.FetchCount() != 1 {
		t.Errorf("expected fetch count 1, got %d", s.FetchCount())
	}
}

func TestNew_ErrorMode(t *testing.T) {
	s := New(WithErrorMode(http.StatusInternalServerError))
	defer s.Close()

	resp, err := http.Get(s.URL + "/api/conversations")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

func TestNew_NotFoundForUnknown(t *testing.T) {
	s := New()
	defer s.Close()

	resp, err := http.Get(s.URL + "/api/conversation/unknown")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}
