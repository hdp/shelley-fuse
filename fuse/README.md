# Shelley FUSE

A FUSE filesystem that exposes the Shelley API, allowing shell tools to interact with Shelley conversations.

## Quick Start

```bash
# Start a conversation in one step (clones, sets cwd, sends, prints ID)
ID=$(echo "Hello, Shelley!" | models/predictable/new/start)

# Read the response(s)
cat conversation/$ID/messages/since/user/1/*/content.md

# Send follow-up
echo "Thanks!" > conversation/$ID/send
```

### Manual Workflow (step by step)

```bash
# Allocate a new conversation
ID=$(cat new/clone)

# Configure model and working directory (optional)
echo "model=claude-sonnet-4.5 cwd=$PWD" > conversation/$ID/ctl

# Send first message (creates conversation on backend)
echo "Hello, Shelley!" > conversation/$ID/send

# Read the response(s)
cat conversation/$ID/messages/since/user/1/*/content.md

# Send follow-up
echo "Thanks!" > conversation/$ID/send
```

## Filesystem Layout

```
/
  README.md              → this file
  models/                → available models
    default              → symlink to default model
    {model-id}/          → directory per model
      id                 → model ID
      ready              → present if model is ready (absence = not ready)
      new/
        clone            → read to get a new conversation with this model preconfigured
        start            → executable: pipe message to start conversation with this model
  new/
    clone                → read to allocate a new conversation ID
    start                → executable: pipe message to start conversation with caller's cwd
  conversation/          → all conversations
    {id}/                → directory per conversation
      ctl                → read/write config; read-only after first message
      send               → write here to send messages
      archived           → present when archived; touch to archive, rm to unarchive
      model              → symlink to ../../models/{model-id}
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
ls models/

# Check default model
readlink models/default

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
