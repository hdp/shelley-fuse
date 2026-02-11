---
id: sf-3r3j
status: closed
deps: []
links: []
created: 2026-02-10T16:51:14Z
type: bug
priority: 2
assignee: hdp
---
# Fix archived conversations visibility and completion

Ticket sf-rebi was wrong. The desired behavior is:
1) archived conversations do not show up in the listing of conversation/ (they would clutter it up)
2) tab completion WITHIN an archived conversation's directory works (e.g. conversation/{ID}/ctl or messages/). This was working previously, so it should be achievable.

Tests should verify both of these requirements.


## Notes

**2026-02-10T16:56:18Z**

This broke tab completion under conversation/{ID}/ for archived conversations. More investigation is required to find out exactly what sequence of syscalls are involved

**2026-02-10T17:11:16Z**

Investigation notes: current implementation (commit 4bc40e0) hides archived conversations from /conversation listing by filtering archivedServerIDs in ConversationListNode.Readdir, but still allows direct Lookup by local/server/slug via fetchArchivedConversations. Tab completion regression likely due to kernel requiring parent dir Readdir results for Lookup completion; hiding archived entries means shell completion for conversation/{ID}/... may fail unless the user already has the ID path. Lookup should still work (tests read conversation/{ID}/messages/count). Possible culprit: shell completion uses readdir on parent, so once archived entry is filtered out, completion inside archived conversation is only available if path is already resolved. Need syscall trace (strace on shell completion) to confirm; expect sequence: readdir on /conversation filters out archived ID, then shell may not attempt lookup for subpaths. No mockserver coverage for archived endpoints; integration tests use real shelley. Consider using diag Tracker or strace to capture ops during completion.

**2026-02-10T17:17:22Z**

Investigation: reviewed fuse/conversations.go. Readdir now builds archivedServerIDs from ListArchivedConversations, then filters out any mapping whose ShelleyConversationID is archived (so local IDs + serverID/slug symlinks are removed from /conversation listing). State entries are NOT deleted, so ConversationListNode.Lookup still resolves local IDs via state.Get and server IDs/slugs via state.GetByShelleyID/GetBySlug (or fetchArchivedConversations for untracked archived convs). Therefore direct path access should work.

Likely regression for tab completion is at shell behavior: bash/zsh completion for paths uses opendir+readdir on parent; archived IDs no longer appear so completion of conversation/<ID>/… cannot offer <ID> (and possibly won’t even opendir it unless user types the full ID). This would manifest as “completion under conversation/{ID}/” failing unless ID is fully known. Needs syscall trace to confirm sequence (expect getdents64 on /conversation, no candidate -> no further lookup/opendir). If full ID typed, lookup should still succeed.

No evidence in code that Lookup for local ID would fail post-archive; only possible failure is parent-dir readdir not listing entries. Next step: strace bash completion with archived convo to confirm readdir/lookup sequence.

**2026-02-10T17:21:45Z**

Note that e.g. `ls /shelley/conversation/cZWOJ7B/c<tab>` fails to complete, so typing the full ID doesn't help.

**2026-02-10T17:28:45Z**

Reviewed fuse/conversations.go. Archived conversations are filtered out of ConversationListNode.Readdir by skipping mappings whose Shelley ID is in archivedServerIDs. State entries are retained, and Lookup should still resolve local IDs + server IDs/slugs (Get / GetByShelleyID / GetBySlug + fetchArchivedConversations). Integration test already verifies direct path access via local ID and server ID. Tab-completion failure even with full ID likely due to shell completion algorithm: bash/zsh expand path components using readdir; if intermediate component (conversation ID) is not returned by readdir, completion won't descend into that directory even when the component is fully typed. This matches report that  fails. Need syscall trace (strace bash completion) to confirm, but likely the shell doesn't attempt lookup/opendir on /conversation/<ID> when the ID is absent from parent readdir.

**2026-02-10T17:28:52Z**

Addendum: in prior note the example path was meant to be 'ls /shelley/conversation/<ID>/c<TAB>' (tab completion). The behavior suggests shell completion does not attempt lookup/opendir on /conversation/<ID> when the ID is absent from parent readdir, so completion within that directory fails even if the ID is fully typed.

**2026-02-10T17:46:59Z**

Debugging: mounted shelley-fuse and created + archived conversation (local ID 3ec0d1e7). Repro: bash tab completion in interactive PTY does not complete when typing ; it emits bell and leaves line unchanged, then ls errors. Using pexpect + strace on the PTY shows bash completion does + on  and then *only* beeps; no statx on candidate paths during completion. When typing  it prints a single completion (), i.e. it lists the directory and completes only when the prefix uniquely matches. So completion is purely from parent directory readdir; if prefix is broad () it expects to list multiple matches; no lookup beyond readdir. This holds for archived and active conversations (same behavior). strace from PTY: openat+getdents64 on conversation dir, then write(a); later ls itself statx's the full path. This confirms that completion doesn't do Lookup on the ID path at all, only readdir, so hiding archived entries from /conversation prevents shell from ever seeing them for completion. (Logs: /tmp/strace-pexpect-archived.log around lines 560-620, 720-760 show getdents64 + bell.)}

**2026-02-10T17:47:06Z**

Debugging: mounted shelley-fuse and created + archived conversation (local ID 3ec0d1e7). Repro via PTY (pexpect): typing ls /tmp/shelley-fuse-mount/conversation/3ec0d1e7/c then TAB emits bell and leaves line unchanged; ls then errors. strace of PTY shows bash completion does openat+getdents64 on /tmp/shelley-fuse-mount/conversation/3ec0d1e7/ and then writes \a (bell). No statx on candidate paths during completion. When typing prefix ct<TAB>, completion outputs ctl (single match), i.e. uses only parent readdir to build matches. So completion never does lookup on the ID path; it relies entirely on readdir results. This matches the regression: hiding archived entries from /conversation means shell completion cannot see them. Same behavior observed for active conversation (not archive-specific). Logs: /tmp/strace-pexpect-archived.log around lines 560-620 and 720-760 show getdents64 + bell.
