---
id: sf-e12j
status: open
deps: [sf-bp4x, sf-3gq8]
links: []
created: 2026-02-16T14:46:23Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# State layer for multi-backend

Extend the state package to support multiple backends with per-backend configuration and conversation isolation.

This story covers the full state layer: schema migration to a per-backend structure, CRUD operations on backends (create/get/delete/rename, default management), URL get/set with persistence, backend-scoped conversation methods (CloneForBackend, GetForBackend, ListForBackend), and automatic migration of legacy flat conversation state to the new format.

When complete, the state package fully supports multiple named backends with isolated conversations, and legacy state files are transparently upgraded on load.

## Tickets

- sf-uh2s State file schema migration
- sf-392p Backend CRUD operations in state
- sf-3gq8 Backend URL operations and persistence
- sf-wghk Backend-aware conversation methods
- sf-bp4x Legacy state migration

