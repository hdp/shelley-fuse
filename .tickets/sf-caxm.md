---
id: sf-caxm
status: closed
deps: []
links: [sf-l0h7]
created: 2026-02-09T02:36:01Z
type: task
priority: 2
assignee: hdp
---
# Kernel cache: set moderate timeouts for semi-stable structural nodes

Some nodes are not immutable but change infrequently enough that short-to-moderate cache timeouts would help. Currently all are 0.

**Semi-stable nodes (suggest 5-30s timeouts):**
- ConversationNode directory — files list changes only when archived status changes, not on every stat
- ConversationNode.Getattr — timestamps update when conversation gets new messages but dont need sub-second freshness
- ConversationListNode.Getattr — directory metadata
- ModelsDirNode / ModelNode / ModelFieldNode / ModelReadyNode — models change very rarely (suggest 5 min)
- FS root, NewDirNode, ReadmeNode — essentially static (suggest 1 hour)

**Approach:**
1. Define timeout constants: cacheTTLImmutable (1h), cacheTTLStatic (1h), cacheTTLModels (5min), cacheTTLConversation (5-10s)
2. Set per-node via out.SetEntryTimeout/SetAttrTimeout in Lookup and out.SetTimeout in Getattr
3. These override the global 0 timeout set in mount options

