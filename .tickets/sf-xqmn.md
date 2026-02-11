---
id: sf-xqmn
status: closed
deps: []
links: []
created: 2026-02-11T03:04:42Z
type: bug
priority: 1
assignee: hdp
---
# Fix diagnostics not triggering during hanging tests

Diagnostics that are supposed to help figure out why tests are hanging aren't triggering. Need to investigate why the diagnostic code isn't being executed and fix the issue so tests can properly debug hanging scenarios.


## Notes

**2026-02-11T03:38:39Z**

Diagnostics now fire correctly 30s before test deadline. Verified: watchdog goroutine in mountTestFSFull uses t.Deadline() and dumps in-flight FUSE ops + goroutine stacks to stderr.
