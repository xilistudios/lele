package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

type panicProvider struct{}

func (p *panicProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	panic("boom")
}

func (p *panicProvider) GetDefaultModel() string { return "test-model" }
func (p *panicProvider) SupportsTools() bool     { return false }
func (p *panicProvider) GetContextWindow() int   { return 4096 }

func TestSubagentPanicRecovery_SignalsDoneAndMarksFailed(t *testing.T) {
	manager := NewSubagentManager(&panicProvider{}, "test-model", t.TempDir(), nil, 10)

	task := &SubagentTask{
		ID:               "subagent-panic-1",
		Task:             "this will panic",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.AddTaskForTest(task, cancel)

	manager.safeGoroutine(task, func() {
		manager.runTask(ctx, task, nil)
	})

	select {
	case <-task.DoneChannel():
	case <-time.After(10 * time.Second):
		t.Fatal("SignalDone was never called — waiter would hang forever")
	}

	snap := task.Snapshot()
	if snap.Status != SubagentStatusFailed {
		t.Errorf("status = %q, want %q", snap.Status, SubagentStatusFailed)
	}
	if !strings.Contains(snap.Result, "boom") {
		t.Errorf("result = %q, want it to contain %q", snap.Result, "boom")
	}
	if !strings.Contains(snap.Summary, "boom") {
		t.Errorf("summary = %q, want it to contain %q", snap.Summary, "boom")
	}
}

func TestSubagentPanicRecovery_SlotFreedForNextTask(t *testing.T) {
	manager := NewSubagentManager(&panicProvider{}, "test-model", t.TempDir(), nil, 1)
	manager.SetMaxConcurrent(1)

	task1 := &SubagentTask{
		ID:               "subagent-panic-slot-1",
		Task:             "this will panic",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task1.InitDoneChannel()

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	manager.AddTaskForTest(task1, cancel1)

	manager.safeGoroutine(task1, func() {
		manager.runTask(ctx1, task1, nil)
	})

	select {
	case <-task1.DoneChannel():
	case <-time.After(10 * time.Second):
		t.Fatal("task1: SignalDone was never called")
	}

	if snap := task1.Snapshot(); snap.Status != SubagentStatusFailed {
		t.Fatalf("task1 status = %q, want %q", snap.Status, SubagentStatusFailed)
	}

	normalProvider := &flakySubagentProvider{
		failAt: 0,
		final:  "STATUS: completed\nSUMMARY: Done\nDETAILS:\nAll good",
	}
	manager.mu.Lock()
	manager.provider = normalProvider
	manager.mu.Unlock()

	task2 := &SubagentTask{
		ID:               "subagent-panic-slot-2",
		Task:             "this should succeed",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task2.InitDoneChannel()

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	manager.AddTaskForTest(task2, cancel2)

	manager.safeGoroutine(task2, func() {
		manager.runTask(ctx2, task2, nil)
	})

	select {
	case <-task2.DoneChannel():
	case <-time.After(10 * time.Second):
		t.Fatal("task2: SignalDone was never called")
	}

	snap2 := task2.Snapshot()
	if snap2.Status != SubagentStatusCompleted {
		t.Errorf("task2 status = %q, want %q (result=%q)", snap2.Status, SubagentStatusCompleted, snap2.Result)
	}
}

func TestSubagentPanicRecovery_ContinueTaskPath(t *testing.T) {
	manager := NewSubagentManager(&panicProvider{}, "test-model", t.TempDir(), nil, 10)

	task := &SubagentTask{
		ID:               "subagent-panic-continue",
		Task:             "continued task that panics",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.AddTaskForTest(task, cancel)

	manager.safeGoroutine(task, func() {
		manager.runTask(ctx, task, nil)
	})

	select {
	case <-task.DoneChannel():
	case <-time.After(10 * time.Second):
		t.Fatal("SignalDone was never called — waiter would hang forever")
	}

	snap := task.Snapshot()
	if snap.Status != SubagentStatusFailed {
		t.Errorf("status = %q, want %q", snap.Status, SubagentStatusFailed)
	}
	if !strings.Contains(snap.Result, "boom") {
		t.Errorf("result = %q, want it to contain %q", snap.Result, "boom")
	}
}
