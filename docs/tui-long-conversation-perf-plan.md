# TUI Long Conversation Performance Fix

## Problem
TUI becomes slow and heavy after several compactions when accumulated input
exceeds ~1M tokens. Fresh sessions are fine.

## Root Cause Analysis

### 1. Full re-render on every message count change
`buildRenderedHistory()` re-renders ALL messages (up to 200) through glamour
markdown renderer on every cache invalidation. When a new message arrives, the
cache key changes (`sessionKey:width:msgCount`), triggering a full rebuild of
200 messages. Each `renderMarkdown()` call does full markdown parsing + ANSI
generation, which is expensive for large messages (10K+ chars common in long
conversations).

**Impact**: ~200ms+ per rebuild for 200 large messages.

### 2. Multiple `GetHistoryView()` calls per render cycle
`updateViewport()` calls `GetHistoryView()` 4+ times:
- `cleanupStreamingIfComplete()`
- `getHistoryMessageCount()`
- `buildRenderedHistory()`
- `lastHistoryRole()`

Each call acquires a mutex lock. For in-memory sessions this is cheap, but the
redundancy is unnecessary.

### 3. No per-message rendering cache
Every time the cache is invalidated, ALL messages are re-rendered from scratch
through glamour. Historical messages never change, so their rendered output
could be cached.

## Solution — IMPLEMENTED ✅

### Phase 1: Per-message render cache (BIGGEST WIN) ✅
- Added `msgRenderCache map[string]string` to Model (fingerprint → rendered output)
- FNV-64a hash of (role, content, reasoningContent, toolCalls, width)
- `buildRenderedHistoryFromHistory()` checks cache before rendering each message
- Cache cleared on width change and session switch, NOT on message count change
- Result: O(k) re-rendering where k = new messages, vs O(n) previously

### Phase 2: Single history fetch per render ✅
- `updateViewport()` fetches history once via `GetHistoryView()`
- Added `cleanupStreamingIfCompleteWithHistory(history)`, `countHistoryMessages(history)`, `lastHistoryRoleFromHistory(history)` pure functions
- Eliminates 3-4 redundant mutex acquisitions per render cycle

### Phase 3: Tests ✅
- `TestMessageFingerprint_Stable` — same message = same fingerprint
- `TestMessageFingerprint_WidthSensitive` — different width = different fingerprint
- `TestMessageFingerprint_RoleSensitive` — different role = different fingerprint
- `TestMessageFingerprint_ToolCallsSensitive` — tool calls affect fingerprint
- `TestCountHistoryMessages` — pure counter matches expected
- `TestLastHistoryRoleFromHistory` — pure role lookup works correctly

## Files Modified
- `pkg/tui/types.go` — added msgRenderCache fields
- `pkg/tui/utils.go` — added messageFingerprint helper (FNV-64a)
- `pkg/tui/viewport.go` — refactored updateViewport, buildRenderedHistory, added pure functions
- `pkg/tui/model.go` — added cleanupStreamingIfCompleteWithHistory, clear cache on session switch
- `pkg/tui/handlers.go` — clear msgRenderCache on terminal width change
- `pkg/tui/perf_cache_test.go` — 6 new tests for fingerprint, counter, role lookup
