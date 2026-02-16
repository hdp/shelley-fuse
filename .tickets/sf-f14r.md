---
id: sf-f14r
status: open
deps: [sf-ebqt, sf-m13c]
links: [sf-b16m]
created: 2026-02-15T14:45:00Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# Update FS root for backend support

Modify the root FS struct and Lookup to include the `backend/` directory and backward-compatibility symlinks. Add NewFSWithBackends constructor that takes ClientManager and state Store. Root entries: `backend/` (BackendListNode dir), `model` (symlink to backend/default/model), `conversation` (symlink to backend/default/conversation), `new` (symlink to backend/default/model/default/new), `README.md`.
