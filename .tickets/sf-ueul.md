---
id: sf-ueul
status: in_progress
deps: [sf-uqkj]
links: []
created: 2026-02-09T02:17:51Z
type: task
priority: 2
assignee: hdp
---
# Add /start endpoint for starting new conversations from shell

Create `/new/start` file that returns a shell script which can be executed to start a new conversation in the calling shell's PWD.

Example usage:
```bash
cd ~/some-repo && echo "first message" | /shelley/new/start
```

This should start a new conversation with the default model and print the ID of the new conversation.

Additionally, make it possible to start new conversations with specific models via `/shelley/models/<model>/new/start`.

Dependencies: This ticket depends on sf-uqkj.
