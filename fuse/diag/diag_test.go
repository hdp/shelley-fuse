package diag

import (
	"strings"
	"testing"
	"time"
)

func TestTrackAndDone(t *testing.T) {
	tr := NewTracker()

	done := tr.Track("ConversationDirNode", "Readdir", "/conversation")
	ops := tr.InFlight()
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].Node != "ConversationDirNode" {
		t.Errorf("node = %q, want ConversationDirNode", ops[0].Node)
	}
	if ops[0].Method != "Readdir" {
		t.Errorf("method = %q, want Readdir", ops[0].Method)
	}
	if ops[0].Detail != "/conversation" {
		t.Errorf("detail = %q, want /conversation", ops[0].Detail)
	}
	if ops[0].ID == 0 {
		t.Error("expected non-zero ID")
	}
	if ops[0].Started.IsZero() {
		t.Error("expected non-zero Started")
	}

	done()
	ops = tr.InFlight()
	if len(ops) != 0 {
		t.Fatalf("expected 0 ops after done, got %d", len(ops))
	}
}

func TestDoneIdempotent(t *testing.T) {
	tr := NewTracker()
	done := tr.Track("X", "Y", "")
	done()
	done() // should not panic
	if len(tr.InFlight()) != 0 {
		t.Fatal("expected 0 ops")
	}
}

func TestInFlightSortedByStartTime(t *testing.T) {
	tr := NewTracker()

	// Inject ops with controlled timestamps by manipulating directly.
	now := time.Now()
	tr.mu.Lock()
	tr.ops[3] = Op{ID: 3, Node: "C", Method: "M", Started: now.Add(2 * time.Second)}
	tr.ops[1] = Op{ID: 1, Node: "A", Method: "M", Started: now}
	tr.ops[2] = Op{ID: 2, Node: "B", Method: "M", Started: now.Add(1 * time.Second)}
	tr.mu.Unlock()

	ops := tr.InFlight()
	if len(ops) != 3 {
		t.Fatalf("expected 3 ops, got %d", len(ops))
	}
	if ops[0].Node != "A" || ops[1].Node != "B" || ops[2].Node != "C" {
		t.Errorf("wrong order: %v, %v, %v", ops[0].Node, ops[1].Node, ops[2].Node)
	}
}

func TestInFlightSameTimeSortsByID(t *testing.T) {
	tr := NewTracker()
	now := time.Now()
	tr.mu.Lock()
	tr.ops[5] = Op{ID: 5, Node: "B", Method: "M", Started: now}
	tr.ops[2] = Op{ID: 2, Node: "A", Method: "M", Started: now}
	tr.mu.Unlock()

	ops := tr.InFlight()
	if ops[0].ID != 2 || ops[1].ID != 5 {
		t.Errorf("expected ID order 2,5 got %d,%d", ops[0].ID, ops[1].ID)
	}
}

func TestMultipleOps(t *testing.T) {
	tr := NewTracker()
	d1 := tr.Track("A", "Open", "")
	d2 := tr.Track("B", "Write", "data")
	d3 := tr.Track("C", "Read", "")

	if len(tr.InFlight()) != 3 {
		t.Fatal("expected 3 ops")
	}

	d2()
	ops := tr.InFlight()
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(ops))
	}
	for _, op := range ops {
		if op.Node == "B" {
			t.Error("B should have been removed")
		}
	}

	d1()
	d3()
	if len(tr.InFlight()) != 0 {
		t.Fatal("expected 0 ops")
	}
}

func TestDumpEmpty(t *testing.T) {
	tr := NewTracker()
	out := tr.Dump()
	if !strings.Contains(out, "no in-flight") {
		t.Errorf("unexpected dump for empty tracker: %q", out)
	}
}

func TestDumpWithOps(t *testing.T) {
	tr := NewTracker()
	d1 := tr.Track("SendNode", "Write", "conv=abc123")
	d2 := tr.Track("CtlNode", "Read", "")
	defer d1()
	defer d2()

	out := tr.Dump()
	if !strings.Contains(out, "2 in-flight operation(s):") {
		t.Errorf("expected count line, got: %q", out)
	}
	if !strings.Contains(out, "SendNode.Write conv=abc123") {
		t.Errorf("expected SendNode.Write detail, got: %q", out)
	}
	if !strings.Contains(out, "CtlNode.Read (") {
		t.Errorf("expected CtlNode.Read without detail, got: %q", out)
	}
}

func TestDumpFormatWithDetail(t *testing.T) {
	tr := NewTracker()
	now := time.Now()
	tr.mu.Lock()
	tr.ops[1] = Op{ID: 1, Node: "N", Method: "M", Detail: "d", Started: now}
	tr.mu.Unlock()
	out := tr.Dump()
	// Format: "  [1] N.M d (0s)"
	if !strings.Contains(out, "[1] N.M d (") {
		t.Errorf("unexpected format: %q", out)
	}
}

func TestDumpFormatWithoutDetail(t *testing.T) {
	tr := NewTracker()
	now := time.Now()
	tr.mu.Lock()
	tr.ops[1] = Op{ID: 1, Node: "N", Method: "M", Detail: "", Started: now}
	tr.mu.Unlock()
	out := tr.Dump()
	// Format: "  [1] N.M (0s)"
	if !strings.Contains(out, "[1] N.M (") {
		t.Errorf("unexpected format: %q", out)
	}
	// Should NOT have double space before paren
	if strings.Contains(out, "N.M  (") {
		t.Errorf("double space in output: %q", out)
	}
}

func TestPackageLevelTrackNil(t *testing.T) {
	// Should not panic with nil tracker
	done := Track(nil, "Node", "Method", "detail")
	done() // no-op, should not panic
}

func TestPackageLevelTrackNonNil(t *testing.T) {
	tr := NewTracker()
	done := Track(tr, "Node", "Method", "detail")
	if len(tr.InFlight()) != 1 {
		t.Fatal("expected 1 op")
	}
	done()
	if len(tr.InFlight()) != 0 {
		t.Fatal("expected 0 ops")
	}
}

func TestIDsAreUnique(t *testing.T) {
	tr := NewTracker()
	var dones []func()
	for i := 0; i < 100; i++ {
		dones = append(dones, tr.Track("N", "M", ""))
	}
	ops := tr.InFlight()
	seen := make(map[uint64]bool)
	for _, op := range ops {
		if seen[op.ID] {
			t.Fatalf("duplicate ID: %d", op.ID)
		}
		seen[op.ID] = true
	}
	for _, d := range dones {
		d()
	}
}
