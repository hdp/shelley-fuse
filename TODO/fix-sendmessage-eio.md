# Fix SendMessage returning EIO on subsequent writes to `new`

## Problem

Writing a second (or later) message to `/conversation/{id}/new` fails with `Input/output error`. The first message (which creates the conversation via `StartConversation`) succeeds, but all subsequent messages (which go through `SendMessage`) fail.

Reproduction:
```
$ id=$(cat /shelley/new/clone)
$ echo "model=predictable" > /shelley/conversation/$id/ctl
$ echo "hello" > /shelley/conversation/$id/new     # works
$ echo "hello again" > /shelley/conversation/$id/new  # EIO
```

## What we know so far

- `ConvNewNode.Write()` in `fuse/filesystem.go:665-696` handles both paths: first write calls `StartConversation()`, subsequent writes call `SendMessage()`.
- The first-write path (`StartConversation` at `shelley/client.go:72-124`) works — it accepts HTTP 200 and 201.
- The subsequent-write path (`SendMessage` at `shelley/client.go:150-186`) fails — it accepts only HTTP 200 and 202.
- `ConvNewNode.Write()` returns `syscall.EIO` on any error from either path, with no logging, so the actual API error is invisible.
- The shelley-fuse binary connects to `http://localhost:9999` but the Shelley server listens with systemd activation. The port may differ from what the FUSE binary expects, or the API may be returning an unexpected status code.

## Investigation needed

1. **Add error logging** to `ConvNewNode.Write()` (around line 682 and 690 in `fuse/filesystem.go`) so the actual error from `StartConversation`/`SendMessage` is visible. Currently both paths silently swallow the error and return `EIO`. At minimum, `log.Printf` the error before returning.

2. **Check what status code `SendMessage` gets back**: The API may return 201 (Created) for new messages but `SendMessage` only accepts 200/202. Compare with `StartConversation` which accepts 200/201. Try adding 201 to `SendMessage`'s accepted status codes.

3. **Check if the Shelley server URL is correct**: The FUSE binary is launched with `http://localhost:9999` but the Shelley server process shows it's using systemd activation, not a fixed port. Verify the server is actually listening on 9999.

4. **Check if the conversation is "busy"**: The first message may leave the conversation in a state where the backend is still processing (generating a response). The second write may arrive before the backend is ready, causing a 409 Conflict or similar.

## Key files

- `fuse/filesystem.go` — `ConvNewNode.Write()` (line 665-696), both the `StartConversation` and `SendMessage` error paths
- `shelley/client.go` — `SendMessage()` (line 150-186), check accepted status codes; `StartConversation()` (line 72-124) for comparison
- `cmd/shelley-fuse/main.go` — where the server URL is passed in
