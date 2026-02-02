---
id: sf-t2kq
status: open
deps: [sf-fbwg]
links: []
created: 2026-02-02T14:29:44Z
type: feature
priority: 1
tags: [fuse, messages]
---
# Named tool call/result files in FUSE

Update MessagesDirNode.Readdir and Lookup to use MessageSlug for filenames instead of raw message Type. Readdir builds the tool name map from all messages and generates filenames like 001-user.md, 100-bash-tool.md, 101-bash-result.md. Lookup fetches the conversation, finds the message by sequence number, computes its slug, and verifies the filename matches. Update messageFileBase() to accept a slug string.

## Acceptance Criteria

ls messages/ shows 001-user.md, 002-shelley.md, 100-bash-tool.md, 101-bash-result.md for conversations with tool use. Reading 100-bash-tool.md returns the tool call message.

