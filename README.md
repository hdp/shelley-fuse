# Shelley FUSE

A FUSE filesystem that exposes the Shelley API as a filesystem, allowing standard shell tools to interact with Shelley conversations.

## Features

The filesystem follows a Plan 9-inspired control file model. Conversations are managed through clone/ctl/new files rather than encoding parameters in paths.

- List models: `ls /models`
- Check model availability: `cat /models/claude-opus-4/ready`
- Allocate a new conversation: `cat /new/clone` (returns a local ID)
- Configure before first message: `echo "model=gpt-4 cwd=/home/user/project" > /conversation/{ID}/ctl`
- Send first message (creates conversation on backend): `echo "Fix the bug" > /conversation/{ID}/new`
- Send follow-up messages: `echo "Actually, also fix this" > /conversation/{ID}/new`
- List all conversations (local and server): `ls /conversation`
- Read conversation status: `cat /conversation/{ID}/status/local_id` or other fields in `status/` directory
- Get full conversation as JSON: `cat /conversation/{ID}/all.json`
- Get full conversation as Markdown: `cat /conversation/{ID}/all.md`
- Get specific message by sequence number: `cat /conversation/{ID}/7.json`
- Get last N messages: `cat /conversation/{ID}/last/5.json`
- Get messages since Nth-to-last from a person: `cat /conversation/{ID}/since/me/2.json` (or `.md`)
- Get Shelley conversation ID: `cat /conversation/{ID}/id`
- Get conversation slug: `cat /conversation/{ID}/slug`
- Get Nth message from a person (from end): `cat /conversation/{ID}/from/shelley/1.json` (or `.md`)

## Usage

```bash
# Build the project
go build -o shelley-fuse ./cmd/shelley-fuse

# Mount the filesystem
./shelley-fuse /mnt/shelley http://localhost:9999

# List available models
ls /mnt/shelley/models

# Check if a model is ready
cat /mnt/shelley/models/predictable/ready

# Allocate a new conversation (returns a local ID like "a1b2c3d4")
ID=$(cat /mnt/shelley/new/clone)

# Configure the conversation before sending the first message
echo "model=predictable cwd=$PWD" > /mnt/shelley/conversation/$ID/ctl

# Check configuration
cat /mnt/shelley/conversation/$ID/ctl

# Send the first message (this creates the conversation on the Shelley backend)
echo "Hello, Shelley!" > /mnt/shelley/conversation/$ID/new

# Read individual status fields (no JSON parsing needed)
cat /mnt/shelley/conversation/$ID/status/local_id
cat /mnt/shelley/conversation/$ID/status/message_count

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
  models/                               → directory of available models (GET /, parse HTML for model list)
    {model-id}/                         → directory for each model
      id                                → read-only file: model ID
      ready                             → read-only file: "true" or "false"
  new/
    clone                               → read to allocate a new local conversation ID
  conversation/                           → lists local IDs + server conversations (merged)
    {id}/                               → directory per conversation (local ID or server conversation ID)
      ctl                               → read/write config (model=X cwd=Y); read-only after creation
      new                               → write here to send a message; first write creates conversation
      status/                            → directory with individual status fields as plain-text files
        local_id                         → local conversation ID
        shelley_id                       → Shelley server conversation ID
        slug, model, cwd                 → conversation configuration
        created                          → "true" or "false"
        created_at                       → RFC3339 timestamp
        message_count                    → number of messages (0 if not created)
      all.json                          → full conversation as JSON
      all.md                            → full conversation as Markdown
      {N}.json                          → specific message by sequence number (virtual, not in listings)
      {N}.md                            → specific message as Markdown (virtual, not in listings)
      {N}.json                          → specific message by sequence number
      {N}.md                            → specific message as Markdown
      last/{N}.json                     → last N messages as JSON
      last/{N}.md                       → last N messages as Markdown
      since/{person}/{N}.json           → messages since Nth-to-last message from {person}
      since/{person}/{N}.md             → same, as Markdown
      from/{person}/{N}.json            → Nth message from {person} (counting from end)
      from/{person}/{N}.md              → same, as Markdown
    {server-id}                         → symlink to local-id: allows access via Shelley server ID
    {slug}                              → symlink to local-id: allows access via conversation slug
```

## API Mapping

| Filesystem Operation | Shelley API Call | Description |
|---------------------|------------------|-------------|
| `ls /models` | GET / (parses HTML) | List available models |
| `cat /models/{id}/ready` | GET / (parses HTML) | Check if model is ready |
| `cat /new/clone` | (local only) | Allocate a new local conversation ID |
| `echo k=v > /conversation/{id}/ctl` | (local only) | Set model/cwd before first message |
| `echo msg > /conversation/{id}/new` (first) | POST /api/conversations/new | Create conversation and send first message |
| `echo msg > /conversation/{id}/new` (subsequent) | POST /api/conversation/{id}/chat | Send message to existing conversation |
| `cat /conversation/{id}/all.json` | GET /api/conversation/{id} | Get full conversation |
| `cat /conversation/{id}/status/local_id` | N/A (local state) | Get individual status field as plain text |
| `ls /conversation` | GET /api/conversations | List all conversations (local + server) |
| `cat /conversation/{id}/id` | (local state) | Get Shelley conversation ID |
| `cat /conversation/{id}/slug` | (local state) | Get conversation slug |

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

The `/conversation` directory automatically discovers and adopts conversations from the Shelley server:

- **All conversations** appear with 8-character hex local IDs (e.g., `a1b2c3d4`)
- **Server conversations** are automatically adopted when you run `ls /conversation`, creating local ID mappings
- **No server IDs in listings** — users always see consistent local IDs

This means conversations created outside of FUSE (e.g., via the web UI or API) are seamlessly integrated into the filesystem with local IDs. You can also access a conversation by its Shelley server ID directly (e.g., `cat /conversation/{server-id}/all.json`), which will adopt it if not already tracked.

The `/conversation` directory provides three ways to access conversations:

- **Local IDs (directories)**: The primary 8-character hex identifiers (e.g., `a1b2c3d4/`)
- **Server IDs (symlinks)**: Point to the local ID directory, allowing access via Shelley backend IDs
- **Slugs (symlinks)**: Point to the local ID directory, allowing access via human-readable slugs

When you run `ls /conversation`, you'll see:
- Directories for each local conversation ID
- Symlinks for server IDs (pointing to their local ID)
- Symlinks for slugs (pointing to their local ID)

All three paths access the same conversation:
```bash
cat /conversation/a1b2c3d4/all.json      # via local ID (directory)
cat /conversation/{server-id}/all.json   # via server ID (symlink)
cat /conversation/{slug}/all.json        # via slug (symlink)
```

This means conversations created outside of FUSE (e.g., via the web UI or API) are seamlessly integrated into the filesystem with local IDs. You can also access a conversation by its Shelley server ID directly (e.g., `cat /conversation/{server-id}/all.json`), which will adopt it if not already tracked.

If the server is unreachable, `ls /conversation` still shows previously-adopted local conversations—server errors are handled gracefully.
