---
id: sf-yywz
status: closed
deps: [sf-fbwg]
links: []
created: 2026-02-02T14:29:57Z
type: feature
priority: 1
tags: [shelley, messages, markdown]
---
# Markdown formatting for tool calls and results

Update FormatMarkdown in shelley/messages.go to produce better output for tool messages. For tool calls (Content[].Type==5): header '## tool call', body shows tool name and pretty-printed input. For tool results (Content[].Type==6): header '## tool result', body shows the output text extracted from ToolResult[].Text. Regular messages keep current behavior (## user, ## shelley with extracted content). Uses the same content parsing as MessageSlug.

## Acceptance Criteria

cat messages/100-bash-tool.md shows '## tool call' header with tool name and input. cat messages/101-bash-result.md shows '## tool result' header with output text. Regular user/shelley messages unchanged.


## Notes

**2026-02-02T15:10:17Z**

Implemented FormatMarkdown updates: tool calls show '## tool call' with tool name and pretty-printed input; tool results show '## tool result' with extracted output text; regular messages unchanged. Added 5 new tests.

**2026-02-02T15:12:35Z**

Implementation complete and reviewed. All acceptance criteria met:
- Tool calls show '## tool call' header with tool name and pretty-printed input
- Tool results show '## tool result' header with output text
- Regular messages unchanged
- All 54 tests pass
- Code review passed with no issues found
