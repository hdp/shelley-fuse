---
id: sf-wghk
status: closed
deps: [sf-392p]
links: []
created: 2026-02-15T14:29:53Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# Backend-aware conversation methods

Migrate existing conversation methods (Clone, Get, MarkCreated, etc.) to take a backend parameter. Add CloneForBackend, GetForBackend, ListForBackend, and similar backend-aware variants. Ensure conversation isolation between backends.

