---
id: sf-uqkj
status: in_progress
deps: []
links: []
created: 2026-02-09T02:09:02Z
type: feature
priority: 2
assignee: hdp
---
# Add /models/<model>/new/clone for model-preconfigured conversations

Create a new read endpoint at /models/<model>/new/clone that returns a local conversation ID with the specified model preconfigured. This should behave exactly as if a new conversation had been cloned from /new/clone and then 'model=<model>' had been written to its ctl file.

## Acceptance Criteria

1. Reading /models/{model-id}/new/clone returns a local 8-character hex conversation ID\n2. The returned conversation has the specified model preconfigured\n3. Reading the conversation's ctl file shows the model is already set\n4. Writing to send creates the conversation on the backend with the correct model

