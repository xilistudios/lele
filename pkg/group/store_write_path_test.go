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

// ---------------------------------------------------------------------------
// e2e write-path persistence test (issue #239, layer 3).
//
// Production symptom: on a live daemon (build 8054806) the groups_state table
// had ZERO rows even though the SQLite store opened fine and
// gm.SetStore(dbStore.Groups()) is wired in pkg/agent/loop.go. Group cards
// therefore vanished on session switch / restart because there was nothing to
// rehydrate from.
//
// This test replicates the production wiring EXACTLY — store.Open →
// NewGroupManager → SetStoreDir → SetStore → SetEnabledHook → Start → Wait —
// and then verifies from a FRESH connection to the same db file that the final
// state really landed on disk. If this passes, the write path itself is sound
// and the bug must live in how production wires (or re-wires) the
// package-level store; if it fails, we have reproduced the bug.
// ---------------------------------------------------------------------------

// openSQLiteStoreAt opens (or creates) the SQLite store at a fixed path.
// Unlike openSQLiteStore (store_sqlite_test.go) it does not use t.TempDir,
// so a second, independent connection can be opened against the same file —
// exactly what we need to prove durability beyond the writer's pool.
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
	// The SQLite repo behind SaveGroup is a package-level global (UseStore);
	// reset it so we don't poison the rest of the package. Safe without
	// t.Parallel discipline here because no test in this package parallelizes.
	prevRepo := getGroupRepo()
	t.Cleanup(func() { UseStore(prevRepo) })

	dbPath := filepath.Join(t.TempDir(), "lele.db")
	s := openSQLiteStoreAt(t, dbPath)

	// --- production wiring, same order as pkg/agent/loop.go ---
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)
	gm.SetStoreDir(t.TempDir())                    // loop.go:490 — dir first...
	gm.SetStore(s.Groups())                        // loop.go:492 — ...then SQLite repo (sets the global)
	gm.SetEnabledHook(func() bool { return true }) // loop.go:489

	// --- run a group to completion ---
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

	// --- assert durability from a FRESH connection to the same db file ---
	// A separate store.Open gives its own *sql.DB pool; if the row is visible
	// here, it is committed on disk (WAL included), not just sitting in the
	// writer's connection.
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

	// The "started" save is hard to time from here, so instead we prove the
	// FINAL save happened AFTER finalize: finalize stamps state.UpdatedAt
	// under gm.mu before emitting the terminal pair, and Wait only returns
	// once that is done — so UpdatedAt must not be in the future relative to
	// the instant we observed completion, and the row we just read carries
	// that terminal timestamp (a row only holding the "started" snapshot
	// would have Status running and a stale UpdatedAt).
	if persisted.UpdatedAt.IsZero() {
		t.Error("persisted UpdatedAt is zero")
	}
	if persisted.UpdatedAt.After(finish.Add(time.Second)) {
		t.Errorf("persisted UpdatedAt = %v, after test finish %v — terminal save not consistent", persisted.UpdatedAt, finish)
	}

	// Cross-check through the domain read path too (ListGroups on the same
	// global repo the reader connection uses).
	all, err := reader.Groups().ListGroupStates()
	if err != nil {
		t.Fatalf("ListGroupStates: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("groups_state has %d rows, want exactly 1", len(all))
	}
}

// TestE2E_GroupWritePath_SQLiteNeedsNoLegacyDir pins the structural invariant
// that motivated issue #239's investigation: when a SQLite repository is
// configured, group state must persist even if the legacy per-file JSON
// directory was never set.
//
// Before the fix, both write sites gated persistence on storeDir != ""
// (Start's "started" save and saveStateBestEffort), while the SQLite backend
// in SaveGroup ignores dir entirely. A manager wired with SetStore(repo) but
// without SetStoreDir therefore persisted NOTHING — a silent data-loss trap
// for any caller that drops the obsolete JSON dir. loop.go happens to set
// both today, which is why production never hit it.
func TestE2E_GroupWritePath_SQLiteNeedsNoLegacyDir(t *testing.T) {
	prevRepo := getGroupRepo()
	t.Cleanup(func() { UseStore(prevRepo) })

	dbPath := filepath.Join(t.TempDir(), "lele.db")
	s := openSQLiteStoreAt(t, dbPath)

	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)
	// Deliberately NO SetStoreDir: only the SQLite backend is configured.
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

// TestE2E_GroupWritePath_DurableBeforeTerminalSignal pins the ordering
// invariant that makes group cards survive a client re-read or a process
// restart: the terminal state must already be on disk by the time the client
// is told the group finished (terminal group.status / group.complete, and by
// extension the close of done that Wait() unblocks).
//
// Before the fix, finalize published the terminal pair and closed done, and
// only afterwards did runGroup's deferred saveStateBestEffort write the row.
// A client that refreshed history as soon as it saw group.complete — exactly
// what the WebUI does on session switch — could therefore read a store with no
// row at all (issue #239's "cards vanish"), and a restart in that window lost
// the group permanently.
//
// The check is deterministic: the assertion runs inside the publisher, on the
// same goroutine that emits the terminal events, so it observes the store at
// the exact instant the signal is delivered.
func TestE2E_GroupWritePath_DurableBeforeTerminalSignal(t *testing.T) {
	prevRepo := getGroupRepo()
	t.Cleanup(func() { UseStore(prevRepo) })

	dbPath := filepath.Join(t.TempDir(), "lele.db")
	s := openSQLiteStoreAt(t, dbPath)

	reader := openSQLiteStoreAt(t, dbPath)

	const groupID = "group:order"

	// terminalSeen records, for each terminal event delivered, whether the
	// durable row was already readable at that moment.
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

		// Observe the store exactly as a client re-reading history would.
		// The row must carry the TERMINAL status, not merely exist: Start
		// already persisted a "running" snapshot, so an existence check alone
		// would pass even with the final save still pending.
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
		// Requirement: the durable row must already be terminal, and — when the
		// event names the status (group.status does; group.complete does not) —
		// it must be the SAME status being announced.
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
