---
id: sf-bz2m
status: closed
deps: []
links: []
created: 2026-02-02T14:30:28Z
type: feature
priority: 2
tags: [fuse, messages]
---
# Drop messages/from/

Remove the from/ subdirectory from messages/. Since filenames now encode the message type (e.g., 001-user.md, 100-bash-tool.md), from/{person}/{N} is redundant — users can just look at filenames or use since/. Remove from MessagesDirNode.Lookup and Readdir entries. Remove queryFrom handling from ConvContentNode.formatResult. Optionally keep FilterFrom in shelley/messages.go for library use but remove FUSE exposure. Update all tests referencing from/ paths.

## Acceptance Criteria

ls messages/ no longer shows 'from' directory. Accessing messages/from/ returns ENOENT. All tests pass.


## Notes

**2026-02-03T00:52:55Z**

Decision: Keep FilterFrom in shelley/messages.go for library use (not removing it entirely). The function remains available for programmatic use but is not exposed through FUSE. Implementation complete in commit 6fc67585.

**2026-02-03T01:05:23Z**


