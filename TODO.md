# TODO

## 1. Human-readable Markdown for message lists

### Problem

`FormatMarkdown()` in `shelley/messages.go:26-39` currently outputs `## {type}` headers followed by the raw content from `UserData`/`LLMData` fields. The `messageContent()` helper (`shelley/messages.go:109-117`) just returns the raw string pointer value. In practice the content stored in `LLMData` is often a JSON blob (the raw API response), so reading `all.md` produces something like:

```
## user

Hello

## shelley

{"content": "Hi there! How can I help?", ...}
```

The goal is human-readable output like:

```
## user

Hello

## shelley

Hi there! How can I help?
```

### Where to change

All markdown formatting flows through a single function: `FormatMarkdown()` in `shelley/messages.go`. The `messageContent()` helper extracts raw content from `Message.UserData` or `Message.LLMData` pointer fields.

The `.md` format is selected in `fuse/filesystem.go` inside `ConvContentNode.formatResult()` (around line 950) which calls `shelley.FormatMarkdown(filtered)`. All `.md` views go through this path: `all.md`, `{N}.md`, `last/{N}.md`, `since/{person}/{N}.md`, `from/{person}/{N}.md`.

### Implementation plan

1. **Investigate the actual content of `LLMData`**: Start the Shelley server (`just dev`), create a conversation, and inspect what's actually stored in `LLMData` by reading the `.json` views. Determine whether it's always a JSON string, always plain text, or varies. This drives the parsing approach.

2. **Update `messageContent()` in `shelley/messages.go`** (or add a new `messageContentMarkdown()` variant): If `LLMData` contains JSON, parse it and extract the text content field. If it's already plain text, return it as-is. The function should be defensive — if JSON parsing fails, fall back to returning the raw string so nothing breaks.

3. **Consider role display names**: The current code uses `m.Type` directly as the header (e.g., `## shelley`, `## user`). Optionally capitalize or map these to friendlier names (e.g., `## User`, `## Shelley`). This is a minor polish item.

4. **Update unit tests in `shelley/messages_test.go`**: The existing `FormatMarkdown` tests check for `## user` and `## shelley` headers. Update them to verify the content is human-readable text, not JSON blobs.

5. **Update integration tests in `fuse/integration_test.go`**: The `TestPlan9Flow` subtest around line 380-503 checks for `##` in markdown output. Update assertions to verify human-readable content.

### Key files

- `shelley/messages.go` — `FormatMarkdown()`, `messageContent()` (primary change)
- `shelley/client.go` — `Message` struct definition (lines 48-57), shows `LLMData *string` and `UserData *string`
- `fuse/filesystem.go` — `ConvContentNode.formatResult()` (calls `FormatMarkdown`)
- `shelley/messages_test.go` — unit tests for formatting
- `fuse/integration_test.go` — integration tests that check `.md` output

---

## 2. Auto-cleanup of unconversed local IDs

### Problem

When a client reads `/new/clone`, `CloneNode.Open()` (`fuse/filesystem.go:231-265`) calls `state.Store.Clone()` (`state/state.go:52-70`), which immediately allocates an 8-char hex ID and persists a `ConversationState{Created: false}` to `~/.shelley-fuse/state.json`. If the client crashes or abandons the flow before writing to `new`, these uncreated entries pile up in state forever.

Additionally, these uncreated conversations currently appear in `ls /conversation` because `ConversationListNode.Readdir()` (`fuse/filesystem.go:357-430`) calls `state.ListMappings()` which returns all entries regardless of `Created` status. The stale-filtering logic (around line 390) only filters entries whose `ShelleyConversationID` is no longer on the server — uncreated entries have an empty `ShelleyConversationID` and pass through the `cs.ShelleyConversationID == ""` branch unconditionally.

### Implementation plan

#### Part A: Hide uncreated conversations from `ls /conversation`

1. **Modify `ConversationListNode.Readdir()` in `fuse/filesystem.go`** (around line 390): In the filtering loop over `mappings`, add a condition to exclude entries where `cs.Created == false`. These are conversations that have been cloned but never had a first message sent. They should be accessible via `Lookup` (so the client can still write to `/conversation/{id}/ctl` and `/conversation/{id}/new`) but not listed in directory output.

2. **No changes to `Lookup`**: The `ConversationListNode.Lookup()` method (around line 280) should continue to resolve uncreated local IDs so the clone→ctl→new workflow still functions. Lookup already calls `state.Get(name)` and returns a `ConversationNode` if found, regardless of `Created` status.

#### Part B: Auto-cleanup after timeout

3. **Add a `-clone-timeout` flag to `cmd/shelley-fuse/main.go`**: Default 30 seconds. This is the duration after which an uncreated conversation is eligible for cleanup. Add it as a `flag.Duration` alongside the existing `-debug` flag. Pass it through to `NewFS()`.

4. **Update `shelleyfuse.NewFS()` in `fuse/filesystem.go`** to accept the timeout value. Store it on the `FS` struct (or on `CloneNode` / a new cleanup goroutine). The `FS` struct is defined around line 25.

5. **Add a `state.Store.Delete(id string) error` method to `state/state.go`**: Removes a conversation from the map and persists. Should only delete if `cs.Created == false` as a safety check — never auto-delete a created conversation.

6. **Add a cleanup goroutine**: Start it in `NewFS()` or in the main function after mount. It should periodically (e.g., every 10s) scan `state.ListMappings()`, find entries where `Created == false` and `time.Since(CreatedAt) > timeout`, and call `state.Delete(id)` on them. Use a `context.Context` from the FUSE server lifecycle so the goroutine stops on unmount.

7. **Alternative simpler approach — lazy cleanup in Readdir**: Instead of a goroutine, perform cleanup directly in `ConversationListNode.Readdir()`. When iterating mappings and finding `Created == false` entries older than the timeout, delete them from state on the spot. This avoids goroutine lifecycle management but means cleanup only happens when someone runs `ls`. Either approach works; the goroutine is more correct but the lazy approach is simpler.

#### Testing

8. **Unit test in `state/state_test.go`**: Test `Delete()` — creates a conversation via `Clone()`, verifies it exists, deletes it, verifies it's gone. Also test that `Delete()` refuses to delete a created conversation.

9. **Integration test in `fuse/integration_test.go`**: Clone a conversation, verify it does NOT appear in `ls /conversation`, write to `new`, verify it now appears. For timeout testing, use a very short timeout (e.g., 100ms), clone, wait, verify the ID is gone from state.

### Key files

- `fuse/filesystem.go` — `ConversationListNode.Readdir()` (hiding), `FS` struct and `NewFS()` (timeout param, cleanup goroutine)
- `state/state.go` — new `Delete()` method
- `cmd/shelley-fuse/main.go` — new `-clone-timeout` flag, pass to `NewFS()`
- `state/state_test.go` — unit tests for `Delete()`
- `fuse/integration_test.go` — integration tests for hiding and cleanup

### Key structs

- `ConversationState` in `state/state.go:16-24` — `Created bool` and `CreatedAt time.Time` are the relevant fields
- `Store` in `state/state.go:27-31` — holds `Conversations map[string]*ConversationState`

---

## 3. Fix missing slugs for FUSE-created conversations

### Problem

Conversations created through the Shelley web UI get slugs, but those created through FUSE do not. There are two contributing issues:

**Issue A — The API may not return a slug at creation time.** `client.StartConversation()` in `shelley/client.go:72-124` POSTs to `/api/conversations/new` and decodes the response. The slug field is `*string` (nullable). If the API returns `null` or omits `slug`, `result.Slug` stays empty. `MarkCreated()` (`state/state.go:105-118`) then stores an empty slug. The Shelley backend likely generates slugs asynchronously (e.g., after the first AI response), so the slug isn't available at conversation creation time.

**Issue B — `AdoptWithSlug()` doesn't update slugs for already-tracked conversations.** `AdoptWithSlug()` in `state/state.go:184-215` checks if a conversation is already tracked by `ShelleyConversationID` (line 189-193). If found, it returns the existing local ID immediately **without updating the slug**, even though the comment on line 183 says it should "update the slug if it was previously empty." This means even when `Readdir()` calls `AdoptWithSlug()` with a fresh slug from the server listing, FUSE-created conversations that were adopted with an empty slug never get updated.

### Implementation plan

1. **Fix `AdoptWithSlug()` in `state/state.go:188-193`**: When an already-tracked conversation is found and the provided `slug` is non-empty but the stored `cs.Slug` is empty (or different), update `cs.Slug` and call `saveLocked()`. This is the critical fix — it allows slugs to be backfilled on subsequent `Readdir()` calls when the server listing includes the slug. Change:

   ```go
   // Current (broken):
   for _, cs := range s.Conversations {
       if cs.ShelleyConversationID == shelleyConversationID {
           return cs.LocalID, nil
       }
   }

   // Fixed:
   for _, cs := range s.Conversations {
       if cs.ShelleyConversationID == shelleyConversationID {
           if slug != "" && cs.Slug != slug {
               cs.Slug = slug
               _ = s.saveLocked()  // best-effort persist
           }
           return cs.LocalID, nil
       }
   }
   ```

2. **Verify the Shelley API behavior**: Determine whether `/api/conversations/new` ever returns a slug. If it does for web-created conversations but not FUSE-created ones, there may be a difference in request parameters. Compare the `ChatRequest` struct fields with what the web UI sends. If the API needs a specific field to trigger slug generation, add it to the request.

3. **Consider fetching the slug after creation**: If the API never returns a slug at creation time, add a follow-up call. After `StartConversation()` succeeds in `ConvNewNode.Write()` (`fuse/filesystem.go:670-690`), call `client.GetConversation(result.ConversationID)` to fetch the full conversation (which includes the slug) and update state. However, this may not help if the slug is generated asynchronously after the AI responds. The fix in step 1 handles this case — the slug will be picked up on the next `ls /conversation`.

4. **Update the `slug` file fallback**: `ConvMetaFieldNode.Read()` in `fuse/filesystem.go` (around line 796-818) currently returns `value + "\n"` even if `value` is empty, resulting in just a newline. Change it to return `ENOENT` when the slug is empty, consistent with the comment in CLAUDE.md that says "ENOENT before creation or if no slug":

   ```go
   case "slug":
       value = cs.Slug
       if value == "" {
           return nil, syscall.ENOENT
       }
   ```

5. **Update unit tests in `state/state_test.go`**: Add/update `TestAdoptWithSlugUpdatesEmptySlug` to verify that calling `AdoptWithSlug()` on an already-tracked conversation with a new slug actually updates the stored slug.

6. **Update integration tests in `fuse/integration_test.go`**: The test at `TestSymlinkWithSlug` (line 1040) currently skips if the slug is empty. After the fix, verify that the slug eventually appears (may need to trigger a Readdir to backfill it).

### Key files

- `state/state.go` — `AdoptWithSlug()` (primary fix, line 184-215), `MarkCreated()` (line 105-118)
- `shelley/client.go` — `StartConversation()` (line 72-124), `GetConversation()` (line 127-147)
- `fuse/filesystem.go` — `ConvNewNode.Write()` (line 645-712), `ConvMetaFieldNode.Read()` (line 796-818), `ConversationListNode.Readdir()` (line 357-430)
- `state/state_test.go` — tests for `AdoptWithSlug`
- `fuse/integration_test.go` — `TestSymlinkWithSlug`

---

## 4. Fix SendMessage returning EIO on subsequent writes to `new`

### Problem

Writing a second (or later) message to `/conversation/{id}/new` fails with `Input/output error`. The first message (which creates the conversation via `StartConversation`) succeeds, but all subsequent messages (which go through `SendMessage`) fail.

Reproduction:
```
$ id=$(cat /shelley/new/clone)
$ echo "model=predictable" > /shelley/conversation/$id/ctl
$ echo "hello" > /shelley/conversation/$id/new     # works
$ echo "hello again" > /shelley/conversation/$id/new  # EIO
```

### What we know so far

- `ConvNewNode.Write()` in `fuse/filesystem.go:665-696` handles both paths: first write calls `StartConversation()`, subsequent writes call `SendMessage()`.
- The first-write path (`StartConversation` at `shelley/client.go:72-124`) works — it accepts HTTP 200 and 201.
- The subsequent-write path (`SendMessage` at `shelley/client.go:150-186`) fails — it accepts only HTTP 200 and 202.
- `ConvNewNode.Write()` returns `syscall.EIO` on any error from either path, with no logging, so the actual API error is invisible.
- The shelley-fuse binary connects to `http://localhost:9999` but the Shelley server listens with systemd activation. The port may differ from what the FUSE binary expects, or the API may be returning an unexpected status code.

### Investigation needed

1. **Add error logging** to `ConvNewNode.Write()` (around line 682 and 690 in `fuse/filesystem.go`) so the actual error from `StartConversation`/`SendMessage` is visible. Currently both paths silently swallow the error and return `EIO`. At minimum, `log.Printf` the error before returning.

2. **Check what status code `SendMessage` gets back**: The API may return 201 (Created) for new messages but `SendMessage` only accepts 200/202. Compare with `StartConversation` which accepts 200/201. Try adding 201 to `SendMessage`'s accepted status codes.

3. **Check if the Shelley server URL is correct**: The FUSE binary is launched with `http://localhost:9999` but the Shelley server process shows it's using systemd activation, not a fixed port. Verify the server is actually listening on 9999.

4. **Check if the conversation is "busy"**: The first message may leave the conversation in a state where the backend is still processing (generating a response). The second write may arrive before the backend is ready, causing a 409 Conflict or similar.

### Key files

- `fuse/filesystem.go` — `ConvNewNode.Write()` (line 665-696), both the `StartConversation` and `SendMessage` error paths
- `shelley/client.go` — `SendMessage()` (line 150-186), check accepted status codes; `StartConversation()` (line 72-124) for comparison
- `cmd/shelley-fuse/main.go` — where the server URL is passed in
