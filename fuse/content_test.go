package fuse

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"shelley-fuse/mockserver"
	"shelley-fuse/shelley"
)

func TestTimestamps_NestedQueryDirsUseConversationTime(t *testing.T) {
	// Test that nested query directories (since/user/) use conversation time
	server := mockConversationsServer(t, []shelley.Conversation{})
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)
	shelleyFS := NewFS(client, store, time.Hour)

	// Wait a bit so we can distinguish times
	time.Sleep(50 * time.Millisecond)

	// Clone a conversation
	convID, err := store.Clone()
	if err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	cs := store.Get(convID)
	convTime := cs.CreatedAt

	// Create mount
	tmpDir, err := ioutil.TempDir("", "shelley-fuse-nested-query-timestamp-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Test since/user directory (nested QueryDirNode)
	t.Run("SinceUserDirectory", func(t *testing.T) {
		info, err := os.Stat(filepath.Join(tmpDir, "conversation", convID, "messages", "since", "user"))
		if err != nil {
			t.Fatalf("Failed to stat since/user: %v", err)
		}
		mtime := info.ModTime()
		diff := mtime.Sub(convTime)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("since/user mtime %v differs from convTime %v by %v", mtime, convTime, diff)
		}
	})

}

func TestQueryResultDirNode_LastN(t *testing.T) {
	convID := "test-conv-last-n"
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("Hello")},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr("Hi there!")},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr("How are you?")},
		{MessageID: "m4", ConversationID: convID, SequenceID: 4, Type: "shelley", LLMData: strPtr("I'm great!")},
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-last-n-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Test last/2 - should contain symlinks to the last 2 messages
	last2Dir := filepath.Join(tmpDir, "conversation", localID, "messages", "last", "2")
	info, err := os.Stat(last2Dir)
	if err != nil {
		t.Fatalf("Failed to stat last/2: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("last/2 should be a directory")
	}

	entries, err := ioutil.ReadDir(last2Dir)
	if err != nil {
		t.Fatalf("Failed to read last/2: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Expected 2 entries in last/2, got %d", len(entries))
	}

	// Verify the entries are ordinal symlinks: 0, 1 (0 = oldest, 1 = newest of last 2)
	// last/2 should have entries "0" and "1"
	// 0 → ../../2-user (seqID 3, 0-indexed as 2)
	// 1 → ../../3-agent (seqID 4, 0-indexed as 3)
	expectedOrdinals := []string{"0", "1"}
	expectedTargets := []string{"../../2-user", "../../3-agent"}
	for i, e := range entries {
		if e.Mode()&os.ModeSymlink == 0 {
			t.Errorf("Expected symlink, got %s with mode %v", e.Name(), e.Mode())
		}
		if e.Name() != expectedOrdinals[i] {
			t.Errorf("Expected name %q, got %q", expectedOrdinals[i], e.Name())
		}
	}

	// Verify symlink targets are correct (../../{message-dir})
	for i, ordinal := range expectedOrdinals {
		target, err := os.Readlink(filepath.Join(last2Dir, ordinal))
		if err != nil {
			t.Errorf("Failed to readlink %s: %v", ordinal, err)
			continue
		}
		if target != expectedTargets[i] {
			t.Errorf("Symlink %s target = %q, want %q", ordinal, target, expectedTargets[i])
		}
	}

	// Verify we can read through the symlinks
	data, err := ioutil.ReadFile(filepath.Join(last2Dir, "0", "type"))
	if err != nil {
		t.Fatalf("Failed to read type through symlink 0: %v", err)
	}
	if strings.TrimSpace(string(data)) != "user" {
		t.Errorf("Expected type=user for ordinal 0, got %q", string(data))
	}

	data, err = ioutil.ReadFile(filepath.Join(last2Dir, "1", "type"))
	if err != nil {
		t.Fatalf("Failed to read type through symlink 1: %v", err)
	}
	if strings.TrimSpace(string(data)) != "shelley" {
		t.Errorf("Expected type=shelley for ordinal 1, got %q", string(data))
	}

	// Test last/3 - should contain 3 messages with ordinals 0, 1, 2
	last3Dir := filepath.Join(tmpDir, "conversation", localID, "messages", "last", "3")
	entries, err = ioutil.ReadDir(last3Dir)
	if err != nil {
		t.Fatalf("Failed to read last/3: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("Expected 3 entries in last/3, got %d", len(entries))
	}
	// Verify ordinals
	for i, e := range entries {
		expected := strconv.Itoa(i)
		if e.Name() != expected {
			t.Errorf("Expected ordinal %q in last/3, got %q", expected, e.Name())
		}
	}
}

// TestQueryResultDirNode_SincePersonN verifies that since/{person}/{N} returns
// a directory containing symlinks to messages after the Nth occurrence of that person.
func TestQueryResultDirNode_SincePersonN(t *testing.T) {
	convID := "test-conv-since-n"
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr("First")},
		{MessageID: "m2", ConversationID: convID, SequenceID: 2, Type: "shelley", LLMData: strPtr("Response 1")},
		{MessageID: "m3", ConversationID: convID, SequenceID: 3, Type: "user", UserData: strPtr("Second")},
		{MessageID: "m4", ConversationID: convID, SequenceID: 4, Type: "shelley", LLMData: strPtr("Response 2")},
		{MessageID: "m5", ConversationID: convID, SequenceID: 5, Type: "user", UserData: strPtr("Third")},
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	client := shelley.NewClient(server.URL)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(client, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-since-n-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Test since/user/2 - messages after the 2nd-to-last user message (2-user)
	// Should include: 3-agent, 4-user
	since2Dir := filepath.Join(tmpDir, "conversation", localID, "messages", "since", "user", "2")
	info, err := os.Stat(since2Dir)
	if err != nil {
		t.Fatalf("Failed to stat since/user/2: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("since/user/2 should be a directory")
	}

	entries, err := ioutil.ReadDir(since2Dir)
	if err != nil {
		t.Fatalf("Failed to read since/user/2: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Expected 2 entries in since/user/2, got %d", len(entries))
	}

	expectedNames := []string{"3-agent", "4-user"}
	for i, e := range entries {
		if e.Mode()&os.ModeSymlink == 0 {
			t.Errorf("Expected symlink, got %s with mode %v", e.Name(), e.Mode())
		}
		if e.Name() != expectedNames[i] {
			t.Errorf("Expected name %q, got %q", expectedNames[i], e.Name())
		}
	}

	// Verify symlink targets are correct (../../../{message-dir})
	for _, name := range expectedNames {
		target, err := os.Readlink(filepath.Join(since2Dir, name))
		if err != nil {
			t.Errorf("Failed to readlink %s: %v", name, err)
			continue
		}
		expectedTarget := "../../../" + name
		if target != expectedTarget {
			t.Errorf("Symlink %s target = %q, want %q", name, target, expectedTarget)
		}
	}

	// Test since/user/1 - messages after the last user message (4-user)
	// Should be empty since it's the last message
	since1Dir := filepath.Join(tmpDir, "conversation", localID, "messages", "since", "user", "1")
	entries, err = ioutil.ReadDir(since1Dir)
	if err != nil {
		t.Fatalf("Failed to read since/user/1: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("Expected 0 entries in since/user/1, got %d", len(entries))
	}

	// Verify we can read through symlinks
	data, err := ioutil.ReadFile(filepath.Join(since2Dir, "3-agent", "type"))
	if err != nil {
		t.Fatalf("Failed to read type through symlink: %v", err)
	}
	if strings.TrimSpace(string(data)) != "shelley" {
		t.Errorf("Expected type=shelley, got %q", string(data))
	}
}

func TestSinceDirLsDoesNotMakeExcessiveAPICalls(t *testing.T) {
	convID := "conv-since-perf"
	numMessages := 100

	// Build a conversation: 1 user message, then (numMessages-1) agent replies
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr(`{"Content":[{"Type":2,"Text":"Hello"}]}`)},
	}
	for i := 2; i <= numMessages; i++ {
		msgs = append(msgs, shelley.Message{
			MessageID:      fmt.Sprintf("m%d", i),
			ConversationID: convID,
			SequenceID:     i,
			Type:           "shelley",
			LLMData:        strPtr(fmt.Sprintf(`{"Content":[{"Type":2,"Text":"Reply %d"}]}`, i)),
		})
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	// Use CachingClient like production
	baseClient := shelley.NewClient(server.URL)
	cachingClient := shelley.NewCachingClient(baseClient, 5*time.Second)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(cachingClient, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-since-perf-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	// Reset counter after mount
	server.ResetFetchCount()

	// ls -l since/user/1/ — should list (numMessages-1) agent messages
	sincePath := filepath.Join(tmpDir, "conversation", localID, "messages", "since", "user", "1")
	entries, err := ioutil.ReadDir(sincePath)
	if err != nil {
		t.Fatalf("ReadDir on since/user/1/ failed: %v", err)
	}
	expected := numMessages - 1
	if len(entries) != expected {
		t.Fatalf("Expected %d entries in since/user/1/, got %d", expected, len(entries))
	}

	// The key assertion: API calls should be bounded, not O(N).
	// With proper caching, we expect at most a small constant number of fetches
	// (1 for the conversation data, possibly a couple more for cache misses).
	// Without the fix, this would be ~100+ fetches.
	fetches := server.FetchCount()
	t.Logf("GetConversation calls for ls -l since/user/1/ with %d entries: %d", expected, fetches)
	if fetches > 5 {
		t.Errorf("Too many GetConversation calls: %d (expected <= 5 with caching)", fetches)
	}
}

// TestSinceDirPerformance verifies that ReadDir on messages/ and
// since/{person}/{N}/ complete in reasonable absolute time for a 100-message
// conversation. This replaced a ratio-based test that became flaky after
// immutable-node caching made messages/ dramatically faster (kernel-cached
// Lstat results) while since/ still does per-entry FUSE roundtrips. Both
// paths are now orders of magnitude faster than the original O(N²) bug
// (~6s for messages/, ~35s for since/ with 150 messages). The absolute
// thresholds here are generous guards against gross regressions, not
// precision benchmarks.
func TestSinceDirPerformance(t *testing.T) {
	convID := "conv-perf-regression"
	numMessages := 100

	// Build a conversation: 1 user message followed by (numMessages-1) agent replies.
	// This ensures since/user/1/ returns (numMessages-1) entries.
	msgs := []shelley.Message{
		{MessageID: "m1", ConversationID: convID, SequenceID: 1, Type: "user", UserData: strPtr(`{"Content":[{"Type":2,"Text":"Hello"}]}`)},
	}
	for i := 2; i <= numMessages; i++ {
		msgs = append(msgs, shelley.Message{
			MessageID:      fmt.Sprintf("m%d", i),
			ConversationID: convID,
			SequenceID:     i,
			Type:           "shelley",
			LLMData:        strPtr(fmt.Sprintf(`{"Content":[{"Type":2,"Text":"Reply %d"}]}`, i)),
		})
	}

	server := mockserver.New(mockserver.WithConversation(convID, msgs))
	defer server.Close()

	baseClient := shelley.NewClient(server.URL)
	cachingClient := shelley.NewCachingClient(baseClient, 5*time.Second)
	store := testStore(t)

	localID, _ := store.Clone()
	store.MarkCreated(localID, convID, "")

	shelleyFS := NewFS(cachingClient, store, time.Hour)

	tmpDir, err := ioutil.TempDir("", "shelley-fuse-perf-regression")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	opts := &fs.Options{}
	entryTimeout := time.Duration(0)
	attrTimeout := time.Duration(0)
	negativeTimeout := time.Duration(0)
	opts.EntryTimeout = &entryTimeout
	opts.AttrTimeout = &attrTimeout
	opts.NegativeTimeout = &negativeTimeout

	fssrv, err := fs.Mount(tmpDir, shelleyFS, opts)
	if err != nil {
		t.Fatalf("Mount failed: %v", err)
	}
	defer fssrv.Unmount()

	messagesPath := filepath.Join(tmpDir, "conversation", localID, "messages")
	sincePath := filepath.Join(tmpDir, "conversation", localID, "messages", "since", "user", "1")

	// Warm up caches
	ioutil.ReadDir(messagesPath)
	ioutil.ReadDir(sincePath)

	const iterations = 5

	// Measure messages/ ReadDir
	var messagesTotal time.Duration
	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, err := ioutil.ReadDir(messagesPath)
		if err != nil {
			t.Fatalf("ReadDir messages/ failed: %v", err)
		}
		messagesTotal += time.Since(start)
	}

	// Measure since/user/1/ ReadDir
	var sinceTotal time.Duration
	for i := 0; i < iterations; i++ {
		start := time.Now()
		entries, err := ioutil.ReadDir(sincePath)
		if err != nil {
			t.Fatalf("ReadDir since/user/1/ failed: %v", err)
		}
		if len(entries) != numMessages-1 {
			t.Fatalf("Expected %d entries, got %d", numMessages-1, len(entries))
		}
		sinceTotal += time.Since(start)
	}

	messagesAvg := messagesTotal / time.Duration(iterations)
	sinceAvg := sinceTotal / time.Duration(iterations)

	t.Logf("messages/ avg: %v, since/user/1/ avg: %v", messagesAvg, sinceAvg)

	// Absolute thresholds: guard against gross regressions. The original bug
	// had ~6s for messages/ and ~35s for since/ with 150 messages. Current
	// performance is ~1-2ms and ~7-15ms respectively. A 500ms threshold per
	// path gives ~30x headroom and will only trip on a real regression.
	const maxAcceptable = 500 * time.Millisecond
	if messagesAvg > maxAcceptable {
		t.Errorf("messages/ avg %v exceeds %v threshold", messagesAvg, maxAcceptable)
	}
	if sinceAvg > maxAcceptable {
		t.Errorf("since/user/1/ avg %v exceeds %v threshold", sinceAvg, maxAcceptable)
	}
}
