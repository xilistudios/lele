package group

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

type contentionExecutor struct {
	gm      *GroupManager
	entered chan struct{}
	release chan struct{}
}

func (e *contentionExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	close(e.entered)
	<-e.release
	if e.gm != nil {
		e.gm.Status(req.GroupID)
	}
	return "ok", 10, nil
}

func TestDrainAllRunGoroutines_TerminalStateSurvivesStoreClose(t *testing.T) {
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

	time.Sleep(50 * time.Millisecond)

	gm.StopAll()

	drained := gm.DrainAllRunGoroutines(5 * time.Second)
	if !drained {
		t.Fatal("DrainAllRunGoroutines returned false within 5 s grace")
	}

	if err := s.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

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

func TestDrainAllRunGoroutines_GraceExpired(t *testing.T) {
	ce := &contentionExecutor{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, ce.execute, pub.publish)

	participants := []Participant{plainParticipant("a"), plainParticipant("b")}
	if _, err := gm.Start(context.Background(), "grace-1", "", "task", "round_robin",
		participants, GroupOptions{Rounds: 100}, "ch", "chat"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	<-ce.entered

	gm.StopAll()

	start := time.Now()
	drained := gm.DrainAllRunGoroutines(100 * time.Millisecond)
	elapsed := time.Since(start)

	if drained {
		t.Fatal("DrainAllRunGoroutines returned true; expected false (grace expired)")
	}
	if elapsed > 2*time.Second {
		t.Errorf("DrainAllRunGoroutines took %v; expected ~100ms grace", elapsed)
	}

	close(ce.release)
	_ = gm.DrainAllRunGoroutines(5 * time.Second)
}

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

	ce.gm = gm

	gm.StopAll()

	drained := make(chan bool, 1)
	go func() {
		drained <- gm.DrainAllRunGoroutines(10 * time.Second)
	}()

	time.Sleep(50 * time.Millisecond)

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
