---
id: sf-9fiu
status: open
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
