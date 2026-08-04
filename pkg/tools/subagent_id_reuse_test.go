package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestClaimTaskID_SkipsExistingSessionKeys verifies that claimTaskID never
// returns an ID whose session key already exists. This is the regression
// test for the ID-reuse bug: after a restart, nextID resets to 1, but
// persisted session files from previous runs remain. Without the existence
// probe, a new run would reuse "subagent-1" and merge into the old file.
func TestClaimTaskID_SkipsExistingSessionKeys(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 10)

	// Simulate persisted session files from a previous run: subagent-1 and
	// subagent-2 already exist on disk for this origin session.
	existing := map[string]bool{
		"cli:direct:subagent-1": true,
		"cli:direct:subagent-2": true,
	}
	var mu sync.Mutex
	sm.SetSessionExistsCallback(func(sessionKey string) bool {
		mu.Lock()
		defer mu.Unlock()
		return existing[sessionKey]
	})

	// First claim: nextID starts at 1, but subagent-1 and subagent-2 are
	// taken, so it must skip to subagent-3.
	got := sm.claimTaskID("cli:direct")
	if got != "subagent-3" {
		t.Fatalf("claimTaskID = %q, want %q (should skip existing subagent-1 and subagent-2)", got, "subagent-3")
	}

	// Next claim: subagent-3 was consumed (nextID advanced past it), and
	// subagent-4 is free.
	got = sm.claimTaskID("cli:direct")
	if got != "subagent-4" {
		t.Fatalf("second claimTaskID = %q, want %q", got, "subagent-4")
	}
}

// TestClaimTaskID_NoCallback verifies that without an existence callback the
// behavior is unchanged: sequential IDs starting at 1.
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

// TestSpawnWithOptions_SkipsExistingSessionKeys verifies the full spawn path
// skips IDs whose session keys already exist, and that the spawned task gets
// the non-colliding ID.
func TestSpawnWithOptions_SkipsExistingSessionKeys(t *testing.T) {
	provider := &scriptedSubagentProvider{}
	sm := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 10)

	// subagent-1 already exists on disk (left over from a previous run).
	sm.SetSessionExistsCallback(func(sessionKey string) bool {
		return sessionKey == "cli:direct:subagent-1"
	})

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

	// Give the spawned task a moment to register, then verify the task map.
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
