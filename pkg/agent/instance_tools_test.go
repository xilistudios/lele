package agent

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
)

func newTestToolRegistry(names ...string) *tools.ToolRegistry {
	r := tools.NewToolRegistry()
	for _, n := range names {
		r.Register(&mockTool{name: n, description: "mock " + n})
	}
	return r
}

func registeredSorted(r *tools.ToolRegistry) []string {
	got := r.List()
	sort.Strings(got)
	return got
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type sliceHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *sliceHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *sliceHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *sliceHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *sliceHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *sliceHandler) levels() []slog.Level {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]slog.Level, 0, len(h.records))
	for i := range h.records {
		out = append(out, h.records[i].Level)
	}
	return out
}

func captureSlog(t *testing.T, fn func()) *sliceHandler {
	t.Helper()
	prev := slog.Default()
	h := &sliceHandler{}
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)
	fn()
	return h
}

func TestApplyToolsAllowlist_EmptyPreservesAll(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
	}{
		{"nil", nil},
		{"empty slice", []string{}},
		{"blank entries only", []string{"  ", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestToolRegistry("exec", "read_file", "write_file")
			before := registeredSorted(r)

			h := captureSlog(t, func() {
				applyToolsAllowlist(r, tc.allowed, "tester")
			})

			if got := registeredSorted(r); !equalStrings(got, before) {
				t.Fatalf("tools changed with empty allowlist: got %v, want %v", got, before)
			}
			if len(h.records) != 0 {
				t.Fatalf("expected no log output, got %v", h.levels())
			}
		})
	}
}

func TestApplyToolsAllowlist_SubsetRemovesOthers(t *testing.T) {
	r := newTestToolRegistry("exec", "read_file", "write_file", "edit_file")

	h := captureSlog(t, func() {
		applyToolsAllowlist(r, []string{"exec", "read_file"}, "tester")
	})

	want := []string{"exec", "read_file"}
	if got := registeredSorted(r); !equalStrings(got, want) {
		t.Fatalf("tools after subset allowlist: got %v, want %v", got, want)
	}
	if len(h.records) != 0 {
		t.Fatalf("expected no log output for valid subset, got %v", h.levels())
	}
}

func TestApplyToolsAllowlist_UnknownNameWarnsButDoesNotRemove(t *testing.T) {
	r := newTestToolRegistry("exec", "read_file", "write_file")

	h := captureSlog(t, func() {
		applyToolsAllowlist(r, []string{"exec", "no_such_tool"}, "tester")
	})

	want := []string{"exec"}
	if got := registeredSorted(r); !equalStrings(got, want) {
		t.Fatalf("tools after allowlist with unknown name: got %v, want %v", got, want)
	}

	if len(h.records) != 1 || h.records[0].Level != slog.LevelWarn {
		t.Fatalf("expected exactly one WARN record, got %v", h.levels())
	}
	if !strings.Contains(h.records[0].Message, "unknown") {
		t.Fatalf("expected warn about unknown tools, got %q", h.records[0].Message)
	}
}

func TestApplyToolsAllowlist_NoIntersectionKeepsAllAndErrors(t *testing.T) {
	r := newTestToolRegistry("exec", "read_file", "write_file")
	before := registeredSorted(r)

	h := captureSlog(t, func() {
		applyToolsAllowlist(r, []string{"ghost_a", "ghost_b"}, "tester")
	})

	if got := registeredSorted(r); !equalStrings(got, before) {
		t.Fatalf("allowlist with no intersection must keep all tools: got %v, want %v", got, before)
	}
	sawError := false
	for i := range h.records {
		if h.records[i].Level == slog.LevelError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatalf("expected an ERROR record for zero-intersection allowlist, got %v", h.levels())
	}
}

func TestApplyToolsAllowlist_NilRegistryIsNoop(t *testing.T) {
	applyToolsAllowlist(nil, []string{"exec"}, "tester")
}

func newAgentInstanceTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Provider = "openai"
	cfg.Agents.Defaults.Model = "openai:gpt-4o-mini"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {Type: "openai"},
	}
	return cfg
}

func TestNewAgentInstance_ToolsAllowlistNilKeepsAll(t *testing.T) {
	cfg := newAgentInstanceTestConfig(t)

	all := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)
	allCount := all.Tools.Count()
	if allCount == 0 {
		t.Fatal("expected default agent to have tools registered")
	}

	agentCfg := &config.AgentConfig{ID: "limited"}
	limited := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg)
	if limited.Tools.Count() != allCount {
		t.Fatalf("nil allowlist changed tool count: got %d, want %d", limited.Tools.Count(), allCount)
	}
}

func TestNewAgentInstance_ToolsAllowlistSubset(t *testing.T) {
	cfg := newAgentInstanceTestConfig(t)

	base := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)
	if _, ok := base.Tools.Get("exec"); !ok {
		t.Fatal("baseline registry must contain exec")
	}
	if _, ok := base.Tools.Get("write_file"); !ok {
		t.Fatal("baseline registry must contain write_file")
	}

	agentCfg := &config.AgentConfig{
		ID:    "readonly",
		Tools: []string{"read_file", "list_dir"},
	}
	agent := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg)

	for _, name := range agentCfg.Tools {
		if _, ok := agent.Tools.Get(name); !ok {
			t.Fatalf("allowed tool %q missing after allowlist", name)
		}
	}
	for _, name := range []string{"exec", "write_file", "edit_file"} {
		if _, ok := agent.Tools.Get(name); ok {
			t.Fatalf("tool %q should have been removed by allowlist", name)
		}
	}
	if agent.Tools.Count() != len(agentCfg.Tools) {
		t.Fatalf("unexpected surviving tools: got %d, want %d (%v)",
			agent.Tools.Count(), len(agentCfg.Tools), agent.Tools.List())
	}
}

func TestNewAgentInstance_ToolsAllowlistNoIntersectionKeepsAll(t *testing.T) {
	cfg := newAgentInstanceTestConfig(t)

	base := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)

	agentCfg := &config.AgentConfig{
		ID:    "broken",
		Tools: []string{"tool_that_does_not_exist"},
	}
	agent := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg)

	if agent.Tools.Count() != base.Tools.Count() {
		t.Fatalf("zero-intersection allowlist must keep all tools: got %d, want %d",
			agent.Tools.Count(), base.Tools.Count())
	}
}

func memoryLines(n int) string {
	return strings.Repeat("line\n", n)
}

func TestNewAgentInstance_RegistersUpdateMemoryTool(t *testing.T) {
	cfg := newAgentInstanceTestConfig(t)

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)
	tool, ok := agent.Tools.Get("update_memory")
	if !ok {
		t.Fatalf("update_memory not registered, have %v", agent.Tools.List())
	}
	if !strings.Contains(tool.Description(), "500 lines") {
		t.Errorf("update_memory description does not mention the 500-line cap: %q", tool.Description())
	}
}

func TestNewAgentInstance_MemoryMaxLinesFallback(t *testing.T) {
	ctx := context.Background()

	t.Run("zero falls back to 500", func(t *testing.T) {
		cfg := newAgentInstanceTestConfig(t)
		cfg.Agents.Defaults.MemoryMaxLines = 0
		agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)

		over := agent.Tools.Execute(ctx, "update_memory", map[string]interface{}{
			"content": memoryLines(501), "mode": "replace",
		})
		if !over.IsError {
			t.Fatalf("501-line write accepted, want rejection (ForLLM: %s)", over.ForLLM)
		}
		fit := agent.Tools.Execute(ctx, "update_memory", map[string]interface{}{
			"content": memoryLines(500), "mode": "replace",
		})
		if fit.IsError {
			t.Fatalf("500-line write rejected, want success (ForLLM: %s)", fit.ForLLM)
		}
	})

	t.Run("configured cap is honoured", func(t *testing.T) {
		cfg := newAgentInstanceTestConfig(t)
		cfg.Agents.Defaults.MemoryMaxLines = 10
		agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)

		over := agent.Tools.Execute(ctx, "update_memory", map[string]interface{}{
			"content": memoryLines(11), "mode": "replace",
		})
		if !over.IsError {
			t.Fatalf("11-line write accepted with a 10-line cap, want rejection (ForLLM: %s)", over.ForLLM)
		}
		fit := agent.Tools.Execute(ctx, "update_memory", map[string]interface{}{
			"content": memoryLines(10), "mode": "replace",
		})
		if fit.IsError {
			t.Fatalf("10-line write rejected with a 10-line cap, want success (ForLLM: %s)", fit.ForLLM)
		}
	})
}

func TestNewAgentInstance_MemoryToolRespectsAllowlist(t *testing.T) {
	cfg := newAgentInstanceTestConfig(t)

	denied := NewAgentInstance(&config.AgentConfig{
		ID:    "denied",
		Tools: []string{"read_file"},
	}, &cfg.Agents.Defaults, cfg)
	if _, ok := denied.Tools.Get("update_memory"); ok {
		t.Error("update_memory must be removed for an allowlist that does not name it")
	}

	allowed := NewAgentInstance(&config.AgentConfig{
		ID:    "allowed",
		Tools: []string{"update_memory"},
	}, &cfg.Agents.Defaults, cfg)
	if _, ok := allowed.Tools.Get("update_memory"); !ok {
		t.Error("update_memory missing for an allowlist that names it")
	}
}
