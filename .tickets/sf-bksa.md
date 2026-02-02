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
