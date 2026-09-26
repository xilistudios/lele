package session

// Tests for the read-only subagent listing path.
//
// FindSubagentSessions used to take the session manager's GLOBAL EXCLUSIVE lock,
// load every matching subagent session from SQLite (whole message lists, JSON
// and all) and insert those loads into the in-memory LRU. These tests pin the
// two properties that matter now: the reported fields are bit-for-bit the same
// as the load-based implementation produced, and the call never materializes a
// session nor perturbs the LRU.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

// recordSubagent persists a subagent session with one user message followed by
// the given assistant replies.
func recordSubagent(t *testing.T, sm *SessionManager, parent, taskID string, replies ...string) string {
	t.Helper()
	key := parent + ":" + taskID
	sm.AddMessage(key, "user", "task "+taskID)
	for _, reply := range replies {
		sm.AddMessage(key, "assistant", reply)
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save(%q): %v", key, err)
	}
	return key
}

func subagentByTaskID(t *testing.T, infos []SubagentSessionInfo, taskID string) SubagentSessionInfo {
	t.Helper()
	for _, info := range infos {
		if info.TaskID == taskID {
			return info
		}
	}
	t.Fatalf("task %q missing from %+v", taskID, infos)
	return SubagentSessionInfo{}
}

func assertSubagentSame(t *testing.T, label string, want, got SubagentSessionInfo) {
	t.Helper()
	if got.Key != want.Key {
		t.Errorf("%s: Key = %q, want %q", label, got.Key, want.Key)
	}
	if got.TaskID != want.TaskID {
		t.Errorf("%s: TaskID = %q, want %q", label, got.TaskID, want.TaskID)
	}
	if got.Name != want.Name {
		t.Errorf("%s: Name = %q, want %q", label, got.Name, want.Name)
	}
	if got.Status != want.Status {
		t.Errorf("%s: Status = %q, want %q", label, got.Status, want.Status)
	}
	if got.Iterations != want.Iterations {
		t.Errorf("%s: Iterations = %d, want %d", label, got.Iterations, want.Iterations)
	}
	if got.Summary != want.Summary {
		t.Errorf("%s: Summary = %q, want %q", label, got.Summary, want.Summary)
	}
	if !got.Created.Equal(want.Created) {
		t.Errorf("%s: Created = %v, want %v", label, got.Created, want.Created)
	}
	if !got.Updated.Equal(want.Updated) {
		t.Errorf("%s: Updated = %v, want %v", label, got.Updated, want.Updated)
	}
}

// TestFindSubagentSessions_ResidentColdAndRestartAgree is the "result
// unchanged" test: for the same parent, a resident session, a metadata-only
// (evicted) session and a session discovered after a restart must report
// identical fields. The evicted and restart cases are the ones that used to
// pay for a full history load.
func TestFindSubagentSessions_ResidentColdAndRestartAgree(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:client-parity"
	recordSubagent(t, sm, parent, "subagent-1", "first answer")
	withSummary := recordSubagent(t, sm, parent, "subagent-2", "short", strings.Repeat("x", 300))
	recordSubagent(t, sm, parent, "subagent-3") // user message only: no assistant reply
	sm.SetSummary(withSummary, "Found 3 files")
	sm.SetSubagentStatus(parent+":subagent-1", "failed")
	sm.SetSubagentStatus(parent+":subagent-2", "completed")
	// A session of another parent and the parent itself must stay out.
	recordSubagent(t, sm, "native:other-client", "subagent-9", "unrelated")
	recordSubagent(t, sm, parent, "not-a-subagent", "unrelated")

	resident := sm.FindSubagentSessions(parent)
	if len(resident) != 3 {
		t.Fatalf("resident listing = %d entries, want 3 (%+v)", len(resident), resident)
	}

	// Sanity-check the values themselves, not just their stability: they are
	// what the WebUI renders.
	one := subagentByTaskID(t, resident, "subagent-1")
	if one.Iterations != 1 || one.Summary != "first answer" || one.Status != "failed" {
		t.Errorf("subagent-1 = %+v, want 1 iteration, summary %q, status failed", one, "first answer")
	}
	two := subagentByTaskID(t, resident, "subagent-2")
	if two.Iterations != 2 || two.Summary != "Found 3 files" {
		t.Errorf("subagent-2 = %+v, want 2 iterations and the stored summary", two)
	}
	three := subagentByTaskID(t, resident, "subagent-3")
	if three.Iterations != 0 || three.Summary != "" || three.Name == "" {
		t.Errorf("subagent-3 = %+v, want 0 iterations, empty summary, named session", three)
	}

	// Phase 2: evict every subagent → metadata-only, no messages in memory.
	for _, taskID := range []string{"subagent-1", "subagent-2", "subagent-3"} {
		if !sm.EvictSession(parent + ":" + taskID) {
			t.Fatalf("EvictSession(%s) = false", taskID)
		}
	}
	cold := sm.FindSubagentSessions(parent)
	if len(cold) != 3 {
		t.Fatalf("cold listing = %d entries, want 3 (%+v)", len(cold), cold)
	}
	for _, want := range resident {
		assertSubagentSame(t, "cold "+want.TaskID, want, subagentByTaskID(t, cold, want.TaskID))
	}

	// Phase 3: a fresh manager (restart) only has the store and the metadata
	// mirror to work from.
	restarted := NewSessionManager()
	restarted.SetSessionRepo(s.Sessions())
	afterRestart := restarted.FindSubagentSessions(parent)
	if len(afterRestart) != 3 {
		t.Fatalf("restart listing = %d entries, want 3 (%+v)", len(afterRestart), afterRestart)
	}
	for _, want := range resident {
		assertSubagentSame(t, "restart "+want.TaskID, want, subagentByTaskID(t, afterRestart, want.TaskID))
	}
}

// TestFindSubagentSessions_DoesNotLoadOrTouchLRU proves the read path stays
// read-only: it must not materialize the matching sessions (which is what fed
// the LRU thrash loop) and must not touch the access times or the resident set.
// maxInMemory is pinned to 1 so that any insert into sm.sessions would
// immediately evict the live chat session — the failure mode this guards.
func TestFindSubagentSessions_DoesNotLoadOrTouchLRU(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:client-noload"
	for i := 1; i <= 4; i++ {
		recordSubagent(t, sm, parent, fmt.Sprintf("subagent-%d", i), "answer")
	}
	chatKey := "native:live-chat"
	sm.AddMessage(chatKey, "user", "hello")
	if err := sm.Save(chatKey); err != nil {
		t.Fatalf("Save(%q): %v", chatKey, err)
	}

	// Subagents are done: evict them so the listing has to work from metadata.
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

	sm.SetMaxInMemory(1) // any load from here on evicts the live chat
	beforeResident := snapshotResident()
	beforeAccess := snapshotAccess()
	beforeMeta := snapshotMeta()

	results := sm.FindSubagentSessions(parent)
	if len(results) != 4 {
		t.Fatalf("listing = %d entries, want 4 (%+v)", len(results), results)
	}
	// The two fields that need the store were still resolved.
	for _, info := range results {
		if info.Iterations != 1 || info.Summary != "answer" {
			t.Errorf("%s: Iterations = %d, Summary = %q, want 1 and %q", info.TaskID, info.Iterations, info.Summary, "answer")
		}
	}

	afterResident := snapshotResident()
	if len(afterResident) != len(beforeResident) {
		t.Fatalf("resident sessions changed: %d → %d (listing must not load sessions)", len(beforeResident), len(afterResident))
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

// TestFindSubagentSessions_ColdCountMatchesFullLoad pins the store-side count
// against the implementation it replaced. When a session has evicted rows
// (first_in_memory_seq > 0) a cold load restores only the rows at/after the
// boundary, so the iteration count is the in-context assistant messages, not
// every assistant row: a naive COUNT(*) of the whole history would report 5
// where the load-based path reported 3.
func TestFindSubagentSessions_ColdCountMatchesFullLoad(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:client-boundary"
	key := parent + ":subagent-1"
	sm.AddMessage(key, "user", "task")
	for i := 0; i < 5; i++ {
		sm.AddMessage(key, "assistant", fmt.Sprintf("answer %d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save(%q): %v", key, err)
	}
	// A save persists the in-memory boundary, so evict first and only then
	// simulate the compaction that evicted rows 0..2 (user + 2 assistants).
	if !sm.EvictSession(key) {
		t.Fatal("EvictSession = false")
	}
	if err := s.Sessions().UpdateFirstInMemorySeq(key, 3); err != nil {
		t.Fatalf("UpdateFirstInMemorySeq: %v", err)
	}

	got := subagentByTaskID(t, sm.FindSubagentSessions(parent), "subagent-1")

	// Reference: the pre-change behaviour, computed from an explicit full load
	// of the same rows through the real load path.
	ref := NewSessionManager()
	ref.SetSessionRepo(s.Sessions())
	ref.ensureLoaded()
	ref.mu.Lock()
	loaded, ok := ref.loadSessionFromDisk(key)
	ref.mu.Unlock()
	if !ok {
		t.Fatal("reference loadSessionFromDisk failed")
	}
	refIterations := 0
	refSummary := loaded.Summary
	if refSummary == "" && len(loaded.Messages) > 0 {
		for i := len(loaded.Messages) - 1; i >= 0; i-- {
			if loaded.Messages[i].Role == "assistant" {
				refSummary = strings.TrimSpace(loaded.Messages[i].Content)
				break
			}
		}
	}
	for _, msg := range loaded.Messages {
		if msg.Role == "assistant" {
			refIterations++
		}
	}

	if refIterations != 3 {
		t.Fatalf("reference load reported %d assistant messages, want 3 (test premise)", refIterations)
	}
	if got.Iterations != refIterations {
		t.Errorf("Iterations = %d, want %d (cold load counted only in-context messages)", got.Iterations, refIterations)
	}
	if got.Summary != refSummary || refSummary != "answer 4" {
		t.Errorf("Summary = %q, want %q", got.Summary, refSummary)
	}

	// The full history really is larger: the listing must not be counting it.
	totalAssistants := 0
	all, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq: %v", err)
	}
	for _, row := range all {
		if strings.Contains(row.JSON, `"role":"assistant"`) {
			totalAssistants++
		}
	}
	if totalAssistants != 5 {
		t.Fatalf("persisted assistant rows = %d, want 5 (test premise)", totalAssistants)
	}
	if got.Iterations == totalAssistants {
		t.Errorf("Iterations = %d equals the full-history count: the eviction boundary was ignored", got.Iterations)
	}
}

// TestFindSubagentSessions_FallbackSummaryEdgeCases keeps the summary fallback
// byte-identical around the two edges the old inline code had: the fallback is
// the LAST assistant message even when its content is empty (no further search
// backwards), and long content is truncated to 200 bytes plus an ellipsis.
func TestFindSubagentSessions_FallbackSummaryEdgeCases(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:client-fallback"
	emptyLast := recordSubagent(t, sm, parent, "subagent-1", "previous answer exists", "")
	longLast := recordSubagent(t, sm, parent, "subagent-2", strings.Repeat("y", 500))

	resident := sm.FindSubagentSessions(parent)
	if got := subagentByTaskID(t, resident, "subagent-1").Summary; got != "" {
		t.Errorf("summary of a session whose last assistant message is empty = %q, want empty", got)
	}
	long := subagentByTaskID(t, resident, "subagent-2").Summary
	// 200 content bytes plus the 3-byte ellipsis rune.
	if len(long) != 203 || !strings.HasSuffix(long, "…") {
		t.Errorf("long summary = %d bytes (suffix %q), want 200 content bytes + ellipsis", len(long), long[len(long)-3:])
	}

	// The metadata-only path must produce the same two strings.
	sm.EvictSession(emptyLast)
	sm.EvictSession(longLast)
	cold := sm.FindSubagentSessions(parent)
	for _, want := range resident {
		assertSubagentSame(t, "cold "+want.TaskID, want, subagentByTaskID(t, cold, want.TaskID))
	}
}

// TestFindSubagentSessions_NoStore covers the store-less manager: resident
// sessions are still listed, and metadata-only keys (which cannot be read
// without a store) are omitted rather than reported as empty shells.
func TestFindSubagentSessions_NoStore(t *testing.T) {
	sm := NewSessionManager()
	parent := "native:client-nostore"
	sm.AddMessage(parent+":subagent-1", "user", "task")
	sm.AddMessage(parent+":subagent-1", "assistant", "done")

	results := sm.FindSubagentSessions(parent)
	if len(results) != 1 {
		t.Fatalf("listing = %d entries, want 1", len(results))
	}
	if results[0].Iterations != 1 || results[0].Summary != "done" {
		t.Errorf("entry = %+v, want 1 iteration and summary %q", results[0], "done")
	}
}

// storedMsgJSON renders a message the way providers.Message marshals into the
// session_messages.message column.
func storedMsgJSON(role, content string) string {
	return storedMessagePayload(role, content, false)
}

// storedMsgJSONExcluded renders a message flagged as excluded from context.
// Both the JSON flag and the row's `excluded` column carry that state
// (saveFullUnlocked derives the column from the field) and the cold load prunes
// on the flag, so a test reproducing evicted history has to set both.
func storedMsgJSONExcluded(role, content string) string {
	return storedMessagePayload(role, content, true)
}

func storedMessagePayload(role, content string, excluded bool) string {
	b, err := json.Marshal(struct {
		Role               string `json:"role"`
		Content            string `json:"content"`
		ExcludeFromContext bool   `json:"exclude_from_context,omitempty"`
	}{Role: role, Content: content, ExcludeFromContext: excluded})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// fullLoadListingReference reproduces what the pre-change listing reported for
// one session by running the real cold load: a throwaway manager loads the
// session from the same store and the assistant messages that ended up in
// memory are counted/summarised exactly as the load-based implementation did.
//
// It is the oracle for the parity tests below — the metadata-only path must
// report the same numbers without materialising anything.
func fullLoadListingReference(t *testing.T, s *store.Store, key string) (iterations int, summary string) {
	t.Helper()
	ref := NewSessionManager()
	ref.SetSessionRepo(s.Sessions())
	ref.ensureLoaded()
	ref.mu.Lock()
	loaded, ok := ref.loadSessionFromDisk(key)
	ref.mu.Unlock()
	if !ok {
		t.Fatalf("reference loadSessionFromDisk(%q) failed", key)
	}
	lastAssistant := ""
	for i := len(loaded.Messages) - 1; i >= 0; i-- {
		if loaded.Messages[i].Role == "assistant" {
			lastAssistant = loaded.Messages[i].Content
			break
		}
	}
	for _, msg := range loaded.Messages {
		if msg.Role == "assistant" {
			iterations++
		}
	}
	return iterations, subagentSummary(loaded.Summary, lastAssistant)
}

// persistRawSubagent writes a session row plus explicit message rows, so a
// test can reproduce stored shapes the regular API cannot produce in one step
// (a boundary left at 0 with excluded rows, a corrupt row, ...).
func persistRawSubagent(t *testing.T, s *store.Store, key string, boundary int, rows []store.MessageRow) {
	t.Helper()
	now := time.Now()
	if err := s.Sessions().UpsertSession(store.SessionMeta{
		Key:              key,
		Mode:             "agent",
		FirstInMemorySeq: boundary,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("UpsertSession(%q): %v", key, err)
	}
	if err := s.Sessions().ReplaceMessages(key, rows); err != nil {
		t.Fatalf("ReplaceMessages(%q): %v", key, err)
	}
}

// coldSubagentListing lists a parent's subagents through a manager that has
// neither the session nor (necessarily) its messages in memory — the path that
// has to consult the store.
func coldSubagentListing(t *testing.T, s *store.Store, parent string) []SubagentSessionInfo {
	t.Helper()
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	return sm.FindSubagentSessions(parent)
}

// TestFindSubagentSessions_ZeroBoundaryWithLeadingExcludedRun pins the
// boundary == 0 shape against the path it replaces. loadFromSQLite prunes a
// leading run of excluded rows when no boundary is recorded and rewrites the
// boundary, so those rows are not iterations of the restored session. A count
// that only filters on `seq >= first_in_memory_seq` reports them anyway
// (verified: 4 against the load path's 2).
func TestFindSubagentSessions_ZeroBoundaryWithLeadingExcludedRun(t *testing.T) {
	s := newTestStore(t)

	parent := "native:client-zero-boundary"
	key := parent + ":subagent-1"
	persistRawSubagent(t, s, key, 0, []store.MessageRow{
		{Seq: 0, Role: "user", JSON: storedMsgJSONExcluded("user", "superseded request"), Excluded: true},
		{Seq: 1, Role: "assistant", JSON: storedMsgJSONExcluded("assistant", "evicted 1"), Excluded: true},
		{Seq: 2, Role: "assistant", JSON: storedMsgJSONExcluded("assistant", "evicted 2"), Excluded: true},
		{Seq: 3, Role: "user", JSON: storedMsgJSON("user", "current request")},
		{Seq: 4, Role: "assistant", JSON: storedMsgJSON("assistant", "in context 1")},
		{Seq: 5, Role: "assistant", JSON: storedMsgJSON("assistant", "in context 2")},
	})

	// The listing runs BEFORE the reference load: loadSessionFromDisk rewrites
	// the persisted boundary, and the point of this test is the stored
	// boundary == 0 state.
	got := subagentByTaskID(t, coldSubagentListing(t, s, parent), "subagent-1")

	// A read-only listing must not have fixed the boundary up behind our back.
	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil {
		t.Fatalf("GetSessionMeta: %v", err)
	}
	if meta == nil || meta.FirstInMemorySeq != 0 {
		t.Fatalf("premise broken: stored boundary = %+v, want 0 (the listing must be read-only)", meta)
	}

	refIterations, refSummary := fullLoadListingReference(t, s, key)
	if refIterations != 2 {
		t.Fatalf("reference load reported %d assistant messages, want 2 (test premise)", refIterations)
	}
	if got.Iterations != refIterations {
		t.Errorf("Iterations = %d, want %d (the cold load prunes the excluded leading run)", got.Iterations, refIterations)
	}
	if got.Summary != refSummary || refSummary != "in context 2" {
		t.Errorf("Summary = %q, want %q", got.Summary, refSummary)
	}

	// The excluded rows really are assistant rows in SQLite: the listing must
	// not be counting "every assistant row", which would give 4.
	all, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq: %v", err)
	}
	assistantRows := 0
	for _, row := range all {
		if strings.Contains(row.JSON, `"role":"assistant"`) {
			assistantRows++
		}
	}
	if assistantRows != 4 {
		t.Fatalf("persisted assistant rows = %d, want 4 (test premise)", assistantRows)
	}
}

// TestFindSubagentSessions_CorruptRowMatchesFullLoad pins the other half of the
// parity claim: a message row whose JSON does not parse is skipped by the cold
// load ("skip corrupted messages"), so it must not be counted and must not
// shadow the summary fallback. Verified against the load path: it reports
// {iterations: 1, summary: "older ok"} while a plain COUNT(*) over the rows
// reports 2 and decodes the corrupt newest row to an empty summary.
func TestFindSubagentSessions_CorruptRowMatchesFullLoad(t *testing.T) {
	s := newTestStore(t)

	parent := "native:client-corrupt"
	key := parent + ":subagent-1"
	persistRawSubagent(t, s, key, 0, []store.MessageRow{
		{Seq: 0, Role: "user", JSON: storedMsgJSON("user", "task")},
		{Seq: 1, Role: "assistant", JSON: storedMsgJSON("assistant", "older ok")},
		// Truncated write: loadFromSQLite drops it.
		{Seq: 2, Role: "assistant", JSON: `{"role":"assistant","content":"trunc`},
	})

	got := subagentByTaskID(t, coldSubagentListing(t, s, parent), "subagent-1")

	refIterations, refSummary := fullLoadListingReference(t, s, key)
	if refIterations != 1 || refSummary != "older ok" {
		t.Fatalf("reference load = {iterations: %d, summary: %q}, want {1, \"older ok\"} (test premise)", refIterations, refSummary)
	}
	if got.Iterations != refIterations {
		t.Errorf("Iterations = %d, want %d (corrupt rows are not resident)", got.Iterations, refIterations)
	}
	if got.Summary != refSummary {
		t.Errorf("Summary = %q, want %q (the older, readable assistant row must stay the fallback)", got.Summary, refSummary)
	}

	// Same rows, session resident in memory: the two paths must agree.
	resident := NewSessionManager()
	resident.SetSessionRepo(s.Sessions())
	resident.ensureLoaded()
	resident.mu.Lock()
	if _, ok := resident.loadSessionFromDisk(key); !ok {
		resident.mu.Unlock()
		t.Fatal("loadSessionFromDisk failed")
	}
	resident.mu.Unlock()
	assertSubagentSame(t, "resident subagent-1", subagentByTaskID(t, resident.FindSubagentSessions(parent), "subagent-1"), got)
}
