package session

// Eviction tests for #343: the shape where the structural invariant of #340
// reduces EvictExcludedMessages to a NO-OP.
//
// ExcludeOldMessagesFromContext's anti-splice guard pushes its boundary
// forward over a tail of tool results, so it can legitimately land on
// len(Messages) (see TestEvictExcluded_BoundaryEqualToLenKeepsContext). The
// #340 invariant then caps evictUpTo at the LAST in-context message, and when
// that message is index 0 — the only case is a session whose whole history
// except the pinned first human turn is excluded — the cap yields
// evictUpTo == 0: nothing is evicted at all (measured: 61 of 957 swept
// layouts, INFO-1 of #343).
//
// A no-op is the right outcome (the alternative is emptying the resident
// context), but until now it was an expensive and misleading one: a SQLite
// round trip writing an unchanged boundary, a fresh copy-on-write snapshot
// published for identical content, and an
// "Evicted excluded messages from memory {evicted=0}" log line.
//
// These tests pin the no-op as a no-op, and pin the bookkeeping that must
// SURVIVE it — the part that keeps the next Save from rewriting the history.

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

// isZeroish reports whether a dirty-tracking field holds its "nothing pending"
// value. [2]int{} excludedRange means no range, 0/false mean none.
func isZeroish(v interface{}) bool {
	return reflect.ValueOf(v).IsZero()
}

// seedCapToZeroSession builds the #343 shape through the real compaction entry
// point: one pinned human turn, then an assistant turn with two parallel tool
// calls and their results. With keepCount=2 the anti-splice guard walks past
// both results onto len, and every message but index 0 ends up excluded — so
// the #340 cap has nothing left to evict.
//
// Going through ExcludeOldMessagesFromContext rather than setting the flags by
// hand is deliberate: if the guard ever stops producing a boundary == len for
// this layout, the precondition below fails and these tests say so instead of
// quietly testing something else.
func seedCapToZeroSession(t *testing.T, sm *SessionManager, key string) {
	t.Helper()

	sm.AddMessage(key, "user", "cuéntame qué repos tengo") // 0: pinned human turn
	sm.AddFullMessage(key, providers.Message{              // 1: assistant, 2 parallel calls
		Role:    "assistant",
		Content: "Los busco.",
		ToolCalls: []providers.ToolCall{
			{ID: "call_a", Function: &providers.FunctionCall{Name: "exec"}},
			{ID: "call_b", Function: &providers.FunctionCall{Name: "exec"}},
		},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "repo-a", ToolCallID: "call_a"}) // 2
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "repo-b", ToolCallID: "call_b"}) // 3

	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	// excludeUpTo = 4-2 = 2 → tool result → 3 → tool result → 4 = len.
	sm.ExcludeOldMessagesFromContext(key, 2)

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
		t.Fatalf("precondition: excludeBoundary = %d, want %d (the anti-splice guard must push the boundary past the tool-result tail)", boundary, total)
	}
	if inContext != 1 || lastInContext != 0 {
		t.Fatalf("precondition: %d in-context message(s) at index %d, want exactly 1 at index 0 — "+
			"that is the shape that makes the #340 cap reduce eviction to nothing", inContext, lastInContext)
	}

	// The eviction precondition: the excluded flags are persisted.
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after excluding: %v", err)
	}
}

// TestEvictExcluded_CapToZeroDoesNoIOfOrRepublish verifies INFO-1: when the cap
// reduces eviction to nothing, the call must not write an unchanged boundary to
// SQLite, must not republish the copy-on-write snapshot, and must not report
// having evicted anything.
func TestEvictExcluded_CapToZeroIsFreeOfIOAndRepublish(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:evict-cap-to-zero"

	seedCapToZeroSession(t, sm, key)

	// State before, straight from memory and from the row.
	beforeSeq, beforeResident, beforeEpoch, beforeSnapEpoch := liveSnapshot(t, sm, key)
	beforeContents := contentsOf(beforeResident)
	beforeRow := persistedFirstInMemorySeq(t, s, key)

	// A fake that RECORDS which boundary statement the eviction issues, without
	// parking it: parking is the wrong instrument here, because the whole point
	// is that this call must never reach the store, and a fake that blocked on
	// an unexpected call would turn a regression into a deadlock instead of a
	// failure. The bounded wait below is what makes a hang observable.
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

// TestEvictExcluded_CapToZeroKeepsNextSaveIdle is the half that must NOT be
// "fixed" by an early return: the phase-3 bookkeeping (dirty flags cleared,
// lastPersistedSeq at the end of the resident slice) is what makes the next Save
// a no-op. Dropping it together with the I/O would turn this cleanup into a
// full history rewrite on the next save of every session that hits the cap.
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

	// Behavioural half: saving now changes nothing in the rows.
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

// mustReturn runs the eviction off the test goroutine and fails instead of
// hanging if it does not come back: a no-op eviction has no reason to wait on
// anything, and a regression that reintroduces the store round trip must be
// reported as a failure rather than as a test that never finishes.
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

// persistedFirstInMemorySeq reads the boundary straight from the row, bypassing
// any in-memory bookkeeping.
func persistedFirstInMemorySeq(t *testing.T, s *store.Store, key string) int {
	t.Helper()
	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil || meta == nil {
		t.Fatalf("GetSessionMeta(%q): %v (meta=%v)", key, err, meta)
	}
	return meta.FirstInMemorySeq
}

// TestEvictExcluded_ToolResultTailLeavesExcludedRowsResident pins the cost
// #343 documents as ACCEPTED, so that changing the retention policy later is a
// deliberate edit to this test rather than an accident found in a profile.
//
// The layout is the one in the issue: an assistant turn with two parallel tool
// calls followed by their two results, compacted with keepCount=2. The
// anti-splice guard walks the boundary past both results, so eviction may only
// go as far as the last in-context message — and everything after it stays
// resident *while being excluded from the model's context*. filterContextMessages
// drops those rows before the request, so the cost is RAM and a longer resident
// slice, not tokens.
//
// The other option on the table (move the boundary BACK and keep the whole tool
// group in context) would remove this cost but changes what the model sees, and
// #343 asks for a token measurement before choosing it. This test is the
// measurement-free half: it says exactly what today's policy keeps in memory.
func TestEvictExcluded_ToolResultTailLeavesExcludedRowsResident(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:evict-tool-tail-cost"

	sm.AddMessage(key, "user", "primera petición") // 0: pinned human turn
	sm.AddMessage(key, "assistant", "respuesta")   // 1
	sm.AddMessage(key, "user", "y ahora los dos")  // 2: recent human turn, preserved
	sm.AddFullMessage(key, providers.Message{      // 3: 2 parallel tool calls
		Role:    "assistant",
		Content: "Los lanzo.",
		ToolCalls: []providers.ToolCall{
			{ID: "call_x", Function: &providers.FunctionCall{Name: "exec"}},
			{ID: "call_y", Function: &providers.FunctionCall{Name: "exec"}},
		},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "x", ToolCallID: "call_x"}) // 4
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "y", ToolCallID: "call_y"}) // 5

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
		t.Errorf("evicted %d, want 2: only [0,2) may go, the boundary cap stops at the last in-context message", evicted)
	}
	if len(resident) != 4 {
		t.Fatalf("resident = %d messages, want 4: %+v", len(resident), contentsOf(resident))
	}

	// The cost itself: rows that are excluded from the model's context but still
	// held in memory because the cap will not evict past the last in-context one.
	excludedResident := 0
	for _, m := range resident {
		if m.ExcludeFromContext {
			excludedResident++
		}
	}
	if excludedResident != 3 {
		t.Errorf("%d excluded rows resident, want 3 (assistant + both tool results): the accepted cost of the anti-splice guard changed — if the policy moved the boundary BACK instead, this test is the thing to update, on purpose",
			excludedResident)
	}
	// And the model must not see them: only the non-excluded rows reach the
	// request (filterContextMessages is what the agent layer runs over this
	// slice before building it).
	inContext := 0
	for _, m := range resident {
		if !m.ExcludeFromContext {
			inContext++
		}
	}
	if inContext != 1 {
		t.Errorf("%d resident messages reach the model, want 1: the rest of the tail is excluded, it is held in RAM but never sent", inContext)
	}
}
