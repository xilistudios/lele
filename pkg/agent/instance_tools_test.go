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

// --- test helpers -----------------------------------------------------------

// newTestToolRegistry builds a registry populated with the given tool names
// using the mockTool defined in context_test.go.
func newTestToolRegistry(names ...string) *tools.ToolRegistry {
	r := tools.NewToolRegistry()
	for _, n := range names {
		r.Register(&mockTool{name: n, description: "mock " + n})
	}
	return r
}

// registeredSorted returns the registry contents in deterministic order.
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

// sliceHandler is a minimal slog.Handler that records every log entry.
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

// captureSlog installs a recording handler on the default logger for the
// duration of fn and returns everything that was logged.
func captureSlog(t *testing.T, fn func()) *sliceHandler {
	t.Helper()
	prev := slog.Default()
	h := &sliceHandler{}
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)
	fn()
	return h
}

// --- applyToolsAllowlist unit tests -----------------------------------------

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
	// A pure subset must not warn or error.
	if len(h.records) != 0 {
		t.Fatalf("expected no log output for valid subset, got %v", h.levels())
	}
}

func TestApplyToolsAllowlist_UnknownNameWarnsButDoesNotRemove(t *testing.T) {
	r := newTestToolRegistry("exec", "read_file", "write_file")

	h := captureSlog(t, func() {
		applyToolsAllowlist(r, []string{"exec", "no_such_tool"}, "tester")
	})

	// "exec" matched, so filtering proceeds: only exec survives.
	want := []string{"exec"}
	if got := registeredSorted(r); !equalStrings(got, want) {
		t.Fatalf("tools after allowlist with unknown name: got %v, want %v", got, want)
	}

	// Exactly one WARN (never an error) must mention the unknown tool.
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
	// Must not panic.
	applyToolsAllowlist(nil, []string{"exec"}, "tester")
}

// --- wiring in NewAgentInstance ---------------------------------------------

// newAgentInstanceTestConfig builds a minimal valid config for NewAgentInstance.
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

	// Baseline: which names exist without a filter.
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
