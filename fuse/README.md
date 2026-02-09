# Shelley FUSE

A FUSE filesystem that exposes the Shelley API, allowing shell tools to interact with Shelley conversations.

## Quick Start

```bash
# Start a conversation with a specific model in one step
ID=$(echo "Hello, Shelley!" | model/claude-sonnet-4-5/new/start)

# Read the response(s)
cat conversation/$ID/messages/since/user/1/*/content.md

# Send follow-up
echo "Thanks!" > conversation/$ID/send
```

The `model/{model}/new/start` script is an executable that reads a message
from stdin, allocates a new conversation with that model preconfigured, sets
the working directory to the caller's `$PWD`, sends the message, and prints
the new conversation ID to stdout.

There is also a top-level `new/start` that works the same way but uses the
server's default model instead of a specific one.

### Manual Workflow (step by step)

```bash
# Allocate a new conversation with a specific model preconfigured
ID=$(cat model/claude-sonnet-4-5/new/clone)

# Optionally set the working directory
echo "cwd=$PWD" > conversation/$ID/ctl

# Send first message (creates conversation on backend)
echo "Hello, Shelley!" > conversation/$ID/send

# Read the response(s)
cat conversation/$ID/messages/since/user/1/*/content.md

# Send follow-up
echo "Thanks!" > conversation/$ID/send
```

Or without choosing a model:

```bash
# Allocate a new conversation (no model preconfigured)
ID=$(cat new/clone)

# Configure model and working directory (optional)
echo "model=claude-sonnet-4-5 cwd=$PWD" > conversation/$ID/ctl

# Send first message (creates conversation on backend)
echo "Hello, Shelley!" > conversation/$ID/send
```

## Filesystem Layout

```
/
  README.md              → this file
  model/                → available models
    default              → symlink to default model
    {model-id}/          → directory per model
      id                 → model ID
      ready              → present if model is ready (absence = not ready)
      new/
        clone            → read to allocate a conversation with this model preconfigured
        start            → executable: pipe message on stdin → clones with this model,
                           sets cwd to caller's $PWD, sends message, prints conversation ID
  new/
    clone                → read to allocate a new conversation ID (no model preconfigured)
    start                → executable: pipe message on stdin → clones, sets cwd to caller's
                           $PWD, sends message, prints conversation ID (default model)
  conversation/          → all conversations
    {id}/                → directory per conversation
      ctl                → read/write config; read-only after first message
      send               → write here to send messages
      archived           → present when archived; touch to archive, rm to unarchive
      model              → symlink to ../../model/{model-id}
      cwd                → symlink to working directory
      id                 → Shelley server conversation ID
      fuse_id            → local FUSE conversation ID
      slug               → conversation slug (if set)
      created            → present if created on backend (absence = not created)
      messages/          → all message content
        all.json         → full conversation as JSON
        all.md           → full conversation as Markdown
        count            → number of messages
        000-user/        → message directory (0-indexed, zero-padded, named by slug)
          content.md     → markdown rendering of the message
          llm_data/      → unpacked JSON (if present)
          usage_data/    → unpacked JSON (if present)
          ...            → plus metadata: message_id, type, created_at, etc.
        last/{N}/        → directory containing the last N messages as symlinks
          {0..N-1}       → ordinal symlinks (0 = oldest, N-1 = newest) → ../../{NNN-{slug}}
          last/1/         → directory with 1 entry: the last message
            0             → ../../004-agent
          last/2/         → directory with 2 entries: the last 2 messages
            0             → ../../003-user
            1             → ../../004-agent
          ...
        since/{slug}/{N}/ → directory containing messages after the Nth-to-last {slug}
          {NNN-{slug}}    → message-name symlinks → ../../../{NNN-{slug}}
          since/user/1/   → messages after the last user message
            004-agent     → ../../../004-agent
          since/user/2/   → messages after the second-to-last user message
            003-user      → ../../../003-user  (the last user message itself, if it follows)
            004-agent     → ../../../004-agent
          ...

```

## Common Operations

```bash
# List available models
ls model/

# Check default model
readlink model/default

# Start a conversation with a specific model (one step)
ID=$(echo "Explain FUSE" | model/claude-sonnet-4-5/new/start)

# Clone a conversation with a model preconfigured, then configure and send manually
ID=$(cat model/claude-sonnet-4-5/new/clone)
echo "cwd=/my/project" > conversation/$ID/ctl
echo "Hello" > conversation/$ID/send

# Start a conversation with the default model (one step)
ID=$(echo "Explain FUSE" | new/start)

# List conversations
ls conversation/

# List the last 2 messages
ls conversation/$ID/messages/last/2/
# 0 -> ../../003-user
# 1 -> ../../004-agent

# Read the content of the very last message (the sole entry in last/1/)
cat conversation/$ID/messages/last/1/0/content.md

# Read all messages since the last user message
ls conversation/$ID/messages/since/user/1/
# 004-agent -> ../../../004-agent
cat conversation/$ID/messages/since/user/1/004-agent/content.md

# Get message count
cat conversation/$ID/messages/count

# Check if conversation is created
test -e conversation/$ID/created && echo created

# Check which model a conversation uses
readlink conversation/$ID/model

# Archive a conversation
touch conversation/$ID/archived

# Unarchive a conversation
rm conversation/$ID/archived

# Check if archived
test -e conversation/$ID/archived && echo archived
```

## Advanced

Each conversation has a `meta/` subdirectory providing conversation metadata
(local_id, timestamps, etc.) as a directory of plain files. Each message
directory also contains metadata files (`message_id`, `conversation_id`,
`sequence_id`, `type`, `created_at`) alongside `content.md`.
