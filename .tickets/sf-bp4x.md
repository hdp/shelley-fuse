---
id: sf-bp4x
status: open
deps: [sf-wghk]
links: []
created: 2026-02-15T14:30:23Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# Legacy state migration

Auto-migrate old flat conversation format to new backend format on load. Add MigrateToBackend and HasLegacyConversations methods. When loading a state file with legacy conversations and no backends, migrate them to a named backend.

