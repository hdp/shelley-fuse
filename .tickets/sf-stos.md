---
id: sf-stos
status: closed
deps: []
links: []
created: 2026-02-11T02:28:54Z
type: feature
priority: 2
assignee: hdp
---
# Use GET /api/models instead of HTML scraping

ListModels() currently scrapes the HTML page and parses window.__SHELLEY_INIT__ with a regex. The server now has GET /api/models returning JSON with id, display_name, source, ready, max_context_tokens. Replace HTML scraping with this endpoint. default_model is only in the HTML init data so keep that as a separate lazy fetch only for the model/default symlink. Also update Model struct to include Source and MaxContextTokens fields. Drop the hardcoded fallback model list.

