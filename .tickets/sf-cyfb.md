---
id: sf-cyfb
status: open
deps: [sf-fbwg, sf-bz2m]
links: []
created: 2026-02-02T14:30:38Z
type: feature
priority: 1
tags: [shelley, fuse, messages]
---
# Fix messages/since/ to use slug-based matching

Update FilterSince in shelley/messages.go to match against message slugs (computed by MessageSlug) instead of raw Type field. This fixes the problem where since/user/1 incorrectly matches tool result messages (which have Type='user' but slug='bash-result'). The person parameter in the URL path is compared against the slug. Examples: since/user/1 returns messages since the last actual user message. since/bash-tool/1 returns messages since the last bash tool call. Update matchPerson or replace it with slug-based comparison. Update QueryDirNode and tests.

## Acceptance Criteria

since/user/1 skips tool result messages and finds the last real user message. since/bash-tool/1 finds the last bash tool call. Integration and unit tests pass.

