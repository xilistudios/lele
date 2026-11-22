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

	storePath := filepath.Join(tmpDir, "rounds-e2e.db")
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

	const sessionKey = "test:rounds-e2e"
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

	if err := agent.Sessions.Save(sessionKey); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

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

	agent.Sessions.AddFullMessage(sessionKey, providers.Message{
		Role:    "user",
		Content: summaryMessageHeader + summary1,
	})

	agent.Sessions.AddMessage(sessionKey, "assistant", "After round 1, let me continue the analysis.")
	agent.Sessions.AddMessage(sessionKey, "user", "What about the DHT bucket refresh interval?")
	agent.Sessions.AddMessage(sessionKey, "assistant", "The bucket refresh is configurable.")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Default is every hour.")
	agent.Sessions.AddMessage(sessionKey, "assistant", "You can set it in config.yaml.")
	agent.Sessions.AddMessage(sessionKey, "user", "Show me the config example.")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Here is the YAML snippet.")
	agent.Sessions.AddMessage(sessionKey, "assistant", "That should get you started.")

	if err := agent.Sessions.Save(sessionKey); err != nil {
		t.Fatalf("round 2 pre-save failed: %v", err)
	}

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

	hist2 := agent.Sessions.GetHistory(sessionKey)
	t.Log("=== Round 2 post-eviction in-memory history ===")
	for i, m := range hist2 {
		t.Logf("  [%2d] role=%-10s excluded=%-5v content=%q", i, m.Role, m.ExcludeFromContext, truncForLog(m.Content, 60))
		if m.ExcludeFromContext {
			t.Errorf("(a) Excluded message still in memory at index %d: %q", i, m.Content)
		}
	}

	if len(hist2) == 0 {
		t.Fatal("(b) history is empty after round 2 — eviction wiped everything")
	}
	ctx2 := filterContextMessages(hist2)
	if len(ctx2) == 0 {
		t.Error("(b) filterContextMessages is empty — no context reaches the model")
	}

	summary2 := agent.Sessions.GetSummary(sessionKey)
	summary2Len := len(summary2)
	t.Logf("Summary: round1=%d chars, round2=%d chars", summary1Len, summary2Len)

	if summary2Len > summary1Len*3 {
		t.Errorf("(c) summary grew too much: round1=%d, round2=%d (> 3× growth)", summary1Len, summary2Len)
	}
}

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
	agent.Sessions.SetSessionRepo(s.Sessions())

	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Summary: User is building a DHT crawler and needs search results.",
			ToolCalls: []providers.ToolCall{},
		},
	}

	const sessionKey = "test:tail-tools-e2e"

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

	stats1, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		t.Fatalf("round 1 summarizeSessionWithError failed: %v", err)
	}
	t.Logf("Round 1 stats: before=%d after=%d dropped=%d", stats1.BeforeMessages, stats1.AfterMessages, stats1.DroppedMessages)

	summary1 := agent.Sessions.GetSummary(sessionKey)
	t.Logf("Round 1 summary length: %d chars", len(summary1))

	hist1 := agent.Sessions.GetHistory(sessionKey)
	t.Logf("Round 1 resident: %d messages", len(hist1))

	agent.Sessions.AddFullMessage(sessionKey, providers.Message{
		Role:    "user",
		Content: summaryMessageHeader + summary1,
	})

	agent.Sessions.AddMessage(sessionKey, "user", "search for DHT implementations and grep for test files")
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{
		Role:    "assistant",
		Content: "Let me search and grep.",
		ToolCalls: []providers.ToolCall{
			{ID: "call_search2", Function: &providers.FunctionCall{Name: "web_search"}},
			{ID: "call_grep2", Function: &providers.FunctionCall{Name: "exec"}},
		},
	})
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{
		Role:       "tool",
		Content:    "Found 5 DHT implementations",
		ToolCallID: "call_search2",
	})
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{
		Role:       "tool",
		Content:    "Found 3 test files matching DHT",
		ToolCallID: "call_grep2",
	})

	if err := agent.Sessions.Save(sessionKey); err != nil {
		t.Fatalf("round 2 pre-save failed: %v", err)
	}

	histPreR2 := agent.Sessions.GetHistory(sessionKey)
	t.Logf("Round 2 pre-compact: %d messages", len(histPreR2))

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

	if len(ctx2) == 0 {
		t.Errorf("(a) filterContextMessages is empty after round 2 — model gets no conversation messages (in_context=0)")
	}
	if len(hist2) == 0 {
		t.Errorf("(b) history is empty after round 2 — eviction wiped all resident context")
	}

	if excludedResident != 0 {
		t.Errorf("(c) #343: excluded_resident = %d, want 0 — Option A keeps the whole tool group, so no excluded row may stay resident", excludedResident)
	}

	groupCallIDs := []string{"call_search2", "call_grep2"}

	ownerInContext := false
	for _, m := range ctx2 {
		if m.Role != "assistant" {
			continue
		}
		calls := make(map[string]bool, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			calls[tc.ID] = true
		}
		complete := true
		for _, id := range groupCallIDs {
			if !calls[id] {
				complete = false
				break
			}
		}
		if complete {
			ownerInContext = true
			break
		}
	}
	if !ownerInContext {
		t.Errorf("(d) #343: the tool group's owning assistant (tool_calls %v) is missing from the filtered context — the tool group itself must reach the model", groupCallIDs)
	}
	for _, id := range groupCallIDs {
		found := false
		for _, m := range ctx2 {
			if m.Role == "tool" && m.ToolCallID == id {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("(d) #343: tool result %q of the kept tool group is missing from the filtered context — the tool group itself must reach the model", id)
		}
	}
}
