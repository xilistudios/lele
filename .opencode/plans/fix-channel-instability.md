# Plan: Fix Channel Integration Instability (Telegram + Web/Native)

## Root Cause Analysis

The MessageBus uses **blocking Go channels** that cause **cascading deadlocks** when any channel is slow:

```
Native streaming (many rapid chunks)
  → native dispatch queue (50) fills
    → dispatchOutbound goroutine blocks
      → bus.outbound (100) fills
        → PublishOutbound blocks in agent loop
          → agent can't process messages
            → bus.inbound (100) fills
              → PublishInbound blocks on ALL channels
                → Telegram: messages not processed
                → Web: WS stops responding → disconnect
```

## Files to Modify

### 1. `pkg/bus/bus.go` - Non-blocking publishes

**Changes:**
- Increase channel buffers: 100 → 500
- `PublishInbound`: use `select` with `default` (non-blocking drop instead of blocking)
- `PublishOutbound`: use `select` with `default` (non-blocking drop instead of blocking)
- Add `sync/atomic` counters for dropped messages
- Add `Stats()` method for observability

**Before:**
```go
func (mb *MessageBus) PublishInbound(msg InboundMessage) {
    mb.mu.RLock()
    defer mb.mu.RUnlock()
    if mb.closed { return }
    mb.inbound <- msg  // BLOCKS when full!
}
```

**After:**
```go
func (mb *MessageBus) PublishInbound(msg InboundMessage) {
    mb.mu.RLock()
    defer mb.mu.RUnlock()
    if mb.closed { return }
    select {
    case mb.inbound <- msg:
    default:
        mb.droppedInbound.Add(1)
    }
}
```

### 2. `pkg/channels/manager.go` - Parallelize dispatch

**Changes:**
- Increase dispatch queue buffers: 50 → 200
- `dispatchOutbound`: use non-blocking send to per-channel queues (skip with warning if full)
- Remove single-goroutine bottleneck

**Before (blocking):**
```go
select {
case queue <- msg:
case <-ctx.Done():
    return
}
```

**After (non-blocking with warning):**
```go
select {
case queue <- msg:
default:
    logger.WarnCF("channels", "Dispatch queue full, dropping message", ...)
}
```

### 3. `pkg/channels/native.go` - Reduce mutex contention

**Changes:**
- Replace `sync.Mutex` with `sync.RWMutex` for `wsClients` access
- `broadcastToSession`/`broadcastAll`: acquire RLock for iteration, only WLock for cleanup
- Use `sync.Map` for `wsClients` instead of `map[string]*WSClient` + mutex
- Increase `SendChan` buffer: 100 → 256
- Rate limit: 30/min → 120/min (`wsMessageLimiter`)

### 4. `pkg/channels/websocket.go` - Fix ping/write contention

**Changes:**
- `wsPingLoop`: use dedicated ping channel instead of competing for `client.mu`
- `wsWriteLoop`: handle ping messages with higher priority via channel select
- Increase read deadline: 60s → 90s
- Remove dead code: `RegisterOutboundHandler` method (never called)

### 5. New file: `pkg/channels/multi_channel_test.go` - E2E integration tests

**Tests to add:**
- `TestMultiChannelConcurrentMessages`: Send messages from telegram + native simultaneously
- `TestNativeStreamingDoesNotBlockTelegram`: Verify heavy native streaming doesn't block telegram
- `TestBusDropMetrics`: Verify dropped message counting works
- `TestWebSocketReconnectUnderLoad`: Verify WS reconnects under heavy streaming
- `TestDispatchQueueOverflow`: Verify messages are dropped gracefully, not blocking

## Execution Order

1. `pkg/bus/bus.go` (foundation - non-blocking bus)
2. `pkg/channels/manager.go` (dispatch parallelization)
3. `pkg/channels/native.go` (mutex contention fix)
4. `pkg/channels/websocket.go` (ping fix + dead code removal)
5. `pkg/channels/multi_channel_test.go` (E2E tests)
6. Run `go build ./...` and `go test ./pkg/bus/... ./pkg/channels/... ./pkg/agent/... -v`
7. Run `golangci-lint run`
