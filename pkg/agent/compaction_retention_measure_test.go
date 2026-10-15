// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// T1 measurement harness for issue #343 (compaction retention policy).
//
// This test drives the PRODUCTION compaction path
// (summarizeSessionWithError -> summarizeSessionCore ->
// ExcludeOldMessagesFromContext -> EvictExcludedMessages) for several rounds on
// a single session and records, per round and per layout, both the
// SummarizeStats returned by the core and the structural shape of the resident
// history: how many rows are still resident, how many of them are still
// flagged ExcludeFromContext (the RAM cost that survives in-process), how many
// actually reach the model, and whether any tool result in context is orphaned
// (a tool result without its owning assistant in context).
//
// It asserts ONLY invariants that hold under BOTH retention policies
// (the pre-#343 forward-push baseline and the shipped Option A move-back):
// in_context > 0, orphans == 0 and resident > 0. Token numbers are LOGGED,
// never asserted: they are exactly what differs between the two policies. To
// make the #343 decision the harness was run twice — once on the baseline
// (forward-push) and once with the temporary Option A patch applied to
// pkg/session/context.go — and the two tables were compared; Option A is now
// the shipped policy. See issue #343 and the PLAN §4 T1.

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

// measure343SessionKey is the session under measurement. One session per
// layout subtest, so rounds accumulate on the same history (the #343 RAM cost
// is "3-4 excluded rows per round", so accumulation matters).
const measure343SessionKey = "test:compaction-343-measure"

// layout343Spec describes one tail shape to measure.
type layout343Spec struct {
	name string
	// results is the number of tool results in the tail group. 0 means the
	// control layout (plain assistant tail, no tool calls).
	results int
	// resultChars is the length of each tool-result payload. Values above the
	// summarizeSessionCore perMessageLimit (2000) are pointless: the limit
	// truncates the SUMMARY prompt only, never the live kept tail, which is
	// exactly the tail being measured here.
	resultChars int
	// rounds is how many compaction rounds to chain on the same session (>= 3
	// so accumulation is observable).
	rounds int
}

// roundStats343 is one measured compaction round.
type roundStats343 struct {
	round            int
	stats            *SummarizeStats
	resident         int // len(history) after eviction
	excludedResident int // resident rows still flagged ExcludeFromContext
	inContext        int // len(filterContextMessages(history)) — reaches the model
	orphans          int // tool results in context without an owning assistant in context
	tailGroupSize    int // size of the tool group kept at the tail (messages)
}

// TestCompaction343Measurement is the T1 harness entry point. It runs every
// layout and logs the full per-round table (t.Logf only — no token assertions).
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

			// Invariants valid under BOTH retention policies. Anything else
			// (token counts, excluded_resident, in_context magnitude) is data
			// to compare between baseline and Option A, not an assertion.
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

// measureLayout343 builds a fresh agent + SQLite store, seeds a base
// conversation, then chains `spec.rounds` production compaction rounds. Between
// rounds it materializes the previous summary the way production does
// (ensureSummaryMaterialized: a "user" message carrying summaryMessageHeader)
// and appends a new block whose tail is the layout's tool group, so the
// anti-splice guard sees the same shape every round.
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

// seedInitial343 seeds a plain (tool-free) base conversation so every layout
// starts from the same history. Two human user turns are included so the
// preserved-user pinning behaves like a real session.
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

// appendRoundBlock343 appends one round's worth of messages and returns the
// size of the tool group placed at the tail (0 for the control layout). For
// rounds > 0 a human turn and an acknowledgement precede the tail, mirroring a
// real user follow-up.
func appendRoundBlock343(agent *AgentInstance, spec layout343Spec, round int) int {
	const key = measure343SessionKey

	if round > 0 {
		agent.Sessions.AddMessage(key, "user", fmt.Sprintf("Round %d follow-up: keep searching the DHT code.", round))
		agent.Sessions.AddMessage(key, "assistant", fmt.Sprintf("Understood, continuing with round %d.", round))
	}

	if spec.results <= 0 {
		// Control: no tool tail at all, so the anti-splice guard never fires —
		// the T1 measurement's two policy tables were identical here, which is
		// exactly what made it a control.
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

// toolResultContent343 builds an ASCII payload of exactly `chars` bytes.
func toolResultContent343(chars, round, idx int) string {
	base := fmt.Sprintf("result r%d #%d ", round, idx)
	if len(base) >= chars {
		return base[:chars]
	}
	return base + strings.Repeat("x", chars-len(base))
}

// countExcludedResident343 counts resident rows still flagged
// ExcludeFromContext (cost #1: rows kept in RAM although the model never sees
// them).
func countExcludedResident343(hist []providers.Message) int {
	n := 0
	for _, m := range hist {
		if m.ExcludeFromContext {
			n++
		}
	}
	return n
}

// countOrphanToolResults343 is the reverse-direction orphan check from
// pkg/session/manager_exclude_test.go: a tool result in context must have a
// matching assistant (by ToolCallID) earlier in the same context.
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

// isToolResult343 mirrors pkg/session.isToolResultMessage (unexported there).
func isToolResult343(m providers.Message) bool {
	return (m.Role == "tool" || m.Role == "user") && m.ToolCallID != ""
}

// logLayout343 dumps the per-round table. This is the evidence the PR carries;
// the values are data, not assertions.
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
