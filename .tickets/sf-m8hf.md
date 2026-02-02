---
id: sf-m8hf
status: closed
deps: []
links: []
created: 2026-02-02T15:43:58Z
type: bug
priority: p2
---
# Tool calls and results sometimes use generic slugs

Tool calls and results should ALWAYS use the actual tool name (e.g., "bash", "subagent", "patch") with the appropriate suffix ("-tool" or "-result").

Currently they sometimes fall back to generic keywords like "tool" or "result" instead of the specific tool name.

**Examples:**
- ✅ Correct: "001-bash-tool", "002-bash-result", "003-subagent-tool"
- ❌ Wrong: "001-tool", "002-tool-result", "003-result" (these are the bug!)

Note: "subagent" is a valid tool name, not a generic keyword. The problem is when the code doesn't have access to the actual tool name and falls back to generic placeholders.

## Root Cause Investigation Needed

The bug occurs when:. ToolName is empty in ContentTypeToolUse
2. toolMap lookup fails for ContentTypeToolResult

The fix should ensure that tool names are ALWAYS available when generating slugs, not add more generic fallbacks.

## Acceptance Criteria

Tool calls and results ALWAYS use {actual-tool-name} suffixes:
- Tool calls: {tool-name}-tool (e.g., "bash-tool", "subagent-tool")
- Tool results: {tool-name}-result (e.g., "bash-result", "subagent-result")

No generic "tool" or "result" filenames should appear in practice. All tests pass.

**2026-02-02T16:25:00Z**

## Root Cause Found and Fixed

### Root Cause
The `ContentItem` struct was missing the `ID` field. The Shelley API uses different fields for the tool identifier:
- **Tool Use (Type 5)**: `ID` field contains the tool use identifier (e.g., "toolu_01VGnH57QLaLQ1kLNRR4gPNp")
- **Tool Result (Type 6)**: `ToolUseID` field references the tool use identifier

The code was expecting `ToolUseID` to be populated in tool_use messages, but it's always empty there. The actual identifier is in the `ID` field.

### Changes Made

1. **shelley/messages.go**:
   - Added `ID` field to `ContentItem` struct with proper JSON tag
   - Updated `BuildToolNameMap()` to use `item.ID` (primary) and `item.ToolUseID` (fallback) when building the tool name map for tool_use messages

2. **Test data updates**:
   - Updated `shelley/messages_test.go` to use `ID` field in tool_use test messages (matching real API format)
   - Updated `fuse/filesystem_test.go` to use `ID` field for tool_use and `ToolUseID` for tool_result (matching real API format)

### Verification
- All unit tests pass: `just test`
- All integration tests pass: `just test-integration`
- Verified with actual Shelley API data from production database:
  - Tool use message: `{"ID":"toolu_01...","Type":5,"ToolName":"think",...}` → slug: "think-tool" ✓
  - Tool result message: `{"Type":6,"ToolUseID":"toolu_01...",...}` → slug: "think-result" ✓

**2026-02-02T16:28:00Z**

## Fix Updated - Removed Generic Fallbacks

Previous implementation incorrectly added explicit generic fallbacks (`return "tool"`, `return "result"`).
The ticket requirement was to ensure tool names are always available, NOT to add fallbacks.

### Updated Fix:
1. **MessageSlug()**: Removed explicit `return "tool"` and `return "result"` fallbacks
   - When tool name can't be determined, now falls through to `msg.Type` (e.g., "shelley", "user")
   - This is more descriptive than generic "tool"/"result"

2. **Tests**: 
   - Removed `TestMessageSlugToolUseEmptyName` (was validating wrong behavior)
   - Updated `TestMessageSlugToolResultUnknown` to expect "user" (msg.Type) instead of "result"

### Behavior Summary:
- Normal case: `{toolname}-tool` or `{toolname}-result` ✓
- Edge case (empty ToolName in tool_use): Falls back to `msg.Type` (e.g., "shelley")
- Edge case (unknown ToolUseID in tool_result): Falls back to `msg.Type` (e.g., "user")

All tests pass.
