---
id: sf-md7j
status: in_progress
deps: []
links: []
created: 2026-02-09T04:37:06Z
type: task
priority: 2
assignee: hdp
---
# Refactor fuse/filesystem.go - split into logical packages

Split up fuse/filesystem.go and fuse/filesystem_test.go logically to make it easier to navigate. Try to find natural API boundaries and create separate packages, if possible. If this is too complicated, stop and split it up into several tickets for refactoring pieces out one by one.

## Acceptance Criteria

- fuse/filesystem.go is split into logical, focused packages
- Each package has clear API boundaries and single responsibility
- All existing tests still pass after refactoring
- Code is easier to navigate and understand

