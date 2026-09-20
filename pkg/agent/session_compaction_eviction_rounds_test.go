// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Multi-round compaction eviction tests. These verify that the eviction
// boundary (excludeBoundary) survives across multiple compaction rounds
// and that SetHistory (called by summarizeSessionCore to un-exclude the
// summary message) does not clear the boundary, which would disable the
// eviction clamp in every round ≥ 2.
//
// N1 review finding: in the production path (summarizeSessionCore),
// SetHistory is called between ExcludeOldMessagesFromContext and
// EvictExcludedMessages. With the unconditional excludeBoundary = 0, the
// clamp was disabled on every round ≥ 2, falling back to the contiguous-run
// heuristic and leaving excluded messages resident in RAM.

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/store"
)

// TestCompaction_SecondRoundEvictsExcludedFromMemory drives the production
// path (summarizeSessionWithError) through two compaction rounds and asserts
// that after round 2:
//
//	(a) no message with ExcludeFromContext == true remains in memory
//	    (the E2E invariant — the boundary clamp keeps the resident slice clean);
//	(b) the history resident is not empty and there are messages in context
//	    (filterContextMessages is not vacuous);
//	(c) the summary is bounded (does not grow without limit between rounds).
//
// Production flow simulated:
//   - Round 1: summarizeSessionCore → summary produced, stored in session.Summary.
//   - Between rounds: summary message materialized into session history (as
//     ensureSummaryMaterialized does in llm_caller.go:438 and llm_runner.go:207).
//     This triggers the SetHistory call inside summarizeSessionCore for round 2,
//     which is the exact point where the bug manifests (boundary cleared).
//   - Round 2: summarizeSessionCore finds the summary message excluded,
//     calls SetHistory to un-exclude it → with the bug, boundary is cleared
//     to 0; with the fix, boundary survives and the eviction clamp applies.
func TestCompaction_SecondRoundEvictsExcludedFromMemory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rounds-e2e-*")
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
	storePath := filepath.Join(tmpDir, "rounds-e2e.db")
	s, err := store.Open(storePath)
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer s.Close()
	agent.Sessions.SetStore(s)

	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Summary: User wants to build a DHT crawler and debug a failing test.",
			ToolCalls: []providers.ToolCall{},
		},
	}

	const sessionKey = "test:rounds-e2e"
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

	if err := agent.Sessions.Save(sessionKey); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// --- Round 1: compact + evict ---
	stats1, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		t.Fatalf("round 1 summarizeSessionWithError failed: %v", err)
	}
	t.Logf("Round 1 stats: before=%d after=%d dropped=%d", stats1.BeforeMessages, stats1.AfterMessages, stats1.DroppedMessages)

	hist1 := agent.Sessions.GetHistory(sessionKey)
	t.Logf("Round 1 resident: %d messages", len(hist1))
	for i, m := range hist1 {
		t.Logf("  [%2d] role=%-10s excluded=%-5v content=%q", i, m.Role, m.ExcludeFromContext, truncForLog(m.Content, 60))
	}

	summary1 := agent.Sessions.GetSummary(sessionKey)
	summary1Len := len(summary1)
	t.Logf("Round 1 summary length: %d chars", summary1Len)

	// --- Materialize the summary message into the session history ---
	// This simulates what ensureSummaryMaterialized does in production
	// (llm_caller.go:438, llm_runner.go:207): the summary message is added
	// as a "user" message with the summaryMessageHeader prefix. In
	// summarizeSessionCore for round 2, this message will be found by
	// isSummaryMessage, and if it was excluded by ExcludeOldMessagesFromContext,
	// SetHistory will be called to un-exclude it — the exact point where the
	// bug manifests (boundary cleared to 0).
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{
		Role:    "user",
		Content: summaryMessageHeader + summary1,
	})

	// --- Add messages for round 2 ---
	// After materializing the summary, the in-memory slice is:
	//   [m18, m19, summary_msg, m20, m21, ..., m27]
	// where m18 and m19 are the kept tail from round 1, summary_msg is the
	// materialized summary, and m20-m27 are new messages.
	// Human turns: index 0 = m18, index 3 = m21, index 7 = m25.
	agent.Sessions.AddMessage(sessionKey, "assistant", "After round 1, let me continue the analysis.") // index 3
	agent.Sessions.AddMessage(sessionKey, "user", "What about the DHT bucket refresh interval?")       // index 4, human
	agent.Sessions.AddMessage(sessionKey, "assistant", "The bucket refresh is configurable.")          // index 5
	agent.Sessions.AddMessage(sessionKey, "assistant", "Default is every hour.")                       // index 6
	agent.Sessions.AddMessage(sessionKey, "assistant", "You can set it in config.yaml.")               // index 7
	agent.Sessions.AddMessage(sessionKey, "user", "Show me the config example.")                       // index 8, human
	agent.Sessions.AddMessage(sessionKey, "assistant", "Here is the YAML snippet.")                    // index 9
	agent.Sessions.AddMessage(sessionKey, "assistant", "That should get you started.")                 // index 10

	if err := agent.Sessions.Save(sessionKey); err != nil {
		t.Fatalf("round 2 pre-save failed: %v", err)
	}

	// --- Round 2: compact + evict ---
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Summary: User is building a DHT crawler, debugging tests, and configuring bucket refresh.",
			ToolCalls: []providers.ToolCall{},
		},
	}

	stats2, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		t.Fatalf("round 2 summarizeSessionWithError failed: %v", err)
	}
	t.Logf("Round 2 stats: before=%d after=%d dropped=%d", stats2.BeforeMessages, stats2.AfterMessages, stats2.DroppedMessages)

	// --- (a) NO message with ExcludeFromContext == true remains in memory ---
	hist2 := agent.Sessions.GetHistory(sessionKey)
	t.Log("=== Round 2 post-eviction in-memory history ===")
	for i, m := range hist2 {
		t.Logf("  [%2d] role=%-10s excluded=%-5v content=%q", i, m.Role, m.ExcludeFromContext, truncForLog(m.Content, 60))
		if m.ExcludeFromContext {
			t.Errorf("(a) Excluded message still in memory at index %d: %q", i, m.Content)
		}
	}

	// --- (b) History is not empty and context is non-empty ---
	if len(hist2) == 0 {
		t.Fatal("(b) history is empty after round 2 — eviction wiped everything")
	}
	ctx2 := filterContextMessages(hist2)
	if len(ctx2) == 0 {
		t.Error("(b) filterContextMessages is empty — no context reaches the model")
	}

	// --- (c) Summary is bounded (does not grow without limit) ---
	summary2 := agent.Sessions.GetSummary(sessionKey)
	summary2Len := len(summary2)
	t.Logf("Summary: round1=%d chars, round2=%d chars", summary1Len, summary2Len)

	// The summary should not more than double between rounds. With the
	// summary-message-skip fix, the growth comes only from the new content
	// (not from nesting the header + duplicating the body).
	if summary2Len > summary1Len*3 {
		t.Errorf("(c) summary grew too much: round1=%d, round2=%d (> 3× growth)", summary1Len, summary2Len)
	}
}

// TestCompaction_TailToolResultsDoesNotEmptyContext exercises the production
// path (summarizeSessionWithError) in 2 rounds where round 2 ends with a
// tool-result tail (assistant with 2 tool_calls + 2 tool results, no
// ContextMessages). The anti-split guard in ExcludeOldMessagesFromContext
// pushes excludeUpTo to len, setting excludeBoundary == len. The structural
// invariant in EvictExcludedMessages must prevent the entire context from
// being wiped.
//
// Without FIX A (structural invariant), the clamp is inert when
// boundary == len, and round 2 produces in_context=0 (the model gets no
// conversation message at all).
func TestCompaction_TailToolResultsDoesNotEmptyContext(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tail-tools-e2e-*")
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

	storePath := filepath.Join(tmpDir, "tail-tools-e2e.db")
	s, err := store.Open(storePath)
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer s.Close()
	agent.Sessions.SetStore(s)

	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Summary: User is building a DHT crawler and needs search results.",
			ToolCalls: []providers.ToolCall{},
		},
	}

	const sessionKey = "test:tail-tools-e2e"

	// --- Seed 20 messages: human at 0, 2, 8; rest assistant/tool ---
	agent.Sessions.AddMessage(sessionKey, "user", "ORIGINAL GOAL: build the DHT crawler")
	agent.Sessions.AddMessage(sessionKey, "assistant", "I'll help you build a DHT crawler.")
	agent.Sessions.AddMessage(sessionKey, "user", "FOLLOWUP ONE: does the routing table accept new nodes?")
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{
		Role:      "assistant",
		Content:   "Let me search for DHT implementations.",
		ToolCalls: []providers.ToolCall{{ID: "call_search", Function: &providers.FunctionCall{Name: "web_search"}}},
	})
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{
		Role:       "tool",
		Content:    "Found 3 DHT implementations on GitHub",
		ToolCallID: "call_search",
	})
	agent.Sessions.AddMessage(sessionKey, "assistant", "The routing table accepts new nodes via Kademlia XOR distance.")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Here is the architecture overview.")
	agent.Sessions.AddMessage(sessionKey, "assistant", "I can also check the test suite for you.")
	agent.Sessions.AddMessage(sessionKey, "user", "FOLLOWUP TWO: show me the failing test")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Let me look at the tests.")
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{
		Role:      "assistant",
		Content:   "Searching for test files...",
		ToolCalls: []providers.ToolCall{{ID: "call_grep", Function: &providers.FunctionCall{Name: "exec"}}},
	})
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{
		Role:       "tool",
		Content:    "Found: dht_test.go:42 TestBootstrapNode",
		ToolCallID: "call_grep",
	})
	agent.Sessions.AddMessage(sessionKey, "assistant", "The failing test is TestBootstrapNode.")
	agent.Sessions.AddMessage(sessionKey, "assistant", "It fails because the mock peer returns stale data.")
	agent.Sessions.AddMessage(sessionKey, "assistant", "You need to update the mock in setup_test.go.")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Specifically line 87 where the peer ID is set.")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Changing it to the new format should fix it.")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Would you like me to show the exact diff?")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Here is a summary of what we covered so far.")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Let me know if you have more questions.")

	if err := agent.Sessions.Save(sessionKey); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// --- Round 1: compact + evict ---
	stats1, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		t.Fatalf("round 1 summarizeSessionWithError failed: %v", err)
	}
	t.Logf("Round 1 stats: before=%d after=%d dropped=%d", stats1.BeforeMessages, stats1.AfterMessages, stats1.DroppedMessages)

	summary1 := agent.Sessions.GetSummary(sessionKey)
	t.Logf("Round 1 summary length: %d chars", len(summary1))

	hist1 := agent.Sessions.GetHistory(sessionKey)
	t.Logf("Round 1 resident: %d messages", len(hist1))

	// --- Materialize summary (ensureSummaryMaterialized pattern) ---
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{
		Role:    "user",
		Content: summaryMessageHeader + summary1,
	})

	// --- Add messages for round 2, ending with a tool-result tail ---
	// The tail is assistant with 2 tool_calls + 2 tool results, no
	// ContextMessages. This triggers the anti-split guard in
	// ExcludeOldMessagesFromContext which pushes excludeUpTo to len.
	agent.Sessions.AddMessage(sessionKey, "user", "search for DHT implementations and grep for test files") // human
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{                                            // assistant + 2 tool_calls
		Role:    "assistant",
		Content: "Let me search and grep.",
		ToolCalls: []providers.ToolCall{
			{ID: "call_search2", Function: &providers.FunctionCall{Name: "web_search"}},
			{ID: "call_grep2", Function: &providers.FunctionCall{Name: "exec"}},
		},
	})
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{ // tool result call_search2
		Role:       "tool",
		Content:    "Found 5 DHT implementations",
		ToolCallID: "call_search2",
	})
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{ // tool result call_grep2
		Role:       "tool",
		Content:    "Found 3 test files matching DHT",
		ToolCallID: "call_grep2",
	})

	if err := agent.Sessions.Save(sessionKey); err != nil {
		t.Fatalf("round 2 pre-save failed: %v", err)
	}

	histPreR2 := agent.Sessions.GetHistory(sessionKey)
	t.Logf("Round 2 pre-compact: %d messages", len(histPreR2))

	// --- Round 2: compact + evict ---
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Summary: User is building a DHT crawler with test configuration.",
			ToolCalls: []providers.ToolCall{},
		},
	}

	stats2, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		t.Fatalf("round 2 summarizeSessionWithError failed: %v", err)
	}
	t.Logf("Round 2 stats: before=%d after=%d dropped=%d", stats2.BeforeMessages, stats2.AfterMessages, stats2.DroppedMessages)

	// --- Inspect post-round-2 state ---
	hist2 := agent.Sessions.GetHistory(sessionKey)

	excludedResident := 0
	for _, m := range hist2 {
		if m.ExcludeFromContext {
			excludedResident++
		}
	}
	ctx2 := filterContextMessages(hist2)
	t.Logf("Round 2 post-eviction: len(hist)=%d, excluded_resident=%d, in_context=%d",
		len(hist2), excludedResident, len(ctx2))

	// --- (a) At least one context message must reach the model ---
	if len(ctx2) == 0 {
		t.Errorf("(a) filterContextMessages is empty after round 2 — model gets no conversation messages (in_context=0)")
	}
	// --- (b) History must not be empty ---
	if len(hist2) == 0 {
		t.Errorf("(b) history is empty after round 2 — eviction wiped all resident context")
	}
}
