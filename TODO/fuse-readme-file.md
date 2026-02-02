# Feature: /README.md in FUSE filesystem root

Expose a `/README.md` file at the root of the mounted FUSE filesystem that explains how to use the filesystem. This makes the filesystem self-documenting — users can `cat /shelley/README.md` to learn how to interact with it.

## Motivation

When a user mounts shelley-fuse and runs `ls`, they see:

```
conversation/  models/  new/
```

This doesn't tell them how to use it. A README.md at the root provides immediate guidance:

```bash
cat /shelley/README.md
```

## Content

The README should cover:

1. **Quick start workflow** (clone → configure → send message → read response):
   ```bash
   # Allocate a new conversation
   ID=$(cat /new/clone)
   
   # Configure model and working directory
   echo "model=claude-opus-4.5 cwd=$PWD" > /conversation/$ID/ctl
   
   # Send first message (creates conversation on backend)
   echo "Hello, Shelley!" > /conversation/$ID/new
   
   # Read the response
   cat /conversation/$ID/messages/all.md
   
   # Send follow-up
   echo "Thanks!" > /conversation/$ID/new
   ```

2. **Filesystem layout overview** — brief description of `/models/`, `/conversation/`, `/new/`

3. **Common operations**:
   - List models: `ls /models`
   - Check default model: `readlink /models/default`
   - List conversations: `ls /conversation`
   - Read last N messages: `cat /conversation/$ID/messages/last/5.md`

4. **Links** to full project documentation (if applicable)

### Source Options

The content could come from:

1. **Embedded in binary**: Compile a usage string into the Go binary, serve it as a static file
2. **Subset of project README.md**: The project had a README.md (deleted in commit `vplxpknn`) with a good "Usage" section. Could embed that portion.
3. **Generated dynamically**: Build the content at runtime (not recommended — adds complexity)

Recommendation: Embed a static usage guide in the binary. Keep it focused on filesystem usage, not build/test instructions.

## Implementation Notes

### Code Location

In `fuse/filesystem.go`, add handling in `RootNode`:

1. **RootNode.Lookup()**: Return a `ReadmeNode` for "README.md"
2. **RootNode.Readdir()**: Include "README.md" in directory listing
3. **ReadmeNode**: Simple file node that returns static content

### ReadmeNode Implementation

```go
type ReadmeNode struct {
    fs.Inode
}

func (n *ReadmeNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
    return nil, fuse.FOPEN_DIRECT_IO, fs.OK
}

func (n *ReadmeNode) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
    content := readmeContent // embedded string constant
    // ... handle offset and length
}

func (n *ReadmeNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
    out.Mode = 0444  // read-only
    out.Size = uint64(len(readmeContent))
    // ... timestamps
}
```

### Content Embedding

Use Go embed directive:

```go
import _ "embed"

//go:embed fuse_readme.md
var readmeContent string
```

Or just define as a const string if it's short.

## Historical Context

The project had a README.md that was deleted in jj commit `vplxpknn` ("feat: implement /models/default symlink functionality"). The deletion was likely unintentional — grouped with "Clean up TODO file and README" in the commit message. That README had useful content including:

- Features list with example commands
- Usage section with full workflow example
- Filesystem layout diagram
- API mapping table
- Server conversation discovery explanation

Much of this was redundant with CLAUDE.md (which remains). For the FUSE README, extract just the user-facing usage portions, not the developer/architecture content.

## Testing

- Verify `cat /shelley/README.md` returns content
- Verify `ls /shelley` includes README.md
- Verify file attributes (mode 0444, correct size)
