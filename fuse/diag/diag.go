// Package diag provides diagnostics for in-flight FUSE operations.
package diag

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Op represents a single in-flight FUSE operation.
type Op struct {
	ID       uint64
	Node     string // Type name of the FUSE node (e.g. "ConversationDirNode")
	Method   string // FUSE method (e.g. "Readdir", "Open", "Write")
	Detail   string // Free-form detail (e.g. path, conversation ID)
	Started  time.Time
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

// Track is a package-level helper that is nil-safe: if t is nil, it returns
// a no-op done function. This lets callers avoid nil checks.
func Track(t *Tracker, node, method, detail string) func() {
	if t == nil {
		return func() {}
	}
	return t.Track(node, method, detail)
}
