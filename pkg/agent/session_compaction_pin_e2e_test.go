// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// End-to-end tests for the "preserve last N human user messages during
// compaction" feature. These tests exercise the full summarise→exclude→evict
// pipeline through the public agent/session API so they prove the contract
// the user cares about: the two most recent human messages are still present
// in the request that goes to the model after a compaction (or verbatim in
// the session summary when eviction is enabled).

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/store"
)

// TestCompaction_PreservesLastTwoHumanUserMessages_E2E proves that the two
// most recent human user messages survive compaction at the level the user
// cares about: after summarization they are still present (not excluded) in
// the history that filterContextMessages sends to the model.
//
// Conversation layout (20 messages, human turns at 0, 2, 8):
//
//	0: user   "ORIGINAL GOAL: build the DHT crawler"
//	1: assistant "I'll help with that."
//	2: user   "FOLLOWUP ONE: does the routing table accept new nodes?"
//	3: assistant (tool_use: call_search)
//	4: tool   result for call_search
//	5: assistant "The routing table does accept new nodes."
//	6: assistant "Here's a summary of the architecture."
//	7: assistant "Let me check one more thing."
//	8: user   "FOLLOWUP TWO: show me the failing test"
//	9–19: assistant/tool filler
//
// With keepCount=2, excludeUpTo=18. The last two human turns (indices 2 and
// 8) sit inside the excluded range [1, 18) and would be excluded by the old
// index-0-only rule. The pin feature keeps them un-excluded.
func TestCompaction_PreservesLastTwoHumanUserMessages_E2E(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pin-e2e-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	testSessionMgr := session.NewSessionManager()
	al.registry.SetSharedSessionManager(testSessionMgr)

	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Summary: User wants to build a DHT crawler and debug a failing test.",
			ToolCalls: []providers.ToolCall{},
		},
	}

	const sessionKey = "test:pin-e2e"
	const human0 = "ORIGINAL GOAL: build the DHT crawler"
	const human2 = "FOLLOWUP ONE: does the routing table accept new nodes?"
	const human8 = "FOLLOWUP TWO: show me the failing test"

	// --- Seed 20 messages: human at 0, 2, 8; rest assistant/tool ---
	agent.Sessions.AddMessage(sessionKey, "user", human0)                                    // 0
	agent.Sessions.AddMessage(sessionKey, "assistant", "I'll help you build a DHT crawler.") // 1
	agent.Sessions.AddMessage(sessionKey, "user", human2)                                    // 2
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{                             // 3
		Role:      "assistant",
		Content:   "Let me search for DHT implementations.",
		ToolCalls: []providers.ToolCall{{ID: "call_search", Function: &providers.FunctionCall{Name: "web_search"}}},
	})
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{ // 4
		Role:       "tool",
		Content:    "Found 3 DHT implementations on GitHub",
		ToolCallID: "call_search",
	})
	agent.Sessions.AddMessage(sessionKey, "assistant", "The routing table accepts new nodes via Kademlia XOR distance.") // 5
	agent.Sessions.AddMessage(sessionKey, "assistant", "Here is the architecture overview.")                             // 6
	agent.Sessions.AddMessage(sessionKey, "assistant", "I can also check the test suite for you.")                       // 7
	agent.Sessions.AddMessage(sessionKey, "user", human8)                                                                // 8
	agent.Sessions.AddMessage(sessionKey, "assistant", "Let me look at the tests.")                                      // 9
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{                                                         // 10
		Role:      "assistant",
		Content:   "Searching for test files...",
		ToolCalls: []providers.ToolCall{{ID: "call_grep", Function: &providers.FunctionCall{Name: "exec"}}},
	})
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{ // 11
		Role:       "tool",
		Content:    "Found: dht_test.go:42 TestBootstrapNode",
		ToolCallID: "call_grep",
	})
	agent.Sessions.AddMessage(sessionKey, "assistant", "The failing test is TestBootstrapNode.")             // 12
	agent.Sessions.AddMessage(sessionKey, "assistant", "It fails because the mock peer returns stale data.") // 13
	agent.Sessions.AddMessage(sessionKey, "assistant", "You need to update the mock in setup_test.go.")      // 14
	agent.Sessions.AddMessage(sessionKey, "assistant", "Specifically line 87 where the peer ID is set.")     // 15
	agent.Sessions.AddMessage(sessionKey, "assistant", "Changing it to the new format should fix it.")       // 16
	agent.Sessions.AddMessage(sessionKey, "assistant", "Would you like me to show the exact diff?")          // 17
	agent.Sessions.AddMessage(sessionKey, "assistant", "Here is a summary of what we covered so far.")       // 18
	agent.Sessions.AddMessage(sessionKey, "assistant", "Let me know if you have more questions.")            // 19

	beforeHistory := agent.Sessions.GetHistory(sessionKey)
	if len(beforeHistory) != 20 {
		t.Fatalf("Expected 20 seeded messages, got %d", len(beforeHistory))
	}

	// --- Act: summarise (which internally calls ExcludeOldMessagesFromContext
	// with keepCount=2 and then CompactSession) ---
	stats, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		t.Fatalf("summarizeSessionWithError failed: %v", err)
	}
	if stats == nil {
		t.Fatal("Expected non-nil stats from summarizeSessionWithError")
	}
	t.Logf("Compaction stats: before=%d after=%d dropped=%d", stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages)

	history := agent.Sessions.GetHistory(sessionKey)

	// --- Print full index→(role, excluded) map for diagnostics ---
	t.Log("=== Post-compaction message map ===")
	for i, m := range history {
		t.Logf("  [%2d] role=%-10s excluded=%-5v toolCallID=%q content=%q",
			i, m.Role, m.ExcludeFromContext, m.ToolCallID,
			truncForLog(m.Content, 60))
	}

	// --- 1. PRECONDITION: the last two human turns sit inside the excluded
	// region and would be excluded without the pin ---
	lastHumanIdx := 8
	nextAfterLastHuman := lastHumanIdx + 1
	if !history[nextAfterLastHuman].ExcludeFromContext {
		t.Fatalf("PRECONDITION FAILED: message[%d] (after last human turn) should be excluded but isn't — test layout does not prove pin necessity",
			nextAfterLastHuman)
	}

	// At least one plain assistant/tool message inside [1, excludeUpTo) is
	// excluded — proves the region is non-trivial.
	foundExcludedInRange := false
	for i := 1; i < 18; i++ {
		if history[i].ExcludeFromContext && i != 2 && i != 8 {
			foundExcludedInRange = true
			break
		}
	}
	if !foundExcludedInRange {
		t.Fatal("PRECONDITION FAILED: no plain message in [1,18) is excluded — test layout does not prove pin necessity")
	}

	// --- 2. PIN ASSERTIONS: the 3 human turns are NOT excluded ---
	for _, idx := range []int{0, 2, 8} {
		if history[idx].ExcludeFromContext {
			t.Errorf("message[%d] (human turn) should NOT be excluded but is", idx)
		}
	}

	// --- 3. CONTRACT: filterContextMessages contains all 3 human turns verbatim ---
	ctx := filterContextMessages(history)
	t.Log("=== Filtered context (what the model sees) ===")
	for i, m := range ctx {
		t.Logf("  [%2d] role=%-10s content=%q", i, m.Role, truncForLog(m.Content, 80))
	}

	assertContextContains(t, ctx, human0)
	assertContextContains(t, ctx, human2)
	assertContextContains(t, ctx, human8)
}

// TestCompaction_PreservesLastTwoHumanUserMessages_E2E/eviction_enabled is
// the eviction-enabled variant. With EvictExcludedFromMemory=true (the
// production default) and a real SQLite store, excluded messages are removed
// from the in-memory slice after persistence. The preserved holes (index 0
// plus the last 2 human turns) are folded into the session summary so no
// information is lost.
func TestCompaction_PreservesLastTwoHumanUserMessages_E2E_eviction(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pin-e2e-evict-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
		Session: config.SessionConfig{
			EvictExcludedFromMemory: true,
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	testSessionMgr := session.NewSessionManager()
	al.registry.SetSharedSessionManager(testSessionMgr)

	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}

	// Set up a real SQLite store so eviction works.
	storePath := filepath.Join(tmpDir, "pin-e2e.db")
	s, err := store.Open(storePath)
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer s.Close()
	agent.Sessions.SetSessionRepo(s.Sessions())

	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Summary: User wants to build a DHT crawler and debug a failing test.",
			ToolCalls: []providers.ToolCall{},
		},
	}

	const sessionKey = "test:pin-e2e-evict"
	const human0 = "ORIGINAL GOAL: build the DHT crawler"
	const human2 = "FOLLOWUP ONE: does the routing table accept new nodes?"
	const human8 = "FOLLOWUP TWO: show me the failing test"

	// Seed the same 20-message conversation.
	agent.Sessions.AddMessage(sessionKey, "user", human0)                                    // 0
	agent.Sessions.AddMessage(sessionKey, "assistant", "I'll help you build a DHT crawler.") // 1
	agent.Sessions.AddMessage(sessionKey, "user", human2)                                    // 2
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{                             // 3
		Role:      "assistant",
		Content:   "Let me search for DHT implementations.",
		ToolCalls: []providers.ToolCall{{ID: "call_search", Function: &providers.FunctionCall{Name: "web_search"}}},
	})
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{ // 4
		Role:       "tool",
		Content:    "Found 3 DHT implementations on GitHub",
		ToolCallID: "call_search",
	})
	agent.Sessions.AddMessage(sessionKey, "assistant", "The routing table accepts new nodes via Kademlia XOR distance.") // 5
	agent.Sessions.AddMessage(sessionKey, "assistant", "Here is the architecture overview.")                             // 6
	agent.Sessions.AddMessage(sessionKey, "assistant", "I can also check the test suite for you.")                       // 7
	agent.Sessions.AddMessage(sessionKey, "user", human8)                                                                // 8
	agent.Sessions.AddMessage(sessionKey, "assistant", "Let me look at the tests.")                                      // 9
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{                                                         // 10
		Role:      "assistant",
		Content:   "Searching for test files...",
		ToolCalls: []providers.ToolCall{{ID: "call_grep", Function: &providers.FunctionCall{Name: "exec"}}},
	})
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{ // 11
		Role:       "tool",
		Content:    "Found: dht_test.go:42 TestBootstrapNode",
		ToolCallID: "call_grep",
	})
	agent.Sessions.AddMessage(sessionKey, "assistant", "The failing test is TestBootstrapNode.")             // 12
	agent.Sessions.AddMessage(sessionKey, "assistant", "It fails because the mock peer returns stale data.") // 13
	agent.Sessions.AddMessage(sessionKey, "assistant", "You need to update the mock in setup_test.go.")      // 14
	agent.Sessions.AddMessage(sessionKey, "assistant", "Specifically line 87 where the peer ID is set.")     // 15
	agent.Sessions.AddMessage(sessionKey, "assistant", "Changing it to the new format should fix it.")       // 16
	agent.Sessions.AddMessage(sessionKey, "assistant", "Would you like me to show the exact diff?")          // 17
	agent.Sessions.AddMessage(sessionKey, "assistant", "Here is a summary of what we covered so far.")       // 18
	agent.Sessions.AddMessage(sessionKey, "assistant", "Let me know if you have more questions.")            // 19

	// Simulate a trailing WebUI approval row as production writes it
	// (pkg/channels/rest_chat.go:persistApprovalMessage). This row has
	// ExcludeFromContext=true and sits at the end of the history. It is the
	// real-world trigger for the eviction-boundary bug: without the boundary
	// clamp, its ExcludeFromContext=true drags evictUpTo to len(Messages),
	// wiping all resident context including the un-excluded summary message.
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{ // 20
		Role:               "tool",
		Content:            "✅ Command approved: `ls`",
		ToolCallID:         "approval:test-req-1",
		ExcludeFromContext: true,
	})

	// Save so the store has all 21 rows before summarization.
	if err := agent.Sessions.Save(sessionKey); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// --- Act: summarise with eviction (EvictExcludedFromMemory defaults to true) ---
	stats, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		t.Fatalf("summarizeSessionWithError failed: %v", err)
	}
	if stats == nil {
		t.Fatal("Expected non-nil stats")
	}
	t.Logf("Compaction stats: before=%d after=%d dropped=%d", stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages)

	// --- (a) RAM invariant: the kept tail must not be empty and the
	// filtered context sent to the model must be non-empty. With main's
	// rule (evictUpTo = lastExcluded + 1, no boundary clamp), the trailing
	// approval row drags evictUpTo past the un-excluded summary message,
	// wiping all resident context — the vacuity guards below catch that.
	// The approval row itself is legitimately excluded from context (that
	// is its production contract) and stays resident above the boundary;
	// it is filtered out before the model sees it.
	hist := agent.Sessions.GetHistory(sessionKey)
	t.Log("=== Post-eviction in-memory history ===")
	for i, m := range hist {
		t.Logf("  [%2d] role=%-10s excluded=%-5v content=%q",
			i, m.Role, m.ExcludeFromContext, truncForLog(m.Content, 60))
	}
	const keepCount = 2
	if len(hist) < keepCount {
		t.Errorf("history length = %d, want >= %d (vacuity guard: eviction wiped the kept tail)", len(hist), keepCount)
	}
	ctxEvict := filterContextMessages(hist)
	if len(ctxEvict) == 0 {
		t.Error("filterContextMessages(hist) is empty (vacuity guard: no context reaches the model)")
	}
	// The compaction region must leave RAM entirely: the resident slice is
	// exactly the kept tail (keepCount messages), never more. Before this PR
	// the clamp could over-evict (empty slice, caught above) and, without the
	// boundary surviving SetHistory, it could under-evict (extra excluded rows
	// resident, caught here).
	if len(hist) != keepCount {
		t.Errorf("resident history = %d messages, want exactly %d (the kept tail; the compaction region must not stay in RAM)", len(hist), keepCount)
	}

	// --- (b) The pinned human turns appear verbatim in the session summary.
	// With eviction, the preserved holes (index 0, 2, 8) are folded into the
	// summary by foldEvictedIntoSummary because they sit inside the eviction
	// region and are removed from the in-memory slice. ---
	summary := agent.Sessions.GetSummary(sessionKey)
	t.Logf("Session summary after eviction: %q", summary)
	if strings.TrimSpace(summary) == "" {
		t.Error("Session summary should be non-empty after eviction")
	}

	// The 3 human turns must appear verbatim in the summary (folded) OR in
	// the filtered context (if they survived in the kept tail). With our
	// layout, all 3 human turns are inside the eviction region (indices 0, 2, 8
	// < evictUpTo=18), so they are folded into the summary.
	ctx := filterContextMessages(hist)
	t.Log("=== Filtered context (post-eviction) ===")
	for i, m := range ctx {
		t.Logf("  [%2d] role=%-10s content=%q", i, m.Role, truncForLog(m.Content, 80))
	}

	// Check each human turn is either in the summary or in the filtered context.
	for _, tc := range []struct {
		label, content string
	}{
		{"human0", human0},
		{"human2", human2},
		{"human8", human8},
	} {
		inSummary := strings.Contains(summary, tc.content)
		inContext := contextContains(ctx, tc.content)
		if !inSummary && !inContext {
			t.Errorf("%s (%q) not found in summary or filtered context", tc.label, tc.content)
		} else {
			where := "summary"
			if inContext {
				where = "filtered context"
			}
			t.Logf("  %s found in %s", tc.label, where)
		}
	}

	// --- (c) SQLite rows: all 21 seeded messages must still exist ---
	rows, err := s.Sessions().LoadMessagesWithSeq(sessionKey)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	if len(rows) != 21 {
		t.Errorf("SQLite rows = %d, want 21 (no data loss after eviction)", len(rows))
	}

	// --- (d) Restart path: cold-load a fresh SessionManager over the SAME
	// store and verify the folded summary survives. Before the fix this
	// assertion would fail because EvictExcludedMessages never persisted the
	// folded summary to SQLite. ---
	smCold := session.NewSessionManager()
	smCold.SetSessionRepo(s.Sessions())
	coldSummary := smCold.GetSummary(sessionKey)
	t.Logf("Cold-loaded summary: %q", coldSummary)
	for _, tc := range []struct {
		label, content string
	}{
		{"human0", human0},
		{"human2", human2},
		{"human8", human8},
	} {
		if !strings.Contains(coldSummary, tc.content) {
			t.Errorf("cold-loaded summary missing %s: got %q", tc.label, coldSummary)
		}
	}

	coldHist := smCold.GetHistory(sessionKey)
	t.Logf("Cold-loaded history length: %d", len(coldHist))
	for i, m := range coldHist {
		t.Logf("  [%2d] role=%-10s excluded=%-5v content=%q", i, m.Role, m.ExcludeFromContext, truncForLog(m.Content, 60))
	}
	// Verify: no compaction-excluded message resurrects on cold load.
	// The approval row is the only legitimately excluded row (its production
	// contract), so identify it exactly instead of exempting by role.
	foundApproval := false
	for i, m := range coldHist {
		if m.ExcludeFromContext && m.ToolCallID != "approval:test-req-1" {
			t.Errorf("cold-loaded history[%d] has ExcludeFromContext=true (resurrected excluded row): role=%s toolcall=%s content=%q", i, m.Role, m.ToolCallID, m.Content)
		}
		if m.ExcludeFromContext && m.ToolCallID == "approval:test-req-1" {
			foundApproval = true
		}
	}
	if !foundApproval {
		t.Error("cold-loaded history is missing the legitimate approval row (ExcludeFromContext=true, ToolCallID=approval:test-req-1)")
	}
}

// assertContextContains fails the test if none of the messages in ctx contain
// the expected substring verbatim.
func assertContextContains(t *testing.T, ctx []providers.Message, expected string) {
	t.Helper()
	for _, m := range ctx {
		if strings.Contains(m.Content, expected) {
			return
		}
	}
	t.Errorf("filtered context does not contain %q", expected)
}

// contextContains reports whether any message in ctx contains the expected
// substring.
func contextContains(ctx []providers.Message, expected string) bool {
	for _, m := range ctx {
		if strings.Contains(m.Content, expected) {
			return true
		}
	}
	return false
}

// truncForLog truncates a string for log output, replacing newlines.
func truncForLog(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
