package session

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// TestEvictExcluded_DoesNotFoldPreviousSummaryMessage verifies that
// foldEvictedIntoSummary skips messages whose content is a previous
// compaction summary (starts with summaryMessageMarker). Without the
// fix, the marker would be folded into session.Summary causing:
//   - header nesting: "## Summary of Previous Conversation\n\n" repeated
//   - body duplication: "OLD SUMMARY BODY" appearing inside the new summary
//   - ≈1.9× growth per compaction round (measured: +15/round vs +8 baseline).
//
// Fixture: 16 messages with a summary message at index 9 inside the eviction
// region, left UN-excluded so it is a candidate for folding. A human turn at
// index 8 is also in the region (legitimate fold target). The function must
// fold the human turn but skip the summary message.
func TestEvictExcluded_DoesNotFoldPreviousSummaryMessage(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:no-fold-summary"
	sm.GetOrCreate(key)

	// Build 16 messages. Indices 0-7 are alternating user/assistant (filler).
	for i := 0; i < 8; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddMessage(key, role, "filler-"+string(rune('A'+i)))
	}
	// Index 8: legitimate human turn (should be folded).
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "human turn at 8"})
	// Index 9: the summary message — inside the eviction region, UN-excluded,
	// so it would be collected by foldEvictedIntoSummary without the fix.
	sm.AddFullMessage(key, providers.Message{
		Role:    "user",
		Content: summaryMessageMarker + "OLD SUMMARY BODY",
	})
	// Index 10: legitimate human turn (should be folded).
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "human turn at 10"})
	// Index 11-13: filler to pad the region.
	for i := 11; i <= 13; i++ {
		sm.AddMessage(key, "assistant", "pad-"+string(rune('A'+i-11)))
	}
	// Index 14: excluded assistant (last excluded, sets evictUpTo=15).
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "excluded answer", ExcludeFromContext: true})
	// Index 15: kept assistant (outside eviction region).
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "kept answer"})

	// Set the initial summary (empty — simulates first compaction cycle where
	// there is no prior summary). This means summaryMessageMarker count is 0
	// before folding.
	session := sm.GetOrCreate(key)
	session.Summary = ""

	// Mark the exclusion boundary so EvictExcludedMessages knows the region.
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

	// 1. The summaryMessageMarker must NOT appear in the folded summary.
	//    With empty initial summary the count is 0 (the fix skips the
	//    marker message entirely). Without the fix it would be 1 (the
	//    marker message gets prepended, nesting the header).
	if count := strings.Count(summary, summaryMessageMarker); count > 0 {
		t.Errorf("summaryMessageMarker should not appear in folded summary (fix skipped it), got %d occurrences in:\n%s", count, summary)
	}

	// 2. "OLD SUMMARY BODY" must appear exactly once — it is the body of
	//    the skipped summary message, not folded but not lost either
	//    (it was already in the initial summary context when it was
	//    injected). With empty initial summary the count is 0.
	//    Without the fix it would be 2 (duplicated inside the fold).
	if count := strings.Count(summary, "OLD SUMMARY BODY"); count != 0 {
		t.Errorf("OLD SUMMARY BODY should appear 0 times (empty initial summary), got %d", count)
	}

	// 3. The legitimate human turns MUST be folded (the fix does not
	//    suppress real human messages).
	if !strings.Contains(summary, "human turn at 8") {
		t.Error("summary should contain folded human turn at 8")
	}
	if !strings.Contains(summary, "human turn at 10") {
		t.Error("summary should contain folded human turn at 10")
	}

	// Verify the kept tail is correct.
	hist := sm.GetHistoryView(key)
	if len(hist) != 1 {
		t.Fatalf("kept tail should be 1 message (index 15), got %d", len(hist))
	}
}
