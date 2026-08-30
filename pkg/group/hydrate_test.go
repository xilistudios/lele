package group

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Regression tests for B7: groups persisted on disk must be rehydrated at
// startup.
//
// GroupManager saved state on disk (Start persists the started snapshot and
// runGroup persists the final one through the deferred saveStateBestEffort),
// but after a process restart nothing ever read it back: the manager came up
// empty and "/group list" plus the WebSocket welcome payload lost every
// finished group, while chat sessions survived the restart.
//
// The contract under test (GroupManager.LoadHistorical):
//   - finished groups come back visible to List/Status/AllSnapshots and Wait
//     returns immediately with a synthesis recomputed from the transcript;
//   - a non-terminal status on disk (the process died mid-run) is re-marked
//     StatusError with a synthetic "terminated by process restart" error, so
//     Wait fails fast instead of hanging forever;
//   - hydration is idempotent and never overwrites an already-tracked ID;
//   - finishedAt is anchored to state.UpdatedAt so the B6 retention sweep
//     drops week-old history on the first read;
//   - with no storeDir configured it is a no-op.
//
// These tests exercise the legacy JSON dir backend, so each one pins
// UseStore(nil) for its duration (the SQLite repo is package-global).
// ---------------------------------------------------------------------------

// useJSONStore pins the package-level store to the legacy JSON backend and
// restores whatever was configured when the test ends.
func useJSONStore(t *testing.T) {
	t.Helper()
	prev := getGroupRepo()
	UseStore(nil)
	t.Cleanup(func() { UseStore(prev) })
}

// withJSONStoreDir returns a manager wired to the mock executor, pinned to the
// legacy JSON backend and persisting into dir.
func withJSONStoreDir(t *testing.T, dir string, pub Publisher) *GroupManager {
	t.Helper()
	useJSONStore(t)
	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, pub)
	gm.SetStoreDir(dir)
	return gm
}

// tryGroupFinalized polls dir until the terminal state of groupID lands there
// (or the timeout elapses), returning nil on timeout. runGroup flushes with a
// deferred saveStateBestEffort that runs *after* finalize closed done, so a
// test that only waited on Wait can still race the write.
func tryGroupFinalized(dir, groupID string, timeout time.Duration) *GroupState {
	deadline := time.Now().Add(timeout)
	for {
		if st, err := LoadGroup(dir, groupID); err == nil && isTerminalGroupStatus(st.Status) {
			return st
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// assertGroupFinalized is tryGroupFinalized with a 5s budget and a hard failure.
func assertGroupFinalized(t *testing.T, dir, groupID string) *GroupState {
	t.Helper()
	st := tryGroupFinalized(dir, groupID, 5*time.Second)
	if st == nil {
		t.Fatalf("group %s: no terminal state persisted under %s after 5s", groupID, dir)
	}
	return st
}

// drainGroup releases a group parked by stepExecutor and waits for its terminal
// flush to reach dir. Tests that start a live group against a t.TempDir() must
// call it (deferred) so the async save cannot race the directory cleanup.
func drainGroup(t *testing.T, exec *stepExecutor, dir, groupID string) {
	t.Helper()
	releaseStep(exec)
	if tryGroupFinalized(dir, groupID, 5*time.Second) == nil {
		t.Errorf("group %s: live run never flushed a terminal state to %s", groupID, dir)
	}
}

// findState returns the snapshot with the given ID from a List() result.
func findState(states []*GroupState, id string) (*GroupState, bool) {
	for _, s := range states {
		if s.ID == id {
			return s, true
		}
	}
	return nil, false
}

// TestRegression_LoadHistoricalRestoresFinishedGroups runs a real group to
// completion with persistence enabled, then hydrates a brand-new manager over
// the same directory — the restart path.
func TestRegression_LoadHistoricalRestoresFinishedGroups(t *testing.T) {
	dir := t.TempDir()
	writer := withJSONStoreDir(t, dir, newLifecycleRecorder().publish)

	const groupID = "group:hydrate-done"
	if _, err := writer.Start(context.Background(), groupID, "p1", "solve it", "round_robin",
		[]Participant{plainParticipant("a"), plainParticipant("b")},
		GroupOptions{Rounds: 1}, "web", "chat-7"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	want, err := writer.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	// The state finalize produced must be what lands on disk: B7 depends on it,
	// because without a persisted terminal status there is nothing to hydrate.
	// runner.go registers saveStateBestEffort as the outermost defer, so the
	// terminal status set by finalize is always the one saved.
	finalized := assertGroupFinalized(t, dir, groupID)
	if finalized.Status != StatusDone {
		t.Fatalf("persisted status = %q, want %q", finalized.Status, StatusDone)
	}

	// The restart: a fresh manager over the same storeDir.
	rec := &mockPublisher{}
	reader := NewGroupManager(mockResolve, (&mockExecutor{}).execute, rec.publish)
	reader.SetStoreDir(dir)
	n, err := reader.LoadHistorical()
	if err != nil {
		t.Fatalf("LoadHistorical: %v", err)
	}
	if n != 1 {
		t.Fatalf("LoadHistorical count = %d, want 1", n)
	}

	st, ok := reader.Status(groupID)
	if !ok {
		t.Fatal("hydrated group missing from Status")
	}
	if st.Status != StatusDone {
		t.Errorf("status = %q, want %q", st.Status, StatusDone)
	}
	if len(st.Transcript) != 2 {
		t.Errorf("transcript len = %d, want 2", len(st.Transcript))
	}
	if st.OriginChatID != "chat-7" {
		t.Errorf("OriginChatID = %q, want %q", st.OriginChatID, "chat-7")
	}

	// Wait must return immediately (done already closed) with the synthesis
	// recovered from the persisted transcript — the value the live group gave.
	var got string
	waitWithTimeout(t, 3*time.Second, "Wait on hydrated group", func() {
		got, err = reader.Wait(groupID)
	})
	if err != nil {
		t.Fatalf("Wait on hydrated group: %v", err)
	}
	if got != want {
		t.Errorf("synthesis = %q, want %q", got, want)
	}

	// List and AllSnapshots see it too.
	if states := reader.List(); len(states) != 1 {
		t.Fatalf("List = %d groups, want 1", len(states))
	}
	snaps := reader.AllSnapshots()
	if len(snaps) != 1 {
		t.Fatalf("AllSnapshots = %d, want 1", len(snaps))
	}
	if snaps[0].Synthesis != want {
		t.Errorf("snapshot synthesis = %q, want %q", snaps[0].Synthesis, want)
	}
	if snaps[0].OriginChatID != "chat-7" || snaps[0].OriginChannel != "web" {
		t.Errorf("snapshot origin = %q/%q, want web/chat-7",
			snaps[0].OriginChannel, snaps[0].OriginChatID)
	}
	if len(snaps[0].Turns) != 2 {
		t.Errorf("snapshot turns = %d, want 2", len(snaps[0].Turns))
	}

	// Hydration published nothing: no replayed started or terminal events.
	if c := rec.count(); c != 0 {
		t.Errorf("publisher got %d events during hydration, want 0", c)
	}
}

// TestRegression_LoadHistoricalRemarksRunningGroup pins the mid-run-crash case:
// a state persisted with a non-terminal status must come back as error, not as
// a group that looks alive forever.
func TestRegression_LoadHistoricalRemarksRunningGroup(t *testing.T) {
	dir := t.TempDir()
	useJSONStore(t)

	orphan := &GroupState{
		ID:           "group:hydrate-running",
		ProfileID:    "p1",
		Task:         "never finished",
		Participants: []Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}},
		Strategy:     "round_robin",
		Status:       StatusRunning,
		CreatedAt:    time.Now().Add(-time.Minute),
		UpdatedAt:    time.Now().Add(-time.Minute),
		Rounds:       1,
		MaxTurns:     5,
		Transcript:   []Turn{{Index: 0, Speaker: "a", Label: "A", Content: "partial", Tokens: 5}},
	}
	if err := SaveGroup(dir, orphan); err != nil {
		t.Fatalf("SaveGroup: %v", err)
	}

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, newLifecycleRecorder().publish)
	gm.SetStoreDir(dir)
	n, err := gm.LoadHistorical()
	if err != nil {
		t.Fatalf("LoadHistorical: %v", err)
	}
	if n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}

	st, ok := gm.Status(orphan.ID)
	if !ok {
		t.Fatal("re-marked group missing from Status")
	}
	if st.Status != StatusError {
		t.Errorf("status = %q, want %q", st.Status, StatusError)
	}

	var got string
	var werr error
	waitWithTimeout(t, 3*time.Second, "Wait on re-marked group", func() {
		got, werr = gm.Wait(orphan.ID)
	})
	if werr == nil {
		t.Fatal("Wait returned nil error, want synthetic restart error")
	}
	if !strings.Contains(werr.Error(), "terminated by process restart") {
		t.Errorf("Wait error = %v, want it to mention process restart", werr)
	}
	// The partial transcript is still surfaced: its last turn stands in for a
	// synthesis nobody ever produced.
	if got != "partial" {
		t.Errorf("result = %q, want %q", got, "partial")
	}
}

// TestRegression_LoadHistoricalIdempotent: calling it twice — or hydrating
// while an active run holds the same ID — must not duplicate or clobber.
func TestRegression_LoadHistoricalIdempotent(t *testing.T) {
	dir := t.TempDir()
	useJSONStore(t)

	for i := 0; i < 3; i++ {
		st := &GroupState{
			ID:           fmt.Sprintf("group:hydrate-idem-%d", i),
			Participants: []Participant{{AgentID: "a", Role: RoleProposer}},
			Strategy:     "round_robin",
			Status:       StatusDone,
			// Minutes (not hours) apart so the default retention window keeps all
			// three visible while the counts are asserted.
			CreatedAt:  time.Now().Add(-time.Duration(i) * time.Minute),
			UpdatedAt:  time.Now().Add(-time.Duration(i) * time.Minute),
			Transcript: []Turn{{Index: 0, Speaker: "a", Content: fmt.Sprintf("s%d", i)}},
		}
		if err := SaveGroup(dir, st); err != nil {
			t.Fatalf("SaveGroup %d: %v", i, err)
		}
	}

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, newLifecycleRecorder().publish)
	gm.SetStoreDir(dir)

	first, err := gm.LoadHistorical()
	if err != nil {
		t.Fatalf("first LoadHistorical: %v", err)
	}
	if first != 3 {
		t.Fatalf("first count = %d, want 3", first)
	}
	second, err := gm.LoadHistorical()
	if err != nil {
		t.Fatalf("second LoadHistorical: %v", err)
	}
	if second != 0 {
		t.Errorf("second count = %d, want 0 (everything already tracked)", second)
	}
	list := gm.List()
	if len(list) != 3 {
		t.Fatalf("List after double hydrate = %d groups, want 3 (duplicates?)", len(list))
	}
	if _, ok := findState(list, "group:hydrate-idem-2"); !ok {
		t.Error("group:hydrate-idem-2 missing from List")
	}

	// An ID already tracked by a live run is never overwritten by the copy on
	// disk.
	exec := newStepExecutor(1)
	live := NewGroupManager(mockResolve, exec.execute, newLifecycleRecorder().publish)
	live.SetStoreDir(dir)
	const busyID = "group:hydrate-idem-0"
	if _, err := live.Start(context.Background(), busyID, "p1", "live task", "round_robin",
		[]Participant{plainParticipant("a")}, GroupOptions{MaxTurns: 50}, "ch", "chat"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer drainGroup(t, exec, dir, busyID)

	loaded, err := live.LoadHistorical()
	if err != nil {
		t.Fatalf("LoadHistorical with active group: %v", err)
	}
	if loaded != 2 {
		t.Errorf("count = %d, want 2 (already-tracked ID skipped)", loaded)
	}
	st, ok := live.Status(busyID)
	if !ok {
		t.Fatal("active group vanished after LoadHistorical")
	}
	if st.Status != StatusRunning || st.Task != "live task" {
		t.Errorf("active group was clobbered: status=%q task=%q", st.Status, st.Task)
	}
	if got := len(live.List()); got != 3 {
		t.Errorf("List = %d groups, want 3", got)
	}
}

// TestRegression_LoadHistoricalExpiredNotShown: retention (B6) must age
// hydrated groups from their real finish instant, so stale history does not
// flood the WS welcome payload with weeks of transcript.
func TestRegression_LoadHistoricalExpiredNotShown(t *testing.T) {
	dir := t.TempDir()
	useJSONStore(t)

	old := time.Now().Add(-2 * time.Hour)
	states := []*GroupState{
		{
			ID: "group:hydrate-old", Status: StatusDone, Strategy: "round_robin",
			Participants: []Participant{{AgentID: "a", Role: RoleProposer}},
			CreatedAt:    old, UpdatedAt: old,
			Transcript: []Turn{{Index: 0, Speaker: "a", Content: "ancient"}},
		},
		{
			ID: "group:hydrate-fresh", Status: StatusDone, Strategy: "round_robin",
			Participants: []Participant{{AgentID: "a", Role: RoleProposer}},
			CreatedAt:    time.Now().Add(-time.Minute), UpdatedAt: time.Now().Add(-time.Minute),
			Transcript: []Turn{{Index: 0, Speaker: "a", Content: "recent"}},
		},
	}
	for _, s := range states {
		if err := SaveGroup(dir, s); err != nil {
			t.Fatalf("SaveGroup %s: %v", s.ID, err)
		}
	}

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, newLifecycleRecorder().publish)
	gm.SetStoreDir(dir)
	gm.SetRetention(30 * time.Minute)

	n, err := gm.LoadHistorical()
	if err != nil {
		t.Fatalf("LoadHistorical: %v", err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2 (hydration itself does not evict)", n)
	}

	// The first read runs the lazy sweep: the 2h-old group is already past its
	// retention window, the 1min-old one is not.
	list := gm.List()
	if len(list) != 1 {
		t.Fatalf("List = %d groups, want 1 (expired hydrated group not swept)", len(list))
	}
	if list[0].ID != "group:hydrate-fresh" {
		t.Errorf("survivor = %q, want %q", list[0].ID, "group:hydrate-fresh")
	}
	if _, ok := gm.Status("group:hydrate-old"); ok {
		t.Error("expired hydrated group still visible in Status")
	}
	if _, err := gm.Wait("group:hydrate-old"); err == nil {
		t.Error("Wait on expired hydrated group returned nil error")
	}
	if snaps := gm.AllSnapshots(); len(snaps) != 1 {
		t.Errorf("AllSnapshots = %d, want 1", len(snaps))
	}
}

// TestRegression_LoadHistoricalZeroUpdatedAtKeepsGroup: a legacy state with no
// UpdatedAt is aged from now instead of expiring the instant it is loaded.
func TestRegression_LoadHistoricalZeroUpdatedAtKeepsGroup(t *testing.T) {
	dir := t.TempDir()
	useJSONStore(t)

	st := &GroupState{
		ID:           "group:hydrate-zero-time",
		Status:       StatusDone,
		Strategy:     "round_robin",
		Participants: []Participant{{AgentID: "a", Role: RoleProposer}},
		Transcript:   []Turn{{Index: 0, Speaker: "a", Content: "undated"}},
	}
	if err := SaveGroup(dir, st); err != nil {
		t.Fatalf("SaveGroup: %v", err)
	}

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, newLifecycleRecorder().publish)
	gm.SetStoreDir(dir)
	gm.SetRetention(30 * time.Minute)
	if _, err := gm.LoadHistorical(); err != nil {
		t.Fatalf("LoadHistorical: %v", err)
	}
	got, ok := gm.Status(st.ID)
	if !ok {
		t.Fatal("group with zero UpdatedAt expired immediately after hydration")
	}
	if got.Status != StatusDone {
		t.Errorf("status = %q, want %q", got.Status, StatusDone)
	}
}

// TestRegression_NoStoreDirNoop: a manager without persistence configured
// hydrates nothing and never fails.
func TestRegression_NoStoreDirNoop(t *testing.T) {
	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, newLifecycleRecorder().publish)
	n, err := gm.LoadHistorical()
	if err != nil {
		t.Fatalf("LoadHistorical without storeDir: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
	if got := len(gm.List()); got != 0 {
		t.Errorf("List = %d groups, want 0", got)
	}
}

// TestRegression_LoadHistoricalEmptyDirIsNoop covers a configured storeDir that
// does not exist yet (fresh install): no error, nothing loaded.
func TestRegression_LoadHistoricalEmptyDirIsNoop(t *testing.T) {
	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, newLifecycleRecorder().publish)
	gm.SetStoreDir(filepath.Join(t.TempDir(), "does-not-exist"))
	n, err := gm.LoadHistorical()
	if err != nil {
		t.Fatalf("LoadHistorical on missing dir: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

// TestRegression_LoadHistoricalMoaSynthesisPicksAggregator pins the synthesis
// rule for a hydrated moa group: the aggregator's last turn, not the last
// proposal, which is what finalize would have reported.
func TestRegression_LoadHistoricalMoaSynthesisPicksAggregator(t *testing.T) {
	dir := t.TempDir()
	useJSONStore(t)

	now := time.Now().Add(-time.Minute)
	st := &GroupState{
		ID:        "group:hydrate-moa",
		Status:    StatusDone,
		Strategy:  "moa",
		Moderator: "agg",
		Participants: []Participant{
			{AgentID: "a", Role: RoleProposer},
			{AgentID: "agg", Role: RoleAggregator},
		},
		CreatedAt: now, UpdatedAt: now,
		Transcript: []Turn{
			{Index: 0, Speaker: "a", Content: "proposal"},
			{Index: 1, Speaker: "agg", Layer: 1, Content: "the synthesis"},
			{Index: 2, Speaker: "a", Content: "a later proposal"},
		},
	}
	if err := SaveGroup(dir, st); err != nil {
		t.Fatalf("SaveGroup: %v", err)
	}

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, newLifecycleRecorder().publish)
	gm.SetStoreDir(dir)
	if _, err := gm.LoadHistorical(); err != nil {
		t.Fatalf("LoadHistorical: %v", err)
	}

	got, err := gm.Wait(st.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got != "the synthesis" {
		t.Errorf("synthesis = %q, want %q", got, "the synthesis")
	}
}

// TestRegression_LoadHistoricalStopIsInert documents that a hydrated group is
// not resumable: Stop reports it and does nothing, and the group stays in its
// terminal state (retention still owns its disappearance).
func TestRegression_LoadHistoricalStopIsInert(t *testing.T) {
	dir := t.TempDir()
	useJSONStore(t)

	now := time.Now().Add(-time.Minute)
	st := &GroupState{
		ID: "group:hydrate-stop", Status: StatusStopped, Strategy: "round_robin",
		Participants: []Participant{{AgentID: "a", Role: RoleProposer}},
		CreatedAt:    now, UpdatedAt: now,
		Transcript: []Turn{{Index: 0, Speaker: "a", Content: "before stop"}},
	}
	if err := SaveGroup(dir, st); err != nil {
		t.Fatalf("SaveGroup: %v", err)
	}

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, newLifecycleRecorder().publish)
	gm.SetStoreDir(dir)
	if _, err := gm.LoadHistorical(); err != nil {
		t.Fatalf("LoadHistorical: %v", err)
	}

	if !gm.Stop(st.ID) {
		t.Error("Stop on hydrated group = false, want true (it is tracked)")
	}
	after, ok := gm.Status(st.ID)
	if !ok {
		t.Fatal("hydrated group disappeared after Stop")
	}
	if after.Status != StatusStopped {
		t.Errorf("status = %q, want %q (Stop must not mutate a finished group)", after.Status, StatusStopped)
	}
	// A stopped group has no error attached, so Wait reports its transcript.
	got, err := gm.Wait(st.ID)
	if err != nil {
		t.Errorf("Wait on hydrated stopped group: %v", err)
	}
	if got != "before stop" {
		t.Errorf("result = %q, want %q", got, "before stop")
	}
}
