# Feature: model symlink in conversation directories

Add `/conversation/{ID}/model` as a symlink pointing to `../../models/{MODEL}`.

This allows users to:

```bash
# See which model a conversation uses
readlink /mnt/shelley/conversation/$ID/model
# -> ../../models/claude-opus-4.5

# Check if the model is ready
cat /mnt/shelley/conversation/$ID/model/ready
# -> true

# Get the model ID
cat /mnt/shelley/conversation/$ID/model/id
# -> claude-opus-4.5
```

## Implementation Notes

- Add symlink node in `ConversationDirNode.Lookup()` for "model"
- Add entry in `ConversationDirNode.Readdir()` for "model" 
- Use `StableAttr{Mode: syscall.S_IFLNK}` for the symlink node
- Implement `Readlink()` method returning `../../models/{model-id}`
- Handle the case where model is not set (return ENOENT?)
- The relative path `../../models/{MODEL}` works because conversation dirs are at `/conversation/{ID}/`
