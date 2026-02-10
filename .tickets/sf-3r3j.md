---
id: sf-3r3j
status: open
deps: []
links: []
created: 2026-02-10T16:51:14Z
type: bug
priority: 2
assignee: hdp
---
# Fix archived conversations visibility and completion

Ticket sf-rebi was wrong. The desired behavior is:
1) archived conversations do not show up in the listing of conversation/ (they would clutter it up)
2) tab completion WITHIN an archived conversation's directory works (e.g. conversation/{ID}/ctl or messages/). This was working previously, so it should be achievable.

Tests should verify both of these requirements.

