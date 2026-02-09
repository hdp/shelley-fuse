---
id: sf-t87k
status: closed
deps: []
links: [sf-l0h7]
created: 2026-02-09T01:51:13Z
type: task
priority: 2
assignee: hdp
---
# Performance: ls -l on since/ directories is 5x slower than on messages/


## Notes

**2026-02-09T02:30:16Z**

Fixed. Root cause: With EntryTimeout=0, every Lstat during ls -l re-traverses the full FUSE path, and QueryDirNode.Lookup was creating fresh QueryResultDirNodes (Ino=0 auto-assign) each time, losing the FilterSince cache. Each cold node runs O(N) FilterSinceWithToolMap (which JSON-unmarshals every message via MessageSlug), causing O(N²) total work. Fix: use stable inode numbers (FNV-64 hash) so go-fuse reuses existing nodes with their caches. Performance: 5x→1.5x vs messages/.
