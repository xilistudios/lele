package tui

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

var scaleBenchSink []channels.SubagentTaskInfo

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

var scaleBenchSizes = []int{40, 400, 2000, 6000}

func withTrueColor(b *testing.B) {
	b.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	b.Cleanup(func() { lipgloss.SetColorProfile(old) })
}

func BenchmarkScaleView(b *testing.B) {
	withTrueColor(b)

	for _, msgs := range scaleBenchSizes {
		m := buildScaleModel(b, msgs/2, 5)
		m.width, m.height = 200, 50
		_ = m.View()
		b.Run(fmt.Sprintf("view_msgs%d", msgs), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				viewSink = m.View()
			}
		})
	}
}

func BenchmarkScaleStreamUpdate(b *testing.B) {
	withTrueColor(b)

	for _, msgs := range scaleBenchSizes {
		m := buildScaleModel(b, msgs/2, 5)
		m.width, m.height = 200, 50
		m.processing = true
		_ = m.View()
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

func BenchmarkScaleIdleUpdate(b *testing.B) {
	withTrueColor(b)

	for _, msgs := range []int{40, 2000, 6000} {
		m := buildScaleModel(b, msgs/2, 5)
		m.width, m.height = 200, 50
		_ = m.View()
		b.Run(fmt.Sprintf("idle_msgs%d", msgs), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = m.Update(tickMsg{})
				viewSink = m.View()
			}
		})
	}
}

func BenchmarkScaleRebuild(b *testing.B) {
	withTrueColor(b)

	for _, msgs := range scaleBenchSizes {
		m := buildScaleModel(b, msgs/2, 5)
		m.width, m.height = 200, 50
		_ = m.View()
		b.Run(fmt.Sprintf("newmsg_msgs%d", msgs), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				m.sessionMgr.AddMessage(m.currentKey, "user", fmt.Sprintf("q%d", i))
				m.sessionMgr.AddMessage(m.currentKey, "assistant", fmt.Sprintf("a%d", i))
				b.StartTimer()
				viewSink = m.View()
				b.StopTimer()
				m.sessionMgr.RemoveLastMessage(m.currentKey)
				m.sessionMgr.RemoveLastMessage(m.currentKey)
				b.StartTimer()
			}
		})
	}
}

func BenchmarkScaleSubagents(b *testing.B) {
	withTrueColor(b)

	for _, n := range []int{0, 10, 50, 200} {
		m := buildScaleModel(b, 200, n)
		m.width, m.height = 200, 50
		_ = m.View()
		b.Run(fmt.Sprintf("subagents%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m.subagentsCache.at = time.Time{}
				viewSink = m.View()
			}
		})
	}
}

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

func BenchmarkScaleSessionsRefresh(b *testing.B) {
	withTrueColor(b)

	for _, n := range []int{0, 10, 50, 200} {
		m := buildScaleModel(b, 200, n)
		m.width, m.height = 200, 50
		b.Run(fmt.Sprintf("sessions%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m.sessionsCache = sessionsCacheState{}
				m.reloadSessions()
			}
		})
	}
}

var (
	scaleBenchListSink  []*session.Session
	scaleBenchCountSink map[string]int
)

func BenchmarkScaleSessionListReads(b *testing.B) {
	for _, n := range []int{0, 200} {
		m := buildScaleModel(b, 200, n)
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

func BenchmarkScaleCombined(b *testing.B) {
	withTrueColor(b)

	type cfg struct{ msgs, subs int }
	for _, c := range []cfg{{40, 0}, {40, 200}, {6000, 0}, {6000, 200}} {
		m := buildScaleModel(b, c.msgs/2, c.subs)
		m.width, m.height = 200, 50
		m.processing = true
		_ = m.View()
		chunk := outboundMsg{msg: bus.OutboundMessage{
			Channel: "tui", ChatID: m.currentKey, Event: "message.stream", Content: "hello ",
		}}
		b.Run(fmt.Sprintf("msgs%d_subs%d", c.msgs, c.subs), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = m.Update(chunk)
				viewSink = m.View()
				if i%20 == 19 {
					m.subagentsCache.at = time.Time{}
				}
			}
		})
	}
}

func BenchmarkGetSessionSubagents(b *testing.B) {
	for _, n := range []int{0, 25, 100, 200, 400} {
		m := buildScaleModel(b, 200, n)
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

func BenchmarkGetSessionSubagentsCold(b *testing.B) {
	for _, n := range []int{0, 50, 200, 500} {
		m := buildScaleModel(b, 200, n)
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

func BenchmarkUpdateSubagentsRefresh(b *testing.B) {
	withTrueColor(b)

	for _, n := range []int{0, 10, 50, 200} {
		m := buildScaleModel(b, 200, n)
		m.width, m.height = 200, 50
		b.Run(fmt.Sprintf("subagents%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				m.subagentsCache.at = time.Time{}
				m.refreshSubagentsCache()
			}
		})
	}
}

func BenchmarkMouseMotion(b *testing.B) {
	withTrueColor(b)

	for _, msgs := range []int{40, 6000} {
		m := buildScaleModel(b, msgs/2, 5)
		m.width, m.height = 200, 50
		_ = m.View()
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
