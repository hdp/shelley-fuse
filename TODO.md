[ ] Change /models to a directory exposing the model names as directories and json fields as files into them, e.g. `cat /models/claude-opus-4.5/ready` -> `"true"`

Plan:
- Replace `ModelsNode` (a single read-only file) with `ModelsDirNode` (a directory) in `fuse/filesystem.go`
- `ModelsDirNode.Readdir` calls `client.ListModels()`, parses the JSON `[]Model` array, and returns a `DirEntry` per model ID
- `ModelsDirNode.Lookup(name)` calls `client.ListModels()`, finds the matching model, and returns a `ModelNode` directory
- `ModelNode` is a directory for a single model. Its `Readdir` returns `{Name: "id", Mode: S_IFREG}, {Name: "ready", Mode: S_IFREG}`
- `ModelNode.Lookup` returns a `ModelFieldNode` for "id" or "ready"
- `ModelFieldNode.Read` returns the field value as a string with newline (e.g. `"true"\n` or `"claude-opus-4.5"\n`)
- All nodes use `FOPEN_DIRECT_IO` since model availability can change
- Update `FS.Lookup("models")` to return `ModelsDirNode` instead of `ModelsNode`; update `FS.Readdir` to mark "models" as `S_IFDIR` instead of `S_IFREG`
- Remove the old `ModelsNode` type
- Update `shelley.ListModels()` to return `([]Model, error)` instead of `([]byte, error)` — currently it marshals to JSON only for the caller to potentially need to unmarshal. Returning the struct directly is cleaner. (Alternatively, add a `ListModelsTyped` method and keep the raw one for backwards compat, but there are only two callers.)
- Update integration test `ReadModels` subtest: change from reading a file to `ls /models` and `cat /models/predictable/ready`
- Update README.md and CLAUDE.md filesystem layout sections

[ ] Allow `ls /conversation` to return the list of conversations from the status.json

Plan:
- `ConversationListNode.Readdir` already returns local IDs from `state.List()` — this works for `ls /conversation`
- The TODO likely means enriching the listing or making it work more like a proper directory listing. Currently it only shows local 8-char hex IDs.
- No code change needed if the intent is just `ls /conversation` showing known conversation directories — this already works.
- If the intent is to also show conversations from the server that aren't tracked locally: add a `client.ListConversations()` call (already exists in `shelley/client.go:238`) in `Readdir`, parse the response to get conversation IDs, and merge with local state. This would require parsing the API response format (need to check what `/api/conversations` returns).
- Likely approach: in `ConversationListNode.Readdir`, call `client.ListConversations()`, parse the JSON response to extract conversation IDs/slugs, merge with local state IDs, return combined list. Also update `Lookup` to handle server-only conversations (ones not cloned locally) and assign them local state IDs so they're also available for interaction.
- This depends on the `/api/conversations` response format — need to verify what fields are returned. The `Conversation` struct has `ConversationID` and `Slug`.

[ ] Include `/conversation/{ID}/slug` and `/conversation/{ID}/id` reflecting the Shelley slug and id for the conversation

Plan:
- Add two new cases to `ConversationNode.Lookup`: "slug" and "id"
- Both return a new `ConvMetaFieldNode` (read-only file, `FOPEN_DIRECT_IO`)
- `ConvMetaFieldNode` holds `localID`, `client`, `state`, and a `field` string ("slug" or "id")
- On `Read`: get `ConversationState` from store. If not created, return ENOENT for both.
  - For "id": return `cs.ShelleyConversationID + "\n"`
  - For "slug": need to fetch from API. Call `client.GetConversation(cs.ShelleyConversationID)`, parse the response to extract the `slug` field. The current `GetConversation` returns raw bytes — parse as JSON, extract `"slug"` from the conversation metadata (not the messages array). Need to check the actual API response shape: it may be `{conversation: {...}, messages: [...]}` or just the conversation object. May need a new client method like `GetConversationMeta` or parse the existing response.
- Add "slug" and "id" to `ConversationNode.Readdir` entries
- Alternatively, store the slug in `ConversationState` when we get it back from `StartConversation` (currently we only save `conversation_id`). This avoids an extra API call on every read. Would need to update `StartConversation` to return the slug too, and `MarkCreated` to accept it.
- Update integration tests to verify `cat /conversation/$ID/id` returns the shelley conversation ID and `cat /conversation/$ID/slug` returns the slug
- Update README.md and CLAUDE.md filesystem layout sections
