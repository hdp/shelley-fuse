---
id: sf-19kz
status: closed
deps: []
links: []
created: 2026-02-11T03:04:14Z
type: task
priority: 2
assignee: hdp
---
# Tests are hanging periodically again, now in ConversationFlow



## Notes

**2026-02-11T13:30:21Z**

Last time this happened it was because the shell tests set cmd.Dir to the mount point, which caused the parent process to hang before exec'ing. These tests don't use a shell, but there may be a similar issue, or it may be completely different e.g. some deadlock

**2026-02-11T13:56:28Z**

## Root cause analysis

### The smoking gun
lsof on a hung shelley server subprocess shows fd 17 open to the FUSE mount's conversation directory:

```
shelley PID exedev 17r unknown /tmp/TestConversationFlow.../002/mount/conversation
```

The fd has O_CLOEXEC set (flags 02100000), proving the *server process itself* opened it at runtime — it was NOT inherited via fork/exec. The server starts BEFORE the mount exists, confirming this.

### How the server opens the FUSE mount

When the shelley server handles `POST /api/conversations/new`, it calls:
1. `AcceptUserMessage` → `Hydrate` → `createSystemPrompt(cwd)`
2. `GenerateSystemPrompt("/tmp")` → `collectSystemData("/tmp")`
3. `collectGitInfo("/tmp")` fails (no git repo) → `searchRoot = "/tmp"`
4. `collectCodebaseInfo("/tmp", nil)` → `findAllGuidanceFiles("/tmp")`
5. `filepath.Walk("/tmp", ...)` — **walks ALL of /tmp recursively**

Since the test's FUSE mount lives under `/tmp/TestConversationFlow.../002/mount/`, the walk enters the FUSE mount and opens directories like `mount/conversation/`.

### The deadlock

1. Test writes to `send` → Flush → HTTP POST (StartConversation) to shelley server
2. Server handles POST → system prompt generation → `filepath.Walk("/tmp")`
3. Walk enters FUSE mount directory → kernel sends FUSE Readdir back to test process
4. FUSE Readdir handler calls HTTP GET (ListConversations) to server
5. Server CAN handle the GET on another goroutine (no server-side deadlock)
6. But walk continues DEEPER into the mount tree, triggering more FUSE ops
7. Each FUSE op (Getattr, Readdir, Lookup on conversation content) triggers more HTTP calls
8. Walk's 5-second context timeout only fires in the callback — if a syscall (os.Lstat/os.Open) blocks on a FUSE path, the timeout never triggers
9. Eventually a FUSE operation blocks indefinitely (likely hitting a message content read that calls GetConversation, which may compete with the in-flight StartConversation for some server resource)

The "periodically" nature of the hang is explained by timing: the walk must reach the FUSE mount during the window when the POST is still in-flight. The predictable model is fast, so usually the POST completes before the walk gets there. But not always.

### Evidence across multiple hung processes

- PID 444293: fd 17 → `/tmp/TestConversationFlow1930086601/002/mount/conversation` (full absolute path)
- PID 442830: fd 17 → `/conversation` (relative path, likely after unmount made the original path invalid)

Both show the identical fd number (17) and the identical pattern: opened after the TCP sockets (fd 15, 18) during request processing.

### Fix options

**Quick fix in FUSE tests:** Change `cwd=/tmp` to a dedicated temp directory that doesn't contain FUSE mounts. E.g.:
```go
cwdDir := t.TempDir() // creates /tmp/TestConversationFlow.../003
ioutil.WriteFile(ctlPath, []byte("model=predictable cwd="+cwdDir), 0644)
```

**Also affects createConversation helper:** When cwd is empty, the server falls back to os.Getwd() which is the `fuse/` package directory. That's not under /tmp, so it's safe. But setting an explicit safe cwd in all test helpers would be defensive.

**Ideal server-side fix (separate ticket):** `findAllGuidanceFiles` should not walk into FUSE mounts. Could skip them by checking mount type, or use a more targeted walk strategy. The 5s timeout also doesn't work because `filepath.Walk` blocks in syscalls, not in the callback.

**2026-02-11T14:30:30Z**

## Fix committed

Committed fbd29a1: two changes that prevent the shelley server from walking
into the FUSE mount during system prompt generation.

## Remaining risks

These fixes address the known trigger (cwd=/tmp + server guidance file walk)
but the underlying architecture is fragile: the FUSE daemon runs in the same
process as the tests, so any code path that touches the mount can self-deadlock.

Created follow-up tickets:
- s1-6w1p: Run FUSE daemon in separate process (eliminates the class of bug)
- s1-6c6x: Add phase tracking to diag tracker (better diagnostics when hangs do occur)
