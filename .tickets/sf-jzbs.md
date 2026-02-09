---
id: sf-jzbs
status: closed
deps: [sf-l0h7]
links: []
created: 2026-02-09T02:36:19Z
type: task
priority: 2
assignee: hdp
---
# Kernel cache: add FOPEN_CACHE_DIR to immutable directory readdir results

The FOPEN_CACHE_DIR flag (available in go-fuse v2) tells the kernel to cache readdir results. This avoids repeated FUSE round-trips for directory listings.

**Directories with immutable readdir results:**
- MessageDirNode.Readdir — the list of fields (message_id, conversation_id, etc.) is fixed at creation. llm_data/usage_data presence is determined once.
- jsonfs directory nodes — structure is fixed at creation

**Directories that MUST NOT cache readdir:**
- ConversationListNode — conversations can be added/adopted at any time
- MessagesDirNode — messages are added as conversation progresses
- ConversationNode — archived/waiting_for_input entries change dynamically
- QueryDirNode, QueryResultDirNode — dynamic content

**Approach:**
Where supported, add FOPEN_CACHE_DIR to the flags returned by Readdir-capable nodes that have immutable content. Note: FOPEN_CACHE_DIR requires FUSE kernel support (Linux 5.1+); verify go-fuse properly supports it.

