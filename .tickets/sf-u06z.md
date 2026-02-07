---
id: sf-u06z
status: open
deps: []
links: []
created: 2026-02-07T13:05:47Z
type: bug
priority: 2
assignee: hdp
---
# docs: avoid encouraging cat .../messages/all.md in README.md

The Quick Start section in README.md shows 'cat conversation//messages/all.md' as the default way to read responses. This encourages reading entire conversations, which is inefficient for large conversations. We should prefer showing last/N usage or partial reads instead.

