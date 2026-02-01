# Feature: Use display names for custom models in /models/

Shelley now supports custom models with a `display_name` field that differs from the internal `id`. Custom models have IDs like `custom-f999b9b0` but human-readable display names like `kimi-2.5-fireworks`.

## Current Behavior

The `/models/` directory uses model IDs as directory names:

```
/models/
  claude-opus-4.5/
  claude-sonnet-4.5/
  custom-f999b9b0/      ← not useful
```

## Desired Behavior

Use `display_name` for the directory name, with the `id` file inside containing the actual ID:

```
/models/
  claude-opus-4.5/
    id                   → "claude-opus-4.5"
    ready                → "true"
  kimi-2.5-fireworks/    ← uses display_name
    id                   → "custom-f999b9b0"  ← actual ID for API calls
    ready                → "true"
```

## API Data Structure

The `window.__SHELLEY_INIT__` models array now includes:

```json
{
  "id": "custom-f999b9b0",
  "display_name": "kimi-2.5-fireworks",
  "source": "custom",
  "ready": true,
  "max_context_tokens": 128000
}
```

For built-in models, `display_name` equals `id`:

```json
{
  "id": "claude-opus-4.5",
  "display_name": "claude-opus-4.5",
  "source": "exe.dev gateway",
  "ready": true,
  "max_context_tokens": 200000
}
```

## Implementation Notes

### 1. Update `shelley/client.go`

Extend the `Model` struct to include `DisplayName`:

```go
type Model struct {
    ID          string `json:"id"`
    DisplayName string `json:"display_name"`
    Ready       bool   `json:"ready"`
}
```

Update `ListModels()` to parse `display_name` from the JSON:

```go
model := Model{
    ID:          getString(modelMap, "id"),
    DisplayName: getString(modelMap, "display_name"),
    Ready:       getBool(modelMap, "ready"),
}
```

### 2. Update `fuse/filesystem.go`

**ModelsNode.Readdir()**: Use `DisplayName` for directory entry names instead of `ID`.

**ModelsNode.Lookup()**: Accept both `DisplayName` and `ID` for lookups:
- Primary: Look up by `DisplayName` (what users see in `ls`)
- Fallback: Look up by `ID` (for backwards compatibility or direct API ID access)

**ModelDirNode**: Store both `ID` and `DisplayName`. The `id` file should return the actual `ID` (needed for API calls), while the directory name uses `DisplayName`.

### 3. Consider Symlinks for Aliases

Optionally, create symlinks from `ID` to `DisplayName` when they differ:

```
/models/
  kimi-2.5-fireworks/    ← directory (display_name)
  custom-f999b9b0        ← symlink → kimi-2.5-fireworks
```

This allows both `cat /models/kimi-2.5-fireworks/ready` and `cat /models/custom-f999b9b0/ready` to work.

### 4. Update Conversation Model Symlink

If `conversation-model-symlink.md` is implemented, the symlink target should use `DisplayName`:

```
/conversation/{ID}/model → ../../models/kimi-2.5-fireworks
```

Not:
```
/conversation/{ID}/model → ../../models/custom-f999b9b0
```

### 5. Update ctl File Model Resolution

The `ctl` file accepts `model=` configuration. Currently users write:

```bash
echo "model=claude-opus-4.5" > /conversation/$ID/ctl
```

With custom models, users should be able to write the display name:

```bash
echo "model=kimi-2.5-fireworks" > /conversation/$ID/ctl
```

Rather than the opaque ID:

```bash
echo "model=custom-f999b9b0" > /conversation/$ID/ctl  # awkward
```

**Implementation**: When processing `model=` in `CtlNode.Write()`:
1. First, check if the value matches a model's `DisplayName`
2. If found, resolve it to the actual `ID` for API calls
3. If not found by display name, fall back to treating it as a literal ID
4. Store/display the display name in the ctl file, but use the ID when calling the API

This requires `CtlNode` to have access to the model list (or a lookup function) to resolve display names to IDs.

## Edge Cases

- **Missing display_name**: Fall back to `id` if `display_name` is empty or missing
- **Duplicate display_names**: Unlikely but possible; may need to append suffix or prefer first
- **Fallback model list**: The hardcoded fallback list in `ListModels()` should set `DisplayName = ID`

## Testing

Integration tests should verify:
- Custom models appear with display names in `ls /models/`
- `cat /models/{display_name}/id` returns the actual custom ID
- Model lookup works by both display name and ID (if symlinks added)
- Conversation model symlinks point to display name paths

## Dependencies

This interacts with:
- `conversation-model-symlink.md`: Model symlinks should use display names
- `default-model-symlink.md`: Default model symlink should use display name
