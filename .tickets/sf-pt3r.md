---
id: sf-pt3r
status: open
deps: []
links: []
created: 2026-02-11T14:35:17Z
type: task
priority: 3
assignee: hdp
---
# Add phase tracking to FUSE diagnostic tracker

The diag.Tracker shows which FUSE methods are in-flight and how long they've
been running, but not WHERE inside the method they're stuck (e.g., "HTTP POST
to /api/conversations/new" vs. "acquiring lock" vs. "walking directory").

Add a `Phase` field to `diag.Op` and change `Track()` to return an `OpHandle`
with `SetPhase()` and `Done()` methods so callers can annotate sub-steps.
The Dump output would change from:

    [11] ConvSendFileHandle.Flush 1b9b6d6a (29.905s)

to:

    [11] ConvSendFileHandle.Flush 1b9b6d6a [HTTP POST StartConversation] (29.905s)

