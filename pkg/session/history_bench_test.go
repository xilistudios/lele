package session

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

var historyViewSink []providers.Message

func newBenchSessionStore(tb testing.TB) *store.Store {
	tb.Helper()
	s, err := store.Open(filepath.Join(tb.TempDir(), "bench.db"))
	if err != nil {
		tb.Fatalf("failed to open store: %v", err)
	}
	tb.Cleanup(func() { s.Close() })
	return s
}

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
