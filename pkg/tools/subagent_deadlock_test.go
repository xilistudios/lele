package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestR1_PanicWhileHoldingMu_DoesNotDeadlock(t *testing.T) {
	manager := NewSubagentManager(&flakySubagentProvider{failAt: 0, final: "ok"}, "test-model", t.TempDir(), nil, 10)

	dep := &SubagentTask{
		ID:               "dep-stuck",
		Task:             "stuck dependency",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-r1",
		OriginSessionKey: "native:chat-r1",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	dep.InitDoneChannel()
	manager.AddTaskForTest(dep, nil)

	msg, err := manager.SpawnWithOptions(
		context.Background(),
		"r1 deadlock test",
		"r1-test",
		"agent",
		"native",
		"chat-r1",
		nil,
		SpawnOptions{Dependencies: []string{"dep-stuck"}},
	)
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}

	var spawnedID string
	for _, part := range strings.Fields(msg) {
		if strings.HasPrefix(part, "subagent-") {
			spawnedID = part
			break
		}
	}
	if spawnedID == "" {
		t.Fatalf("could not extract task ID from %q", msg)
	}

	rawTask := manager.tasks[spawnedID]
	if rawTask == nil {
		t.Fatalf("task %q not in manager.tasks", spawnedID)
	}

	manager.mu.Lock()
	manager.tasks["dep-stuck"] = nil
	manager.mu.Unlock()

	select {
	case <-rawTask.DoneChannel():
	case <-time.After(10 * time.Second):
		t.Fatal("SignalDone never called — safeGoroutine recover deadlocked on sm.mu.Lock() (R-1 bug)")
	}

	acquired := make(chan struct{})
	go func() {
		manager.mu.RLock()
		manager.mu.RUnlock()
		close(acquired)
	}()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("sm.mu appears permanently locked")
	}

	snap, ok := manager.GetTask(spawnedID)
	if !ok {
		t.Fatalf("task %q not found", spawnedID)
	}
	if snap.Status != SubagentStatusFailed {
		t.Errorf("status = %q, want %q", snap.Status, SubagentStatusFailed)
	}
}

func TestR1_TaskCtxDoneWhilePending_DoesNotDeadlock(t *testing.T) {
	manager := NewSubagentManager(&flakySubagentProvider{failAt: 0, final: "ok"}, "test-model", t.TempDir(), nil, 10)

	neverDone := &SubagentTask{
		ID:               "dep-cancel-never",
		Task:             "never finishes",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-r1-cancel",
		OriginSessionKey: "native:chat-r1-cancel",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	neverDone.InitDoneChannel()
	manager.AddTaskForTest(neverDone, nil)

	msg, err := manager.SpawnWithOptions(
		context.Background(),
		"cancel-path test",
		"r1-cancel",
		"agent",
		"native",
		"chat-r1-cancel",
		nil,
		SpawnOptions{Dependencies: []string{"dep-cancel-never"}},
	)
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}

	var spawnedID string
	for _, part := range strings.Fields(msg) {
		if strings.HasPrefix(part, "subagent-") {
			spawnedID = part
			break
		}
	}

	rawTask := manager.tasks[spawnedID]
	if rawTask == nil {
		t.Fatalf("task %q not found", spawnedID)
	}

	manager.StopTask(spawnedID)

	select {
	case <-rawTask.DoneChannel():
	case <-time.After(10 * time.Second):
		t.Fatal("SignalDone not called after cancel")
	}

	snap, _ := manager.GetTask(spawnedID)
	if snap.Status != SubagentStatusCancelled {
		t.Errorf("status = %q, want %q", snap.Status, SubagentStatusCancelled)
	}

	done := make(chan struct{})
	go func() {
		manager.ListTasks()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sm.mu permanently locked after cancel-path test")
	}
}

func TestR1_MutexUsableAfterPanicRecovery(t *testing.T) {
	manager := NewSubagentManager(&panicProvider{}, "test-model", t.TempDir(), nil, 10)

	lockDep := &SubagentTask{
		ID:               "dep-stress-lock",
		Task:             "hold the gate",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-stress",
		OriginSessionKey: "native:chat-stress",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	lockDep.InitDoneChannel()
	manager.AddTaskForTest(lockDep, nil)

	const N = 3
	spawnedIDs := make([]string, N)
	rawTasks := make([]*SubagentTask, N)
	for i := 0; i < N; i++ {
		msg, err := manager.SpawnWithOptions(
			context.Background(),
			fmt.Sprintf("stress task %d", i),
			"stress",
			"agent",
			"native",
			"chat-stress",
			nil,
			SpawnOptions{Dependencies: []string{"dep-stress-lock"}},
		)
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		for _, p := range strings.Fields(msg) {
			if strings.HasPrefix(p, "subagent-") {
				spawnedIDs[i] = p
			}
		}
		rawTasks[i] = manager.tasks[spawnedIDs[i]]
	}

	lockDep.mu.Lock()
	lockDep.Status = SubagentStatusCompleted
	lockDep.mu.Unlock()
	lockDep.SignalDone()

	for i, raw := range rawTasks {
		select {
		case <-raw.DoneChannel():
		case <-time.After(15 * time.Second):
			t.Fatalf("task %d: SignalDone not called", i)
		}
		final, _ := manager.GetTask(spawnedIDs[i])
		if final.Status != SubagentStatusFailed {
			t.Errorf("task %d status = %q, want %q", i, final.Status, SubagentStatusFailed)
		}
	}

	done := make(chan struct{})
	go func() {
		manager.ListTasks()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sm.mu permanently locked after stress test")
	}
}
