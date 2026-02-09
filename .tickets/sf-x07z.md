---
id: sf-x07z
status: closed
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


## Notes

**2026-02-09T16:07:39Z**

Replaced brittle text parsing of systemctl output with JSON parsing. Changed from 'systemctl show shelley.socket -p Listen' to 'systemctl list-sockets shelley.socket --output=json' which returns structured JSON. Updated parseListenAddress to parse JSON instead of text, and updated all corresponding tests.
