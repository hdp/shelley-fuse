---
id: sf-q2nw
status: closed
deps: [sf-g5ah]
links: []
created: 2026-02-08T14:53:11Z
type: task
priority: 2
assignee: hdp
---
# Add Diag tracker to FS and thread to I/O nodes

Add a diag.Tracker field to FS (always created by NewFS/NewFSWithCacheTTL). Thread it through NewDirNode, CloneNode, ConversationListNode, ConversationNode, ConvSendNode, ConvContentNode, ModelsDirNode — every node type that makes HTTP calls or state mutations. Add 'defer diag.Track(...)()' to each method that does I/O: CloneNode.Open, CloneFileHandle.Read, ConversationListNode.Lookup/Readdir, ConversationNode.Lookup/Readdir/Create/Unlink, ConvSendFileHandle.Flush, ConvContentNode.Open, ModelsDirNode.Lookup/Readdir, MessagesDirNode.Lookup/Readdir, QueryDirNode.Lookup, QueryResultDirNode.Lookup/Readdir.

## Acceptance Criteria

- FS.Diag is non-nil after NewFS
- All methods that call client.* or state.Clone are instrumented
- Detail string includes the lookup name or conversation ID where applicable
- Existing tests still pass (no behavioral change)

