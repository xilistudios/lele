# Message Ordering Analysis — lele web

**Date**: 2026-04-26  
**Scope**: Full trace of message flow from backend → frontend store → render  
**Status**: Analysis only — no code changes made

---

## Table of Contents

1. [How messages are added/ordered in the store](#1-how-messages-are-addedordered-in-the-store)
2. [Two input paths: WS live vs HTTP history](#2-two-input-paths-ws-live-vs-http-history)
3. [How MessageList renders (sort vs array order)](#3-how-messagelist-renders-sort-vs-array-order)
4. [Ordering fields: timestamp/id/seq — where they come from](#4-ordering-fields-timestampidseq--where-they-come-from)
5. [Race conditions, Date.now(), setTimeout, async issues](#5-race-conditions-datenow-settimeout-async-issues)
6. [Root cause and recommendation](#6-root-cause-and-recommendation)

---

## 1. How messages are added/ordered in the store

### State architecture (three layers)

| Layer | Hook | State type | Purpose |
|---|---|---|---|
| Streaming | `useMessages` | `streamingMessages: ChatMessage[]` | Live WS-driven messages (optimistic user, streaming assistant, tool msgs) |
| Base (HTTP) | `useChatHistory` (React Query) | `query.data.messages: ChatMessage[]` | Canonical history from HTTP `/api/v1/chat/sessions/{key}/history` |
| Merged | `useChatHistory` → `mergeMessages()` | `messages: ChatMessage[]` | Final combined array passed to `MessageList` |

### Key files and line numbers

- **`src/hooks/useMessages.ts`** — streaming state management
  - Line 26: `const [streamingMessages, setStreamingMessages] = useState<ChatMessage[]>([])`
  - Line 131-162: `sendMessage()` — creates optimistic user message, appends to `streamingMessages`, also patches React Query cache
  - Line 97: `handleEvent` delegates to `dispatchMessageEvent()`

- **`src/hooks/messageEventHandlers.ts`** — all WS event handlers
  - Line 148-154: `handleMessageStream()` — enqueues streaming chunks via `streamQueues.enqueueChunk()`
  - Line 196-206: `handleMessageAck()` — ensures assistant placeholder exists
  - Line 210-255: `handleMessageComplete()` — marks assistant as `streaming: false`, removes restore placeholders
  - Line 258-276: `handleHistoryUpdated()` — invalidates React Query cache, cleans streaming copies
  - Line 278-302: `handleMessagesCatchup()` — replaces base history with catchup data, clears assistant/tool streaming
  - Line 304-330: `handleToolExecuting()` — inserts tool messages after last assistant (and its trailing tools)
  - Line 332-362: `handleToolResult()` — updates tool message status to completed/error

- **`src/hooks/useStreamQueues.ts`** — character-by-character animation
  - Line 21: `STREAM_CHAR_INTERVAL_MS = 12` — 12ms per character
  - Line 33-83: `ensureAssistantPlaceholder()` — creates/updates assistant in `streamingMessages`, inserts before trailing tools
  - Line 96-118: `enqueueChunk()` — queues characters, starts `setInterval` timer

- **`src/hooks/useChatHistory.ts`** — HTTP history + merge
  - Line 64-222: `mergeMessages()` — the critical merge function
  - Line 224-338: `useChatHistory()` — React Query hook, `queryFn` fetches history, `loadMore` for pagination
  - Line 339-355: `updateChatHistoryFromRaw()` — helper used by `handleMessagesCatchup`

### How `streamingMessages` array is built (append/insert logic)

Messages are added to `streamingMessages` via `setStreamingMessages()` with functional updaters:

1. **User messages** (`sendMessage` at `useMessages.ts:131`): `[...current, userMessage]` — always appended at end

2. **Assistant placeholders** (`ensureAssistantPlaceholder` at `useStreamQueues.ts:33`):
   - If assistant already exists: `map()` to update in-place
   - If new assistant: inserts **before trailing tools** of the last assistant, or before all tools if no assistant exists
   - Insertion logic at lines 52-78

3. **Tool messages** (`handleToolExecuting` at `messageEventHandlers.ts:304`):
   - If `toolCallId` matches existing tool: update in-place
   - Otherwise: find last assistant, skip past all trailing tools, insert at that position
   - This ensures chronological order: `assistant → tool1 → tool2 → tool3`
   - Insertion logic at lines 316-328

4. **Completed messages** (`handleMessageComplete` at `messageEventHandlers.ts:210`):
   - Uses `flatMap()` to filter/update: marks assistant `streaming: false`, removes empty users, marks tools `streaming: false`

5. **History cleanup** (`handleHistoryUpdated` at `messageEventHandlers.ts:258`):
   - Removes optimistic users and completed (non-streaming) assistants from `streamingMessages`
   - The HTTP refetch (triggered by `invalidateQueries`) will provide them in `baseMessages` instead

### How `mergeMessages()` combines the two layers

**File**: `src/hooks/useChatHistory.ts`, lines 64-222

The merge uses **position-based matching** (NOT ID-based), because:
- WS messages use UUID IDs (`message_id` from backend, e.g. `uuid.New().String()`)
- HTTP messages use content-hash IDs (`sessionKey:sha256[:8]`)

**Algorithm**:
1. Count base users/assistants, find optimistic user
2. Determine `baseHasCurrentTurn` — true only when base has BOTH the user AND the assistant for the current turn
3. Calculate `matchOffset` — streaming assistants correspond to the LAST N base assistants
4. Build `filteredBase`:
   - For matched assistants (at offset): if streaming copy is actively streaming, use it in-place; otherwise keep base version
   - For tool messages: replace base tool with streaming version if `toolCallId` matches
5. Build `filteredStreaming` — keep only items not already placed in `filteredBase`
6. Return `[...filteredBase, ...filteredStreaming]`

---

## 2. Two input paths: WS live vs HTTP history

### Path A: WebSocket (live streaming)

**Flow**:
```
User sends message
  → wsSend('message', {...})
  → Backend receives, sends message.ack
  → Frontend: handleMessageAck → ensureAssistantPlaceholder
  → Backend streams: message.stream (chunks), message.thinking (reasoning)
  → Frontend: handleMessageStream → enqueueChunk → setInterval(12ms) → setStreamingMessages
  → Backend executes tools: tool.executing → tool.result
  → Frontend: handleToolExecuting/handleToolResult → setStreamingMessages
  → Backend finishes: message.complete
  → Frontend: handleMessageComplete → marks streaming=false
  → Backend persists, sends history.updated
  → Frontend: handleHistoryUpdated → invalidateQueries → HTTP refetch
```

**ID scheme**: `message_id` from backend = `uuid.New().String()` (random UUID)  
**Source**: `native.go:564-566` — `messageID := uuid.New().String()`

**Timestamp**: `createdAt` set by `new Date().toISOString()` at the moment the frontend creates the ChatMessage object. Different events get different timestamps because they happen at different moments.

### Path B: HTTP history (page load / refetch / pagination)

**Flow**:
```
Page load / refetch / scroll up
  → useChatHistory.queryFn → api.history(sessionKey, ...)
  → GET /api/v1/chat/sessions/{sessionKey}/history
  → Backend: handleChatHistory (rest_chat.go:68)
  → Returns ChatHistoryMessage[] (ordered by history position)
  → Frontend: toChatMessages() → ChatMessage[]
  → Stored in React Query cache as baseMessages
```

**ID scheme**: Content-hash based — `sessionKey:sha256(role+content+toolCallId+toolCalls)[:8]`  
**Source**: `rest_chat.go:149-162`

**Timestamp**: `createdAt` set by `new Date().toISOString()` at the moment `toChatMessages()` runs. ALL messages in a single batch get nearly identical timestamps (within milliseconds of each other).

**Critical difference**: The `ChatHistoryMessage` struct (types.go:219-229) does NOT include a `created_at` field. The backend returns messages in chronological order but without timestamps. The frontend fabricates timestamps at conversion time.

### Path C: Catchup (WS reconnection)

**Flow**:
```
WebSocket reconnects → welcome/reconnected event with in_progress_messages
  → handleWelcome: restores streaming messages from accumulated content
  → messages.catchup event (if initial)
  → handleMessagesCatchup: replaces base history via updateChatHistoryFromRaw()
  → Clears assistant/tool streaming messages
```

**Source**: `messageEventHandlers.ts:278-302`

### How the paths differ

| Aspect | WS live | HTTP history | Catchup |
|---|---|---|---|
| ID type | UUID | Content-hash | Provided by backend |
| Timestamp | Moment of ChatMessage creation | Moment of `toChatMessages()` call | N/A (uses provided data) |
| Ordering guarantee | Array insertion order | Array order from backend | Array order from backend |
| When it arrives | During active conversation | On page load, refetch, scroll | On WS reconnection |
| Dedup strategy | Position-based matching in `mergeMessages()` | Serves as canonical base | Replaces base entirely |

---

## 3. How MessageList renders (sort vs array order)

**File**: `src/components/organisms/MessageList.tsx`

### Rendering pipeline

1. **Receive `messages`** from `useAppLogicContext()` (line 42) — this is the merged array from `mergeMessages()`

2. **Filter guidance messages** (line 188-190):
   ```typescript
   const visibleMessages = messages.filter(
     (m) => !m.content.startsWith('⚠️ GUIDANCE:') && !m.content.startsWith('GUIDANCE:'),
   )
   ```

3. **Build `renderItems`** — interleaves messages and group blocks (lines 195-205):
   ```typescript
   for (let i = 0; i < visibleMessages.length; i++) {
     renderItems.push({ type: 'message', message: visibleMessages[i], index: i })
   }
   for (const group of groups.values()) {
     renderItems.push({ type: 'group', group })
   }
   ```

4. **⚠️ SORT BY createdAt** (lines 207-211):
   ```typescript
   renderItems.sort((a, b) => {
     const timeA = a.type === 'message' ? a.message.createdAt : a.group.createdAt
     const timeB = b.type === 'message' ? b.message.createdAt : b.group.createdAt
     return new Date(timeA).getTime() - new Date(timeB).getTime()
   })
   ```

### The sort problem

This is the **critical sort** that can reorder messages. It sorts ALL render items (messages + groups) by `createdAt` timestamp.

**Why this is problematic**:
- `mergeMessages()` already produces a correctly ordered array (preserving backend canonical order)
- The sort REORDERS based on fabricated timestamps that don't reflect actual message creation time
- For messages from the same HTTP refetch batch, `createdAt` values are nearly identical (within ms), making the sort unstable
- For streaming messages that completed before the HTTP refetch, their `createdAt` (from WS time) can be OLDER than base messages' `createdAt` (from refetch time), causing them to sort to the wrong position

### Example of the bug

```
Timeline:
  T=1000: User sends message → optimistic user createdAt=T1000
  T=1100: WS streams assistant → streaming assistant createdAt=T1100
  T=1200: WS tool.executing → tool message createdAt=T1200
  T=1500: WS message.complete → assistant streaming=false
  T=1600: history.updated → invalidateQueries → HTTP refetch starts
  T=1700: HTTP refetch returns [user, assistant, tool] → ALL get createdAt=T1700
  T=1700: mergeMessages() returns [...base(T1700), streamingAssistant(T1100)]
  
  renderItems.sort() by createdAt:
    streamingAssistant(T1100) sorts BEFORE base messages(T1700)
    
  Result: assistant appears ABOVE the user message ← WRONG ORDER
```

---

## 4. Ordering fields: timestamp/id/seq — where they come from

### `createdAt` (the only ordering field used in render)

| Source | Value | Stability |
|---|---|---|
| `createUserMessage()` | `props.createdAt ?? new Date().toISOString()` | Changes on every call if not provided |
| `createAssistantMessage()` | `props.createdAt ?? new Date().toISOString()` | Changes on every call if not provided |
| `createToolMessage()` | `props.createdAt ?? new Date().toISOString()` | Changes on every call if not provided |
| `createUserMessage()` in `sendMessage()` | No `createdAt` provided → `Date.now()` | Different on each send |
| `createAssistantMessage()` in `ensureAssistantPlaceholder()` | No `createdAt` provided → `Date.now()` | Different on each WS chunk |
| `createToolMessage()` in `handleToolExecuting()` | No `createdAt` provided → `Date.now()` | Different on each tool event |
| `toChatMessages()` for HTTP history | No `createdAt` in data → `new Date().toISOString()` | All messages in batch get ~same timestamp |
| `snapshotToGroupInfo()` for groups | `s.created_at \|\| new Date().toISOString()` | From backend if available, else `Date.now()` |

**Key observation**: The backend does NOT provide `created_at` for regular chat messages in the HTTP history endpoint. The `ChatHistoryMessage` struct (types.go:219-229) has no timestamp field. Only group snapshots include `created_at`.

### `id` (not used for ordering, but for dedup/keying)

| Source | Format | Example |
|---|---|---|
| HTTP history | `sessionKey:sha256[:8]` | `native:abc123:a1b2c3d4e5f6a7b8` |
| WS streaming | UUID from backend | `550e8400-e29b-41d4-a716-446655440000` |
| Optimistic user | `temp-user-${Date.now()}` | `temp-user-1714137600000` |
| Tool messages | `tool:${messageId}:${toolCallId}` or `tool-${toolName}-${Date.now()}` | `tool:uuid:call-1` |
| Restored streaming | `restore-${sessionKey}` | `restore-native:abc123` |

**The ID mismatch between WS (UUID) and HTTP (content-hash) is the reason `mergeMessages()` uses position-based matching instead of ID-based matching.**

### `seq` / sequence number

**There is no sequence number field.** The system has no monotonically increasing counter for message ordering. Messages rely entirely on array position (from backend) and `createdAt` timestamps (fabricated by frontend).

### What the backend provides

The `ChatHistoryMessage` struct:
```go
type ChatHistoryMessage struct {
    ID                 string            `json:"id"`
    Role               string            `json:"role"`
    Content            string            `json:"content"`
    ReasoningContent   string            `json:"reasoning_content,omitempty"`
    ToolCalls          []HistoryToolCall `json:"tool_calls,omitempty"`
    ToolCallID         string            `json:"tool_call_id,omitempty"`
    ToolName           string            `json:"tool_name,omitempty"`
    ExcludeFromContext bool              `json:"exclude_from_context,omitempty"`
}
```

**No `created_at`, no `timestamp`, no `seq`.** The backend returns messages in chronological order from `GetSessionHistory()` but provides no ordering metadata.

---

## 5. Race conditions, Date.now(), setTimeout, async issues

### Race condition 1: Timestamp drift between WS and HTTP

**Location**: `chatMessageBuilder.ts:165,181,194` and `messageEventHandlers.ts:69,612,652,718,743`

When a WS event creates a ChatMessage (e.g., streaming assistant at T=1100), and later an HTTP refetch creates new ChatMessage objects for the same logical messages (at T=1700), the merge produces a mixed array where streaming copies have OLDER timestamps than base copies. The `renderItems.sort()` then reorders them incorrectly.

**Trigger**: Any live conversation where `message.complete` fires before the HTTP refetch completes. This is the normal case — `history.updated` triggers `invalidateQueries`, which starts an async HTTP fetch. During the fetch window, the streaming assistant is marked `streaming: false` (T=1500) but still in `streamingMessages`. The HTTP refetch completes at T=1700 with newer timestamps.

### Race condition 2: Optimistic user in base cache

**Location**: `useMessages.ts:143-160` (sendMessage patches queryClient cache) and `useChatHistory.ts:64-222` (mergeMessages)

`sendMessage()` adds the optimistic user to BOTH `streamingMessages` AND the React Query cache (as `baseMessages`). When the HTTP refetch fires, the `queryFn` merges new messages with cached ones (`useChatHistory.ts:273-299`), potentially keeping the optimistic user alongside the real user. The `baseUserCount` then includes the optimistic user, which affects `baseHasCurrentTurn` calculation.

The fix for this is in the `baseUserCountNonOptimistic` calculation (line 78-81), but the optimistic user still leaks timestamps into the base.

### Race condition 3: `handleHistoryUpdated` cleanup timing

**Location**: `messageEventHandlers.ts:258-276`

`handleHistoryUpdated` removes completed assistants from `streamingMessages` and triggers `invalidateQueries`. But `invalidateQueries` is async — the HTTP refetch may not complete before the next render. During this window:
- `streamingMessages` no longer contains the completed assistant
- `baseMessages` still has the OLD cache (before refetch)
- The assistant disappears temporarily

### Race condition 4: `messages.catchup` vs normal flow

**Location**: `messageEventHandlers.ts:278-302`

After WS reconnection, `messages.catchup` replaces the entire base history via `updateChatHistoryFromRaw()`. But if a normal `message.complete` + `history.updated` cycle was in-flight, the catchup can overwrite the results, and the in-flight HTTP refetch can overwrite the catchup data.

### Race condition 5: Stream queue timer vs React batching

**Location**: `useStreamQueues.ts:96-118`

`setInterval(drainQueue, 12ms)` calls `setStreamingMessages` every 12ms per character. React 18 batches state updates, but `setInterval` callbacks run outside of React's batching scope (they're macrotasks). Each character triggers a separate render cycle. With multiple concurrent streams, this creates N timers × (1/0.012) renders/second.

Additionally, when `handleMessageComplete` fires, it calls `clearQueue(messageId)` which `clearInterval`s the timer. But if the queue has undrained characters, they're lost. The `done` flag handling (lines 106-111) tries to mitigate this, but there's a window where `clearQueue` runs before the last `drainQueue` callback.

### Race condition 6: Session switch during streaming

**Location**: `useMessages.ts:178-184` (clearStreaming) and `useAppLogic.ts:370-392` (handleSelectSession)

When switching sessions, `clearStreaming()` is called which clears all queues and streaming messages. But WS events for the old session may still arrive and be processed by `handleEvent` before the session key ref updates. The `isSessionMismatch` check (messageEventHandlers.ts:99-107) mitigates this, but there's a brief window where the ref hasn't updated yet.

### Date.now() usage summary

| Location | Usage | Risk |
|---|---|---|
| `chatMessageBuilder.ts:14` | `createOptimisticUserId()` = `temp-user-${Date.now()}` | ID collision if two messages sent in same ms |
| `chatMessageBuilder.ts:19` | `createToolMessageId()` = `tool-${name}-${Date.now()}` | ID collision for rapid tool calls |
| `chatMessageBuilder.ts:165,181,194` | Default `createdAt` = `new Date().toISOString()` | Timestamp drift between refetches |
| `messageEventHandlers.ts:69,612,652,718,743` | Group `createdAt` fallback = `new Date().toISOString()` | Groups get current time if backend omits `created_at` |
| `useMessages.ts:51` | `debouncedSessionRefresh` uses `Date.now()` for debounce | Correct usage, no ordering impact |

---

## 6. Root cause and recommendation

### Root cause

**The `renderItems.sort()` in `MessageList.tsx` (lines 207-211) sorts by `createdAt` timestamps that are fabricated at conversion time, not at actual message creation time.**

This has two manifestations:

1. **Timestamp drift**: Streaming messages get `createdAt` from the moment WS events arrive (e.g., T=1100). HTTP refetch recreates the same logical messages with new `createdAt` from the refetch time (e.g., T=1700). After `mergeMessages()`, the array contains a mix of old-timestamped streaming copies and new-timestamped base copies. The sort reorders them, breaking the canonical order that `mergeMessages()` carefully preserved.

2. **Unstable sort for same-batch messages**: All messages from a single HTTP refetch get nearly identical `createdAt` values (within ms). JavaScript's `Array.sort()` is not guaranteed to be stable in all engines (though modern engines typically are). Even with stable sort, messages that arrive in different refetches get different timestamps, causing position changes.

3. **Group interleaving depends entirely on timestamps**: Groups and messages are merged into a single `renderItems` array and sorted by `createdAt`. Since group `createdAt` comes from `snapshotToGroupInfo()` (using `s.created_at || new Date().toISOString()`), and message `createdAt` is fabricated, the interleaving is unreliable.

### The fix is NOT to add timestamps to the backend

Even if the backend provided real timestamps, the sort would still be fragile because:
- WS and HTTP use different ID schemes, so the same logical message has different timestamps depending on source
- The sort would need to handle the transition from streaming (WS timestamps) to confirmed (HTTP timestamps) seamlessly

### Recommended fix: Remove the sort, trust array order

**The `mergeMessages()` function already produces messages in the correct canonical order.** The sort in `MessageList` is redundant for messages and harmful because it can reorder them based on fabricated timestamps.

**Change needed in `MessageList.tsx` (lines 207-211)**:

Instead of sorting all `renderItems` by `createdAt`, use a **merge approach** that preserves the message array order and inserts groups at the correct position based on their `createdAt` relative to surrounding messages:

```typescript
// OPTION A: Insert groups into the message array at the right position
// instead of sorting everything by timestamp.
// 
// Since messages are already in correct order, we just need to find
// where each group belongs relative to the messages.

// Build a list of (timestamp, isGroup, index) for groups only,
// then merge them into the message stream at the right positions.
```

Alternatively, a simpler approach:

```typescript
// OPTION B: Don't sort at all. Groups are appended at the end
// of the messages array (they're added via upsertGroup which
// sets createdAt from the backend). If groups need interleaving,
// use message indices rather than timestamps.
```

**Option C (most robust)**: Have the backend return a `seq` (monotonic sequence number) or `created_at` timestamp in the HTTP history response. Use this for ordering instead of frontend-fabricated timestamps. This would require:
1. Backend: Add `created_at` field to `ChatHistoryMessage` struct
2. Frontend: Use the server-provided timestamp in `toChatMessages()`
3. Frontend: Keep the sort but use stable server timestamps

### Secondary fixes needed

1. **`chatMessageBuilder.ts`**: All factory functions (`createUserMessage`, `createAssistantMessage`, `createToolMessage`) should accept and propagate server-provided timestamps when available, only falling back to `Date.now()` for truly new messages.

2. **`useChatHistory.ts:mergeMessages()`**: The position-based matching is correct in principle but fragile. Consider adding a **fuzzy ID matching** layer that tries content-hash first (for when WS and HTTP IDs happen to match), falling back to position-based matching.

3. **`useStreamQueues.ts`**: The 12ms `setInterval` should use `requestAnimationFrame` instead for smoother animation and better React batching. Consider debouncing `setStreamingMessages` to batch multiple character updates per frame.

4. **`messageEventHandlers.ts:handleHistoryUpdated()`**: The cleanup of streaming messages should be deferred until the HTTP refetch completes, not fired immediately. Use `queryClient.fetchQuery()` (blocking) instead of `invalidateQueries()` (async) to ensure the base is updated before streaming copies are removed.

---

## Appendix: File reference summary

| File | Lines | Purpose |
|---|---|---|
| `src/hooks/useMessages.ts` | 1-235 | Streaming state, sendMessage, event delegation |
| `src/hooks/messageEventHandlers.ts` | 1-783 | All WS event handlers |
| `src/hooks/useStreamQueues.ts` | 1-128 | Character animation queues |
| `src/hooks/useChatHistory.ts` | 1-355 | HTTP history, mergeMessages, pagination |
| `src/components/organisms/MessageList.tsx` | 1-330 | Render pipeline with sort |
| `src/lib/chatMessageBuilder.ts` | 1-290 | Message factories, toChatMessages |
| `src/lib/types.ts` | 640-672 | ChatMessage type definition |
| `src/hooks/useAppLogic.ts` | 1-559 | Orchestrates all hooks |
| `src/contexts/AppLogicContext.tsx` | 1-130 | Context provider |
| `src/hooks/messageOrdering.test.ts` | 1-230 | Existing ordering tests |
| `src/hooks/useMessages.test.ts` | 1-865 | Existing event handler tests |
| `pkg/channels/rest_chat.go` | 68-220 | Backend HTTP history handler |
| `pkg/channels/types.go` | 219-229 | ChatHistoryMessage struct (no timestamp) |
| `pkg/channels/native.go` | 564-620 | Backend WS event emission |
