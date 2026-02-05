---
id: sf-wzqq
status: open
deps: []
links: []
created: 2026-02-04T13:08:00Z
type: feature
priority: 2
---
# Rename /new to /send for message sending

The current /new endpoint creates a conversation and prepares it for use, but the actual act of sending a message to a conversation happens by writing to the 'new' file within a conversation directory. This creates naming confusion. The /new directory name suggests 'new conversation' when it's actually about sending messages.

## Acceptance Criteria

- All references to /new directory renamed to /send
- Documentation updated to reflect the new name
- Tests updated to use /send instead of /new

