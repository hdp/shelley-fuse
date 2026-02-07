---
id: sf-xt31
status: in_progress
deps: []
links: []
created: 2026-02-05T04:54:35Z
type: task
priority: 2
---
# Write integration tests that exercise README.md example flows


## Notes

**2026-02-05T04:54:46Z**

This ticket replaces lost ticket sf-g3l2 from the jj repo recovery.

The goal is to write integration tests that exercise the exact shell command flows documented in README.md's Quick Start and Common Operations sections.

Key flows to test:
1. Allocate new conversation: ID=$(cat new/clone)
2. Configure model and cwd via ctl file
3. Send first message to create conversation on backend
4. Read response via messages/all.md
5. Send follow-up message
6. List available models: ls models/
7. Check default model: readlink models/default
8. List conversations: ls conversation/
9. List last N messages: ls conversation/$ID/messages/last/5/
10. Read last N message contents: cat conversation/$ID/messages/last/5/*/content.md
11. List messages since user's last message: ls conversation/$ID/messages/since/user/1/
12. Get message count: cat conversation/$ID/messages/count
13. Check if conversation created: test -e conversation/$ID/created

Tests should use actual shell commands (via exec.Command with bash -c) rather than reimplementing the operations in Go test code. This validates the documented user experience.
