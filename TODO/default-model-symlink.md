# Feature: /models/default symlink

Add `/models/default` as a symlink pointing to Shelley's default model.

The default model is available in `window.__SHELLEY_INIT__` as `default_model`:
```json
{"default_model":"claude-opus-4.5", ...}
```

This allows users to:

```bash
# See what the default model is
readlink /mnt/shelley/models/default
# -> claude-opus-4.5

# Check if default model is ready
cat /mnt/shelley/models/default/ready
# -> true
```

## Implementation Notes

1. **Extend shelley/client.go**:
   - Modify `ListModels()` to also return the default model ID
   - Either change return type to `([]Model, string, error)` or add a `DefaultModel` field to a result struct
   - Parse `default_model` from the `__SHELLEY_INIT__` JSON

2. **Extend fuse/filesystem.go**:
   - Modify `ModelsNode.Lookup()` to handle "default" as a symlink
   - Add "default" entry in `ModelsNode.Readdir()` with `Mode: syscall.S_IFLNK`
   - Create symlink node that returns the default model ID from `Readlink()`
   - Cache or re-fetch default model as needed
