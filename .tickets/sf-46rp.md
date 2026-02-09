---
id: sf-46rp
status: in_progress
deps: [sf-l0h7, sf-ym37]
links: []
created: 2026-02-09T02:35:51Z
type: task
priority: 2
assignee: hdp
---
# Kernel cache: remove FOPEN_DIRECT_IO from immutable read-only files

Every Open() in the codebase returns fuse.FOPEN_DIRECT_IO, which bypasses the kernel page cache entirely. For immutable content, this means every read() call goes through FUSE even for repeated reads of the same file.

**Files that should use kernel page cache (remove DIRECT_IO):**
- MessageFieldNode.Open — value is baked into the struct at creation, never changes
- ConvContentNode.Open for individual message content.md (queryBySeq) — message content is immutable
- jsonfs leaf nodes — data is fixed at node creation

**Files that MUST keep FOPEN_DIRECT_IO:**
- CloneNode.Open — generates a new ID each time
- ConvSendNode.Open — writable, side effects
- CtlNode.Open — writable, dynamic state
- MessageCountNode.Open — changes as messages are added
- ConvContentNode.Open for all.md/all.json — grows as conversation progresses
- ConvStatusFieldNode.Open — may change (e.g. slug, id, created)
- ArchivedNode.Open — mutable state

**Approach:**
For immutable files, return FOPEN_KEEP_CACHE instead of FOPEN_DIRECT_IO. Combined with attr timeouts, this lets the kernel serve repeated reads from its page cache. Consider returning 0 (no flags) for files with proper size in Getattr, which allows normal caching.

