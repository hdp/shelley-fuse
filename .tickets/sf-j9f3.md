---
id: sf-j9f3
status: open
deps: [sf-b16m]
links: []
created: 2026-02-16T14:47:23Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# Client management and FS wiring

Create the multi-client manager and wire everything together into the root filesystem and bootstrap flow.

This story covers ClientManager (lazy per-backend client creation, URL change detection, CachingClient wrapping), wiring BackendNode's model/ and conversation/ lookups to ClientManager, updating the root FS with the backend/ directory and backward-compat symlinks (model, conversation, new), and updating main.go bootstrap to initialize backends from the command-line URL, migrate legacy state, and use NewFSWithBackends.

When complete, shelley-fuse boots with multi-backend support, backward-compat symlinks work, and each backend's model/conversation directories serve from the correct client.

## Tickets

- sf-m13c Multi-client manager
- sf-w15c Wire BackendNode to ClientManager
- sf-f14r Update FS root for backend support
- sf-b16m Bootstrap logic in main.go

