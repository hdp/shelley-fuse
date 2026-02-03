---
id: sf-9fiu
status: closed
deps: []
links: []
created: 2026-02-03T02:45:34Z
type: bug
priority: 1
---
# Integration tests interfere with each other when multiple test runs share /tmp, causing FUSE mount deadlocks and zombie processes


## Notes

**2026-02-03T02:45:40Z**

Multiple concurrent test runs (from agent subagents) spawn shelley servers and FUSE mounts that interfere. Dozens of zombie fuse.test and shelley processes accumulate, with some fuse.test processes stuck in D state. Tests use /tmp as cwd via exec.Command which can cause cross-test FUSE mount interactions. Need to ensure each test run uses isolated temp dirs for both shelley server state and FUSE mounts, and properly cleans up on failure.

**2026-02-03T03:13:38Z**

Dozens of zombie fuse.test and shelley processes accumulated from agent test runs. Several fuse.test processes stuck in D state (uninterruptible sleep) waiting on dead FUSE mounts. Root cause: integration tests that write to conversation/new trigger a FUSE-level deadlock where Flush (calling StartConversation) blocks concurrently with a kernel-triggered ReadDirPlus (calling ListConversations). When tests time out, the FUSE mount cleanup in t.Cleanup does fusermount -uz but the D-state processes can't be killed. The agent also ran pkill -9 shelley which killed the main shelley server. The test isolation (t.TempDir for mounts, random ports for servers) is adequate - the core issue is the FUSE deadlock, not test interference.

**2026-02-03T04:20:21Z**

Investigation findings:

1. No direct mutex deadlock found in filesystem.go or state.go
2. HTTP calls in ListConversations and StartConversation don't hold application-level locks
3. CachingClient properly releases locks before HTTP calls
4. state.Store uses proper lock ordering (no nested lock acquisition)

The 'FUSE-level deadlock' may actually be:
- Kernel-side contention when entry/attr timeouts are 0 (every op revalidates)
- HTTP server bottleneck when concurrent requests are made
- Or a more subtle issue in how go-fuse handles concurrent ReadDirPlus and Flush

Need to investigate further or try to reproduce with specific test scenarios.

**2026-02-03T04:23:27Z**

Fixed CachingClient race condition causing thundering herd on cache miss.

The issue: In ListConversations(), GetConversation(), and ListModels(), the caching pattern was:
1. RLock, check cache, RUnlock
2. If miss: make HTTP call (no lock held)
3. Lock, write to cache, Unlock

This allowed multiple goroutines to see a cache miss simultaneously and all make duplicate HTTP calls to the backend. When concurrent FUSE operations (like ReadDirPlus and Flush) both triggered cache misses, they would race to make HTTP calls, causing excessive load on the Shelley server.

The fix: Implemented double-checked locking pattern:
1. Fast path: RLock, check cache, RUnlock - return on hit
2. Slow path: Lock, check cache again (another goroutine may have filled it)
3. If still miss: make HTTP call while holding lock
4. Write to cache, then Unlock

This ensures only one goroutine makes an HTTP call for each cache miss, preventing the thundering herd problem and reducing contention between concurrent FUSE operations.

**2026-02-03T04:28:56Z**

Improved fix: Use singleflight instead of double-checked locking.

The initial fix using double-checked locking had a critical flaw: it held the 
write lock during HTTP calls. This could cause the same D-state problem we were
trying to fix - if one goroutine's HTTP call was slow, all other goroutines
would block waiting for the lock.

The proper fix uses golang.org/x/sync/singleflight to coalesce duplicate requests:
1. Fast path: Check cache with RLock, return on hit
2. Slow path: Use singleflight.Do() with a unique key per resource
3. Inside singleflight: Make HTTP call (no lock held), then Lock briefly to update cache

Benefits of singleflight:
- Only one HTTP call per cache key, even with concurrent misses
- No locks held during HTTP calls
- Other goroutines waiting for same key get result without making duplicate calls
- Different cache keys can proceed independently (e.g., ListConversations vs GetConversation)

This prevents both the thundering herd problem AND the D-state issue from holding
locks during slow HTTP calls.
