package tools

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestStopAll_DoesNotHoldMuDuringCancel(t *testing.T) {
	manager := NewSubagentManager(&flakySubagentProvider{failAt: 0, final: "ok"}, "test-model", t.TempDir(), nil, 10)

	release := make(chan struct{})
	blockingCancel := func() { <-release }

	task := &SubagentTask{
		ID:               "stopall-lock-task",
		Task:             "lock hygiene probe",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-lock",
		OriginSessionKey: "native:chat-lock",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()
	manager.AddTaskForTest(task, blockingCancel)

	stopAllDone := make(chan int, 1)
	go func() { stopAllDone <- manager.StopAll() }()

	lockAcquired := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !lockAcquired {
		time.Sleep(10 * time.Millisecond)
		if manager.mu.TryLock() {
			manager.mu.Unlock()
			lockAcquired = true
		}
	}
	close(release)

	if !lockAcquired {
		t.Fatal("sm.mu stayed locked while StopAll invoked cancel funcs — cancel() runs under the lock")
	}

	select {
	case n := <-stopAllDone:
		if n != 1 {
			t.Fatalf("StopAll stopped %d tasks, want 1", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("StopAll did not return after cancels were released")
	}

	if task.Status != SubagentStatusCancelled {
		t.Fatalf("task status = %v, want cancelled", task.Status)
	}
}

func TestStopAll_CancelsContextTask(t *testing.T) {
	manager := NewSubagentManager(&flakySubagentProvider{failAt: 0, final: "ok"}, "test-model", t.TempDir(), nil, 10)

	ctx, cancel := context.WithCancel(context.Background())
	task := &SubagentTask{
		ID:               "stopall-ctx-task",
		Task:             "plain ctx task",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-ctx",
		OriginSessionKey: "native:chat-ctx",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()
	manager.AddTaskForTest(task, cancel)

	if n := manager.StopAll(); n != 1 {
		t.Fatalf("StopAll stopped %d tasks, want 1", n)
	}

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("context was not cancelled by StopAll")
	}
}
