---
id: sf-oygp
status: closed
deps: [sf-n20r]
links: []
created: 2026-02-08T14:53:40Z
type: task
priority: 2
assignee: hdp
---
# Add runShellWithDiag that dumps diagnostics on timeout

Add a test helper (e.g. runShellDiag or modify runShellOK) that accepts a diag.Tracker. On command timeout (context deadline exceeded), it calls Dump() on the tracker and includes the in-flight ops in the test failure message. This is the key payoff: when 'cat new/clone' hangs, the test output shows exactly which FUSE operation is stuck. Update readme_shell_test.go and integration_test.go to use it.

## Acceptance Criteria

- When a shell command times out, the failure message includes diag Dump output
- Normal (non-timeout) failures are unchanged
- At least one test uses the new helper

