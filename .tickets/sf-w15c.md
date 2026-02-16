---
id: sf-w15c
status: open
deps: [sf-b11n, sf-m13c]
links: [sf-f14r]
created: 2026-02-15T14:45:00Z
type: feature
priority: 2
assignee: hdp
tags: [multi-backend]
---
# Wire BackendNode to ClientManager

Connect BackendNode's model/ and conversation/ lookups to use ClientManager for obtaining the correct ShelleyClient per backend. Add clientMgr field to BackendNode. Model lookup creates ModelsDirNode with the backend's client, conversation lookup creates ConversationListNode with the backend's client. BackendConnectedNode also gets a client reference for connectivity checks.
