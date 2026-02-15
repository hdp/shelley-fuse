---
id: sf-lsqv
status: closed
deps: []
links: []
created: 2026-02-14T21:16:10Z
type: task
priority: 2
assignee: hdp
---
# All tests should be shell tests instead of direct reads

The main use case for this FUSE fs is typical shell tools like cat, ls, echo, grep, and so on. Many of the tests use direct reads from Go, which then don't necessarily verify everything we think they do, and several bugs have snuck through as a result. There are some shell tests, but they don't cover everything. We should standardize on shell-based tests, which have the additional advantage of being less likely to hang the test due to deadlocks with fuse since they run in external processes, and consolidate away any duplicated coverage.

