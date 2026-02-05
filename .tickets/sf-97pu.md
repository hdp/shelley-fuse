---
id: sf-97pu
status: closed
deps: []
links: []
created: 2026-02-04T02:31:53Z
type: task
priority: 2
tags: [fuse, metadata, filesystem, api]
---

# metadata: map JSON fields to directory stat attributes

Create a reusable pattern for mapping JSON timestamp and metadata fields to directory stat attributes (ctime/mtime visible via `ls -l`, `stat`) when representing objects as directories. For example, the 'created_at' JSON field should populate the ctime/mtime of the directory representing the object (conversation or message). Other useful mappings may include 'updated_at' for mtime, 'modified_at', etc. This makes the implicit pattern in several existing tickets explicit and codified as a general mechanism.

**Note:** This ticket is distinct from but complementary to sf-37oa/sf-icyd, which handle directory CONTENTS (mapping JSON fields to files inside directories). This ticket addresses metadata/attributes, not file contents.

## Design

Define a metadata mapping system that can be applied uniformly across different node types. The mapping should apply to any node that represents a JSON object as a directory:

1. Conversation nodes: created_at → directory ctime/mtime, updated_at → mtime
2. Message nodes: created_at → directory ctime/mtime (if available)

The mapping should be configurable and declarative, using field names as keys and filesystem attributes as values. This allows easy extension as new metadata fields become available from the API.

Implementation approach:
- Create a metadata package with a generic mapping function
- Use field name → attribute mappings (e.g., created_at → {ctime: true, mtime: true})
- Apply in Setattr() or when computing node attributes
- Consider caching for performance but ensure timely invalidation

## Notes

**2026-02-04T03:44:03Z**

Implemented metadata mapping system for JSON fields to filesystem stat attributes.

Key components:
1. New metadata package with:
   - Mapping type: declarative field → attribute mappings
   - Timestamps struct: holds ctime/mtime/atime values
   - ConversationMapping: created_at → ctime/mtime, updated_at → mtime
   - MessageMapping: created_at → ctime/mtime

2. State package changes:
   - Added APICreatedAt and APIUpdatedAt fields to ConversationState
   - New AdoptWithMetadata() to capture API timestamps during adoption

3. Fuse package changes:
   - ConversationNode.Getattr uses metadata mapping (mtime=updated_at, ctime=created_at)
   - MessagesDirNode.Getattr uses conversation metadata
   - MessageDirNode.Getattr uses message's created_at

4. Test coverage:
   - metadata package unit tests
   - Integration tests for API metadata mapping
   - Tests for ctime/mtime separation
   - Tests for fallback behavior
