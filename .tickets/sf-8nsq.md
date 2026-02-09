---
id: sf-8nsq
status: closed
deps: [sf-l0h7]
links: []
created: 2026-02-09T02:36:27Z
type: task
priority: 3
assignee: hdp
---
# Kernel cache: use stable inode numbers for message field nodes

Currently all nodes use Ino: 0 in StableAttr (auto-assign), meaning each Lookup creates a new kernel inode. For message field nodes, using a stable deterministic inode number would let the kernel recognize the same logical file across lookups and reuse cached data.

**Approach:**
Compute stable inodes by hashing (conversationID, sequenceID, fieldName). This ensures the same message field always maps to the same kernel inode. Combined with entry/attr timeouts, this eliminates unnecessary invalidation when the kernel forgets and re-discovers the same inode.

**Note:** This is lower priority — the timeout-based caching (sf-l0h7) provides the biggest win. Stable inodes are an additional optimization that may help with certain access patterns (e.g., holding file descriptors open across cache expiry).

