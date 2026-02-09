---
id: sf-l0h7
status: in_progress
deps: []
links: [sf-t87k, sf-caxm]
created: 2026-02-09T02:35:41Z
type: task
priority: 2
assignee: hdp
---
# Kernel cache: set long entry/attr timeouts for immutable message nodes

Currently all entry and attr timeouts are 0 (set globally in main.go and tests), meaning the kernel re-asks for every stat/lookup. Many nodes contain data that never changes once a message exists.

**Immutable message field nodes** (under messages/{NNN}-{slug}/):
- message_id, conversation_id, sequence_id, type, created_at, content.md
- llm_data/, usage_data/ (and their jsonfs subtrees)

These should get long entry/attr timeouts (e.g. 1 hour) via out.SetEntryTimeout() / out.SetAttrTimeout() in their parent Lookup, and out.SetTimeout() in their Getattr.

**Approach:**
1. In MessageDirNode.Lookup, call out.SetEntryTimeout(1h) and out.SetAttrTimeout(1h) for each child
2. In MessageDirNode.Getattr, call out.SetTimeout(1h)
3. In MessageFieldNode.Getattr, call out.SetTimeout(1h)
4. In jsonfs node Getattr, set long timeouts (via Config)
5. In MessagesDirNode.Lookup, set long timeouts when returning MessageDirNode children (names are immutable)

Directly addresses ls -l performance: stat calls served from kernel cache instead of FUSE round-trips.

