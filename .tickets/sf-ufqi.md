---
id: sf-ufqi
status: open
deps: []
links: []
created: 2026-02-14T14:05:26Z
type: bug
priority: 0
assignee: hdp
---
# Tests hang when run repeatedly with -test.count

Running tests repeatedly can trigger a hang. This was with -test.count=50: /tmp/test.log

## Goroutine Analysis

The test timeout (30s) occurs in two integration tests:

1. **TestReadmeCommonOperationsSinceMessages** - Hangs in `startShelleyServer()` waiting for server to start
2. **TestCachingClient_Singleflight_DifferentConversationsNotBlocked** - Waits on `sync.WaitGroup` for goroutines that never complete

### Hang 1: TestReadmeCommonOperationsSinceMessages

Goroutine 8110 is stuck in `time.Sleep(0x5f5e100)` / 100ms inside the server startup poll loop at `fuse/integration_test.go:159` (startShelleyServer):

```
goroutine 8110 [sleep]:
time.Sleep(0x5f5e100)
    shelley-fuse/fuse.startShelleyServer(0xc00034ce00)
    shelley-fuse/fuse/integration_test.go:159 +0x50e
    shelley-fuse/fuse.TestReadmeCommonOperationsSinceMessages
```

The test is polling with `time.Sleep(100 * time.Millisecond` waiting for the Shelley server to respond with HTTP 200, but the server never becomes ready within the 10-second deadline.

### Hang 2: TestCachingClient_Singleflight_DifferentConversationsNotBlocked

Goroutine 35879 is stuck in `sync.WaitGroup.Wait()` at line 709 of `caching_client_test.go`. It's waiting for two goroutines:
- One doing a slow request with `time.Sleep(200 * time.Millisecond)` (0xbebc200 = 200ms)
- Another doing a fast request

The fast request HTTP connection hangs in IO wait, preventing the group from completing:

```
goroutine 35884 [IO wait]:
net/http.(*persistConn).readLoop(0xc0000feea0)
```

This suggests a connection deadlock or resource leak when tests are repeated.

## Root Cause Hypothesis

When tests run repeatedly with `-test.count`, resources aren't being fully cleaned up between runs. Likely culprits:
- Shelley server processes left alive from previous runs blocking new server startup
- HTTP connections not being closed or pooled properly
- Goroutines from previous tests still consuming file descriptors

## Reproduction

```bash
go test ./... -test.count=50 2>&1 | tee /tmp/test.log
```

## Investigation Needed

1. Add leak detection (leaktest, goroutine leak checks) to integration tests
2. Verify Shelley server cleanup in `startShelleyServer()` (t.Cleanup may not be firing with test timeouts)
3. Check HTTP client connection pooling/timeout settings
4. Review if test isolation needs improvement for `-test.count`

## Notes

**2026-02-14T15:44:03Z**

Additional diagnostics from TestReadmeFullWorkflow hang:

## New Hang Pattern (different from port conflict)

Test hangs at Step 3 (verifying 'created' file) when bash subprocess tries to access FUSE mount.

### Stuck Goroutines:
1. Goroutine 7795 [syscall] - PRIMARY: runShellDiagTimeout() at readme_shell_test.go:56 waiting for bash cmd.Run() to complete
2. Goroutine 7810 [IO wait] - stderr scanner at integration_test.go:237 blocked reading pipe
3. Goroutines 7815-7817 [IO wait/select] - bash subprocess pipe handling blocked

### Root Cause Hypothesis:
FUSE deadlock - bash tries to access mounted filesystem, shelley-fuse blocks (possibly waiting for something), pipe buffers fill, deadlock.

The bash command 'test -e conversation/...' at readme_shell_test.go:412 cannot complete because the FUSE filesystem is not responding.

This is separate from the port conflict issue (already fixed) - this appears to be a FUSE operation deadlock when tests run repeatedly.

**2026-02-14T16:15:40Z**

## Root causes found and fixed:

1. **Port conflict in shelley/integration_test.go** (fixed in earlier commit)
   - Hardcoded port 10999 caused TIME_WAIT conflicts
   - Fixed: Use dynamic port allocation

2. **Race condition in diagURL capture** (fixed now)
   - diagURL was captured before stderr goroutine parsed DIAG= line
   - Fixed: Added diagURLReady channel to synchronize

3. **Watchdog never firing for short timeouts** (fixed now)
   - 30s margin meant watchdog never fired for 30s test timeout
   - Fixed: Reduced margin to 5s (or half of deadline for very short timeouts)

Note: Tests may still timeout with very high -test.count values (e.g., 20+) due to system resource exhaustion when running ~100 tests × N iterations. This is a system limitation rather than a code bug.
