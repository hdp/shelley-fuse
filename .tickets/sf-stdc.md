---
id: sf-stdc
status: closed
deps: []
links: []
created: 2026-02-11T01:58:50Z
type: feature
priority: 2
assignee: hdp
---
# Support conversation hard-delete

The Shelley server has POST /api/conversation/{id}/delete. Currently FUSE only supports archive/unarchive. Add delete support, e.g. via rmdir on the conversation directory.

