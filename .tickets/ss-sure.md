---
id: ss-sure
status: open
deps: []
links: []
created: 2026-02-19T02:00:06Z
type: task
priority: 2
assignee: hdp
---
# Fix kernel cache invalidation for backend/default symlink

The backend/default symlink should be able to be changed via ln -s, but the kernel is caching the entry and returning EEXIST even after the symlink is 'removed'. Need to investigate proper cache invalidation for dynamic entries that can be created and removed.

Acceptance criteria:
- ln -s backend-a /shelley/backend/default works
- rm /shelley/backend/default works
- After rm, ln -s backend-b /shelley/backend/default works
- TestBackendSymlink passes
