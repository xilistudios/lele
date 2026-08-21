package store

import (
	"database/sql"
	"reflect"
	"testing"
	"time"
)

// seedSessionWithMessages upserts a session and inserts the given messages,
// returning the repo. It is a convenience helper for the update tests.
func seedSessionWithMessages(t *testing.T, repo *SessionRepo, key string, messages []MessageRow) {
	t.Helper()
	now := time.Now()
	if err := repo.UpsertSession(SessionMeta{Key: key, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("UpsertSession(%q) failed: %v", key, err)
	}
	if err := repo.InsertMessages(key, messages); err != nil {
		t.Fatalf("InsertMessages(%q) failed: %v", key, err)
	}
}

// mustUpdateMessage eases the common "update then assert" pattern.
func mustUpdateMessage(t *testing.T, repo *SessionRepo, key string, seq int, expectedJSON string) string {
	t.Helper()
	msgs, err := repo.LoadMessages(key)
	if err != nil {
		t.Fatalf("LoadMessages(%q) failed: %v", key, err)
	}
	if seq < 0 || seq >= len(msgs) {
		t.Fatalf("seq %d out of range for %d messages", seq, len(msgs))
	}
	return msgs[seq]
}

func TestSessionRepo_UpdateMessages(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	key := "test:update-messages"
	if err := repo.UpsertSession(SessionMeta{Key: key, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertSession() failed: %v", err)
	}
	seed := []MessageRow{
		{Seq: 0, Role: "user", JSON: `{"role":"user","content":"original0"}`},
		{Seq: 1, Role: "assistant", JSON: `{"role":"assistant","content":"original1"}`},
		{Seq: 2, Role: "user", JSON: `{"role":"user","content":"original2"}`},
	}
	if err := repo.InsertMessages(key, seed); err != nil {
		t.Fatalf("InsertMessages() failed: %v", err)
	}

	// Update two of the three messages including role + JSON + excluded flag.
	updates := []MessageRow{
		{Seq: 1, Role: "assistant", JSON: `{"role":"assistant","content":"final1","tool_calls":[]}`, Excluded: true},
		{Seq: 2, Role: "user", JSON: `{"role":"user","content":"final2"}`, Excluded: false},
	}
	if err := repo.UpdateMessages(key, updates); err != nil {
		t.Fatalf("UpdateMessages() failed: %v", err)
	}

	// Verifies payloads changed.
	got := mustUpdateMessage(t, repo, key, 1, "")
	if got != updates[0].JSON {
		t.Errorf("seq1 = %q, want %q", got, updates[0].JSON)
	}
	got = mustUpdateMessage(t, repo, key, 2, "")
	if got != updates[1].JSON {
		t.Errorf("seq2 = %q, want %q", got, updates[1].JSON)
	}

	// The updated seq=1 message must now be excluded.
	rows, err := repo.LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq() failed: %v", err)
	}
	for _, r := range rows {
		switch r.Seq {
		case 1:
			if !r.Excluded {
				t.Errorf("seq1 Excluded = false, want true (updated with excluded flag)")
			}
		case 2:
			if r.Excluded {
				t.Errorf("seq2 Excluded = true, want false (updated with excluded=false)")
			}
		default:
			if r.Excluded {
				t.Errorf("seq%d Excluded = true, want false (untouched)", r.Seq)
			}
		}
	}

	// The untouched message 0 is still present.
	if got := mustUpdateMessage(t, repo, key, 0, `{"role":"user","content":"original0"}`); got != `{"role":"user","content":"original0"}` {
		t.Errorf("seq0 = %q, want original0", got)
	}
}

func TestSessionRepo_UpdateMessages_EmptyShortCircuit(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	// Empty slice must be a no-op (no error).
	if err := repo.UpdateMessages("missing-session", nil); err != nil {
		t.Fatalf("UpdateMessages(nil) failed: %v", err)
	}
	if err := repo.UpdateMessages("missing-session", []MessageRow{}); err != nil {
		t.Fatalf("UpdateMessages(empty) failed: %v", err)
	}
}

func TestSessionRepo_UpdateMessagesExcluded(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	key := "test:update-excluded"
	seed := []MessageRow{
		{Seq: 0, Role: "user", JSON: `{"role":"user","content":"m0"}`, Excluded: false},
		{Seq: 1, Role: "assistant", JSON: `{"role":"assistant","content":"m1"}`, Excluded: false},
		{Seq: 2, Role: "user", JSON: `{"role":"user","content":"m2"}`, Excluded: false},
		{Seq: 3, Role: "assistant", JSON: `{"role":"assistant","content":"m3"}`, Excluded: false},
		{Seq: 4, Role: "user", JSON: `{"role":"user","content":"m4"}`, Excluded: false},
	}
	seedSessionWithMessages(t, repo, key, seed)

	// Exclude [fromSeq=0, toSeq=3) — marks seq 0,1,2 as excluded.
	if err := repo.UpdateMessagesExcluded(key, 0, 3, true); err != nil {
		t.Fatalf("UpdateMessagesExcluded(0,3,true) failed: %v", err)
	}

	// Re-count excluded; want 3.
	count, err := repo.CountExcludedMessages(key)
	if err != nil {
		t.Fatalf("CountExcludedMessages() failed: %v", err)
	}
	if count != 3 {
		t.Errorf("excluded count = %d, want 3", count)
	}

	// Clear excluded for [fromSeq=1, toSeq=4) — marks seq 1,2,3 as not excluded,
	// leaving seq 0 excluded.
	if err := repo.UpdateMessagesExcluded(key, 1, 4, false); err != nil {
		t.Fatalf("UpdateMessagesExcluded(1,4,false) failed: %v", err)
	}
	count, _ = repo.CountExcludedMessages(key)
	if count != 1 {
		t.Errorf("excluded count after unset = %d, want 1", count)
	}

	// Verify exact flags with LoadMessagesWithSeq.
	rows, err := repo.LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq() failed: %v", err)
	}
	wantExcluded := map[int]bool{0: true, 1: false, 2: false, 3: false, 4: false}
	if !flagsMatch(rows, wantExcluded) {
		t.Errorf("excluded flags = %v", flagsString(rows, wantExcluded))
	}

	// Unaffected session key: no-op, no error.
	if err := repo.UpdateMessagesExcluded("missing", 0, 5, true); err != nil {
		t.Fatalf("UpdateMessagesExcluded(missing) failed: %v", err)
	}
}

func TestSessionRepo_UpdateMessagesExcludedWithJSON(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	key := "test:update-excluded-json"
	seed := []MessageRow{
		{Seq: 0, Role: "user", JSON: `{"role":"user","content":"m0"}`, Excluded: false},
		{Seq: 1, Role: "assistant", JSON: `{"role":"assistant","content":"m1"}`, Excluded: false},
		{Seq: 2, Role: "user", JSON: `{"role":"user","content":"m2"}`, Excluded: false},
		{Seq: 3, Role: "assistant", JSON: `{"role":"assistant","content":"m3"}`, Excluded: false},
	}
	seedSessionWithMessages(t, repo, key, seed)

	// Batch update: toggle excluded + new JSON on messages 0 and 3.
	updates := []MessageRow{
		{Seq: 0, Role: "user", JSON: `{"role":"user","content":"new0"}`, Excluded: true},
		{Seq: 3, Role: "assistant", JSON: `{"role":"assistant","content":"new3"}`, Excluded: true},
	}
	if err := repo.UpdateMessagesExcludedWithJSON(key, updates); err != nil {
		t.Fatalf("UpdateMessagesExcludedWithJSON() failed: %v", err)
	}

	// Payloads updated.
	got := mustUpdateMessage(t, repo, key, 0, "")
	if got != updates[0].JSON {
		t.Errorf("seq0 = %q, want %q", got, updates[0].JSON)
	}
	got = mustUpdateMessage(t, repo, key, 3, "")
	if got != updates[1].JSON {
		t.Errorf("seq3 = %q, want %q", got, updates[1].JSON)
	}

	// Excluded flags updated only on the specified rows.
	rows, err := repo.LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq() failed: %v", err)
	}
	wantExcluded := map[int]bool{0: true, 1: false, 2: false, 3: true}
	if !flagsMatch(rows, wantExcluded) {
		t.Errorf("excluded flags = %v", flagsString(rows, wantExcluded))
	}
}

func TestSessionRepo_UpdateMessagesExcludedWithJSON_EmptyShortCircuit(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	if err := repo.UpdateMessagesExcludedWithJSON("missing", nil); err != nil {
		t.Fatalf("UpdateMessagesExcludedWithJSON(nil) failed: %v", err)
	}
	if err := repo.UpdateMessagesExcludedWithJSON("missing", []MessageRow{}); err != nil {
		t.Fatalf("UpdateMessagesExcludedWithJSON(empty) failed: %v", err)
	}
}

// flagsMatch reports whether the excluded flags in rows equal want for every
// seq present in want.
func flagsMatch(rows []MessageRowFull, want map[int]bool) bool {
	for _, r := range rows {
		expected, ok := want[r.Seq]
		if !ok {
			continue
		}
		if r.Excluded != expected {
			return false
		}
	}
	return true
}

// flagsString pretty-prints the actual excluded flags for diagnostics.
func flagsString(rows []MessageRowFull, want map[int]bool) string {
	var result []int
	for _, r := range rows {
		if _, ok := want[r.Seq]; !ok {
			continue
		}
		got := 0
		if r.Excluded {
			got = 1
		}
		result = append(result, got)
	}
	return "excluded flags=" + func() string {
		out := ""
		for i := 0; i < len(result); i++ {
			if i > 0 {
				out += ","
			}
			out += string(rune('0' + result[i]))
		}
		return out
	}()
}

func TestSessionRepo_UpdateMessage_ExcludedFlag(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	key := "test:update-message-flag"
	seedSessionWithMessages(t, repo, key, []MessageRow{
		{Seq: 0, Role: "user", JSON: `{"role":"user","content":"m0"}`, Excluded: false},
	})

	// UpdateMessage loops over the true/false excluded branches.
	if err := repo.UpdateMessage(key, 0, "user", `{"role":"user","content":"m0x"}`, true); err != nil {
		t.Fatalf("UpdateMessage(...,true) failed: %v", err)
	}
	count, _ := repo.CountExcludedMessages(key)
	if count != 1 {
		t.Errorf("excluded count = %d, want 1 (after exact=true)", count)
	}

	if err := repo.UpdateMessage(key, 0, "user", `{"role":"user","content":"m0y"}`, false); err != nil {
		t.Fatalf("UpdateMessage(...,false) failed: %v", err)
	}
	count, _ = repo.CountExcludedMessages(key)
	if count != 0 {
		t.Errorf("excluded count = %d, want 0 (after exact=false)", count)
	}
}

func TestSessionRepo_InsertMessage_ExcludedPaths(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	key := "test:insert-excluded"
	seedSessionWithMessages(t, repo, key, nil)

	// Upsert path - excluded=true sets column to 1.
	if err := repo.InsertMessage(key, 0, "user", `{"role":"user","content":"a"}`, true); err != nil {
		t.Fatalf("InsertMessage(...,true) failed: %v", err)
	}
	// REPLACE path - excluded=false overwrites existing seq 0.
	if err := repo.InsertMessage(key, 0, "user", `{"role":"user","content":"b"}`, false); err != nil {
		t.Fatalf("InsertMessage(...,false) failed: %v", err)
	}
	count, _ := repo.CountExcludedMessages(key)
	if count != 0 {
		t.Errorf("excluded count = %d, want 0", count)
	}

	// InsertMessages with mixed Excluded flags (true branch).
	if err := repo.InsertMessages(key, []MessageRow{
		{Seq: 1, Role: "assistant", JSON: `{"role":"assistant","content":"c"}`, Excluded: true},
		{Seq: 2, Role: "user", JSON: `{"role":"user","content":"d"}`, Excluded: false},
	}); err != nil {
		t.Fatalf("InsertMessages(mixed) failed: %v", err)
	}
	count, _ = repo.CountExcludedMessages(key)
	if count != 1 {
		t.Errorf("excluded count = %d, want 1", count)
	}

	// ReplaceMessages handles both excluded branches.
	if err := repo.ReplaceMessages(key, []MessageRow{
		{Seq: 0, Role: "user", JSON: `{"role":"user","content":"r0"}`, Excluded: true},
		{Seq: 1, Role: "assistant", JSON: `{"role":"assistant","content":"r1"}`, Excluded: false},
	}); err != nil {
		t.Fatalf("ReplaceMessages() failed: %v", err)
	}
	count, _ = repo.CountExcludedMessages(key)
	if count != 1 {
		t.Errorf("excluded count after replace = %d, want 1", count)
	}
}

func TestSessionRepo_InsertMessages_EmptyShortCircuit(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	if err := repo.InsertMessages("missing", nil); err != nil {
		t.Fatalf("InsertMessages(nil) failed: %v", err)
	}
}

func TestSessionRepo_UpdateMessages_RoundTripAgainstRows(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	key := "test:update-roundtrip"
	seedSessionWithMessages(t, repo, key, []MessageRow{
		{Seq: 0, Role: "user", JSON: `{"role":"user","content":"a"}`, Excluded: false},
		{Seq: 1, Role: "assistant", JSON: `{"role":"assistant","content":"b"}`, Excluded: true},
	})

	// Update seq 1's role and JSON.
	if err := repo.UpdateMessages(key, []MessageRow{
		{Seq: 1, Role: "assistant", JSON: `{"role":"assistant","content":"B2"}`, Excluded: true},
	}); err != nil {
		t.Fatalf("UpdateMessages() failed: %v", err)
	}

	rows, err := repo.LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq() failed: %v", err)
	}
	want := []MessageRowFull{
		{Seq: 0, JSON: `{"role":"user","content":"a"}`, Excluded: false},
		{Seq: 1, JSON: `{"role":"assistant","content":"B2"}`, Excluded: true},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("LoadMessagesWithSeq() = %+v, want %+v", rows, want)
	}
}

// TestSessionRepo_UpdateMessages_NilDB verifies the error path for
// UpdateMessages when the underlying connection is closed.
func TestSessionRepo_UpdateMessages_NilDB(t *testing.T) {
	path := t.TempDir() + "/closed.db"
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	repo := s.Sessions()

	// Begin on a closed DB fails.
	if err := repo.UpdateMessages("key", []MessageRow{{Seq: 0}}); err == nil {
		t.Error("UpdateMessages() on closed DB returned nil error, want error")
	}
	if err := repo.UpdateMessagesExcludedWithJSON("key", []MessageRow{{Seq: 0}}); err == nil {
		t.Error("UpdateMessagesExcludedWithJSON() on closed DB returned nil error, want error")
	}
}

func TestSessionRepo_OpenClosed_Error(t *testing.T) {
	path := t.TempDir() + "/closed.db"
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// MaxSeq on closed DB yields an error path (sql.ErrConnDone path).
	repo := s.Sessions()
	if _, err := repo.MaxSeq("k"); err == nil {
		t.Error("MaxSeq() on closed DB returned nil error, want error")
	}
	if _, err := repo.MessageCount("k"); err == nil {
		t.Error("MessageCount() on closed DB returned nil error, want error")
	}
}

func TestSessionRepo_CreatedAtParsing(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	key := "test:created-at"
	now := time.Now().Truncate(time.Nanosecond).UTC()
	// Use a weird-but-parseable timestamp layout via a direct insert to ensure
	// parse of RFC3339Nano works (GetSessionMeta uses time.Parse).
	if err := repo.UpsertSession(SessionMeta{Key: key, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("UpsertSession() failed: %v", err)
	}
	meta, err := repo.GetSessionMeta(key)
	if err != nil {
		t.Fatalf("GetSessionMeta() failed: %v", err)
	}
	if meta == nil {
		t.Fatal("GetSessionMeta() = nil, want non-nil")
	}
	if meta.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero, want parsed non-zero time")
	}
	if meta.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero, want parsed non-zero time")
	}
}

// sqliteNilDB returns a *sql.DB handle with no driver attached to force
// query errors. This exercises ListSessionMeta/LoadMessages error paths.
func sqliteNilDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open(invalid) failed: %v", err)
	}
	// Forced close to make queries fail.
	_ = db.Close()
	return db
}

func TestSessionRepo_QueryErrorPaths(t *testing.T) {
	repo := &SessionRepo{db: sqliteNilDB(t)}

	if _, err := repo.ListSessionMeta(); err == nil {
		t.Error("ListSessionMeta() on closed DB returned nil error")
	}
	if _, err := repo.ListSessionMetaByMode("agent"); err == nil {
		t.Error("ListSessionMetaByMode() on closed DB returned nil error")
	}
	if _, err := repo.LoadMessages("k"); err == nil {
		t.Error("LoadMessages() on closed DB returned nil error")
	}
	if _, err := repo.LoadMessagesBeforeSeq("k", 1); err == nil {
		t.Error("LoadMessagesBeforeSeq() on closed DB returned nil error")
	}
	if _, err := repo.LoadMessagesFromSeq("k", 1); err == nil {
		t.Error("LoadMessagesFromSeq() on closed DB returned nil error")
	}
	if _, err := repo.LoadMessagesWithSeq("k"); err == nil {
		t.Error("LoadMessagesWithSeq() on closed DB returned nil error")
	}
	if _, err := repo.CountExcludedMessages("k"); err == nil {
		t.Error("CountExcludedMessages() on closed DB returned nil error")
	}
	if _, err := repo.AllMessageCounts(); err == nil {
		t.Error("AllMessageCounts() on closed DB returned nil error")
	}
	if _, err := repo.PruneExcluded("k", 1); err == nil {
		t.Error("PruneExcluded() on closed DB returned nil error")
	}
	if _, err := repo.GetSessionMeta("k"); err == nil {
		t.Error("GetSessionMeta() on closed DB returned nil error")
	}
}

func TestSessionRepo_ExecErrorPaths(t *testing.T) {
	repo := &SessionRepo{db: sqliteNilDB(t)}

	if err := repo.UpsertSession(SessionMeta{Key: "k"}); err == nil {
		t.Error("UpsertSession() on closed DB returned nil error")
	}
	if err := repo.DeleteSession("k"); err == nil {
		t.Error("DeleteSession() on closed DB returned nil error")
	}
	if err := repo.ReplaceMessages("k", []MessageRow{{Seq: 0}}); err == nil {
		t.Error("ReplaceMessages() on closed DB returned nil error")
	}
	if err := repo.InsertMessage("k", 0, "user", "{}", false); err == nil {
		t.Error("InsertMessage() on closed DB returned nil error")
	}
	if err := repo.InsertMessages("k", []MessageRow{{Seq: 0}}); err == nil {
		t.Error("InsertMessages() on closed DB returned nil error")
	}
	if err := repo.UpdateMessage("k", 0, "user", "{}", false); err == nil {
		t.Error("UpdateMessage() on closed DB returned nil error")
	}
	if err := repo.UpdateMessages("k", []MessageRow{{Seq: 0}}); err == nil {
		t.Error("UpdateMessages() on closed DB returned nil error")
	}
	if err := repo.UpdateMessagesExcluded("k", 0, 1, true); err == nil {
		t.Error("UpdateMessagesExcluded() on closed DB returned nil error")
	}
	if err := repo.UpdateMessagesExcludedWithJSON("k", []MessageRow{{Seq: 0}}); err == nil {
		t.Error("UpdateMessagesExcludedWithJSON() on closed DB returned nil error")
	}
	if _, err := repo.DeleteLastMessage("k"); err == nil {
		t.Error("DeleteLastMessage() on closed DB returned nil error")
	}
	if err := repo.DeleteMessagesFrom("k", 0); err == nil {
		t.Error("DeleteMessagesFrom() on closed DB returned nil error")
	}
	if err := repo.UpdateFirstInMemorySeq("k", 0); err == nil {
		t.Error("UpdateFirstInMemorySeq() on closed DB returned nil error")
	}
}