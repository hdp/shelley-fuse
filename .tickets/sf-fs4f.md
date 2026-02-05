---
id: sf-fs4f
status: closed
deps: []
links: []
created: 2026-02-04T13:09:42Z
type: task
priority: 1
---
# Use 'agent' consistently instead of 'shelley' in user-facing filesystem paths

The Shelley backend uses Type='shelley' for AI responses, but user-facing documentation and filesystem paths should consistently use 'agent' instead. Currently CLAUDE.md shows message types as 'user, shelley, bash-tool, bash-result' which is confusing because 'shelley' references the internal implementation detail rather than the semantic role.

The filesystem path hierarchy and any slug generation should use 'agent' where possible for consistency with how users understand the conversation. Messages from the AI should appear as 'agent' in directory names, markdown headers, and any other user-facing representations.

## Acceptance Criteria

- Documentation (CLAUDE.md) updated to show 'agent' instead of 'shelley' for message types
- Message directory slugs use 'agent' where appropriate (e.g., 001-agent/ not 001-shelley/)
- Markdown headers generated as '## agent' not '## shelley'
- If backend Type field must remain 'shelley', the mapping to user-facing names is clear and consistent

