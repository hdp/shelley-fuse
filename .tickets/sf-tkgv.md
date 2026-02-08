---
id: sf-tkgv
status: closed
deps: [sf-oygp]
links: []
created: 2026-02-08T14:53:48Z
type: task
priority: 2
assignee: hdp
---
# Add goroutine dump to diag on timeout

Enhance the timeout diagnostic output to also include a goroutine stack dump (runtime.Stack) alongside the in-flight FUSE ops. This catches cases where the hang is in go-fuse internals or the kernel driver rather than in our FUSE method implementations.

## Acceptance Criteria

- Timeout diagnostic output includes both diag.Dump() and goroutine stacks
- Goroutine dump is truncated to a reasonable size (e.g. 64KB)


## Notes

**2026-02-08T19:58:54Z**

Implementation complete. Unit tests pass. go test ./... hangs on integration tests (pre-existing).
