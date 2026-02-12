package fuse

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"shelley-fuse/fuse/diag"
	"strings"
	"testing"
	"time"
)

// startDiagServer starts an httptest server serving a diag.Tracker and
// returns the diag URL (e.g. "http://127.0.0.1:PORT/diag").
func startDiagServer(t *testing.T, tracker *diag.Tracker) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/diag", tracker.Handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return fmt.Sprintf("%s/diag", srv.URL)
}

// TestRunShellDiagTimeoutIncludesDump verifies that when a command times out,
// the error includes the diag endpoint's in-flight operation dump.
func TestRunShellDiagTimeoutIncludesDump(t *testing.T) {
	tracker := diag.NewTracker()

	// Simulate a stuck FUSE operation.
	h := tracker.Track("SendNode", "Write", "conv=deadbeef")
	defer h.Done()

	diagURL := startDiagServer(t, tracker)

	// Use a very short timeout so the "sleep" command is killed quickly.
	_, _, err := runShellDiagTimeout(t, t.TempDir(), "sleep 60", diagURL, 100*time.Millisecond)
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
}

// TestRunShellDiagNonTimeoutNoDump verifies that non-timeout errors do NOT
// include the diag dump.
func TestRunShellDiagNonTimeoutNoDump(t *testing.T) {
	tracker := diag.NewTracker()
	h := tracker.Track("SendNode", "Write", "conv=deadbeef")
	defer h.Done()

	diagURL := startDiagServer(t, tracker)

	_, _, err := runShellDiag(t, t.TempDir(), "exit 1", diagURL)
	if err == nil {
		t.Fatal("expected error from exit 1")
	}
	if strings.Contains(err.Error(), "diag dump") {
		t.Error("non-timeout error should NOT include diag dump")
	}
}

// TestRunShellDiagEmptyURL verifies that an empty diagURL does not cause
// a panic, even on timeout.
func TestRunShellDiagEmptyURL(t *testing.T) {
	_, _, err := runShellDiagTimeout(t, t.TempDir(), "sleep 60", "", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	// Should not panic and should not contain diag dump.
	if strings.Contains(err.Error(), "diag dump") {
		t.Error("empty diagURL should not produce diag dump")
	}
}

// TestRunShellDiagOKSuccess verifies the happy path.
func TestRunShellDiagOKSuccess(t *testing.T) {
	tracker := diag.NewTracker()
	diagURL := startDiagServer(t, tracker)
	out := runShellDiagOK(t, t.TempDir(), "echo hello", diagURL)
	if strings.TrimSpace(out) != "hello" {
		t.Errorf("got %q, want %q", out, "hello\n")
	}
}
