---
id: sf-f0yd
status: open
deps: []
links: []
created: 2026-02-07T13:05:47Z
type: task
priority: 3
assignee: hdp
---
# refactor: move README.md from embedded string to file

The README.md is currently embedded as a Go string constant in fuse/filesystem.go. It should be a separate file in the repository for easier editing and better tool integration. The code would then read the file at startup to populate the filesystem node.

