---
id: sf-squ7
status: in_progress
deps: []
links: []
created: 2026-02-09T02:36:10Z
type: task
priority: 2
assignee: hdp
---
# Kernel cache: set short negative-entry timeouts for dynamic presence files

Some files use presence/absence semantics — they exist only in certain states. Currently, negative lookups (ENOENT) have 0 timeout, so the kernel re-asks on every access.

**Dynamic presence files:**
- created — appears once, never disappears (could use long negative timeout after checking, or short before creation)
- archived — can appear and disappear (archive/unarchive), needs short negative timeout (1-3s)
- waiting_for_input — changes with every message, needs very short or 0 negative timeout
- model symlink — set once via ctl, never changes after; ENOENT before set
- cwd symlink — set once via ctl, never changes after; ENOENT before set

**Approach:**
Lookup methods that return ENOENT should call out.SetEntryTimeout() with an appropriate short duration rather than relying on the global 0. For files like "created" that can only transition ENOENT→exists (never back), once they exist, set long entry timeout; when absent, use a short negative timeout (1-3s).

