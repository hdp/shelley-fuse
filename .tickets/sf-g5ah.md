---
id: sf-g5ah
status: in_progress
deps: []
links: []
created: 2026-02-08T14:53:01Z
type: task
priority: 2
assignee: hdp
---
# Add fuse/diag package with operation tracker

Create fuse/diag/diag.go with a Tracker that records in-flight FUSE operations. Each op has: ID, Node type, Method name, Detail string, Started timestamp. Tracker has Track(node, method, detail) returning a done func, InFlight() returning sorted snapshot, Dump() returning human-readable text. Include a nil-safe package-level Track() helper so callers don't need nil checks. No HTTP handler yet — just the core data structure.

## Acceptance Criteria

- Tracker.Track returns done func that removes the op
- InFlight returns ops sorted by start time
- Dump returns readable multi-line summary
- Package-level Track(nil, ...) is a no-op
- Unit tests for all of the above

