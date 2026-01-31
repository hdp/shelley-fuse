# Shelley FUSE

A FUSE filesystem that exposes the Shelley API as a filesystem, allowing standard shell tools to interact with Shelley conversations.

## Features

The filesystem follows a Plan 9-inspired control file model. Conversations are managed through clone/ctl/new files rather than encoding parameters in paths.

- List models: `cat /models`
- Allocate a new conversation: `cat /new/clone` (returns a local ID)
- Configure before first message: `echo "model=gpt-4 cwd=/home/user/project" > /conversation/{ID}/ctl`
- Send first message (creates conversation on backend): `echo "Fix the bug" > /conversation/{ID}/new`
- Send follow-up messages: `echo "Actually, also fix this" > /conversation/{ID}/new`
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
  conversation/
    {id}/                               → directory per conversation
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

## Testing

Run unit tests with:

```bash
go test ./...
```

Run integration tests with a real Shelley server:

```bash
go test -v ./shelley -run TestIntegration
```

The integration tests use the real `/usr/local/bin/shelley` binary with the `predictable` model for testing.

### In-Process FUSE Server Testing

The `testutil` package provides in-process FUSE server testing. Tests using this skip automatically if `fusermount` is not available.

## Systemd Service

Create a systemd service file at `/etc/systemd/system/shelley-fuse@.service`:

```ini
[Unit]
Description=Shelley FUSE filesystem for %I
After=network.target

[Service]
Type=forking
User=%i
ExecStart=/usr/local/bin/shelley-fuse /home/%i/shelley http://localhost:9999
ExecStop=/bin/fusermount -u /home/%i/shelley
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

Then enable and start the service:

```bash
sudo systemctl enable shelley-fuse@username.service
sudo systemctl start shelley-fuse@username.service
```

## Limitations

- Streaming responses are not yet implemented
- Model listing requires parsing HTML (no dedicated API endpoint)
- Conversation state is stored in `~/.shelley-fuse/state.json`; losing this file loses local-to-shelley ID mappings
## Easy Testing with Justfile

For easier testing of the FUSE filesystem, install [just](https://github.com/casey/just) and use the provided Justfile:

```bash
# Quick demo (starts server and FUSE)
just demo

# Start test server and FUSE for manual testing
just test-shell

# Or step by step
just build-tools        # Build the test tools
just start-server       # Start test server
just fuse              # Mount FUSE (auto-detects server)
```

**Available Justfile commands:**
- `just build` - Build all binaries
- `just build-tools` - Build just the test tools
- `just start-server` - Start test server
- `just fuse` - Mount FUSE filesystem
- `just demo` - Quick demo with defaults
- `just test-shell` - Start environment for testing
- `just status` - Show environment status
- `just stop` - Stop all test services
- `just clean` - Clean up artifacts

Manual testing without just:
```bash
# Build the tools
cd tools
go build -o bin/start-test-server ./start-test-server
go build -o bin/start-fuse ./start-fuse

# Start test server and mount FUSE in one step
./bin/start-fuse -mount /tmp/shelley-test

# Test the filesystem
ls /tmp/shelley-test/
cat /tmp/shelley-test/models
ID=$(cat /tmp/shelley-test/new/clone)
echo "model=predictable cwd=/tmp" > /tmp/shelley-test/conversation/$ID/ctl
echo 'Hello, Shelley!' > /tmp/shelley-test/conversation/$ID/new
cat /tmp/shelley-test/conversation/$ID/all.md

# Cleanup
./bin/start-fuse -stop -mount /tmp/shelley-test
./bin/start-test-server -stop -port 11002
```
