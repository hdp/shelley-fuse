---
id: sf-i17t
status: open
deps: [sf-b16m]
links: [sf-d18r]
created: 2026-02-15T14:45:00Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# Multi-backend integration tests

Add integration tests for the full multi-backend flow. Test creating backends via mkdir, setting URLs via file write, switching default via symlink, verifying model directories use the correct backend's client. Uses mock servers for each backend. Tests should cover: create and switch backends, conversation isolation between backends, compatibility symlinks resolving correctly.
