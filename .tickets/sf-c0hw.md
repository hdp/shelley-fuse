---
id: sf-c0hw
status: open
deps: [sf-ebqt]
links: [sf-cmm3]
created: 2026-02-15T14:31:26Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# BackendListNode mkdir operation

Implement mkdir /shelley/backend/{name} to create backends. Simple names (no dots) get default URL https://{name}.shelley.exe.xyz. Dotted names get empty URL. Reserved name 'default' returns EEXIST.

