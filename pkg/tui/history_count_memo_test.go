package tui

import (
	"fmt"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func historyCountScanCounter(t *testing.T, m *Model) func() int {
	t.Helper()
	scans := 0
	m.onHistoryCountScan = func() { scans++ }
	t.Cleanup(func() { m.onHistoryCountScan = nil })
	return func() int { return scans }
}

func TestHistoryCountMemo_MatchesRolescanAfterEveryChange(t *testing.T) {
	const key = "tui:chat:count-memo-changes"
	m := newFrameReadTestModel(t, key, 1)

	cases := []struct {
		name   string
		change func()
	}{
		{"append user message", func() {
			m.sessionMgr.AddMessage(key, "user", "another question")
		}},
		{"append assistant message", func() {
			m.sessionMgr.AddMessage(key, "assistant", "another answer")
		}},
		{"append tool result (rendered, never counted)", func() {
			m.sessionMgr.AddMessage(key, "tool", "tool output")
		}},
		{"append system message (not counted)", func() {
			m.sessionMgr.AddMessage(key, "system", "system prompt")
		}},
		{"streaming chunk creating the in-progress assistant", func() {
			m.sessionMgr.AppendAssistantChunk(key, "partial")
		}},
		{"streaming chunk growing it in place (length unchanged)", func() {
			m.sessionMgr.AppendAssistantChunk(key, " answer")
		}},
		{"finalize the in-progress assistant (replace last, length unchanged)", func() {
			m.sessionMgr.AddFullMessage(key, providers.Message{Role: "assistant", Content: "final answer"})
		}},
		{"same length, different roles (eviction/compaction)", func() {
			cur := m.sessionMgr.GetHistoryView(key)
			next := make([]providers.Message, len(cur))
			for i := range next {
				next[i].Role = "tool"
			}
			next[0].Role = "user"
			m.sessionMgr.SetHistory(key, next)
		}},
		{"empty history", func() {
			m.sessionMgr.SetHistory(key, nil)
		}},
		{"refilled history (compaction summary counts as a user message)", func() {
			m.sessionMgr.SetHistory(key, []providers.Message{
				{Role: "user", Content: "## Summary of Previous Conversation\n\nolder turns"},
				{Role: "assistant", Content: "answer"},
			})
		}},
	}

	for _, tc := range cases {
		tc.change()

		history := m.historyView()
		want := countHistoryMessages(history)
		if got := m.historyMessageCount(history); got != want {
			t.Fatalf("%s: memoized count = %d, rolescan = %d", tc.name, got, want)
		}
		if got := m.historyMessageCount(history); got != want {
			t.Fatalf("%s: second access to the same snapshot = %d, want %d", tc.name, got, want)
		}
		if got := m.getHistoryMessageCount(); got != want {
			t.Fatalf("%s: frame count = %d, rolescan = %d", tc.name, got, want)
		}
		if len(history) > 0 {
			if !m.countMemo.valid || m.countMemo.n != len(history) || m.countMemo.head != &history[0] {
				t.Fatalf("%s: memo does not describe the current snapshot: %+v (len=%d)",
					tc.name, m.countMemo, len(history))
			}
			if m.countMemo.count != want {
				t.Fatalf("%s: memo holds count %d, rolescan = %d", tc.name, m.countMemo.count, want)
			}
		}
	}
}

func TestHistoryCountMemo_SeparatesSessionsOfEqualLength(t *testing.T) {
	const keyA, keyB = "tui:chat:count-memo-a", "tui:chat:count-memo-b"
	m := newTestModel(t)
	m.sessionMgr.GetOrCreate(keyA)
	m.sessionMgr.GetOrCreate(keyB)

	m.sessionMgr.SetHistory(keyA, []providers.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
	})
	m.sessionMgr.SetHistory(keyB, []providers.Message{
		{Role: "user", Content: "q1"},
		{Role: "tool", Content: "t1"},
		{Role: "tool", Content: "t2"},
		{Role: "tool", Content: "t3"},
	})

	for _, tc := range []struct {
		key  string
		want int
	}{
		{keyA, 4}, {keyB, 1}, {keyA, 4}, {keyB, 1},
	} {
		m.currentKey = tc.key
		if got := m.getHistoryMessageCount(); got != tc.want {
			t.Fatalf("session %s resolved count %d, want %d (stale cross-session memo)", tc.key, got, tc.want)
		}
	}
}

func TestHistoryCountMemo_ScansOncePerSnapshot(t *testing.T) {
	t.Run("repeated accesses to one snapshot", func(t *testing.T) {
		const key = "tui:chat:count-memo-o1-access"
		m := newFrameReadTestModel(t, key, 30)
		history := m.historyView()
		want := countHistoryMessages(history)

		m.countMemo = historyCountMemo{}
		scans := historyCountScanCounter(t, m)
		const accesses = 50
		for i := 0; i < accesses; i++ {
			if got := m.historyMessageCount(history); got != want {
				t.Fatalf("access %d returned %d, want %d", i, got, want)
			}
		}
		if got := scans(); got != 1 {
			t.Fatalf("%d accesses to one snapshot performed %d rolescans, want 1", accesses, got)
		}
	})

	t.Run("one rebuild", func(t *testing.T) {
		const key = "tui:chat:count-memo-o1-rebuild"
		m := newFrameReadTestModel(t, key, 30)
		m.sessionMgr.AddMessage(key, "tool", "tool output")

		scans := historyCountScanCounter(t, m)
		m.updateViewport()
		if got := scans(); got != 1 {
			t.Fatalf("one rebuild performed %d rolescans, want 1 (frame read + rebuild share the memo)", got)
		}
		history := m.historyView()
		if want := countHistoryMessages(history); m.renderedBaseMsgCount != want {
			t.Fatalf("rebuild materialized count %d, want %d", m.renderedBaseMsgCount, want)
		}
	})

	t.Run("idle frames", func(t *testing.T) {
		const key = "tui:chat:count-memo-o1-idle"
		m := newFrameReadTestModel(t, key, 30)
		_ = m.View()

		scans := historyCountScanCounter(t, m)
		for i := 0; i < 5; i++ {
			if frame := m.View(); frame == "" {
				t.Fatal("View() returned an empty frame")
			}
		}
		if got := scans(); got != 0 {
			t.Fatalf("5 idle frames performed %d rolescans, want 0 (unchanged snapshot, memo hit)", got)
		}
	})

	t.Run("streaming frames rescan only when the snapshot changed", func(t *testing.T) {
		const key = "tui:chat:count-memo-o1-stream"
		m := newFrameReadTestModel(t, key, 30)
		startStreamingChunk(m, "partial answer")
		_ = m.View()

		scans := historyCountScanCounter(t, m)
		_ = m.View()
		if got := scans(); got != 0 {
			t.Fatalf("a frame with no new chunk performed %d rolescans, want 0", got)
		}

		m.sessionMgr.AppendAssistantChunk(key, " more")
		_ = m.View()
		if got := scans(); got != 1 {
			t.Fatalf("a frame after one chunk performed %d rolescans, want 1", got)
		}
	})
}

func TestHistoryCountMemo_KeepsCountSemantics(t *testing.T) {
	const key = "tui:chat:count-memo-semantics"
	m := newFrameReadTestModel(t, key, 0)

	history := []providers.Message{
		{Role: "system", Content: "prompt"},
		{Role: "user", Content: "## Summary of Previous Conversation\n\nolder"},
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "answer"},
		{Role: "tool", Content: "tool output"},
		{Role: "assistant", Content: "", Streaming: true},
	}
	m.sessionMgr.SetHistory(key, history)

	got := m.historyMessageCount(m.historyView())
	if want := countHistoryMessages(history); got != want {
		t.Fatalf("memoized count = %d, rolescan = %d", got, want)
	}
	if got != 4 {
		t.Fatalf("memoized count = %d, want 4 (2 user + 2 assistant; system/tool excluded)", got)
	}
	if got := m.getHistoryMessageCount(); got != 4 {
		t.Fatalf("frame count = %d, want 4", got)
	}
	if got := m.displayMessageCountFrom(m.historyMessageCount(m.historyView())); got != 4 {
		t.Fatalf("display count = %d, want 4", got)
	}
}

var historyCountMemoSink int

func BenchmarkHistoryMessageCount(b *testing.B) {
	for _, n := range []int{400, 2000, 6000} {
		history := make([]providers.Message, n)
		for i := range history {
			history[i].Role = "assistant"
		}
		want := n
		var m Model

		b.Run(fmt.Sprintf("memoized_msgs%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				historyCountMemoSink = m.historyMessageCount(history)
			}
			if historyCountMemoSink != want {
				b.Fatalf("memoized count = %d, want %d", historyCountMemoSink, want)
			}
		})
		b.Run(fmt.Sprintf("rolescan_msgs%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				historyCountMemoSink = countHistoryMessages(history)
			}
			if historyCountMemoSink != want {
				b.Fatalf("rolescan count = %d, want %d", historyCountMemoSink, want)
			}
		})
	}
}
