---
id: sf-14gm
status: closed
deps: []
links: []
created: 2026-02-04T01:47:31Z
type: task
priority: 2
---
# using shell to touch conversation/{ID}/archived results in error

touch: setting times of '/shelley/conversation/.../archived': Operation not supported. it should allow and ignore setting the times. if it's possible to get the archived time from the shelley API, return that as the ctime/mtime of the file, otherwise don't worry about it


## Notes

**2026-02-04T02:21:41Z**

Implemented Setattr for ArchivedNode to accept touch (time setting) operations. The implementation simply delegates to Getattr, accepting the request silently. Added integration test TestArchivedFileTouchSetattr. The Shelley API doesn't expose an ArchivedAt timestamp, so we use the conversation's CreatedAt as the file timestamp.

**2026-02-04T02:24:22Z**

Reopened to add enhancement: use conversation.updated_at as the ctime/mtime for /archived file when conversation.archived = true

**2026-02-04T02:28:02Z**

Enhancement implemented: ArchivedNode.Getattr now uses conversation.UpdatedAt as the ctime/mtime for the /archived file. The timestamp is fetched from the API when displaying the file attributes. Added TestArchivedFileTimestamp integration test to verify the behavior.
