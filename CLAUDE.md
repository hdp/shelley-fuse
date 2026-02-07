# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Shelley FUSE is a Go FUSE filesystem that exposes the Shelley API (an AI conversation platform, https://github.com/boldsoftware/shelley is the source) as a mountable filesystem. Shell tools interact with Shelley conversations through standard file operations (cat, echo, ls).

## Build & Test Commands

```bash
# Build the FUSE binary
just build

# Run all tests
just test

# Run integration tests (requires /usr/local/bin/shelley and fusermount)
just test-integration

# Start for manual testing (Ctrl+C to unmount)
just dev

# Clean artifacts
just clean
```

This project uses a CLI ticket system for task management. Run `tk help` when you need to use it.

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
  models/                               → directory of available models (GET /, parse HTML for model list)
    default                             → symlink to default model's {model-id} (only if backend has a default configured)
    {model-id}/                         → directory for each model
      id                                → read-only file: model ID
      ready                             → present only if model is ready (presence semantics)
  new/
    clone                               → read to allocate a new local conversation ID
  conversation/                           → lists local IDs + server conversations (merged via GET /api/conversations)
    {local-id}/                         → directory per conversation (8-character hex local ID)
      ctl                               → read/write config (model=X cwd=Y); becomes read-only after creation
      send                              → write here to send a message; first write creates conversation on backend
      id                                → read-only: Shelley server conversation ID (ENOENT before creation)
      slug                              → read-only: conversation slug (ENOENT before creation or if no slug)
      fuse_id                           → read-only: local FUSE conversation ID (8-character hex)
      created                           → present only when created on backend (presence semantics, mtime = creation time)
      archived                          → present only when conversation is archived (presence semantics, mtime = updated_at);
                                            touch/create to archive, rm to unarchive; ENOENT before backend creation
      model                             → symlink to ../../models/{model-id} (only if model is set)
      cwd                               → symlink to working directory (only if cwd is set)
      meta/                             → conversation metadata as jsonfs directory tree
        local_id                        → local FUSE conversation ID
        conversation_id                 → Shelley API conversation ID (only if created)
        slug                            → conversation slug (only if set)
        model                           → selected model (only if set)
        cwd                             → working directory path (only if set)
        created                         → boolean "true" or "false"
        local_created_at                → local timestamp when conversation was cloned
        api_created_at                  → server timestamp (only if created)
        api_updated_at                  → server timestamp (only if created)
      messages/                         → all message content
        all.json                        → full conversation as JSON
        all.md                          → full conversation as Markdown
        count                           → number of messages in conversation (0 before creation)
        {N}-{slug}/                     → message directory (0-indexed, e.g. 0-user/, 99-bash-tool/, 100-bash-result/)
          message_id                    → message UUID
          conversation_id               → conversation ID
          sequence_id                   → sequence number
          type                          → message type (user, agent, bash-tool, bash-result, etc.)
          created_at                    → timestamp
          content.md                    → markdown rendering of the message
          llm_data/                     → unpacked JSON directory (present only if message has LLM data)
          usage_data/                   → unpacked JSON directory (present only if message has usage data)
        last/{N}/                       → directory with symlinks to last N message directories
        since/{slug}/{N}/               → directory with symlinks to messages after Nth-to-last {slug}
    {server-id}                         → symlink to local-id: allows access via Shelley server ID
    {slug}                              → symlink to local-id: allows access via conversation slug
```

Key design: conversation creation is split into clone → configure via ctl → first write to send. The `state` package maps local IDs to Shelley backend conversation IDs, persisted to `~/.shelley-fuse/state.json`.

The `/conversation` directory automatically discovers and adopts server-side conversations. When `ConversationListNode.Readdir` is called, it fetches conversations from `client.ListConversations()` and immediately adopts any that aren't already tracked locally via `state.AdoptWithSlug()`. This ensures all conversations always appear with 8-character local IDs—there are no "server-only" conversations visible to users. The `Lookup` method also supports accessing conversations by their Shelley server ID for backwards compatibility, adopting them on first access.

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
