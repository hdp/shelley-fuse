---
id: sf-vxgb
status: open
deps: []
links: []
created: 2026-02-07T02:38:16Z
type: bug
priority: 2
assignee: hdp
tags: [fuse, filesystem, ordering]
---
# Zero-pad message directory names for correct ls ordering

Message directories in messages/ need to be zero-padded to fit the total message count so they appear in the correct order when listed with ls.

Examples:
- If <= 99 messages: 00-user, 01-agent, 02-bash-tool, etc.
- If <= 999 messages: 000-user, 001-agent, 002-bash-tool, etc.

Currently they appear as 0-user, 1-agent, 2-bash-tool which causes lexicographic sorting issues (e.g., 10 comes before 2).

## Acceptance Criteria

- Message directory names are zero-padded based on total message count
- Padding is calculated dynamically (e.g., 150 messages → 3 digits: 000-149)
- ls messages/ shows directories in correct numeric order

