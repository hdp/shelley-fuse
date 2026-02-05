---
id: sf-tfic
status: closed
deps: []
links: []
created: 2026-02-03T00:55:15Z
type: task
priority: 2
---
# Add a configurable cache for backend Shelley content

Sometimes shell commands have multiple interactions with the Shelley FUSE fs. It'd be nice if those didn't require a fetch each time. something like a 1s or 5s default for conversation-scoped data -- invalidated with a write anywhere in the conversation -- would be good.

## Notes

**2026-02-03T03:45:32Z**

REOPENED: The initial caching implementation (commit dbcea7c) only solved the HTTP fetch layer. 
Real bottleneck remains: MessagesDirNode.Lookup() repeatedly parses ALL messages and rebuilds 
the toolMap for every file lookup (~O(N²) for ls -l on N messages).

Current issue: ls -l on long conversation message directories hangs due to repeated parsing work:
- Readdir() called once: fetches (cached) and lists 2N entries (json+md per message)
- Kernel calls Lookup() for each entry (or stat-like ops)
- Each Lookup(): gets conversation (cached), parses ALL msgs, builds toolMap, finds msg
- For 200-message conversation: 400 lookups × (parse 200 msgs + toolMap 200 msgs) = expensive

Solution needed: Cache parsed messages and toolMap at filesystem level, not just HTTP layer.
Invalidate conversation-level caches (messages + toolMap) on any write to that conversation.

**2026-02-03T03:54:00Z**

IMPLEMENTED: Added ParsedMessageCache at the FUSE filesystem level.

Changes made:
1. Added `ParsedMessageCache` struct in `fuse/filesystem.go` that caches:
   - Parsed `[]shelley.Message` slices
   - Pre-built `toolMap` (map[string]string for tool name lookups)
   - Per-conversation entries with TTL-based expiration

2. Added cache to `FS` struct with 5-second default TTL (matching HTTP cache)
   - `NewFS()` creates cache automatically
   - `NewFSWithCacheTTL()` allows custom TTL configuration

3. Propagated cache through node hierarchy:
   - FS → ConversationListNode → ConversationNode → MessagesDirNode, ConvNewNode

4. Updated `MessagesDirNode.Lookup()` and `MessagesDirNode.Readdir()` to use cache:
   - `GetOrParse()` returns cached or freshly-parsed messages + toolMap
   - Eliminates O(N²) re-parsing on `ls -l` for long conversations

5. Invalidation on writes:
   - `ConvNewFileHandle.Flush()` invalidates cache when message sent
   - Works for both new conversations (StartConversation) and follow-ups (SendMessage)

6. Nil-safe implementation:
   - `GetOrParse()` and `Invalidate()` handle nil receiver gracefully
   - Allows tests to use nodes without cache configured

Tests added:
- `TestParsedMessageCacheReducesParsing` - verifies caching behavior
- `TestParsedMessageCacheNilSafe` - verifies nil-safety
- `TestParsedMessageCacheZeroTTL` - verifies disabled caching with TTL=0

All existing tests pass (unit and integration).
