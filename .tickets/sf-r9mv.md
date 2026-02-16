---
id: sf-r9mv
status: open
deps: [sf-ebqt]
links: [sf-c0hw, sf-cmm3]
created: 2026-02-15T14:45:00Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# BackendListNode rename operation

Implement `mv /shelley/backend/{old} /shelley/backend/{new}` to rename backends. Only supports rename within the same directory (EXDEV for cross-directory). Returns EINVAL for renaming to/from 'default'. Updates DefaultBackend if the renamed backend was the default.
