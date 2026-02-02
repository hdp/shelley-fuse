---
id: sf-fbwg
status: open
deps: []
links: []
created: 2026-02-02T14:29:36Z
type: feature
priority: 1
tags: [shelley, messages]
---
# Message slug detection from content

Add MessageSlug() function to shelley/messages.go that inspects message JSON content to determine the slug used in filenames and since/ matching. For tool_use (Content[].Type==5), return '{toolname}-tool'. For tool_result (Content[].Type==6), cross-reference ToolUseID to find the tool name and return '{toolname}-result'. For regular messages, return lowercased Type field. Also add BuildToolNameMap() to build ToolUseID→ToolName mapping from a message list.

## Acceptance Criteria

MessageSlug returns 'bash-tool' for a tool call message with ToolName=bash. MessageSlug returns 'bash-result' for a tool result whose ToolUseID matches a bash tool call. MessageSlug returns 'user' for a plain user message. Unit tests pass.

