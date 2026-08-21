// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
)

var bgCtx = context.Background()

// ---- subagent_helpers.go pure formatters ----

func TestFormatSubagentLabel(t *testing.T) {
	if got := formatSubagentLabel("hello"); got != "hello" {
		t.Errorf("got %q", got)
	}
	if got := formatSubagentLabel("  "); got != "(unnamed)" {
		t.Errorf("got %q", got)
	}
	if got := formatSubagentLabel(""); got != "(unnamed)" {
		t.Errorf("got %q", got)
	}
}

func TestFormatSubagentAgent(t *testing.T) {
	if got := formatSubagentAgent("agent-1"); got != "agent-1" {
		t.Errorf("got %q", got)
	}
	if got := formatSubagentAgent(" "); got != "(default)" {
		t.Errorf("got %q", got)
	}
	if got := formatSubagentAgent(""); got != "(default)" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateSubagentText(t *testing.T) {
	if got := truncateSubagentText("short", 100); got != "short" {
		t.Errorf("got %q", got)
	}
	if got := truncateSubagentText("  padded  ", 100); strings.HasPrefix(got, " ") {
		t.Errorf("expected trimmed, got %q", got)
	}
	if got := truncateSubagentText(strings.Repeat("a", 10), 5); got != strings.Repeat("a", 5)+"..." {
		t.Errorf("got %q", got)
	}
	if got := truncateSubagentText("abc", 0); got != "abc" {
		t.Errorf("limit<=0 should return original, got %q", got)
	}
}

func TestFormatSubagentTaskInfo(t *testing.T) {
	if got := formatSubagentTaskInfo(nil); got != "Subagent task not found" {
		t.Errorf("got %q", got)
	}

	task := &tools.SubagentTask{
		ID:     "subagent-1",
		Status: tools.SubagentStatusCompleted,
		Label:  "my-label",
	}
	got := formatSubagentTaskInfo(task)
	if !strings.Contains(got, "Task subagent-1") ||
		!strings.Contains(got, "Status: completed") ||
		!strings.Contains(got, "Agent: (default)") ||
		!strings.Contains(got, "Label: my-label") {
		t.Errorf("got: %q", got)
	}

	full := &tools.SubagentTask{
		ID:             "subagent-2",
		Status:         tools.SubagentStatusFailed,
		AgentID:        "coder",
		Label:          "x",
		Summary:        "done summary",
		ContextRequest: "need more data",
		Guidance:       []string{"g1", "g2"},
		Result:         strings.Repeat("r", 2000),
	}
	got2 := formatSubagentTaskInfo(full)
	if !strings.Contains(got2, "Summary: done summary") ||
		!strings.Contains(got2, "Context needed: need more data") ||
		!strings.Contains(got2, "Guidance entries: 2") ||
		!strings.Contains(got2, "Details:") ||
		!strings.Contains(got2, "Agent: coder") {
		t.Errorf("got2: %q", got2)
	}
}

func TestFormatSubagentTaskList(t *testing.T) {
	got := formatSubagentTaskList(nil)
	if !strings.Contains(got, "No active or waiting subagents") {
		t.Errorf("empty list: %q", got)
	}

	tasks := []*tools.SubagentTask{
		{ID: "s2", Status: tools.SubagentStatusRunning, Created: 200, Label: "two"},
		{ID: "s1", Status: tools.SubagentStatusNeedsContext, Created: 100, Label: "one"},
	}
	got = formatSubagentTaskList(tasks)
	if !strings.Contains(got, "s1") || !strings.Contains(got, "s2") {
		t.Errorf("task list: %q", got)
	}
	if !strings.Contains(got, "needs_context") || !strings.Contains(got, "running") {
		t.Errorf("task list missing statuses: %q", got)
	}
}

func TestFormatSubagentsCommand(t *testing.T) {
	coord := &covCoordinatorStub{found: true, stopOK: true, resp: "continued ok"}

	got := formatSubagentsCommand(bgCtx, coord, "ses", nil)
	if !strings.Contains(got, "No active or waiting subagents") {
		t.Errorf("no args: %q", got)
	}

	coord.task = &tools.SubagentTask{ID: "s1", Status: "running", Label: "lbl"}
	got = formatSubagentsCommand(bgCtx, coord, "ses", []string{})
	if !strings.Contains(got, "s1") {
		t.Errorf("list with tasks: %q", got)
	}

	got = formatSubagentsCommand(bgCtx, coord, "ses", []string{"info"})
	if !strings.Contains(got, "Usage: /subagents info") {
		t.Errorf("info short: %q", got)
	}
	coord.found = false
	got = formatSubagentsCommand(bgCtx, coord, "ses", []string{"info", "nope"})
	if !strings.Contains(got, "Subagent task not found") {
		t.Errorf("info not found: %q", got)
	}
	coord.found = true
	got = formatSubagentsCommand(bgCtx, coord, "ses", []string{"info", "abc"})
	if !strings.Contains(got, "Task s1") {
		t.Errorf("info success: %q", got)
	}
	got = formatSubagentsCommand(bgCtx, coord, "ses", []string{"stop", "abc"})
	if !strings.Contains(got, "Stopping subagent task: abc") {
		t.Errorf("stop: %q", got)
	}
	got = formatSubagentsCommand(bgCtx, coord, "ses", []string{"stop"})
	if !strings.Contains(got, "Usage: /subagents stop") {
		t.Errorf("stop short: %q", got)
	}
	coord.stopOK = false
	got = formatSubagentsCommand(bgCtx, coord, "ses", []string{"stop", "abc"})
	if !strings.Contains(got, "Subagent task not running") {
		t.Errorf("stop failure: %q", got)
	}
	coord.stopOK = true
	got = formatSubagentsCommand(bgCtx, coord, "ses", []string{"continue"})
	if !strings.Contains(got, "Usage: /subagents continue") {
		t.Errorf("continue short: %q", got)
	}
	got = formatSubagentsCommand(bgCtx, coord, "ses", []string{"continue", "abc", "   "})
	if !strings.Contains(got, "Usage: /subagents continue") {
		t.Errorf("continue empty: %q", got)
	}
	coord.err = errors.New("boom")
	got = formatSubagentsCommand(bgCtx, coord, "ses", []string{"continue", "abc", "gogo"})
	if !strings.Contains(got, "Unable to continue subagent task") {
		t.Errorf("continue error: %q", got)
	}
	coord.err = nil
	got = formatSubagentsCommand(bgCtx, coord, "ses", []string{"continue", "abc", "gogo"})
	if !strings.Contains(got, "continued ok") {
		t.Errorf("continue success: %q", got)
	}
	got = formatSubagentsCommand(bgCtx, coord, "ses", []string{"bogus"})
	if !strings.Contains(got, "Usage: /subagents [info|stop|continue]") {
		t.Errorf("unknown: %q", got)
	}
}

// covCoordinatorStub implements toolCoordinator for formatter tests.
type covCoordinatorStub struct {
	task   *tools.SubagentTask
	found  bool
	stopOK bool
	resp   string
	err    error
}

func (m *covCoordinatorStub) listRunningSubagentTasks() []*tools.SubagentTask {
	if m.task == nil {
		return nil
	}
	return []*tools.SubagentTask{m.task}
}
func (m *covCoordinatorStub) getSubagentTask(id string) (*tools.SubagentTask, bool) {
	if m.found {
		return m.task, true
	}
	return nil, false
}
func (m *covCoordinatorStub) stopSubagentTask(id string) bool { return m.stopOK }
func (m *covCoordinatorStub) continueSubagentTask(ctx context.Context, ses, id, g string) (string, error) {
	return m.resp, m.err
}
func (m *covCoordinatorStub) updateToolContexts(a *AgentInstance, c, ci, s string) {}
func (m *covCoordinatorStub) stopAllSubagents() int                               { return 0 }
func (m *covCoordinatorStub) stopSessionSubagents(s string) int                   { return 0 }
func (m *covCoordinatorStub) cancelAll() int                                      { return 0 }
func (m *covCoordinatorStub) cancelSession(s string)                              {}
func (m *covCoordinatorStub) markSessionSubagentsDelivered(s string)              {}
func (m *covCoordinatorStub) markSubagentDelivered(id string) bool                { return false }
func (m *covCoordinatorStub) GetStartupInfo() map[string]interface{}              { return nil }
func (m *covCoordinatorStub) RegisterTool(t tools.Tool)                           {}
func (m *covCoordinatorStub) GetSubagents() map[string]*tools.SubagentManager     { return nil }
func (m *covCoordinatorStub) getBackgroundExecs(c bool) []BackgroundExecInfo      { return nil }
func (m *covCoordinatorStub) getBackgroundExecOutput(id string, tail int) (string, string, time.Duration, error) {
	return "", "", 0, nil
}
func (m *covCoordinatorStub) stopBackgroundExec(id string) error { return nil }

// ---- tool_coordinator ----

func TestToolCoordinatorStub_ImplementsInterface(t *testing.T) {
	var _ toolCoordinator = (*covCoordinatorStub)(nil)
	var _ toolCoordinator = (*toolCoordinatorImpl)(nil)
}

func TestToolCoordinator_SubagentLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := buildCovConfig(tmpDir)
	al := NewAgentLoop(cfg, bus.NewMessageBus())

	provider := &mockProvider{mockResponse: "resp"}
	svc := tools.NewSubagentManager(provider, "m", tmpDir, al.bus, 5)
	managers := map[string]*tools.SubagentManager{
		al.getDefaultAgentID(): svc,
	}
	tc := newToolCoordinatorWithSubagents(al, managers, map[string]*tools.BackgroundProcessManager{})

	if n := tc.stopAllSubagents(); n != 0 {
		t.Errorf("stopAllSubagents on empty = %d", n)
	}
	if n := tc.cancelAll(); n != 0 {
		t.Errorf("cancelAll = %d", n)
	}
	if len(tc.GetSubagents()) != 0 {
		t.Errorf("expected cleared subagents")
	}
	if _, ok := tc.getSubagentTask("x"); ok {
		t.Error("expected not found")
	}
	if tc.stopSubagentTask("x") {
		t.Error("expected false stop on missing")
	}
	if n := len(tc.listRunningSubagentTasks()); n != 0 {
		t.Errorf("empty running tasks = %d", n)
	}
	if n := len(tc.getBackgroundExecs(true)); n != 0 {
		t.Errorf("empty bg execs = %d", n)
	}
	if _, _, _, err := tc.getBackgroundExecOutput("nope", 0); err == nil {
		t.Error("expected not found error")
	}
	if err := tc.stopBackgroundExec("nope"); err == nil {
		t.Error("expected not found error")
	}
}

func TestToolCoordinator_GetStartupInfo(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := buildCovConfig(tmpDir)
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	tc := newToolCoordinator(al)
	info := tc.GetStartupInfo()
	if info["tools"] == nil {
		t.Error("expected tools info")
	}
	if info["skills"] == nil || info["agents"] == nil {
		t.Error("expected skills and agents info")
	}
}

func TestToolCoordinator_GetBgManagers(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := buildCovConfig(tmpDir)
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	tc := newToolCoordinator(al)
	if tc.GetBgManagers() == nil {
		t.Error("expected non-nil bg managers")
	}
}

func TestToolCoordinator_RegisterTool(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := buildCovConfig(tmpDir)
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	tc := newToolCoordinator(al)
	before := len(al.registry.GetDefaultAgent().Tools.List())
	tc.RegisterTool(deployFakeTool{})
	after := len(al.registry.GetDefaultAgent().Tools.List())
	if after <= before {
		t.Errorf("expected tool to be registered: before=%d after=%d", before, after)
	}
}

func TestToolCoordinator_StopSessionSubagents(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := buildCovConfig(tmpDir)
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	tc := newToolCoordinator(al)
	if n := tc.stopSessionSubagents("ses"); n != 0 {
		t.Errorf("expected 0 stopped, got %d", n)
	}
	tc.markSessionSubagentsDelivered("ses")
	_, _ = tc.continueSubagentTask(bgCtx, "ses", "nope", "g")
}

func buildCovConfig(tmpDir string) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Workspace: tmpDir, Model: "testp:m", MaxTokens: 4096, MaxToolIterations: 10}},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"testp": {Type: "openai", ProviderConfig: config.ProviderConfig{APIKey: "k", APIBase: "https://x"}},
			},
		},
	}
}

// deployFakeTool is a minimal Tool implementation for RegisterTool test.
type deployFakeTool struct{}

func (deployFakeTool) Name() string        { return "cov_fake_tool" }
func (deployFakeTool) Description() string { return "cov fake" }
func (deployFakeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{}
}
func (deployFakeTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	return tools.SilentResult("ok")
}