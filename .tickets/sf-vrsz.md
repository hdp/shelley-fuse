---
id: sf-vrsz
status: open
deps: []
links: [sf-94sv]
created: 2026-02-04T13:33:57Z
type: bug
priority: 2
---
# model= display names for custom models not supported

The 'model=' configuration parameter in /conversation/{id}/ctl currently requires the internal model ID (e.g., 'custom-f999b9b0') instead of the display name (e.g., 'kimi-2.5-fireworks'). Similarly, custom models don't appear as directories in /models using their display names. Internal custom-XXX IDs should never be exposed to users.

## Design

1. Store and display custom models using only display names\n2. Use display names as directory names in /models\n3. When parsing 'model=' in ctl, always use display name lookup to get internal ID\n4. Internal custom-XXX IDs are an implementation detail - never expose to user

## Acceptance Criteria

1. Can set 'model=kimi-2.5-fireworks' in ctl file\n2. /models/kimi-2.5-fireworks/ exists as a directory with id file containing internal ID (implementation detail)\n3. Custom model directories show up after listing /models\n4. Internal custom-XXX IDs are never exposed in any user-facing output or configuration


## Notes

**2026-02-05T04:55:18Z**

## Implementation Notes (from recovered TODO/custom-model-display-names.md)

### 1. Update shelley/client.go

Extend the Model struct to include DisplayName:

```go
type Model struct {
    ID          string `json:"id"`
    DisplayName string `json:"display_name"`
    Ready       bool   `json:"ready"`
}
```

Update ListModels() to parse display_name from the JSON.

### 2. Update fuse/filesystem.go

**ModelsNode.Readdir()**: Use DisplayName for directory entry names instead of ID.

**ModelsNode.Lookup()**: Accept both DisplayName and ID for lookups:
- Primary: Look up by DisplayName (what users see in ls)
- Fallback: Look up by ID (for backwards compatibility)

**ModelDirNode**: Store both ID and DisplayName. The id file returns the actual ID (for API calls), while the directory name uses DisplayName.

### 3. Consider Symlinks for Aliases

Optionally, create symlinks from ID to DisplayName when they differ:
```
/models/
  kimi-2.5-fireworks/    ← directory (display_name)
  custom-f999b9b0        ← symlink → kimi-2.5-fireworks
```

### 4. Update Conversation Model Symlink

The symlink target should use DisplayName:
```
/conversation/{ID}/model → ../../models/kimi-2.5-fireworks
```

### 5. ctl File Model Resolution

When processing model= in CtlNode.Write():
1. First, check if the value matches a model's DisplayName
2. If found, resolve it to the actual ID for API calls
3. If not found by display name, fall back to treating it as a literal ID
4. Store/display the display name in the ctl file, but use the ID when calling the API

### Edge Cases

- Missing display_name: Fall back to id if display_name is empty or missing
- Duplicate display_names: Unlikely but possible; may need suffix or prefer first
- Fallback model list: The hardcoded fallback list should set DisplayName = ID
