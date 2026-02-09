---
id: sf-e1jv
status: closed
deps: []
links: []
created: 2026-02-09T16:25:18Z
type: bug
priority: 0
assignee: hdp
---
# All messages directories are completely empty

All message directories are showing as completely empty. The readme tests should have caught this bug. This suggests either a regression in the FUSE node implementation or the tests are not properly validating the message directory functionality.

## Acceptance Criteria

1. Message directories contain message files\n2. Readme tests pass and validate message directory contents

