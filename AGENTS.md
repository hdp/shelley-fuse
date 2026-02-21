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

This project uses `lai` (launch-and-iterate) for ticket-driven development with a two-phase workflow:

1. **Planning phase** — an agent reads the ticket, explores the codebase, writes a concrete implementation plan
2. **Execution phase** — agents iterate in a loop, each making changes and handing off, until one approves

### Structural Self-Approval Guard

Agents cannot approve their own changes. `lai run` records HEAD before launching; `lai approve` refuses if HEAD has moved or the worktree is dirty. An agent that commits changes must call `lai run` to hand off to a fresh agent.

### As a Planning Agent

You were launched by `lai start-work {ticket}`. Your job:

1. Read the ticket (`.tickets/{ticket}.md`) for requirements and acceptance criteria
2. Read `AGENTS.md` (this file) for project conventions and architecture
3. Explore the codebase to understand what exists and what needs to change
4. Write a concrete implementation plan (which files to change, how, in what order)
5. Submit the plan via `lai plan {ticket}` (reads from stdin)
6. Exit

Do NOT implement anything — execution agents will do that.

### As an Execution Agent

You were launched by `lai run {ticket}`. Your job:

1. Read the ticket for requirements and acceptance criteria
2. Read the plan via `lai show-plan {ticket}`
3. Read `AGENTS.md` for project conventions
4. Review the current diff: `git diff main...HEAD`
5. Decide:
   - **Changes needed**: Make them, run `just test`, commit, call `lai run {ticket}` as your final action (hands off to a fresh agent)
   - **Everything complete**: Write a commit summary and pipe to `lai commit-message {ticket}`, then call `lai approve {ticket}` as your final action
   - **Fundamentally broken**: Escalate to a human

Do NOT approve if you changed code — the structural guard will refuse it.

### Ticket Splitting

If a ticket is too large, implement what you can, then:

1. Create new ticket(s) for remaining work: `tk create`
2. If the current ticket has dependents, update the dependency chain: `tk dep`
3. Commit the new ticket files (`.tickets/*.md`)
4. Call `lai run {ticket}` to hand off

### Available Commands

**Workflow commands:**
- `lai start-work {ticket}` — Create worktree and launch planning agent
- `lai plan {ticket}` — Submit implementation plan (reads from stdin, called by planning agent)
- `lai show-plan {ticket}` — Display the implementation plan for a ticket
- `lai run {ticket}` — Launch execution agent (records HEAD for self-approval guard)
- `lai commit-message {ticket}` — Submit commit summary (reads from stdin, called by execution agent)
- `lai approve {ticket}` — Approve with guards: close ticket, squash, rebase, merge, cleanup
- `lai status` — Show active worktrees and their state

**Build and test:**
- `just build` — Build the binary
- `just test` — Run all tests

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
