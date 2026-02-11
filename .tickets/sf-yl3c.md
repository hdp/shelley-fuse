---
id: sf-yl3c
status: closed
deps: []
links: []
created: 2026-02-11T01:58:50Z
type: bug
priority: 2
assignee: hdp
---
# Capture cwd from adopted conversations

The server returns cwd in conversation objects but shelley-fuse doesn't capture it when adopting server-side conversations. Update AdoptWithMetadata to also store cwd from API responses.

