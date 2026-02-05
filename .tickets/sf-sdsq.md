---
id: sf-sdsq
status: closed
deps: [sf-icyd]
links: []
created: 2026-02-04T02:12:44Z
type: feature
priority: 2
---
# Convert last/N and since/{who}/N to directories with symlinks

After messages become directories (sf-icyd), last/N and since/{who}/N should also become directories containing symlinks to the actual message directories.

Current:
```
last/5.json    -> concatenated JSON of last 5 messages
last/5.md      -> concatenated markdown of last 5 messages
since/user/1.json
since/user/1.md
```

New:
```
last/5/
  001-user     -> symlink to ../../001-user
  002-bash-tool -> symlink to ../../002-bash-tool
  ...
since/user/1/
  023-user     -> symlink to ../../023-user
  024-bash-tool -> symlink to ../../024-bash-tool
  ...
```

This changes the access pattern:
- Old: `cat since/user/1.md`
- New: `cat since/user/1/*/content.md` (cats files in order)

Benefits:
- Consistent structure - everything is symlinks to message directories
- Access to all message fields, not just combined JSON/markdown
- Reuses the message directory structure from sf-icyd


## Notes

**2026-02-04T04:29:13Z**

Implementation complete. Changes:

1. Created QueryResultDirNode struct to represent last/{N}/ and since/{person}/{N}/ directories
2. Modified QueryDirNode.Lookup to return directories instead of files for numeric lookups
3. QueryResultDirNode contains symlinks to actual message directories:
   - last/{N}/ symlinks use '../../' prefix (2 levels up)
   - since/{person}/{N}/ symlinks use '../../../' prefix (3 levels up)
4. Updated README documentation in filesystem.go
5. Added unit tests: TestQueryResultDirNode_LastN and TestQueryResultDirNode_SincePersonN
6. Updated integration tests to use new directory structure

Access pattern changes:
- Old: cat last/5.md (concatenated content)
- New: ls last/5/ (list symlinks) + cat last/5/*/content.md (read through symlinks)

All tests passing.
