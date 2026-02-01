# Expose per-conversation status.json as a directory

## Problem

Currently `status.json` is a single read-only JSON blob at `/conversation/{id}/status.json`. To access individual fields, clients must parse JSON. This is inconsistent with the Plan 9 philosophy of the rest of the filesystem, where individual pieces of data are exposed as separate files (e.g., `id`, `slug`, `ctl`).

## Goal

Replace (or supplement) the `status.json` file with a `status/` directory that exposes each JSON field as its own file, similar to how the `models/` directory exposes per-model information:

```
/conversation/{id}/status/
  local_id
  shelley_id
  slug
  model
  message_count
  created
  ...
```

Each file returns the plain-text value of that field (with a trailing newline). This lets shell scripts read individual fields with simple `cat` commands instead of piping through `jq`.

## Implementation plan

1. **Add a `ConvStatusDirNode`** in `fuse/filesystem.go` that implements `Readdir` and `Lookup`. `Readdir` returns a directory entry for each status field. `Lookup` returns a `ConvStatusFieldNode` for the requested field name.

2. **Add a `ConvStatusFieldNode`** that reads the conversation state and returns the value of the specific field. Use `FOPEN_DIRECT_IO` to bypass kernel page cache since values are dynamic.

3. **Register `status` as a directory child** of `ConversationNode` (alongside the existing `ctl`, `new`, `id`, `slug`, etc.). Decide whether to keep `status.json` as a file alongside the directory for backwards compatibility or remove it.

4. **Update integration tests** in `fuse/integration_test.go` to verify that `cat /conversation/{id}/status/local_id` returns the expected value, and that `ls /conversation/{id}/status/` lists all expected fields.

## Key files

- `fuse/filesystem.go` — new `ConvStatusDirNode` and `ConvStatusFieldNode` types, update `ConversationNode.Lookup()`
- `state/state.go` — `ConversationState` struct (source of status fields)
- `fuse/integration_test.go` — new test cases
