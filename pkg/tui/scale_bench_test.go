package tui

// Permanent scale benchmark harness for the TUI.
//
// Context: the TUI was reported as slow with long chats (6000 messages),
// hundreds of accumulated subagents and continuous streaming. This file keeps
// the scale benchmarks that reproduce those scenarios so every future fix can
// be verified against a stable baseline (see docs/perf/tui-long-chat-baseline.md).
//
// It uses the same model/terminal setup as view_bench_test.go (200x50,
// TrueColor, temporary stores) so numbers stay comparable across benchmarks:
//   - newBenchModel(tb) lives in view_bench_test.go and is reused here.
//   - viewSink lives in view_bench_test.go and is reused here.
//
// Run with: make bench (or `go test -run '^$' -bench . ./pkg/tui/`).
//
// Scale note: sub-benchmark names encode the message count (pairs*2), so
// view_msgs6000 means 3000 user/assistant pairs == 6000 messages.

import (
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/session"
)

// scaleBenchSink keeps the subagent listings alive so the compiler cannot
// elide the measured calls.
var scaleBenchSink []channels.SubagentTaskInfo

// buildScaleModel creates a model with `pairs` user/assistant pairs and
// `subagents` completed subagent sessions registered in the session manager.
// The subagent listing cache is seeded as a just-refreshed one (T6): the
// render path only reads it, so benchmarks that provoke a stale cache can zero
// subagentsCache.at — the frame must still serve the cached listing.
func buildScaleModel(tb testing.TB, pairs, subagents int) *Model {
	tb.Helper()
	m := newBenchModel(tb)

	key := "tui:chat:bench"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	for i := 0; i < pairs; i++ {
		m.sessionMgr.AddMessage(key, "user", fmt.Sprintf("Question %d: how do I implement feature X with proper error handling and tests?", i))
		m.sessionMgr.AddMessage(key, "assistant",
			fmt.Sprintf("Answer %d:\nHere is an approach:\n```go\nfunc feature%d() error {\n\tif err := doWork(); err != nil {\n\t\treturn fmt.Errorf(\"feature %d: %%w\", err)\n\t}\n\treturn nil\n}\n```\nThis handles the error path and is covered by tests.", i, i, i))
	}

	m.currentKey = key
	m.showWelcome = false

	var list []channels.SubagentTaskInfo
	for i := 0; i < subagents; i++ {
		sk := fmt.Sprintf("native:%s:subagent-%d", key, i)
		m.sessionMgr.GetOrCreate(sk)
		m.sessionMgr.AddMessage(sk, "user", "task")
		m.sessionMgr.AddMessage(sk, "assistant", "done")
		list = append(list, channels.SubagentTaskInfo{
			TaskID:     fmt.Sprintf("subagent-%d", i),
			Label:      fmt.Sprintf("Phase %d", i),
			Status:     "completed",
			SessionKey: sk,
		})
	}
	seedSubagentListing(m, key, time.Now(), list)

	return m
}

// scaleBenchSizes is the message-count ladder used across the scale
// benchmarks: 40 (short chat), 400 (long chat), 2000 and 6000 (pathological
// chats reported by users).
var scaleBenchSizes = []int{40, 400, 2000, 6000}

// withTrueColor forces the TrueColor profile for the duration of a benchmark,
// mirroring view_bench_test.go so all frames render the same way.
func withTrueColor(b *testing.B) {
	b.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	b.Cleanup(func() { lipgloss.SetColorProfile(old) })
}

// BenchmarkScaleView measures steady-state View() cost as the chat grows.
// The viewport cache is warmed first: this is the per-frame cost the user
// feels while idle/scrolling, not the one-time cold markdown render.
func BenchmarkScaleView(b *testing.B) {
	withTrueColor(b)

	for _, msgs := range scaleBenchSizes {
		m := buildScaleModel(b, msgs/2, 5)
		m.width, m.height = 200, 50
		_ = m.View() // warm the viewport cache
		b.Run(fmt.Sprintf("view_msgs%d", msgs), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				viewSink = m.View()
			}
		})
	}
}

// BenchmarkScaleStreamUpdate measures the real streaming hot path: one
// message.stream outbound event + one View(), which is what bubbletea does
// for every token chunk.
func BenchmarkScaleStreamUpdate(b *testing.B) {
	withTrueColor(b)

	for _, msgs := range scaleBenchSizes {
		m := buildScaleModel(b, msgs/2, 5)
		m.width, m.height = 200, 50
		m.processing = true
		_ = m.View() // warm
		chunk := outboundMsg{msg: bus.OutboundMessage{
			Channel: "tui", ChatID: m.currentKey, Event: "message.stream", Content: "hello world ",
		}}
		b.Run(fmt.Sprintf("stream_update_msgs%d", msgs), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = m.Update(chunk)
				viewSink = m.View()
			}
		})
	}
}

// BenchmarkScaleIdleUpdate measures an idle tick frame (spinner/blink), the
// cheapest possible frame: it exposes any per-frame O(history) work.
func BenchmarkScaleIdleUpdate(b *testing.B) {
	withTrueColor(b)

	for _, msgs := range []int{40, 2000, 6000} {
		m := buildScaleModel(b, msgs/2, 5)
		m.width, m.height = 200, 50
		_ = m.View() // warm
		b.Run(fmt.Sprintf("idle_msgs%d", msgs), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = m.Update(tickMsg{})
				viewSink = m.View()
			}
		})
	}
}

// BenchmarkScaleRebuild measures the rebuild triggered by a new message
// arriving (append + View), i.e. the cost at the end of each agent turn.
//
// The two AddMessage calls are the SETUP of the frame, not the frame: they run
// with the timer stopped, and the appended pair is trimmed again before the
// next iteration, so every timed View() sees exactly the msgs+2 history the
// sub-benchmark name promises. Before this, the appends ran inside the timed
// loop, so "newmsg_msgs6000" was really measuring 6.7k–7.2k messages and the
// number moved with -benchtime (the history grew for as long as the loop ran).
func BenchmarkScaleRebuild(b *testing.B) {
	withTrueColor(b)

	for _, msgs := range scaleBenchSizes {
		m := buildScaleModel(b, msgs/2, 5)
		m.width, m.height = 200, 50
		_ = m.View() // warm
		b.Run(fmt.Sprintf("newmsg_msgs%d", msgs), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				m.sessionMgr.AddMessage(m.currentKey, "user", fmt.Sprintf("q%d", i))
				m.sessionMgr.AddMessage(m.currentKey, "assistant", fmt.Sprintf("a%d", i))
				b.StartTimer()
				viewSink = m.View()
				b.StopTimer()
				// Undo the pair: keep the history at its advertised size.
				m.sessionMgr.RemoveLastMessage(m.currentKey)
				m.sessionMgr.RemoveLastMessage(m.currentKey)
				b.StartTimer()
			}
		})
	}
}

// BenchmarkScaleSubagents measures View() with an EXPIRED subagent listing
// cache (subagentsCache.at zeroed every iteration) for a growing number of
// completed subagent sessions — the steady state of a long-running session that
// has spawned hundreds of subagents.
//
// Before T6 this was the M1 bug: an expired cache made the frame call
// GetSessionSubagents, so 200 subagents cost 4.33 ms/frame (baseline). The frame
// now serves the cached listing, so the cost is flat in the subagent count; if
// a refresh is ever put back on the render path this benchmark jumps straight
// back to the baseline shape.
func BenchmarkScaleSubagents(b *testing.B) {
	withTrueColor(b)

	for _, n := range []int{0, 10, 50, 200} {
		m := buildScaleModel(b, 200, n) // 400 messages
		m.width, m.height = 200, 50
		_ = m.View() // warm
		b.Run(fmt.Sprintf("subagents%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// Provoke staleness: pre-T6 this forced a refresh from the frame.
				m.subagentsCache.at = time.Time{}
				viewSink = m.View()
			}
		})
	}
}

// BenchmarkScaleReloadSessions measures reloadSessions(), which several
// events call, against the number of sessions (chat + subagents) held by the
// session manager.
func BenchmarkScaleReloadSessions(b *testing.B) {
	withTrueColor(b)

	for _, n := range []int{0, 10, 50, 200} {
		m := buildScaleModel(b, 200, n)
		m.width, m.height = 200, 50
		b.Run(fmt.Sprintf("reload_s%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m.reloadSessions()
			}
		})
	}
}

// BenchmarkScaleSessionsRefresh measures the OTHER side of T8 (same shape as
// BenchmarkUpdateSubagentsRefresh): the listing walk is not free, it is paid at
// most once per sessionsRefreshTTL instead of once per event. Each iteration
// zeroes the snapshot, i.e. simulates the worst case of a walk on every event —
// compare with BenchmarkScaleReloadSessions, which measures the coalesced
// per-event cost and must stay nearly flat in the session count.
//
// The batched store counts this refresh asks for cannot be forced from here
// (pkg/session owns that cache and only its TTL or a residency change re-runs
// the GROUP BY, which is exactly the point); the measured numbers include the
// resident-count pass and the map build, not the table scan.
func BenchmarkScaleSessionsRefresh(b *testing.B) {
	withTrueColor(b)

	for _, n := range []int{0, 10, 50, 200} {
		m := buildScaleModel(b, 200, n)
		m.width, m.height = 200, 50
		b.Run(fmt.Sprintf("sessions%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m.sessionsCache = sessionsCacheState{} // force the walk
				m.reloadSessions()
			}
		})
	}
}

// scaleBenchListSink and scaleBenchCountSink keep the two listing halves alive
// so the compiler cannot elide the measured calls. They are typed (not
// interface{}) on purpose: a slice/map stored in an interface{} is boxed, which
// would add a per-iteration allocation to both halves.
var (
	scaleBenchListSink  []*session.Session
	scaleBenchCountSink map[string]int
)

// BenchmarkScaleSessionListReads decomposes the walk that
// BenchmarkScaleReloadSessions measures as a whole into its two halves: the
// session listing itself (ListSessions) and the batched per-session message
// counts (AllTotalMessageCounts, the call T8 added to stop the refresh from
// scanning the message table per session).
//
// Both are priced per event, at most once per sessionsRefreshTTL, so the useful
// signal is the SHAPE against the session count: the listing half may grow with
// it (it returns one entry per session), the counts half must not grow faster
// than that map build. A per-session walk sneaking back into either half shows
// up here, before it shows up in a frame.
//
// Sizes: 0 (short chat) and 200 (the steady state of a long-running session with
// hundreds of finished subagents).
func BenchmarkScaleSessionListReads(b *testing.B) {
	for _, n := range []int{0, 200} {
		m := buildScaleModel(b, 200, n) // 400 messages + n subagent sessions
		b.Run(fmt.Sprintf("list_s%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				scaleBenchListSink = m.sessionMgr.ListSessions()
			}
		})
		b.Run(fmt.Sprintf("counts_s%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				scaleBenchCountSink = m.sessionMgr.AllTotalMessageCounts()
			}
		})
	}
}

// BenchmarkScaleCombined reproduces the reported scenario end to end: long
// chat + many finished subagents + a streaming chunk per frame, with the
// subagent cache invalidated every 20 frames (simulating TTL expiry or an
// event-driven invalidation).
func BenchmarkScaleCombined(b *testing.B) {
	withTrueColor(b)

	type cfg struct{ msgs, subs int }
	for _, c := range []cfg{{40, 0}, {40, 200}, {6000, 0}, {6000, 200}} {
		m := buildScaleModel(b, c.msgs/2, c.subs)
		m.width, m.height = 200, 50
		m.processing = true
		_ = m.View() // warm
		chunk := outboundMsg{msg: bus.OutboundMessage{
			Channel: "tui", ChatID: m.currentKey, Event: "message.stream", Content: "hello ",
		}}
		b.Run(fmt.Sprintf("msgs%d_subs%d", c.msgs, c.subs), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = m.Update(chunk)
				viewSink = m.View()
				if i%20 == 19 {
					m.subagentsCache.at = time.Time{} // simulate TTL expiry
				}
			}
		})
	}
}

// BenchmarkGetSessionSubagents measures the agent-side listing of the
// subagents belonging to a session (the call behind the TUI subagent
// sidebar), with the subagent sessions still resident in memory.
func BenchmarkGetSessionSubagents(b *testing.B) {
	for _, n := range []int{0, 25, 100, 200, 400} {
		m := buildScaleModel(b, 200, n) // 400 messages + n subagent sessions
		ap := m.agentLoop.GetProvidable()
		key := "native:" + m.currentKey
		b.Run(fmt.Sprintf("n%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				scaleBenchSink = ap.GetSessionSubagents(key)
			}
		})
	}
}

// BenchmarkGetSessionSubagentsCold measures the same listing when the
// subagent sessions are NOT resident (the state after LRU eviction or
// retention cleanup, i.e. the steady state of a long-running server with many
// finished subagents): every subagent has to be reloaded from storage.
func BenchmarkGetSessionSubagentsCold(b *testing.B) {
	for _, n := range []int{0, 50, 200, 500} {
		m := buildScaleModel(b, 200, n)
		// Force every subagent session out of memory.
		for i := 0; i < n; i++ {
			m.sessionMgr.EvictSession(fmt.Sprintf("native:tui:chat:bench:subagent-%d", i))
		}
		ap := m.agentLoop.GetProvidable()
		key := "native:" + m.currentKey
		b.Run(fmt.Sprintf("cold%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				scaleBenchSink = ap.GetSessionSubagents(key)
			}
		})
	}
}

// BenchmarkUpdateSubagentsRefresh measures the OTHER side of T6: the listing
// lookup is not free, it is paid from Update() at most once per
// subagentsCacheTTL. This is the cost the frame no longer carries (compare with
// BenchmarkScaleSubagents, which renders the same model and must stay flat in
// the subagent count). Each iteration zeroes the cache clock, i.e. simulates
// the worst case of a refresh on every Update — the real cadence is one per 3 s.
func BenchmarkUpdateSubagentsRefresh(b *testing.B) {
	withTrueColor(b)

	for _, n := range []int{0, 10, 50, 200} {
		m := buildScaleModel(b, 200, n) // 400 messages
		m.width, m.height = 200, 50
		b.Run(fmt.Sprintf("subagents%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m.subagentsCache.at = time.Time{} // force the refresh
				m.refreshSubagentsCache()
			}
		})
	}
}

// BenchmarkMouseMotion measures a full frame triggered by an unconsumed mouse
// motion event (EnableMouseCellMotion reports every pixel of movement, and
// each one runs Update + View).
func BenchmarkMouseMotion(b *testing.B) {
	withTrueColor(b)

	for _, msgs := range []int{40, 6000} {
		m := buildScaleModel(b, msgs/2, 5)
		m.width, m.height = 200, 50
		_ = m.View() // warm
		ev := tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonRight, X: 40 + (msgs % 3), Y: 10}
		b.Run(fmt.Sprintf("motion_msgs%d", msgs), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = m.Update(ev)
				viewSink = m.View()
			}
		})
	}
}
