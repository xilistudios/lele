package session

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

func metaByTaskID(t *testing.T, rows []SubagentSessionMeta, taskID string) SubagentSessionMeta {
	t.Helper()
	for _, row := range rows {
		if row.TaskID == taskID {
			return row
		}
	}
	t.Fatalf("task %q missing from %+v", taskID, rows)
	return SubagentSessionMeta{}
}

func metaByKey(t *testing.T, rows []SubagentSessionMeta, key string) SubagentSessionMeta {
	t.Helper()
	for _, row := range rows {
		if row.Key == key {
			return row
		}
	}
	t.Fatalf("key %q missing from %+v", key, rows)
	return SubagentSessionMeta{}
}

func TestListSubagentSessionMeta_ResidentAndColdAgree(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:meta-parity"
	recordSubagent(t, sm, parent, "subagent-1", "first answer")
	recordSubagent(t, sm, parent, "subagent-2", "second answer")
	sm.SetSubagentStatus(parent+":subagent-1", "failed")
	sm.SetSubagentAgentID(parent+":subagent-1", "coder")
	recordSubagent(t, sm, "native:other-meta", "subagent-9", "unrelated")
	recordSubagent(t, sm, parent, "not-a-subagent", "unrelated")

	resident := sm.ListSubagentSessionMeta([]string{parent})
	if len(resident) != 2 {
		t.Fatalf("resident listing = %d rows, want 2 (%+v)", len(resident), resident)
	}
	for _, row := range resident {
		if !row.Resident {
			t.Errorf("%s: Resident = false, want true", row.Key)
		}
		if row.ParentKey != parent {
			t.Errorf("%s: ParentKey = %q, want %q", row.Key, row.ParentKey, parent)
		}
		if row.Key != parent+":"+row.TaskID {
			t.Errorf("%s: Key/TaskID disagree (%q)", row.Key, row.TaskID)
		}
		if row.Name == "" {
			t.Errorf("%s: Name is empty, want the session name", row.Key)
		}
	}

	for _, taskID := range []string{"subagent-1", "subagent-2"} {
		if !sm.EvictSession(parent + ":" + taskID) {
			t.Fatalf("EvictSession(%s) = false", taskID)
		}
	}
	cold := sm.ListSubagentSessionMeta([]string{parent})
	if len(cold) != 2 {
		t.Fatalf("cold listing = %d rows, want 2 (%+v)", len(cold), cold)
	}
	for _, want := range resident {
		got := metaByKey(t, cold, want.Key)
		if got.Resident {
			t.Errorf("%s: Resident = true after eviction, want false", want.Key)
		}
		if got.TaskID != want.TaskID || got.ParentKey != want.ParentKey || got.Name != want.Name {
			t.Errorf("%s: cold row = %+v, want identifying fields of %+v", want.Key, got, want)
		}
		if got.Status != want.Status || got.AgentID != want.AgentID {
			t.Errorf("%s: cold status/agent = %q/%q, want %q/%q", want.Key, got.Status, got.AgentID, want.Status, want.AgentID)
		}
		if !got.Created.Equal(want.Created) || !got.Updated.Equal(want.Updated) {
			t.Errorf("%s: cold timestamps = %v/%v, want %v/%v", want.Key, got.Created, got.Updated, want.Created, want.Updated)
		}
	}
}

func TestListSubagentSessionMeta_NoStoreStillReportsColdRows(t *testing.T) {
	sm := NewSessionManager()
	parent := "native:meta-nostore"
	key := parent + ":subagent-1"
	sm.AddMessage(key, "user", "task")
	sm.AddMessage(key, "assistant", "done")
	sm.SetSubagentStatus(key, "completed")

	if !sm.EvictSession(key) {
		t.Fatal("EvictSession = false")
	}
	if _, resident := sm.sessions[key]; resident {
		t.Fatal("premise broken: session is still resident")
	}

	rows := sm.ListSubagentSessionMeta([]string{parent})
	if len(rows) != 1 {
		t.Fatalf("listing = %d rows, want 1 (%+v)", len(rows), rows)
	}
	row := rows[0]
	if row.Key != key || row.TaskID != "subagent-1" || row.ParentKey != parent {
		t.Errorf("row = %+v, want key %q / task subagent-1 / parent %q", row, key, parent)
	}
	if row.Resident {
		t.Error("Resident = true, want false (the row came from the metadata mirror)")
	}
	if row.Status != "completed" {
		t.Errorf("Status = %q, want %q", row.Status, "completed")
	}

	if got := sm.FindSubagentSessions(parent); len(got) != 0 {
		t.Errorf("FindSubagentSessions = %d rows, want 0 (its no-store behaviour must not change)", len(got))
	}
}

func TestListSubagentSessionMeta_DoesNotLoadOrTouchLRU(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:meta-noload"
	for i := 1; i <= 4; i++ {
		recordSubagent(t, sm, parent, fmt.Sprintf("subagent-%d", i), "answer")
	}
	chatKey := "native:meta-live-chat"
	sm.AddMessage(chatKey, "user", "hello")
	if err := sm.Save(chatKey); err != nil {
		t.Fatalf("Save(%q): %v", chatKey, err)
	}
	for i := 1; i <= 4; i++ {
		if !sm.EvictSession(fmt.Sprintf("%s:subagent-%d", parent, i)) {
			t.Fatalf("EvictSession(subagent-%d) = false", i)
		}
	}

	snapshotResident := func() map[string]*Session {
		sm.mu.RLock()
		defer sm.mu.RUnlock()
		snap := make(map[string]*Session, len(sm.sessions))
		for key, session := range sm.sessions {
			snap[key] = session
		}
		return snap
	}
	snapshotAccess := func() map[string]time.Time {
		sm.mu.RLock()
		defer sm.mu.RUnlock()
		snap := make(map[string]time.Time, len(sm.accessTimes))
		for key, at := range sm.accessTimes {
			snap[key] = at
		}
		return snap
	}
	snapshotMeta := func() map[string]*sessionMetadata {
		sm.mu.RLock()
		defer sm.mu.RUnlock()
		snap := make(map[string]*sessionMetadata, len(sm.sessionMeta))
		for key, meta := range sm.sessionMeta {
			snap[key] = meta
		}
		return snap
	}

	rec := useRecordingSessionRepo(t, s.Sessions())

	sm.SetMaxInMemory(1)
	beforeResident := snapshotResident()
	beforeAccess := snapshotAccess()
	beforeMeta := snapshotMeta()

	rows := sm.ListSubagentSessionMeta([]string{parent})
	if len(rows) != 4 {
		t.Fatalf("listing = %d rows, want 4 (%+v)", len(rows), rows)
	}
	if queried := rec.queried(); len(queried) != 0 {
		t.Errorf("the metadata listing queried the store for %v, want no I/O at all", queried)
	}

	afterResident := snapshotResident()
	if len(afterResident) != len(beforeResident) {
		t.Fatalf("resident sessions changed: %d → %d (the listing must not load sessions)", len(beforeResident), len(afterResident))
	}
	for key, session := range beforeResident {
		if afterResident[key] != session {
			t.Errorf("resident session %q was replaced or evicted by the listing", key)
		}
	}
	for i := 1; i <= 4; i++ {
		key := fmt.Sprintf("%s:subagent-%d", parent, i)
		if _, resident := afterResident[key]; resident {
			t.Errorf("subagent %q became resident: the listing loaded a full session", key)
		}
	}

	afterAccess := snapshotAccess()
	if len(afterAccess) != len(beforeAccess) {
		t.Fatalf("accessTimes changed: %d → %d", len(beforeAccess), len(afterAccess))
	}
	for key, at := range beforeAccess {
		if !afterAccess[key].Equal(at) {
			t.Errorf("accessTimes[%q] moved: %v → %v", key, at, afterAccess[key])
		}
	}

	afterMeta := snapshotMeta()
	if len(afterMeta) != len(beforeMeta) {
		t.Fatalf("sessionMeta changed: %d → %d", len(beforeMeta), len(afterMeta))
	}
	for key, meta := range beforeMeta {
		if afterMeta[key] != meta {
			t.Errorf("sessionMeta[%q] was replaced by the listing", key)
		}
	}
}

func TestListSubagentSessionMeta_DedupesAcrossOverlappingPrefixes(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:dedupe-parent"
	key := recordSubagent(t, sm, parent, "subagent-1", "answer")

	prefixes := []string{"native", "native:dedupe-parent", parent, parent}
	rows := sm.ListSubagentSessionMeta(prefixes)
	if len(rows) != 1 {
		t.Fatalf("listing = %d rows, want 1 (dedupe by session key) (%+v)", len(rows), rows)
	}
	if rows[0].Key != key {
		t.Errorf("Key = %q, want %q", rows[0].Key, key)
	}

	if !sm.EvictSession(key) {
		t.Fatal("EvictSession = false")
	}
	if rows := sm.ListSubagentSessionMeta(prefixes); len(rows) != 1 {
		t.Fatalf("cold listing = %d rows, want 1 (%+v)", len(rows), rows)
	}
}

func TestListSubagentSessionMeta_EmptyAndBlankPrefixes(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	recordSubagent(t, sm, "native:blank-parent", "subagent-1", "answer")

	if rows := sm.ListSubagentSessionMeta(nil); rows != nil {
		t.Errorf("nil prefixes = %+v, want nil", rows)
	}
	if rows := sm.ListSubagentSessionMeta([]string{}); rows != nil {
		t.Errorf("empty prefixes = %+v, want nil", rows)
	}
	if rows := sm.ListSubagentSessionMeta([]string{"", ""}); rows != nil {
		t.Errorf("blank prefixes = %+v, want nil (a blank prefix must not match every subagent)", rows)
	}
	rows := sm.ListSubagentSessionMeta([]string{"", "native:blank-parent"})
	if len(rows) != 1 || rows[0].TaskID != "subagent-1" {
		t.Errorf("mixed prefixes = %+v, want exactly subagent-1", rows)
	}
}

func TestListSubagentSessionMeta_CrossOriginSameTaskIDBothReported(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	chatA := "native:cross-a"
	chatB := "native:cross-b"
	keyA := recordSubagent(t, sm, chatA, "subagent-1", "answer A")
	keyB := recordSubagent(t, sm, chatB, "subagent-1", "answer B")

	rows := sm.ListSubagentSessionMeta([]string{chatA, chatB})
	if len(rows) != 2 {
		t.Fatalf("listing = %d rows, want 2 (same task ID, different origins) (%+v)", len(rows), rows)
	}
	metaByKey(t, rows, keyA)
	metaByKey(t, rows, keyB)

	scoped := sm.ListSubagentSessionMeta([]string{chatA})
	if len(scoped) != 1 || scoped[0].Key != keyA {
		t.Errorf("scoped listing = %+v, want only %q", scoped, keyA)
	}
}

func TestListSubagentSessionMeta_StatusAndAgentIDFromMetadata(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:meta-status"
	statuses := map[string]string{
		"subagent-1": "failed",
		"subagent-2": "cancelled",
		"subagent-3": "not_done",
		"subagent-4": "completed",
	}
	for taskID := range statuses {
		recordSubagent(t, sm, parent, taskID, "answer")
	}
	for taskID, status := range statuses {
		sm.SetSubagentStatus(parent+":"+taskID, status)
		sm.SetSubagentAgentID(parent+":"+taskID, "coder")
	}

	check := func(label string, rows []SubagentSessionMeta) {
		t.Helper()
		for taskID, status := range statuses {
			row := metaByTaskID(t, rows, taskID)
			if row.Status != status {
				t.Errorf("%s %s: Status = %q, want %q", label, taskID, row.Status, status)
			}
			if row.AgentID != "coder" {
				t.Errorf("%s %s: AgentID = %q, want %q", label, taskID, row.AgentID, "coder")
			}
		}
	}

	check("resident", sm.ListSubagentSessionMeta([]string{parent}))

	for taskID := range statuses {
		if !sm.EvictSession(parent + ":" + taskID) {
			t.Fatalf("EvictSession(%s) = false", taskID)
		}
	}
	check("cold", sm.ListSubagentSessionMeta([]string{parent}))
}

type recordingSessionRepo struct {
	sessionRepo
	mu   sync.Mutex
	keys [][]string
}

func (r *recordingSessionRepo) SessionListingStats(keys []string) (map[string]store.SessionListingStats, error) {
	r.mu.Lock()
	r.keys = append(r.keys, append([]string(nil), keys...))
	r.mu.Unlock()
	return r.sessionRepo.(*store.SessionRepo).SessionListingStats(keys)
}

func useRecordingSessionRepo(t *testing.T, inner *store.SessionRepo) *recordingSessionRepo {
	t.Helper()
	rec := &recordingSessionRepo{sessionRepo: inner}
	prev := repoFor
	repoFor = func(*SessionManager) sessionRepo { return rec }
	t.Cleanup(func() { repoFor = prev })
	return rec
}

func (r *recordingSessionRepo) queried() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var all []string
	for _, batch := range r.keys {
		all = append(all, batch...)
	}
	return all
}

func TestSubagentSessionDetails_BoundedKeySet(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:details-window"
	var keys []string
	for i := 1; i <= 10; i++ {
		keys = append(keys, recordSubagent(t, sm, parent, fmt.Sprintf("subagent-%d", i), fmt.Sprintf("answer %d", i)))
	}
	for _, key := range keys[1:] {
		if !sm.EvictSession(key) {
			t.Fatalf("EvictSession(%q) = false", key)
		}
	}

	rec := useRecordingSessionRepo(t, s.Sessions())

	page := []string{keys[0], keys[3], keys[7], keys[7]}
	got := sm.SubagentSessionDetails(page)
	if len(got) != 3 {
		t.Fatalf("details = %d entries, want 3 (%+v)", len(got), got)
	}
	if got[keys[0]].Iterations != 1 || got[keys[0]].Summary != "answer 1" {
		t.Errorf("resident detail = %+v, want 1 iteration and %q", got[keys[0]], "answer 1")
	}
	if got[keys[3]].Summary != "answer 4" || got[keys[7]].Summary != "answer 8" {
		t.Errorf("cold details = %+v / %+v, want answers 4 and 8", got[keys[3]], got[keys[7]])
	}

	queried := rec.queried()
	if len(queried) != 2 {
		t.Fatalf("store queried %d keys (%v), want exactly the 2 cold requested keys", len(queried), queried)
	}
	for _, key := range queried {
		if key == keys[0] {
			t.Errorf("resident key %q was sent to the store", key)
		}
		if key != keys[3] && key != keys[7] {
			t.Errorf("unrequested key %q was sent to the store", key)
		}
	}
}

func TestSubagentSessionDetails_MatchesFindSubagentSessionsSummary(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:details-parity"
	withSummary := recordSubagent(t, sm, parent, "subagent-1", "short", strings.Repeat("x", 300))
	plain := recordSubagent(t, sm, parent, "subagent-2", "first answer")
	sm.SetSummary(withSummary, "Found 3 files")

	assertAgrees := func(label string, mgr *SessionManager) {
		t.Helper()
		listing := mgr.FindSubagentSessions(parent)
		if len(listing) != 2 {
			t.Fatalf("%s: FindSubagentSessions = %d entries, want 2", label, len(listing))
		}
		keys := []string{withSummary, plain}
		details := mgr.SubagentSessionDetails(keys)
		for _, info := range listing {
			detail, ok := details[info.Key]
			if !ok {
				t.Fatalf("%s: no detail for %q", label, info.Key)
			}
			if detail.Iterations != info.Iterations {
				t.Errorf("%s %s: Iterations = %d, want %d", label, info.TaskID, detail.Iterations, info.Iterations)
			}
			if detail.Summary != info.Summary {
				t.Errorf("%s %s: Summary = %q, want %q", label, info.TaskID, detail.Summary, info.Summary)
			}
		}
	}

	assertAgrees("resident", sm)

	for _, key := range []string{withSummary, plain} {
		if !sm.EvictSession(key) {
			t.Fatalf("EvictSession(%q) = false", key)
		}
	}
	assertAgrees("cold", sm)

	restarted := NewSessionManager()
	restarted.SetSessionRepo(s.Sessions())
	assertAgrees("restart", restarted)
}

func TestSubagentSessionDetails_CorruptRowOmitted(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:details-corrupt"
	good := parent + ":subagent-1"
	corrupt := parent + ":subagent-2"
	persistRawSubagent(t, s, good, 0, []store.MessageRow{
		{Seq: 0, Role: "user", JSON: storedMsgJSON("user", "task")},
		{Seq: 1, Role: "assistant", JSON: storedMsgJSON("assistant", "older ok")},
	})
	persistRawSubagent(t, s, corrupt, 0, []store.MessageRow{
		{Seq: 0, Role: "user", JSON: storedMsgJSON("user", "task")},
		{Seq: 1, Role: "assistant", JSON: `{"role":"assistant","content":"trunc`},
	})

	details := sm.SubagentSessionDetails([]string{good, corrupt})
	if _, ok := details[good]; !ok {
		t.Fatalf("readable session missing from details (%+v)", details)
	}
	if details[good].Iterations != 1 || details[good].Summary != "older ok" {
		t.Errorf("readable detail = %+v, want 1 iteration and %q", details[good], "older ok")
	}
	if got, ok := details[corrupt]; !ok || got.Iterations != 0 || got.Summary != "" {
		t.Errorf("corrupt detail = %+v (present %v), want {0, \"\"}", got, ok)
	}

	if _, ok := details[parent+":subagent-404"]; ok {
		t.Error("missing session was reported instead of omitted")
	}
}

func TestSubagentSessionDetails_NoStoreReturnsEmpty(t *testing.T) {
	sm := NewSessionManager()
	residentKey := "native:details-nostore:subagent-1"
	sm.AddMessage(residentKey, "user", "task")
	sm.AddMessage(residentKey, "assistant", "done")

	details := sm.SubagentSessionDetails([]string{residentKey, "native:details-nostore:subagent-2"})
	if len(details) != 1 {
		t.Fatalf("details = %+v, want only the resident key", details)
	}
	if got := details[residentKey]; got.Iterations != 1 || got.Summary != "done" {
		t.Errorf("resident detail = %+v, want 1 iteration and %q", got, "done")
	}

	if got := sm.SubagentSessionDetails(nil); got == nil || len(got) != 0 {
		t.Errorf("nil keys = %+v, want an empty non-nil map", got)
	}
	if got := sm.SubagentSessionDetails([]string{"", ""}); len(got) != 0 {
		t.Errorf("blank keys = %+v, want empty", got)
	}
}

func TestFindSubagentSessions_UnchangedByLedgerAdditions(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:ledger-guard"
	key := recordSubagent(t, sm, parent, "subagent-1", "first answer")
	sm.SetSubagentStatus(key, "failed")

	before := sm.FindSubagentSessions(parent)
	if len(before) != 1 {
		t.Fatalf("listing = %d entries, want 1", len(before))
	}

	sm.ListSubagentSessionMeta([]string{parent, "native"})
	sm.SubagentSessionDetails([]string{key})

	after := sm.FindSubagentSessions(parent)
	if len(after) != len(before) {
		t.Fatalf("listing = %d entries after the ledger calls, want %d", len(after), len(before))
	}
	for i := range before {
		assertSubagentSame(t, "after ledger calls", before[i], after[i])
	}
	if after[0].Status != "failed" || after[0].Summary != "first answer" || after[0].Iterations != 1 {
		t.Errorf("entry = %+v, want status failed, summary %q, 1 iteration", after[0], "first answer")
	}
}

func TestListSubagentSessionMeta_NoResidencyNoLRUTouch(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:meta-noresidency"
	for i := 1; i <= 3; i++ {
		recordSubagent(t, sm, parent, fmt.Sprintf("subagent-%d", i), "answer")
	}
	liveKey := "native:meta-noresidency-live"
	sm.AddMessage(liveKey, "user", "hello")
	if err := sm.Save(liveKey); err != nil {
		t.Fatalf("Save(%q): %v", liveKey, err)
	}
	for i := 1; i <= 3; i++ {
		if !sm.EvictSession(fmt.Sprintf("%s:subagent-%d", parent, i)) {
			t.Fatalf("EvictSession(subagent-%d) = false", i)
		}
	}

	sm.SetMaxInMemory(1)

	keys := func() []string {
		sm.mu.RLock()
		defer sm.mu.RUnlock()
		out := make([]string, 0, len(sm.sessions))
		for key := range sm.sessions {
			out = append(out, key)
		}
		return out
	}
	access := func() map[string]time.Time {
		sm.mu.RLock()
		defer sm.mu.RUnlock()
		out := make(map[string]time.Time, len(sm.accessTimes))
		for key, at := range sm.accessTimes {
			out[key] = at
		}
		return out
	}

	beforeKeys := keys()
	beforeAccess := access()

	rows := sm.ListSubagentSessionMeta([]string{parent})
	if len(rows) != 3 {
		t.Fatalf("listing = %d rows, want 3 (%+v)", len(rows), rows)
	}

	afterKeys := keys()
	if len(afterKeys) != len(beforeKeys) {
		t.Fatalf("resident sessions changed: %v → %v (the listing must not load sessions)", beforeKeys, afterKeys)
	}
	before := make(map[string]bool, len(beforeKeys))
	for _, key := range beforeKeys {
		before[key] = true
	}
	for _, key := range afterKeys {
		if !before[key] {
			t.Errorf("the listing made %q resident", key)
		}
	}
	for i := 1; i <= 3; i++ {
		key := fmt.Sprintf("%s:subagent-%d", parent, i)
		if before[key] {
			t.Errorf("subagent %q was resident before the call; the fixture is wrong", key)
		}
	}

	afterAccess := access()
	if len(afterAccess) != len(beforeAccess) {
		t.Fatalf("accessTimes changed: %d → %d entries", len(beforeAccess), len(afterAccess))
	}
	for key, at := range beforeAccess {
		if !afterAccess[key].Equal(at) {
			t.Errorf("accessTimes[%q] moved: %v → %v", key, at, afterAccess[key])
		}
	}

	if _, ok := beforeAccess[liveKey]; !ok {
		t.Fatalf("the live chat %q is not resident; the fixture is wrong", liveKey)
	}
}

func TestListSubagentSessionMeta_NestedSubagentParent(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const grandparent = "native:nested-grandparent"
	const parent = grandparent + ":subagent-1"
	const child = parent + ":subagent-2"

	recordSubagent(t, sm, grandparent, "subagent-1", "parent answer")
	recordSubagent(t, sm, parent, "subagent-2", "child answer")

	rows := sm.ListSubagentSessionMeta([]string{parent})
	if len(rows) != 1 {
		t.Fatalf("listing for the parent = %d rows, want 1 (%+v)", len(rows), rows)
	}
	row := metaByKey(t, rows, child)
	if row.ParentKey != parent {
		t.Errorf("ParentKey = %q, want %q (the innermost parent)", row.ParentKey, parent)
	}
	if row.TaskID != "subagent-2" {
		t.Errorf("TaskID = %q, want %q", row.TaskID, "subagent-2")
	}
	if got := row.ParentKey + ":" + row.TaskID; got != child {
		t.Errorf("ParentKey+TaskID = %q, want the session key %q", got, child)
	}

	grandRows := sm.ListSubagentSessionMeta([]string{grandparent})
	if len(grandRows) != 2 {
		t.Fatalf("listing for the grandparent = %d rows, want 2 (%+v)", len(grandRows), grandRows)
	}
	own := metaByKey(t, grandRows, grandparent+":subagent-1")
	if own.ParentKey != grandparent {
		t.Errorf("ParentKey of the grandparent's own child = %q, want %q", own.ParentKey, grandparent)
	}
	nested := metaByKey(t, grandRows, child)
	if nested.ParentKey != parent {
		t.Errorf("the grandchild was attributed to %q, want the innermost parent %q", nested.ParentKey, parent)
	}
	if nested.ParentKey == grandparent {
		t.Errorf("the grandchild %q was attributed to the grandparent", child)
	}

	if !sm.EvictSession(child) {
		t.Fatalf("EvictSession(%q) = false", child)
	}
	cold := metaByKey(t, sm.ListSubagentSessionMeta([]string{parent}), child)
	if cold.ParentKey != parent || cold.TaskID != "subagent-2" {
		t.Errorf("cold row = %+v, want parent %q and task subagent-2", cold, parent)
	}
	if cold.Resident {
		t.Error("Resident = true for an evicted session")
	}
}
