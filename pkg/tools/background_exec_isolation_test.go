package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// BackgroundProcess.VisibleTo
// ---------------------------------------------------------------------------

func TestBackgroundProcess_VisibleTo(t *testing.T) {
	tests := []struct {
		name   string
		owner  string
		caller string
		want   bool
	}{
		{"owner matches caller", "telegram:chat-1", "telegram:chat-1", true},
		{"parent sees subagent process", "telegram:chat-1:subagent-1", "telegram:chat-1", true},
		{"parent sees nested subagent process", "telegram:chat-1:subagent-1:subagent-2", "telegram:chat-1", true},
		{"sibling subagent hidden", "telegram:chat-1:subagent-1", "telegram:chat-1:subagent-2", false},
		{"different session hidden", "telegram:chat-1", "telegram:chat-2", false},
		{"prefix-only string is not parent", "telegram:chat-10", "telegram:chat-1", false},
		{"unowned visible to everyone", "", "telegram:chat-1", true},
		{"empty caller sees everything", "telegram:chat-1", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &BackgroundProcess{OwnerSessionKey: tt.owner}
			if got := p.VisibleTo(tt.caller); got != tt.want {
				t.Errorf("VisibleTo(%q) with owner %q = %v, want %v", tt.caller, tt.owner, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Manager session-scoped accessors
// ---------------------------------------------------------------------------

func TestBackgroundProcessManager_SessionScoping(t *testing.T) {
	mgr := NewBackgroundProcessManager()

	var stdout, stderr threadSafeBuffer
	owned := mgr.Register(exec.Command("echo", "a"), "echo a", "/tmp", &stdout, &stderr, func() {}, "telegram:chat-1")
	ownedSub := mgr.Register(exec.Command("echo", "b"), "echo b", "/tmp", &stdout, &stderr, func() {}, "telegram:chat-1:subagent-1")
	foreign := mgr.Register(exec.Command("echo", "c"), "echo c", "/tmp", &stdout, &stderr, func() {}, "telegram:chat-2")
	unowned := mgr.Register(exec.Command("echo", "d"), "echo d", "/tmp", &stdout, &stderr, func() {}, "")

	// Owner sees own + subagent + unowned, but not foreign.
	visible := mgr.ListForSession("telegram:chat-1")
	ids := processIDs(visible)
	if !containsID(ids, owned.ID) || !containsID(ids, ownedSub.ID) || !containsID(ids, unowned.ID) {
		t.Errorf("owner should see own, subagent and unowned processes, got %v", ids)
	}
	if containsID(ids, foreign.ID) {
		t.Errorf("owner must not see foreign process, got %v", ids)
	}

	// Foreign session sees only its own + unowned.
	visible = mgr.ListForSession("telegram:chat-2")
	ids = processIDs(visible)
	if !containsID(ids, foreign.ID) || !containsID(ids, unowned.ID) {
		t.Errorf("foreign session should see its own and unowned processes, got %v", ids)
	}
	if containsID(ids, owned.ID) || containsID(ids, ownedSub.ID) {
		t.Errorf("foreign session must not see other sessions' processes, got %v", ids)
	}

	// GetForSession hides foreign processes.
	if _, ok := mgr.GetForSession(foreign.ID, "telegram:chat-1"); ok {
		t.Error("GetForSession must hide foreign process from other session")
	}
	if _, ok := mgr.GetForSession(foreign.ID, "telegram:chat-2"); !ok {
		t.Error("GetForSession must return own process")
	}
	if _, ok := mgr.GetForSession(ownedSub.ID, "telegram:chat-1"); !ok {
		t.Error("GetForSession must return subagent process to parent")
	}
	if _, ok := mgr.GetForSession("bg-999", "telegram:chat-1"); ok {
		t.Error("GetForSession must return false for missing process")
	}

	// ListRunningForSession applies the same filter.
	running := mgr.ListRunningForSession("telegram:chat-1")
	ids = processIDs(running)
	if containsID(ids, foreign.ID) {
		t.Errorf("ListRunningForSession must hide foreign process, got %v", ids)
	}
}

// ---------------------------------------------------------------------------
// Tool-level isolation (list / get / stop)
// ---------------------------------------------------------------------------

func TestBackgroundExecTools_SessionIsolation(t *testing.T) {
	mgr := NewBackgroundProcessManager()

	var stdout, stderr threadSafeBuffer
	stdout.Write([]byte("secret output"))
	proc := mgr.Register(exec.Command("sleep", "30"), "sleep 30", "/tmp", &stdout, &stderr, func() {}, "telegram:chat-1")

	ownerCtx := WithAgentToolContext(context.Background(), "main", "telegram:chat-1")
	otherCtx := WithAgentToolContext(context.Background(), "main", "telegram:chat-2")

	listTool := NewListBackgroundExecsTool(mgr)
	getTool := NewGetBackgroundExecOutputTool(mgr)
	stopTool := NewStopBackgroundExecTool(mgr)

	// Owner sees the process.
	res := listTool.Execute(ownerCtx, map[string]interface{}{"include_completed": true})
	if !strings.Contains(res.ForLLM, proc.ID) {
		t.Errorf("owner list should contain %s, got: %s", proc.ID, res.ForLLM)
	}

	// Other session does not see it.
	res = listTool.Execute(otherCtx, map[string]interface{}{"include_completed": true})
	if strings.Contains(res.ForLLM, proc.ID) {
		t.Errorf("foreign list must not contain %s, got: %s", proc.ID, res.ForLLM)
	}

	// Other session cannot read output.
	res = getTool.Execute(otherCtx, map[string]interface{}{"id": proc.ID})
	if !res.IsError || !strings.Contains(res.ForLLM, "not found") {
		t.Errorf("foreign get should report not found, got error=%v: %s", res.IsError, res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "secret output") {
		t.Error("foreign get must not leak process output")
	}

	// Other session cannot stop it.
	res = stopTool.Execute(otherCtx, map[string]interface{}{"id": proc.ID})
	if !res.IsError || !strings.Contains(res.ForLLM, "not found") {
		t.Errorf("foreign stop should report not found, got error=%v: %s", res.IsError, res.ForLLM)
	}
	if p, _ := mgr.Get(proc.ID); p.Status != BgExecStatusRunning {
		t.Errorf("foreign stop must not affect process, status=%s", p.Status)
	}

	// Owner can read output.
	res = getTool.Execute(ownerCtx, map[string]interface{}{"id": proc.ID})
	if res.IsError || !strings.Contains(res.ForLLM, "secret output") {
		t.Errorf("owner get should return output, got error=%v: %s", res.IsError, res.ForLLM)
	}

	// Owner can stop it.
	res = stopTool.Execute(ownerCtx, map[string]interface{}{"id": proc.ID})
	if res.IsError {
		t.Errorf("owner stop should succeed, got: %s", res.ForLLM)
	}
	if p, _ := mgr.Get(proc.ID); p.Status != BgExecStatusStopped {
		t.Errorf("owner stop should stop process, status=%s", p.Status)
	}
}

// TestBackgroundExecTools_NoSessionKey verifies backward compatibility:
// callers without a session key (e.g. legacy paths) still see everything.
func TestBackgroundExecTools_NoSessionKey(t *testing.T) {
	mgr := NewBackgroundProcessManager()

	var stdout, stderr threadSafeBuffer
	proc := mgr.Register(exec.Command("echo", "x"), "echo x", "/tmp", &stdout, &stderr, func() {}, "telegram:chat-1")

	listTool := NewListBackgroundExecsTool(mgr)
	res := listTool.Execute(context.Background(), map[string]interface{}{"include_completed": true})
	if !strings.Contains(res.ForLLM, proc.ID) {
		t.Errorf("caller without session key should see all processes, got: %s", res.ForLLM)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func processIDs(procs []*BackgroundProcess) []string {
	ids := make([]string, 0, len(procs))
	for _, p := range procs {
		ids = append(ids, p.ID)
	}
	return ids
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
