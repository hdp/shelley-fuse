---
id: sf-ksxr
status: closed
deps: []
links: []
created: 2026-02-11T01:58:50Z
type: feature
priority: 2
assignee: hdp
---
# Support conversation cancel

The Shelley server has POST /api/conversation/{id}/cancel to stop an in-progress agent loop. Expose as a FUSE operation, e.g. a writable cancel file or unlink on a working presence file.

