---
id: sf-ufqi
status: closed
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
