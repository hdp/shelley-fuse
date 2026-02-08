---
id: sf-l15g
status: in_progress
deps: [sf-g5ah]
links: []
created: 2026-02-08T14:53:20Z
type: task
priority: 2
assignee: hdp
---
# Add diag HTTP handler to Tracker

Add Handler() method to diag.Tracker that returns an http.Handler. GET /diag returns human-readable text, GET /diag?json returns JSON array of in-flight ops. This is a small addition to the diag package.

## Acceptance Criteria

- Handler serves text by default, JSON with ?json query param
- Empty ops list returns 'no in-flight FUSE operations'
- Unit test exercises both text and JSON responses

