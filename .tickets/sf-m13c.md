---
id: sf-m13c
status: open
deps: [sf-3gq8]
links: [sf-w15c, sf-f14r]
created: 2026-02-15T14:45:00Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# Multi-client manager

Create a ClientManager that holds multiple ShelleyClient instances, one per backend. Lazily creates clients on first access, detects URL changes and recreates clients when invalidated. Provides GetClient(backendName), InvalidateClient(backendName), and GetDefaultClient() methods. Wraps base clients with CachingClient when cacheTTL > 0.
