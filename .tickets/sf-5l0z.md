---
id: sf-5l0z
status: open
deps: [sf-e12j, sf-cgza, sf-j9f3, sf-42r2]
links: []
created: 2026-02-16T14:45:45Z
type: epic
priority: 1
assignee: hdp
tags: [multi-backend]
---
# Multi-backend support

Allow shelley-fuse to connect to multiple Shelley backends simultaneously, each with its own URL, model directory, and conversation history, managed through the FUSE filesystem interface.

## Motivation

Currently shelley-fuse connects to a single backend specified via `-server` flag. Users working with multiple Shelley instances (e.g., different providers, staging vs production) must run separate shelley-fuse processes. Multi-backend support lets a single mount manage all backends.

## Filesystem layout

```
/shelley/
  backend/
    default -> claude           # symlink to current default backend
    claude/
      url                       # read/write file: https://claude.shelley.exe.xyz
      connected                 # presence file: exists when backend is reachable
      model/                    # model directory tree from this backend
      conversation/             # conversation history for this backend
      new -> model/default/new  # shortcut to start new conversation
    ollama/
      url                       # read/write file: https://ollama.shelley.exe.xyz
      connected
      model/
      conversation/
      new -> model/default/new
  model -> backend/default/model              # backward-compat symlink
  conversation -> backend/default/conversation # backward-compat symlink
  new -> backend/default/model/default/new     # backward-compat symlink
  README.md
```

## Architecture layers

1. **State layer** — `state/state.go` gains a BackendState type. State file migrates from a flat conversation map to per-backend structure. CRUD operations (create, get, delete, rename) on backends, URL get/set, default backend management, and backend-scoped conversation methods.

2. **FUSE backend list** — `BackendListNode` implements `/shelley/backend/` as a directory. Supports Readdir, Lookup, plus mkdir (create backend), rmdir (delete), rename, and symlink (set default via `ln -s name default`). Simple hostnames (no dots) auto-generate `https://{name}.shelley.exe.xyz` URLs.

3. **FUSE backend node** — `BackendNode` implements each `/shelley/backend/{name}/` directory. Contains `url` (read/write file with FOPEN_DIRECT_IO), `connected` (presence file), `model/`, `conversation/`, `new` symlink.

4. **Client management** — `ClientManager` holds multiple `ShelleyClient` instances keyed by backend name. Lazily creates clients on first access. Detects URL changes and recreates stale clients. Wraps with `CachingClient` when cache TTL > 0.

5. **Root FS integration** — `NewFSWithBackends` constructor creates root with `backend/` directory and backward-compat symlinks. Bootstrap in `main.go` initializes from command-line URL, migrates legacy state, creates `ClientManager`, and builds FS.

## Key design decisions

- **Backward compatibility via symlinks**: Root-level `model`, `conversation`, `new` symlinks point through `backend/default/`, so existing scripts and workflows keep working unchanged.
- **Reserved name 'default'**: Cannot be used as a backend name; reserved for the symlink to the current default. `rmdir default` → EINVAL, `mkdir default` → EEXIST.
- **Cannot delete default backend**: `rmdir` on the current default returns EBUSY. User must switch default first.
- **Lazy client creation**: `ClientManager` doesn't connect at startup; clients are created on first access to a backend's model or conversation directory.
- **Conversation isolation**: Conversations are scoped per-backend. No cross-backend conversation access.
- **Backend name from hostname**: Bootstrap extracts backend name from the first label of the server hostname (e.g., "claude" from "claude.shelley.exe.xyz").
- **Auto-migration**: On first load with legacy flat conversations, they are migrated to a named backend automatically.
- **URL validation**: Backend URLs must have http or https scheme.
- **POSIX error semantics**: All FUSE operations return standard errno values (EEXIST, ENOENT, EBUSY, EINVAL, EPERM, EXDEV) for appropriate error conditions.

