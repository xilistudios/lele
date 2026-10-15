package session

// Eviction tests for #343: the shape where the structural invariant of #340
// reduces EvictExcludedMessages to a NO-OP.
//
// ExcludeOldMessagesFromContext can legitimately pin its boundary at
// len(Messages) — a whole-prefix compaction does (see seedCapToZeroSession),
// as do the cold-load / stale-boundary / pin-widening routes. The #340
// invariant then caps evictUpTo at the LAST in-context message, and when that
// message is index 0 — a session whose whole history except the pinned first
// human turn is excluded — the cap yields evictUpTo == 0: nothing is evicted
// at all (measured: 61 of 957 swept layouts, INFO-1 of #343).
//
// Under the Option A retention policy of #343 the anti-splice guard no longer
// walks the boundary forward over a tool-result tail, so that particular route
// to boundary == len is gone; the cap-to-zero shape itself is unchanged and
// still reachable.
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

// seedCapToZeroSession builds a session whose whole history except the pinned
// first human turn is excluded, so the #340 cap reduces eviction to nothing.
//
// Before the Option A retention policy (#343) this shape was produced by the
// anti-splice guard walking its boundary past a tool-result tail; that route is
// gone. It is reached instead through a policy-independent boundary case: a
// whole-prefix compaction (keepCount=0) pins excludeBoundary at len(Messages)
// and leaves index 0 as the only in-context message.
//
// Going through ExcludeOldMessagesFromContext rather than setting the flags by
// hand is deliberate: if this ever stops yielding boundary == total with a
// single in-context message, the preconditions below fail loudly instead of
// quietly testing something else.
func seedCapToZeroSession(t *testing.T, sm *SessionManager, key string) {
	t.Helper()

	sm.AddMessage(key, "user", "cuéntame qué repos tengo") // 0: the only human turn (pinned)
	sm.AddMessage(key, "assistant", "Los busco.")          // 1
	sm.AddMessage(key, "assistant", "Aquí están.")         // 2
	sm.AddMessage(key, "assistant", "¿Algo más?")          // 3

	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	// keepCount=0 → excludeUpTo = len: everything from index 1 on is excluded
	// and the boundary is pinned at len. With no human turn after index 0,
	// index 0 is the only message left in context.
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

	// The eviction precondition: the excluded flags are persisted.
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after excluding: %v", err)
	}
}

// TestEvictExcluded_CapToZeroIsFreeOfIOAndRepublish verifies INFO-1: when the cap
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

// TestEvictExcluded_ToolResultTailKeepsWholeGroupInContext anchors the #343
// retention policy at the session level: when the kept tail would start with a
// tool result whose owning assistant is about to be excluded, the boundary
// moves BACK to that owner and the whole tool group stays in context.
//
// The layout is the one in the issue: an assistant turn with two parallel tool
// calls followed by their two results, compacted with keepCount=2. The group
// {3,4,5} is kept whole, so no excluded row is left resident — the RAM cost the
// old forward-push accepted (the assistant and both results held in memory
// while filterContextMessages dropped them from the request) is gone. The token
// price of keeping the group live was measured before adopting the policy
// (#343 T1: at most 1.88% of the 128k context window).
//
// This is the deliberate rewrite the old version of this test asked for: the
// change of retention policy is exactly the situation it was designed to
// detect.
func TestEvictExcluded_ToolResultTailKeepsWholeGroupInContext(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:evict-tool-tail-whole-group"

	sm.AddMessage(key, "user", "primera petición") // 0: pinned human turn
	sm.AddMessage(key, "assistant", "respuesta")   // 1
	sm.AddMessage(key, "user", "y ahora los dos")  // 2: recent human turn, preserved
	sm.AddFullMessage(key, providers.Message{      // 3: 2 parallel tool calls (group owner)
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
		t.Errorf("evicted %d, want 2: only [0,2) may go; the boundary moved back to the group owner so the group is kept", evicted)
	}
	if len(resident) != 4 {
		t.Fatalf("resident = %d messages, want 4 (the user turn plus the whole group {3,4,5}): %+v", len(resident), contentsOf(resident))
	}

	// The whole group is in context, and no excluded row lingers in RAM: this is
	// the cost the old policy accepted and the new policy removes.
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
