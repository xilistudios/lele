# TUI long-chat performance — results (post-fix)

**What this page is:** the *after* column of
[`docs/perf/tui-long-chat-baseline.md`](tui-long-chat-baseline.md). That page is frozen
at the pre-fix measurement; this one was measured on the fixed tree with the same
command, harness and machine, and is the page to read for "how fast is the TUI now".

| | |
|---|---|
| Date | 2026-09-27 |
| Branch | `perf/tui-long-chat` (base `c97d78d`, working tree with the whole fix series, uncommitted) |
| Machine | Intel(R) Core™ i5-10300H @ 2.50GHz, 8 cores, 15 GiB RAM, openSUSE Tumbleweed (kernel 7.2.6-1-default) |
| Go | go1.26.6 linux/amd64 |
| Harness | `pkg/tui/scale_bench_test.go`, `pkg/tui/history_count_memo_test.go`, `pkg/session/history_bench_test.go` |
| Command | `make bench` → `go test -run '^$' -bench 'History|Scale|Subagents|MouseMotion|Fingerprint' -benchmem ./pkg/tui/ ./pkg/session/` |
| Conditions | one run with `-benchtime=1s` (default). **The machine is shared with parallel workstreams**, and the baseline page already documents the resulting ±10 % noise (it observed 15 ms instead of 4.3 ms for one row under contention). The "after" column is that single run; a second pass (`-count=3`) was made to characterise the small-chat rows and the un-optimised rows, and both are noted where they differ. **A third, full `make bench` was run at gate time on the frozen tree** and reproduced the whole after column: every headline row within 1 %, the worst deviation 11 % on the deliberately untouched `BenchmarkGetSessionSubagents` rows, package times 99.0 s / 9.3 s (the numeric comparison is in the gate evidence, `/tmp/cmp_a.txt` vs `/tmp/cmp_b.txt`) |
| Package times | `pkg/tui` 100.2 s, `pkg/session` 9.5 s — **not comparable** to the baseline's 64.9 s / 4.6 s: the harness grew (count memo, sessions refresh, per-half listing reads, subagent refresh, the split fingerprint) |

## 1. Symptom and root causes

**Symptom.** The TUI was reported as slow in long chats (≈6000 messages) with hundreds
of accumulated finished subagents and continuous streaming: every frame — an idle
spinner tick, an unconsumed mouse-motion cell, a single streamed chunk — cost 3.2 ms at
6000 messages and allocated 4.5 MB, and only ~50 lines were visible.

Four root causes were identified and fixed:

1. **`GetHistoryView` copied the whole history, three times per frame.** It is a
   read-only view (`pkg/agent/agent_providable.go` contract) but it copied the full
   slice under the read lock on *every* call — 328 µs and 1.16 MB at 6000 messages per
   call, ≈1 ms and ≈3.5 MB per frame, ~31 % of frame time and ~78 % of its allocation.
   Three call sites per `View()`: the viewport rebuild, and twice through
   `getTokenUsage → getHistoryMessageCount` (bottom bar + sidebar).
2. **`View()` rebuilt the viewport on every frame and read the history several times
   per frame.** bubbletea calls `View()` after *every* `Update` — every chunk, tick,
   mouse move and keypress — and `View()` reached `updateViewport()` unconditionally,
   which defeated the 32 ms streaming throttle and put O(history) work (plus repeated
   `GetHistoryView` reads and an O(n) role scan per consumer) on every frame.
3. **Subagent/session listing and post-compaction invalidation were O(n) + SQLite on the
   render path.** The sidebar and status line called `GetSessionSubagents` from `View()`
   (2.0 ms / 414 KB at 200 subagents, 6.9 ms / 1.2 MB at 500 cold ones), `reloadSessions`
   re-walked `ListSessions` + a `GROUP BY` scan of the message table on each of its ~14
   call sites (444 µs / 179 KB at 200 sessions, fired per finished turn, per tab and per
   sidebar click), and `/compact` wiped the per-message render cache and forced a full
   glamour re-render of the whole window (21.7 ms / 8.9 MB per compaction result).
4. **Persistence did its I/O under the write lock.** Every `GetHistoryView` takes the
   read lock, so each compaction (full rewrite, eviction-boundary write) and each metadata
   write blocked the frames for the whole SQLite round trip + JSON encode/decode.

## 2. Measured: baseline → final

"Before" is the frozen value from `docs/perf/tui-long-chat-baseline.md`; "after" is this
run. Ratios are `after / before` (lower is better). `B/op` is what the frame allocates:
the story there is not a ratio but the *shape* — the frame no longer allocates the whole
history, so bytes stop growing with the chat (4.47 MB → 0.96 MB at 6000 messages, a value
essentially identical to the 40-message row).

### 2.1 History reads — `pkg/session`

| Benchmark | before ns/op | after ns/op | ratio | before B/op | after B/op |
|---|---:|---:|---:|---:|---:|
| `BenchmarkGetHistoryView/msgs40` | 1 726 | 33.9 | **0.020×** | 8 192 | 0 |
| `BenchmarkGetHistoryView/msgs2000` | 100 187 | 32.3 | **0.0003×** | 385 024 | 0 |
| `BenchmarkGetHistoryView/msgs6000` | 327 629 | 34.2 | **0.0001×** | 1 155 075 | 0 |

The copy is gone on the hot path: O(1), zero allocations, flat in the history length
(33.9 ns at 40 messages, 34.2 ns at 6000). Its cost moved to the writers, which copy once
per *structural* mutation instead of once per *read*.

### 2.2 Frames — `pkg/tui`

| Benchmark | before ns/op | after ns/op | ratio | before B/op | after B/op |
|---|---:|---:|---:|---:|---:|
| `BenchmarkScaleView/view_msgs40` | 2 031 197 | 2 019 116 | 0.99× | 994 205 | 969 840 |
| `BenchmarkScaleView/view_msgs400` | 2 053 130 | 2 026 759 | 0.99× | 1 216 072 | 964 702 |
| `BenchmarkScaleView/view_msgs2000` | 2 275 744 | 1 932 321 | **0.85×** | 2 138 152 | 963 124 |
| `BenchmarkScaleView/view_msgs6000` | 3 163 815 | 1 918 672 | **0.61×** | 4 467 982 | 961 443 |
| `BenchmarkScaleStreamUpdate/stream_update_msgs40` | 1 942 793 | 2 055 159 | 1.06× | 997 407 | 969 347 |
| `BenchmarkScaleStreamUpdate/stream_update_msgs400` | 2 051 858 | 2 048 295 | 1.00× | 1 218 204 | 968 208 |
| `BenchmarkScaleStreamUpdate/stream_update_msgs2000` | 2 361 769 | 1 944 884 | **0.82×** | 2 138 850 | 967 279 |
| `BenchmarkScaleStreamUpdate/stream_update_msgs6000` | 3 227 007 | 1 888 822 | **0.59×** | 4 464 810 | 965 702 |
| `BenchmarkScaleRebuild/newmsg_msgs40` | 2 459 118 | 1 857 385 | **0.76×** | 1 859 332 | 912 714 |
| `BenchmarkScaleRebuild/newmsg_msgs400` | 2 649 108 | 1 869 068 | **0.71×** | 2 166 315 | 913 682 |
| `BenchmarkScaleRebuild/newmsg_msgs2000` | 3 076 183 | 1 959 663 | **0.64×** | 3 290 534 | 920 426 |
| `BenchmarkScaleRebuild/newmsg_msgs6000` | 4 579 141 | 2 030 983 | **0.44×** | 6 191 525 | 932 255 |
| `BenchmarkMouseMotion/motion_msgs40` | 1 923 544 | 1 974 358 | 1.03× | 986 569 | 958 200 |
| `BenchmarkMouseMotion/motion_msgs6000` | 2 849 936 | 1 909 192 | **0.67×** | 4 445 740 | 956 935 |

**The frame is now flat in the chat length.** At 6000 messages the idle/streaming/motion
frame costs 1.89–1.92 ms instead of 2.85–3.23 ms, and the 40-message frame costs the same
(~1.9–2.0 ms). The deltas on the 40-message rows (1.03–1.06×) are inside the documented
noise: the same rows re-measured with `-count=3` gave `view_msgs40` 1 944 163–1 966 151
ns/op, `stream_update_msgs40` 1 927 452–1 989 186 and `motion_msgs40` 1 974 740–2 108 658,
i.e. the old value. Small chats were not made slower and have nothing to gain — their cost
is the fixed per-frame paint (see follow-up 4).

`allocs/op` is likewise flat (~3.1 k at every size, 3 143 → 3 116 at 40 messages and
3 150 → 3 118 at 6000): the allocation *count* is dominated by the same fixed paint
(chrome, borders, sidebar rows) and was never the growth term. The growth term was bytes,
and it is gone.

### 2.3 Subagents and session listing — `pkg/tui`

| Benchmark | before ns/op | after ns/op | ratio | before B/op | after B/op |
|---|---:|---:|---:|---:|---:|
| `BenchmarkScaleSubagents/subagents0` | 1 865 575 | 1 786 600 | 0.96× | 1 178 146 | 927 123 |
| `BenchmarkScaleSubagents/subagents10` | 2 041 638 | 1 987 792 | 0.97× | 1 222 624 | 962 499 |
| `BenchmarkScaleSubagents/subagents50` | 2 246 796 | 2 191 851 | 0.98× | 1 287 221 | 1 003 607 |
| `BenchmarkScaleSubagents/subagents200` | 4 327 108 | 2 162 621 | **0.50×** | 1 662 730 | 1 002 044 |
| `BenchmarkScaleReloadSessions/reload_s0` | 20 562 | 3 640 | **0.18×** | 83 162 | 1 400 |
| `BenchmarkScaleReloadSessions/reload_s10` | 24 095 | 4 551 | **0.19×** | 83 986 | 1 648 |
| `BenchmarkScaleReloadSessions/reload_s50` | 38 970 | 8 372 | **0.21×** | 88 434 | 3 024 |
| `BenchmarkScaleReloadSessions/reload_s200` | 444 091 | 37 775 | **0.085×** | 179 270 | 7 771 |
| `BenchmarkScaleCombined/msgs40_subs0` | 1 931 324 | 1 937 180 | 1.00× | 958 910 | 931 575 |
| `BenchmarkScaleCombined/msgs40_subs200` | 2 423 445 | 2 274 262 | 0.94× | 1 049 629 | 1 020 983 |
| `BenchmarkScaleCombined/msgs6000_subs0` | 2 806 187 | 1 804 316 | **0.64×** | 4 417 811 | 930 902 |
| `BenchmarkScaleCombined/msgs6000_subs200` | 3 306 371 | 2 360 527 | **0.71×** | 4 509 078 | 1 020 528 |

The subagent count no longer prices the frame: `subagents200` went from 4.33 ms (an
expired cache made the frame call `GetSessionSubagents`) to 2.16 ms — the same frame as
with no subagents at all — while the listing call itself is untouched (see 2.5).
`reloadSessions` is 11.8× cheaper at 200 sessions with 23× fewer bytes and a flat
24 allocs (it was 815): the walk still happens, but at most once per coalescing window
and off the frames.

### 2.4 New benchmark rows present in this run (no baseline row)

Two mechanisms are new, so they were added to the harness rather than compared against a
frozen value:

| Benchmark | after ns/op | after B/op | what it pins |
|---|---:|---:|---|
| `BenchmarkScaleSessionListReads/list_s0` | 292.3 | 32 | the session-listing half of a refresh |
| `BenchmarkScaleSessionListReads/list_s200` | 51 877 | 63 840 | …at 200 sessions (one entry per session, by design) |
| `BenchmarkScaleSessionListReads/counts_s0` | 291.9 | 256 | the batched per-session counts half |
| `BenchmarkScaleSessionListReads/counts_s200` | 29 248 | 6 616 | …at 200 sessions: per-event, it is now memoized (no message-table scan) |
| `BenchmarkHistoryMessageCount/memoized_msgs6000` | 3.0 | 0 | the memoized count: a key compare, flat in the length |
| `BenchmarkHistoryMessageCount/rolescan_msgs6000` | 45 906 | 0 | the old O(n) role scan it replaces (15 223× the memo) |
| `BenchmarkScaleSessionsRefresh/sessions200` | 94 876 | 71 225 | the *other* side: the walk itself, paid ≤ once per 250 ms |
| `BenchmarkUpdateSubagentsRefresh/subagents200` | 2 188 601 | 401 717 | the *other* side of the subagent cache: paid ≤ once per 3 s from `Update()` |

### 2.5 Rows deliberately unchanged

| Benchmark | before ns/op | after ns/op | ratio | note |
|---|---:|---:|---:|---|
| `BenchmarkGetSessionSubagents/n200` | 2 025 894 | 1 921 042 | 0.95× | the listing call was **not** optimised — only the frequency of its call from the frame. Re-run: 1 972 684–2 012 193 |
| `BenchmarkGetSessionSubagents/n400` | 4 798 421 | 5 016 762 | 1.05× | idle; re-run 4 662 754–4 860 844 (baseline 4.80 ms sits inside that band) |
| `BenchmarkGetSessionSubagentsCold/cold200` | 2 603 484 | 2 932 988 | 1.13× | idle; re-run 2 690 239–2 723 497 |
| `BenchmarkGetSessionSubagentsCold/cold500` | 6 927 664 | 7 860 963 | 1.13× | idle; re-run **6 978 777–7 451 279** — the single run was a contended one (the baseline page documents the same effect) |
| `BenchmarkMessageFingerprint/representative` | 1 713 | 1 759 | 1.03× | already in the frozen table (the baseline row was regenerated after the fingerprint rewrite); re-run 1 677–1 681 |
| `BenchmarkMessageFingerprint/huge_tool_result` | 141 009 | 146 722 | 1.04× | same; re-run 140 529–142 189 (baseline's own range was 139 253–144 463) |

The subagent-listing benchmark rows measure code this series did not change: what changed
is that the frame no longer calls it, so those rows are kept honestly as "idle" rather
than dressed up. Relative to the *original* pre-rewrite fingerprint (26 472 ns on the
representative input in the baseline's superseded row), the word-at-a-time FNV-1a variant
shipped in the tree is what produced the 1 713 ns baseline row — that gain is already
inside the frozen table, not a new delta here.

## 3. Design of each mechanism

- **History view O(1) — copy-on-write snapshot + `saveEpoch`** (`pkg/session/view.go`,
  `session.go`, `manager.go`). Writers publish a *private* immutable copy of
  `Session.Messages` through an `atomic.Pointer[messageSnapshot]`; readers hand it out
  without copying. Validity is tagged with `saveEpoch` (already bumped by every logical
  mutation), so a forgotten/stale publication costs a lazy rebuild, never correctness —
  and the snapshot is a copy, not a header aliasing the live array, because messages are
  mutated *in place* (streaming chunks, in-place replacement, exclusion flags).
  Streaming chunks deliberately do **not** publish (`streaming.go`): at chunk rate the
  O(n) copy would cost more than the reads it saves; the epoch check turns that into one
  rebuild per read burst instead of one copy per chunk.
- **One history read per frame — frame snapshot** (`pkg/tui/viewport.go`,
  `view.go`). `View()` opens a render frame; the first `historyView()` reads
  `GetHistoryView` once and every consumer of that frame — viewport rebuild,
  token/context readout (bottom bar + sidebar), pending-message checks — reuses it. The
  snapshot is dropped when the frame ends, because history can be mutated in place with
  an unchanged length (a finalized streaming message) and a slice kept across frames
  would freeze the finished turn out of the transcript.
- **Render path without O(n) reads — freshness fingerprint, throttle, count memo**
  (`pkg/tui/viewport.go`, `model.go`, `types.go`). `View()` no longer rebuilds
  unconditionally: it compares an O(1) fingerprint of everything the viewport content
  depends on (`viewportContentKey`) against the state the on-screen content was
  materialized from, plus the layout geometry it owns; the O(lines) rebuild is reached
  only when one of them fails, and a pending streaming rebuild is deferred only within
  the throttle window (never a geometry change, never longer than
  `streamThrottleInterval`). The Update side shares the *same* fingerprint, so the two
  paths cannot drift. The user+assistant count is memoized on the *identity* of the
  history slice (first-element address + length), which is sound precisely because the
  COW snapshot is republished with a new identity on every structural change: two
  consumers of one snapshot share one role scan, and an unchanged snapshot costs none.
- **Per-message render cache: fingerprint + no caching of transient renders**
  (`pkg/tui/utils.go`, `viewport.go`). `messageFingerprint` is now a word-at-a-time
  FNV-1a variant that consumes strings in place (no `[]byte` conversion, no
  `fmt.Sprintf`), mixing every field behind a fixed-width length prefix so partitions
  cannot collide; it stays a process-local cache key. Renders of the currently-executing
  message (whose tool-call rows are suppressed in favour of the overlay) are a transient
  variant of the same fingerprint and are therefore **never stored**, so a cache hit can
  never resurrect a tool-row-less frame after the message stops being the last one.
- **Persistence lock-split: collect → unlock → I/O → relock** (`pkg/session/persist.go`,
  `eviction.go`). The full rewrite, the excluded-range write, the eviction-boundary
  write and the meta/incremental/delete paths collect a consistent snapshot under the
  write lock, release it for *all* SQLite round trips and JSON work, then re-acquire and
  reconcile through the existing epoch guard (skipping bookkeeping on a concurrent
  mutation so it is re-persisted). Every release/re-acquire goes through one helper
  (`sm.unlocked`) so a panic in the I/O cannot leave the lock free for the caller's
  deferred `Unlock`; the payloads are free functions with no access to `sm.mu`, which
  makes "no I/O under the lock" structural rather than a convention.
- **LRU eviction watermark** (`pkg/session/eviction.go`). Both places that materialize a
  session call `evictIfNeeded`; with a one-in-one-out trigger every insertion paid a full
  cascade (sort + save + `ReplaceMessages`). A 16-session hysteresis band now runs the
  LRU pass only when the resident set reaches `maxInMemory+16`, then evicts the whole
  backlog down to `maxInMemory` in one batch (~16× fewer cascades in a 200-session
  burst), capped at `maxInMemory` so small configurations do not overshoot. The TTL sweep
  stays unconditional (it cannot repeat), and the resident peak is bounded.
- **Cached and coalesced listings** (`pkg/tui/model.go`, `types.go`;
  `pkg/session/listing.go`). The subagent listing has one writer served from `Update()`
  (`refreshSubagentsCache`, ≤ once per 3 s, with "running" materialized so the status
  line answers in O(1)) and the frame only reads it; the session listing has one walk
  (`refreshSessionsCache`, ≤ once per 250 ms) with resident entries holding live session
  pointers, and the two paths that must be exact (a switch onto an unknown chat, the
  `/sessions` picker) bypass the window. In `pkg/session`, the batched store-side message
  counts behind `AllMessageCounts`/`AllTotalMessageCounts` are memoized with a 1 s TTL
  plus an exact cold-key-set check, so resident counts stay exact and the message-table
  scan is not re-run per event.
- **Minimal invalidation after compaction** (`pkg/tui/handlers_async.go`). `/compact`
  changes exclusion state and evicts the prefix; surviving messages keep the exact
  content their fingerprints were built from, so the per-message cache is *not* dropped.
  Everything that does move is already a cache-key term (archived fingerprint, resident
  length/count, streaming flag), and the one render a fingerprint could not vouch for —
  the last message's tool rows while a tool runs — is not cached in the first place. The
  result is 21.7 ms → 1.3 ms and 8.9 MB → 630 KB per compaction result, with no full
  glamour re-render of untouched messages.
- **Subagent retention sweeper + listing cache** (`pkg/tools/subagent_manager.go`,
  `pkg/agent/tool_coordinator.go`, `pkg/agent/loop.go`). A periodic sweeper (once a
  minute) reaps terminal tasks past `agents.defaults.subagent_retention_minutes` (5 min
  by default) and fires the existing session-evict callback, so a chat that stops
  spawning no longer pins every finished task and its session forever; it is idempotent
  (one goroutine per manager) and reads the period on every pass. Teardown is bounded:
  stopping joins the sweeper with a grace, and giving up *detaches* the manager (the
  sweeper stops sweeping, `CleanupTerminalTasks` refuses to touch the map, evictions
  already in flight are honoured) — `AgentLoop.StopWithin` then leaves the store open
  instead of closing it under a live write.

## 4. Guarantees (semantics intact)

- The whole Go suite is green: `go test -count=1 ./...` → **41 packages `ok`, 0 `FAIL`**;
  `-race` green for `pkg/session` and `pkg/tui` (49.7 s / 83.2 s, re-run on the frozen tree at
  gate time); `go vet ./...` and `gofmt -l` clean. `pkg/agent` under `-race` fails on one
  **pre-existing** test race, reproduced byte-for-byte on the pristine base commit — see
  follow-up 6.
- Every mechanism carries a red-check: the test was shown to fail with the fix reverted
  (single mutation), per task and again in the two repair rounds — e.g. removing the COW
  snapshot re-introduces the per-call copy; removing the frame snapshot makes a frame read
  the history twice; dropping the memo hit re-runs the role scan; caching the suppressed
  render again makes an appended message keep its tool-row-less frame; restoring the old
  `/compact` handler re-renders all 24 cached messages; making the listing cache ignore
  its window re-walks per event; reverting the panic-safe lock helper kills the process
  with `fatal error: sync: Unlock of unlocked RWMutex`.
- Read-only contracts are stated where they are enforced: `GetHistoryView` returns an
  immutable shared snapshot ("MUST NOT mutate"), `GetSessionHistory` returns a defensive
  copy for cross-package callers (the WebUI history endpoint), and `metric`-level
  behaviour that depends on it (WebUI history pagination, read from index 0) is covered
  by tests.
- Persistence parity is pinned, not assumed: seq re-basing, byte-parity of the evicted
  prefix, single-use tables, the exclusion-range heal, corrupted-row skipping and the
  epoch guard each have an explicit test, and the lock-split kept them green.
- No public API, config key (other than the added `subagent_retention_minutes`), wire
  format or user-visible semantics changed; the LLM-visible history is the same slice of
  the same messages, only cheaper to hand out.

## 5. Known follow-ups (measured, deliberately deferred)

1. **`messagesEpoch` separate from `saveEpoch`.** Metadata-only writes (name, mode,
   summary, token counts, subagent status — `pkg/session/settings.go`, `tokens.go`) bump
   the same epoch that tags the history snapshot without republishing it, so the next
   read clones the history once to rebuild a snapshot whose *messages* never changed:
   one O(n) clone per turn. A second counter bumped only by message mutations would
   remove it.
2. **`viewportContentKey` builds two `Sprintf` strings per frame.** O(1), but it is real
   per-frame string churn (a few hundred bytes and a couple of allocations) on the frame
   path; hashing the fields into an integer key or maintaining an incremental key would
   remove it.
3. **Predecessor-dependent header not folded into the render-cache fingerprint.**
   `messageFingerprint` covers role/content/reasoning/tool calls/width, not the context a
   renderer derives from the previous message (the agent-name header on the first resident
   message). Consequence is cosmetic: after `/compact`, the first resident message can
   show the previous agent name for one render. Fixing it means changing the fingerprint
   contract used by the perf tests of other lanes, so it is deferred on purpose.
4. **Residual fixed frame floor ≈1.9 ms and ≈0.96 MB per frame, independent of history.**
   A 40-message chat now costs essentially the same as a 6000-message one, and both are
   the fixed full-frame paint (`lipgloss.Place` + the app container render +
   `reapplyBackground`). That is a separate workstream (lipgloss/paintFrame) and the
   largest remaining cost; note the container deliberately uses `MaxWidth`/`MaxHeight`
   (clamping) rather than `Width` (which would re-measure and re-pad every line, ~200 µs
   of it per 200×50 frame), so any rewrite must keep the clamping semantics.
5. **`cmd/lele/gateway.go` logs every abandoned teardown as "turns still in flight".**
   Since the retention-sweeper teardown can now abandon a stop for a different reason, the
   message over-attributes the cause; the agent-loop error itself was already fixed to
   "work in flight".
6. **Pre-existing `-race` failures outside the gate set.** `pkg/tools` has 4 data races in
   test helpers (reproduced on the pristine base commit as well as the fixed tree; race
   mode fails 2–4 tests on every run), `pkg/channels` has 2 more in the MaixCam
   stop/send tests (`BaseChannel.setRunning` vs `IsRunning`, `base.go`/`maixcam.go` —
   files this branch does not touch; measured today: `go test -race -count=1
   ./pkg/channels/` → `FAIL`, 2 races, 42 s), and `pkg/agent` has one in a test:
   `TestHarnessManagerFor_ConcurrentPerWorkspaceAccess` (`harness_command_test.go:895-902`)
   mutates `cfg.Commands` **in place** on the very pointer `al.cfg()` hands out while eight
   goroutines read it through `harnessManagerFor`, so the walk of `pkg/agent` under `-race`
   reports the test's own write even though the production map itself is correctly guarded
   by `harnessMu`; both files are untouched by this branch and the failure reproduces on the
   pristine base commit (`git archive c97d78d` → `-count=2` → 5 race reports, 1 `FAIL`,
   `/tmp/base-race-agent.txt`). That is why the gate's `-race` set is `pkg/session` +
   `pkg/tui` and excludes those three packages: none of the three is a race in code this
   series touches. One-line remedy for the agent one: store a fresh copy of the config
   (`cp := *cfg; cp.Commands = next; al.cfgPtr.Store(&cp)`) instead of mutating the live
   pointer — deliberately left to its owner, since the test file belongs to the
   harness-commands lane.