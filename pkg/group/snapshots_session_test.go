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

func pinGroupRepo(t *testing.T, repo *store.GroupRepo) {
	t.Helper()
	prev := getGroupRepo()
	UseStore(repo)
	t.Cleanup(func() { UseStore(prev) })
}

func newSQLiteStoreAt(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(dir, "lele.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return s
}

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

func findSnapshot(snaps []GroupSnapshot, id string) (GroupSnapshot, bool) {
	for _, s := range snaps {
		if s.GroupID == id {
			return s, true
		}
	}
	return GroupSnapshot{}, false
}

func snapshotIDs(snaps []GroupSnapshot) []string {
	ids := make([]string, 0, len(snaps))
	for _, s := range snaps {
		ids = append(ids, s.GroupID)
	}
	return ids
}

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

func TestSnapshotsForSession_FiltersBySessionAndAlias(t *testing.T) {
	pinGroupRepo(t, nil)
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

	const memoryID = "group:memory-alias"
	if _, err := gm.Start(context.Background(), memoryID, "p1", "t", "round_robin",
		[]Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}},
		GroupOptions{Rounds: 1}, "web", "chat-old"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(memoryID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
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

	plain := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)
	plain.SetStoreDir(dir)
	if got := snapshotIDs(plain.SnapshotsForSession("chat-1")); len(got) != 1 || got[0] != "group:mine" {
		t.Errorf("without a resolver SnapshotsForSession = %v, want [group:mine]", got)
	}
}

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
	if len(snap.Turns) != 1 {
		t.Errorf("len(Turns) = %d, want 1 (partial transcript preserved)", len(snap.Turns))
	}
}

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

func TestSnapshotsForSession_OrdersNewestFirst(t *testing.T) {
	s := openSQLiteStoreForSnapshots(t)
	pinGroupRepo(t, s.Groups())

	base := time.Now().UTC().Truncate(time.Second)
	seedSQLiteState(t, s.Groups(), persistedState("group:oldest", "chat-1", StatusDone, base.Add(-4*time.Hour)))
	seedSQLiteState(t, s.Groups(), persistedState("group:newest", "chat-1", StatusDone, base))
	seedSQLiteState(t, s.Groups(), persistedState("group:middle", "chat-1", StatusDone, base.Add(-time.Hour)))
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

	if err := s.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	got := snapshotIDs(gm.SnapshotsForSession("chat-5"))
	if len(got) != 1 || got[0] != groupID {
		t.Fatalf("SnapshotsForSession after a store failure = %v, want [%s]", got, groupID)
	}
}

func TestSnapshotsForSession_LiveRunningGroupIsIncluded(t *testing.T) {
	pinGroupRepo(t, nil)

	exec := newStepExecutor(1)
	gm := NewGroupManager(mockResolve, exec.execute, (&mockPublisher{}).publish)

	const groupID = "group:live"
	if _, err := gm.Start(context.Background(), groupID, "p1", "t", "round_robin",
		[]Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}, {AgentID: "b", Role: RoleProposer, Label: "B"}},
		GroupOptions{Rounds: 2, MaxTurns: 4}, "web", "chat-6"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { releaseStep(exec); _, _ = gm.Wait(groupID) })
	exec.waitBlockedFor(t, 2)

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
	if got := gm.SnapshotsForSession("chat-other"); len(got) != 0 {
		t.Errorf("live group leaked to another session: %v", snapshotIDs(got))
	}
}
