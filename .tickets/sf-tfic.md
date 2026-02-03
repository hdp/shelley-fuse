---
id: sf-tfic
status: closed
deps: []
links: []
created: 2026-02-03T00:55:15Z
type: task
priority: 2
---
# Add a configurable cache for backend Shelley content

Sometimes shell commands have multiple interactions with the Shelley FUSE fs. It'd be nice if those didn't require a fetch each time. something like a 1s or 5s default for conversation-scoped data -- invalidated with a write anywhere in the conversation -- would be good.
