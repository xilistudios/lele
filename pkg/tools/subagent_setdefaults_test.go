// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package tools

import (
	"sync"
	"testing"
)

// TestSetDefaults_ReplacesConstructorFields verifies that SetDefaults updates
// the provider/model/workspace/iteration budget on an existing manager — the
// config-reload path re-uses managers so running tasks keep a home, and the
// manager must still pick up the recreated agent's new defaults.
func TestSetDefaults_ReplacesConstructorFields(t *testing.T) {
	sm := NewSubagentManager(nil, "old-model", "/old/workspace", nil, 5)

	first := &SubagentTask{ID: "subagent-1", Task: "long work", Status: SubagentStatusRunning}
	first.InitDoneChannel()
	sm.AddTaskForTest(first, func() {})

	sm.SetDefaults(nil, "new-model", "/new/workspace", 9)

	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sm.defaultModel != "new-model" {
		t.Errorf("defaultModel = %q, want %q", sm.defaultModel, "new-model")
	}
	if sm.workspace != "/new/workspace" {
		t.Errorf("workspace = %q, want %q", sm.workspace, "/new/workspace")
	}
	if sm.maxIterations != 9 {
		t.Errorf("maxIterations = %d, want 9", sm.maxIterations)
	}
	// The running task must survive the defaults swap untouched.
	if got := sm.tasks["subagent-1"]; got != first {
		t.Errorf("running task was replaced or dropped by SetDefaults")
	}
}

// TestSetDefaults_ConcurrentWithListTasks is a minimal data-race guard: the
// reload path writes defaults while the parent agent polls task status.
func TestSetDefaults_ConcurrentWithListTasks(t *testing.T) {
	sm := NewSubagentManager(nil, "m", "/w", nil, 5)
	task := &SubagentTask{ID: "subagent-1", Task: "work", Status: SubagentStatusRunning}
	task.InitDoneChannel()
	sm.AddTaskForTest(task, func() {})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); sm.SetDefaults(nil, "m2", "/w2", 7) }()
	go func() { defer wg.Done(); _ = sm.ListTasks() }()
	wg.Wait()
}
