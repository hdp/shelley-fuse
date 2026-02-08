---
id: sf-xt31
status: closed
deps: []
links: []
created: 2026-02-05T04:54:35Z
type: task
priority: 2
---
# Write integration tests that exercise README.md example flows


## Notes

**2026-02-05T04:54:46Z**

This ticket replaces lost ticket sf-g3l2 from the jj repo recovery.

The goal is to write integration tests that exercise the exact shell command flows documented in README.md's Quick Start and Common Operations sections.

Key flows to test:
1. Allocate new conversation: ID=$(cat new/clone)
2. Configure model and cwd via ctl file
3. Send first message to create conversation on backend
4. Read response via messages/all.md
5. Send follow-up message
6. List available models: ls models/
7. Check default model: readlink models/default
8. List conversations: ls conversation/
9. List last N messages: ls conversation/$ID/messages/last/5/
10. Read last N message contents: cat conversation/$ID/messages/last/5/*/content.md
11. List messages since user's last message: ls conversation/$ID/messages/since/user/1/
12. Get message count: cat conversation/$ID/messages/count
13. Check if conversation created: test -e conversation/$ID/created

Tests should use actual shell commands (via exec.Command with bash -c) rather than reimplementing the operations in Go test code. This validates the documented user experience.

**2026-02-07T02:30:12Z**

## Root Cause Analysis: Hung fuse.test Processes

### The Evidence

At time of diagnosis: **51 fuse.test processes in D state** (uninterruptible sleep), **36 zombies**, **14 stale FUSE mounts**, dating back to Feb 2.

### The Deadlock Cycle

Three interacting bugs in the test infrastructure combine to create unkillable hung processes:

#### Bug 1: runShell() has no timeout (readme_shell_test.go:11-19)

Every FUSE filesystem operation that hits the network (Flush->StartConversation/SendMessage, Readdir->ListConversations, Read->GetConversation, etc.) blocks on an HTTP call with a 2-minute timeout. Since runShell uses exec.Command with NO context and NO timeout, when the shelley server is slow or dead, the bash subprocess is stuck in a FUSE syscall (kernel D state) for up to 2 minutes, and the Go test blocks with it.

#### Bug 2: Cleanup has an unconditional infinite wait (integration_test.go:104-114)

When the 5-second graceful fssrv.Unmount() fails, the fallback uses fusermount -u (non-lazy). This fails if any process has open fds on the mount (e.g., a stuck bash subprocess from Bug 1). The code then does <-done with NO timeout, blocking the cleanup goroutine forever. fssrv.Unmount() also can't complete because the kernel won't unmount a busy filesystem.

#### Bug 3: Go test timeout doesn't run cleanup

When Go's test timeout fires (default 10 minutes), it calls panic("test timed out") which does NOT run t.Cleanup functions. The process exits without unmounting FUSE. The kernel mount remains. Any future process that touches that mount path enters D state.

### Why Multiple Worktrees Make It Worse

With N agents running tests concurrently across N worktrees:
1. Each test run starts its own shelley server + FUSE mount
2. Resource contention makes shelley servers slower to start/respond
3. More test runs = more chances for the deadlock cycle to trigger
4. Stale mounts from crashed tests can block t.TempDir() cleanup in others
5. Agents see hung tests, kill them with SIGKILL, leaving more stale mounts
6. The stale mount count grows monotonically, nothing cleans them up

### Fixes Needed

**Fix 1: Add timeout to runShell** - Use exec.CommandContext with a 30-second context.

**Fix 2: Use lazy unmount and bounded cleanup** - Change fusermount -u to fusermount -uz (lazy unmount detaches the mount immediately even if busy). Add a second timeout after fusermount so cleanup never blocks forever.

**Fix 3: Add a TestMain cleanup** - Clean up stale mounts from previous crashed runs at the start of each test suite.

### Immediate Cleanup Done

Lazy-unmounted all 13 stale test mounts and killed all hung processes.

**2026-02-08T21:09:47Z**

## Pre-existing Issues Found

1. **BASH_ENV breaks shell tests**: The VM has BASH_ENV=/home/exedev/.bash_env which sources a custom cd() function. This cd() returns exit code 1 when there's no .venv in parent dirs (because `[[ "$venv" ]] && source ...` returns 1 when $venv is empty). This causes all runShellDiag calls to fail because `cd <tmpdir>` returns 1. Fix: clear BASH_ENV in spawned commands.

2. **TestRunShellDiagOKSuccess and TestRunShellDiagTimeoutIncludesDump are broken** by the BASH_ENV issue. These aren't integration tests (no FUSE needed) but they still fail because cd() returns 1.

3. **Quick Start test doesn't exercise since/user/1/*/content.md** as documented - it reads all.md instead.

Plan:
- Fix BASH_ENV issue in runShellDiagTimeout by clearing it in cmd.Env
- Add missing shell tests for README operations: readlink model, touch/rm archived, last/1/0/content.md, since/user/1/*/content.md
