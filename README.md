# Shelley FUSE

A FUSE filesystem that exposes the [Shelley](https://github.com/boldsoftware/shelley) API, allowing shell tools to interact with Shelley conversations through standard file operations.

## What is Shelley FUSE?

Shelley FUSE mounts the Shelley AI conversation platform as a local filesystem. This lets you create conversations, send messages, and read responses using familiar shell commands like `cat`, `echo`, and `ls`.

```bash
# Start a conversation from the shell
ID=$(echo "Explain Go interfaces" | model/claude-sonnet-4-5/new/start)

# Read the response
cat conversation/$ID/messages/last/1/0/content.md

# Send a follow-up
echo "Show me an example" > conversation/$ID/send
```

## Installation

### Prerequisites

- Go 1.22.2 or later
- `fusermount` binary (usually provided by `fuse` package)
- A running [Shelley](https://github.com/boldsoftware/shelley) server

### Build from source

```bash
git clone https://github.com/hdp/shelley-fuse
cd shelley-fuse
just build
```

### Install

```bash
# Copy to a directory in your PATH
sudo cp shelley-fuse /usr/local/bin/

# Or use the justfile
just install
```

## Quick Start

```bash
# Mount the filesystem
mkdir -p ~/shelley-mount
shelley-fuse -mount ~/shelley-mount -server http://localhost:9999

# In another terminal, start a conversation
ID=$(echo "Hello, Shelley!" | ~/shelley-mount/model/claude-sonnet-4.5/new/start)

# Read the response
cat ~/shelley-mount/conversation/$ID/messages/last/1/0/content.md
```

## Filesystem Usage

Once mounted, the filesystem provides a shell-friendly control file interface. See the embedded `README.md` at the mountpoint for complete documentation:

```bash
cat ~/shelley-mount/README.md
```

Or browse the source at [`fuse/README.md`](fuse/README.md).

### Key Concepts

- **Models**: Models supported by Shelley backend under `model/{model-id}/`
- **Conversations**: Active conversations under `conversation/{id}/`
- **Control files**: Configure conversations via `ctl`, send messages via `send`
- **Messages**: Read conversation history in `messages/{N}`, `messages/last/{N}/`, or `messages/since/{slug}/{N}/`
- **Content**: Individual fields from nested JSON objects exposed as files, or read `messages/{N}/content.md` for a rendered view

## Development

### Quick commands

```bash
# Build
just build

# Run tests (requires /usr/local/bin/shelley and fusermount)
just test

# Start for manual testing
just dev

# Clean build artifacts
just clean
```

## Links

- [Shelley](https://github.com/boldsoftware/shelley) — The AI conversation platform
- [`fuse/README.md`](fuse/README.md) — Detailed filesystem usage documentation

## License

MIT
