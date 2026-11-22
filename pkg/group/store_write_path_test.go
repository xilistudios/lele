package group

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/store"
)

func openSQLiteStoreAt(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open(%q) failed: %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store.Close() failed: %v", err)
		}
	})
	return s
}

func TestE2E_GroupWritePath_PersistsToSQLite(t *testing.T) {
	prevRepo := getGroupRepo()
	t.Cleanup(func() { UseStore(prevRepo) })

	dbPath := filepath.Join(t.TempDir(), "lele.db")
	s := openSQLiteStoreAt(t, dbPath)

	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)
	gm.SetStoreDir(t.TempDir())
	gm.SetStore(s.Groups())
	gm.SetEnabledHook(func() bool { return true })

	ctx := context.Background()
	const groupID = "group:wtest"
	if _, err := gm.Start(ctx, groupID, "", "task", "round_robin",
		[]Participant{
			{AgentID: "a", Role: RoleProposer, Label: "A"},
			{AgentID: "b", Role: RoleAggregator, Label: "B"},
		},
		GroupOptions{Rounds: 1, MaxTurns: 2}, "native", "chat-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	finish := time.Now()

	reader := openSQLiteStoreAt(t, dbPath)

	stateJSON, found, err := reader.Groups().GetGroupState(groupID)
	if err != nil {
		t.Fatalf("GetGroupState(%q): %v", groupID, err)
	}
	if !found {
		t.Fatalf("no row in groups_state for %q: production write path is broken (issue #239 reproduced)", groupID)
	}

	var persisted GroupState
	if err := json.Unmarshal([]byte(stateJSON), &persisted); err != nil {
		t.Fatalf("unmarshal persisted state: %v", err)
	}

	if persisted.Status != StatusDone {
		t.Errorf("persisted Status = %q, want %q", persisted.Status, StatusDone)
	}
	if len(persisted.Transcript) < 1 {
		t.Errorf("persisted Transcript has %d turns, want >= 1", len(persisted.Transcript))
	}

	if persisted.UpdatedAt.IsZero() {
		t.Error("persisted UpdatedAt is zero")
	}
	if persisted.UpdatedAt.After(finish.Add(time.Second)) {
		t.Errorf("persisted UpdatedAt = %v, after test finish %v — terminal save not consistent", persisted.UpdatedAt, finish)
	}

	all, err := reader.Groups().ListGroupStates()
	if err != nil {
		t.Fatalf("ListGroupStates: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("groups_state has %d rows, want exactly 1", len(all))
	}
}

func TestE2E_GroupWritePath_SQLiteNeedsNoLegacyDir(t *testing.T) {
	prevRepo := getGroupRepo()
	t.Cleanup(func() { UseStore(prevRepo) })

	dbPath := filepath.Join(t.TempDir(), "lele.db")
	s := openSQLiteStoreAt(t, dbPath)

	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)
	gm.SetStore(s.Groups())
	gm.SetEnabledHook(func() bool { return true })

	ctx := context.Background()
	const groupID = "group:nodir"
	if _, err := gm.Start(ctx, groupID, "", "task", "round_robin",
		[]Participant{
			{AgentID: "a", Role: RoleProposer, Label: "A"},
			{AgentID: "b", Role: RoleAggregator, Label: "B"},
		},
		GroupOptions{Rounds: 1, MaxTurns: 2}, "native", "chat-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	reader := openSQLiteStoreAt(t, dbPath)
	stateJSON, found, err := reader.Groups().GetGroupState(groupID)
	if err != nil {
		t.Fatalf("GetGroupState(%q): %v", groupID, err)
	}
	if !found {
		t.Fatalf("SQLite-backed group was not persisted because storeDir is empty: "+
			"persistence must key off the configured backend, not the legacy dir (%q)", groupID)
	}

	var persisted GroupState
	if err := json.Unmarshal([]byte(stateJSON), &persisted); err != nil {
		t.Fatalf("unmarshal persisted state: %v", err)
	}
	if persisted.Status != StatusDone {
		t.Errorf("persisted Status = %q, want %q", persisted.Status, StatusDone)
	}
	if len(persisted.Transcript) < 1 {
		t.Errorf("persisted Transcript has %d turns, want >= 1", len(persisted.Transcript))
	}
}

func TestE2E_GroupWritePath_DurableBeforeTerminalSignal(t *testing.T) {
	prevRepo := getGroupRepo()
	t.Cleanup(func() { UseStore(prevRepo) })

	dbPath := filepath.Join(t.TempDir(), "lele.db")
	s := openSQLiteStoreAt(t, dbPath)

	reader := openSQLiteStoreAt(t, dbPath)

	const groupID = "group:order"

	type observation struct {
		event  string
		status string
	}
	var (
		obsMu        sync.Mutex
		observations []observation
		missing      []string
	)

	publish := func(msg bus.OutboundMessage) {
		if msg.Event != "group.status" && msg.Event != "group.complete" {
			return
		}
		status := msg.Metadata["status"]
		terminal := msg.Event == "group.complete" ||
			status == StatusDone || status == StatusStopped || status == StatusError
		if !terminal {
			return
		}

		obsMu.Lock()
		observations = append(observations, observation{event: msg.Event, status: status})
		obsMu.Unlock()

		stateJSON, found, err := reader.Groups().GetGroupState(groupID)
		if err != nil {
			t.Errorf("GetGroupState during %s: %v", msg.Event, err)
			return
		}
		persistedStatus := "<no row>"
		if found {
			var st GroupState
			if err := json.Unmarshal([]byte(stateJSON), &st); err != nil {
				t.Errorf("unmarshal persisted state during %s: %v", msg.Event, err)
				return
			}
			persistedStatus = st.Status
		}
		ok := isTerminalGroupStatus(persistedStatus) &&
			(status == "" || persistedStatus == status)
		if !ok {
			obsMu.Lock()
			missing = append(missing, fmt.Sprintf("%s(durable=%q, signalled=%q)",
				msg.Event, persistedStatus, status))
			obsMu.Unlock()
		}
	}

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, publish)
	gm.SetStoreDir(t.TempDir())
	gm.SetStore(s.Groups())
	gm.SetEnabledHook(func() bool { return true })

	ctx := context.Background()
	if _, err := gm.Start(ctx, groupID, "", "task", "round_robin",
		[]Participant{
			{AgentID: "a", Role: RoleProposer, Label: "A"},
			{AgentID: "b", Role: RoleAggregator, Label: "B"},
		},
		GroupOptions{Rounds: 1, MaxTurns: 2}, "native", "chat-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	obsMu.Lock()
	nObs := len(observations)
	obsMu.Unlock()
	if nObs == 0 {
		t.Fatal("no terminal events observed — test is not exercising the invariant")
	}
	obsMu.Lock()
	missingEvents := append([]string(nil), missing...)
	obsMu.Unlock()
	if len(missingEvents) > 0 {
		t.Errorf("row for %q was NOT durable when these terminal signals were delivered: %v — "+
			"persistence must happen before the terminal signal, not after", groupID, missingEvents)
	}
}
