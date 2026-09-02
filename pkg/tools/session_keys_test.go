// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/lele
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package tools

import (
	"context"
	"testing"
)

func TestSessionKeysRelated(t *testing.T) {
	related := [][2]string{
		// exact
		{"telegram:123", "telegram:123"},
		// channel-qualified vs bare (issue #230 native/WebUI case)
		{"native:550e8400-e29b-41d4-a716-446655440000", "550e8400-e29b-41d4-a716-446655440000"},
		// runtime agent key vs origin key
		{"agent:main:telegram:123", "telegram:123"},
		{"agent:main:telegram:123", "123"},
		// parent vs alias / child session
		{"telegram:123", "telegram:123:chat:2"},
		{"telegram:123", "telegram:123:subagent-1"},
	}
	for _, pair := range related {
		if !SessionKeysRelated(pair[0], pair[1]) {
			t.Errorf("expected related %q <-> %q", pair[0], pair[1])
		}
		if !SessionKeysRelated(pair[1], pair[0]) {
			t.Errorf("expected symmetric relation %q <-> %q", pair[1], pair[0])
		}
	}

	unrelated := [][2]string{
		// siblings must not leak
		{"telegram:123:chat:1", "telegram:123:chat:2"},
		{"native:uuid-a", "native:uuid-b"},
		// cross-channel same chat id must not leak
		{"native:uuid", "telegram:uuid"},
		// substring that is not segment-aligned
		{"agent:x:native:uuid", "ative:uuid"},
		// empty never matches
		{"", ""},
		{"", "telegram:123"},
		{"telegram:123", ""},
	}
	for _, pair := range unrelated {
		if SessionKeysRelated(pair[0], pair[1]) {
			t.Errorf("expected unrelated %q <-> %q", pair[0], pair[1])
		}
	}
}

func TestTaskBelongsToSession(t *testing.T) {
	tests := []struct {
		name      string
		spawner   string
		origin    string
		taskID    string
		keys      []string
		wantMatch bool
	}{
		{
			name:      "origin matches bare runtime key (issue #230)",
			origin:    "native:uuid-1",
			taskID:    "subagent-1",
			keys:      []string{"uuid-1"},
			wantMatch: true,
		},
		{
			name:      "spawner matches runtime key directly",
			spawner:   "agent:main:telegram:123",
			origin:    "telegram:123",
			taskID:    "subagent-1",
			keys:      []string{"agent:main:telegram:123"},
			wantMatch: true,
		},
		{
			name:      "child session key matches",
			origin:    "telegram:123",
			taskID:    "subagent-7",
			keys:      []string{"telegram:123:subagent-7"},
			wantMatch: true,
		},
		{
			name:      "other session does not match",
			spawner:   "agent:main:telegram:999",
			origin:    "telegram:999",
			taskID:    "subagent-1",
			keys:      []string{"telegram:123"},
			wantMatch: false,
		},
		{
			// Intentional: an origin key covers every alias of the session —
			// aliases share one history, so a stop must cancel all of them.
			name:      "origin covers sibling alias",
			origin:    "telegram:123",
			taskID:    "subagent-1",
			keys:      []string{"telegram:123:chat:2"},
			wantMatch: true,
		},
		{
			// With only per-alias spawner keys, sibling aliases stay isolated.
			name:      "sibling alias does not match on spawner alone",
			spawner:   "telegram:123:chat:1",
			taskID:    "subagent-1",
			keys:      []string{"telegram:123:chat:2"},
			wantMatch: false,
		},
		{
			name:      "empty spawner with no origin never matches",
			keys:      []string{"telegram:123"},
			wantMatch: false,
		},
		{
			name:      "empty key list matches nothing",
			origin:    "telegram:123",
			taskID:    "subagent-1",
			keys:      []string{},
			wantMatch: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := taskBelongsToSession(tc.spawner, tc.origin, tc.taskID, tc.keys)
			if got != tc.wantMatch {
				t.Errorf("taskBelongsToSession(%q, %q, %q, %v) = %v, want %v",
					tc.spawner, tc.origin, tc.taskID, tc.keys, got, tc.wantMatch)
			}
		})
	}
}

// TestSpawnWithOptionsCapturesSpawnerKey verifies the spawn-time attribution
// used for cancellation: the runtime session key from the agent tool context
// is recorded on the task (and stays empty without one).
func TestSpawnWithOptionsCapturesSpawnerKey(t *testing.T) {
	sm := NewSubagentManager(&recordingProvider{}, "test-model", "/tmp/test", nil, 10)

	ctx := WithAgentToolContext(context.Background(), "main", "agent:main:native:uuid-9")
	if _, err := sm.SpawnWithOptions(ctx, "task", "label", "", "native", "uuid-9", nil, SpawnOptions{}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	tasks := sm.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	task := tasks[0]
	if task.SpawnerSessionKey != "agent:main:native:uuid-9" {
		t.Errorf("SpawnerSessionKey = %q, want %q", task.SpawnerSessionKey, "agent:main:native:uuid-9")
	}
	if task.OriginSessionKey != "native:uuid-9" {
		t.Errorf("OriginSessionKey = %q, want %q", task.OriginSessionKey, "native:uuid-9")
	}

	// Spawn without agent tool context: spawner key stays empty, origin is
	// still recorded, and the task remains stoppable via the origin.
	if _, err := sm.SpawnWithOptions(context.Background(), "task 2", "label", "", "native", "uuid-9", nil, SpawnOptions{}); err != nil {
		t.Fatalf("plain spawn: %v", err)
	}
	var plain *SubagentTask
	for _, tk := range sm.ListTasks() {
		if tk.Task == "task 2" {
			plain = tk
		}
	}
	if plain == nil {
		t.Fatal("second task not found")
	}
	if plain.SpawnerSessionKey != "" {
		t.Errorf("SpawnerSessionKey = %q, want empty", plain.SpawnerSessionKey)
	}
	if !TaskBelongsToSession(plain, []string{"uuid-9"}) {
		t.Error("task without spawner key must still belong to its session via origin")
	}
}

// TestTaskOwnershipKey documents the ownership inheritance used by the
// subagent tool loop (nested spawns belong to the spawner's session).
func TestTaskOwnershipKey(t *testing.T) {
	if got := taskOwnershipKey(&SubagentTask{SpawnerSessionKey: "agent:main:native:u", OriginSessionKey: "native:u"}); got != "agent:main:native:u" {
		t.Errorf("taskOwnershipKey = %q, want spawner key", got)
	}
	if got := taskOwnershipKey(&SubagentTask{OriginSessionKey: "native:u"}); got != "native:u" {
		t.Errorf("taskOwnershipKey = %q, want origin fallback", got)
	}
	if got := taskOwnershipKey(nil); got != "" {
		t.Errorf("taskOwnershipKey(nil) = %q, want empty", got)
	}
}
