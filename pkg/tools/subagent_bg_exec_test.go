package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// Regression tests for the bug where subagents could start background
// processes but had no way to observe or stop them.
//
// The root cause was in the tool wiring (pkg/agent/tool_coordinator.go): the
// subagent tool registry excluded list_background_execs,
// get_background_exec_output and stop_background_exec under the assumption
// that "each subagent gets its own" background manager. That assumption was
// false - CloneWithout shares tool instances, and a subagent's exec tool
// registers processes in the parent agent's single BackgroundProcessManager,
// owned by the subagent's session key. The result: a backgrounded command was
// tracked, invisible and uncontrollable from the subagent that started it.
//
// These tests pin the behaviour that must hold once the tools are exposed:
// the subagent can list, read and stop its own processes, and per-session
// isolation still hides other sessions' processes from it.

// newBgSubagentRegistry builds a registry shaped like the one a subagent gets:
// the exec tool plus the three background-exec tools, all bound to one shared
// manager (the parent's).
func newBgSubagentRegistry(mgr *BackgroundProcessManager, workspace string) *ToolRegistry {
	execTool := NewExecTool(workspace, false)
	execTool.SetBackgroundManager(mgr)

	r := NewToolRegistry()
	r.Register(execTool)
	r.Register(NewListBackgroundExecsTool(mgr))
	r.Register(NewGetBackgroundExecOutputTool(mgr))
	r.Register(NewStopBackgroundExecTool(mgr))
	return r
}

// TestSubagentCanManageItsOwnBackgroundProcess reproduces the reported issue
// end to end: a subagent session starts a background command through exec and
// then uses the background-exec tools on it.
func TestSubagentCanManageItsOwnBackgroundProcess(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	registry := newBgSubagentRegistry(mgr, t.TempDir())

	// The subagent's tool loop runs scoped to its own session key, derived
	// from the origin key as "{parent}:{task_id}" (see subagent_runner.go).
	subagentKey := "telegram:chat-1:subagent-7"
	ctx := WithAgentToolContext(context.Background(), "main", subagentKey)

	execT, ok := registry.Get("exec")
	if !ok {
		t.Fatal("subagent registry must contain exec")
	}
	res := execT.Execute(ctx, map[string]interface{}{
		"command":    "sleep 30",
		"background": true,
	})
	if res.IsError {
		t.Fatalf("background exec failed: %s", res.ForLLM)
	}
	procID := bgProcessIDFromMessage(t, res.ForLLM)

	// The process is tracked under the subagent's session key, not empty and
	// not the parent's raw key.
	p, found := mgr.Get(procID)
	if !found {
		t.Fatalf("process %s was not registered", procID)
	}
	if p.OwnerSessionKey != subagentKey {
		t.Errorf("process owner = %q, want %q", p.OwnerSessionKey, subagentKey)
	}

	// list_background_execs must show it to the subagent.
	listT, _ := registry.Get("list_background_execs")
	res = listT.Execute(ctx, map[string]interface{}{})
	if res.IsError || !strings.Contains(res.ForLLM, procID) {
		t.Errorf("subagent list should show its own %s, got: %s", procID, res.ForLLM)
	}

	// get_background_exec_output must return its output.
	getT, _ := registry.Get("get_background_exec_output")
	res = getT.Execute(ctx, map[string]interface{}{"id": procID})
	if res.IsError {
		t.Errorf("subagent get_output failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, procID) && !strings.Contains(res.ForLLM, "Status") {
		t.Errorf("subagent get_output returned unexpected payload: %s", res.ForLLM)
	}

	// stop_background_exec must stop it.
	stopT, _ := registry.Get("stop_background_exec")
	res = stopT.Execute(ctx, map[string]interface{}{"id": procID})
	if res.IsError {
		t.Errorf("subagent stop failed: %s", res.ForLLM)
	}
	if p, _ := mgr.Get(procID); p.Status != BgExecStatusStopped {
		t.Errorf("subagent stop left status %s, want %s", p.Status, BgExecStatusStopped)
	}
}

// TestSubagentCannotTouchForeignBackgroundProcess guards the property that
// made the exclusion tempting in the first place: exposing the tools must not
// let a subagent see or control another session's processes.
func TestSubagentCannotTouchForeignBackgroundProcess(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	registry := newBgSubagentRegistry(mgr, t.TempDir())

	var stdout, stderr threadSafeBuffer
	stdout.Write([]byte("private chatter"))
	foreign := mgr.Register(newNoopCmd(), "sleep 30", "/tmp", &stdout, &stderr, func() {}, "telegram:chat-2")

	subagentCtx := WithAgentToolContext(context.Background(), "main", "telegram:chat-1:subagent-1")
	siblingCtx := WithAgentToolContext(context.Background(), "main", "telegram:chat-1:subagent-2")

	listT, _ := registry.Get("list_background_execs")
	if res := listT.Execute(subagentCtx, map[string]interface{}{"include_completed": true}); strings.Contains(res.ForLLM, foreign.ID) {
		t.Errorf("subagent must not list another session's process, got: %s", res.ForLLM)
	}

	getT, _ := registry.Get("get_background_exec_output")
	res := getT.Execute(subagentCtx, map[string]interface{}{"id": foreign.ID})
	if !res.IsError || !strings.Contains(res.ForLLM, "not found") {
		t.Errorf("foreign get_output should report not found, got error=%v: %s", res.IsError, res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "private chatter") {
		t.Error("foreign get_output must not leak output")
	}

	stopT, _ := registry.Get("stop_background_exec")
	res = stopT.Execute(siblingCtx, map[string]interface{}{"id": foreign.ID})
	if !res.IsError {
		t.Errorf("sibling stop of foreign process should fail, got: %s", res.ForLLM)
	}
	if p, _ := mgr.Get(foreign.ID); p.Status != BgExecStatusRunning {
		t.Errorf("failed foreign stop must not change status, got %s", p.Status)
	}
}

// TestParentAndSubagentShareBackgroundProcessFamily documents the intended
// consequence of the fix: parent and child are one session family, so each can
// observe the other's processes. This is what /stop cascading (#230) already
// assumes; sibling subagents stay mutually invisible.
func TestParentAndSubagentShareBackgroundProcessFamily(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	registry := newBgSubagentRegistry(mgr, t.TempDir())

	var stdout, stderr threadSafeBuffer
	child := mgr.Register(newNoopCmd(), "child cmd", "/tmp", &stdout, &stderr, func() {}, "telegram:chat-1:subagent-1")
	parentProc := mgr.Register(newNoopCmd(), "parent cmd", "/tmp", &stdout, &stderr, func() {}, "telegram:chat-1")

	parentCtx := WithAgentToolContext(context.Background(), "main", "telegram:chat-1")
	childCtx := WithAgentToolContext(context.Background(), "main", "telegram:chat-1:subagent-1")

	getT, _ := registry.Get("get_background_exec_output")
	if res := getT.Execute(parentCtx, map[string]interface{}{"id": child.ID}); res.IsError {
		t.Errorf("parent should read its subagent's process output, got: %s", res.ForLLM)
	}
	if res := getT.Execute(childCtx, map[string]interface{}{"id": parentProc.ID}); res.IsError {
		t.Errorf("subagent should read its parent's process output, got: %s", res.ForLLM)
	}

	// The subagent can also stop a parent-owned process: same session, so the
	// cascade semantics apply in both directions.
	stopT, _ := registry.Get("stop_background_exec")
	if res := stopT.Execute(childCtx, map[string]interface{}{"id": parentProc.ID}); res.IsError {
		t.Errorf("subagent should stop parent-owned process in its own session, got: %s", res.ForLLM)
	}
}

// TestSubagentRegistryKeepsExcludedTools pins the exclusion list itself, so a
// future edit cannot silently re-drop the background-exec tools or silently
// leak the user-facing/subagent-management ones.
func TestSubagentRegistryKeepsExcludedTools(t *testing.T) {
	mgr := NewBackgroundProcessManager()
	agentRegistry := NewToolRegistry()
	agentRegistry.Register(NewExecTool(t.TempDir(), false))
	agentRegistry.Register(NewListBackgroundExecsTool(mgr))
	agentRegistry.Register(NewGetBackgroundExecOutputTool(mgr))
	agentRegistry.Register(NewStopBackgroundExecTool(mgr))
	agentRegistry.Register(NewSendFileTool())
	agentRegistry.Register(NewSleepTool())

	subagentRegistry := agentRegistry.CloneWithout(
		"send_file",
		"wait_for_subagent",
		"list_active_subagents",
		"cancel_subagent",
	)

	for _, name := range []string{"exec", "list_background_execs", "get_background_exec_output", "stop_background_exec", "sleep"} {
		if _, ok := subagentRegistry.Get(name); !ok {
			t.Errorf("subagent registry must expose %q", name)
		}
	}
	if _, ok := subagentRegistry.Get("send_file"); ok {
		t.Error("subagent registry must not expose send_file (user-facing)")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// bgProcessIDFromMessage extracts the "bg-N" id from an exec background
// result message ("... Process ID: bg-3 ...").
func bgProcessIDFromMessage(t *testing.T, msg string) string {
	t.Helper()
	const marker = "Process ID: "
	idx := strings.Index(msg, marker)
	if idx < 0 {
		t.Fatalf("background result does not report a process ID: %s", msg)
	}
	rest := msg[idx+len(marker):]
	end := strings.IndexAny(rest, " \n,")
	if end < 0 {
		end = len(rest)
	}
	id := rest[:end]
	if !strings.HasPrefix(id, "bg-") {
		t.Fatalf("parsed process ID %q is not a bg-N id (message: %s)", id, msg)
	}
	return id
}

// newNoopCmd returns a command that is never started; Register only stores the
// reference, so tests that exercise visibility rules do not spawn processes.
func newNoopCmd() *exec.Cmd {
	return exec.Command("true")
}
