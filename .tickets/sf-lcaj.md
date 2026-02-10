---
id: sf-lcaj
status: closed
deps: []
links: []
created: 2026-02-09T16:49:14Z
type: bug
priority: 0
assignee: hdp
---
# The subshell on line 86-87 of scripts/finish-work has a misplaced && that causes a bash syntax error. The && after the closing ) of a subshell at the start of a new line is rejected by bash. Workaround: run finish-work steps manually.

