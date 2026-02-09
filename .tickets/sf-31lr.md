---
id: sf-31lr
status: open
deps: []
links: []
created: 2026-02-09T05:01:34Z
type: task
priority: 2
assignee: hdp
---
# avoid hardcoding backend shelley url in systemd service

The systemd service file currently has a hardcoded backend shelley URL. This should be configurable via environment variable or configuration file to allow easy deployment changes without modifying service files.

## Acceptance Criteria

1. Backend shelley URL is configurable via environment variable or config file\n2. Systemd service file reads configuration from the appropriate source\n3. Documentation updated with configuration instructions

