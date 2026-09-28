# TUI long-chat performance baseline

**Purpose:** frozen reference of the TUI costs that make long chats (6000 messages),
accumulated subagents (200–500) and continuous streaming feel slow. Any performance
fix must be compared against this table.

> **This table is the PRE-FIX ("before") column, not the current cost.**
> Every row below was measured on the unfixed tree and is frozen on purpose, so the
> diff each fix produced stays visible. The post-fix numbers — measured today with the
> same command and harness — live in
> [`docs/perf/tui-long-chat-results.md`](tui-long-chat-results.md), which is the page to
> read for "how fast is the TUI now". Mechanism details written here (cache TTLs, field
> names, which path calls what) also describe the pre-fix tree and are superseded there;
> only the numbers were frozen.

| | |
|---|---|
| Date | 2026-09-27 |
| Commit | `c97d78d` (`perf/tui-long-chat`, working tree dirty — production code untouched by T0) |
| Machine | Intel(R) Core™ i5-10300H @ 2.50GHz, 8 cores, 15 GiB RAM, openSUSE Tumbleweed (kernel 7.2.6-1-default) |
| Go | go1.26.6 linux/amd64 |
| Harness | `pkg/tui/scale_bench_test.go`, `pkg/session/history_bench_test.go` |
| Command | `make bench` → `go test -run '^$' -bench 'History|Scale|Subagents|MouseMotion|Fingerprint' -benchmem ./pkg/tui/ ./pkg/session/` |
| Conditions | single run with `-benchtime=1s` (default); ~300–650 iterations for the scale benchmarks, so treat ±10 % as noise. The numbers below were spot-checked with 2–3 extra runs and reproduced (B/op byte-identical); **the machine is shared with parallel workstreams**, so a busy moment can inflate an individual sub-benchmark (observed 15 ms instead of 4.3 ms for `ScaleSubagents/subagents200` under contention) — re-measure before declaring a regression |

Setup is identical for every benchmark: model built through `newBenchModel`
(temporary `LELE_CONFIG_DIR` + temp SQLite store), terminal **200×50**, TrueColor
profile, viewport cache warmed with one `View()` before timing. Message counts are
`pairs*2` (user+assistant); "msgs6000" = 3000 pairs = the pathological chat size
reported. The subagent cache is pinned (never self-expires); benchmarks that want the
cold path reset `subagentsCache.at` explicitly.

## 1. Frame cost — `pkg/tui`

| Benchmark | ns/op | ms/op | B/op | allocs/op |
|---|---:|---:|---:|---:|
| `BenchmarkScaleView/view_msgs40` | 2 031 197 | 2.03 | 994 205 | 3 143 |
| `BenchmarkScaleView/view_msgs400` | 2 053 130 | 2.05 | 1 216 072 | 3 145 |
| `BenchmarkScaleView/view_msgs2000` | 2 275 744 | 2.28 | 2 138 152 | 3 146 |
| `BenchmarkScaleView/view_msgs6000` | 3 163 815 | 3.16 | 4 467 982 | 3 150 |
| `BenchmarkScaleIdleUpdate/idle_msgs40` | 2 091 699 | 2.09 | 990 764 | 3 150 |
| `BenchmarkScaleIdleUpdate/idle_msgs2000` | 2 321 350 | 2.32 | 2 134 160 | 3 153 |
| `BenchmarkScaleIdleUpdate/idle_msgs6000` | 3 092 908 | 3.09 | 4 454 368 | 3 156 |
| `BenchmarkScaleStreamUpdate/stream_update_msgs40` | 1 942 793 | 1.94 | 997 407 | 3 153 |
| `BenchmarkScaleStreamUpdate/stream_update_msgs400` | 2 051 858 | 2.05 | 1 218 204 | 3 155 |
| `BenchmarkScaleStreamUpdate/stream_update_msgs2000` | 2 361 769 | 2.36 | 2 138 850 | 3 156 |
| `BenchmarkScaleStreamUpdate/stream_update_msgs6000` | 3 227 007 | 3.23 | 4 464 810 | 3 160 |
| `BenchmarkScaleRebuild/newmsg_msgs40` | 2 459 118 | 2.46 | 1 859 332 | 4 750 |
| `BenchmarkScaleRebuild/newmsg_msgs400` | 2 649 108 | 2.65 | 2 166 315 | 4 827 |
| `BenchmarkScaleRebuild/newmsg_msgs2000` | 3 076 183 | 3.08 | 3 290 534 | 4 692 |
| `BenchmarkScaleRebuild/newmsg_msgs6000` | 4 579 141 | 4.58 | 6 191 525 | 4 424 |
| `BenchmarkMouseMotion/motion_msgs40` | 1 923 544 | 1.92 | 986 569 | 3 148 |
| `BenchmarkMouseMotion/motion_msgs6000` | 2 849 936 | 2.85 | 4 445 740 | 3 152 |

## 2. Subagents — `pkg/tui`

| Benchmark | ns/op | ms/op | B/op | allocs/op |
|---|---:|---:|---:|---:|
| `BenchmarkScaleSubagents/subagents0` | 1 865 575 | 1.87 | 1 178 146 | 2 843 |
| `BenchmarkScaleSubagents/subagents10` | 2 041 638 | 2.04 | 1 222 624 | 3 373 |
| `BenchmarkScaleSubagents/subagents50` | 2 246 796 | 2.25 | 1 287 221 | 4 264 |
| `BenchmarkScaleSubagents/subagents200` | 4 327 108 | 4.33 | 1 662 730 | 7 471 |
| `BenchmarkScaleReloadSessions/reload_s0` | 20 562 | 0.021 | 83 162 | 22 |
| `BenchmarkScaleReloadSessions/reload_s10` | 24 095 | 0.024 | 83 986 | 28 |
| `BenchmarkScaleReloadSessions/reload_s50` | 38 970 | 0.039 | 88 434 | 32 |
| `BenchmarkScaleReloadSessions/reload_s200` | 444 091 | 0.444 | 179 270 | 815 |
| `BenchmarkGetSessionSubagents/n0` | 645.6 | — | 16 | 1 |
| `BenchmarkGetSessionSubagents/n25` | 10 055 | 0.010 | 18 744 | 18 |
| `BenchmarkGetSessionSubagents/n100` | 793 643 | 0.794 | 167 456 | 1 099 |
| `BenchmarkGetSessionSubagents/n200` | 2 025 894 | 2.03 | 413 787 | 3 208 |
| `BenchmarkGetSessionSubagents/n400` | 4 798 421 | 4.80 | 975 743 | 7 445 |
| `BenchmarkGetSessionSubagentsCold/cold0` | 641.4 | — | 16 | 1 |
| `BenchmarkGetSessionSubagentsCold/cold50` | 717 012 | 0.717 | 128 270 | 1 116 |
| `BenchmarkGetSessionSubagentsCold/cold200` | 2 603 484 | 2.60 | 517 361 | 4 280 |
| `BenchmarkGetSessionSubagentsCold/cold500` | 6 927 664 | 6.93 | 1 225 474 | 10 647 |

## 3. Combined scenario + non-scale benchmarks — `pkg/tui`

| Benchmark | ns/op | ms/op | B/op | allocs/op |
|---|---:|---:|---:|---:|
| `BenchmarkScaleCombined/msgs40_subs0` | 1 931 324 | 1.93 | 958 910 | 2 851 |
| `BenchmarkScaleCombined/msgs40_subs200` | 2 423 445 | 2.42 | 1 049 629 | 4 413 |
| `BenchmarkScaleCombined/msgs6000_subs0` | 2 806 187 | 2.81 | 4 417 811 | 2 856 |
| `BenchmarkScaleCombined/msgs6000_subs200` | 3 306 371 | 3.31 | 4 509 078 | 4 420 |
| `BenchmarkMessageFingerprint/representative` (from `pkg/tui/utils_fingerprint_test.go`, parallel workstream — included because it matches the `make bench` filter) | 1 713 | 0.0017 | 16 | 1 |
| `BenchmarkMessageFingerprint/huge_tool_result` (same file: ~544 KB tool-result content + ~256 KB tool arguments) | 141 009 | 0.14 | 16 | 1 |

> **Row regenerated 2026-09-27 (the `BenchmarkMessageFingerprint` row above is superseded).**
> The slot originally held a single `BenchmarkMessageFingerprint` at 26 472 ns/op — the
> pre-rewrite fingerprint (byte-at-a-time `fnv.New64a` fed with one `[]byte` conversion per
> field, `fmt.Sprintf("%016x")` key). The tree now ships the word-at-a-time FNV-1a variant
> (no field copies, fixed-width length-prefixed fields, 16-byte hex key) and the benchmark is
> split into the two sub-benchmarks above, so nothing in the tree reports the old number.
> Re-measured with the same command and filter, narrowed to this row
> (`go test -run '^$' -bench 'Fingerprint' -benchmem ./pkg/tui/`, `-benchtime=1s`), four runs;
> the two table rows above are their averages:
> representative **1 684–1 772 ns/op** (5 032–5 296 MB/s), huge_tool_result **139 253–144 463 ns/op**
> (5 671–5 883 MB/s); **16 B/op and 1 alloc/op in every run**. The 26 472 ns figure is kept only
> as the pre-rewrite reference — do not compare it with the rows above it.

## 4. History reads — `pkg/session`

| Benchmark | ns/op | µs/op | B/op | allocs/op |
|---|---:|---:|---:|---:|
| `BenchmarkGetHistoryView/msgs40` | 1 726 | 1.73 | 8 192 | 1 |
| `BenchmarkGetHistoryView/msgs2000` | 100 187 | 100.2 | 385 024 | 1 |
| `BenchmarkGetHistoryView/msgs6000` | 327 629 | 327.6 | 1 155 075 | 1 |

Raw output: `make bench` (package times: `pkg/tui` 64.9 s, `pkg/session` 4.6 s).

## Interpretation

1. **Every TUI frame is O(history) and allocates the whole history.** An idle frame,
   an unconsumed mouse-motion event and a streaming chunk all cost ≈2.0 ms at 40 msgs
   and ≈3.2 ms at 6000 msgs, with **4.5 MB allocated per frame** at 6000 (994 KB at
   40). Frame cost grows with the chat even though only ~50 lines are visible: the
   growth is allocation/copy work, not rendering of visible cells.
2. **`GetHistoryView` is a full O(n) copy: 328 µs and 1.1 MB at 6000 messages, per
   call — and it is called 3× per `View()`.** Measured on a diagnostic copy with call
   counters (`/tmp/leleperf`, `TestDiagPerFrame`): 3 calls per frame at 40 msgs and 3
   at 6000 msgs, for both idle and streaming frames (three call sites: the viewport
   rebuild in `updateViewport`, the token/context readout at `view.go:255`/`view.go:464`
   via `getTokenUsage → getHistoryMessageCount`, and the pending-message/streaming-cleanup
   checks). That
   is ≈0.98 ms and ≈3.5 MB of the 3.16 ms / 4.47 MB frame at 6000 messages — ~31 % of
   frame time and ~78 % of frame allocation is pure memcpy + GC pressure. It is the
   single hottest primitive in the table and the first thing a fix should attack
   (read-only reference / COW instead of a per-call copy).
3. **Subagents dominate the worst frames, and the cost is not the sidebar rows but the
   listing scan.** With a cold 500 ms cache, 200 completed subagents take the frame
   from 1.87 ms (0 subagents) to **4.33 ms**, and one `GetSessionSubagents` call costs
   2.03 ms at n=200 (4.80 ms at n=400) with 414 KB–976 KB allocated; when those
   subagent sessions are *cold* (evicted/reloaded from storage, i.e. the steady state
   of a long-running server) the same call costs **6.93 ms and 1.2 MB at n=500**.
   `reloadSessions` shows the same shape: 21 µs with no extra sessions, **444 µs with
   200** (20× worse). The subagent listing is only cached for 500 ms by the TUI, so a
   long-running session pays the full 2–7 ms above on every cache expiry or
   event-driven invalidation (that is exactly what `BenchmarkScaleSubagents` and
   `BenchmarkScaleCombined` simulate).
4. **Streaming and mouse motion multiply the above.** At 6000 messages a chunk costs
   3.23 ms/frame, i.e. a hard ceiling of ~310 stream chunks/s before the event queue
   backs up, and a single un-consumed motion event (`EnableMouseCellMotion` reports
   every cell) costs 2.85 ms even though `Update` discards it — with the mouse moving,
   that is a full 3 ms+ frame at 60+ Hz competing with the stream.