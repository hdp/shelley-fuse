package fuse

import (
	"shelley-fuse/fuse/diag"
	"strings"
	"testing"
	"time"
)

// TestRunShellDiagTimeoutIncludesDump verifies that when a command times out,
// the error includes the diag tracker's in-flight operation dump and goroutine stacks.
func TestRunShellDiagTimeoutIncludesDump(t *testing.T) {
	tracker := diag.NewTracker()

	// Simulate a stuck FUSE operation.
	done := tracker.Track("SendNode", "Write", "conv=deadbeef")
	defer done()

	// Use a very short timeout so the "sleep" command is killed quickly.
	_, _, err := runShellDiagTimeout(t, t.TempDir(), "sleep 60", tracker, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "diag dump") {
		t.Errorf("timeout error should include diag dump, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "SendNode.Write conv=deadbeef") {
		t.Errorf("diag dump should show stuck op, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "goroutine stacks") {
		t.Errorf("timeout error should include goroutine stacks, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "goroutine ") {
		t.Errorf("goroutine stacks should contain actual stack traces, got: %s", errMsg)
	}
}

// TestRunShellDiagNonTimeoutNoDump verifies that non-timeout errors do NOT
// include the diag dump.
func TestRunShellDiagNonTimeoutNoDump(t *testing.T) {
	tracker := diag.NewTracker()
	done := tracker.Track("SendNode", "Write", "conv=deadbeef")
	defer done()

	_, _, err := runShellDiag(t, t.TempDir(), "exit 1", tracker)
	if err == nil {
		t.Fatal("expected error from exit 1")
	}
	if strings.Contains(err.Error(), "diag dump") {
		t.Error("non-timeout error should NOT include diag dump")
	}
}

// TestRunShellDiagNilTracker verifies that a nil tracker does not cause
// a panic, even on timeout.
func TestRunShellDiagNilTracker(t *testing.T) {
	_, _, err := runShellDiagTimeout(t, t.TempDir(), "sleep 60", nil, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	// Should not panic and should not contain diag dump.
	if strings.Contains(err.Error(), "diag dump") {
		t.Error("nil tracker should not produce diag dump")
	}
}

// TestRunShellDiagOKSuccess verifies the happy path.
func TestRunShellDiagOKSuccess(t *testing.T) {
	tracker := diag.NewTracker()
	out := runShellDiagOK(t, t.TempDir(), "echo hello", tracker)
	if strings.TrimSpace(out) != "hello" {
		t.Errorf("got %q, want %q", out, "hello\n")
	}
}
