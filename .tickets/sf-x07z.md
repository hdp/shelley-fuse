---
id: sf-x07z
status: open
deps: []
links: []
created: 2026-02-09T16:05:02Z
type: bug
priority: 2
assignee: hdp
---
# Use systemctl machine-readable output instead of parsing text

Currently the code parses systemctl's text output, which can be brittle. We should use systemctl's machine-readable formats like --output=json instead. For example, use 'systemctl list-sockets --output=json' to get structured JSON data.

## Acceptance Criteria

Replace text parsing with JSON parsing for systemctl commands

