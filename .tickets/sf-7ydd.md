---
id: sf-7ydd
status: closed
deps: []
links: []
created: 2026-02-04T02:00:16Z
type: feature
priority: 2
---
# Tool result markdown should show which command produced each result

Tool result messages can contain multiple results (one per tool call). Currently they just concatenate the output text. They should show which command produced each result.

Example current output:
```
## tool result: bash

tk - minimal ticket system...
some file contents
```

Expected output:
```
## tool result: bash

### command: tk help

```
tk - minimal ticket system...
```

### command: cat /foo

```
some file contents
```
```

This requires correlating tool results with their corresponding tool calls to extract the command that was run.


## Notes

**2026-02-04T03:23:33Z**

Supervisor selected this ticket for implementation work using subagents.

**2026-02-04T03:55:55Z**

Verified that the implementation is complete. The tool result markdown formatting now shows which command produced each result:

- `BuildToolCallMap` builds a map from ToolUseID to ToolCallInfo (name + input)
- `formatToolResultContent` extracts the command from tool input and formats each result with:
  - `### command: <command>` subheader
  - Code block with the output

Test `TestFormatMarkdownMultipleToolResults` verifies the expected format:
- `## tool result: bash` header
- `### command: tk help` followed by code block with output
- `### command: cat /foo` followed by code block with output

All tests pass.
