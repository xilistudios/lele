// Lele - coverage tests (round 2): tool coordinator background exec,
// subagent delivered marking, SetSessionAgent migration, and related paths.
// License: MIT

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

// ============================================================================
// tool_coordinator — getBackgroundExecs / getBackgroundExecOutput / stopBackgroundExec
// ============================================================================

func TestToolCoordinator_BackgroundExec_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := buildCovConfig(tmpDir)
	al := NewAgentLoop(cfg, bus.NewMessageBus())

	tc := al.toolCoordinator.(*toolCoordinatorImpl)
	agent := al.registry.GetDefaultAgent()

	// Launch a real background command via the exec tool so the manager
	// registers a process (Register is not accessible from outside package tools).
	execTool, ok := agent.Tools.Get("exec")
	if !ok {
		t.Skip("no exec tool registered")
	}
	ctx := tools.WithAgentToolContext(context.Background(), agent.ID, "native:bgctx")
	res := execTool.Execute(ctx, map[string]interface{}{
		"command":    "echo hello background; sleep 30",
		"background": true,
	})
	if res.IsError {
		t.Fatalf("background exec error: %s", res.ForLLM)
	}

	// Find the process registered against this coordinator's manager.
	all := tc.getBackgroundExecs(true)
	if len(all) == 0 {
		t.Skip("no background processes registered")
	}
	procID := all[0].ID

	// Verify getBackgroundExecs metadata populated.
	found := false
	for _, info := range all {
		if info.ID == procID {
			found = true
			if info.Status == "" {
				t.Error("expected non-empty status")
			}
			if info.StartTime.IsZero() {
				t.Error("expected non-zero start time")
			}
		}
	}
	if !found {
		t.Fatalf("process %s not found in getBackgroundExecs result", procID)
	}

	// getBackgroundExecOutput with tail>0.
	output, status, elapsed, err := tc.getBackgroundExecOutput(procID, 5)
	if err != nil {
		t.Fatalf("getBackgroundExecOutput: %v", err)
	}
	if status == "" {
		t.Error("expected non-empty status")
	}
	if elapsed < 0 {
		t.Error("elapsed should be non-negative")
	}
	_ = output

	// getBackgroundExecOutput with tail<=0 returns full output. Poll briefly
	// for the background process to flush its stdout into the capture buffer.
	var fullOutput string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		o, _, _, e := tc.getBackgroundExecOutput(procID, 0)
		if e == nil && strings.Contains(o, "hello background") {
			fullOutput = o
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(fullOutput, "hello background") {
		t.Errorf("output = %q, want to contain 'hello background'", fullOutput)
	}

	// stopBackgroundExec succeeds while running.
	if err := tc.stopBackgroundExec(procID); err != nil {
		t.Fatalf("stopBackgroundExec: %v", err)
	}

	// Stopping again (not running) returns error.
	if err := tc.stopBackgroundExec(procID); err == nil {
		t.Error("expected error stopping a not-running process")
	}
}

// TestToolCoordinator_BackgroundExec_NilManagers covers the bgManagers == nil
// passthrough and manager-not-found paths.
func TestToolCoordinator_BackgroundExec_NilManagers(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := buildCovConfig(tmpDir)
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	tc := newToolCoordinator(al)

	if got := tc.getBackgroundExecs(true); got != nil && len(got) != 0 {
		t.Errorf("expected empty result, got %d", len(got))
	}
	if _, _, _, err := tc.getBackgroundExecOutput("nope", 0); err == nil {
		t.Error("expected not-found error")
	}
	if err := tc.stopBackgroundExec("nope"); err == nil {
		t.Error("expected not-found error")
	}
}

// ============================================================================
// tool_coordinator — markSessionSubagentsDelivered / continueSubagentTask
// ============================================================================

// TestToolCoordinator_SubagentDeliveredTests covers markSessionSubagentsDelivered
// for terminal and non-matching tasks, continueSubagentTask not-found, and
// markSubagentDelivered on a missing task.
func TestToolCoordinator_SubagentDeliveredTests(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := buildCovConfig(tmpDir)
	al := NewAgentLoop(cfg, bus.NewMessageBus())

	provider := &mockProvider{mockResponse: "resp"}
	svc := tools.NewSubagentManager(provider, "m", tmpDir, al.bus, 5)
	managers := map[string]*tools.SubagentManager{al.getDefaultAgentID(): svc}
	tc := newToolCoordinatorWithSubagents(al, managers, map[string]*tools.BackgroundProcessManager{})

	// markSessionSubagentsDelivered with no tasks.
	tc.markSessionSubagentsDelivered("nonexistent:ses")

	// markSubagentDelivered for missing task -> false.
	if tc.markSubagentDelivered("nope") {
		t.Error("expected false for missing task")
	}
	// stopSubagentTask missing -> false.
	if tc.stopSubagentTask("nope") {
		t.Error("expected false stopping missing task")
	}
	// continueSubagentTask missing -> error.
	if _, err := tc.continueSubagentTask(context.Background(), "ses", "nope", "g"); err == nil {
		t.Error("expected error for missing task")
	}
	// listRunningSubagentTasks with empty manager -> empty.
	if n := len(tc.listRunningSubagentTasks()); n != 0 {
		t.Errorf("listRunningSubagentTasks = %d, want 0", n)
	}
	// getSubagentTask missing -> not found.
	if _, ok := tc.getSubagentTask("nope"); ok {
		t.Error("expected not found")
	}
}

// ============================================================================
// tool_coordinator — updateToolContexts (no-op with empty managers)
// ============================================================================

func TestToolCoordinator_UpdateToolContexts_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := buildCovConfig(tmpDir)
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	tc := newToolCoordinator(al)
	tc.updateToolContexts(al.registry.GetDefaultAgent(), "cli", "chat", "native:ctx")
	tc.stopAllSubagents()
}

// ============================================================================
// SetSessionAgent — migration paths
// ============================================================================

func TestSetSessionAgent_Migration(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.GetProvidable()
	key := "native:migration"

	defaultAgent := al.registry.GetDefaultAgent()
	// Seed a couple messages + a summary on the default agent's session.
	defaultAgent.Sessions.AddMessage(key, "user", "hello")
	defaultAgent.Sessions.AddMessage(key, "assistant", "world")
	defaultAgent.Sessions.SetSummary(key, "my summary")
	defaultAgent.Sessions.SetName(key, "my session")
	defaultAgent.Sessions.SetVerboseLevel(key, "basic")
	defaultAgent.Sessions.SetThinkingLevel(key, "low")

	// Switch to the "coder" agent.
	ap.SetSessionAgent(key, "coder")

	// Verify the session agent mapping updated.
	if got := ap.GetSessionAgent(key); got != "coder" {
		t.Errorf("GetSessionAgent = %q, want coder", got)
	}

	// Switching again to the same agent should be a no-op.
	ap.SetSessionAgent(key, "coder")

	// History should have been migrated to the new agent.
	coder, coderOK := al.registry.GetAgent("coder")
	if !coderOK {
		t.Fatalf("coder agent not found")
	}
	if got := coder.Sessions.GetHistory(key); len(got) != 2 {
		t.Errorf("migrated history len = %d, want 2", len(got))
	}
	if got := coder.Sessions.GetSummary(key); got != "my summary" {
		t.Errorf("migrated summary = %q, want 'my summary'", got)
	}
	if got := coder.Sessions.GetName(key); got != "my session" {
		t.Errorf("migrated name = %q, want 'my session'", got)
	}
}

// TestSetSessionAgent_NoHistory exercises migration with empty history (only
// the mapping is updated) and switching to a non-existent agent.
func TestSetSessionAgent_NoHistory(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.GetProvidable()
	key := "native:migration-empty"

	// Switch to a non-existent agent — should still set mapping.
	ap.SetSessionAgent(key, "does-not-exist")
	if got := ap.GetSessionAgent(key); got != "does-not-exist" {
		t.Errorf("GetSessionAgent = %q, want mapping updated", got)
	}

	// Switch back to a real agent with no history — no crash.
	ap.SetSessionAgent(key, "main")
	if got := ap.GetSessionAgent(key); got != "main" {
		t.Errorf("GetSessionAgent = %q, want main", got)
	}
}

// ============================================================================
// GetSessionModel (additional branches)
// ============================================================================

func TestGetSessionModel_Additional(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	key := "native:vision"

	// No session model -> falls back to agent model.
	_ = ap.GetSessionModel(key)

	// Set a vision model override.
	al.sessionModels.Store(key, "test:vision-model")
	if got := ap.GetSessionModel(key); got != "test:vision-model" {
		t.Errorf("GetSessionModel override = %q, want test:vision-model", got)
	}
	_ = ap.GetSessionModelSupportsImages(key)
} // ============================================================================
// session_manager — forceCompression
// ============================================================================

// TestForceCompression_NoSummary builds a session with >4 messages and no
// summary, forcing the local-summary construction branch.
func TestForceCompression_NoSummary(t *testing.T) {
	al := newCovTestLoop(t)
	main, _ := al.registry.GetAgent("main")
	key := "native:fc1"

	// Seed 9 messages (user/assistant alternating) plus a tool call round-trip.
	for i := 0; i < 4; i++ {
		main.Sessions.AddMessage(key, "user", "q"+string(rune('a'+i)))
		main.Sessions.AddMessage(key, "assistant", "a"+string(rune('a'+i)))
	}
	main.Sessions.AddMessage(key, "user", "final")
	if got := main.Sessions.GetSummary(key); got != "" {
		t.Fatalf("expected no summary, got %q", got)
	}

	sm := al.sessionManager.(*sessionManagerImpl)
	sm.forceCompression(main, key)

	if got := main.Sessions.GetSummary(key); got == "" {
		t.Error("expected auto-built local summary")
	} else if !strings.Contains(got, "Conversation Summary") {
		t.Errorf("summary = %q, want auto-compressed marker", got)
	}
	// Compaction counter should have incremented at least once.
	if got := al.providable.GetCompactionCount(key); got < 1 {
		t.Errorf("compaction count = %d, want >= 1", got)
	}
	// History with excluded markers: excluded messages must remain present
	// (they stay in storage) but be flagged ExcludeFromContext.
	hist := main.Sessions.GetHistoryView(key)
	excluded := 0
	for _, m := range hist {
		if m.ExcludeFromContext {
			excluded++
		}
	}
	if excluded == 0 {
		t.Errorf("expected some messages marked excluded, got %d", excluded)
	}
}

// TestForceCompression_TooShort covers the early-return when history <= 4.
func TestForceCompression_TooShort(t *testing.T) {
	al := newCovTestLoop(t)
	main, _ := al.registry.GetAgent("main")
	key := "native:fc2"
	main.Sessions.AddMessage(key, "user", "hi")
	main.Sessions.AddMessage(key, "assistant", "yo")

	sm := al.sessionManager.(*sessionManagerImpl)
	sm.forceCompression(main, key)

	if got := al.providable.GetCompactionCount(key); got != 0 {
		t.Errorf("compaction count = %d, want 0 (no-op)", got)
	}
}

// TestForceCompression_ExistingSummary exercises the branch where a summary
// already exists (no local-summary rebuild) with tool-call messages included.
func TestForceCompression_ExistingSummary(t *testing.T) {
	al := newCovTestLoop(t)
	main, _ := al.registry.GetAgent("main")
	key := "native:fc3"

	for i := 0; i < 4; i++ {
		main.Sessions.AddMessage(key, "user", "q"+string(rune('a'+i)))
		main.Sessions.AddFullMessage(key, providers.Message{Role: "assistant", Content: "tool call #" + string(rune('a'+i)), ToolCalls: []providers.ToolCall{{ID: "call-1", Function: &providers.FunctionCall{Name: "exec", Arguments: "{}"}}}})
	}
	main.Sessions.AddMessage(key, "user", "final")
	main.Sessions.SetSummary(key, "existing summary")

	sm := al.sessionManager.(*sessionManagerImpl)
	sm.forceCompression(main, key)

	// Summary stays unchanged (existing branch).
	if got := main.Sessions.GetSummary(key); got != "existing summary" {
		t.Errorf("summary = %q, want unchanged", got)
	}
	if got := al.providable.GetCompactionCount(key); got < 1 {
		t.Errorf("compaction count = %d, want >= 1", got)
	}
	// ExcludeOldMessagesFromContext paths with empty conversation all handled.
	hist := main.Sessions.GetHistoryView(key)
	if len(hist) == 0 {
		t.Fatal("expected history retained")
	}
}
