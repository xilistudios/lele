package session

import (
	"testing"
	"time"
)

func seedCountedSessions(t *testing.T, sm *SessionManager, prefix string, n, msgs int) []string {
	t.Helper()
	keys := make([]string, 0, n)
	for i := 0; i < n; i++ {
		key := prefix + string(rune('a'+i))
		sm.GetOrCreate(key)
		for m := 0; m < msgs; m++ {
			if m%2 == 0 {
				sm.AddMessage(key, "user", "question")
			} else {
				sm.AddMessage(key, "assistant", "answer")
			}
		}
		if err := sm.Save(key); err != nil {
			t.Fatalf("save %s: %v", key, err)
		}
		keys = append(keys, key)
	}
	return keys
}

func TestStoreCountsCache_CoalescesBursts(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	keys := seedCountedSessions(t, sm, "coalesce-", 3, 2)
	for _, key := range keys {
		if !sm.EvictSession(key) {
			t.Fatalf("EvictSession(%s) = false", key)
		}
	}

	counts := sm.AllTotalMessageCounts()
	for _, key := range keys {
		if counts[key] != 2 {
			t.Fatalf("first refresh: counts[%s] = %d, want 2", key, counts[key])
		}
	}
	at0 := sm.countsCache.at
	if at0.IsZero() {
		t.Fatal("the first refresh did not run the query")
	}

	for i := 0; i < 50; i++ {
		counts = sm.AllTotalMessageCounts()
		for _, key := range keys {
			if counts[key] != 2 {
				t.Fatalf("refresh %d: counts[%s] = %d, want 2", i, key, counts[key])
			}
		}
	}
	if !sm.countsCache.at.Equal(at0) {
		t.Fatalf("50 refreshes inside the window re-ran the query (clock moved from %v to %v)",
			at0, sm.countsCache.at)
	}

	before := sm.countsCache.at
	if got := sm.AllMessageCounts(); got[keys[0]] != 2 {
		t.Fatalf("AllMessageCounts[%s] = %d, want 2", keys[0], got[keys[0]])
	}
	if !sm.countsCache.at.Equal(before) {
		t.Fatal("AllMessageCounts re-ran the query instead of reusing the cache")
	}

	sm.countsCache.at = time.Now().Add(-2 * storeCountsTTL)
	counts = sm.AllTotalMessageCounts()
	if counts[keys[0]] != 2 {
		t.Fatalf("after the window: counts[%s] = %d, want 2", keys[0], counts[keys[0]])
	}
	if time.Since(sm.countsCache.at) > time.Second {
		t.Fatal("the post-window refresh did not update the query clock")
	}
}

func TestStoreCountsCache_ResidentCountsAreAlwaysLive(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	keys := seedCountedSessions(t, sm, "live-", 2, 2)
	active, cold := keys[0], keys[1]
	if !sm.EvictSession(cold) {
		t.Fatalf("EvictSession(%s) = false", cold)
	}

	counts := sm.AllTotalMessageCounts()
	if counts[active] != 2 || counts[cold] != 2 {
		t.Fatalf("baseline counts = %d/%d, want 2/2", counts[active], counts[cold])
	}

	sm.AddMessage(active, "user", "third")
	sm.AddMessage(active, "assistant", "fourth")
	counts = sm.AllTotalMessageCounts()
	if counts[active] != 4 {
		t.Fatalf("counts[%s] = %d after two appends, want 4 (resident counts must be live)",
			active, counts[active])
	}
	if counts[cold] != 2 {
		t.Fatalf("counts[%s] = %d, want 2 (cold key must keep its queried count)",
			cold, counts[cold])
	}
}

func TestStoreCountsCache_InvalidatesWhenColdSetChanges(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	anchor := "cold-anchor"
	sm.GetOrCreate(anchor)
	sm.AddMessage(anchor, "user", "q")
	sm.AddMessage(anchor, "assistant", "a")
	if err := sm.Save(anchor); err != nil {
		t.Fatalf("save anchor: %v", err)
	}
	if !sm.EvictSession(anchor) {
		t.Fatalf("EvictSession(%s) = false", anchor)
	}

	counts := sm.AllTotalMessageCounts()
	if counts[anchor] != 2 {
		t.Fatalf("baseline counts[%s] = %d, want 2", anchor, counts[anchor])
	}
	if sm.countsCache.at.IsZero() {
		t.Fatal("baseline: the query did not run")
	}

	late := "cold-late"
	sm.GetOrCreate(late)
	sm.AddMessage(late, "user", "q")
	sm.AddMessage(late, "assistant", "a")
	if err := sm.Save(late); err != nil {
		t.Fatalf("save late: %v", err)
	}
	if !sm.EvictSession(late) {
		t.Fatalf("EvictSession(%s) = false", late)
	}
	counts = sm.AllTotalMessageCounts()
	if counts[late] != 2 {
		t.Fatalf("counts[%s] = %d after it became cold, want 2 — the cold-set change did not invalidate the cache",
			late, counts[late])
	}

	grown := "cold-grown"
	sm.GetOrCreate(grown)
	sm.AddMessage(grown, "user", "q")
	sm.AddMessage(grown, "assistant", "a")
	if err := sm.Save(grown); err != nil {
		t.Fatalf("save grown: %v", err)
	}
	second := "cold-anchor-2"
	sm.GetOrCreate(second)
	sm.AddMessage(second, "user", "q")
	if err := sm.Save(second); err != nil {
		t.Fatalf("save second anchor: %v", err)
	}
	if !sm.EvictSession(second) {
		t.Fatalf("EvictSession(%s) = false", second)
	}
	counts = sm.AllTotalMessageCounts()
	if counts[grown] != 2 {
		t.Fatalf("precondition: counts[%s] = %d, want the persisted 2 while resident", grown, counts[grown])
	}

	sm.AddMessage(grown, "user", "q2")
	sm.AddMessage(grown, "assistant", "a2")
	if !sm.EvictSession(grown) {
		t.Fatalf("EvictSession(%s) = false", grown)
	}
	counts = sm.AllTotalMessageCounts()
	if counts[grown] != 4 {
		t.Fatalf("counts[%s] = %d after growing and being evicted, want 4 — the stale count was served",
			grown, counts[grown])
	}

	sm.mu.Lock()
	delete(sm.sessionMeta, second)
	sm.mu.Unlock()
	counts = sm.AllTotalMessageCounts()
	if _, ok := counts[second]; ok {
		t.Fatalf("counts still carries the session %s = %d after it left the index", second, counts[second])
	}
}
