package group

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func useJSONStore(t *testing.T) {
	t.Helper()
	prev := getGroupRepo()
	UseStore(nil)
	t.Cleanup(func() { UseStore(prev) })
}

func withJSONStoreDir(t *testing.T, dir string, pub Publisher) *GroupManager {
	t.Helper()
	useJSONStore(t)
	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, pub)
	gm.SetStoreDir(dir)
	return gm
}

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

func assertGroupFinalized(t *testing.T, dir, groupID string) *GroupState {
	t.Helper()
	st := tryGroupFinalized(dir, groupID, 5*time.Second)
	if st == nil {
		t.Fatalf("group %s: no terminal state persisted under %s after 5s", groupID, dir)
	}
	return st
}

func drainGroup(t *testing.T, exec *stepExecutor, dir, groupID string) {
	t.Helper()
	releaseStep(exec)
	if tryGroupFinalized(dir, groupID, 5*time.Second) == nil {
		t.Errorf("group %s: live run never flushed a terminal state to %s", groupID, dir)
	}
}

func findState(states []*GroupState, id string) (*GroupState, bool) {
	for _, s := range states {
		if s.ID == id {
			return s, true
		}
	}
	return nil, false
}

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
	finalized := assertGroupFinalized(t, dir, groupID)
	if finalized.Status != StatusDone {
		t.Fatalf("persisted status = %q, want %q", finalized.Status, StatusDone)
	}
	writer.DrainRunGoroutine(groupID)

	rec := &mockPublisher{}
	reader := NewGroupManager(mockResolve, (&mockExecutor{}).execute, rec.publish)
	reader.SetStoreDir(dir)
	var n int
	deadline := time.Now().Add(5 * time.Second)
	for {
		n, err = reader.LoadHistorical()
		if err != nil {
			t.Fatalf("LoadHistorical: %v", err)
		}
		if n >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
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

	if c := rec.count(); c != 0 {
		t.Errorf("publisher got %d events during hydration, want 0", c)
	}
}

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
	if got != "partial" {
		t.Errorf("result = %q, want %q", got, "partial")
	}
}

func TestRegression_LoadHistoricalIdempotent(t *testing.T) {
	dir := t.TempDir()
	useJSONStore(t)

	for i := 0; i < 3; i++ {
		st := &GroupState{
			ID:           fmt.Sprintf("group:hydrate-idem-%d", i),
			Participants: []Participant{{AgentID: "a", Role: RoleProposer}},
			Strategy:     "round_robin",
			Status:       StatusDone,
			CreatedAt:    time.Now().Add(-time.Duration(i) * time.Minute),
			UpdatedAt:    time.Now().Add(-time.Duration(i) * time.Minute),
			Transcript:   []Turn{{Index: 0, Speaker: "a", Content: fmt.Sprintf("s%d", i)}},
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
	got, err := gm.Wait(st.ID)
	if err != nil {
		t.Errorf("Wait on hydrated stopped group: %v", err)
	}
	if got != "before stop" {
		t.Errorf("result = %q, want %q", got, "before stop")
	}
}
