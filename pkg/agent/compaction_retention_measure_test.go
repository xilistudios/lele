package agent

import (
	"fmt"
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

const measure343SessionKey = "test:compaction-343-measure"

type layout343Spec struct {
	name        string
	results     int
	resultChars int
	rounds      int
}

type roundStats343 struct {
	round            int
	stats            *SummarizeStats
	resident         int
	excludedResident int
	inContext        int
	orphans          int
	tailGroupSize    int
}

func TestCompaction343Measurement(t *testing.T) {
	layouts := []layout343Spec{
		{name: "L1_degenerate_small", results: 2, resultChars: 10, rounds: 3},
		{name: "L2_large_results", results: 2, resultChars: 2000, rounds: 3},
		{name: "L3_multi_result", results: 3, resultChars: 2000, rounds: 3},
		{name: "L4_control_no_tool_tail", results: 0, resultChars: 0, rounds: 3},
	}

	for _, spec := range layouts {
		t.Run(spec.name, func(t *testing.T) {
			rows := measureLayout343(t, spec)
			logLayout343(t, spec, rows)

			for _, r := range rows {
				if r.resident <= 0 {
					t.Errorf("layout %s round %d: resident=%d, want > 0 (eviction wiped the history)",
						spec.name, r.round, r.resident)
				}
				if r.inContext <= 0 {
					t.Errorf("layout %s round %d: in_context=%d, want > 0 (model gets no conversation)",
						spec.name, r.round, r.inContext)
				}
				if r.orphans != 0 {
					t.Errorf("layout %s round %d: orphans=%d, want 0 (tool result without owner in context)",
						spec.name, r.round, r.orphans)
				}
			}
		})
	}
}

func measureLayout343(t *testing.T, spec layout343Spec) []roundStats343 {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "compact343-measure-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
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
			Anthropic: config.ProviderConfig{APIKey: "test-key"},
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
		t.Fatal("no default agent found")
	}

	ctxWindow := al.getSessionContextWindow(measure343SessionKey)
	t.Logf("layout %s: context window = %d tokens (10%% = %d, 75%% compaction threshold = %d)",
		spec.name, ctxWindow, ctxWindow/10, ctxWindow*75/100)

	storePath := filepath.Join(tmpDir, "compact343-measure.db")
	s, err := store.Open(storePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	agent.Sessions.SetSessionRepo(s.Sessions())

	seedInitial343(agent)
	if err := agent.Sessions.Save(measure343SessionKey); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	rows := make([]roundStats343, 0, spec.rounds)
	for round := 0; round < spec.rounds; round++ {
		if round > 0 {
			if sum := agent.Sessions.GetSummary(measure343SessionKey); sum != "" {
				agent.Sessions.AddFullMessage(measure343SessionKey, providers.Message{
					Role:    "user",
					Content: summaryMessageHeader + sum,
				})
			}
		}
		groupSize := appendRoundBlock343(agent, spec, round)
		if err := agent.Sessions.Save(measure343SessionKey); err != nil {
			t.Fatalf("layout %s round %d pre-save: %v", spec.name, round, err)
		}

		agent.Provider = &llmRunnerMockLLMProvider{
			response: &providers.LLMResponse{
				Content:   fmt.Sprintf("Summary %d: the DHT crawler work continued across rounds.", round),
				ToolCalls: []providers.ToolCall{},
			},
		}

		stats, err := sm.summarizeSessionWithError(agent, measure343SessionKey)
		if err != nil {
			t.Fatalf("layout %s round %d summarizeSessionWithError: %v", spec.name, round, err)
		}

		hist := agent.Sessions.GetHistory(measure343SessionKey)
		ctx := filterContextMessages(hist)

		rows = append(rows, roundStats343{
			round:            round,
			stats:            stats,
			resident:         len(hist),
			excludedResident: countExcludedResident343(hist),
			inContext:        len(ctx),
			orphans:          countOrphanToolResults343(ctx),
			tailGroupSize:    groupSize,
		})
	}
	return rows
}

func seedInitial343(agent *AgentInstance) {
	const key = measure343SessionKey
	agent.Sessions.AddMessage(key, "user", "ORIGINAL GOAL: build the DHT crawler and debug the failing test")
	agent.Sessions.AddMessage(key, "assistant", "I will help you build a DHT crawler and debug the test suite.")
	for i := 2; i <= 13; i++ {
		if i == 5 {
			agent.Sessions.AddMessage(key, "user", "FOLLOWUP: does the routing table accept new nodes?")
			continue
		}
		agent.Sessions.AddMessage(key, "assistant",
			fmt.Sprintf("Filler analysis step %d about the DHT routing table and its test suite.", i))
	}
}

func appendRoundBlock343(agent *AgentInstance, spec layout343Spec, round int) int {
	const key = measure343SessionKey

	if round > 0 {
		agent.Sessions.AddMessage(key, "user", fmt.Sprintf("Round %d follow-up: keep searching the DHT code.", round))
		agent.Sessions.AddMessage(key, "assistant", fmt.Sprintf("Understood, continuing with round %d.", round))
	}

	if spec.results <= 0 {
		agent.Sessions.AddMessage(key, "assistant", fmt.Sprintf("Round %d plain answer one.", round))
		agent.Sessions.AddMessage(key, "assistant", fmt.Sprintf("Round %d plain answer two.", round))
		return 2
	}

	tcs := make([]providers.ToolCall, 0, spec.results)
	ids := make([]string, 0, spec.results)
	for i := 0; i < spec.results; i++ {
		id := fmt.Sprintf("call_r%d_%d", round, i)
		ids = append(ids, id)
		tcs = append(tcs, providers.ToolCall{
			ID:       id,
			Function: &providers.FunctionCall{Name: "exec", Arguments: "{}"},
		})
	}
	agent.Sessions.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   fmt.Sprintf("Running %d tools for round %d.", spec.results, round),
		ToolCalls: tcs,
	})
	for i, id := range ids {
		agent.Sessions.AddFullMessage(key, providers.Message{
			Role:       "tool",
			Content:    toolResultContent343(spec.resultChars, round, i),
			ToolCallID: id,
		})
	}
	return spec.results + 1
}

func toolResultContent343(chars, round, idx int) string {
	base := fmt.Sprintf("result r%d #%d ", round, idx)
	if len(base) >= chars {
		return base[:chars]
	}
	return base + strings.Repeat("x", chars-len(base))
}

func countExcludedResident343(hist []providers.Message) int {
	n := 0
	for _, m := range hist {
		if m.ExcludeFromContext {
			n++
		}
	}
	return n
}

func countOrphanToolResults343(ctx []providers.Message) int {
	n := 0
	for i, m := range ctx {
		if !isToolResult343(m) {
			continue
		}
		found := false
		for j := i - 1; j >= 0; j-- {
			if ctx[j].Role != "assistant" {
				continue
			}
			for _, tc := range ctx[j].ToolCalls {
				if tc.ID == m.ToolCallID {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			n++
		}
	}
	return n
}

func isToolResult343(m providers.Message) bool {
	return (m.Role == "tool" || m.Role == "user") && m.ToolCallID != ""
}

func logLayout343(t *testing.T, spec layout343Spec, rows []roundStats343) {
	t.Helper()
	t.Logf("=== layout %s (results=%d result_chars=%d rounds=%d) ===", spec.name, spec.results, spec.resultChars, spec.rounds)
	t.Logf("%-5s %-9s %-8s %-8s %-10s %-9s %-9s %-8s %-9s %-7s %-8s %-6s",
		"round", "beforeMsg", "afterMsg", "dropped", "beforeTok", "afterTok", "savedTok",
		"resident", "exclRes", "inCtx", "orphans", "tail")
	for _, r := range rows {
		t.Logf("%-5d %-9d %-8d %-8d %-10d %-9d %-9d %-8d %-9d %-7d %-8d %-6d",
			r.round,
			r.stats.BeforeMessages, r.stats.AfterMessages, r.stats.DroppedMessages,
			r.stats.BeforeTokens, r.stats.AfterTokens, r.stats.SavedTokens,
			r.resident, r.excludedResident, r.inContext, r.orphans, r.tailGroupSize)
	}
}
