---
id: s4-wedk
status: open
deps: []
links: []
created: 2026-02-09T03:49:32Z
type: task
priority: 2
assignee: hdp
---
# FOPEN_KEEP_CACHE for ConvContentNode queryBySeq (individual message content.md)

ConvContentNode.Open (line ~2069) returns FOPEN_DIRECT_IO for all query kinds. For queryBySeq (individual message content), the data is immutable and should use FOPEN_KEEP_CACHE.

**Complication:** Content is fetched from the API during Open(), so Getattr cannot report the correct Size ahead of time. Without DIRECT_IO, the kernel uses Size (which is 0) to bound reads, returning empty data.

**Approach options:**
(a) Implement fs.FileGetattrer on ConvContentFileHandle so after Open() the kernel can get the real size from the file handle. The handle already has the content cached.
(b) Pre-fetch content in Getattr for queryBySeq (expensive, partially defeats caching purpose).
(c) Accept that ConvContentNode queryBySeq needs DIRECT_IO and skip this optimization.

Option (a) is preferred — add a Getattr method to ConvContentFileHandle that reports len(content) as Size. Only return FOPEN_KEEP_CACHE when query.kind == queryBySeq; all other query kinds keep FOPEN_DIRECT_IO.

