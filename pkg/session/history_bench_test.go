package session

// Benchmark harness for SessionManager.GetHistoryView, the read the TUI
// render loop performs on every frame (and several times per frame).
//
// GetHistoryView returns the writer-published IMMUTABLE snapshot of the
// session's messages (copy-on-write, see view.go), so on the hot path the cost
// is O(1) and allocation-free: the reader just hands out a slice the writer
// already copied — the caller must not modify it. The O(n) copy only happens
// when no snapshot is published for the current Session.saveEpoch, i.e. once
// per read burst after a streaming chunk (streaming writers deliberately leave
// the snapshot stale rather than copy per chunk); GetHistoryView rebuilds and
// republishes it lazily in that case.
//
// This benchmark therefore measures the HOT path (a resident session whose
// snapshot is fresh), which is what the frame loop pays per call: it used to
// quantify the per-call defensive copy (328 µs / 1.16 MB at 6000 messages,
// docs/perf/tui-long-chat-baseline.md §4), the primitive the COW change
// replaced with a 32 ns / 0 B read.
//
// Baseline numbers live in docs/perf/tui-long-chat-baseline.md.
// Run with: make bench (or `go test -run '^$' -bench GetHistoryView ./pkg/session/`).

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

// historyViewSink keeps the returned slice alive so the compiler cannot elide
// the measured call.
var historyViewSink []providers.Message

// newBenchSessionStore opens a temporary SQLite store for benchmarks. It
// mirrors newTestStore (manager_sqlite_test.go) but accepts testing.TB.
func newBenchSessionStore(tb testing.TB) *store.Store {
	tb.Helper()
	s, err := store.Open(filepath.Join(tb.TempDir(), "bench.db"))
	if err != nil {
		tb.Fatalf("failed to open store: %v", err)
	}
	tb.Cleanup(func() { s.Close() })
	return s
}

// seedBenchHistory fills a session with `msgs` realistic user/assistant
// messages, the same shape used by the TUI scale benchmarks.
func seedBenchHistory(sm *SessionManager, key string, msgs int) {
	for i := 0; i < msgs; i++ {
		if i%2 == 0 {
			sm.AddMessage(key, "user", fmt.Sprintf("Question %d: how do I implement feature X with proper error handling and tests?", i))
			continue
		}
		sm.AddMessage(key, "assistant",
			fmt.Sprintf("Answer %d:\nHere is an approach:\n```go\nfunc feature%d() error {\n\tif err := doWork(); err != nil {\n\t\treturn fmt.Errorf(\"feature %d: %%w\", err)\n\t}\n\treturn nil\n}\n```\nThis handles the error path and is covered by tests.", i, i, i))
	}
}

// BenchmarkGetHistoryView measures the resident (hot path) read used by the
// TUI frame loop at each conversation size.
func BenchmarkGetHistoryView(b *testing.B) {
	for _, msgs := range []int{40, 2000, 6000} {
		b.Run(fmt.Sprintf("msgs%d", msgs), func(b *testing.B) {
			sm := NewSessionManager()
			sm.SetSessionRepo(newBenchSessionStore(b).Sessions())

			key := "tui:chat:bench"
			sm.GetOrCreate(key)
			seedBenchHistory(sm, key, msgs)
			if got := len(sm.GetHistoryView(key)); got != msgs {
				b.Fatalf("seeded %d messages, got %d back", msgs, got)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				historyViewSink = sm.GetHistoryView(key)
			}
		})
	}
}
