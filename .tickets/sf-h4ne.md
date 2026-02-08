---
id: sf-h4ne
status: open
deps: []
links: []
created: 2026-02-08T21:10:35Z
type: task
priority: 2
assignee: hdp
---
# messages/ and messages/last/ can be out of sync

I just had a conversation where messages/last/ was returning fresher data than messages/, so symlinks were broken, possibly due to caching on the client (OS) side?

