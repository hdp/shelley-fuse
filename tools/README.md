# Shelley FUSE Tools

This directory contains Go tool programs for testing Shelley FUSE filesystem.

## Tools

### start-test-server
Starts a predictable-only Shelley server for testing.

```bash
# Start on default port 11002
./bin/start-test-server

# Start on specific port
./bin/start-test-server -port 11003

# Stop server
./bin/start-test-server -stop -port 11002
```

### start-fuse
Starts a shelley-fuse filesystem that connects to a test server.

```bash
# Auto-start test server and mount FUSE
./bin/start-fuse -mount /tmp/fuse-test

# Use existing server
./bin/start-fuse -mount /tmp/fuse-test -server-url http://localhost:11002

# Use existing server on specific port (auto-detect)
./bin/start-fuse -mount /tmp/fuse-test -server-port 11002

# Stop FUSE mount
./bin/start-fuse -stop -mount /tmp/fuse-test
```

## Architecture

The tools share a common `testutils` package that reuses code from `shelley/integration_test.go`:

- `testutils/server.go`: Server management utilities (reuses `waitForServer`, env clearing patterns)
- `testutils/fuse.go`: FUSE filesystem management utilities

Both tools handle:
- Process management with PID files
- Graceful shutdown
- Log management
- Error handling

## Building

```bash
cd tools
go mod tidy
go build -o bin/start-test-server ./start-test-server
go build -o bin/start-fuse ./start-fuse
```

## Usage Examples

### Quick Test
```bash
# 1. Start test server
./bin/start-test-server &

# 2. Mount FUSE (reuses running server)
./bin/start-fuse -mount /tmp/my-fuse

# 3. Test filesystem
ls /tmp/my-fuse/default/
cat /tmp/my-fuse/default/models

# 4. Cleanup
./bin/start-fuse -stop -mount /tmp/my-fuse
./bin/start-test-server -stop -port 11002
```

### All-in-One Development
```bash
# Auto-start server and FUSE in one step
./bin/start-fuse
# Press Ctrl+C to stop both
```

## Architecture Benefits

- Reusable Go components
- Better error handling
- Shared test utilities
- Modular design
- Type safety
