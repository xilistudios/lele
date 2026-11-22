package session

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestEvictExcluded_TrailingExcludedRowStaysResident(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const key = "test:trailing-excluded"
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

	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	sm.ExcludeOldMessagesFromContext(key, 2)
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after exclude failed: %v", err)
	}

	sm.AddFullMessage(key, providers.Message{
		Role:               "tool",
		Content:            "✅ Command approved: `ls`",
		ToolCallID:         "approval:req-1",
		ExcludeFromContext: true,
	})
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after approval row failed: %v", err)
	}

	evicted := sm.EvictExcludedMessages(key)
	t.Logf("evicted=%d", evicted)

	hist := sm.GetHistory(key)
	t.Logf("resident messages after eviction: %d", len(hist))
	for i, m := range hist {
		t.Logf("  [%2d] role=%-10s excluded=%-5v content=%q", i, m.Role, m.ExcludeFromContext, m.Content)
	}

	if len(hist) < 3 {
		t.Errorf("resident messages = %d, want >= 3 (kept tail must survive)", len(hist))
	}

	nonExcluded := 0
	for _, m := range hist {
		if !m.ExcludeFromContext {
			nonExcluded++
		}
	}
	if nonExcluded == 0 {
		t.Errorf("non-excluded messages in memory = 0; the bug wiped all resident context")
	}

	summary := sm.GetSummary(key)
	for _, tc := range []struct {
		label, text string
	}{
		{"human0", human0},
		{"human2", human2},
		{"human8", human8},
	} {
		if !strings.Contains(summary, tc.text) {
			t.Errorf("session summary missing %s: %q", tc.label, summary)
		}
	}
}

func TestEvictExcluded_NoBoundaryFallsBackToContiguousRun(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const key = "test:no-boundary-fallback"

	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "assistant", msgContent(i))
	}

	sm.mu.Lock()
	session, ok := sm.sessions[key]
	if !ok {
		sm.mu.Unlock()
		t.Fatalf("session not found")
	}
	session.Messages[0].ExcludeFromContext = false
	for i := 1; i <= 5; i++ {
		session.Messages[i].ExcludeFromContext = true
	}
	for i := 6; i <= 8; i++ {
		session.Messages[i].ExcludeFromContext = false
	}
	session.Messages[9].ExcludeFromContext = true
	session.excludeBoundary = 0
	sm.mu.Unlock()

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	evicted := sm.EvictExcludedMessages(key)
	t.Logf("evicted=%d", evicted)

	hist := sm.GetHistory(key)
	t.Logf("resident messages after eviction: %d", len(hist))
	for i, m := range hist {
		t.Logf("  [%2d] role=%-10s excluded=%-5v content=%q", i, m.Role, m.ExcludeFromContext, m.Content)
	}

	if evicted != 6 {
		t.Errorf("evicted = %d, want 6 (contiguous run [0..5])", evicted)
	}
	if len(hist) != 4 {
		t.Errorf("resident messages = %d, want 4", len(hist))
	}
	foundApproval := false
	for _, m := range hist {
		if m.ExcludeFromContext && m.Content == msgContent(9) {
			foundApproval = true
			break
		}
	}
	if !foundApproval {
		t.Error("trailing excluded row (index 9) was evicted — fallback is broken")
	}
}

func msgContent(i int) string {
	return "message-" + string(rune('A'+i))
}

func TestEvictExcluded_BoundaryEqualToLenKeepsContext(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:boundary-eq-len"

	sm.AddMessage(key, "user", "initial request")
	sm.AddMessage(key, "assistant", "I'll look into that.")
	sm.AddMessage(key, "user", "check both tools")
	sm.AddFullMessage(key, providers.Message{
		Role:    "assistant",
		Content: "Let me search and grep.",
		ToolCalls: []providers.ToolCall{
			{ID: "call_search", Function: &providers.FunctionCall{Name: "web_search"}},
			{ID: "call_grep", Function: &providers.FunctionCall{Name: "exec"}},
		},
	})
	sm.AddFullMessage(key, providers.Message{
		Role:       "tool",
		Content:    "Found 5 repos",
		ToolCallID: "call_search",
	})
	sm.AddFullMessage(key, providers.Message{
		Role:       "tool",
		Content:    "Found 3 files",
		ToolCallID: "call_grep",
	})

	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	sm.mu.Lock()
	session, ok := sm.sessions[key]
	if !ok {
		sm.mu.Unlock()
		t.Fatalf("session %q not resident", key)
	}
	session.excludeBoundary = len(session.Messages)
	for i := 1; i < len(session.Messages); i++ {
		session.Messages[i].ExcludeFromContext = true
	}
	sm.mu.Unlock()

	session = sm.GetOrCreate(key)
	t.Logf("after setup: len=%d boundary=%d", len(session.Messages), session.excludeBoundary)
	if session.excludeBoundary != 6 {
		t.Fatalf("precondition failed: excludeBoundary = %d, want 6 (= len)", session.excludeBoundary)
	}

	sm.SetHistory(key, session.Messages)

	session = sm.GetOrCreate(key)
	t.Logf("after SetHistory: len=%d boundary=%d", len(session.Messages), session.excludeBoundary)
	if session.excludeBoundary != 6 {
		t.Fatalf("boundary should survive SetHistory: got %d, want 6", session.excludeBoundary)
	}

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after SetHistory failed: %v", err)
	}

	evicted := sm.EvictExcludedMessages(key)
	t.Logf("evicted=%d", evicted)

	hist := sm.GetHistory(key)
	t.Logf("resident after eviction: %d messages", len(hist))
	for i, m := range hist {
		t.Logf("  [%2d] role=%-10s excluded=%-5v content=%q", i, m.Role, m.ExcludeFromContext, m.Content)
	}

	nonExcluded := 0
	for _, m := range hist {
		if !m.ExcludeFromContext {
			nonExcluded++
		}
	}
	if nonExcluded == 0 {
		t.Errorf("all non-excluded context wiped — the structural invariant failed (boundary==len case)")
	}
	if len(hist) == 0 {
		t.Errorf("entire resident slice emptied — eviction wiped everything")
	}
}
