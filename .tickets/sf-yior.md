---
id: sf-yior
status: closed
deps: [sf-37oa]
links: []
created: 2026-02-04T02:16:23Z
type: feature
priority: 2
---
# Apply JSON-to-filesystem model to conversation metadata

After sf-37oa provides the JSON-to-filesystem abstraction, apply the same model to conversation-level data.

Currently conversations have individual files like:
```
conversation/{id}/
  ctl
  id
  slug
  fuse_id
  created
  model -> symlink
  cwd -> symlink
  messages/
```

The conversation API returns JSON with additional fields that could be exposed. Consider whether the conversation directory should also use the JSON-to-filesystem abstraction to expose all conversation metadata consistently.

This may involve:
- Exposing additional conversation fields from the API
- Using the same abstraction as messages for consistency
- Keeping symlinks (model, cwd) and special files (ctl, new) as-is since they have special semantics

Depends on sf-37oa for the abstraction.


## Notes

**2026-02-04T06:28:38Z**

Implementation complete:

- Added ConvMetaDirNode that exposes conversation metadata as a jsonfs directory tree
- The meta/ directory is now available at /conversation/{id}/meta/
- Exposes local state fields: local_id, created, model, cwd, slug, conversation_id, local_created_at
- Exposes API fields when conversation is created: api_created_at, api_updated_at
- Uses jsonfs.NewNode for consistency with message directory structure
- Maintains backward compatibility - existing files (id, slug, fuse_id, created) remain unchanged
- Added tests: TestConvMetaDirNode and TestConvMetaDirNode_UncreatedConversation

Fields exposed in meta/:
- local_id: The local FUSE conversation ID
- conversation_id: The Shelley API conversation ID (only if created)
- slug: The conversation slug (only if set)
- model: The selected model (only if set)
- cwd: The working directory (only if set)
- created: Boolean indicating if conversation exists on backend (true/false)
- local_created_at: Local timestamp when conversation was cloned
- api_created_at: Server timestamp from API (only if created)
- api_updated_at: Server timestamp from API (only if created)

**2026-02-04T13:12:36Z**

MISINTERPRETATION - Correcting intent:

The original intent was NOT to create a meta/ subdirectory. Instead, the conversation directory structure should directly mirror the JSON blob format returned by the API. Additional special files (archived, ctl, fuse_id, etc.) can be added on top.

For example, if the conversation JSON is:
{
  "id": "abc123",
  "slug": "my-convo",
  "model": "gpt-4",
  "created_at": "2024-01-01T00:00:00Z",
  "updated_at": "2024-01-02T00:00:00Z",
  "archived": false
}

The filesystem structure should be:
/conversation/{local-id}/
  id               -> "abc123"
  slug             -> "my-convo"
  model            -> "gpt-4"
  created_at       -> "2024-01-01T00:00:00Z"
  updated_at       -> "2024-01-02T00:00:00Z"
  archived         -> present/absent boolean file
  ctl              -> special config file
  fuse_id          -> local FUSE ID (extra)
  messages/        -> message array (0-indexed, not 1-indexed)

The messages/ subdir should follow same pattern - 0-indexed to match JSON arrays.

The meta/ subdirectory approach was wrong - fields should be flat at the conversation level, not nested.

**2026-02-05T13:26:13Z**

messages/ were not reworked to be 0-indexed
