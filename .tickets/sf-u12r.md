---
id: sf-u12r
status: open
deps: [sf-b11n]
links: []
created: 2026-02-15T14:45:00Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# BackendURLNode and BackendConnectedNode

Implement the `url` file node (read/write) and `connected` presence file node for individual backends. BackendURLNode supports Open/Read/Write with FOPEN_DIRECT_IO - reading returns the URL with newline, writing validates URL scheme (http/https) and calls SetBackendURL. BackendConnectedNode returns ENOENT when backend has no URL or is disconnected.
