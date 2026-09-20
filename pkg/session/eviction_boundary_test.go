package session

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// TestEvictExcluded_TrailingExcludedRowStaysResident is the regression test for
// BLOCKER B1 (PR #335). Before the fix, EvictExcludedMessages used the last
// ExcludeFromContext=true row in the entire slice as the eviction boundary.
// The WebUI approval message (rest_chat.go:889-896) writes a trailing
// ExcludeFromContext=true row at the tail of the session. That row dragged
// evictUpTo to len(Messages), wiping all resident context (0/0).
//
// Fixture: 20 messages with human turns at 0, 2, 8. After
// ExcludeOldMessagesFromContext(key, 18) and Save, a trailing approval row
// (ExcludeFromContext=true) is appended. Eviction must preserve the kept tail
// and never evict past the compaction boundary.
func TestEvictExcluded_TrailingExcludedRowStaysResident(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	const key = "test:trailing-excluded"
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

	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// Exclude old messages (keepCount=2 → excludes indices [1, 18)).
	sm.ExcludeOldMessagesFromContext(key, 2)
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after exclude failed: %v", err)
	}

	// Simulate the WebUI approval message: a trailing ExcludeFromContext=true
	// row appended by persistApprovalMessage (rest_chat.go:889-896).
	sm.AddFullMessage(key, providers.Message{
		Role:               "tool",
		Content:            "✅ Command approved: `ls`",
		ToolCallID:         "approval:req-1",
		ExcludeFromContext: true,
	})
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after approval row failed: %v", err)
	}

	// Act: evict excluded messages from memory.
	evicted := sm.EvictExcludedMessages(key)
	t.Logf("evicted=%d", evicted)

	// Inspect the resident messages.
	hist := sm.GetHistory(key)
	t.Logf("resident messages after eviction: %d", len(hist))
	for i, m := range hist {
		t.Logf("  [%2d] role=%-10s excluded=%-5v content=%q", i, m.Role, m.ExcludeFromContext, m.Content)
	}

	// --- Assertion A: the kept tail must be non-empty ---
	// With the bug, evictUpTo reached len(Messages) (21), wiping everything.
	// After the fix, at least the 2 non-excluded messages (indices 18-19)
	// plus the trailing approval row (index 20) survive. Lower bound: ≥ 3.
	if len(hist) < 3 {
		t.Errorf("resident messages = %d, want >= 3 (kept tail must survive)", len(hist))
	}

	// --- Assertion B: non-excluded messages in memory > 0 ---
	// This is the assertion that makes the test non-vacuous. Before B1 fix
	// this was 0 (catastrophic).
	nonExcluded := 0
	for _, m := range hist {
		if !m.ExcludeFromContext {
			nonExcluded++
		}
	}
	if nonExcluded == 0 {
		t.Errorf("non-excluded messages in memory = 0; the bug wiped all resident context")
	}

	// --- Assertion C: preserved human turns survive in the summary ---
	// The pinned human turns (0, 2, 8) sit inside the evicted region and are
	// folded into the summary by foldEvictedIntoSummary.
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

// TestEvictExcluded_NoBoundaryFallsBackToContiguousRun verifies the fallback
// path when no compaction boundary has been recorded (cold load, direct call).
// EvictExcludedMessages must evict only the contiguous excluded run at the
// front, not a trailing excluded row at the tail — matching the pre-branch
// base behaviour.
func TestEvictExcluded_NoBoundaryFallsBackToContiguousRun(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	const key = "test:no-boundary-fallback"

	// Seed 10 messages.
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "assistant", msgContent(i))
	}

	// Mark excluded flags manually: [1..5] excluded (contiguous run after
	// index 0 which is NOT excluded), and index 9 also excluded (trailing).
	// Indices 6, 7, 8 are NOT excluded — they sit between the two runs.
	sm.mu.Lock()
	session, ok := sm.sessions[key]
	if !ok {
		sm.mu.Unlock()
		t.Fatalf("session not found")
	}
	// Index 0: not excluded
	session.Messages[0].ExcludeFromContext = false
	// Indices 1-5: excluded (contiguous run)
	for i := 1; i <= 5; i++ {
		session.Messages[i].ExcludeFromContext = true
	}
	// Indices 6-8: not excluded
	for i := 6; i <= 8; i++ {
		session.Messages[i].ExcludeFromContext = false
	}
	// Index 9: excluded (trailing, like a WebUI approval row)
	session.Messages[9].ExcludeFromContext = true
	// No excludeBoundary set (cold load / direct call).
	session.excludeBoundary = 0
	sm.mu.Unlock()

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Act: evict.
	evicted := sm.EvictExcludedMessages(key)
	t.Logf("evicted=%d", evicted)

	hist := sm.GetHistory(key)
	t.Logf("resident messages after eviction: %d", len(hist))
	for i, m := range hist {
		t.Logf("  [%2d] role=%-10s excluded=%-5v content=%q", i, m.Role, m.ExcludeFromContext, m.Content)
	}

	// Only the contiguous run [0..5] (indices 0 through 5 = 6 messages)
	// should be evicted. The trailing excluded row at index 9 must survive
	// because there's no compaction boundary and the fallback uses contiguous-
	// run logic. After eviction the resident slice is [6, 7, 8, 9].
	if evicted != 6 {
		t.Errorf("evicted = %d, want 6 (contiguous run [0..5])", evicted)
	}
	// Resident messages: originally 10, evicted 6 → 4.
	if len(hist) != 4 {
		t.Errorf("resident messages = %d, want 4", len(hist))
	}
	// The trailing excluded row (original index 9) must be present.
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

// msgContent generates a simple message content string for the i-th message.
func msgContent(i int) string {
	return "message-" + string(rune('A'+i))
}

// TestEvictExcluded_BoundaryEqualToLenKeepsContext verifies the structural
// invariant that EvictExcludedMessages never empties the entire resident
// slice when excludeBoundary == len(Messages). The anti-split guard in
// ExcludeOldMessagesFromContext pushes excludeUpTo to the end when the
// session tail is a tool-result group (an assistant with ≥2 tool_calls
// followed by their results). With the boundary preserved through SetHistory
// (the production path in summarizeSessionCore), the clamp would be inert
// and evictUpTo would reach len, wiping all context. The structural
// invariant caps evictUpTo at the last non-excluded message so at least one
// message survives.
func TestEvictExcluded_BoundaryEqualToLenKeepsContext(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:boundary-eq-len"

	// Build 6 messages: human turns at 0, 2. Assistant with 2 tool_calls at
	// index 3, tool results at indices 4 and 5. The anti-split guard in
	// ExcludeOldMessagesFromContext will push excludeUpTo past both tool
	// results to len=6, setting excludeBoundary=6.
	sm.AddMessage(key, "user", "initial request")           // 0
	sm.AddMessage(key, "assistant", "I'll look into that.") // 1
	sm.AddMessage(key, "user", "check both tools")          // 2
	sm.AddFullMessage(key, providers.Message{               // 3: assistant with 2 tool_calls
		Role:    "assistant",
		Content: "Let me search and grep.",
		ToolCalls: []providers.ToolCall{
			{ID: "call_search", Function: &providers.FunctionCall{Name: "web_search"}},
			{ID: "call_grep", Function: &providers.FunctionCall{Name: "exec"}},
		},
	})
	sm.AddFullMessage(key, providers.Message{ // 4: tool result for call_search
		Role:       "tool",
		Content:    "Found 5 repos",
		ToolCallID: "call_search",
	})
	sm.AddFullMessage(key, providers.Message{ // 5: tool result for call_grep
		Role:       "tool",
		Content:    "Found 3 files",
		ToolCallID: "call_grep",
	})

	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// Exclude with keepCount=2: excludeUpTo = 6 - 2 = 4.
	// Index 4 is a tool result → anti-split guard pushes to 5.
	// Index 5 is a tool result → pushed to 6 = len.
	// excludeBoundary = 6.
	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)
	t.Logf("after Exclude: len=%d boundary=%d", len(session.Messages), session.excludeBoundary)
	if session.excludeBoundary != 6 {
		t.Fatalf("precondition failed: excludeBoundary = %d, want 6 (= len)", session.excludeBoundary)
	}

	// Materialize a summary message via SetHistory (same slice, like
	// summarizeSessionCore does in the production path).
	sm.SetHistory(key, session.Messages)

	session = sm.GetOrCreate(key)
	t.Logf("after SetHistory: len=%d boundary=%d", len(session.Messages), session.excludeBoundary)
	// Boundary must survive (6 <= 6 = len).
	if session.excludeBoundary != 6 {
		t.Fatalf("boundary should survive SetHistory: got %d, want 6", session.excludeBoundary)
	}

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after SetHistory failed: %v", err)
	}

	// Act: evict.
	evicted := sm.EvictExcludedMessages(key)
	t.Logf("evicted=%d", evicted)

	// Inspect.
	hist := sm.GetHistory(key)
	t.Logf("resident after eviction: %d messages", len(hist))
	for i, m := range hist {
		t.Logf("  [%2d] role=%-10s excluded=%-5v content=%q", i, m.Role, m.ExcludeFromContext, m.Content)
	}

	// Assert: at least one non-excluded message must survive.
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
