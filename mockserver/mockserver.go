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
	"sync/atomic"

	"shelley-fuse/shelley"
)

// Server wraps an httptest.Server with a preconfigured Shelley mock backend.
type Server struct {
	*httptest.Server

	// FetchCount tracks the total number of requests to /api/conversation/{id}
	// endpoints. Use this in tests that verify caching behavior.
	fetchCount int32

	models       []shelley.Model
	defaultModel string

	// conversations is keyed by conversation ID.
	conversations map[string]conversationData

	// chatHandler is called for POST /api/conversation/{id}/chat.
	// If nil, returns 200 OK.
	chatHandler func(w http.ResponseWriter, r *http.Request)

	// newConvHandler is called for POST /api/conversations/new.
	// If nil, returns 404.
	newConvHandler func(w http.ResponseWriter, r *http.Request)

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
func New(opts ...Option) *Server {
	s := &Server{
		conversations: make(map[string]conversationData),
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
		var convs []shelley.Conversation
		for _, cd := range s.conversations {
			convs = append(convs, cd.conv)
		}
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
		if cd, ok := s.conversations[convID]; ok {
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
