---
id: sf-bksa
status: open
deps: []
links: []
created: 2026-02-02T14:51:07Z
type: task
priority: 2
---
# cat file > .../conversation/{id}/new breaks on newline

When sending a message by writing to /new, don't assume a newline means the message is done. We should be able to tell when the filehandle is closed and send the message then.

