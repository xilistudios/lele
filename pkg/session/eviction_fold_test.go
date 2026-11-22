package session

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestEvictExcluded_DoesNotFoldPreviousSummaryMessage(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:no-fold-summary"
	sm.GetOrCreate(key)

	for i := 0; i < 8; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddMessage(key, role, "filler-"+string(rune('A'+i)))
	}
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "human turn at 8"})
	sm.AddFullMessage(key, providers.Message{
		Role:    "user",
		Content: summaryMessageMarker + "OLD SUMMARY BODY",
	})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "human turn at 10"})
	for i := 11; i <= 13; i++ {
		sm.AddMessage(key, "assistant", "pad-"+string(rune('A'+i-11)))
	}
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "excluded answer", ExcludeFromContext: true})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "kept answer"})

	session := sm.GetOrCreate(key)
	session.Summary = ""

	session.excludeBoundary = 15

	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	evicted := sm.EvictExcludedMessages(key)
	if evicted != 15 {
		t.Fatalf("EvictExcludedMessages evicted %d, want 15", evicted)
	}

	summary := sm.GetSummary(key)
	if summary == "" {
		t.Fatal("summary should be non-empty after folding")
	}

	if count := strings.Count(summary, summaryMessageMarker); count > 0 {
		t.Errorf("summaryMessageMarker should not appear in folded summary (fix skipped it), got %d occurrences in:\n%s", count, summary)
	}

	if count := strings.Count(summary, "OLD SUMMARY BODY"); count != 0 {
		t.Errorf("OLD SUMMARY BODY should appear 0 times (empty initial summary), got %d", count)
	}

	if !strings.Contains(summary, "human turn at 8") {
		t.Error("summary should contain folded human turn at 8")
	}
	if !strings.Contains(summary, "human turn at 10") {
		t.Error("summary should contain folded human turn at 10")
	}

	hist := sm.GetHistoryView(key)
	if len(hist) != 1 {
		t.Fatalf("kept tail should be 1 message (index 15), got %d", len(hist))
	}
}
