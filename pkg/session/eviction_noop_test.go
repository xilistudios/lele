package session

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

func isZeroish(v interface{}) bool {
	return reflect.ValueOf(v).IsZero()
}

func seedCapToZeroSession(t *testing.T, sm *SessionManager, key string) {
	t.Helper()

	sm.AddMessage(key, "user", "cuéntame qué repos tengo")
	sm.AddMessage(key, "assistant", "Los busco.")
	sm.AddMessage(key, "assistant", "Aquí están.")
	sm.AddMessage(key, "assistant", "¿Algo más?")

	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	sm.ExcludeOldMessagesFromContext(key, 0)

	sm.mu.Lock()
	session, ok := sm.sessions[key]
	if !ok {
		sm.mu.Unlock()
		t.Fatalf("session %q not resident", key)
	}
	boundary, total := session.excludeBoundary, len(session.Messages)
	inContext := 0
	lastInContext := -1
	for i, m := range session.Messages {
		if !m.ExcludeFromContext {
			inContext++
			lastInContext = i
		}
	}
	sm.mu.Unlock()

	if boundary != total {
		t.Fatalf("precondition: excludeBoundary = %d, want %d (a whole-prefix compaction pins the boundary at len)", boundary, total)
	}
	if inContext != 1 || lastInContext != 0 {
		t.Fatalf("precondition: %d in-context message(s) at index %d, want exactly 1 at index 0 — "+
			"that is the shape that makes the #340 cap reduce eviction to nothing", inContext, lastInContext)
	}

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after excluding: %v", err)
	}
}

func TestEvictExcluded_CapToZeroIsFreeOfIOAndRepublish(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:evict-cap-to-zero"

	seedCapToZeroSession(t, sm, key)

	beforeSeq, beforeResident, beforeEpoch, beforeSnapEpoch := liveSnapshot(t, sm, key)
	beforeContents := contentsOf(beforeResident)
	beforeRow := persistedFirstInMemorySeq(t, s, key)

	gate := newIOGate(&sm.mu)
	defer gate.unlock()
	fake := useBlockingEvictionRepo(t, sm, gate, false, false)

	evicted := mustReturn(t, sm, key)

	if evicted != 0 {
		t.Errorf("EvictExcludedMessages returned %d, want 0 (nothing may be evicted: the only in-context message is index 0)", evicted)
	}
	if got := fake.method.Load(); got != boundaryMethodNone {
		t.Errorf("the no-op eviction issued boundary statement %d, want none: it rewrote a FirstInMemorySeq that had not changed", got)
	}

	afterSeq, afterResident, afterEpoch, afterSnapEpoch := liveSnapshot(t, sm, key)
	if afterSeq != beforeSeq {
		t.Errorf("firstInMemorySeq moved %d → %d on an eviction of zero messages", beforeSeq, afterSeq)
	}
	if afterEpoch != beforeEpoch || afterSnapEpoch != beforeSnapEpoch {
		t.Errorf("epoch moved %d→%d (snapshot %d→%d): the no-op republished the history view, so every GetHistoryView reader's snapshot was invalidated for identical content",
			beforeEpoch, afterEpoch, beforeSnapEpoch, afterSnapEpoch)
	}
	if got := contentsOf(afterResident); fmt.Sprint(got) != fmt.Sprint(beforeContents) {
		t.Errorf("resident slice changed on a no-op eviction:\n before=%v\n after =%v", beforeContents, got)
	}
	if row := persistedFirstInMemorySeq(t, s, key); row != beforeRow {
		t.Errorf("persisted FirstInMemorySeq changed %d → %d", beforeRow, row)
	}
}

func TestEvictExcluded_CapToZeroKeepsNextSaveIdle(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:evict-cap-to-zero-save"

	seedCapToZeroSession(t, sm, key)

	gate := newIOGate(&sm.mu)
	defer gate.unlock()
	fake := useBlockingEvictionRepo(t, sm, gate, false, false)

	if n := mustReturn(t, sm, key); n != 0 {
		t.Fatalf("evicted %d, want 0", n)
	}

	sm.mu.Lock()
	session := sm.sessions[key]
	lastPersisted := session.lastPersistedSeq
	resident := len(session.Messages)
	pending := map[string]interface{}{
		"metaDirty":      session.metaDirty,
		"msgsAppended":   session.msgsAppended,
		"modifiedFrom":   session.modifiedFrom,
		"excludedRange":  session.excludedRange,
		"lastMsgDeleted": session.lastMsgDeleted,
		"deleteFromSeq":  session.deleteFromSeq,
	}
	sm.mu.Unlock()

	if lastPersisted != resident-1 {
		t.Errorf("lastPersistedSeq = %d, want %d: the next Save would rewrite the whole history instead of idling", lastPersisted, resident-1)
	}
	for name, v := range pending {
		if isZeroish(v) {
			continue
		}
		t.Errorf("%s = %v after the eviction: the caller's Save had just persisted this state, so the next Save must idle (a no-op eviction that leaves dirt behind turns into a full history rewrite)", name, v)
	}
	if got := fake.method.Load(); got != boundaryMethodNone {
		t.Errorf("boundary statement %d issued, want none", got)
	}

	rowsBefore, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq: %v", err)
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after the no-op eviction: %v", err)
	}
	rowsAfter, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq after Save: %v", err)
	}
	if len(rowsAfter) != len(rowsBefore) {
		t.Fatalf("row count changed %d → %d on an idle Save", len(rowsBefore), len(rowsAfter))
	}
	for i := range rowsBefore {
		if rowsBefore[i].Excluded != rowsAfter[i].Excluded {
			t.Errorf("row %d excluded flag rewritten %v → %v", i, rowsBefore[i].Excluded, rowsAfter[i].Excluded)
		}
	}
}

func mustReturn(t *testing.T, sm *SessionManager, key string) int {
	t.Helper()
	done := evictAsync(sm, key)
	select {
	case n := <-done:
		return n
	case <-time.After(15 * time.Second):
		t.Fatal("EvictExcludedMessages did not return: a no-op eviction must not block on the store")
		return 0
	}
}

func persistedFirstInMemorySeq(t *testing.T, s *store.Store, key string) int {
	t.Helper()
	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil || meta == nil {
		t.Fatalf("GetSessionMeta(%q): %v (meta=%v)", key, err, meta)
	}
	return meta.FirstInMemorySeq
}

func TestEvictExcluded_ToolResultTailKeepsWholeGroupInContext(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:evict-tool-tail-whole-group"

	sm.AddMessage(key, "user", "primera petición")
	sm.AddMessage(key, "assistant", "respuesta")
	sm.AddMessage(key, "user", "y ahora los dos")
	sm.AddFullMessage(key, providers.Message{
		Role:    "assistant",
		Content: "Los lanzo.",
		ToolCalls: []providers.ToolCall{
			{ID: "call_x", Function: &providers.FunctionCall{Name: "exec"}},
			{ID: "call_y", Function: &providers.FunctionCall{Name: "exec"}},
		},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "x", ToolCallID: "call_x"})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "y", ToolCallID: "call_y"})

	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	sm.ExcludeOldMessagesFromContext(key, 2)
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after excluding: %v", err)
	}

	evicted := sm.EvictExcludedMessages(key)

	_, resident, _, _ := liveSnapshot(t, sm, key)
	if evicted != 2 {
		t.Errorf("evicted %d, want 2: only [0,2) may go; the boundary moved back to the group owner so the group is kept", evicted)
	}
	if len(resident) != 4 {
		t.Fatalf("resident = %d messages, want 4 (the user turn plus the whole group {3,4,5}): %+v", len(resident), contentsOf(resident))
	}

	excludedResident := 0
	inContext := 0
	for _, m := range resident {
		if m.ExcludeFromContext {
			excludedResident++
		} else {
			inContext++
		}
	}
	if excludedResident != 0 {
		t.Errorf("%d excluded rows resident, want 0: the boundary moves back to the group owner, so nothing excluded is left in memory", excludedResident)
	}
	if inContext != 4 {
		t.Errorf("%d resident messages reach the model, want 4 (index 2 plus the whole group {3,4,5}): the whole tool group is kept", inContext)
	}
}
