package store

// Contract tests for SessionRepo.SessionListingStats, the batched read-only
// query that replaced the subagent listing endpoint's full session loads.
// Its job is to answer "how many assistant messages are in context, and what
// does the newest of them say" for a set of keys, with the same numbers a
// cold load of those sessions would report.

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// msgJSON builds a stored message payload in the shape providers.Message
// marshals to.
func msgJSON(role, content string) string {
	b, err := json.Marshal(struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{Role: role, Content: content})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestSessionListingStats_CountsSummaryAndLastAssistant(t *testing.T) {
	repo := newKeysRepo(t)

	if err := repo.UpsertSession(SessionMeta{
		Key:       "parent:subagent-1",
		Mode:      "agent",
		Summary:   "Found 3 files",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rows := []MessageRow{
		{Seq: 0, Role: "user", JSON: msgJSON("user", "task")},
		{Seq: 1, Role: "assistant", JSON: msgJSON("assistant", "answer one")},
		{Seq: 2, Role: "user", JSON: msgJSON("user", "follow up")},
		{Seq: 3, Role: "assistant", JSON: msgJSON("assistant", "answer two")},
	}
	if err := repo.ReplaceMessages("parent:subagent-1", rows); err != nil {
		t.Fatalf("replace messages: %v", err)
	}

	got, err := repo.SessionListingStats([]string{"parent:subagent-1"})
	if err != nil {
		t.Fatalf("SessionListingStats: %v", err)
	}
	stat, ok := got["parent:subagent-1"]
	if !ok {
		t.Fatalf("missing entry for parent:subagent-1 (got %v)", got)
	}
	if stat.AssistantCount != 2 {
		t.Errorf("AssistantCount = %d, want 2", stat.AssistantCount)
	}
	if stat.Summary != "Found 3 files" {
		t.Errorf("Summary = %q, want %q", stat.Summary, "Found 3 files")
	}
	if want := msgJSON("assistant", "answer two"); stat.LastAssistantJSON != want {
		t.Errorf("LastAssistantJSON = %s, want %s", stat.LastAssistantJSON, want)
	}
}

func TestSessionListingStats_EmptySessionRowIsReported(t *testing.T) {
	repo := newKeysRepo(t)

	// A session row with no messages must still come back (as an empty shell),
	// exactly like loading an empty session did. A key with no row at all must
	// not be invented.
	if err := repo.UpsertSession(SessionMeta{Key: "empty", Mode: "agent"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := repo.SessionListingStats([]string{"empty", "missing"})
	if err != nil {
		t.Fatalf("SessionListingStats: %v", err)
	}
	stat, ok := got["empty"]
	if !ok {
		t.Fatal("session row without messages must be reported")
	}
	if stat.AssistantCount != 0 || stat.Summary != "" || stat.LastAssistantJSON != "" {
		t.Errorf("empty session stats = %+v, want zero values", stat)
	}
	if _, ok := got["missing"]; ok {
		t.Error("key without a sessions row must be absent from the result")
	}
}

func TestSessionListingStats_HonoursEvictionBoundary(t *testing.T) {
	repo := newKeysRepo(t)

	// Rows below sessions.first_in_memory_seq are evicted from memory, so a
	// cold load only restores seq >= boundary. Counting the full history here
	// would inflate the iteration count the listing reports.
	if err := repo.UpsertSession(SessionMeta{
		Key:              "bounded",
		Mode:             "agent",
		FirstInMemorySeq: 3,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := repo.ReplaceMessages("bounded", []MessageRow{
		{Seq: 0, Role: "assistant", JSON: msgJSON("assistant", "evicted 1"), Excluded: true},
		{Seq: 1, Role: "assistant", JSON: msgJSON("assistant", "evicted 2"), Excluded: true},
		{Seq: 2, Role: "assistant", JSON: msgJSON("assistant", "evicted 3"), Excluded: true},
		{Seq: 3, Role: "assistant", JSON: msgJSON("assistant", "in context 1")},
		{Seq: 4, Role: "assistant", JSON: msgJSON("assistant", "in context 2")},
	}); err != nil {
		t.Fatalf("replace messages: %v", err)
	}

	got, err := repo.SessionListingStats([]string{"bounded"})
	if err != nil {
		t.Fatalf("SessionListingStats: %v", err)
	}
	stat := got["bounded"]
	if stat.AssistantCount != 2 {
		t.Errorf("AssistantCount = %d, want 2 (only rows at/after the boundary)", stat.AssistantCount)
	}
	if want := msgJSON("assistant", "in context 2"); stat.LastAssistantJSON != want {
		t.Errorf("LastAssistantJSON = %s, want %s", stat.LastAssistantJSON, want)
	}
}

func TestSessionListingStats_NoAssistantMessages(t *testing.T) {
	repo := newKeysRepo(t)

	if err := repo.UpsertSession(SessionMeta{Key: "users-only", Mode: "agent"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := repo.ReplaceMessages("users-only", []MessageRow{
		{Seq: 0, Role: "user", JSON: msgJSON("user", "hi")},
		{Seq: 1, Role: "user", JSON: msgJSON("user", "still waiting")},
	}); err != nil {
		t.Fatalf("replace messages: %v", err)
	}

	got, err := repo.SessionListingStats([]string{"users-only"})
	if err != nil {
		t.Fatalf("SessionListingStats: %v", err)
	}
	stat := got["users-only"]
	if stat.AssistantCount != 0 {
		t.Errorf("AssistantCount = %d, want 0", stat.AssistantCount)
	}
	if stat.LastAssistantJSON != "" {
		t.Errorf("LastAssistantJSON = %q, want empty", stat.LastAssistantJSON)
	}
}

// TestSessionListingStats_ChunksLargeKeySets covers the batching: more keys
// than one statement can carry must still be answered completely and keep the
// per-key association intact (an off-by-one in the chunk slicing would show up
// as shifted counts).
func TestSessionListingStats_ChunksLargeKeySets(t *testing.T) {
	repo := newKeysRepo(t)

	const total = sessionListingStatsChunk + 5
	keys := make([]string, 0, total)
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("chunk:%d", i)
		keys = append(keys, key)
		if err := repo.UpsertSession(SessionMeta{Key: key, Mode: "agent"}); err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		rows := make([]MessageRow, 0, i+1)
		for seq := 0; seq <= i; seq++ {
			rows = append(rows, MessageRow{
				Seq:  seq,
				Role: "assistant",
				JSON: msgJSON("assistant", fmt.Sprintf("reply %d", seq)),
			})
		}
		if err := repo.ReplaceMessages(key, rows); err != nil {
			t.Fatalf("replace %s: %v", key, err)
		}
	}

	got, err := repo.SessionListingStats(keys)
	if err != nil {
		t.Fatalf("SessionListingStats: %v", err)
	}
	if len(got) != total {
		t.Fatalf("result has %d entries, want %d", len(got), total)
	}
	for i, key := range keys {
		stat := got[key]
		if stat.AssistantCount != i+1 {
			t.Errorf("%s: AssistantCount = %d, want %d", key, stat.AssistantCount, i+1)
		}
		if want := msgJSON("assistant", fmt.Sprintf("reply %d", i)); stat.LastAssistantJSON != want {
			t.Errorf("%s: LastAssistantJSON = %s, want %s", key, stat.LastAssistantJSON, want)
		}
	}
}

func TestSessionListingStats_NoKeys(t *testing.T) {
	repo := newKeysRepo(t)

	got, err := repo.SessionListingStats(nil)
	if err != nil {
		t.Fatalf("SessionListingStats(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("result = %v, want empty", got)
	}
}

// TestSessionListingStats_ZeroBoundaryWithLeadingExcludedRun covers the second
// stored shape of evicted history: sessions.first_in_memory_seq is still 0
// while the first rows carry excluded = 1. loadFromSQLite does not ignore that
// case — when boundary == 0 it prunes the leading excluded run and persists
// the new boundary — so those rows are NOT resident after a cold load and must
// not be counted here.
func TestSessionListingStats_ZeroBoundaryWithLeadingExcludedRun(t *testing.T) {
	repo := newKeysRepo(t)

	// FirstInMemorySeq is left at its default 0 on purpose: this is the shape
	// between a compaction that only flagged rows (no eviction yet) and the
	// next cold load.
	if err := repo.UpsertSession(SessionMeta{
		Key:  "zero-boundary",
		Mode: "agent",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := repo.ReplaceMessages("zero-boundary", []MessageRow{
		{Seq: 0, Role: "user", JSON: msgJSON("user", "superseded request"), Excluded: true},
		{Seq: 1, Role: "assistant", JSON: msgJSON("assistant", "evicted answer 1"), Excluded: true},
		{Seq: 2, Role: "assistant", JSON: msgJSON("assistant", "evicted answer 2"), Excluded: true},
		{Seq: 3, Role: "user", JSON: msgJSON("user", "current request")},
		{Seq: 4, Role: "assistant", JSON: msgJSON("assistant", "in context 1")},
		{Seq: 5, Role: "assistant", JSON: msgJSON("assistant", "in context 2")},
	}); err != nil {
		t.Fatalf("replace messages: %v", err)
	}

	got, err := repo.SessionListingStats([]string{"zero-boundary"})
	if err != nil {
		t.Fatalf("SessionListingStats: %v", err)
	}
	stat := got["zero-boundary"]
	if stat.AssistantCount != 2 {
		t.Errorf("AssistantCount = %d, want 2 (the excluded leading run is not resident)", stat.AssistantCount)
	}
	if want := msgJSON("assistant", "in context 2"); stat.LastAssistantJSON != want {
		t.Errorf("LastAssistantJSON = %s, want %s", stat.LastAssistantJSON, want)
	}
}

// TestSessionListingStats_SkipsCorruptAssistantRows pins the "skip corrupted
// messages" half of the parity claim: a row whose JSON does not parse never
// becomes a resident message, so it must neither inflate the count nor shadow
// the summary fallback by being the newest assistant row.
func TestSessionListingStats_SkipsCorruptAssistantRows(t *testing.T) {
	repo := newKeysRepo(t)

	if err := repo.UpsertSession(SessionMeta{
		Key:  "corrupt",
		Mode: "agent",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := repo.ReplaceMessages("corrupt", []MessageRow{
		{Seq: 0, Role: "user", JSON: msgJSON("user", "task")},
		{Seq: 1, Role: "assistant", JSON: msgJSON("assistant", "older ok")},
		// Truncated write: not parseable as JSON at all.
		{Seq: 2, Role: "assistant", JSON: `{"role":"assistant","content":"trunc`},
	}); err != nil {
		t.Fatalf("replace messages: %v", err)
	}

	got, err := repo.SessionListingStats([]string{"corrupt"})
	if err != nil {
		t.Fatalf("SessionListingStats: %v", err)
	}
	stat := got["corrupt"]
	if stat.AssistantCount != 1 {
		t.Errorf("AssistantCount = %d, want 1 (the loader skips the corrupt row)", stat.AssistantCount)
	}
	if want := msgJSON("assistant", "older ok"); stat.LastAssistantJSON != want {
		t.Errorf("LastAssistantJSON = %s, want %s (the corrupt newest row must not shadow it)", stat.LastAssistantJSON, want)
	}
}
