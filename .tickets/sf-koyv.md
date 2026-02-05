---
id: sf-koyv
status: closed
deps: [sf-icyd]
links: []
created: 2026-02-04T02:09:14Z
type: feature
priority: 2
---
# Remove separate NNN-{who}.md files, use content.md inside message directories

Currently we have both:
- messages/001-user.json 
- messages/001-user.md

After sf-icyd, the .json files become directories. This ticket removes the separate .md files and adds content.md inside each message directory instead.

Before: `messages/001-user.md`
After: `messages/001-user/content.md`

Depends on sf-icyd (message directories must exist first).


## Notes

**2026-02-04T03:10:25Z**

Investigation complete: sf-icyd already fully implemented this. The code no longer creates/serves separate NNN-{who}.md files. Messages are directories with content.md inside. All tests pass. No changes needed - ticket can be closed as already implemented.
