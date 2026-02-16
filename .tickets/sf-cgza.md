---
id: sf-cgza
status: open
deps: [sf-c0hw, sf-cmm3, sf-r9mv, sf-s10l, sf-u12r, sf-w15c]
links: []
created: 2026-02-16T14:46:53Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# Backend FUSE directory tree

Implement the full FUSE directory tree for browsing and managing backends.

This story covers BackendListNode (/shelley/backend/ directory with Readdir, Lookup, and the default symlink), its mutation operations (mkdir to create backends, rmdir to delete, rename, symlink to set default), and BackendNode (/shelley/backend/{name}/ with url file, connected presence file, model/, conversation/, new symlink), including BackendURLNode and BackendConnectedNode.

When complete, the entire backend filesystem tree is navigable and supports CRUD operations via standard POSIX commands (mkdir, rmdir, mv, ln -s, cat, echo).

## Tickets

- sf-ebqt BackendListNode FUSE directory
- sf-c0hw BackendListNode mkdir operation
- sf-cmm3 BackendListNode rmdir operation
- sf-r9mv BackendListNode rename operation
- sf-s10l BackendListNode symlink operation
- sf-b11n BackendNode directory
- sf-u12r BackendURLNode and BackendConnectedNode

