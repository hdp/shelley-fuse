# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Shelley FUSE is a Go FUSE filesystem that exposes the Shelley API (an AI conversation platform, source at https://github.com/boldsoftware/shelley) as a mountable filesystem. Shell tools interact with Shelley conversations through standard file operations (cat, echo, ls).

## Filesystem Architecture

The authoritative documentation for the filesystem layout, usage examples, and common operations lives in **`fuse/README.md`**. That file is embedded into the binary and served at the mountpoint as `/README.md`, making the filesystem self-documenting.

**Read `fuse/README.md` before doing any feature work.**

## Build & Test Commands

```bash
# Build the FUSE binary
just build

# Run all tests (requires /usr/local/bin/shelley and fusermount for integration tests)
just test

# Start for manual testing (Ctrl+C to unmount)
just dev

# Clean artifacts
just clean
```

This project uses a CLI ticket system for task management. Run `tk help` when you need to use it.

## Finishing Work

When you're done with a ticket, run `just finish-work` (from any worktree — ticket is auto-detected).
This closes the ticket, rebases onto main, ff-merges, and removes the worktree and branch.
It's idempotent — run it repeatedly until it exits 0. If the rebase has conflicts, fix them and re-run.

## Architecture

### Core Packages

- **`fuse/`** - FUSE filesystem implementation using `go-fuse/v2`. Contains the node hierarchy that maps filesystem paths to API calls. This is where most feature work happens.
- **`shelley/`** - HTTP client for the Shelley REST API. Wraps conversation CRUD, model listing, and message parsing/formatting.
- **`state/`** - Local conversation state management. Tracks the mapping between local FUSE conversation IDs and Shelley backend conversation IDs, persisted to `~/.shelley-fuse/state.json`.
- **`cmd/shelley-fuse/`** - Main binary entry point. Parses args and mounts the filesystem.

### Key Design Decisions

- The filesystem follows a Plan 9-inspired control file model. Typed nodes in `fuse/filesystem.go` implement the hierarchy documented in `fuse/README.md`.
- Conversation creation is split into clone → configure via ctl → first write to send. The `state` package maps local IDs to Shelley backend conversation IDs.
- The `/conversation` directory automatically discovers and adopts server-side conversations on Readdir and Lookup, ensuring all conversations always appear with 8-character local IDs.

### go-fuse API Notes

The codebase uses `go-fuse/v2`'s dynamic filesystem pattern (not static/OnAdd). Key considerations:
- Nodes use `NewInode()` (non-persistent) — lifetime controlled by kernel FORGET messages
- `StableAttr.Mode` is immutable after creation (file vs dir cannot change)
- `StableAttr.Ino` with value 0 means auto-assign; same Ino deduplicates to same kernel inode
- For dynamic content, `Open()` should return `FOPEN_DIRECT_IO` to bypass kernel page cache
- The kernel may call Setattr (truncate) before Write when creating files via shell redirection
- Entry/attr timeouts control kernel caching; short or zero timeouts needed for dynamic content
- `Readdir` results don't need to match `Lookup` — a node can be discoverable via Lookup even if not listed in Readdir

### Testing

Integration tests (`fuse/integration_test.go`) start a real Shelley server on a random free port, mount a FUSE filesystem in-process, and exercise the full Plan 9 workflow. They skip automatically if `fusermount` or `/usr/local/bin/shelley` is not available.

Tests clear `FIREWORKS_API_KEY`, `ANTHROPIC_API_KEY`, and `OPENAI_API_KEY` environment variables to prevent accidental use of real API keys. Integration tests use the `predictable` model for deterministic responses.

### Key Dependencies

- `github.com/hanwen/go-fuse/v2` - FUSE library (nodefs API)
- Go 1.22.2+
- `fusermount` binary required at runtime
