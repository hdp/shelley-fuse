# Feature: cwd symlink in conversation directories

Currently `/conversation/{ID}/status/cwd` is a text file containing a directory path.

Change this to be a symlink pointing to the actual directory, so users can:

```bash
cd $(readlink /mnt/shelley/conversation/$ID/status/cwd)
# or directly:
ls /mnt/shelley/conversation/$ID/status/cwd/
```

## Implementation Notes

- Modify `StatusDirNode` in `fuse/filesystem.go` to return a symlink node for "cwd" instead of a file
- Use `StableAttr{Mode: syscall.S_IFLNK}` for the symlink node
- Implement `Readlink()` method returning the cwd path from conversation state
- Handle the case where cwd is empty (return ENOENT or empty symlink?)
