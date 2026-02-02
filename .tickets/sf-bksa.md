---
id: sf-bksa
status: closed
deps: []
links: []
created: 2026-02-02T14:51:07Z
type: task
priority: 0
---
# cat file > .../conversation/{id}/new breaks on newline

When sending a message by writing to /new, don't assume a newline means the message is done. We should be able to tell when the filehandle is closed and send the message then.


## Notes

**2026-02-02T15:37:35Z**

This implementation is broken and the tests don't catch it. Sending to /new no longer creates a conversation.

**2026-02-02T15:40:16Z**

## Analysis

The current implementation has these issues:

1. `Open()` returns `nil` for the file handle - no state is maintained per-open
2. `Write()` immediately sends the message on each call
3. There's no `Release()` handler to send the message when the file is closed

The fix:
1. Create a `ConvNewFileHandle` struct that:
   - References the parent node (for client, state, localID)
   - Has a buffer to accumulate write data
2. `Open()` creates and returns this file handle
3. `Write()` appends data to the buffer
4. `Release()` sends the accumulated message to the backend

This ensures the message is sent when the file handle is closed, not on each write.
Also need to add a test that verifies multiline writes work correctly.

**2026-02-02T15:47:33Z**

## Implementation Complete

Fixed the /new file write behavior to buffer writes and send the message when the file handle is closed.

### Changes:
1. **fuse/filesystem.go**: 
   - Created `ConvNewFileHandle` struct that buffers writes
   - Changed from `NodeWriter` on the node to `FileWriter` on the file handle
   - Changed from `Release` (async) to `Flush` (sync) so callers block until message is sent
   - Added `flushed` flag to handle multiple Flush calls (dup'd file descriptors)
   - Fixed SendMessage to pass model from state to avoid default model issues

2. **fuse/integration_test.go**:
   - Added `TestMultilineWriteToNew` test that verifies:
     - Multiple Write() calls are buffered
     - Message is sent as a single unit when file is closed
     - Multiline content is preserved

### Key insight:
The original tests passed because they used `ioutil.WriteFile` which does open/write/close in sequence, making it appear to work. The real issue was that FUSE's Release callback is async (kernel sends it after close returns), so using Release meant the message wasn't guaranteed to be sent before close() returned. Using Flush fixes this since it's called synchronously during close().

**2026-02-02T15:49:44Z**

## Code Review - PASS

### Requirements Met:
✅ Buffers writes instead of sending on newline
✅ Sends message on close (via Flush, which is synchronous)
✅ Bug fixed - conversations are properly created

### Implementation Quality:
- Clean file handle pattern with ConvNewFileHandle struct
- Proper mutex protection for thread safety
- Handles edge cases:
  - Empty writes (returns 0, no message sent)
  - Multiple Flush calls (flushed flag for dup'd fds)
  - Missing state (returns ENOENT)
- Maintains FOPEN_DIRECT_IO for dynamic content
- Good choice of Flush over Release (sync vs async)

### Test Coverage:
- TestMultilineWriteToNew specifically tests multiple Write() calls
- Verifies message is sent as single unit on close
- TestConversationFlow validates basic conversation creation still works
- All tests pass (fresh run without cache)

### Commit:
Commit message is clear and includes ticket reference.

### Minor Observations:
- Added model parameter to SendMessage call (improvement over empty string)
- Removed NodeWriter interface assertion (correct since Write moved to handle)

Reviewed by: subagent

**2026-02-02T15:59:52Z**

## Fix Implemented

Moved `h.flushed = true` to AFTER the empty buffer check in `ConvNewFileHandle.Flush()`.

### Changes:
1. **fuse/filesystem.go**: One-line fix - moved `h.flushed = true` after the empty message check
2. **fuse/integration_test.go**: Added `TestShellRedirectToNew` that simulates shell redirect behavior (open/dup/close/write/close)

### Tests:
- All unit tests pass: `just test`
- All integration tests pass: `just test-integration`
- New test specifically validates the shell redirect pattern that was failing

### Root cause confirmed:
The `flushed` flag was set before checking if the buffer was empty. When bash does a shell redirect like `echo msg > file`, it:
1. Opens the file
2. Dups the fd
3. Closes the original (triggers empty Flush, set flushed=true)
4. Writes to dup'd fd
5. Closes dup'd fd (Flush skipped because flushed was already true)

Now the flag is only set when there's actual data to send.

**2026-02-02T16:01:41Z**

## Code Review - PASS

### Root Cause Analysis: CORRECT
The bug was that `h.flushed = true` was set BEFORE the empty buffer check. When bash does shell redirect:
1. open fd 3
2. dup2(3, 1) 
3. close(3) → triggers Flush with empty buffer, sets `flushed = true`
4. echo writes to fd 1
5. close(1) → Flush called but returns early because `flushed` is true

Moving `h.flushed = true` to AFTER the empty check fixes this. Empty flushes no longer "consume" the flag.

### Test Coverage: ADEQUATE
`TestShellRedirectToNew` precisely replicates the shell redirect pattern using syscall.Open/Dup/Close/Write/Close. It validates:
- The open/dup/close sequence (empty flush)
- Write to dup'd fd
- Close dup'd fd (should send message)
- Message actually arrives at server

### Edge Case Analysis:

1. **Multiple empty Flush calls?** Safe - each returns 0 without setting `flushed`. No infinite loop possible since Flush is kernel-triggered on close.

2. **Empty flush → data → flush → more data → flush?** 
   - Empty flush: returns 0, `flushed` stays false ✓
   - Write data
   - Flush: sends, sets `flushed = true` ✓
   - More writes
   - Flush: returns early (flushed=true), NO double-send ✓
   This is correct for `/new` semantics - one message per file handle.

3. **Concurrent access?** Protected by `h.mu` mutex.

### Code Quality: EXCELLENT
- Minimal change (one line moved)
- Clear comments explaining why
- Diff is easy to review

### Tests: ALL PASS
```
go test ./... -count=1
ok  shelley-fuse/fuse    2.804s
ok  shelley-fuse/shelley 0.158s
ok  shelley-fuse/state   0.011s
```

Reviewed by: code-review-subagent
