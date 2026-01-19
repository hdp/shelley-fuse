# Shelley FUSE

A FUSE filesystem that exposes the Shelley API as a filesystem, allowing standard shell tools to interact with Shelley conversations. It parses the initial HTML page to extract model information since there's no dedicated API endpoint for models.

## Features

- Multi-host support: Access different Shelley instances via `/host:port/` paths
- Default host: Use `/default/` to access the host specified at mount time
- List models: `cat /default/models` or `cat /localhost:9999/models`
- Create new conversation: `cat /default/model/predictable/new/$PWD` (returns conversationID)
- Get conversation: `cat /default/conversation/{conversationID}`
- Send message to conversation: `cat >> /default/conversation/{conversationID}`
- Simple conversation creation: `cat /default/new`

## Usage

```bash
# Build the project
go build -o shelley-fuse ./cmd/shelley-fuse

# Mount the filesystem
./shelley-fuse /mnt/shelley http://localhost:9999

# List models
ls /mnt/shelley/default/models

# Create a new conversation with predictable model (for testing)
echo "Hello, Shelley!" > /mnt/shelley/default/model/predictable/new/$PWD

# Create a new conversation with specific model and directory
mkdir -p /mnt/shelley/default/model/predictable/new/
printf "Hello from specific directory" > /mnt/shelley/default/model/predictable/new//home/exedev

# Access a different Shelley instance
ls /mnt/shelley/localhost:8000/models

# Get conversation content
cat /mnt/shelley/default/conversation/c123456

# Send a message to a conversation
echo "Follow up message" >> /mnt/shelley/default/conversation/c123456

# Unmount with Ctrl+C or kill the process
```

## API Mapping

| Filesystem Path | Shelley API Call | Description |
|-----------------|------------------|-------------|
| `/default/models` | GET / (parses HTML for model info) | List available models |
| `/default/new` | POST /api/conversations/new | Create new conversation with defaults |
| `/default/model/{model}/new/{cwd}` | POST /api/conversations/new | Create new conversation with specific model and cwd |
| `/default/conversation/{id}` (read) | GET /api/conversation/{id} | Get conversation content |
| `/default/conversation/{id}` (write) | POST /api/conversation/{id}/chat | Send message to conversation |

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

The project now includes enhanced testing capabilities with in-process FUSE server support:

- **New `testutil` package**: Provides generic in-process FUSE server testing capabilities
- **Enhanced `testhelper` package**: Uses the shared library for both external and in-process testing
- **Better error collection**: In-process servers can capture and report errors more easily
- **Flexible API**: Generic interface allows testing of any FUSE filesystem

Example usage in tests:

```go
config := &testutil.InProcessFUSEConfig{
    MountPoint: "/tmp/mount",
    CreateFS: func() (fs.InodeEmbedder, error) {
        client := shelley.NewClient(serverURL)
        return fuse.NewFS(client), nil
    },
}

server, err := testutil.StartInProcessFUSE(config)
// ... use server, check for errors, etc.
```

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
- Directory listing for conversations is not implemented
- Error handling could be improved
- Model listing requires parsing HTML (no dedicated API endpoint)

## Future Work

- Implement streaming reads for conversations
- Add directory listing for conversations
- Improve error handling and reporting
- Add support for additional Shelley API features
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
ls /tmp/shelley-test/default/
cat /tmp/shelley-test/default/models
echo 'Hello, Shelley!' > /tmp/shelley-test/default/model/predictable/new/test

# Cleanup
./bin/start-fuse -stop -mount /tmp/shelley-test
./bin/start-test-server -stop -port 11002
```