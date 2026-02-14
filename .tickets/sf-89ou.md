---
id: sf-89ou
status: closed
deps: []
links: []
created: 2026-02-14T14:09:08Z
type: task
priority: 2
assignee: hdp
---
# json objects mapped to directories and files are empty

llm_data and other JSON objects mapped to directories/files are showing as empty directories with no files inside



## Notes

### Initial Research (2025-02-13)

#### Issue Description
JSON objects mapped to directories like `llm_data` and `usage_data` are showing as empty directories with no files inside.

#### Current Design

**jsonfs package** provides FUSE abstraction for JSON objects:
- `objectNode` - JSON objects become directories with keys as entries
- `arrayNode` - JSON arrays become directories with numeric indices
- `valueNode` - JSON primitives become files

The `jsonfs.Config.StringifyFields` controls whether stringified JSON fields should be **recursively unpacked** into nested directory structures.

**In fuse/messages.go**, `llm_data` is handled like this:
```go
config := &jsonfs.Config{StartTime: t, CacheTimeout: cacheTTLImmutable}
node, err := jsonfs.NewNodeFromJSON([]byte(*m.message.LLMData), config)
// ... sets Mode to S_IFDIR
return m.NewInode(ctx, node, fs.StableAttr{Mode: fuse.S_IFDIR, Ino: ino}), 0
```

Note: **No StringifyFields are configured** - the config just has StartTime and CacheTimeout. This means if `llm_data` contains stringified JSON (which it does), it won't be recursively unpacked.

#### Real Data Example
From the Shelley database:
```sql
SELECT llm_data FROM messages WHERE llm_data IS NOT NULL LIMIT 1;
-- Returns: {"Role":0,"Content":[{"ID":"","Type":2,"Text":"You are Shelley..."}]}
```

This is a **valid JSON object** that should be unpacked by `jsonfs.NewNodeFromJSON()`.

#### Test Coverage
- `jsonfs/jsonfs_test.go` has `TestStringifyFields_Unpack` which validates stringify unpacking
- `fuse/messages_test.go` tests that `llm_data/Content/0/Text` is accessible and contains "Hello from LLM"

#### Potential Root Causes

1. **StringifyFields not configured** - If `llm_data` comes in as a string containing JSON (which it does from the DB), the config's `StringifyFields` list would need to include "llm_data" to unpack it. But the config in `fuse/messages.go` doesn't include any stringify fields.

2. **JSON parsing** - `jsonfs.NewNodeFromJSON()` should parse the string as JSON directly. Let me verify this works.

3. **Empty object** - The parsed JSON might be an empty object `{}`?

#### Next Steps

- Check if `llm_data` is being sent as a raw JSON string or as a Go object
- Verify `jsonfs.NewNodeFromJSON()` behavior with the actual data format
- Determine if StringifyFields is actually needed, or if the issue is elsewhere

### Follow-up (2025-02-14)

The root cause: `jsonfs.objectNode` embeds `fs.Inode`, but when we wrap it with `m.NewInode(ctx, node, ...)`, the inner Inode is invalid causing method dispatch failures.

Fix required: Make jsonfs nodes NOT embed `fs.Inode`. They should be plain data structures. Inode embedding only at the FUSE layer in `NewInode` calls.

This is a straightforward refactor but touches multiple files in jsonfs.


**2026-02-14T21:14:45Z**

Root cause identified and fixed: jsonfs objectNode/arrayNode implemented NodeOpendirHandler, which causes go-fuse to skip its internal NodeReaddirer-based dispatch in OpenDir. The OpendirHandle methods returned nil FileHandle, so go-fuse's ReadDir path checked f.file.(FileReaddirenter), got nil, and returned an empty directory listing. Fix: OpendirHandle now calls Readdir() and returns the DirStream (which implements FileReaddirenter via dirArray) as the FileHandle. Added regression tests that exercise os.ReadDir on llm_data, llm_data/Content, and usage_data directories.
