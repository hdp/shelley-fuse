---
id: sf-37oa
status: closed
deps: []
links: []
created: 2026-02-04T02:08:59Z
type: feature
priority: 2
---
# JSON-to-filesystem abstraction for exposing JSON blobs as directory trees

Create a reusable abstraction that can expose any JSON blob as a tree of directories and files:

- JSON objects become directories
- JSON primitive values (string, number, boolean, null) become files containing the value
- JSON arrays become directories with numeric index names (0, 1, 2, ...)
- Stringified JSON fields (like llm_data, usage_data) should be recursively unpacked

Example: Given JSON `{"name": "foo", "count": 42, "nested": {"x": 1}}`
Produces:
```
name     -> "foo"
count    -> "42"
nested/
  x      -> "1"
```

This abstraction will be used for message files and potentially other JSON data in the filesystem.


## Notes

**2026-02-04T02:44:23Z**

Design decision: Creating a new  package with the following components:
1. Config struct for specifying stringify fields and start time
2. NewNode() function that takes any JSON value and returns appropriate fs.InodeEmbedder
3. Three node types: objectNode (directory), arrayNode (directory), valueNode (file)
4. Automatic detection of stringified JSON fields for recursive unpacking
5. Using go-fuse/v2 interfaces matching existing codebase patterns

**2026-02-04T02:48:38Z**

Implementation complete. Created jsonfs package with:
- objectNode: exposes JSON objects as directories
- arrayNode: exposes JSON arrays as directories with numeric indices (0, 1, 2...)
- valueNode: exposes primitives as read-only files
- Config.StringifyFields: list of field names to recursively unpack from stringified JSON
- All 18 tests passing, full project compiles

**2026-02-04T02:49:51Z**

Implemented jsonfs package with 258 lines of code and 18 passing tests. Provides abstraction for exposing JSON as directories/files. Review approved with note about edge cases (JSON key validation not needed for shelley-fuse use case).
