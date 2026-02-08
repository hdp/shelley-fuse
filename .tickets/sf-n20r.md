---
id: sf-n20r
status: open
deps: [sf-q2nw, sf-l15g]
links: []
created: 2026-02-08T14:53:30Z
type: task
priority: 2
assignee: hdp
---
# Expose diag endpoint from test mount helper

Change mountTestFS / mountTestFSWithStore to return a testMount struct (or add a new mountTestFSWithDiag variant) that includes the FS, the fuse.Server, and starts a diag HTTP server on a random port. The test can query the diag endpoint or call fs.Diag.Dump() directly. Return the diag URL in the struct so shell helpers can use it.

## Acceptance Criteria

- mountTestFS still works for existing tests (no signature change, or minimal)
- Diag tracker is accessible from tests
- HTTP diag server starts on random port and shuts down in cleanup

