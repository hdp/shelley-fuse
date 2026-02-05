---
id: sf-icyd
status: closed
deps: [sf-37oa]
links: []
created: 2026-02-04T02:09:06Z
type: feature
priority: 2
parent: sf-37oa
---
# Convert message JSON files to directories with field files

Change message files from single .json files to directories containing one file per field.

Current: `messages/001-user.json` (single JSON file)

New structure:
```
messages/001-user/
  message_id        -> "5cf46c3d-fa32-..."
  conversation_id   -> "c6564Q3"
  sequence_id       -> "1"
  type              -> "user"
  created_at        -> "2026-02-03T13:59:27Z"
  llm_data/         -> unpacked JSON (see sf-37oa)
    Content/
      0/
        Type        -> "2"
        Text        -> "Hello world"
  usage_data/       -> unpacked JSON
    input_tokens    -> "5311"
    ...
  content.md        -> markdown rendering of this message (replaces NNN-{who}.md)
```

Depends on sf-37oa for the JSON-to-filesystem abstraction.


## Notes

**2026-02-04T02:12:50Z**

Note: The message directory format applies everywhere messages appear. See sf-sdsq for converting last/N and since/{who}/N to use symlinks to these message directories.

**2026-02-04T02:59:31Z**

Implementation complete. Changes:

1. Created MessageDirNode struct to represent individual message directories
2. Created MessageFieldNode for simple field files (message_id, type, etc.)
3. Modified MessagesDirNode.Lookup to return directories instead of files
4. Modified MessagesDirNode.Readdir to list directories instead of .json/.md files
5. Uses jsonfs package for unpacking llm_data and usage_data into nested directories
6. Added content.md file in each message directory for markdown rendering
7. Updated README documentation
8. Updated all unit and integration tests

Message directory structure:
- messages/001-user/
  - message_id, conversation_id, sequence_id, type, created_at (field files)
  - llm_data/ (jsonfs directory if present)
  - usage_data/ (jsonfs directory if present)
  - content.md (markdown rendering)

All tests passing: unit tests and integration tests.
