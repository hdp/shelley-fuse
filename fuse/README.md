# Shelley FUSE

A FUSE filesystem that exposes the Shelley API, allowing shell tools to interact with Shelley conversations.

## Quick Start

```bash
# Allocate a new conversation
ID=$(cat new/clone)

# Configure model and working directory (optional)
echo "model=claude-sonnet-4.5 cwd=$PWD" > conversation/$ID/ctl

# Send first message (creates conversation on backend)
echo "Hello, Shelley!" > conversation/$ID/send

# Read the response
cat conversation/$ID/messages/all.md

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
    clone                → read to allocate a new conversation ID
  conversation/          → all conversations
    {id}/                → directory per conversation
      ctl                → read/write config; read-only after first message
      send               → write here to send messages
      id                 → Shelley server conversation ID
      slug               → conversation slug (if set)
      created            → present if created on backend (absence = not created)
      messages/          → all message content
        all.json         → full conversation as JSON
        all.md           → full conversation as Markdown
        count            → number of messages
        0-user/          → message directory (0-indexed, named by slug)
          message_id     → message UUID
          conversation_id → conversation ID
          sequence_id    → sequence number
          type           → message type (user, agent, etc.)
          created_at     → timestamp
          content.md     → markdown rendering
          llm_data/      → unpacked JSON (if present)
          usage_data/    → unpacked JSON (if present)
        last/{N}         → symlink to Nth-to-last message (../../{NNN-{slug}})
          last/1         → symlink to last message
          last/2         → symlink to second-to-last message
          ...
        since/{slug}/{N} → symlink to Nth message after last {slug} (../../../{NNN-{slug}})
          since/user/1   → symlink to first message after last user message
          since/user/2   → symlink to second message after last user message
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

# Read the last message
cat conversation/$ID/messages/last/1/content.md

# Read the second-to-last message
cat conversation/$ID/messages/last/2/content.md

# Read the first message after your last message
cat conversation/$ID/messages/since/user/1/content.md

# Read the second message after your last message
cat conversation/$ID/messages/since/user/2/content.md

# Get message count
cat conversation/$ID/messages/count

# Check if conversation is created
test -e conversation/$ID/created && echo created
```
