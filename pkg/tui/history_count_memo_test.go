package tui

// T12 regression tests: the history message count is memoized per consumed slice.
//
// Context: countHistoryMessages runs an O(n) rolescan over the session history
// and the render path resolved it twice per rebuild — once for the frame's
// single read (historyView → frameCount) and once inside the rebuild itself
// (updateViewportWithHistory → residentCount). Both consume the SAME immutable
// copy-on-write snapshot, so the second scan was pure repeated work, paid on
// every throttled rebuild: at 6000 messages tens of µs of the rebuild that
// exist only because nobody remembered the first answer.
//
// The fix memoizes the count in Model.countMemo, keyed by the identity of the
// slice it belongs to (head element address + len). What the tests below pin:
//
//  1. the memoized value always equals the rolescan, after every kind of change
//     that can move the resident set — append, in-place replacement, a streaming
//     chunk that does not change the length, eviction/compaction that keeps the
//     length but changes the roles, emptying and refilling the history
//     (TestHistoryCountMemo_MatchesRolescanAfterEveryChange);
//  2. the key is the slice IDENTITY, not its length or a session name: two
//     sessions holding different roles at the same length never share a count
//     (TestHistoryCountMemo_SeparatesSessionsOfEqualLength);
//  3. the scan is O(1) amortized: N accesses to one snapshot cost one scan, one
//     rebuild costs one scan (the frame read and the rebuild share the memo) and
//     a frame over an unchanged snapshot costs none
//     (TestHistoryCountMemo_ScansOncePerSnapshot).
//
// Red-checks (each mutation applied alone, the tests run, then reverted — the
// failing test/line reported is the one that has to fail):
//
//	a) dropping the key comparison so a valid memo is always served ⇒
//	   (TestHistoryCountMemo_MatchesRolescanAfterEveryChange) fails on the first
//	   append ("memoized count = 3, rolescan = 4") and
//	   TestHistoryCountMemo_SeparatesSessionsOfEqualLength fails ("session …
//	   count-memo-b resolved count 4, want 1");
//	b) dropping the memo (always scanning) ⇒ (3) fails on all three parts: "50
//	   accesses to one snapshot performed 50 rolescans, want 1", "one rebuild
//	   performed 2 rolescans, want 1" and "5 idle frames performed 5 rolescans,
//	   want 0";
//	c) comparing only len(history) (the shape of the historyCountLen cache T2
//	   replaced) ⇒ the streaming step of (1) fails ("memo does not describe the
//	   current snapshot", same length, different slice) and (2) fails with the
//	   stale count above.

import (
	"fmt"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// historyCountScanCounter installs the model's rolescan hook and returns a
// function reporting how many scans ran since. The hook fires on the memo's miss
// path of Model.historyMessageCount — the only place countHistoryMessages is
// reached from the hot path — so the count is exhaustive.
func historyCountScanCounter(t *testing.T, m *Model) func() int {
	t.Helper()
	scans := 0
	m.onHistoryCountScan = func() { scans++ }
	t.Cleanup(func() { m.onHistoryCountScan = nil })
	return func() int { return scans }
}

// TestHistoryCountMemo_MatchesRolescanAfterEveryChange is the correctness half:
// whatever moves the resident set, the memoized count must be the count a plain
// rolescan of the same snapshot would produce. The cases are cumulative, so each
// one is verified against the state the previous ones left behind — including
// the ones that keep len(history) untouched and only move the roles.
func TestHistoryCountMemo_MatchesRolescanAfterEveryChange(t *testing.T) {
	const key = "tui:chat:count-memo-changes"
	m := newFrameReadTestModel(t, key, 1) // user + assistant

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
			// A compacted prefix that lands on other roles: same number of
			// messages, one counted instead of five. A key that only looked at
			// len(history) would answer with the pre-compaction count.
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
			// The count is role-based, exactly as countHistoryMessages defines
			// it: a compaction summary is a user message and IS counted, even
			// though the base renderer skips painting it.
			m.sessionMgr.SetHistory(key, []providers.Message{
				{Role: "user", Content: "## Summary of Previous Conversation\n\nolder turns"},
				{Role: "assistant", Content: "answer"},
			})
		}},
	}

	for _, tc := range cases {
		tc.change()

		history := m.historyView()
		want := countHistoryMessages(history) // the source of truth
		if got := m.historyMessageCount(history); got != want {
			t.Fatalf("%s: memoized count = %d, rolescan = %d", tc.name, got, want)
		}
		// The memo is stable for the same snapshot…
		if got := m.historyMessageCount(history); got != want {
			t.Fatalf("%s: second access to the same snapshot = %d, want %d", tc.name, got, want)
		}
		// …and the frame path (a fresh read that must resolve the same value)
		// agrees with the rolescan.
		if got := m.getHistoryMessageCount(); got != want {
			t.Fatalf("%s: frame count = %d, rolescan = %d", tc.name, got, want)
		}
		// The memo describes the snapshot that was just read, never an older
		// one (len == 0 has no identity to key on and is deliberately not
		// memoized).
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

// TestHistoryCountMemo_SeparatesSessionsOfEqualLength is the identity half: the
// key is the slice, not the length nor the session. Two sessions holding
// different roles at the same length must never share a memoized count — the
// shape the old historyCountLen cache got wrong.
func TestHistoryCountMemo_SeparatesSessionsOfEqualLength(t *testing.T) {
	const keyA, keyB = "tui:chat:count-memo-a", "tui:chat:count-memo-b"
	m := newTestModel(t)
	m.sessionMgr.GetOrCreate(keyA)
	m.sessionMgr.GetOrCreate(keyB)

	// Same length (4), different roles: A holds two turns, B holds one turn plus
	// three tool rows.
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

// TestHistoryCountMemo_ScansOncePerSnapshot is the O(1) half, in three parts:
// repeated accesses to one snapshot scan once, a rebuild scans once (its count
// consumers share the memo) and a frame over an unchanged snapshot scans
// nothing.
func TestHistoryCountMemo_ScansOncePerSnapshot(t *testing.T) {
	t.Run("repeated accesses to one snapshot", func(t *testing.T) {
		const key = "tui:chat:count-memo-o1-access"
		m := newFrameReadTestModel(t, key, 30) // 60 counted messages
		history := m.historyView()
		want := countHistoryMessages(history)

		m.countMemo = historyCountMemo{} // cold memo: the first access must pay
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
		m.sessionMgr.AddMessage(key, "tool", "tool output") // fresh snapshot: len +1

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
		_ = m.View() // cold frame: materializes everything

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
		_ = m.View() // no chunk arrived: the published snapshot is unchanged
		if got := scans(); got != 0 {
			t.Fatalf("a frame with no new chunk performed %d rolescans, want 0", got)
		}

		// A chunk mutates the in-progress message in place and leaves the
		// published snapshot stale, so the next read rebuilds the copy — a new
		// identity, i.e. one scan (never more: the two count consumers of the
		// rebuild still share it).
		m.sessionMgr.AppendAssistantChunk(key, " more")
		_ = m.View()
		if got := scans(); got != 1 {
			t.Fatalf("a frame after one chunk performed %d rolescans, want 1", got)
		}
	})
}

// TestHistoryCountMemo_KeepsCountSemantics pins WHAT is counted so the memo
// cannot silently drift from the rolescan it replaces: user and assistant rows
// (including a compaction summary, which is a user row) count, tool/system rows
// do not.
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

// historyCountMemoSink keeps the benchmarked results observable.
var historyCountMemoSink int

// BenchmarkHistoryMessageCount measures the accessor against the rolescan it
// replaces, at the sizes scale_bench_test.go uses. It documents the win the
// scan-counter tests assert structurally: the memoized access is a key compare
// (flat in the history length) while the rolescan grows linearly with it — and
// the hot path resolved the same snapshot twice per rebuild.
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
