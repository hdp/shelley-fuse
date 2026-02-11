---
id: sf-19kz
status: open
deps: []
links: []
created: 2026-02-11T03:04:14Z
type: task
priority: 2
assignee: hdp
---
# Tests are hanging periodically again, now in ConversationFlow



## Notes

**2026-02-11T13:30:21Z**

Last time this happened it was because the shell tests set cmd.Dir to the mount point, which caused the parent process to hang before exec'ing. These tests don't use a shell, but there may be a similar issue, or it may be completely different e.g. some deadlock
