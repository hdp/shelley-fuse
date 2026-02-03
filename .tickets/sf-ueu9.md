---
id: sf-ueu9
status: open
deps: []
links: []
created: 2026-02-02T14:30:12Z
type: feature
priority: 2
tags: [fuse, boolean]
---
# Boolean files as present/absent

Change boolean files from containing 'true'/'false' text to using file presence/absence semantics. Specifically: (1) conversation/{id}/created: exists only when conversation is created on backend, with mtime=creation time. Returns ENOENT when not created. Drop created_at entirely — the mtime of 'created' serves the same purpose. (2) models/{model}/ready: exists only when model is ready, absent otherwise. Update ConvStatusFieldNode, ConversationNode.Lookup/Readdir, ModelNode.Lookup/Readdir, and all tests.

## Acceptance Criteria

stat conversation/{id}/created succeeds after creation with correct mtime. stat returns ENOENT before creation. created_at file no longer exists. stat models/predictable/ready succeeds. All unit and integration tests pass.


## Notes

**2026-02-03T03:51:45Z**

Confirmed: The work was never actually implemented. Commit `1683a75` marked the ticket closed but only updated the `.tickets/` file — no code changes to `fuse/filesystem.go` or tests were made. Later commit `wplkymtv` (sf-t6ct) updated docs to describe what "should" have been done, creating false impression work was complete.

Current state:
- `created_at` file still exists in fuse/filesystem.go and integration_test.go
- Case "created_at" in ConversationNode.Lookup returns ConvStatusFieldNode
- created_at listed in Readdir results
- Tests still reference and validate created_at

Need to implement the actual changes described in this ticket.
