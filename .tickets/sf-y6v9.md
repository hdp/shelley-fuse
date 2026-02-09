---
id: sf-y6v9
status: open
deps: []
links: []
created: 2026-02-09T04:25:20Z
type: task
priority: 2
assignee: hdp
---
# Unify the various mocks of the Shelley backend, particularly the __SHELLEY_INIT__ scraping

There are currently multiple mock implementations of the Shelley backend scattered throughout the codebase. These mocks handle the initial page scraping (detecting the __SHELLEY_INIT__ injection) in different ways, leading to inconsistency and maintenance burden. This ticket aims to consolidate these mocks into a single, consistent implementation.

## Acceptance Criteria

1. Identify all mock implementations of Shelley backend in the codebase\n2. Create a unified mock package that handles __SHELLEY_INIT__ scraping consistently\n3. Replace all existing mock usages with the unified implementation\n4. Ensure all tests pass after consolidation

