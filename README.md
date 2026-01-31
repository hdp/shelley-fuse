# Shelley FUSE

A FUSE filesystem that exposes the Shelley API as a filesystem, allowing standard shell tools to interact with Shelley conversations.

## Features

The filesystem follows a Plan 9-inspired control file model. Conversations are managed through clone/ctl/new files rather than encoding parameters in paths.

- List models: `cat /models`
- Allocate a new conversation: `cat /new/clone` (returns a local ID)
- Configure before first message: `echo "model=gpt-4 cwd=/home/user/project" > /conversation/{ID}/ctl`
- Send first message (creates conversation on backend): `echo "Fix the bug" > /conversation/{ID}/new`
- Send follow-up messages: `echo "Actually, also fix this" > /conversation/{ID}/new`
- List all conversations (local and server): `ls /conversation`
- Read conversation status: `cat /conversation/{ID}/status.json`
- Get full conversation as JSON: `cat /conversation/{ID}/all.json`
- Get full conversation as Markdown: `cat /conversation/{ID}/all.md`
- Get specific message by sequence number: `cat /conversation/{ID}/7.json`
- Get last N messages: `cat /conversation/{ID}/last/5.json`
- Get messages since Nth-to-last from a person: `cat /conversation/{ID}/since/me/2.json` (or `.md`)
- Get Nth message from a person (from end): `cat /conversation/{ID}/from/shelley/1.json` (or `.md`)

## Usage

```bash
# Build the project
go build -o shelley-fuse ./cmd/shelley-fuse

# Mount the filesystem
./shelley-fuse /mnt/shelley http://localhost:9999

# List available models
cat /mnt/shelley/models

# Allocate a new conversation (returns a local ID like "a1b2c3d4")
ID=$(cat /mnt/shelley/new/clone)

# Configure the conversation before sending the first message
echo "model=predictable cwd=$PWD" > /mnt/shelley/conversation/$ID/ctl

# Check configuration
cat /mnt/shelley/conversation/$ID/ctl

# Send the first message (this creates the conversation on the Shelley backend)
echo "Hello, Shelley!" > /mnt/shelley/conversation/$ID/new

# Check conversation status
cat /mnt/shelley/conversation/$ID/status.json

# Read the full conversation
cat /mnt/shelley/conversation/$ID/all.json
cat /mnt/shelley/conversation/$ID/all.md

# Send a follow-up message
echo "Follow up message" > /mnt/shelley/conversation/$ID/new

# Unmount with Ctrl+C or kill the process
```

## Filesystem Layout

```
/
  models                                → read-only file (GET /, parse HTML for model list)
  new/
    clone                               → read to allocate a new local conversation ID
  conversation/                           → lists local IDs + server conversations (merged)
    {id}/                               → directory per conversation (local ID or server conversation ID)
      ctl                               → read/write config (model=X cwd=Y); read-only after creation
      new                               → write here to send a message; first write creates conversation
      status.json                       → read-only status (local ID, shelley ID, message count, etc.)
      all.json                          → full conversation as JSON
      all.md                            → full conversation as Markdown
      {N}.json                          → specific message by sequence number
      {N}.md                            → specific message as Markdown
      last/{N}.json                     → last N messages as JSON
      last/{N}.md                       → last N messages as Markdown
      since/{person}/{N}.json           → messages since Nth-to-last message from {person}
      since/{person}/{N}.md             → same, as Markdown
      from/{person}/{N}.json            → Nth message from {person} (counting from end)
      from/{person}/{N}.md              → same, as Markdown
```

## API Mapping

| Filesystem Operation | Shelley API Call | Description |
|---------------------|------------------|-------------|
| `cat /models` | GET / (parses HTML) | List available models |
| `cat /new/clone` | (local only) | Allocate a new local conversation ID |
| `echo k=v > /conversation/{id}/ctl` | (local only) | Set model/cwd before first message |
| `echo msg > /conversation/{id}/new` (first) | POST /api/conversations/new | Create conversation and send first message |
| `echo msg > /conversation/{id}/new` (subsequent) | POST /api/conversation/{id}/chat | Send message to existing conversation |
| `cat /conversation/{id}/all.json` | GET /api/conversation/{id} | Get full conversation |
| `cat /conversation/{id}/status.json` | GET /api/conversation/{id} | Get conversation status |
| `ls /conversation` | GET /api/conversations | List all conversations (local + server) |

## Testing

```bash
# Run all tests
just test

# Run integration tests (requires /usr/local/bin/shelley and fusermount)
just test-integration
```

Integration tests start a real Shelley server (`-predictable-only`) on a random free port, mount a FUSE filesystem, and exercise the full clone → ctl → new → read cycle. They skip automatically if `fusermount` or `/usr/local/bin/shelley` is not available.

## Development

```bash
# Build and mount for manual testing (Ctrl+C to unmount)
just dev

# Or with custom mount point and server URL
just dev mount=/tmp/my-mount url=http://localhost:9999
```

## Limitations

- Streaming responses are not yet implemented
- Model listing requires parsing HTML (no dedicated API endpoint)
- Conversation state is stored in `~/.shelley-fuse/state.json`; losing this file loses local-to-shelley ID mappings

## Server Conversation Discovery

The `/conversation` directory automatically merges local conversations with those on the Shelley server:

- **Local conversations** appear with their 8-character hex IDs (e.g., `a1b2c3d4`)
- **Server-only conversations** appear by their full Shelley conversation ID
- **Already-tracked conversations** (local conversations linked to server conversations) appear only by their local ID to avoid duplicates

When you access a server-only conversation (via `ls` or `cat`), it's automatically "adopted" into local state, creating a short local ID mapping. This allows seamless interaction with conversations created outside of FUSE.

If the server is unreachable, `ls /conversation` still shows local conversations—server errors are handled gracefully.
