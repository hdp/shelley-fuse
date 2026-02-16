---
id: sf-b11n
status: open
deps: [sf-ebqt]
links: [sf-u12r, sf-w15c]
created: 2026-02-15T14:45:00Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# BackendNode directory

Create individual backend directory node for `/shelley/backend/{name}/` with Readdir and Lookup. Each backend directory contains: `url` (file), `connected` (presence file), `model/` (directory), `conversation/` (directory), and `new` (symlink to model/default/new). The model and conversation entries initially return ENOENT until wired to ClientManager.
