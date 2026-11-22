package session

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestEvictExcluded_FoldedSummaryIsPersisted(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const key = "test:fold-summary-persist"
	const llmSummary = "LLMSUMMARY"
	const human0 = "ORIGINAL GOAL: build the DHT crawler"
	const human2 = "FOLLOWUP ONE: does the routing table accept new nodes?"
	const human8 = "FOLLOWUP TWO: show me the failing test"

	sm.AddMessage(key, "user", human0)
	sm.AddMessage(key, "assistant", "I'll help with that.")
	sm.AddMessage(key, "user", human2)
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "Let me search for DHT implementations.",
		ToolCalls: []providers.ToolCall{{ID: "call_search", Function: &providers.FunctionCall{Name: "web_search"}}},
	})
	sm.AddFullMessage(key, providers.Message{
		Role:       "tool",
		Content:    "Found 3 DHT implementations on GitHub",
		ToolCallID: "call_search",
	})
	sm.AddMessage(key, "assistant", "The routing table accepts new nodes.")
	sm.AddMessage(key, "assistant", "Here is the architecture overview.")
	sm.AddMessage(key, "assistant", "I can also check the test suite for you.")
	sm.AddMessage(key, "user", human8)
	sm.AddMessage(key, "assistant", "Let me look at the tests.")
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "Searching for test files...",
		ToolCalls: []providers.ToolCall{{ID: "call_grep", Function: &providers.FunctionCall{Name: "exec"}}},
	})
	sm.AddFullMessage(key, providers.Message{
		Role:       "tool",
		Content:    "Found: dht_test.go:42 TestBootstrapNode",
		ToolCallID: "call_grep",
	})
	sm.AddMessage(key, "assistant", "The failing test is TestBootstrapNode.")
	sm.AddMessage(key, "assistant", "It fails because the mock peer returns stale data.")
	sm.AddMessage(key, "assistant", "You need to update the mock in setup_test.go.")
	sm.AddMessage(key, "assistant", "Specifically line 87 where the peer ID is set.")
	sm.AddMessage(key, "assistant", "Changing it to the new format should fix it.")
	sm.AddMessage(key, "assistant", "Would you like me to show the exact diff?")
	sm.AddMessage(key, "assistant", "Here is a summary of what we covered so far.")
	sm.AddMessage(key, "assistant", "Let me know if you have more questions.")

	sm.SetSummary(key, llmSummary)
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	sm.ExcludeOldMessagesFromContext(key, 2)
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after exclude failed: %v", err)
	}

	evicted := sm.EvictExcludedMessages(key)
	if evicted != 18 {
		t.Fatalf("expected 18 evicted, got %d", evicted)
	}

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

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
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
	if len(coldHist) != 2 {
		t.Errorf("cold-loaded history length = %d, want 2", len(coldHist))
	}
	for i, m := range coldHist {
		if m.ExcludeFromContext {
			t.Errorf("cold-loaded history[%d] has ExcludeFromContext=true (resurrected excluded row)", i)
		}
	}

	if meta.FirstInMemorySeq != 18 {
		t.Errorf("SQLite FirstInMemorySeq = %d, want 18", meta.FirstInMemorySeq)
	}
}
