# AGENTS.md

This file provides guidance to AI coding agents working with code in this repository.

## Project Overview

Shelley FUSE is a Go FUSE filesystem that exposes the Shelley API as a mountable filesystem. Shell tools interact with Shelley conversations through standard file operations (cat, echo, ls).

See the [root README.md](README.md) for project introduction and installation instructions.

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

## Development Workflow

This project uses a review-gated workflow. Implementing agents do not merge their own work — a separate review agent checks the work first.

### As an Implementing Agent

You were launched by `just start-work` or `just implement`. Your job:

1. Read your ticket (`.tickets/{ticket}.md`) for requirements
2. Read `AGENTS.md` (this file) for project conventions
3. Implement the work, run `just test`, commit
4. When done, run `just review {ticket}` as your **final action**

Do NOT run `just finish-work` — that's the reviewer's job.

### As a Review Agent

You were launched by `just review`. Your job:

1. Read the ticket for requirements and acceptance criteria
2. Review `git diff main...HEAD` against the ticket
3. Run `just test`
4. Either:
   - **Approve**: run `just finish-work {ticket}` (closes ticket, rebases, merges, cleans up)
   - **Reject**: edit the ticket to clarify what's needed, commit the edit, then run `just implement {ticket}`

### Ticket Splitting

If a ticket is too large for one pass, create new tickets for remaining work with `tk create`, adjust the dependency chain with `tk dep`, and commit the new ticket files. The reviewer will check the split.

### Available Commands

- `just start-work {ticket}` — Create worktree and start implementing (first time)
- `just implement {ticket}` — Resume implementing in existing worktree
- `just review {ticket}` — Launch review on existing worktree
- `just finish-work {ticket}` — Approve: close, rebase, merge, clean up (reviewer only)
- `just next-ticket` — Print the next ticket ready for work
- `just test` — Run all tests
- `just build` — Build the binary

## Architecture

### Core Packages

- **`fuse/`** - FUSE filesystem implementation using `go-fuse/v2`. Contains the node hierarchy that maps filesystem paths to API calls. This is where most feature work happens.
  - **`fuse/README.md`** - Embedded into the binary and served at the mountpoint as `/README.md`, making the filesystem self-documenting. This is the authoritative source for filesystem usage documentation.
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
