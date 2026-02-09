---
id: s4-qnoq
status: in_progress
deps: []
links: []
created: 2026-02-09T03:49:23Z
type: task
priority: 2
assignee: hdp
---
# FOPEN_KEEP_CACHE for ModelFieldNode (fix missing Size in Getattr)

ModelFieldNode.Open (line ~469) returns FOPEN_DIRECT_IO. The node is immutable (value baked in at creation), but Getattr does NOT set out.Size. Read returns m.value+newline, so size should be len(m.value)+1.

Steps:
1. In ModelFieldNode.Getattr, add: out.Size = uint64(len(m.value) + 1)
2. In ModelFieldNode.Open, swap FOPEN_DIRECT_IO to FOPEN_KEEP_CACHE
3. Verify: just test passes (without step 1, tests fail with empty reads)

