# Feature: Consolidated messages/ directory

Consolidate all message access into a single `messages/` subdirectory with cleaner organization.

## Current Structure

Messages are currently scattered across the conversation root:

```
conversation/{ID}/
  all.json               → full conversation as JSON
  all.md                 → full conversation as Markdown
  {N}.json               → specific message (virtual, not in Readdir)
  {N}.md                 → specific message (virtual, not in Readdir)
  last/{N}.json          → last N messages as JSON
  last/{N}.md            → last N messages as Markdown
  since/{person}/{N}.json → messages since Nth-to-last from {person}
  since/{person}/{N}.md
  from/{person}/{N}.json  → Nth message from {person}
  from/{person}/{N}.md
```

## Proposed Structure

```
conversation/{ID}/
  messages/
    all.json             → full conversation as JSON
    all.md               → full conversation as Markdown
    1.json, 1.md         → first message
    2.json, 2.md         → second message
    ...                  → listed in Readdir up to message_count
    {N}.json, {N}.md     → Nth message (all listed, not virtual)
    last/
      {N}.json           → last N messages
      {N}.md
    since/
      {person}/
        {N}.json         → messages since Nth-to-last from {person}
        {N}.md
    from/
      {person}/
        {N}.json         → Nth message from {person} (from end)
        {N}.md
```

## Key Changes

### 1. Individual Messages Now Listed

Currently `{N}.json` and `{N}.md` files are "virtual" — accessible via `Lookup()` but not returned by `Readdir()`. This is noted in CLAUDE.md:

> `{N}.json` → specific message by sequence number (virtual, not in listings)

Change this: `Readdir()` should return entries for `1.json`, `1.md`, `2.json`, `2.md`, ... up to `message_count`. This makes `ls messages/` show all available messages.

### 2. Move all.json and all.md

These currently live at the conversation root. Move them into `messages/` for consistency — all message-related content in one place.

### 3. Move last/, since/, from/ Subdirectories

These filter directories currently live at conversation root. Move them under `messages/` since they're all about accessing messages in different ways.

## Implementation Notes

### Code Locations

1. **Create `MessagesDirNode`** in `fuse/filesystem.go`:
   - New directory node type for `/conversation/{ID}/messages/`
   - `Readdir()` returns: `all.json`, `all.md`, `1.json`, `1.md`, ..., `last/`, `since/`, `from/`
   - `Lookup()` handles all these entries

2. **Modify `ConversationDirNode`**:
   - Remove `all.json`, `all.md`, `{N}.json`, `{N}.md`, `last/`, `since/`, `from/` handling
   - Add `messages` entry pointing to `MessagesDirNode`

3. **Move existing node types**:
   - `LastDirNode`, `SinceDirNode`, `FromDirNode` become children of `MessagesDirNode`
   - `AllJsonNode`, `AllMdNode`, `MessageJsonNode`, `MessageMdNode` become children of `MessagesDirNode`

### Readdir for Individual Messages

The `MessagesDirNode.Readdir()` needs to know `message_count` to list `1.json` through `{N}.json`. This requires either:
- Fetching the conversation from the API (has message count)
- Reading from local state (if cached)

Consider caching or lazy-loading to avoid API calls on every `ls`.

### API Calls

The message content comes from `GET /api/conversation/{id}` which returns the full conversation including all messages. The `shelley.Client.GetConversation()` method handles this. Individual message extraction is done client-side from the full response.

### Virtual vs Listed Trade-off

Making messages non-virtual (listed in Readdir) means:
- **Pro**: `ls messages/` shows what's available, more discoverable
- **Pro**: Tab completion works for message numbers
- **Con**: More entries in directory listings
- **Con**: Need to know message count for Readdir (API call or cache)

The pro outweighs — discoverability is important for a filesystem interface.

## Testing Impact

Integration tests need updating for new paths:
- `all.json` → `messages/all.json`
- `all.md` → `messages/all.md`
- `{N}.json` → `messages/{N}.json`
- `last/{N}.json` → `messages/last/{N}.json`
- `since/{person}/{N}.json` → `messages/since/{person}/{N}.json`
- `from/{person}/{N}.json` → `messages/from/{person}/{N}.json`

## Documentation Updates

Update `CLAUDE.md` and `README.md` filesystem hierarchy documentation.

## Dependencies

This can be implemented independently of other TODOs, but should coordinate with `flatten-status-directory.md` since both modify `ConversationDirNode`.
