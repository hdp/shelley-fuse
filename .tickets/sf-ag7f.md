---
id: sf-ag7f
status: closed
deps: []
links: []
created: 2026-02-08T21:26:15Z
type: task
priority: 2
assignee: hdp
---
# ls -l on messages/since/user/1/ is very slow with tens or 100+ messages, caching may have been broken


## Notes

**2026-02-08T21:40:52Z**

Root cause: ls -l on since/user/1/ triggered O(N) FilterSince calls (each rebuilding tool map) plus O(N) FNV checksums and data copies via CachingClient. Fix: (1) Cache filtered results on QueryResultDirNode with pointer-identity check, (2) Eliminate byte-copy in CachingClient cache hits, (3) Add pointer-identity fast path to ParsedMessageCache, (4) Add WithToolMap variants to avoid redundant tool map rebuilding, (5) Cache maxSeqID in ParsedMessageCache to avoid O(N) recomputation per lookup.
