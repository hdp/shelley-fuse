---
id: sf-46rz
status: closed
deps: []
links: []
created: 2026-02-11T01:58:50Z
type: feature
priority: 2
assignee: hdp
---
# Expose subagents directory under conversations

The Shelley server has GET /api/conversation/{id}/subagents returning child conversations. Expose as a subagents/ directory under each conversation node, listing subagent conversations with symlinks back to their entries in the conversation list.

