---
id: sf-tyhd
status: closed
deps: [sf-koyv, sf-sdsq, sf-yior]
links: []
created: 2026-02-04T02:09:21Z
type: chore
priority: 2
---
# Update documentation for message directory structure

After sf-icyd and sf-koyv, the messages/ structure has changed significantly. Update all documentation to reflect:

- messages/NNN-{who}/ are now directories, not files
- Individual fields are accessible as files within the directory
- llm_data/ and usage_data/ subdirectories contain unpacked JSON
- content.md replaces the old NNN-{who}.md files
- Update CLAUDE.md filesystem hierarchy section
- Update any other docs or comments referencing the old structure


## Notes

**2026-02-04T06:41:17Z**

Implementation complete:

Updated documentation in three files:

1. CLAUDE.md - Filesystem hierarchy section:
   - Changed {NNN}-{slug}.json/.md files to {NNN}-{slug}/ directories
   - Added field files inside message directories: message_id, conversation_id, sequence_id, type, created_at, content.md
   - Added llm_data/ and usage_data/ subdirectories
   - Changed last/{N}.json/.md to last/{N}/ directories with symlinks
   - Changed since/{slug}/{N}.json/.md to since/{slug}/{N}/ directories with symlinks
   - Added meta/ directory documentation (from sf-yior)

2. fuse/filesystem.go - Inline README:
   - Added meta/ directory section with its fields

3. TODO/messages-directory.md:
   - Marked as IMPLEMENTED
   - Updated to show actual implemented structure (directories, not files)
   - Added key design decisions explaining the implementation choices
   - Added related tickets section

Also renamed messageFileBase to messageDirBase in fuse/filesystem.go to reflect that messages are now directories.

**2026-02-04T06:48:48Z**

Note: The function rename (messageFileBase → messageDirBase) was done incorrectly and broke the build. This was out of scope for sf-tyhd which is documentation-only. The rename moved to ticket sf-adbt as a separate cleanup task.
