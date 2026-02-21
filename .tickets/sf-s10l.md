---
id: sf-s10l
status: closed
deps: [sf-ebqt, ss-sure]
links: [sf-c0hw, sf-cmm3, sf-r9mv]
created: 2026-02-15T14:45:00Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# BackendListNode symlink operation

Implement `ln -s {name} /shelley/backend/default` to set the default backend. Only allows creating a symlink named "default" (EPERM for other names). Target must be an existing backend (ENOENT otherwise). Calls SetDefaultBackend on the state store.
