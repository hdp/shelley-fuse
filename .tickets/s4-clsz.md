---
id: s4-clsz
status: open
deps: []
links: []
created: 2026-02-09T03:49:17Z
type: task
priority: 2
assignee: hdp
---
# FOPEN_KEEP_CACHE for MessageFieldNode, ReadmeNode, ModelReadyNode

These three node types are immutable and their Getattr already reports correct Size. The change is a simple flag swap from FOPEN_DIRECT_IO to FOPEN_KEEP_CACHE in Open():

**MessageFieldNode.Open** (line ~1623): Value is baked into the struct at creation. Getattr already sets out.Size correctly (accounting for noNewline). Swap flag.

**ReadmeNode.Open** (line ~310): Content is an embedded string constant. Getattr already sets out.Size = len(readmeContent). Swap flag.

**ModelReadyNode.Open** (line ~496): Empty presence file. Getattr sets out.Size = 0. Swap flag.

Verify: just test passes after each change.

