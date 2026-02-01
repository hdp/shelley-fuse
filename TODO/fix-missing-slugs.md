# Fix missing slugs for FUSE-created conversations

## Problem

Conversations created through the Shelley web UI get slugs, but those created through FUSE do not. There are two contributing issues:

**Issue A — The API may not return a slug at creation time.** `client.StartConversation()` in `shelley/client.go:72-124` POSTs to `/api/conversations/new` and decodes the response. The slug field is `*string` (nullable). If the API returns `null` or omits `slug`, `result.Slug` stays empty. `MarkCreated()` (`state/state.go:105-118`) then stores an empty slug. The Shelley backend likely generates slugs asynchronously (e.g., after the first AI response), so the slug isn't available at conversation creation time.

**Issue B — `AdoptWithSlug()` doesn't update slugs for already-tracked conversations.** `AdoptWithSlug()` in `state/state.go:184-215` checks if a conversation is already tracked by `ShelleyConversationID` (line 189-193). If found, it returns the existing local ID immediately **without updating the slug**, even though the comment on line 183 says it should "update the slug if it was previously empty." This means even when `Readdir()` calls `AdoptWithSlug()` with a fresh slug from the server listing, FUSE-created conversations that were adopted with an empty slug never get updated.

## Implementation plan

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

## Key files

- `state/state.go` — `AdoptWithSlug()` (primary fix, line 184-215), `MarkCreated()` (line 105-118)
- `shelley/client.go` — `StartConversation()` (line 72-124), `GetConversation()` (line 127-147)
- `fuse/filesystem.go` — `ConvNewNode.Write()` (line 645-712), `ConvMetaFieldNode.Read()` (line 796-818), `ConversationListNode.Readdir()` (line 357-430)
- `state/state_test.go` — tests for `AdoptWithSlug`
- `fuse/integration_test.go` — `TestSymlinkWithSlug`
