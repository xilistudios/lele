package group

import (
	"context"
	"fmt"
	"testing"
)

// toolCallExecutor is a TurnExecutor that invokes OnToolCall twice
// (executing then completed) for a single tool call, then returns content.
type toolCallExecutor struct {
	toolID   string
	toolName string
	args     string
	result   string
}

func (e *toolCallExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	if req.OnToolCall != nil {
		req.OnToolCall(e.toolID, e.toolName, e.args, "executing", "")
		req.OnToolCall(e.toolID, e.toolName, e.args, "completed", e.result)
	}
	return "tool-turn-content", 42, nil
}

func TestToolCalls_PersistedOnTurn(t *testing.T) {
	exec := &toolCallExecutor{
		toolID:   "call-abc-123",
		toolName: "read_file",
		args:     `{"path":"/x"}`,
		result:   "ok",
	}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "tc-1", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 1}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	_, err = gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found after Wait")
	}
	if st.Status != StatusDone {
		t.Errorf("status = %s, want done", st.Status)
	}
	if len(st.Transcript) != 1 {
		t.Fatalf("transcript length = %d, want 1", len(st.Transcript))
	}

	turn := st.Transcript[0]
	if len(turn.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length = %d, want 1", len(turn.ToolCalls))
	}

	tc := turn.ToolCalls[0]
	if tc.ToolCallID != "call-abc-123" {
		t.Errorf("ToolCallID = %q, want %q", tc.ToolCallID, "call-abc-123")
	}
	if tc.Tool != "read_file" {
		t.Errorf("Tool = %q, want %q", tc.Tool, "read_file")
	}
	if tc.Status != "completed" {
		t.Errorf("Status = %q, want %q", tc.Status, "completed")
	}
	if tc.Arguments != `{"path":"/x"}` {
		t.Errorf("Arguments = %q, want %q", tc.Arguments, `{"path":"/x"}`)
	}
	if tc.Result != "ok" {
		t.Errorf("Result = %q, want %q", tc.Result, "ok")
	}

	// Verify group.tool events were still published (bus side unchanged).
	toolEvents := pub.byEvent("group.tool")
	if len(toolEvents) != 2 {
		t.Errorf("group.tool events = %d, want 2", len(toolEvents))
	}
}

func TestToolCalls_NoToolCallsMeansNilSlice(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "notc-1", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 1}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	_, err = gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}

	turn := st.Transcript[0]
	if turn.ToolCalls != nil {
		t.Errorf("ToolCalls = %v, want nil (no tools used)", turn.ToolCalls)
	}
}

// errorToolCallExecutor invokes OnToolCall with status "executing" then "error".
type errorToolCallExecutor struct {
	toolID   string
	toolName string
	args     string
	errMsg   string
}

func (e *errorToolCallExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	if req.OnToolCall != nil {
		req.OnToolCall(e.toolID, e.toolName, e.args, "executing", "")
		req.OnToolCall(e.toolID, e.toolName, e.args, "error", e.errMsg)
	}
	return "error-turn", 10, nil
}

func TestToolCalls_ErrorStatus(t *testing.T) {
	exec := &errorToolCallExecutor{
		toolID:   "call-err-1",
		toolName: "web_fetch",
		args:     `{"url":"https://example.com"}`,
		errMsg:   "timeout",
	}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "tc-err-1", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 1}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	_, err = gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}

	turn := st.Transcript[0]
	if len(turn.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length = %d, want 1", len(turn.ToolCalls))
	}

	tc := turn.ToolCalls[0]
	if tc.Status != "error" {
		t.Errorf("Status = %q, want %q", tc.Status, "error")
	}
	if tc.Result != "timeout" {
		t.Errorf("Result = %q, want %q", tc.Result, "timeout")
	}
	if tc.Arguments != `{"url":"https://example.com"}` {
		t.Errorf("Arguments = %q, want %q", tc.Arguments, `{"url":"https://example.com"}`)
	}
}

// multiToolCallExecutor invokes OnToolCall for two distinct tool calls.
type multiToolCallExecutor struct{}

func (e *multiToolCallExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	if req.OnToolCall != nil {
		req.OnToolCall("call-1", "read_file", `{"path":"/a"}`, "executing", "")
		req.OnToolCall("call-2", "web_search", `{"q":"golang"}`, "executing", "")
		req.OnToolCall("call-1", "read_file", `{"path":"/a"}`, "completed", "content-a")
		req.OnToolCall("call-2", "web_search", `{"q":"golang"}`, "completed", "results")
	}
	return "multi-turn", 20, nil
}

func TestToolCalls_MultipleDistinctCalls(t *testing.T) {
	exec := &multiToolCallExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "tc-multi-1", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 1}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	_, err = gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}

	turn := st.Transcript[0]
	if len(turn.ToolCalls) != 2 {
		t.Fatalf("ToolCalls length = %d, want 2", len(turn.ToolCalls))
	}

	// Verify both are present and completed.
	byID := make(map[string]GroupToolCall)
	for _, tc := range turn.ToolCalls {
		byID[tc.ToolCallID] = tc
	}

	tc1, ok := byID["call-1"]
	if !ok {
		t.Fatal("missing tool call call-1")
	}
	if tc1.Tool != "read_file" || tc1.Status != "completed" || tc1.Result != "content-a" {
		t.Errorf("call-1 = %+v", tc1)
	}

	tc2, ok := byID["call-2"]
	if !ok {
		t.Fatal("missing tool call call-2")
	}
	if tc2.Tool != "web_search" || tc2.Status != "completed" || tc2.Result != "results" {
		t.Errorf("call-2 = %+v", tc2)
	}
}

// completedOnlyExecutor sends a "completed" event without a prior "executing".
type completedOnlyExecutor struct{}

func (e *completedOnlyExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	if req.OnToolCall != nil {
		req.OnToolCall("call-solo", "exec_code", `{"cmd":"ls"}`, "completed", "file.go")
	}
	return "solo", 5, nil
}

func TestToolCalls_CompletedWithoutExecuting(t *testing.T) {
	exec := &completedOnlyExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "tc-solo", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 1}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	_, err = gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}

	turn := st.Transcript[0]
	if len(turn.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length = %d, want 1", len(turn.ToolCalls))
	}

	tc := turn.ToolCalls[0]
	if tc.ToolCallID != "call-solo" {
		t.Errorf("ToolCallID = %q, want %q", tc.ToolCallID, "call-solo")
	}
	if tc.Status != "completed" {
		t.Errorf("Status = %q, want %q", tc.Status, "completed")
	}
	if tc.Result != "file.go" {
		t.Errorf("Result = %q, want %q", tc.Result, "file.go")
	}
}

// Verify that tool calls from one speaker don't leak into another speaker's turn.
type speakerSpecificExecutor struct {
	calls map[string]*toolCallExecutor // keyed by speaker
}

func (e *speakerSpecificExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	if tcExec, ok := e.calls[req.Speaker]; ok {
		if req.OnToolCall != nil {
			req.OnToolCall(tcExec.toolID, tcExec.toolName, tcExec.args, "executing", "")
			req.OnToolCall(tcExec.toolID, tcExec.toolName, tcExec.args, "completed", tcExec.result)
		}
	}
	return fmt.Sprintf("turn-%s", req.Speaker), 10, nil
}

func TestToolCalls_IsolatedPerSpeaker(t *testing.T) {
	exec := &speakerSpecificExecutor{
		calls: map[string]*toolCallExecutor{
			"a": {toolID: "call-a", toolName: "tool_a", args: `{"a":1}`, result: "res-a"},
			// "b" has no tool calls.
		},
	}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
		plainParticipant("b"),
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "tc-iso", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 1}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	_, err = gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}
	if len(st.Transcript) != 2 {
		t.Fatalf("transcript length = %d, want 2", len(st.Transcript))
	}

	// Turn 0 (speaker "a") should have tool calls.
	if len(st.Transcript[0].ToolCalls) != 1 {
		t.Errorf("turn[0].ToolCalls length = %d, want 1", len(st.Transcript[0].ToolCalls))
	}

	// Turn 1 (speaker "b") should have no tool calls.
	if st.Transcript[1].ToolCalls != nil {
		t.Errorf("turn[1].ToolCalls = %v, want nil", st.Transcript[1].ToolCalls)
	}
}
