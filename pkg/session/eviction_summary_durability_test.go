package session

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// TestEvictExcluded_FoldedSummaryIsPersisted proves that the folded summary
// (preserved human turns appended to session.Summary by foldEvictedIntoSummary)
// is durably persisted to SQLite — not just held in the in-memory struct.
//
// Before the fix, EvictExcludedMessages called foldEvictedIntoSummary which
// assigned session.Summary but never set session.metaDirty = true. The only
// store write was UpdateFirstInMemorySeq (which only persists the eviction
// boundary), and clearDirtyFlags() then wiped metaDirty. A cold restart would
// lose the folded text because only the LLM's summary was persisted (via the
// earlier SetSummary → Save).
//
// Layout: 20 messages with human turns at indices 0, 2, 8. After
// ExcludeOldMessagesFromContext(key, 2), indices 2 and 8 are pinned inside
// the excluded region [1, 18). EvictExcludedMessages folds all 3 human
// turns (0, 2, 8) into session.Summary and evicts 18 messages.
func TestEvictExcluded_FoldedSummaryIsPersisted(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	const key = "test:fold-summary-persist"
	const llmSummary = "LLMSUMMARY"
	const human0 = "ORIGINAL GOAL: build the DHT crawler"
	const human2 = "FOLLOWUP ONE: does the routing table accept new nodes?"
	const human8 = "FOLLOWUP TWO: show me the failing test"

	// Seed 20 messages: human at 0, 2, 8; rest assistant/tool filler.
	sm.AddMessage(key, "user", human0)                      // 0
	sm.AddMessage(key, "assistant", "I'll help with that.") // 1
	sm.AddMessage(key, "user", human2)                      // 2
	sm.AddFullMessage(key, providers.Message{               // 3
		Role:      "assistant",
		Content:   "Let me search for DHT implementations.",
		ToolCalls: []providers.ToolCall{{ID: "call_search", Function: &providers.FunctionCall{Name: "web_search"}}},
	})
	sm.AddFullMessage(key, providers.Message{ // 4
		Role:       "tool",
		Content:    "Found 3 DHT implementations on GitHub",
		ToolCallID: "call_search",
	})
	sm.AddMessage(key, "assistant", "The routing table accepts new nodes.")     // 5
	sm.AddMessage(key, "assistant", "Here is the architecture overview.")       // 6
	sm.AddMessage(key, "assistant", "I can also check the test suite for you.") // 7
	sm.AddMessage(key, "user", human8)                                          // 8
	sm.AddMessage(key, "assistant", "Let me look at the tests.")                // 9
	sm.AddFullMessage(key, providers.Message{                                   // 10
		Role:      "assistant",
		Content:   "Searching for test files...",
		ToolCalls: []providers.ToolCall{{ID: "call_grep", Function: &providers.FunctionCall{Name: "exec"}}},
	})
	sm.AddFullMessage(key, providers.Message{ // 11
		Role:       "tool",
		Content:    "Found: dht_test.go:42 TestBootstrapNode",
		ToolCallID: "call_grep",
	})
	sm.AddMessage(key, "assistant", "The failing test is TestBootstrapNode.")             // 12
	sm.AddMessage(key, "assistant", "It fails because the mock peer returns stale data.") // 13
	sm.AddMessage(key, "assistant", "You need to update the mock in setup_test.go.")      // 14
	sm.AddMessage(key, "assistant", "Specifically line 87 where the peer ID is set.")     // 15
	sm.AddMessage(key, "assistant", "Changing it to the new format should fix it.")       // 16
	sm.AddMessage(key, "assistant", "Would you like me to show the exact diff?")          // 17
	sm.AddMessage(key, "assistant", "Here is a summary of what we covered so far.")       // 18
	sm.AddMessage(key, "assistant", "Let me know if you have more questions.")            // 19

	// Set the LLM-produced summary (already persisted via SetSummary + Save).
	sm.SetSummary(key, llmSummary)
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// Exclude old messages (keepCount=2 → excludes indices [1, 18)).
	sm.ExcludeOldMessagesFromContext(key, 2)
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after exclude failed: %v", err)
	}

	// Act: evict excluded messages from memory.
	evicted := sm.EvictExcludedMessages(key)
	if evicted != 18 {
		t.Fatalf("expected 18 evicted, got %d", evicted)
	}

	// --- Assertion 1: in-memory summary contains all 3 human texts ---
	memSummary := sm.GetSummary(key)
	for _, tc := range []struct {
		label, text string
	}{
		{"human0", human0},
		{"human2", human2},
		{"human8", human8},
	} {
		if !strings.Contains(memSummary, tc.text) {
			t.Errorf("in-memory summary missing %s: %q", tc.label, memSummary)
		}
	}

	// --- Assertion 2 (THE FAILING ONE ON HEAD): direct SQLite read ---
	// This proves the folded summary was persisted, not just held in RAM.
	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil {
		t.Fatalf("GetSessionMeta failed: %v", err)
	}
	if meta == nil {
		t.Fatal("session meta not found in SQLite after eviction")
	}
	t.Logf("SQLite summary: %q", meta.Summary)
	for _, tc := range []struct {
		label, text string
	}{
		{"human0", human0},
		{"human2", human2},
		{"human8", human8},
	} {
		if !strings.Contains(meta.Summary, tc.text) {
			t.Errorf("SQLite summary missing %s: got %q", tc.label, meta.Summary)
		}
	}

	// --- Assertion 3: cold load recovers the folded summary + no excluded rows ---
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	coldSummary := sm2.GetSummary(key)
	for _, tc := range []struct {
		label, text string
	}{
		{"human0", human0},
		{"human2", human2},
		{"human8", human8},
	} {
		if !strings.Contains(coldSummary, tc.text) {
			t.Errorf("cold-loaded summary missing %s: %q", tc.label, coldSummary)
		}
	}

	coldHist := sm2.GetHistory(key)
	// Post-eviction tail has 2 messages (indices 18-19).
	if len(coldHist) != 2 {
		t.Errorf("cold-loaded history length = %d, want 2", len(coldHist))
	}
	for i, m := range coldHist {
		if m.ExcludeFromContext {
			t.Errorf("cold-loaded history[%d] has ExcludeFromContext=true (resurrected excluded row)", i)
		}
	}

	// --- Assertion 4: FirstInMemorySeq persisted correctly ---
	if meta.FirstInMemorySeq != 18 {
		t.Errorf("SQLite FirstInMemorySeq = %d, want 18", meta.FirstInMemorySeq)
	}
}
