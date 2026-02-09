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
	Started time.Time
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

// Track records the start of a FUSE operation and returns a done function
// that must be called when the operation completes.
func (t *Tracker) Track(node, method, detail string) func() {
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
	return func() {
		t.mu.Lock()
		delete(t.ops, id)
		t.mu.Unlock()
	}
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
		if op.Detail != "" {
			fmt.Fprintf(&b, "  [%d] %s.%s %s (%s)\n", op.ID, op.Node, op.Method, op.Detail, elapsed)
		} else {
			fmt.Fprintf(&b, "  [%d] %s.%s (%s)\n", op.ID, op.Node, op.Method, elapsed)
		}
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
// a no-op done function. This lets callers avoid nil checks.
func Track(t *Tracker, node, method, detail string) func() {
	if t == nil {
		return func() {}
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
