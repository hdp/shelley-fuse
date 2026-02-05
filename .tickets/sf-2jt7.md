---
id: sf-2jt7
status: closed
deps: []
links: []
created: 2026-02-04T02:00:21Z
type: feature
priority: 2
---
# Tool call arguments should be formatted more readably

Tool call markdown currently shows arguments as raw JSON:
```
{
  "command": "tk help"
}
```

For common tools like bash, this should be formatted more readably, e.g.:
```
command: tk help
```

Or for simple single-field inputs, just show the value directly.


## Notes

**2026-02-04T02:38:08Z**

Implemented readable formatting for tool call arguments:
- Single-field objects with simple values: `key: value` format
- Multi-field objects with simple values: multiple `key: value` lines (sorted)
- Complex nested objects/arrays: fall back to pretty-printed JSON

Examples:
- `{"command": "ls -la"}` → `command: ls -la`
- `{"path": "test.txt", "operation": "replace"}` → `operation: replace\npath: test.txt`

Added 9 test cases covering different input structures.
