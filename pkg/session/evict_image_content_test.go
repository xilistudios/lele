package session

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

func evictImg(filler string) providers.ContentPart {
	return providers.ContentPart{
		Type:     "image_url",
		ImageURL: &providers.ImageURL{URL: "data:image/png;base64," + strings.Repeat(filler, 1024), Detail: "auto"},
	}
}

func evictSeedSession(t *testing.T, sm *SessionManager, key string) []providers.Message {
	t.Helper()
	msgs := []providers.Message{
		{Role: "user", ContentParts: []providers.ContentPart{evictImg("A")}},
		{
			Role:         "assistant",
			Content:      "looking at it",
			ToolCalls:    []providers.ToolCall{{ID: "call_1", Name: "read_image"}},
			ContentParts: []providers.ContentPart{evictImg("B")},
		},
		{Role: "tool", ToolCallID: "call_1", ContentParts: []providers.ContentPart{evictImg("C")}},
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

func evictRowJSONs(t *testing.T, repo *store.SessionRepo, key string) []string {
	t.Helper()
	rows, err := repo.LoadMessagesFromSeq(key, 0)
	if err != nil {
		t.Fatalf("LoadMessagesFromSeq(%q): %v", key, err)
	}
	return rows
}

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
	if n != 2 {
		t.Fatalf("evicted = %d, want 2", n)
	}

	hist := sm.GetHistory(key)
	if len(hist) != len(seeded) {
		t.Fatalf("history len = %d, want %d (eviction must not delete messages)", len(hist), len(seeded))
	}
	if count, _ := providers.LiveMultimodalParts(hist[2:3]); count != 1 {
		t.Errorf("newest message heavy parts = %d, want 1", count)
	}
	if got := hist[0].ContentParts; len(got) != 1 || got[0].Type != "text" || got[0].Text != "[image omitted from context]" {
		t.Errorf("message 0 parts = %+v, want single placeholder text part", got)
	}
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

	rowsAfter := evictRowJSONs(t, st.Sessions(), key)
	if len(rowsAfter) != len(seeded) {
		t.Fatalf("persisted rows = %d, want %d", len(rowsAfter), len(seeded))
	}
	evictAssertRowState(t, rowsAfter, 0, []string{"[image omitted from context]"}, []string{blobA})
	evictAssertRowState(t, rowsAfter, 1, []string{"looking at it", "call_1"}, []string{blobB})
	evictAssertRowState(t, rowsAfter, 2, []string{blobC}, nil)
	evictAssertRowState(t, rowsAfter, 3, []string{"and the text-only follow-up"}, nil)

	fresh := NewSessionManager()
	fresh.SetSessionRepo(st.Sessions())
	reloaded := fresh.GetHistory(key)
	if len(reloaded) != len(seeded) {
		t.Fatalf("reloaded len = %d, want %d", len(reloaded), len(seeded))
	}
	if count, _ := providers.LiveMultimodalParts(reloaded); count != 0 {
		t.Errorf("reloaded heavy parts = %d, want 0 (WEAK check: reload drops content_parts by construction; the durable proof is the row assertions above)", count)
	}

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

	n, err := sm.EvictImageContent(key, 5)
	if n != 0 || err != nil {
		t.Fatalf("keepLast-exceeded call: got (%d, %v), want (0, nil)", n, err)
	}

	after := capture()
	if after != before {
		t.Errorf("no-op dirtied the session:\n before=%+v\n after =%+v", before, after)
	}

	rowsBefore := evictRowJSONs(t, st.Sessions(), key)
	if !strings.Contains(rowsBefore[0], strings.Repeat("N", 1024)) {
		t.Fatalf("harness: seeded row 0 does not carry the blob: %s", rowsBefore[0])
	}
	rowsAfter := evictRowJSONs(t, st.Sessions(), key)
	if !reflect.DeepEqual(rowsBefore, rowsAfter) {
		t.Errorf("persisted rows changed on a no-op:\n before=%v\n after =%v", rowsBefore, rowsAfter)
	}

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

func TestEvictImageContent_PersistsMediaOnlyEviction(t *testing.T) {
	st := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(st.Sessions())

	key := "native:evict-media-only"
	const placeholder = "[image omitted from context]"
	blobM := strings.Repeat("M", 1024)
	blobN := strings.Repeat("N", 1024)
	mediaM := "data:image/png;base64," + blobM
	mediaN := "data:image/png;base64," + blobN

	msgs := []providers.Message{
		{Role: "user", Content: "", Media: []string{mediaM}},
		{Role: "user", Content: "", Media: []string{mediaN, "/tmp/keep.jpg"}},
		{Role: "user", Content: "text-only tail"},
	}
	for _, m := range msgs {
		sm.AddFullMessage(key, m)
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	rowsBefore := evictRowJSONs(t, st.Sessions(), key)
	if len(rowsBefore) != len(msgs) {
		t.Fatalf("seeded rows = %d, want %d", len(rowsBefore), len(msgs))
	}
	evictAssertRowState(t, rowsBefore, 0, []string{mediaM}, nil)
	evictAssertRowState(t, rowsBefore, 1, []string{mediaN, `"/tmp/keep.jpg"]}`}, nil)
	evictAssertRowState(t, rowsBefore, 2, []string{"text-only tail"}, nil)
	t.Logf("fixture row 0: %s", rowsBefore[0])
	t.Logf("fixture row 1: %s", rowsBefore[1])

	n, err := sm.EvictImageContent(key, 0)
	if err != nil {
		t.Fatalf("EvictImageContent: %v", err)
	}
	if n != 2 {
		t.Fatalf("evicted = %d, want 2 (one heavy Media entry per message: row 0, row 1)", n)
	}

	rowsAfter := evictRowJSONs(t, st.Sessions(), key)
	if len(rowsAfter) != len(msgs) {
		t.Fatalf("persisted rows = %d, want %d", len(rowsAfter), len(msgs))
	}
	evictAssertRowState(t, rowsAfter, 0,
		[]string{placeholder},
		[]string{blobM, `"media"`})
	evictAssertRowState(t, rowsAfter, 1,
		[]string{`media":["/tmp/keep.jpg"`},
		[]string{blobN, placeholder})
	if rowsAfter[2] != rowsBefore[2] {
		t.Errorf("row 2 (text-only tail) was not byte-identical:\n before=%s\n after =%s",
			rowsBefore[2], rowsAfter[2])
	}
	t.Logf("evicted row 0: %s", rowsAfter[0])
	t.Logf("evicted row 1: %s", rowsAfter[1])

	hist := sm.GetHistory(key)
	if len(hist) != len(msgs) {
		t.Fatalf("history len = %d, want %d (eviction must not delete messages)", len(hist), len(msgs))
	}
	if count, _ := providers.LiveMultimodalParts(hist); count != 0 {
		t.Errorf("live heavy payloads = %d, want 0", count)
	}
	if !reflect.DeepEqual(hist[1].Media, []string{"/tmp/keep.jpg"}) {
		t.Errorf("message 1 Media = %v, want [/tmp/keep.jpg]", hist[1].Media)
	}
	if len(hist[0].Media) != 0 {
		t.Errorf("message 0 Media = %v, want empty", hist[0].Media)
	}
	if got := hist[0].ContentParts; len(got) != 1 || got[0].Type != "text" || got[0].Text != placeholder {
		t.Errorf("message 0 parts = %+v, want single placeholder text part", got)
	}
	if hist[0].Content != "" {
		t.Errorf("message 0 Content = %q, want empty", hist[0].Content)
	}
	if got := hist[1].ContentParts; len(got) != 0 {
		t.Errorf("message 1 parts = %+v, want none (the surviving path entry keeps the message valid)", got)
	}
	if hist[2].Content != msgs[2].Content || hist[2].Role != msgs[2].Role {
		t.Errorf("message 2 = %+v, want unchanged %+v", hist[2], msgs[2])
	}
}
