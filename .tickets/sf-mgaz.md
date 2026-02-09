---
id: sf-mgaz
status: in_progress
deps: []
links: []
created: 2026-02-09T03:01:25Z
type: feature
priority: 2
assignee: hdp
---
# Make /new a symlink to /models/default/new

Create /new as a symlink to /models/default/new instead of having it as a direct implementation. This simplifies the filesystem by having a single source of truth for the 'new conversation' interface.

