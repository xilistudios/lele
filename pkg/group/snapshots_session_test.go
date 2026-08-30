package group

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

// ---------------------------------------------------------------------------
// SnapshotsForSession — read path for issue #239 (layer 2).
//
// Production symptom: a group card in the WebUI disappears permanently after a
// session switch (or a daemon restart). The history endpoints answered from
// AllGroupSnapshots(), which is memory-only: once the B6 retention sweep evicted
// a finished group (30 min), the persisted row in groups_state was never read
// back into the session-scoped payload.
//
// The contract under test:
//   - SnapshotsForSession returns the UNION of in-memory groups and persisted
//     rows (SQLite repo first, legacy JSON dir as fallback), so history outlives
//     retention;
//   - filtering is by OriginChatID, honouring the injected session-alias
//     resolver, and a group with no origin never leaks into any session;
//   - dedupe is per group ID and memory wins (the live state is ahead of the
//     last persisted flush);
//   - a persisted row whose status is not terminal means the writer died before
//     finalizing: it is reported as StatusError with an explanatory synthesis
//     instead of hanging forever as "running";
//   - results are ordered newest first (UpdatedAt desc, ID asc tie-break),
//     matching ListGroups;
//   - a failing store degrades to memory-only instead of dropping the payload.
// ---------------------------------------------------------------------------

// pinGroupRepo points the package-level SQLite repo at repo for the duration of
// the test and restores whatever was configured before. Unlike useTestGroupRepo
// it restores the previous value instead of nil, keeping tests order-independent.
func pinGroupRepo(t *testing.T, repo *store.GroupRepo) {
	t.Helper()
	prev := getGroupRepo()
	UseStore(repo)
	t.Cleanup(func() { UseStore(prev) })
}

// newSQLiteStoreAt opens a SQLite store without registering a Close, so a test
// can close it mid-case to exercise store failures.
func newSQLiteStoreAt(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(dir, "lele.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return s
}

// openSQLiteStoreForSnapshots opens a SQLite store in a fresh temp dir and closes
// it when the test ends.
func openSQLiteStoreForSnapshots(t *testing.T) *store.Store {
	t.Helper()
	s := newSQLiteStoreAt(t, t.TempDir())
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	return s
}

// seedSQLiteState persists a GroupState through the repository, bypassing the
// manager so tests can plant arbitrary rows.
func seedSQLiteState(t *testing.T, repo *store.GroupRepo, st *GroupState) {
	t.Helper()
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal seed state: %v", err)
	}
	if err := repo.SetGroupState(st.ID, string(data)); err != nil {
		t.Fatalf("seed SetGroupState: %v", err)
	}
}

// seedJSONState writes a GroupState JSON file into dir (legacy backend).
func seedJSONState(t *testing.T, dir string, st *GroupState) {
	t.Helper()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatalf("marshal seed state: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	name := sanitizeGroupID(st.ID) + ".json"
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatalf("write seed state: %v", err)
	}
}

// persistedState builds a GroupState for seeding the store.
func persistedState(id, originChat, status string, updated time.Time) *GroupState {
	return &GroupState{
		ID:            id,
		ProfileID:     "p1",
		Task:          "persisted task " + id,
		Participants:  []Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}},
		Strategy:      "round_robin",
		Status:        status,
		CreatedAt:     updated.Add(-time.Minute),
		UpdatedAt:     updated,
		Transcript:    []Turn{{Index: 0, Speaker: "a", Label: "A", Content: "persisted from disk", Tokens: 7}},
		TotalTokens:   7,
		Rounds:        1,
		MaxTurns:      1,
		OriginChannel: "web",
		OriginChatID:  originChat,
	}
}

// findSnapshot returns the snapshot with the given group ID.
func findSnapshot(snaps []GroupSnapshot, id string) (GroupSnapshot, bool) {
	for _, s := range snaps {
		if s.GroupID == id {
			return s, true
		}
	}
	return GroupSnapshot{}, false
}

// snapshotIDs maps a snapshot slice to its group IDs, preserving order.
func snapshotIDs(snaps []GroupSnapshot) []string {
	ids := make([]string, 0, len(snaps))
	for _, s := range snaps {
		ids = append(ids, s.GroupID)
	}
	return ids
}

// TestSnapshotsForSession_IncludesEvictedPersistedGroup is the core #239
// regression: a finished group that is no longer tracked in memory must still be
// served from the persisted store.
func TestSnapshotsForSession_IncludesEvictedPersistedGroup(t *testing.T) {
	s := openSQLiteStoreForSnapshots(t)
	pinGroupRepo(t, s.Groups())

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)
	gm.SetStoreDir(t.TempDir())
	gm.SetRetention(time.Nanosecond)

	const groupID = "group:evicted-session"
	if _, err := gm.Start(context.Background(), groupID, "p1", "solve it", "round_robin",
		[]Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}},
		GroupOptions{Rounds: 1}, "web", "chat-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Precondition: retention really swept it out of memory.
	if _, ok := gm.Status(groupID); ok {
		t.Fatal("precondition failed: group is still tracked in memory")
	}

	got := gm.SnapshotsForSession("chat-1")
	snap, ok := findSnapshot(got, groupID)
	if !ok {
		t.Fatalf("evicted group missing from SnapshotsForSession: %v", snapshotIDs(got))
	}
	if snap.Status != StatusDone {
		t.Errorf("Status = %q, want %q", snap.Status, StatusDone)
	}
	if snap.OriginChatID != "chat-1" {
		t.Errorf("OriginChatID = %q, want %q", snap.OriginChatID, "chat-1")
	}
	if len(snap.Turns) != 1 || snap.Turns[0].Content != "turn-1-a" {
		t.Errorf("Turns = %+v, want the persisted transcript", snap.Turns)
	}
}

// TestSnapshotsForSession_UnionMemoryAndStore proves both sources contribute.
func TestSnapshotsForSession_UnionMemoryAndStore(t *testing.T) {
	s := openSQLiteStoreForSnapshots(t)
	pinGroupRepo(t, s.Groups())

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)
	gm.SetStoreDir(t.TempDir())

	const liveID = "group:memory-only"
	if _, err := gm.Start(context.Background(), liveID, "p1", "live task", "round_robin",
		[]Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}},
		GroupOptions{Rounds: 1}, "web", "chat-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(liveID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// A second group that exists exclusively in the store.
	seedSQLiteState(t, s.Groups(), persistedState("group:store-only", "chat-1", StatusDone, time.Now()))

	got := gm.SnapshotsForSession("chat-1")
	ids := snapshotIDs(got)
	if len(ids) != 2 {
		t.Fatalf("SnapshotsForSession = %v, want the union of memory and store (2 groups)", ids)
	}
	for _, want := range []string{liveID, "group:store-only"} {
		if _, ok := findSnapshot(got, want); !ok {
			t.Errorf("%s missing from union: %v", want, ids)
		}
	}
}

// TestSnapshotsForSession_MemoryWinsOnDedupe pins the dedupe rule: a group that
// is both tracked in memory and present on disk appears exactly once, carrying
// the in-memory (fresher) content.
func TestSnapshotsForSession_MemoryWinsOnDedupe(t *testing.T) {
	s := openSQLiteStoreForSnapshots(t)
	pinGroupRepo(t, s.Groups())

	const groupID = "group:dedupe"

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)
	gm.SetStoreDir(t.TempDir())
	if _, err := gm.Start(context.Background(), groupID, "p1", "fresh task", "round_robin",
		[]Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}, {AgentID: "b", Role: RoleProposer, Label: "B"}},
		GroupOptions{Rounds: 1}, "web", "chat-1"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Overwrite the row the run just persisted with a stale, divergent copy:
	// one turn, still "running". Whatever SnapshotsForSession reports for this
	// ID can therefore only have come from memory.
	seedSQLiteState(t, s.Groups(), persistedState(groupID, "chat-1", StatusRunning, time.Now().Add(-time.Hour)))

	got := gm.SnapshotsForSession("chat-1")
	matches := 0
	for _, sn := range got {
		if sn.GroupID == groupID {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("group appears %d times, want exactly 1 (%v)", matches, snapshotIDs(got))
	}
	snap, _ := findSnapshot(got, groupID)
	if snap.Status != StatusDone {
		t.Errorf("Status = %q, want %q (memory must win over the stale row)", snap.Status, StatusDone)
	}
	if len(snap.Turns) != 2 {
		t.Errorf("len(Turns) = %d, want 2 (memory copy, not the 1-turn stale row)", len(snap.Turns))
	}
}

// TestSnapshotsForSession_FiltersBySessionAndAlias covers the filter branches:
// exact match, alias match through the injected resolver, and non-match.
//
// The alias cases are deliberately built the same way for both sources: the
// group's origin is the pre-rotation key while the caller asks with the rotated
// one. A memory group whose origin already equalled the request would match
// exactly and prove nothing about the resolver, so the live group here is
// started under "chat-old" and looked up as "chat-1".
func TestSnapshotsForSession_FiltersBySessionAndAlias(t *testing.T) {
	pinGroupRepo(t, nil) // legacy JSON store: no shared-repo interference
	dir := t.TempDir()

	now := time.Now().UTC().Truncate(time.Second)
	seedJSONState(t, dir, persistedState("group:mine", "chat-1", StatusDone, now))
	seedJSONState(t, dir, persistedState("group:mine-alias", "chat-old", StatusDone, now))
	seedJSONState(t, dir, persistedState("group:theirs", "chat-2", StatusDone, now))

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)
	gm.SetStoreDir(dir)
	gm.SetSessionAliasResolver(func(chatID string) string {
		if chatID == "chat-old" {
			return "chat-1"
		}
		return chatID
	})

	// A group tracked in memory whose origin is the pre-rotation key, so the
	// resolver must be applied to the memory source as well.
	const memoryID = "group:memory-alias"
	if _, err := gm.Start(context.Background(), memoryID, "p1", "t", "round_robin",
		[]Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}},
		GroupOptions{Rounds: 1}, "web", "chat-old"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(memoryID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	// Precondition: it really is tracked in memory, not just on disk.
	if _, ok := gm.Status(memoryID); !ok {
		t.Fatal("precondition failed: live group is not tracked in memory")
	}

	got := snapshotIDs(gm.SnapshotsForSession("chat-1"))
	joined := strings.Join(got, ",")
	for _, want := range []string{"group:mine", "group:mine-alias", memoryID} {
		if !strings.Contains(joined, want) {
			t.Errorf("%s missing from the session result: %v", want, got)
		}
	}
	if strings.Contains(joined, "group:theirs") {
		t.Errorf("another session's group leaked into the result: %v", got)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3 (%v)", len(got), got)
	}

	// Control: without the resolver only the exact match survives, which is what
	// makes the assertions above meaningful.
	plain := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)
	plain.SetStoreDir(dir)
	if got := snapshotIDs(plain.SnapshotsForSession("chat-1")); len(got) != 1 || got[0] != "group:mine" {
		t.Errorf("without a resolver SnapshotsForSession = %v, want [group:mine]", got)
	}
}

// TestSnapshotsForSession_UnknownOriginIsHidden pins the anti-leak rule: a group
// stored without an origin chat must not be reported for any session, and an
// empty sessionKey must not act as a wildcard.
func TestSnapshotsForSession_UnknownOriginIsHidden(t *testing.T) {
	s := openSQLiteStoreForSnapshots(t)
	pinGroupRepo(t, s.Groups())

	seedSQLiteState(t, s.Groups(), persistedState("group:no-origin", "", StatusDone, time.Now()))

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)
	gm.SetStoreDir(t.TempDir())

	if got := gm.SnapshotsForSession("chat-1"); len(got) != 0 {
		t.Fatalf("group without origin leaked: %v", snapshotIDs(got))
	}
	if got := gm.SnapshotsForSession(""); len(got) != 0 {
		t.Errorf("empty sessionKey matched %v, want no groups", snapshotIDs(got))
	}
}

// TestSnapshotsForSession_NonTerminalStoreRowReportedAsError implements the
// "non-terminal store rows reported as error" rule: a row still marked running
// means the writer died mid-run, so the card must not hang as "running" forever.
func TestSnapshotsForSession_NonTerminalStoreRowReportedAsError(t *testing.T) {
	s := openSQLiteStoreForSnapshots(t)
	pinGroupRepo(t, s.Groups())

	const groupID = "group:orphan"
	seedSQLiteState(t, s.Groups(), persistedState(groupID, "chat-1", StatusRunning, time.Now().Add(-time.Hour)))

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)
	gm.SetStoreDir(t.TempDir())

	got := gm.SnapshotsForSession("chat-1")
	snap, ok := findSnapshot(got, groupID)
	if !ok {
		t.Fatalf("orphan row missing from result: %v", snapshotIDs(got))
	}
	if snap.Status != StatusError {
		t.Errorf("Status = %q, want %q", snap.Status, StatusError)
	}
	if !strings.Contains(snap.Synthesis, "restart") {
		t.Errorf("Synthesis = %q, want an explanation mentioning the restart", snap.Synthesis)
	}
	// The transcript that did survive is still shown.
	if len(snap.Turns) != 1 {
		t.Errorf("len(Turns) = %d, want 1 (partial transcript preserved)", len(snap.Turns))
	}
}

// TestSnapshotsForSession_TerminalStoreRowKeepsItsStatus is the counterpart: a
// stopped or failed group must come back as it was, never rewritten.
func TestSnapshotsForSession_TerminalStoreRowKeepsItsStatus(t *testing.T) {
	s := openSQLiteStoreForSnapshots(t)
	pinGroupRepo(t, s.Groups())

	seedSQLiteState(t, s.Groups(), persistedState("group:stopped", "chat-1", StatusStopped, time.Now()))
	seedSQLiteState(t, s.Groups(), persistedState("group:failed", "chat-1", StatusError, time.Now()))

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)
	gm.SetStoreDir(t.TempDir())

	for id, want := range map[string]string{"group:stopped": StatusStopped, "group:failed": StatusError} {
		snap, ok := findSnapshot(gm.SnapshotsForSession("chat-1"), id)
		if !ok {
			t.Fatalf("%s missing from result", id)
		}
		if snap.Status != want {
			t.Errorf("%s Status = %q, want %q (terminal statuses are preserved)", id, snap.Status, want)
		}
	}
}

// TestSnapshotsForSession_OrdersNewestFirst pins the ordering contract shared
// with ListGroups: UpdatedAt descending, ID ascending as tie-break.
func TestSnapshotsForSession_OrdersNewestFirst(t *testing.T) {
	s := openSQLiteStoreForSnapshots(t)
	pinGroupRepo(t, s.Groups())

	base := time.Now().UTC().Truncate(time.Second)
	seedSQLiteState(t, s.Groups(), persistedState("group:oldest", "chat-1", StatusDone, base.Add(-4*time.Hour)))
	seedSQLiteState(t, s.Groups(), persistedState("group:newest", "chat-1", StatusDone, base))
	seedSQLiteState(t, s.Groups(), persistedState("group:middle", "chat-1", StatusDone, base.Add(-time.Hour)))
	// Tie pair (same timestamp) to exercise the ID-ascending tie-break.
	tie := base.Add(-2 * time.Hour)
	seedSQLiteState(t, s.Groups(), persistedState("group:middle-b", "chat-1", StatusDone, tie))
	seedSQLiteState(t, s.Groups(), persistedState("group:middle-a", "chat-1", StatusDone, tie))

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)
	gm.SetStoreDir(t.TempDir())

	got := snapshotIDs(gm.SnapshotsForSession("chat-1"))
	want := []string{"group:newest", "group:middle", "group:middle-a", "group:middle-b", "group:oldest"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// TestSnapshotsForSession_LegacyJSONDirFallback covers deployments without a
// SQLite repo: the JSON directory is the store.
func TestSnapshotsForSession_LegacyJSONDirFallback(t *testing.T) {
	pinGroupRepo(t, nil)

	dir := t.TempDir()
	seedJSONState(t, dir, persistedState("group:legacy", "chat-9", StatusDone, time.Now()))
	seedJSONState(t, dir, persistedState("group:legacy-other", "chat-8", StatusDone, time.Now()))

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)
	gm.SetStoreDir(dir)

	got := snapshotIDs(gm.SnapshotsForSession("chat-9"))
	if len(got) != 1 || got[0] != "group:legacy" {
		t.Fatalf("SnapshotsForSession = %v, want [group:legacy]", got)
	}
}

// TestSnapshotsForSession_NoStoreConfiguredUsesMemoryOnly pins that a manager
// without any persistence still answers from memory (the pre-#239 behaviour must
// not regress for in-memory groups).
func TestSnapshotsForSession_NoStoreConfiguredUsesMemoryOnly(t *testing.T) {
	pinGroupRepo(t, nil)

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)

	const groupID = "group:no-store"
	if _, err := gm.Start(context.Background(), groupID, "p1", "t", "round_robin",
		[]Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}},
		GroupOptions{Rounds: 1}, "web", "chat-3"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	got := snapshotIDs(gm.SnapshotsForSession("chat-3"))
	if len(got) != 1 || got[0] != groupID {
		t.Fatalf("SnapshotsForSession = %v, want [%s]", got, groupID)
	}
	if got := gm.SnapshotsForSession("chat-other"); len(got) != 0 {
		t.Errorf("other session got %v, want none", got)
	}
}

// TestSnapshotsForSession_WithoutResolverMatchesExactly documents the default:
// no resolver installed means exact OriginChatID matching only.
func TestSnapshotsForSession_WithoutResolverMatchesExactly(t *testing.T) {
	s := openSQLiteStoreForSnapshots(t)
	pinGroupRepo(t, s.Groups())

	seedSQLiteState(t, s.Groups(), persistedState("group:alias-noop", "chat-old", StatusDone, time.Now()))

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)
	gm.SetStoreDir(t.TempDir())

	if got := gm.SnapshotsForSession("chat-1"); len(got) != 0 {
		t.Fatalf("without a resolver, %v leaked into chat-1", snapshotIDs(got))
	}
	if got := snapshotIDs(gm.SnapshotsForSession("chat-old")); len(got) != 1 {
		t.Fatalf("exact match failed: %v", got)
	}
}

// TestSnapshotsForSession_StoreFailureDegradesToMemory guarantees an unreadable
// store never blanks the payload: the client still gets what memory holds.
func TestSnapshotsForSession_StoreFailureDegradesToMemory(t *testing.T) {
	dir := t.TempDir()
	s := newSQLiteStoreAt(t, dir)
	pinGroupRepo(t, s.Groups())

	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)
	gm.SetStoreDir(t.TempDir())

	const groupID = "group:survivor"
	if _, err := gm.Start(context.Background(), groupID, "p1", "t", "round_robin",
		[]Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}},
		GroupOptions{Rounds: 1}, "web", "chat-5"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Break the store: closing the pool makes every read fail.
	if err := s.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	got := snapshotIDs(gm.SnapshotsForSession("chat-5"))
	if len(got) != 1 || got[0] != groupID {
		t.Fatalf("SnapshotsForSession after a store failure = %v, want [%s]", got, groupID)
	}
}

// TestSnapshotsForSession_LiveRunningGroupIsIncluded covers the in-flight case:
// a group that is still running must appear with status running so the WebUI can
// reattach its card mid-run after a reconnect.
func TestSnapshotsForSession_LiveRunningGroupIsIncluded(t *testing.T) {
	pinGroupRepo(t, nil)

	exec := newStepExecutor(1)
	// No storeDir on purpose: this case is about the memory source, and an
	// unconfigured backend means no async flush can race the test's cleanup.
	gm := NewGroupManager(mockResolve, exec.execute, (&mockPublisher{}).publish)

	const groupID = "group:live"
	if _, err := gm.Start(context.Background(), groupID, "p1", "t", "round_robin",
		[]Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}, {AgentID: "b", Role: RoleProposer, Label: "B"}},
		GroupOptions{Rounds: 2, MaxTurns: 4}, "web", "chat-6"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { releaseStep(exec); _, _ = gm.Wait(groupID) })
	exec.waitBlockedFor(t, 2)

	// The group is parked inside turn 2: it is running, with one turn stored.
	snap, ok := findSnapshot(gm.SnapshotsForSession("chat-6"), groupID)
	if !ok {
		t.Fatal("live group not reported by SnapshotsForSession")
	}
	if snap.Status != StatusRunning {
		t.Errorf("Status = %q, want %q", snap.Status, StatusRunning)
	}
	if len(snap.Turns) != 1 {
		t.Errorf("len(Turns) = %d, want 1", len(snap.Turns))
	}
	// Another session must not see it.
	if got := gm.SnapshotsForSession("chat-other"); len(got) != 0 {
		t.Errorf("live group leaked to another session: %v", snapshotIDs(got))
	}
}
