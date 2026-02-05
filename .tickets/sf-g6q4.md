---
id: sf-g6q4
status: closed
deps: []
links: []
created: 2026-02-03T03:39:32Z
type: task
priority: 2
---
# markdown tool calls should show arguments, not just e.g. 'bash'


## Notes

**2026-02-03T04:52:29Z**

The header should stay the same but the body of the tool call block should have the tool call args

**2026-02-03T13:40:26Z**

Implemented: Header now includes tool name (e.g., '## tool call: bash'), body shows only the pretty-printed input arguments.

**2026-02-04T01:41:19Z**

Tool calls are not showing actual tool arguments in markdown, they should show something useful but right now only show 'bash' for example

**2026-02-04T01:47:46Z**

Verified: Fix is already implemented in the working tree (commit lpqyrwou). The bug on main: formatToolCallContent wrote ToolName to body, and header was just 'tool call'. Current fix: header is 'tool call: bash', body shows only the pretty-printed Input JSON. All tests pass.

**2026-02-04T01:52:07Z**

Root cause found: JSON field name mismatch. Real API returns 'ToolInput' but Go struct has json tag 'Input'. This causes item.Input to always be empty, so formatToolCallContent returns empty string. Fix: change json tag from Input to ToolInput.

**2026-02-04T01:53:28Z**

Additional issue: formatMessageMarkdown returns early on first tool call, ignoring: 1) Text content that precedes tool calls (Type 2), 2) Multiple tool calls in the same message. Real messages often have text explanation followed by multiple tool calls. Need to process ALL content items in the message.

**2026-02-04T01:55:05Z**

TWO BUGS identified: 1) JSON field name mismatch - struct uses 'Input' but API returns 'ToolInput'. 2) formatMessageMarkdown returns early on first tool call, ignoring preceding text (Type 2) and all subsequent tool calls. Real messages have multiple content items that all need to be rendered.
