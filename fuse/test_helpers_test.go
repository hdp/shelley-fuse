package fuse

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"shelley-fuse/fuse/diag"
	"shelley-fuse/mockserver"
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

func testStore(t *testing.T) *state.Store {
	t.Helper()
	s, err := state.NewStore(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func strPtr(s string) *string { return &s }

// statIno extracts the inode number from an os.FileInfo via the underlying syscall.Stat_t.
func statIno(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

func mockConversationsServer(t *testing.T, conversations []shelley.Conversation) *mockserver.Server {
	t.Helper()
	opts := make([]mockserver.Option, len(conversations))
	for i, c := range conversations {
		opts[i] = mockserver.WithFullConversation(c, nil)
	}
	return mockserver.New(opts...)
}

// mockErrorServer creates a test server that returns errors for the conversations endpoint
func mockErrorServer(t *testing.T) *mockserver.Server {
	t.Helper()
	return mockserver.New(mockserver.WithErrorMode(http.StatusInternalServerError))
}

func mountTestFSWithServer(t *testing.T, server *mockserver.Server, store *state.Store) (string, func()) {
	t.Helper()

	client := shelley.NewClient(server.URL)
	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Mount failed: %v", err)
	}

	return tmpDir, func() {
		fssrv.Unmount()
		os.RemoveAll(tmpDir)
	}
}

func mockModelsServer(t *testing.T, models []shelley.Model) *mockserver.Server {
	t.Helper()
	return mockModelsServerWithDefault(t, models, "")
}

// mockModelsServerWithDefault creates a test server that returns mock model data with an optional default model
func mockModelsServerWithDefault(t *testing.T, models []shelley.Model, defaultModel string) *mockserver.Server {
	t.Helper()
	opts := []mockserver.Option{mockserver.WithModels(models)}
	if defaultModel != "" {
		opts = append(opts, mockserver.WithDefaultModel(defaultModel))
	}
	return mockserver.New(opts...)
}

// =============================================================================
// Shell Test Helpers
// =============================================================================

// filterEnv returns a copy of env with the named variables removed.
func filterEnv(env []string, names ...string) []string {
	result := make([]string, 0, len(env))
	for _, e := range env {
		exclude := false
		for _, name := range names {
			if strings.HasPrefix(e, name+"=") {
				exclude = true
				break
			}
		}
		if !exclude {
			result = append(result, e)
		}
	}
	return result
}

// runShellDiag executes a shell command like runShell, but accepts a
// diagURL string. If the command times out (context deadline exceeded)
// and diagURL is non-empty, the diag endpoint's output is fetched via
// HTTP and included in the returned error message to help diagnose
// stuck FUSE operations.
func runShellDiag(t *testing.T, dir, command string, diagURL string) (string, string, error) {
	t.Helper()
	return runShellDiagTimeout(t, dir, command, diagURL, 1*time.Second)
}

// runShellDiagTimeout is the implementation of runShellDiag with a
// configurable timeout, used for testing the timeout+dump path.
func runShellDiagTimeout(t *testing.T, dir, command string, diagURL string, timeout time.Duration) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Don't set cmd.Dir to the FUSE mount point.
	// Under the hood, os.StartProcess tries to stat the directory before running the command, which means we can deadlock before getting to cmd.Run().
	cmd := exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf("if ! cd \"%s\"; then echo \"cd %s: $?\" 1>&2; exit 1; fi; %s", dir, dir, command))
	// Clear BASH_ENV so that custom bash init scripts (e.g. a cd() wrapper)
	// don't interfere with our shell commands.
	cmd.Env = filterEnv(os.Environ(), "BASH_ENV")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil && diagURL != "" && ctx.Err() == context.DeadlineExceeded {
		dump, fetchErr := fetchDiag(diagURL)
		if fetchErr != nil {
			dump = fmt.Sprintf("(failed to fetch diag: %v)", fetchErr)
		}
		return stdout.String(), stderr.String(), fmt.Errorf("%w\n\ndiag dump:\n%s", err, dump)
	}
	return stdout.String(), stderr.String(), err
}

// runShellDiagOK executes a shell command and fails the test if it errors.
// On timeout, the failure message includes the diag endpoint's in-flight
// operation dump. Returns stdout.
func runShellDiagOK(t *testing.T, dir, command string, diagURL string) string {
	t.Helper()
	stdout, stderr, err := runShellDiag(t, dir, command, diagURL)
	if err != nil {
		t.Fatalf("Command failed: %s\nstdout: %s\nstderr: %s\nerror: %v", command, stdout, stderr, err)
	}
	return stdout
}

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
