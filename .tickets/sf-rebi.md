---
id: sf-rebi
status: in_progress
deps: []
links: []
created: 2026-02-10T02:46:04Z
type: bug
priority: 2
assignee: hdp
---
# Tab completion fails in archived conversation's directory and messages/ subdirectory

When attempting to use tab completion (bash/zsh autocomplete) within an archived conversation's directory or its messages/ subdirectory, completion fails to work properly. This makes it difficult to explore archived conversations or navigate their message files using standard shell tab completion.

<<<<<<< Updated upstream
=======

## Notes

**2026-02-10T02:48:28Z**

Tab completion uses readdir to get directory entries. Found that ConversationListNode.Readdir() calls fetchServerConversations() but NOT fetchArchivedConversations(). However, ConversationListNode.Lookup() calls both. This means archived conversations are accessible via lookup (explicit path) but don't appear in readdir output (tab completion). The fix is to also fetch archived conversations in Readdir() and include them in the listing.

**2026-02-10T02:48:31Z**

Root cause identified: ConversationListNode.Readdir() doesn't call fetchArchivedConversations(), so archived conversations don't appear in directory listings (tab completion). Need to: 1) Call fetchArchivedConversations() in Readdir() and include those conversations. 2) Check if the same issue affects MessagesDirNode.Readdir() for archived conversations - messages retrieval might fail for archived convs.

**2026-02-10T02:50:21Z**

Fix implemented and tested. Summary:

Root cause: ConversationListNode.Readdir() only fetched active conversations (via fetchServerConversations) and built a validServerIDs set to filter out 'stale' entries. Archived conversations got filtered out because they weren't in the validServerIDs set, even though they were valid conversations.

Fix: Modified ConversationListNode.Readdir() to also call fetchArchivedConversations() and include those conversation IDs in the validServerIDs set. This ensures archived conversations appear in directory listings and tab completion works.

The fix is simple and minimal - it just adds ~10 lines to include archived conversations in the same way active conversations are included. The MessagesDirNode doesn't need changes because it already uses GetConversation which works for both archived and active conversations.

Test: Added TestArchivedConversationInDirectoryListing which verifies archived conversations appear in directory listings and their subdirectories remain accessible. All tests pass.

**2026-02-10T02:53:48Z**

CORRECTION: The fix I implemented was incorrect. Archived conversations should NOT appear in /conversation/ directory listings (they would pollute the directory). 

The actual issue is that within an already-accessed archived conversation directory (e.g., when you cd into conversation/abc where abc is archived), tab completion for files inside it (like ctl, send, messages/) fails. The messages/ subdirectory's tab completion also fails for message entries.

The fix needs to be about ensuring Readdir() works correctly for files/enums inside an archived conversation's directory and its messages/ subdirectory, NOT about showing archived conversations in /conversation/ listings.

Need to investigate why files/messages listings fail within archived conversation directories.
>>>>>>> Stashed changes
