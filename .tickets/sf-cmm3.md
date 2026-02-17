---
id: sf-cmm3
status: closed
deps: [sf-ebqt]
links: [sf-c0hw]
created: 2026-02-15T14:31:56Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# BackendListNode rmdir operation

Implement rmdir /shelley/backend/{name} to delete backends. Returns EBUSY if the backend is the current default. Returns EINVAL for 'default' name.

