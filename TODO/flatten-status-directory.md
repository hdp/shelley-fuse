# Feature: Flatten status/ directory into conversation root

The current `/conversation/{ID}/status/` subdirectory adds unnecessary depth. Move its contents up to `/conversation/{ID}/` directly, removing redundancy.

## Current Structure

```
conversation/{ID}/
  ctl                    → read/write config (model=X cwd=Y)
  new                    → write to send message
  id                     → Shelley server conversation ID
  slug                   → conversation slug
  all.json, all.md       → full conversation
  {N}.json, {N}.md       → message by sequence number
  last/, since/, from/   → message filters
  status/
    local_id             → local conversation ID
    shelley_id           → Shelley server conversation ID (DUPLICATE of ../id)
    slug                 → conversation slug (DUPLICATE of ../slug)
    model                → model text file
    cwd                  → working directory text file
    created              → "true" or "false"
    created_at           → RFC3339 timestamp
    message_count        → number of messages
```

## Proposed Structure

```
conversation/{ID}/
  ctl                    → read/write config (model=X cwd=Y)
  new                    → write to send message
  id                     → Shelley server conversation ID (kept)
  fuse_id                → local FUSE conversation ID (renamed from local_id)
  slug                   → conversation slug (kept)
  model                  → SYMLINK to ../../models/{MODEL} (see conversation-model-symlink.md)
  cwd                    → SYMLINK to actual directory (see cwd-symlink.md)
  created                → "true" or "false" (moved up)
  created_at             → RFC3339 timestamp (moved up)
  message_count          → number of messages (moved up)
  messages/              → see messages-directory.md
```

## Removals (Redundant)

- `status/shelley_id` → redundant with `id`
- `status/slug` → redundant with `slug`
- `status/local_id` → renamed to `fuse_id` and moved up
- `status/model` → replaced by `model` symlink
- `status/cwd` → replaced by `cwd` symlink
- `status/` directory itself → eliminated

## Implementation Notes

### Code Locations

1. **`fuse/filesystem.go`**: `StatusDirNode` and its methods should be removed entirely
2. **`ConversationDirNode.Lookup()`**: Currently delegates `status` lookups to `StatusDirNode`. Change to:
   - Handle `fuse_id`, `created`, `created_at`, `message_count` directly as file nodes
   - Handle `model`, `cwd` as symlink nodes (per other TODOs)
   - Remove the `status` case
3. **`ConversationDirNode.Readdir()`**: Add entries for the new files, remove `status` entry

### Naming Rationale

- `id` stays as-is: it's the Shelley backend conversation ID, the "real" identifier
- `fuse_id` (not `local_id`): clearer that this is the FUSE-assigned 8-char hex ID, not "local" in some vague sense
- `shelley_id` removed: `id` already means the Shelley ID; adding "shelley_" prefix was redundant

### Testing Impact

Integration tests in `fuse/integration_test.go` read from `status/` paths. All such tests need updating:
- `status/local_id` → `fuse_id`
- `status/shelley_id` → `id`
- `status/model` → `model` (now a symlink, use `readlink` or just read through it)
- `status/cwd` → `cwd` (now a symlink)
- `status/created` → `created`
- `status/created_at` → `created_at`
- `status/message_count` → `message_count`

### Documentation Updates

Update `CLAUDE.md` and `README.md` filesystem hierarchy documentation to reflect the flattened structure.

## Dependencies

This TODO should be implemented AFTER:
- `cwd-symlink.md` (so we know how cwd symlinks work)
- `conversation-model-symlink.md` (so we know how model symlinks work)

Or implement them all together as a single coordinated change.
