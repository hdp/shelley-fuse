---
id: sf-n8dm
status: closed
deps: []
links: []
created: 2026-02-16T02:29:08Z
type: bug
priority: 1
assignee: hdp
---
# Ticket: Fix start script mountpoint resolution with symlinks

**ID**: sf-n8dm
**Status**: todo

## Problem

The generated `/model/{model}/new/start` shell scripts use relative path resolution to find the mountpoint:

```sh
DIR="$(cd "$(dirname "$0")" && pwd)"
MOUNT="$(cd "$DIR/../../.." && pwd)"
```

When the script is accessed via a symlink to the model directory, `$0` resolves to the symlink path, and `dirname "$0"` gives the symlink's directory instead of the actual script's location. This causes the relative `../../..` navigation to miss the actual mountpoint.

## Acceptance Criteria

- The `start` script calculates the correct mountpoint even when invoked via a symlink
- The fix uses `realpath`, `readlink -f`, or a similar POSIX-compliant approach to resolve the actual script location
- The solution works across different symlink scenarios:
  - Symlink to model directory: `ln -s /mount/model/claude /local/claude`
  - Symlink to start file: `ln -s /mount/model/claude/new/start /local/start`
- No change to the FUSE filesystem API or behavior
- Tests added to verify symlink scenarios work correctly

## Implementation Notes

The current implementation in `fuse/models.go` uses:

```sh
const modelStartScriptTemplate = `#!/bin/sh
set -e
DIR="$(cd "$(dirname "$0")" && pwd)"
MOUNT="$(cd "$DIR/../../.." && pwd)"
...
```

Options to fix:

1. Use `realpath` (if available): `DIR="$(dirname "$(realpath "$0")")"` and `MOUNT="$(realpath "$DIR/../../..")"`
2. Use shell fallback: First try `realpath`, fall back to `readlink -f`, then current behavior
3. Resolve symlinks manually in shell (more complex but pure POSIX)

Consider compatibility across systems. The `realpath` utility is POSIX but may not be everywhere. `readlink -f` is common on Linux but macOS may differ.

## Related Code

- `fuse/models.go:308-319` — `modelStartScriptTemplate` definition
- `fuse/integration_test.go` — Existing tests for start script
