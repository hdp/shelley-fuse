---
id: sf-m8hf
status: open
deps: []
links: []
created: 2026-02-02T15:43:58Z
type: bug
priority: p2
---
# Tool calls and results have inconsistent slugs

They currently are sometimes e.g. '003-bash-tool' but other times '003-subagent-tool' and similarly often or always '004-tool-result' instead of '004-bash-result'. They should always use the tool name, never 'tool' or 'subagent' (unless 'subagent' is the name of a tool').

