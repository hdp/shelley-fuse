---
id: sf-s8na
status: open
deps: []
links: []
created: 2026-02-09T04:26:45Z
type: chore
priority: 2
assignee: hdp
---
# Deduplicate CLAUDE.md and fuse/README.md

fuse/README.md is exposed at the mountpoint ($mntpoint/README.md) as API documentation for clients. CLAUDE.md happens to reuse it as developer docs, creating duplication. We need to deduplicate while ensuring CLAUDE.md still directs agents to read fuse/README.md for architecture details.

## Acceptance Criteria

- fuse/README.md remains as authoritative source exposed at $mntpoint/README.md for clients
- CLAUDE.md focuses on agent guidance, build/test commands, and ticket workflow only
- CLAUDE.md directs agents to read fuse/README.md for filesystem architecture and usage
- Only authoritative architecture documentation exists in fuse/README.md
- No duplication between CLAUDE.md and fuse/README.md
