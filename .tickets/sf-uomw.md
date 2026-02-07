---
id: sf-uomw
status: closed
deps: []
links: []
created: 2026-02-07T13:05:47Z
type: bug
priority: 2
assignee: hdp
---
# docs: fix last/N and since/N documentation in README.md

The embedded /shelley/README.md incorrectly documents last/N and since/slug/N as symlinks directly to messages. After commit 08dfb1d, these are actually directories containing entries (ordinal 0,1,2,... for last/N, or message names for since/slug/N). The documentation needs to be updated with example usage showing the new structure.


## Notes

**2026-02-07T13:46:20Z**

Code review of fuse/README.md: PASS. Documentation accurately reflects implementation. last/{N}/ directories with ordinal symlinks and since/{slug}/{N}/ directories with message-name symlinks are correctly documented. FilterSince semantics (Nth-to-last occurrence, excluding reference message) are accurately described. Common operations section has correct usage examples. Minor: layout tree mixes actual children with examples at same indentation (pre-existing convention, not a regression).
