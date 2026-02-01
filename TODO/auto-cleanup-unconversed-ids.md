# Auto-cleanup of unconversed local IDs

## Problem

When a client reads `/new/clone`, `CloneNode.Open()` (`fuse/filesystem.go:231-265`) calls `state.Store.Clone()` (`state/state.go:52-70`), which immediately allocates an 8-char hex ID and persists a `ConversationState{Created: false}` to `~/.shelley-fuse/state.json`. If the client crashes or abandons the flow before writing to `new`, these uncreated entries pile up in state forever.

Additionally, these uncreated conversations currently appear in `ls /conversation` because `ConversationListNode.Readdir()` (`fuse/filesystem.go:357-430`) calls `state.ListMappings()` which returns all entries regardless of `Created` status. The stale-filtering logic (around line 390) only filters entries whose `ShelleyConversationID` is no longer on the server — uncreated entries have an empty `ShelleyConversationID` and pass through the `cs.ShelleyConversationID == ""` branch unconditionally.

## Implementation plan

### Part A: Hide uncreated conversations from `ls /conversation`

1. **Modify `ConversationListNode.Readdir()` in `fuse/filesystem.go`** (around line 390): In the filtering loop over `mappings`, add a condition to exclude entries where `cs.Created == false`. These are conversations that have been cloned but never had a first message sent. They should be accessible via `Lookup` (so the client can still write to `/conversation/{id}/ctl` and `/conversation/{id}/new`) but not listed in directory output.

2. **No changes to `Lookup`**: The `ConversationListNode.Lookup()` method (around line 280) should continue to resolve uncreated local IDs so the clone→ctl→new workflow still functions. Lookup already calls `state.Get(name)` and returns a `ConversationNode` if found, regardless of `Created` status.

### Part B: Auto-cleanup after timeout

3. **Add a `-clone-timeout` flag to `cmd/shelley-fuse/main.go`**: Default 1 hour. This is the duration after which an uncreated conversation is eligible for cleanup. Add it as a `flag.Duration` alongside the existing `-debug` flag. Pass it through to `NewFS()`.

4. **Update `shelleyfuse.NewFS()` in `fuse/filesystem.go`** to accept the timeout value. Store it on the `FS` struct (or on `CloneNode` / a new cleanup goroutine). The `FS` struct is defined around line 25.

5. **Add a `state.Store.Delete(id string) error` method to `state/state.go`**: Removes a conversation from the map and persists. Should only delete if `cs.Created == false` as a safety check — never auto-delete a created conversation.

6. **Add a cleanup goroutine**: Start it in `NewFS()` or in the main function after mount. It should periodically (e.g., every 60s) scan `state.ListMappings()`, find entries where `Created == false` and `time.Since(CreatedAt) > timeout`, and call `state.Delete(id)` on them. Use a `context.Context` from the FUSE server lifecycle so the goroutine stops on unmount.

7. **Alternative simpler approach — lazy cleanup in Readdir**: Instead of a goroutine, perform cleanup directly in `ConversationListNode.Readdir()`. When iterating mappings and finding `Created == false` entries older than the timeout, delete them from state on the spot. This avoids goroutine lifecycle management but means cleanup only happens when someone runs `ls`. Either approach works; the goroutine is more correct but the lazy approach is simpler.

### Testing

8. **Unit test in `state/state_test.go`**: Test `Delete()` — creates a conversation via `Clone()`, verifies it exists, deletes it, verifies it's gone. Also test that `Delete()` refuses to delete a created conversation.

9. **Integration test in `fuse/integration_test.go`**: Clone a conversation, verify it does NOT appear in `ls /conversation`, write to `new`, verify it now appears. For timeout testing, use a very short timeout (e.g., 100ms), clone, wait, verify the ID is gone from state.

## Key files

- `fuse/filesystem.go` — `ConversationListNode.Readdir()` (hiding), `FS` struct and `NewFS()` (timeout param, cleanup goroutine)
- `state/state.go` — new `Delete()` method
- `cmd/shelley-fuse/main.go` — new `-clone-timeout` flag, pass to `NewFS()`
- `state/state_test.go` — unit tests for `Delete()`
- `fuse/integration_test.go` — integration tests for hiding and cleanup

## Key structs

- `ConversationState` in `state/state.go:16-24` — `Created bool` and `CreatedAt time.Time` are the relevant fields
- `Store` in `state/state.go:27-31` — holds `Conversations map[string]*ConversationState`
