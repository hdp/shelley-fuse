---
id: sf-q2x4
status: closed
deps: []
links: []
created: 2026-02-02T14:30:19Z
type: feature
priority: 2
tags: [fuse, messages]
---
# Move message_count to messages/count

Move the message_count file from conversation/{id}/message_count to conversation/{id}/messages/count. Add a 'count' case to MessagesDirNode.Lookup that creates a read-only file node returning the message count. Remove message_count from ConversationNode.Lookup/Readdir and ConvStatusFieldNode. Update all tests.

## Acceptance Criteria

cat conversation/{id}/messages/count returns the number of messages. conversation/{id}/message_count no longer exists. Tests pass.

