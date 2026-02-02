---
id: sf-bksa
status: open
deps: []
links: []
created: 2026-02-02T14:51:07Z
type: task
priority: 0
---
# cat file > .../conversation/{id}/new breaks on newline

When sending a message by writing to /new, don't assume a newline means the message is done. We should be able to tell when the filehandle is closed and send the message then.


## Notes

**2026-02-02T15:37:35Z**

This implementation is broken and the tests don't catch it. Sending to /new no longer creates a conversation.
