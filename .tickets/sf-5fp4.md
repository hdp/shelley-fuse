---
id: sf-5fp4
status: closed
deps: []
links: []
created: 2026-02-11T14:35:12Z
type: task
priority: 2
assignee: hdp
---
# Run FUSE daemon in a separate process during integration tests

The integration tests currently run the FUSE daemon in-process. This means
the test process itself can enter D state (uninterruptible sleep) if it
touches the FUSE mount during cleanup or path resolution, creating a
self-deadlock that no Go-level timeout can break.

Running the FUSE daemon in a child process would fully isolate the test
process from the mount, eliminating this class of bug entirely. The test
process would only interact with the mount via normal file operations, and
if the child FUSE process hangs, the test can kill it and lazy-unmount.

This would also fix the issue where `t.TempDir()` cleanup can block trying
to `os.RemoveAll` a directory containing a stuck mount.

