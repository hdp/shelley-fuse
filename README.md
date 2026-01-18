# Shelley FUSE

A FUSE filesystem that exposes the Shelley API as a filesystem, allowing standard shell tools to interact with Shelley conversations. It parses the initial HTML page to extract model information since there's no dedicated API endpoint for models.

## Features

- Multi-host support: Access different Shelley instances via `/host:port/` paths
- Default host: Use `/default/` to access the host specified at mount time
- List models: `cat /default/models` or `cat /localhost:9999/models`
- Create new conversation: `cat /default/model/predictable/new/$PWD` (returns conversationID)
- Get conversation: `cat /default/conversation/{conversationID}`
- Send message to conversation: `cat >> /default/conversation/{conversationID}`
- Simple conversation creation: `cat /default/new` (uses defaults)

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