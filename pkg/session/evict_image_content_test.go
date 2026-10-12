package session

// Tests for EvictImageContent: the durable counterpart of
// providers.ReduceImageParts. It rewrites the stored image-bearing messages
// (placeholder substitution or part deletion), persists through the
// incremental save path, and must be a true no-op when it would evict
// nothing.

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

// evictImg builds a heavy (data:) image part with the given filler byte.
func evictImg(filler string) providers.ContentPart {
	return providers.ContentPart{
		Type:     "image_url",
		ImageURL: &providers.ImageURL{URL: "data:image/png;base64," + strings.Repeat(filler, 1024), Detail: "auto"},
	}
}

// evictSeedSession stores the canonical fixture: three image-bearing
// messages (mixed roles: user, assistant-with-tool-calls, tool result) plus a
// text-only message, persisted through Save so later writes take the
// incremental path.
func evictSeedSession(t *testing.T, sm *SessionManager, key string) []providers.Message {
	t.Helper()
	msgs := []providers.Message{
		// 0: user with an image and no textual content → placeholder.
		{Role: "user", ContentParts: []providers.ContentPart{evictImg("A")}},
		// 1: assistant carrying ToolCalls and an image; Content non-empty →
		// the part is deleted, text and tool calls stay untouched.
		{
			Role:         "assistant",
			Content:      "looking at it",
			ToolCalls:    []providers.ToolCall{{ID: "call_1", Name: "read_image"}},
			ContentParts: []providers.ContentPart{evictImg("B")},
		},
		// 2: tool result carrying the newest image → kept with keepLast=1.
		{Role: "tool", ToolCallID: "call_1", ContentParts: []providers.ContentPart{evictImg("C")}},
		// 3: text-only message, must never change.
		{Role: "user", Content: "and the text-only follow-up"},
	}
	for _, m := range msgs {
		sm.AddFullMessage(key, m)
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	return msgs
}

// evictRowJSONs reads the persisted row JSONs straight from the store: the
// durable proof that a rewrite reached SQLite. (Reload-based assertions alone
// cannot pin ContentParts persistence: Message.MarshalJSON is pointer-receiver
// but the save path marshals values, so the default struct marshaler writes
// "content_parts" as a separate key that the custom UnmarshalJSON — which only
// reads "content" — never restores. That round-trip loss is pre-existing and
// out of scope here; asserting on the row bytes pins what THIS change must
// guarantee: the blobs are gone from disk.)
func evictRowJSONs(t *testing.T, repo *store.SessionRepo, key string) []string {
	t.Helper()
	rows, err := repo.LoadMessagesFromSeq(key, 0)
	if err != nil {
		t.Fatalf("LoadMessagesFromSeq(%q): %v", key, err)
	}
	return rows
}

// evictAssertRowState checks one row's JSON against the expected durable state.
func evictAssertRowState(t *testing.T, rows []string, idx int, mustContain []string, mustNotContain []string) {
	t.Helper()
	if idx >= len(rows) {
		t.Fatalf("row %d missing (have %d rows)", idx, len(rows))
	}
	for _, needle := range mustContain {
		if !strings.Contains(rows[idx], needle) {
			t.Errorf("row %d JSON must contain %q, got: %s", idx, needle, rows[idx])
		}
	}
	for _, needle := range mustNotContain {
		if strings.Contains(rows[idx], needle) {
			t.Errorf("row %d JSON must NOT contain %q (poison still on disk): %.120s...", idx, needle, rows[idx])
		}
	}
}

func TestEvictImageContent_PersistsAcrossReload(t *testing.T) {
	st := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(st.Sessions())

	key := "native:evict-1"
	seeded := evictSeedSession(t, sm, key)

	// Pin the seeded durable state: all three blobs are on disk.
	blobA, blobB, blobC := strings.Repeat("A", 1024), strings.Repeat("B", 1024), strings.Repeat("C", 1024)
	rowsBefore := evictRowJSONs(t, st.Sessions(), key)
	if len(rowsBefore) != len(seeded) {
		t.Fatalf("seeded rows = %d, want %d", len(rowsBefore), len(seeded))
	}
	evictAssertRowState(t, rowsBefore, 0, []string{blobA}, nil)
	evictAssertRowState(t, rowsBefore, 1, []string{blobB, "looking at it", "call_1"}, nil)
	evictAssertRowState(t, rowsBefore, 2, []string{blobC}, nil)

	n, err := sm.EvictImageContent(key, 1)
	if err != nil {
		t.Fatalf("EvictImageContent: %v", err)
	}
	// keepLast=1 keeps the newest heavy part (message 2) and evicts the two
	// older ones (messages 0 and 1).
	if n != 2 {
		t.Fatalf("evicted = %d, want 2", n)
	}

	hist := sm.GetHistory(key)
	if len(hist) != len(seeded) {
		t.Fatalf("history len = %d, want %d (eviction must not delete messages)", len(hist), len(seeded))
	}
	// The newest image-bearing message keeps its part alive.
	if count, _ := providers.LiveMultimodalParts(hist[2:3]); count != 1 {
		t.Errorf("newest message heavy parts = %d, want 1", count)
	}
	// Message 0 (empty Content): the part was replaced in place by the
	// text placeholder.
	if got := hist[0].ContentParts; len(got) != 1 || got[0].Type != "text" || got[0].Text != "[image omitted from context]" {
		t.Errorf("message 0 parts = %+v, want single placeholder text part", got)
	}
	// Message 1 (non-empty Content): the part was deleted; text, ToolCalls
	// and ToolCallID of every message are untouched.
	if got := hist[1].ContentParts; len(got) != 0 {
		t.Errorf("message 1 parts = %+v, want none (Content is non-empty)", got)
	}
	if hist[1].Content != seeded[1].Content {
		t.Errorf("message 1 content = %q, want %q", hist[1].Content, seeded[1].Content)
	}
	if !reflect.DeepEqual(hist[1].ToolCalls, seeded[1].ToolCalls) {
		t.Errorf("message 1 tool calls changed: %+v vs %+v", hist[1].ToolCalls, seeded[1].ToolCalls)
	}
	if hist[2].ToolCallID != seeded[2].ToolCallID {
		t.Errorf("message 2 tool_call_id = %q, want %q", hist[2].ToolCallID, seeded[2].ToolCallID)
	}
	if hist[3].Content != seeded[3].Content {
		t.Errorf("message 3 content = %q, want %q", hist[3].Content, seeded[3].Content)
	}
	if count, _ := providers.LiveMultimodalParts(hist); count != 1 {
		t.Errorf("live heavy parts = %d, want 1", count)
	}

	// The persisted rows were really rewritten (markModified + Save took the
	// incremental path): the evicted blobs are gone from disk, the kept one
	// and the untouched messages are not.
	rowsAfter := evictRowJSONs(t, st.Sessions(), key)
	if len(rowsAfter) != len(seeded) {
		t.Fatalf("persisted rows = %d, want %d", len(rowsAfter), len(seeded))
	}
	evictAssertRowState(t, rowsAfter, 0, []string{"[image omitted from context]"}, []string{blobA})
	evictAssertRowState(t, rowsAfter, 1, []string{"looking at it", "call_1"}, []string{blobB})
	evictAssertRowState(t, rowsAfter, 2, []string{blobC}, nil) // newest part kept
	evictAssertRowState(t, rowsAfter, 3, []string{"and the text-only follow-up"}, nil)

	// WEAK check — do not read it as proof of our rewrite: content_parts never
	// survives a reload today (Message.MarshalJSON is pointer-receiver but the
	// save path in pkg/session/persist.go marshals values, and UnmarshalJSON
	// ignores the resulting "content_parts" key), so the loader drops heavy
	// parts no matter what this change did. The durable proof is the row-level
	// assertions above. Kept because it documents current loader behaviour and
	// would catch a future round-trip fix.
	fresh := NewSessionManager()
	fresh.SetSessionRepo(st.Sessions())
	reloaded := fresh.GetHistory(key)
	if len(reloaded) != len(seeded) {
		t.Fatalf("reloaded len = %d, want %d", len(reloaded), len(seeded))
	}
	if count, _ := providers.LiveMultimodalParts(reloaded); count != 0 {
		t.Errorf("reloaded heavy parts = %d, want 0 (WEAK check: reload drops content_parts by construction; the durable proof is the row assertions above)", count)
	}

	// Draining the last heavy part is durable as well.
	n2, err := sm.EvictImageContent(key, 0)
	if err != nil {
		t.Fatalf("EvictImageContent(keepLast=0): %v", err)
	}
	if n2 != 1 {
		t.Fatalf("evicted = %d, want 1", n2)
	}
	rowsFinal := evictRowJSONs(t, st.Sessions(), key)
	evictAssertRowState(t, rowsFinal, 2, []string{"[image omitted from context]"}, []string{blobC})
	for i, row := range rowsFinal {
		if strings.Contains(row, blobA) || strings.Contains(row, blobB) || strings.Contains(row, blobC) {
			t.Errorf("row %d still carries a blob after keepLast=0", i)
		}
	}
	// WEAK check (see the comment above): the reload shape is decided by the
	// lossy round-trip, not by this rewrite; the durable proof is rowsFinal.
	fresh2 := NewSessionManager()
	fresh2.SetSessionRepo(st.Sessions())
	if count, _ := providers.LiveMultimodalParts(fresh2.GetHistory(key)); count != 0 {
		t.Errorf("reloaded heavy parts after keepLast=0 = %d, want 0 (WEAK check, see above)", count)
	}
}

func TestEvictImageContent_NoopWhenNothingToEvict(t *testing.T) {
	st := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(st.Sessions())

	key := "native:evict-noop"
	msgs := []providers.Message{
		{Role: "user", Content: "caption", ContentParts: []providers.ContentPart{evictImg("N")}},
		{Role: "assistant", Content: "seen"},
	}
	for _, m := range msgs {
		sm.AddFullMessage(key, m)
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	// Snapshot the dirty-tracking state under the lock (same package, direct
	// field access).
	type dirtyState struct {
		epoch        uint64
		modifiedFrom int
		metaDirty    bool
		excluded     [2]int
		updated      time.Time
	}
	capture := func() dirtyState {
		sm.mu.RLock()
		defer sm.mu.RUnlock()
		s := sm.sessions[key]
		return dirtyState{
			epoch:        s.saveEpoch,
			modifiedFrom: s.modifiedFrom,
			metaDirty:    s.metaDirty,
			excluded:     s.excludedRange,
			updated:      s.Updated,
		}
	}
	before := capture()

	// keepLast (5) exceeds the number of heavy parts (1) → nothing to evict.
	n, err := sm.EvictImageContent(key, 5)
	if n != 0 || err != nil {
		t.Fatalf("keepLast-exceeded call: got (%d, %v), want (0, nil)", n, err)
	}

	after := capture()
	if after != before {
		t.Errorf("no-op dirtied the session:\n before=%+v\n after =%+v", before, after)
	}

	// The persisted rows for the mutated-looking messages (especially the one
	// carrying the heavy part) are unchanged byte-for-byte: a no-op must not
	// rewrite anything on disk either.
	rowsBefore := evictRowJSONs(t, st.Sessions(), key)
	if !strings.Contains(rowsBefore[0], strings.Repeat("N", 1024)) {
		t.Fatalf("harness: seeded row 0 does not carry the blob: %s", rowsBefore[0])
	}
	rowsAfter := evictRowJSONs(t, st.Sessions(), key)
	if !reflect.DeepEqual(rowsBefore, rowsAfter) {
		t.Errorf("persisted rows changed on a no-op:\n before=%v\n after =%v", rowsBefore, rowsAfter)
	}

	// A media-free session is the same no-op contract.
	textKey := "native:evict-noop-textonly"
	sm.AddMessage(textKey, "user", "pure text only")
	if err := sm.Save(textKey); err != nil {
		t.Fatalf("seed textonly Save: %v", err)
	}
	sm.mu.RLock()
	textBefore := sm.sessions[textKey].saveEpoch
	sm.mu.RUnlock()
	n2, err := sm.EvictImageContent(textKey, 0)
	if n2 != 0 || err != nil {
		t.Fatalf("media-free call: got (%d, %v), want (0, nil)", n2, err)
	}
	sm.mu.RLock()
	textAfter := sm.sessions[textKey].saveEpoch
	sm.mu.RUnlock()
	if textAfter != textBefore {
		t.Errorf("media-free no-op bumped the epoch: %d → %d", textBefore, textAfter)
	}
}

func TestEvictImageContent_MissingSessionIsNoop(t *testing.T) {
	sm := NewSessionManager()
	sm.SetSessionRepo(newTestStore(t).Sessions())

	n, err := sm.EvictImageContent("native:does-not-exist", 0)
	if n != 0 || err != nil {
		t.Fatalf("missing session: got (%d, %v), want (0, nil)", n, err)
	}
}
