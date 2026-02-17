---
id: sf-ebqt
status: closed
deps: [sf-392p]
links: [sf-c0hw, sf-cmm3]
created: 2026-02-15T14:30:53Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# BackendListNode FUSE directory

Create /shelley/backend/ directory node with Readdir and Lookup. Lists all backends as subdirectories plus a 'default' symlink pointing to the current default backend name.

## Important Constraint

The name 'default' is reserved and should NEVER exist as an actual backend directory. The 'default' entry in /shelley/backend/ must always be a symlink pointing to the actual default backend name. The auto-created default backend should use a different internal name (e.g., 'main' or the server URL-derived name) to avoid conflict with the reserved 'default' symlink name.

