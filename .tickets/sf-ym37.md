---
id: sf-ym37
status: in_progress
deps: []
links: []
created: 2026-02-09T02:36:35Z
type: task
priority: 2
assignee: hdp
---
# Kernel cache: add jsonfs cache timeout support via Config

The jsonfs package creates directory/file trees from JSON blobs. Currently its nodes have no awareness of caching. Since jsonfs trees are created from a snapshot of data and never change, they are perfect candidates for long cache timeouts.

**Changes needed:**
1. Add CacheTTL field to jsonfs.Config
2. In jsonfs node Getattr implementations, call out.SetTimeout(config.CacheTTL)
3. In jsonfs directory Lookup implementations, call out.SetEntryTimeout(config.CacheTTL) and out.SetAttrTimeout(config.CacheTTL)
4. In jsonfs leaf Open implementations, return FOPEN_KEEP_CACHE instead of FOPEN_DIRECT_IO when CacheTTL > 0

Callers (MessageDirNode.Lookup for llm_data/usage_data, ConversationNode.Lookup for meta/) set CacheTTL in their jsonfs.Config based on whether the data is immutable (message data: 1h) or semi-stable (conversation metadata: 10s).

