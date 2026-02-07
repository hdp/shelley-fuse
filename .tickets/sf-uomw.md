---
id: sf-uomw
status: open
deps: []
links: []
created: 2026-02-07T13:05:47Z
type: bug
priority: 2
assignee: hdp
---
# docs: fix last/N and since/N documentation in README.md

The embedded /shelley/README.md incorrectly documents last/N and since/slug/N as symlinks directly to messages. After commit 08dfb1d, these are actually directories containing entries (ordinal 0,1,2,... for last/N, or message names for since/slug/N). The documentation needs to be updated with example usage showing the new structure.

