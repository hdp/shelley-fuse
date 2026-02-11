---
id: sf-5o1i
status: open
deps: []
links: []
created: 2026-02-11T13:14:50Z
type: feature
priority: 2
assignee: hdp
---
# Add symlink dir /conversation/last/{N} for most recent conversations

Add a symlink directory /conversation/last/{N} where:
- last/1 is a symlink to the most recent conversation (by created time), regardless of archived status
- last/2 is a symlink to the second most recent conversation
- last/N is a symlink to the Nth most recent conversation

This provides quick access to the most recent conversations without needing to know their IDs.

