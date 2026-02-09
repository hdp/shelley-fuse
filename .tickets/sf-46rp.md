---
id: sf-46rp
status: in_progress
deps: [sf-l0h7, sf-ym37, s4-clsz, s4-qnoq, s4-wedk]
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


## Notes

**2026-02-09T03:48:58Z**

Investigation findings:

Key discovery: switching from FOPEN_DIRECT_IO to FOPEN_KEEP_CACHE requires that Getattr reports correct Size. Without DIRECT_IO, the kernel uses Size to bound reads — if Size=0, the kernel returns empty data without ever calling Read.

Node-by-node analysis:

EASY (Getattr already reports correct Size):
- MessageFieldNode: Size set correctly (len(value)+1 for newline, or len(value) if noNewline). Just swap flag.
- ReadmeNode: Size = len(readmeContent). Just swap flag.
- ModelReadyNode: Size = 0 (correct, it's an empty presence file). Just swap flag.

NEEDS SIZE FIX (Getattr missing Size):
- ModelFieldNode: Getattr doesn't set out.Size. Read returns value+newline, so Size should be len(value)+1. Fix Getattr then swap flag.

COMPLEX (Size not known at Getattr time):
- ConvContentNode (queryBySeq): Content is fetched from API in Open(), so Getattr can't know the size ahead of time. Options: (a) implement FileGetattrer on ConvContentFileHandle to report size after Open, (b) pre-fetch in Getattr (expensive/defeats purpose), (c) use a different approach.

ALREADY DONE:
- jsonfs leaf nodes: Handled by sf-ym37 — already returns FOPEN_KEEP_CACHE when CacheTimeout > 0.
