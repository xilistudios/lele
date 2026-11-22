package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClaimTaskID_SkipsExistingSessionKeys(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 10)

	existing := map[string]bool{
		"cli:direct:subagent-1": true,
		"cli:direct:subagent-2": true,
	}
	var mu sync.Mutex
	sm.SetLedgerWiring(
		func([]string) []PersistedSubagentTask { return nil },
		func(sessionKey string) bool {
			mu.Lock()
			defer mu.Unlock()
			return existing[sessionKey]
		},
	)

	got := sm.claimTaskID("cli:direct")
	if got != "subagent-3" {
		t.Fatalf("claimTaskID = %q, want %q (should skip existing subagent-1 and subagent-2)", got, "subagent-3")
	}

	got = sm.claimTaskID("cli:direct")
	if got != "subagent-4" {
		t.Fatalf("second claimTaskID = %q, want %q", got, "subagent-4")
	}
}

func TestClaimTaskID_NoCallback(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 10)

	for i := 1; i <= 3; i++ {
		got := sm.claimTaskID("cli:direct")
		want := fmt.Sprintf("subagent-%d", i)
		if got != want {
			t.Fatalf("claimTaskID #%d = %q, want %q", i, got, want)
		}
	}
}

func TestSpawnWithOptions_SkipsExistingSessionKeys(t *testing.T) {
	provider := &scriptedSubagentProvider{}
	sm := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 10)

	sm.SetLedgerWiring(
		func([]string) []PersistedSubagentTask { return nil },
		func(sessionKey string) bool { return sessionKey == "cli:direct:subagent-1" },
	)

	msg, err := sm.SpawnWithOptions(context.Background(), "do a thing", "test", "", "cli", "direct", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("SpawnWithOptions failed: %v", err)
	}
	if !strings.Contains(msg, "subagent-2") {
		t.Fatalf("spawn message = %q, want it to reference subagent-2 (subagent-1 is taken)", msg)
	}
	if strings.Contains(msg, "subagent-1 ") || strings.HasSuffix(msg, "subagent-1") {
		t.Fatalf("spawn message references subagent-1, which already exists: %q", msg)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sm.mu.RLock()
		_, has2 := sm.tasks["subagent-2"]
		_, has1 := sm.tasks["subagent-1"]
		sm.mu.RUnlock()
		if has2 {
			if has1 {
				t.Fatal("subagent-1 was registered despite its session key already existing")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("subagent-2 never appeared in the task map")
}
