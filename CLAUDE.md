# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Shelley FUSE is a Go FUSE filesystem that exposes the Shelley API (an AI conversation platform) as a mountable filesystem. Shell tools interact with Shelley conversations through standard file operations (cat, echo, ls).

## Build & Test Commands

```bash
# Build everything (main binary + tools)
just build

# Build only the FUSE binary
go build -o shelley-fuse ./cmd/shelley-fuse

# Build only test tools
just build-tools

# Run unit tests
go test ./...

# Run a single test
go test -v ./fuse -run TestInProcessFUSE

# Run integration tests (requires /usr/local/bin/shelley binary)
go test -v ./shelley -run TestIntegration

# Start dev environment for manual testing
just test-shell

# Quick demo
just demo

# Stop all test services
just stop

# Clean artifacts
just clean
```

## Architecture

### Core Packages

- **`fuse/`** - FUSE filesystem implementation using `go-fuse/v2`. Contains the node hierarchy that maps filesystem paths to API calls. This is where most feature work happens.
- **`shelley/`** - HTTP client for the Shelley REST API. Wraps conversation CRUD, model listing, and message parsing/formatting.
- **`state/`** - Local conversation state management. Tracks the mapping between local FUSE conversation IDs and Shelley backend conversation IDs, persisted to `~/.shelley-fuse/state.json`.
- **`cmd/shelley-fuse/`** - Main binary entry point. Parses args and mounts the filesystem.

### Filesystem Node Hierarchy

The filesystem follows a Plan 9-inspired control file model. There are no host directories — a single shelley-fuse instance connects to one Shelley backend. Typed nodes in `fuse/filesystem.go` implement the hierarchy:

```
/
  models                                → read-only file (GET /, parse HTML for model list)
  new/
    clone                               → read to allocate a new local conversation ID
  conversation/
    {id}/                               → directory per conversation (id from clone)
      ctl                               → read/write config (model=X cwd=Y); becomes read-only after creation
      new                               → write here to send a message; first write creates conversation on backend
      status.json                       → read-only JSON status (local ID, shelley ID, message count, etc.)
      all.json                          → full conversation as JSON
      all.md                            → full conversation as Markdown
      {N}.json                          → specific message by sequence number
      {N}.md                            → specific message as Markdown
      last/{N}.json                     → last N messages as JSON
      last/{N}.md                       → last N messages as Markdown
      since/{person}/{N}.json           → messages since Nth-to-last message from {person}
      since/{person}/{N}.md             → same, as Markdown
      from/{person}/{N}.json            → Nth message from {person} (counting from end)
      from/{person}/{N}.md              → same, as Markdown
```

Key design: conversation creation is split into clone → configure via ctl → first write to new. The `state` package maps local IDs to Shelley backend conversation IDs, persisted to `~/.shelley-fuse/state.json`.

### go-fuse API Notes

The codebase uses `go-fuse/v2`'s dynamic filesystem pattern (not static/OnAdd). Key considerations:
- Nodes use `NewInode()` (non-persistent) — lifetime controlled by kernel FORGET messages
- `StableAttr.Mode` is immutable after creation (file vs dir cannot change)
- `StableAttr.Ino` with value 0 means auto-assign; same Ino deduplicates to same kernel inode
- For dynamic content, `Open()` should return `FOPEN_DIRECT_IO` to bypass kernel page cache
- The kernel may call Setattr (truncate) before Write when creating files via shell redirection
- Entry/attr timeouts control kernel caching; short or zero timeouts needed for dynamic content
- `Readdir` results don't need to match `Lookup` — a node can be discoverable via Lookup even if not listed in Readdir

### Testing Infrastructure

Three layers of test support exist:

1. **`testutil/`** - Generic in-process FUSE server testing library. Preferred approach for new tests. Uses `InProcessFUSEConfig` with a `CreateFS` factory function.
2. **`testhelper/`** - External process FUSE testing (legacy). Spawns the FUSE binary as a subprocess with PID file management.
3. **`tools/`** - Standalone binaries (`start-test-server`, `start-fuse`) for manual testing. Has its own `go.mod`.

In-process tests skip automatically if `fusermount` is not available.

### Key Dependencies

- `github.com/hanwen/go-fuse/v2` - FUSE library (nodefs API)
- Go 1.22.2+
- `fusermount` binary required at runtime

### API Key Handling

Tests explicitly clear `FIREWORKS_API_KEY`, `ANTHROPIC_API_KEY`, and `OPENAI_API_KEY` environment variables to prevent accidental use of real API keys during testing. Integration tests use the `predictable` model for deterministic responses.
