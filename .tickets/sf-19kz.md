---
id: sf-19kz
status: open
deps: []
links: []
created: 2026-02-11T03:04:14Z
type: task
priority: 2
assignee: hdp
---
# Tests are hanging periodically again, now in ConversationFlow


## Notes

**2026-02-11T03:38:44Z**

With diagnostics fix (sf-xqmn), hang in TestConversationFlow is confirmed: ConvSendFileHandle.Flush and ConversationListNode.Readdir both stuck waiting on HTTP responses from the Shelley server. The Shelley server (started by startShelleyServer) is apparently not responding. Both calls are shelley.StartConversation and shelley.ListConversations — the shelley.Client has no HTTP timeout set.
