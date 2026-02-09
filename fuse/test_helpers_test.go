package fuse

import (
	"io/ioutil"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
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
