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

The systemd service file currently hardcodes `http://localhost:9999` as the backend shelley URL. The service should instead dynamically discover the socket address from the `shelley.socket` unit to avoid duplication and ensure consistency.

## Acceptance Criteria

1. Service discovers backend URL from shelley.socket (via `systemctl show` or similar)
2. No hardcoded URL in service file
3. Falls back to reasonable default if socket configuration cannot be determined

