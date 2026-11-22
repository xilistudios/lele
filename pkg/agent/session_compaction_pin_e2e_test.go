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

	agent.Sessions.AddMessage(sessionKey, "user", human0)
	agent.Sessions.AddMessage(sessionKey, "assistant", "I'll help you build a DHT crawler.")
	agent.Sessions.AddMessage(sessionKey, "user", human2)
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
	agent.Sessions.AddMessage(sessionKey, "user", human8)
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

	beforeHistory := agent.Sessions.GetHistory(sessionKey)
	if len(beforeHistory) != 20 {
		t.Fatalf("Expected 20 seeded messages, got %d", len(beforeHistory))
	}

	stats, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		t.Fatalf("summarizeSessionWithError failed: %v", err)
	}
	if stats == nil {
		t.Fatal("Expected non-nil stats from summarizeSessionWithError")
	}
	t.Logf("Compaction stats: before=%d after=%d dropped=%d", stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages)

	history := agent.Sessions.GetHistory(sessionKey)

	t.Log("=== Post-compaction message map ===")
	for i, m := range history {
		t.Logf("  [%2d] role=%-10s excluded=%-5v toolCallID=%q content=%q",
			i, m.Role, m.ExcludeFromContext, m.ToolCallID,
			truncForLog(m.Content, 60))
	}

	lastHumanIdx := 8
	nextAfterLastHuman := lastHumanIdx + 1
	if !history[nextAfterLastHuman].ExcludeFromContext {
		t.Fatalf("PRECONDITION FAILED: message[%d] (after last human turn) should be excluded but isn't — test layout does not prove pin necessity",
			nextAfterLastHuman)
	}

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

	for _, idx := range []int{0, 2, 8} {
		if history[idx].ExcludeFromContext {
			t.Errorf("message[%d] (human turn) should NOT be excluded but is", idx)
		}
	}

	ctx := filterContextMessages(history)
	t.Log("=== Filtered context (what the model sees) ===")
	for i, m := range ctx {
		t.Logf("  [%2d] role=%-10s content=%q", i, m.Role, truncForLog(m.Content, 80))
	}

	assertContextContains(t, ctx, human0)
	assertContextContains(t, ctx, human2)
	assertContextContains(t, ctx, human8)
}

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

	agent.Sessions.AddMessage(sessionKey, "user", human0)
	agent.Sessions.AddMessage(sessionKey, "assistant", "I'll help you build a DHT crawler.")
	agent.Sessions.AddMessage(sessionKey, "user", human2)
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
	agent.Sessions.AddMessage(sessionKey, "user", human8)
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

	agent.Sessions.AddFullMessage(sessionKey, providers.Message{
		Role:               "tool",
		Content:            "✅ Command approved: `ls`",
		ToolCallID:         "approval:test-req-1",
		ExcludeFromContext: true,
	})

	if err := agent.Sessions.Save(sessionKey); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	stats, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		t.Fatalf("summarizeSessionWithError failed: %v", err)
	}
	if stats == nil {
		t.Fatal("Expected non-nil stats")
	}
	t.Logf("Compaction stats: before=%d after=%d dropped=%d", stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages)

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
	if len(hist) != keepCount {
		t.Errorf("resident history = %d messages, want exactly %d (the kept tail; the compaction region must not stay in RAM)", len(hist), keepCount)
	}

	summary := agent.Sessions.GetSummary(sessionKey)
	t.Logf("Session summary after eviction: %q", summary)
	if strings.TrimSpace(summary) == "" {
		t.Error("Session summary should be non-empty after eviction")
	}

	ctx := filterContextMessages(hist)
	t.Log("=== Filtered context (post-eviction) ===")
	for i, m := range ctx {
		t.Logf("  [%2d] role=%-10s content=%q", i, m.Role, truncForLog(m.Content, 80))
	}

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

	rows, err := s.Sessions().LoadMessagesWithSeq(sessionKey)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	if len(rows) != 21 {
		t.Errorf("SQLite rows = %d, want 21 (no data loss after eviction)", len(rows))
	}

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

func assertContextContains(t *testing.T, ctx []providers.Message, expected string) {
	t.Helper()
	for _, m := range ctx {
		if strings.Contains(m.Content, expected) {
			return
		}
	}
	t.Errorf("filtered context does not contain %q", expected)
}

func contextContains(ctx []providers.Message, expected string) bool {
	for _, m := range ctx {
		if strings.Contains(m.Content, expected) {
			return true
		}
	}
	return false
}

func truncForLog(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
