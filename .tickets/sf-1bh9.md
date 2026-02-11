---
id: sf-1bh9
status: open
deps: []
links: []
created: 2026-02-11T02:29:08Z
type: feature
priority: 2
assignee: hdp
---
# Parse model field from conversation API responses

The Conversation struct is missing the model field. The server returns model in both /api/conversations and /api/conversation/{id} responses (added in migration 013). Add Model *string to Conversation, capture it when adopting server conversations, and store it in state so the model symlink works for all conversations, not just FUSE-created ones.

