---
id: sf-b16m
status: open
deps: [sf-bp4x, sf-f14r]
links: []
created: 2026-02-15T14:45:00Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# Bootstrap logic in main.go

Update main.go to initialize backends from command-line URL. New flow: parse URL, create state store, if legacy conversations exist migrate them via MigrateToBackend, if no backends exist bootstrap from URL using extractBackendName (first part of hostname before dots), create ClientManager, create FS with NewFSWithBackends. Replace single-client initialization.
