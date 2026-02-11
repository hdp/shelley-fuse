// Package diag provides diagnostics for in-flight FUSE operations.
package diag

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Op represents a single in-flight FUSE operation.
type Op struct {
	ID      uint64
	Node    string // Type name of the FUSE node (e.g. "ConversationDirNode")
	Method  string // FUSE method (e.g. "Readdir", "Open", "Write")
	Detail  string // Free-form detail (e.g. path, conversation ID)
	Phase   string // Current sub-step (e.g. "HTTP POST StartConversation")
	Started time.Time
}

// OpHandle is a handle to an in-flight operation that allows callers to
// annotate sub-steps via SetPhase and to signal completion via Done.
type OpHandle struct {
	tracker *Tracker
	id      uint64
}

// SetPhase updates the phase annotation for this in-flight operation.
func (h *OpHandle) SetPhase(phase string) {
	if h.tracker == nil {
		return
	}
	h.tracker.mu.Lock()
	if op, ok := h.tracker.ops[h.id]; ok {
		op.Phase = phase
		h.tracker.ops[h.id] = op
	}
	h.tracker.mu.Unlock()
}

// Done marks the operation as complete and removes it from the tracker.
func (h *OpHandle) Done() {
	if h.tracker == nil {
		return
	}
	h.tracker.mu.Lock()
	delete(h.tracker.ops, h.id)
	h.tracker.mu.Unlock()
}

// Tracker records in-flight FUSE operations.
type Tracker struct {
	nextID atomic.Uint64
	mu     sync.Mutex
	ops    map[uint64]Op
}

// NewTracker creates a new operation tracker.
func NewTracker() *Tracker {
	return &Tracker{
		ops: make(map[uint64]Op),
	}
}

// Track records the start of a FUSE operation and returns an OpHandle
// whose Done method must be called when the operation completes.
func (t *Tracker) Track(node, method, detail string) *OpHandle {
	id := t.nextID.Add(1)
	op := Op{
		ID:      id,
		Node:    node,
		Method:  method,
		Detail:  detail,
		Started: time.Now(),
	}
	t.mu.Lock()
	t.ops[id] = op
	t.mu.Unlock()
	return &OpHandle{tracker: t, id: id}
}

// InFlight returns a snapshot of all in-flight operations, sorted by start time.
func (t *Tracker) InFlight() []Op {
	t.mu.Lock()
	ops := make([]Op, 0, len(t.ops))
	for _, op := range t.ops {
		ops = append(ops, op)
	}
	t.mu.Unlock()
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Started.Equal(ops[j].Started) {
			return ops[i].ID < ops[j].ID
		}
		return ops[i].Started.Before(ops[j].Started)
	})
	return ops
}

// Dump returns a human-readable multi-line summary of in-flight operations.
func (t *Tracker) Dump() string {
	ops := t.InFlight()
	if len(ops) == 0 {
		return "no in-flight operations\n"
	}
	now := time.Now()
	var b strings.Builder
	fmt.Fprintf(&b, "%d in-flight operation(s):\n", len(ops))
	for _, op := range ops {
		elapsed := now.Sub(op.Started).Truncate(time.Millisecond)
		fmt.Fprintf(&b, "  [%d] %s.%s", op.ID, op.Node, op.Method)
		if op.Detail != "" {
			fmt.Fprintf(&b, " %s", op.Detail)
		}
		if op.Phase != "" {
			fmt.Fprintf(&b, " [%s]", op.Phase)
		}
		fmt.Fprintf(&b, " (%s)\n", elapsed)
	}
	return b.String()
}

// Handler returns an http.Handler that serves diagnostic information.
// By default it returns human-readable text. With the ?json query parameter,
// it returns a JSON array of in-flight operations.
func (t *Tracker) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, wantJSON := r.URL.Query()["json"]
		if wantJSON {
			w.Header().Set("Content-Type", "application/json")
			ops := t.InFlight()
			if err := json.NewEncoder(w).Encode(ops); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		ops := t.InFlight()
		if len(ops) == 0 {
			fmt.Fprint(w, "no in-flight FUSE operations\n")
			return
		}
		fmt.Fprint(w, t.Dump())
	})
}

// Track is a package-level helper that is nil-safe: if t is nil, it returns
// a no-op OpHandle. This lets callers avoid nil checks.
func Track(t *Tracker, node, method, detail string) *OpHandle {
	if t == nil {
		return &OpHandle{}
	}
	return t.Track(node, method, detail)
}

// maxGoroutineStackSize is the maximum size of the goroutine stack dump.
const maxGoroutineStackSize = 64 * 1024 // 64KB

// GoroutineStacks returns a string containing the stack traces of all
// goroutines, truncated to 64KB. This is useful for diagnosing hangs
// that occur in go-fuse internals or the kernel driver rather than in
// our FUSE method implementations.
func GoroutineStacks() string {
	buf := make([]byte, maxGoroutineStackSize)
	n := runtime.Stack(buf, true)
	s := string(buf[:n])
	if n >= maxGoroutineStackSize {
		s += "\n... truncated at 64KB ...\n"
	}
	return s
}
