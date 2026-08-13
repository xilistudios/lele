# Plan: Context-Only In-Memory Messages (Evict Excluded, Lazy-Load on Scroll)

**Date:** 2026-08-12
**Status:** Draft
**Related:** `docs/session-sqlite-optimization-plan.md`, `pkg/session/manager.go`, `pkg/store/sessions.go`, `pkg/tui/viewport.go`, `pkg/channels/rest_chat.go`

---

## 1. Problem

After compaction (`/compact`, auto-compaction at 75% of context window, or
intra-loop compaction), old messages are marked `ExcludeFromContext = true`
and persisted with `excluded = 1` in SQLite. They never reach the LLM
(`filterContextMessages` drops them), but they **stay in the in-memory
`session.Messages` slice forever**:

- `PruneExcludedMessages` (`pkg/session/manager.go:1788`) exists but has
  **zero callers** — it is dead code.
- On session load (`loadFromSQLite`, `pkg/session/manager.go:134`), **all**
  messages are loaded from SQLite, including excluded ones.
- The LRU (`maxInMemory: 50`, `evictionTTL: 30m`) evicts whole sessions, but
  an active long-lived session keeps its full excluded history in RAM.

Result: long sessions that compact repeatedly accumulate megabytes of
dead-weight messages in memory (and re-parse them on every load), even
though only the summary + the last few messages are in context.

## 2. Goal

**Only messages that are in context live in memory.** Messages not in
context (excluded) are evicted from RAM after compaction and **loaded on
demand** when a consumer needs them (TUI scroll-up, WebUI history paging).

SQLite remains the source of truth: excluded messages stay persisted
(`excluded = 1`) and are fully recoverable.

### Non-goals

- Changing what the LLM sees (context composition is already correct).
- Compressing/archiving excluded messages in SQLite (the existing
  `PruneExcluded` 10k cap in `saveFullUnlocked` stays as-is).
- WebUI frontend changes beyond what the backend contract change requires
  (the endpoint keeps returning excluded messages; it just reads them from
  SQLite instead of RAM).

## 3. Current Architecture (verified 2026-08-12)

### 3.1 Session memory model

- `Session` (`pkg/session/manager.go:25-52`): `Messages []providers.Message`
  plus dirty-tracking fields: `lastPersistedSeq`, `metaDirty`,
  `msgsAppended`, `msgsModified`, `excludedRange [2]int`, `lastMsgDeleted`.
- **`lastPersistedSeq` is a slice index**, not a stored seq: save paths
  derive `seq = slice index` (`saveIncrementalUnlocked` uses
  `Seq: i` for `i := lastPersistedSeq+1 .. len-1`; `saveExcludedRangeUnlocked`
  uses `Seq: i` for the excluded range). This is the central constraint of
  the design.
- `GetHistoryView` (`manager.go:508`) returns the **live slice** (no copy).
  `GetHistory` (`manager.go:480`) returns a defensive copy.
- Save routing (`saveUnlocked`, `manager.go:1530`):
  `lastPersistedSeq == -1` → full rewrite → `lastMsgDeleted` → delete last →
  `excludedRange` set → range UPDATE → `msgsAppended/msgsModified` →
  incremental INSERT/UPDATE → `metaDirty` → meta only → no-op.
- `SetHistory` (`manager.go:835`) replaces the slice and forces
  `lastPersistedSeq = -1` (full rewrite on next save).

### 3.2 Compaction flow

Three entry points, all converge on the same state transition
(**exclude + persist + save**, summary stored in `session.Summary` metadata):

1. `/compact` → `CompactSession` → `summarizeSession` →
   `summarizeSessionCore` (`pkg/agent/session_manager.go:544`):
   LLM summary → `SetSummary` → `ExcludeOldMessagesFromContext(key, 2)` →
   un-exclude last materialized summary message → `Save` →
   `IncrementCompactionCount`.
2. Auto (`maybeSummarize`, `pkg/agent/session_manager.go:197`) and reactive
   (`llm_caller.go:347`, falls back to `forceCompression`,
   `pkg/agent/session_manager.go:337`).
3. Intra-loop (`pkg/agent/llm_runner.go:829-890`):
   `tools.CompactLoopMessages` → `SetSummary` →
   `ExcludeOldMessagesFromContext(key, 6)` → `Save`.

`ExcludeOldMessagesFromContext` (`pkg/session/manager.go:675`):
- Never excludes index 0 (original user request stays in context).
- Adjusts boundary to keep tool_use/tool_result groups intact.
- Sets `excludedRange = [rangeStart, excludeUpTo)` for the save path.

The summary is **metadata** (`sessions.summary` column), materialized as a
synthetic user message only for LLM calls (`context.go:390`) — except when
`ensureSummaryMaterialized` (`context.go:629`, called from
`llm_runner.go:125` and `llm_caller.go:362`) appends it as a **real
persisted message** via `SetHistory`. Materialized summary messages are
in-context (never excluded) and must survive eviction.

### 3.3 Consumers of the full in-memory history

| Consumer | Needs excluded msgs? | Notes |
|---|---|---|
| LLM context (`BuildMessages`/`filterContextMessages`) | No | Already filters excluded |
| `EstimateTokens`, `/status`, `maybeSummarize` | No | Already skips excluded |
| Tail readers (`lastAssistantResponse`, clipboard, streaming cleanup) | No | Read only the tail |
| `/compact` threshold checks (`len(history) <= 4`) | Count only | 3 sites: `agent_providable.go:379`, `command_handler.go:188`, `message_processor.go:321` |
| TUI render (`viewport.go:82`, `buildRenderedHistoryLines`) | **Yes (display)** | Renders full history incl. excluded; lazy window `renderStartIdx` |
| WebUI history (`rest_chat.go:102`, `GetSessionHistory`) | **Yes (display)** | Cursor pagination by content hash; exposes `ExcludeFromContext` |
| WebUI session list (`rest_chat.go:264`) | Count only | Message count per session |
| `resetAgentSession` backup (`loop.go:918`) | No | Backup/restore around `/clear` |

### 3.4 Store facts

- `session_messages` has `excluded INTEGER` and `UNIQUE(session_key, seq)`;
  FK `ON DELETE CASCADE` with `foreign_keys=1` enabled.
- `LoadMessages`: `SELECT message FROM session_messages WHERE session_key = ?
  ORDER BY seq ASC` — no pagination, no excluded filter.
- `PruneExcluded(key, keepCount)` (`store/sessions.go:461`) deletes oldest
  excluded rows; only called from `saveFullUnlocked` with cap 10000.
- No range/paginated load exists yet.

## 4. Design

### 4.1 Core model: eviction gap + absolute seqs

Evicting excluded messages from the middle of the slice breaks the
"seq == slice index" invariant that incremental saves rely on. The design
makes the gap explicit:

```
SQLite:   [excluded ...][excluded][msg0'][msg1'][msg2']...   (seqs 0..N-1, contiguous, immutable)
                    ↑ gap (evicted)      ↑ firstInMemorySeq
Memory:                              [msg0'][msg1'][msg2']...  (slice index 0..M-1)
```

New `Session` fields:

```go
// firstInMemorySeq is the SQLite seq of slice element 0.
// 0 = no eviction gap (slice index == seq, legacy behavior).
// > 0 = messages with seq < firstInMemorySeq were evicted from memory
//       (they remain in SQLite with excluded = 1).
firstInMemorySeq int

// evictedTotal is the number of messages currently persisted in SQLite
// but not present in the in-memory slice (evicted after compaction).
// Used for display counters ("N earlier messages") and total counts.
evictedTotal int
```

**Invariant:** for every save path, `seq = firstInMemorySeq + sliceIndex`.

- `saveIncrementalUnlocked`: `Seq: firstInMemorySeq + i`.
- `saveExcludedRangeUnlocked`: `Seq: firstInMemorySeq + i`.
- `saveFullUnlocked`: rewrite uses slice order; after rewrite,
  `firstInMemorySeq = 0` and seqs are re-based (full rewrite is the only
  path that may renumber).
- `saveDeleteLastUnlocked`: unchanged (deletes max seq).

**Load:** `loadFromSQLite` keeps loading all messages (simple, correct) and
sets `firstInMemorySeq = 0`, `evictedTotal = 0`. A follow-up phase (7) adds
an in-context-only load for cold sessions.

**Eviction is memory-only.** No `DELETE` from SQLite at eviction time —
excluded rows stay for display and reload.

### 4.2 Eviction

Rewrite `PruneExcludedMessages` into a gap-aware `EvictExcludedMessages`:

1. Persist first (caller contract: call after `Save` succeeded).
2. Scan the slice; keep messages with `!ExcludeFromContext`.
3. Because `ExcludeOldMessagesFromContext` excludes a **contiguous prefix**
   (index 1..excludeUpTo, never index 0, tool groups kept intact), the kept
   set would otherwise be a contiguous suffix **plus index 0**. Keeping index
   0 in memory would create a non-contiguous gap that breaks the
   `seq = firstInMemorySeq + sliceIndex` invariant (index 0 has abs seq 0 but
   slice index 0). Decision: **fold index 0's content into the session
   summary and evict it too**, so the in-memory slice is a fully contiguous
   suffix and `firstInMemorySeq` = number of evicted rows exactly. Index 0 is
   normally the original user request; folding it into the summary (which is
   kept in context and persisted in metadata) preserves that context. The
   evicted set is the whole `[0..evictUpTo)` prefix (index 0 + excluded run).
4. Set `firstInMemorySeq += evicted`, `evictedTotal += evicted`, clear dirty
   flags, `lastPersistedSeq = len(slice) - 1` (relative accounting
   continues). Index 0 keeps its stored `excluded=0` flag in SQLite so a
   reload/lazy-load restores it as in-context.
5. Log with session key, pruned count, remaining count.

Call sites (after `Save` succeeds, guarded by `err == nil`):

- `summarizeSessionCore` (after `Save` + `IncrementCompactionCount`)
- `forceCompression` (after `Save` + `IncrementCompactionCount`)
- Intra-loop compaction sync block in `llm_runner.go` (after `Save`)

Config escape hatch: `session.evict_excluded_from_memory` (default `true`,
env `LELE_EVICT_EXCLUDED`). When disabled, behavior is identical to today.

### 4.3 Lazy-load API

New `SessionManager` method:

```go
// LoadEvictedMessages re-inserts evicted (excluded) messages from SQLite
// back into the in-memory slice (before the current first in-memory
// message), restoring full display history. Idempotent: no-op when
// evictedTotal == 0. Returns the number of messages loaded.
func (sm *SessionManager) LoadEvictedMessages(key string) int
```

Implementation:
1. If `evictedTotal == 0`, return 0.
2. New store method `LoadMessagesBeforeSeq(sessionKey, beforeSeq)` →
   `SELECT message FROM session_messages WHERE session_key = ? AND seq < ?
   ORDER BY seq ASC`.
3. Prepend to `session.Messages`, set `firstInMemorySeq = 0`,
   `evictedTotal = 0`, `lastPersistedSeq = len(slice) - 1`.
4. No persistence needed (data already in SQLite, flags unchanged).

Exposed through the layers:
- `SessionManager.LoadEvictedMessages`
- `AgentProvidable` interface + impl (`pkg/agent/agent_providable.go`)
- `channels.AgentInterface` (`pkg/channels/agent_interface.go`)

### 4.4 TUI integration

The TUI already has a lazy render window (`renderStartIdx`,
`maybeExpandRenderWindow`, batch 50). Extend it:

1. When the render window reaches the top of the in-memory slice
   (`renderStartIdx <= 0`) **and** the session reports evicted messages
   (`evictedTotal > 0`, exposed via a new providable method
   `GetEvictedMessageCount(sessionKey) int`), call
   `LoadEvictedMessages` once, then re-render. The existing
   fingerprint-based render cache makes the re-render cheap (only newly
   loaded messages go through glamour).
2. The "↑ N earlier messages" header (`viewport.go:365`) must count
   `renderStartIdx + evictedNotYetLoaded` instead of just `renderStartIdx`.
3. Scroll position compensation already exists in
   `maybeExpandRenderWindow` (YOffset adjust) — reuse the same pattern.

Batching (optional optimization): instead of loading all evicted messages at
once, add `LoadMessagesSeqRange(sessionKey, fromSeq, toSeq)` and load in
batches of ~50 to mirror the render window. Default implementation loads all
at once (SQLite reads are fast; the render cache is the bottleneck, not the
query). Batched loading can be added later without API changes.

### 4.5 WebUI / REST integration

`handleChatHistory` (`rest_chat.go:88`) currently reads
`GetSessionHistory` (in-memory). Two options:

- **Option A (chosen):** call `LoadEvictedMessages` before reading history
  when the endpoint needs older pages (i.e., when `before_id` pagination
  walks past the in-memory head, or simply always for this endpoint). The
  endpoint semantics, cursor IDs (content-hash based, position-independent)
  and `ExcludeFromContext` exposure stay unchanged.
- Option B (rejected): serve history directly from SQLite. Cleaner long-term
  but duplicates the message-filtering/ID logic and changes the endpoint's
  data source mid-flight.

Session list count (`rest_chat.go:264`): use
`GetEvictedMessageCount + len(history)` (or a new
`GetTotalMessageCount(sessionKey)` on the providable) so sidebar counts
don't shrink after eviction.

### 4.6 Counters and threshold checks

Add to `SessionManager`:

```go
// TotalMessageCount returns len(Messages) + evictedTotal.
func (sm *SessionManager) TotalMessageCount(key string) int
```

Replace the three `len(history) <= 4` compaction guards with
`TotalMessageCount(...) <= 4` so `/compact` availability is unaffected by
eviction. (The guards exist to avoid compacting tiny sessions; eviction
should not make a compactable session look empty.)

### 4.7 Cold-load optimization (Phase 7, optional)

`loadFromSQLite` currently loads all messages. With eviction in place, a
cold session reload re-inflates excluded messages into RAM until the next
compaction. Optimize:

- `LoadMessagesFiltered(sessionKey, excluded bool)` →
  `SELECT message FROM session_messages WHERE session_key = ? AND excluded = ?
  ORDER BY seq ASC`
- `loadFromSQLite` loads only `excluded = 0` messages, sets
  `firstInMemorySeq` to the seq of the first loaded row (needs a parallel
  `SELECT MIN(seq) ... WHERE excluded = 0` or `SELECT seq, message` variant),
  and `evictedTotal` from `SELECT COUNT(*) WHERE excluded = 1`.
- Exception: index-0 semantics — the first message is never excluded, so the
  in-context load always includes it.

This phase is independent and can ship later.

## 5. Phases & Atomic Tasks

Each task is one focused change with its own tests. Order matters within a
phase; phases 1→2→3 are the critical path. Phases 4-7 are independent once
phase 2 lands.

### Phase 1 — Store layer (foundation)

**Task 1.1: Range/predicate load methods on `SessionRepo`**
- File: `pkg/store/sessions.go`
- Add `LoadMessagesBeforeSeq(sessionKey string, beforeSeq int) ([]string, error)`.
- Add `LoadMessagesWithSeq(sessionKey string) ([]MessageRowFull, error)`
  returning `{Seq int, JSON string, Excluded bool}` (needed by phase 7 and
  for `firstInMemorySeq` recovery after crash/reload).
- Add `CountExcludedMessages(sessionKey string) (int, error)`.
- Tests: `pkg/store/sessions_test.go` — round-trip insert/evict-sim/load
  before seq, ordering, empty results.

### Phase 2 — Session manager: gap-aware accounting + eviction + lazy load

**Task 2.1: Gap-aware seq accounting in save paths**
- File: `pkg/session/manager.go`
- Add `firstInMemorySeq`, `evictedTotal` fields to `Session` (not
  JSON-persisted; they are derived from SQLite state on load).
- `saveIncrementalUnlocked`: `Seq = firstInMemorySeq + i`.
- `saveExcludedRangeUnlocked`: `Seq = firstInMemorySeq + i`.
- `saveFullUnlocked`: after `ReplaceMessages`, reset `firstInMemorySeq = 0`
  (full rewrite renumbers from 0).
- With `firstInMemorySeq == 0` (no eviction yet), behavior is bit-identical
  to today.
- Tests: unit tests asserting seq values passed to a fake store for both
  `firstInMemorySeq == 0` and `> 0`.

**Task 2.2: `EvictExcludedMessages` (rewrite of `PruneExcludedMessages`)**
- File: `pkg/session/manager.go`
- Replace `PruneExcludedMessages` with `EvictExcludedMessages(key) int`:
  removes excluded messages, keeps index 0 and the in-context suffix, sets
  `firstInMemorySeq` (seq of first kept message = number of evicted rows
  before it), increments `evictedTotal`, resets dirty flags, updates
  `lastPersistedSeq` to the new slice-relative end.
- Precondition documented: caller must have saved the excluded flags first.
- Tests: eviction shape (contiguous prefix excluded), index-0 preservation,
  idempotency (second call is a no-op), dirty-flag state after eviction
  (next `Save` must be a no-op, NOT a full rewrite).

**Task 2.3: `LoadEvictedMessages` + `TotalMessageCount` + `GetEvictedMessageCount`**
- File: `pkg/session/manager.go`
- Implement as in §4.3/§4.6 using `LoadMessagesBeforeSeq`.
- Tests: evict → load round-trip restores identical slice (order, flags,
  content); `TotalMessageCount` stable across eviction; load is idempotent.

**Task 2.4: Reload correctness (`loadFromSQLite`)**
- File: `pkg/session/manager.go`
- On load, compute `firstInMemorySeq`/`evictedTotal` correctly for the
  current all-messages load (both 0). Add a regression test that a session
  saved post-eviction reloads with correct seqs (no duplicate/missing rows):
  save → evict → append → save → evict session from map → reload → verify
  slice + SQLite rows.

### Phase 3 — Wiring eviction into compaction

**Task 3.1: Config flag**
- Files: `pkg/config/config.go`, `pkg/config/document_types.go`
- `Session.EvictExcludedFromMemory bool` (default true) + env override
  `LELE_EVICT_EXCLUDED` + accessor `EvictExcludedFromMemory()`.
- Tests: default, explicit false, env override.

**Task 3.2: Call `EvictExcludedMessages` after compaction saves**
- Files: `pkg/agent/session_manager.go` (`summarizeSessionCore`,
  `forceCompression`), `pkg/agent/llm_runner.go` (intra-loop sync block).
- Guard with the config flag; call only when `Save` returned nil error.
- Tests: fake-session test asserting slice shrinks after
  `summarizeSessionCore` (reuse existing compaction test scaffolding);
  regression: LLM context after eviction contains summary + kept messages
  only (no behavior change vs pre-eviction, since excluded never reached
  the LLM anyway).

### Phase 4 — Interface plumbing

**Task 4.1: Expose lazy-load + counters through providable/interfaces**
- Files: `pkg/agent/agent_providable.go`, `pkg/channels/agent_interface.go`
- Add `LoadEvictedMessages(sessionKey) int`,
  `GetEvictedMessageCount(sessionKey) int`,
  `GetTotalMessageCount(sessionKey) int` to `AgentProvidable` and
  `channels.AgentInterface` (+ impls delegating to `SessionManager`).
- Tests: delegation tests with a stub session manager.

**Task 4.2: Fix compaction threshold guards**
- Files: `pkg/agent/agent_providable.go:379`, `pkg/agent/command_handler.go:188`,
  `pkg/agent/message_processor.go:321`
- Replace `len(history) <= 4` with `TotalMessageCount(...) <= 4`.
- Tests: session with evicted history still passes the guard.

### Phase 5 — TUI lazy-load on scroll

**Task 5.1: Expand render window through eviction boundary**
- Files: `pkg/tui/viewport.go`, `pkg/tui/types.go`, `pkg/tui/handlers.go`
- In `maybeExpandRenderWindow`: when `renderStartIdx <= 0` and
  `GetEvictedMessageCount(currentKey) > 0`, call `LoadEvictedMessages`,
  set `renderStartIdx` to the number of newly loaded messages (so the
  window expands in the usual 50-message batches from there), invalidate
  `renderedBaseValid`, compensate `YOffset`.
- Header count: `renderStartIdx + remainingEvicted`.
- Reset path: session switch already resets `renderStartIdx = -1`; ensure
  `evictedTotal` is re-read per session.
- Tests: TUI-level tests with a stub providable — expansion triggers load
  exactly once, header count correct, no load when nothing evicted.

### Phase 6 — WebUI backend

**Task 6.1: History endpoint loads evicted messages**
- File: `pkg/channels/rest_chat.go`
- `handleChatHistory`: if `GetEvictedMessageCount > 0`, call
  `LoadEvictedMessages` before `GetSessionHistory` (keeps cursor semantics
  and content-hash IDs intact).
- `handleChatSessions`: message count = in-memory count + evicted count.
- Tests: endpoint test with evicted session returns full history; counts
  stable.

### Phase 7 — Cold-load optimization (optional, independent)

**Task 7.1: In-context-only cold load**
- Files: `pkg/store/sessions.go` (`LoadMessagesFiltered` /
  `LoadMessagesWithSeq`), `pkg/session/manager.go` (`loadFromSQLite`)
- Load only `excluded = 0` rows on cold load; set `firstInMemorySeq` from
  the MIN loaded seq; `evictedTotal` from `CountExcludedMessages`.
- Tests: cold load of a compacted session has only in-context messages;
  `LoadEvictedMessages` restores the rest; seq accounting survives restart.

## 6. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Seq desync corrupts SQLite rows (duplicate/missing) | Phase 2.1 tests with fake store asserting exact seqs; invariant `seq = firstInMemorySeq + idx` enforced in one helper `seqForIndex(i)` |
| `SetHistory`/`TruncateHistory` full rewrite after eviction renumbers seqs | Allowed: full rewrite is atomic (`ReplaceMessages` in tx); after it, `firstInMemorySeq = 0`. `ensureSummaryMaterialized` uses `SetHistory` — verify in 2.4 test that append-after-eviction + materialization stays consistent |
| Live-slice consumers (`GetHistoryView`) race with eviction/prepend | Eviction and `LoadEvictedMessages` run under `sm.mu`; the slice is only replaced (never reallocated under callers) — same contract as today. TUI render reads happen between frames; a one-frame stale view is acceptable (next tick re-renders) |
| `resetAgentSession` backup (`loop.go:918`) restores without evicted msgs | Acceptable: `/clear` wipes the session anyway; backup only guards a failed save. Document it |
| Index-0 special case (never excluded) complicates gap shape | Keep index 0 in memory always (one small message). Evicted set stays a contiguous prefix → single `firstInMemorySeq` suffices |
| WebUI/TUI see fewer messages after eviction | Both get explicit lazy-load paths (phases 5-6); counters use `TotalMessageCount` |
| Behavior change behind a flag | `EvictExcludedFromMemory` config (default on) allows instant rollback without code revert |
| Crash between exclude-save and eviction | Eviction is memory-only and idempotent; after restart the session reloads from SQLite (all messages) and the next compaction evicts again. No persistent state depends on eviction |

## 7. Test Strategy

- **Unit (store):** range loads, counts, ordering (task 1.1).
- **Unit (session):** seq accounting for all 5 save paths with
  `firstInMemorySeq ∈ {0, >0}`; evict/load round-trips; idempotency;
  reload correctness (tasks 2.1-2.4).
- **Unit (agent):** eviction wired after each compaction path; LLM context
  unchanged; threshold guards (tasks 3.2, 4.2).
- **TUI:** window expansion across eviction boundary; header counts
  (task 5.1).
- **REST:** history endpoint + session counts with evicted sessions
  (task 6.1).
- **Integration/regression:** full `go test ./...` green; manual smoke:
  long session → `/compact` → scroll up in TUI → old messages load;
  WebUI history shows full conversation; restart → same behavior.

## 8. Migration & Rollout

- No schema migration needed (no new columns).
- Existing sessions: nothing changes until their next compaction, which
  triggers eviction. Fully backward compatible.
- Rollback: set `session.evict_excluded_from_memory: false` (or
  `LELE_EVICT_EXCLUDED=false`).
- Phase 7 changes cold-load shape but is toggleable by reverting one commit;
  data on disk is identical either way.

## 9. Out of Scope / Future

- Batched lazy-load (`LoadMessagesSeqRange`) if all-at-once loading ever
  becomes measurable (sessions with 10k+ excluded messages).
- Serving WebUI history directly from SQLite (removes the in-memory
  dependency entirely) — larger refactor, candidate for a separate plan.
- Archiving excluded messages to compressed blobs after N compactions.
