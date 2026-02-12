---
id: sf-u8he
status: closed
deps: []
links: []
created: 2026-02-11T02:29:08Z
type: feature
priority: 2
assignee: hdp
---
# Replace waiting_for_input with working presence file

The server provides a working boolean on /api/conversations and in stream ConversationState events. Replace the current waiting_for_input symlink (which requires expensive message parsing to check EndOfTurn) with a working presence file using the server's state directly.

