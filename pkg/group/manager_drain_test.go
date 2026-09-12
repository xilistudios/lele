package group

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

// contentionExecutor is a test executor that blocks until release is closed,
// then optionally calls gm.Status() to contend for gm.mu. It deliberately
// ignores context cancellation so the test can control when the run goroutine
// proceeds through explicit synchronization.
type contentionExecutor struct {
	gm      *GroupManager // if non-nil, Status() is called after release to contend for gm.mu
	entered chan struct{} // closed when the executor begins
	release chan struct{} // closed by the test to let the executor proceed
}

func (e *contentionExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	close(e.entered)
	<-e.release
	if e.gm != nil {
		// Call Status() to acquire gm.mu. If DrainAllRunGoroutines holds
		// gm.mu during runWg.Wait(), this call blocks and deadlocks.
		e.gm.Status(req.GroupID)
	}
	return "ok", 10, nil
}

// ---------------------------------------------------------------------------
// Test 1 — Regression: terminal state survives store close (FAILS without fix)
// ---------------------------------------------------------------------------

// TestDrainAllRunGoroutines_TerminalStateSurvivesStoreClose is the core
// regression test for CORE-04. It proves that draining group run goroutines
// BEFORE closing the SQLite store preserves the terminal group state on disk.
//
// Without DrainAllRunGoroutines, the deferred saveStateBestEffort in runGroup
// executes against a closed DB and the terminal state is silently lost.
func TestDrainAllRunGoroutines_TerminalStateSurvivesStoreClose(t *testing.T) {
	// --- SQLite wiring (same pattern as store_write_path_test.go) ---
	prevRepo := getGroupRepo()
	t.Cleanup(func() { UseStore(prevRepo) })

	dbPath := filepath.Join(t.TempDir(), "lele.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	be := &blockingExecutor{unblockCh: make(chan struct{})}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, be.execute, pub.publish)
	gm.SetStore(s.Groups())

	ctx := context.Background()
	const groupID = "drain-regression"
	participants := []Participant{
		plainParticipant("a"),
		plainParticipant("b"),
	}
	if _, err := gm.Start(ctx, groupID, "", "task", "round_robin",
		participants, GroupOptions{Rounds: 100}, "ch", "chat"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Let the run goroutine enter the blocking executor.
	time.Sleep(50 * time.Millisecond)

	// --- Shutdown sequence (with fix) ---
	gm.StopAll()

	drained := gm.DrainAllRunGoroutines(5 * time.Second)
	if !drained {
		t.Fatal("DrainAllRunGoroutines returned false within 5 s grace")
	}

	// Close the store — exactly what StopWithin does after draining.
	if err := s.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// --- Verify from a FRESH connection that the terminal state landed ---
	reader, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open (reader): %v", err)
	}
	t.Cleanup(func() { reader.Close() })

	stateJSON, found, err := reader.Groups().GetGroupState(groupID)
	if err != nil {
		t.Fatalf("GetGroupState(%q): %v", groupID, err)
	}
	if !found {
		t.Fatalf("no row for %q: terminal state was lost — DrainAllRunGoroutines is not working", groupID)
	}

	var persisted GroupState
	if err := json.Unmarshal([]byte(stateJSON), &persisted); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !isTerminalGroupStatus(persisted.Status) {
		t.Errorf("persisted Status = %q, want a terminal status (done/stopped/error)", persisted.Status)
	}
}

// ---------------------------------------------------------------------------
// Test 2 — Grace expired: drain returns without hanging
// ---------------------------------------------------------------------------

// TestDrainAllRunGoroutines_GraceExpired verifies that when a run goroutine
// refuses to exit (ignores cancellation), the drain gives up after the grace
// window and returns false instead of hanging the shutdown forever.
func TestDrainAllRunGoroutines_GraceExpired(t *testing.T) {
	ce := &contentionExecutor{
		entered: make(chan struct{}),
		release: make(chan struct{}), // never closed — executor blocks forever
	}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, ce.execute, pub.publish)

	participants := []Participant{plainParticipant("a"), plainParticipant("b")}
	if _, err := gm.Start(context.Background(), "grace-1", "", "task", "round_robin",
		participants, GroupOptions{Rounds: 100}, "ch", "chat"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	<-ce.entered // wait for the executor to enter

	gm.StopAll() // cancel context — executor ignores it

	start := time.Now()
	drained := gm.DrainAllRunGoroutines(100 * time.Millisecond)
	elapsed := time.Since(start)

	if drained {
		t.Fatal("DrainAllRunGoroutines returned true; expected false (grace expired)")
	}
	if elapsed > 2*time.Second {
		t.Errorf("DrainAllRunGoroutines took %v; expected ~100ms grace", elapsed)
	}

	// Clean up: release the stuck executor so the goroutine can exit.
	close(ce.release)
	_ = gm.DrainAllRunGoroutines(5 * time.Second)
}

// ---------------------------------------------------------------------------
// Test 3 — No deadlock: drain waits OUTSIDE gm.mu
// ---------------------------------------------------------------------------

// TestDrainAllRunGoroutines_NoDeadlock proves that DrainAllRunGoroutines does
// NOT hold gm.mu while waiting on runWg.Wait(). If it did, a run goroutine
// that calls gm.Status() (which acquires gm.mu) would deadlock.
//
// The test starts a group whose executor blocks, then releases it so it calls
// Status() while DrainAllRunGoroutines is waiting. If the drain holds the
// lock, Status() blocks on gm.mu, runWg.Done() never fires, and the test
// hangs — failing by timeout.
func TestDrainAllRunGoroutines_NoDeadlock(t *testing.T) {
	ce := &contentionExecutor{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, ce.execute, pub.publish)

	participants := []Participant{plainParticipant("a"), plainParticipant("b")}
	const groupID = "deadlock-1"
	if _, err := gm.Start(context.Background(), groupID, "", "task", "round_robin",
		participants, GroupOptions{Rounds: 100}, "ch", "chat"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	<-ce.entered

	// Set gm AFTER Start so the executor's Status() call contends for the
	// same lock DrainAllRunGoroutines might be holding.
	ce.gm = gm

	gm.StopAll()

	// Run drain in a separate goroutine so we can release the executor
	// while it's waiting.
	drained := make(chan bool, 1)
	go func() {
		drained <- gm.DrainAllRunGoroutines(10 * time.Second)
	}()

	// Give DrainAllRunGoroutines time to enter runWg.Wait().
	time.Sleep(50 * time.Millisecond)

	// Release the executor. It will call gm.Status() which acquires gm.mu.
	// If DrainAllRunGoroutines holds gm.mu during the wait, Status() blocks,
	// runWg.Done() never fires, and we deadlock.
	close(ce.release)

	select {
	case ok := <-drained:
		if !ok {
			t.Error("DrainAllRunGoroutines returned false")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock detected: DrainAllRunGoroutines held gm.mu while waiting on runWg")
	}
}
