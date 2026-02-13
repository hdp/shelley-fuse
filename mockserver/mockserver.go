// Package mockserver provides a unified mock Shelley backend for testing.
//
// It handles all standard API endpoints including the __SHELLEY_INIT__
// HTML page scraping for model discovery, conversation listing, message
// retrieval, chat, archiving, and conversation creation.
//
// Usage:
//
//	s := mockserver.New(
//		mockserver.WithModels([]shelley.Model{{ID: "test", Ready: true}}),
//		mockserver.WithConversation("conv-1", msgs),
//	)
//	defer s.Close()
//	client := shelley.NewClient(s.URL)
package mockserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"

	"shelley-fuse/shelley"
)

// Server wraps an httptest.Server with a preconfigured Shelley mock backend.
type Server struct {
	*httptest.Server

	mu sync.Mutex

	// FetchCount tracks the total number of requests to /api/conversation/{id}
	// endpoints. Use this in tests that verify caching behavior.
	fetchCount int32

	models       []shelley.Model
	defaultModel string

	// conversations is keyed by conversation ID.
	conversations map[string]conversationData

	// subagents maps parent conversation ID to child conversation IDs
	subagents map[string][]string

	// chatHandler is called for POST /api/conversation/{id}/chat.
	// If nil, returns 200 OK.
	chatHandler func(w http.ResponseWriter, r *http.Request)

	// newConvHandler is called for POST /api/conversations/new.
	// If nil, returns 404.
	newConvHandler func(w http.ResponseWriter, r *http.Request)

	// continueHandler is called for POST /api/conversations/continue.
	// If nil, uses a default handler that validates and creates a new conversation.
	continueHandler func(w http.ResponseWriter, r *http.Request)

	// errorMode, if set, returns this status code for /api/conversations.
	errorMode int

	// requestHook, if set, is called on every request before routing.
	requestHook func(r *http.Request)
}

type conversationData struct {
	conv     shelley.Conversation
	messages []shelley.Message
	// rawDetail, if non-nil, is returned verbatim for GET /api/conversation/{id}
	// instead of wrapping messages in {"messages": [...]}.
	rawDetail []byte
}

// Option configures a mock server.
type Option func(*Server)

// WithModels sets the models returned by GET / (__SHELLEY_INIT__).
func WithModels(models []shelley.Model) Option {
	return func(s *Server) {
		s.models = models
	}
}

// WithDefaultModel sets the default_model in __SHELLEY_INIT__.
func WithDefaultModel(model string) Option {
	return func(s *Server) {
		s.defaultModel = model
	}
}

// WithConversation registers a conversation with messages.
// The conversation appears in the list endpoint and its messages
// are returned from the detail endpoint.
func WithConversation(id string, messages []shelley.Message) Option {
	return func(s *Server) {
		s.conversations[id] = conversationData{
			conv:     shelley.Conversation{ConversationID: id},
			messages: messages,
		}
	}
}

// WithFullConversation registers a conversation with full metadata and messages.
func WithFullConversation(conv shelley.Conversation, messages []shelley.Message) Option {
	return func(s *Server) {
		s.conversations[conv.ConversationID] = conversationData{
			conv:     conv,
			messages: messages,
		}
	}
}

// WithConversationRawDetail registers a conversation whose detail endpoint
// returns raw JSON bytes instead of the standard {"messages": [...]} wrapper.
func WithConversationRawDetail(conv shelley.Conversation, rawDetail []byte) Option {
	return func(s *Server) {
		s.conversations[conv.ConversationID] = conversationData{
			conv:      conv,
			rawDetail: rawDetail,
		}
	}
}

// WithChatHandler sets a custom handler for POST /api/conversation/{id}/chat.
func WithChatHandler(h func(w http.ResponseWriter, r *http.Request)) Option {
	return func(s *Server) {
		s.chatHandler = h
	}
}

// WithNewConversationHandler sets a custom handler for POST /api/conversations/new.
func WithNewConversationHandler(h func(w http.ResponseWriter, r *http.Request)) Option {
	return func(s *Server) {
		s.newConvHandler = h
	}
}

// WithContinueHandler sets a custom handler for POST /api/conversations/continue.
func WithContinueHandler(h func(w http.ResponseWriter, r *http.Request)) Option {
	return func(s *Server) {
		s.continueHandler = h
	}
}

// WithConversationWorking sets the working state for a conversation.
// Must be applied after WithConversation or WithFullConversation.
func WithConversationWorking(id string, working bool) Option {
	return func(s *Server) {
		if cd, ok := s.conversations[id]; ok {
			cd.conv.Working = working
			s.conversations[id] = cd
		}
	}
}

// WithErrorMode makes /api/conversations return the given HTTP status code.
func WithErrorMode(statusCode int) Option {
	return func(s *Server) {
		s.errorMode = statusCode
	}
}

// WithRequestHook sets a callback invoked on every request before routing.
func WithRequestHook(h func(r *http.Request)) Option {
	return func(s *Server) {
		s.requestHook = h
	}
}

// New creates and starts a mock Shelley backend server.
// WithSubagent registers a child conversation (subagent) under a parent conversation.
// Both parent and child must be registered via WithConversation or WithFullConversation.
func WithSubagent(parentID, childID string) Option {
	return func(s *Server) {
		s.subagents[parentID] = append(s.subagents[parentID], childID)
	}
}

func New(opts ...Option) *Server {
	s := &Server{
		conversations: make(map[string]conversationData),
		subagents:     make(map[string][]string),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handler))
	return s
}

// FetchCount returns the number of GET requests to /api/conversation/{id} endpoints.
func (s *Server) FetchCount() int32 {
	return atomic.LoadInt32(&s.fetchCount)
}

// ResetFetchCount resets the fetch counter to zero.
func (s *Server) ResetFetchCount() {
	atomic.StoreInt32(&s.fetchCount, 0)
}

func (s *Server) handler(w http.ResponseWriter, r *http.Request) {
	if s.requestHook != nil {
		s.requestHook(r)
	}

	path := r.URL.Path

	// Error mode: all endpoints fail
	if s.errorMode != 0 {
		w.WriteHeader(s.errorMode)
		fmt.Fprintf(w, "mock error %d", s.errorMode)
		return
	}

	// GET / → __SHELLEY_INIT__ HTML page
	if path == "/" || path == "" {
		s.serveInit(w, r)
		return
	}

	// GET /api/models → models list (JSON array)
	if path == "/api/models" && r.Method == "GET" {
		s.serveModels(w, r)
		return
	}

	// GET /api/conversations → conversation list
	if path == "/api/conversations" && r.Method == "GET" {
		s.mu.Lock()
		var convs []shelley.Conversation
		for _, cd := range s.conversations {
			convs = append(convs, cd.conv)
		}
		s.mu.Unlock()
		data, _ := json.Marshal(convs)
		w.Write(data)
		return
	}

	// POST /api/conversations/new → create conversation
	if path == "/api/conversations/new" && r.Method == "POST" {
		if s.newConvHandler != nil {
			s.newConvHandler(w, r)
			return
		}
		http.NotFound(w, r)
		return
	}

	// POST /api/conversations/continue → continue conversation
	if path == "/api/conversations/continue" && r.Method == "POST" {
		if s.continueHandler != nil {
			s.continueHandler(w, r)
			return
		}
		s.handleContinueDefault(w, r)
		return
	}

	// GET /api/conversation/{id}/subagents → subagents list
	if strings.HasPrefix(path, "/api/conversation/") && strings.HasSuffix(path, "/subagents") && r.Method == "GET" {
		convID := strings.TrimPrefix(path, "/api/conversation/")
		convID = strings.TrimSuffix(convID, "/subagents")
		s.mu.Lock()
		childIDs := s.subagents[convID]
		var children []shelley.Conversation
		for _, childID := range childIDs {
			if cd, ok := s.conversations[childID]; ok {
				children = append(children, cd.conv)
			}
		}
		s.mu.Unlock()
		if children == nil {
			children = []shelley.Conversation{}
		}
		data, _ := json.Marshal(children)
		w.Write(data)
		return
	}

	// POST /api/conversation/{id}/cancel → cancel in-progress agent loop
	if strings.HasSuffix(path, "/cancel") && r.Method == "POST" {
		convID := strings.TrimPrefix(path, "/api/conversation/")
		convID = strings.TrimSuffix(convID, "/cancel")
		s.mu.Lock()
		cd, exists := s.conversations[convID]
		if exists {
			cd.conv.Working = false
			s.conversations[convID] = cd
		}
		s.mu.Unlock()
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "conversation %s not found", convID)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"cancelled"}`)
		return
	}

	// POST /api/conversation/{id}/delete → delete conversation
	if strings.HasSuffix(path, "/delete") && r.Method == "POST" {
		convID := strings.TrimPrefix(path, "/api/conversation/")
		convID = strings.TrimSuffix(convID, "/delete")
		s.mu.Lock()
		_, exists := s.conversations[convID]
		if exists {
			delete(s.conversations, convID)
		}
		s.mu.Unlock()
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "conversation %s not found", convID)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"deleted"}`)
		return
	}

	// POST /api/conversation/{id}/chat → send message
	if strings.HasSuffix(path, "/chat") && r.Method == "POST" {
		if s.chatHandler != nil {
			s.chatHandler(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	// GET /api/conversation/{id} → conversation detail
	if strings.HasPrefix(path, "/api/conversation/") && r.Method == "GET" {
		convID := strings.TrimPrefix(path, "/api/conversation/")
		s.mu.Lock()
		cd, ok := s.conversations[convID]
		s.mu.Unlock()
		if ok {
			atomic.AddInt32(&s.fetchCount, 1)
			if cd.rawDetail != nil {
				w.Write(cd.rawDetail)
				return
			}
			data, _ := json.Marshal(struct {
				Messages []shelley.Message `json:"messages"`
			}{Messages: cd.messages})
			w.Write(data)
			return
		}
	}

	http.NotFound(w, r)
}

// continueSeqNum is used to generate unique conversation IDs for continue operations.
var continueSeqNum int32

func (s *Server) handleContinueDefault(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceConversationID string `json:"source_conversation_id"`
		Model                string `json:"model,omitempty"`
		Cwd                  string `json:"cwd,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "invalid JSON: %v", err)
		return
	}
	if req.SourceConversationID == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "source_conversation_id is required")
		return
	}
	s.mu.Lock()
	_, sourceExists := s.conversations[req.SourceConversationID]
	s.mu.Unlock()
	if !sourceExists {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "conversation %s not found", req.SourceConversationID)
		return
	}
	newID := fmt.Sprintf("continued-%s-%d", req.SourceConversationID, atomic.AddInt32(&continueSeqNum, 1))
	// Register the new conversation so it appears in list endpoints
	s.mu.Lock()
	s.conversations[newID] = conversationData{
		conv:     shelley.Conversation{ConversationID: newID},
		messages: nil,
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"status":          "created",
		"conversation_id": newID,
	})
}

func (s *Server) serveInit(w http.ResponseWriter, r *http.Request) {
	defaultModelJSON, _ := json.Marshal(s.defaultModel)
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w,
		`<html><script>window.__SHELLEY_INIT__ = {"default_model": %s};</script></html>`,
		defaultModelJSON)
}

func (s *Server) serveModels(w http.ResponseWriter, r *http.Request) {
	models := s.models
	if models == nil {
		models = []shelley.Model{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models)
}
