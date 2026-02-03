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

Confirmed: created_at file still exists in fuse/filesystem.go and is still being tested in fuse/integration_test.go. The ticket was marked closed but the work is incomplete:
- created_at file is still handled in ConversationNode.Lookup (line with 'created_at': return...)
- created_at is still listed in Readdir results
- Tests still reference created_at
Per acceptance criteria: 'created_at file no longer exists' — this needs to be done.
