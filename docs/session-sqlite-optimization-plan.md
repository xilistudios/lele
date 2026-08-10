# Session Manager: SQLite Optimization & Dead Code Cleanup

## Status: ✅ IMPLEMENTED (2026-08-10)

## Context

Sessions were migrated from JSON files to SQLite (`feat/sqlite-sessions`), but the
SessionManager still uses a "full rewrite" persistence strategy: every `saveUnlocked()`
call does `DELETE all messages + INSERT all messages` via `ReplaceMessages()`. This is
O(n) per save and happens very frequently (every 200ms during streaming, on every metadata
change, on eviction). The `SessionRepo` already has `InsertMessage`, `UpdateMessage`,
`UpdateMessagesExcluded`, and `DeleteMessagesFrom` methods that are **never used**.

Additionally, several dead-code artifacts from the JSON era remain (unused constants,
stale comments, deprecated methods).

## Findings

### Dead Code / Stale JSON References

| Item | Location | Status |
|------|----------|--------|
| `const indexFileName = "_index.json"` | manager.go:20 | Unused constant |
| Stale comments for `loadIndex`, `saveIndexUnlocked`, `loadSessionMetadataParallel` | manager.go:822-827 | Empty doc stubs for deleted functions |
| `NewSessionManager(storage string)` param | manager.go:70 | `storage` param is never stored or used |
| `StoragePath()` method | manager.go:99 | Returns "", never called externally |
| `loadSessionMetadata()` wrapper | manager.go:792 | Thin wrapper that just calls `loadSessionMetadataFromSQLite()` |

### Performance: Full-Rewrite on Every Save

`saveUnlocked()` (line 1262) marshals ALL messages to JSON, then calls `ReplaceMessages()`
which does `DELETE FROM session_messages WHERE session_key = ?` + INSERT each row. This
happens on:

- **Streaming flush** (`maybeFlushStream`) — every 200ms during active streaming
- **Metadata setters** — `SetName`, `SetModel`, `SetMode`, `SetVerboseMode`,
  `SetVerboseLevel`, `SetThinkingLevel` — only metadata changes, messages unchanged
- **Eviction** (`saveForEviction`) — full rewrite before dropping from memory
- **Explicit `Save()`** — called by various consumers

The `lastPersistedSeq` field already exists and is maintained (set to `len(Messages)-1`
after each save) but is **never used** to do incremental appends.

### Unused SessionRepo Methods

These methods exist in `store/sessions.go` and are ready to use but are never called:

- `InsertMessage(sessionKey, seq, role, content, json, excluded)` — append single message
- `UpdateMessage(sessionKey, seq, role, content, json, excluded)` — update single message
- `UpdateMessagesExcluded(sessionKey, fromSeq, toSeq, excluded)` — batch exclude flag update
- `DeleteMessagesFrom(sessionKey, fromSeq)` — truncate from seq onward

### Missing Dirty Tracking

All in-memory mutations set `session.Updated = time.Now()` and call `saveUnlocked()`,
but there's no distinction between:
- "only metadata changed" → need `UpsertSession` only
- "messages appended" → need `InsertMessage` for new ones
- "messages modified" (streaming) → need `UpdateMessage` for the last one
- "messages deleted/reordered" → need `ReplaceMessages` (full rewrite)

---

## Plan

### Phase 1: Dead Code Cleanup (low risk)

**File:** `pkg/session/manager.go`

1. **Remove `const indexFileName`** (line 20) — unused
2. **Remove stale doc comments** for `loadIndex`, `saveIndexUnlocked`, `loadSessionMetadataParallel` (lines 822-827)
3. **Remove `storage` parameter** from `NewSessionManager` — change signature to `NewSessionManager() *SessionManager`. Update callers:
   - `pkg/agent/instance.go:202` — `session.NewSessionManager(sessionsDir)` → `session.NewSessionManager()`
   - `pkg/agent/loop.go:342` — `session.NewSessionManager(unifiedSessionsDir)` → `session.NewSessionManager()`
4. **Remove `StoragePath()` method** (lines 98-101) — returns "", never called
5. **Inline `loadSessionMetadata()`** into `ensureLoaded()` — remove the extra wrapper. The `ensureLoaded` already checks `sm.store != nil` before calling.

### Phase 2: Incremental Message Saves (high impact)

**File:** `pkg/session/manager.go`

Introduce a `saveMode` concept to distinguish save scopes:

```go
type saveMode int

const (
    saveMetaOnly   saveMode = iota // only UpsertSession (metadata changed)
    saveIncremental                // UpsertSession + InsertMessage for new msgs + UpdateMessage for modified
    saveFull                       // UpsertSession + ReplaceMessages (nuclear option)
)
```

1. **Add `saveMetaOnlyUnlocked(key)` method** — calls only `UpsertSession()`, skips messages entirely. Used by metadata-only setters.

2. **Add `saveIncrementalUnlocked(key)` method** — uses `lastPersistedSeq`:
   - If `lastPersistedSeq == -1` (new session): full insert via `ReplaceMessages`
   - If messages were appended (new `lastPersistedSeq < len-1`): call `InsertMessage` for each new message
   - If last message was modified in-place (streaming): call `UpdateMessage` for that seq
   - Always call `UpsertSession` for metadata
   - Update `lastPersistedSeq` at end

3. **Refactor `saveUnlocked(key)` to auto-detect mode**:
   - Track `messagesDirty` and `metaDirty` flags on Session
   - `saveUnlocked` checks flags: if only meta → `saveMetaOnly`, if messages added/changed → `saveIncremental`
   - Keep `saveFull` path available for `TruncateHistory` / `SetHistory` / `ExcludeOldMessagesFromContext`

4. **Update callers**:
   - `SetName`, `SetModel`, `SetMode`, `SetVerboseMode`, `SetVerboseLevel`, `SetThinkingLevel` → set `metaDirty`, call `saveMetaOnlyUnlocked`
   - `AddFullMessage` → set `messagesDirty` (append), next save uses incremental
   - `AppendAssistantChunk` / `flushStreamNow` → update only the streaming message via `UpdateMessage`
   - `TruncateHistory`, `SetHistory` → set `messagesDirty` (reorder), force `saveFull`

### Phase 3: Streaming Save Optimization (high impact)

**File:** `pkg/session/manager.go`

The streaming path (`maybeFlushStream` → `saveUnlocked`) is the hottest path.
Current: every 200ms, DELETE all + INSERT all messages.

Optimized `maybeFlushStream`:
1. Call `UpsertSession` (metadata: updated_at, etc.)
2. For the streaming message (always the last one): call `UpdateMessage` with the current content
3. If new messages were added since last flush: call `InsertMessage` for each

This changes streaming persistence from O(n) to O(1) per flush.

### Phase 4: Dirty Flags on Session

**File:** `pkg/session/manager.go` (Session struct)

Add internal tracking fields (not persisted):

```go
type Session struct {
    // ... existing fields ...
    metaDirty    bool // metadata changed since last save
    msgsAppended int  // number of messages appended since lastPersistedSeq
    msgsModified bool // existing messages modified in-place (e.g., streaming update)
}
```

These are set by the mutation methods and cleared after a successful save.

### Phase 5: Tests

**File:** `pkg/session/manager_sqlite_test.go`

1. **Incremental append test**: add 3 messages, save, add 2 more, save → verify all 5 in DB, verify only 2 INSERTs happened (not 5)
2. **Metadata-only save test**: change model/name → verify messages table untouched
3. **Streaming optimization test**: simulate streaming with multiple flushes → verify UpdateMessage used, not ReplaceMessages
4. **Full rewrite test**: TruncateHistory → verify full ReplaceMessages used
5. **Dirty flag test**: verify flags are set/cleared correctly

---

## File Changes Summary

| File | Changes |
|------|---------|
| `pkg/session/manager.go` | Remove dead code, add save modes, incremental saves, dirty flags, optimize streaming |
| `pkg/agent/instance.go` | Update `NewSessionManager()` call (remove arg) |
| `pkg/agent/loop.go` | Update `NewSessionManager()` call (remove arg) |
| `pkg/session/manager_sqlite_test.go` | New tests for incremental saves, metadata-only, streaming |

## Verification

```bash
# Run session tests
go test ./pkg/session/ -v -count=1

# Run full test suite
go test ./... -count=1

# Verify no remaining JSON file references
grep -rn "_index\.json\|loadIndex\|saveIndex\|loadSessionMetadataParallel" pkg/session/
```

## Out of Scope

- `migrate-storage` CLI (`cmd/lele/migrate_storage.go`) — legitimately reads JSON for migration, keep as-is
- `json` tags on `Session` struct — still needed for API serialization (REST/TUI)
- `encoding/json` import — still needed for `json.Marshal(msg)` when storing messages as JSON strings in SQLite
