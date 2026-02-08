---
id: sf-h4ne
status: closed
deps: []
links: []
created: 2026-02-08T21:10:35Z
type: task
priority: 2
assignee: hdp
---
# messages/ and messages/last/ can be out of sync

I just had a conversation where messages/last/ was returning fresher data than messages/, so symlinks were broken, possibly due to caching on the client (OS) side?


## Notes

**2026-02-08T21:21:36Z**

Root cause: ParsedMessageCache had its own independent TTL, separate from CachingClient's TTL. When CachingClient's cache expired and returned fresh data, ParsedMessageCache.GetOrParse() would ignore the new rawData and return stale parsed results because its own TTL hadn't expired yet. QueryDirNode/QueryResultDirNode (messages/last/) bypassed ParsedMessageCache entirely, always parsing fresh data. This meant messages/ could serve stale data while last/ served fresh data, producing broken symlinks.

Fix: Replaced time-based ParsedMessageCache with content-addressed caching (FNV-1a checksum of raw bytes). Now all nodes share the same ParsedMessageCache, and the cache only returns a previously-parsed result when the upstream data is byte-identical. Different data always triggers a re-parse. Also threaded parsedCache through to QueryDirNode, QueryResultDirNode, ConvContentNode, and MessageCountNode so all code paths share the same cache.
