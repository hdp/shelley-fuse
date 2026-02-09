---
id: sf-sevm
status: open
deps: []
links: []
created: 2026-02-04T13:16:41Z
type: feature
priority: 2
---
# Expose waiting_for_input as symlink to last agent EndOfTurn

We discovered that checking llm_data.EndOfTurn on agent messages is insufficient for determining if a conversation is waiting for input, because EndOfTurn=true can be followed by tool calls (which means the agent isn't actually done).

Instead, create /conversation/{id}/waiting_for_input as a symlink to messages/{NNN}-agent/llm_data/EndOfTurn using filesystem presence/absence semantics.

**Symlink exists when:**
- Last content-bearing message (excluding gitinfo) is from agent
- Zero or more complete tool call/result pairs follow (no pending tool calls)
- gitinfo messages may follow (ignored for status purposes)
- No user messages follow the agent

**Symlink absent (ENOENT) when:**
- There's a pending tool call (tool message with no matching result yet)
- A user message follows the agent
- Other non-tool-result content follows

This elegantly uses filesystem semantics to express 'is the conversation ready for input?'

## Acceptance Criteria

- /conversation/{id}/waiting_for_input symlink implemented
- Symlink correctly handles tool call/result pairing
- gitinfo messages are ignored for status purposes
- Tests cover edge cases (pending tool calls, user messages, nested tools)


## Notes

**2026-02-06T15:19:32Z**

Implementation complete. Added AnalyzeWaitingForInput function with comprehensive logic for tool call/result pairing. Symlink target points to messages/{N}-agent/llm_data/EndOfTurn. Tests cover: empty conversations, simple agent responses, user messages after agent, pending tool calls, completed tool calls, multiple tool calls, partial completion, and gitinfo messages. All unit and integration tests pass.

**2026-02-06T15:36:06Z**

Fixed critical bugs: (1) isAgentMessage() now checks only Type='shelley' instead of also requiring slug='agent', which was wrong for tool call messages. (2) Added LastAgentSlug field to WaitingForInputStatus and use it when constructing symlink target instead of hardcoding 'agent'. (3) Added test for 'tool call completed with no follow-up text' scenario. All tests pass.

**2026-02-09T16:12:24Z**

The waiting_for_input symlink got lost somehow in some refactoring. Readd it and make sure tests exercise it

**2026-02-09T16:17:10Z**

The code is still present, but even on archived conversations, which by definition should be waiting for input (since they're stopped), waiting_for_input isn't showing up in the conversation's directory listing
