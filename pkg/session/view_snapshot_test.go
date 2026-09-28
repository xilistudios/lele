package session

// Tests for the copy-on-write history view (view.go, GetHistoryView).
//
// The contract under test has two halves:
//   - correctness (unchanged from the old copy-per-read implementation): the
//     view always reflects every mutation, is never aliased by writers, and is
//     safe to hold across later mutations;
//   - publication (new): structural writers must publish a fresh snapshot so
//     the read is O(1), while streaming chunk writers must NOT (coalescing).
//     A writer that forgets to publish stays correct (the epoch check rebuilds
//     the view) but silently loses the optimization — that is what
//     TestHistoryView_PublishContract guards.

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// newSnapshotTestManager returns a store-backed manager with one seeded
// session, so eviction (which requires SQLite) can be exercised too.
func newSnapshotTestManager(t *testing.T, key string, msgs ...providers.Message) *SessionManager {
	t.Helper()
	sm := NewSessionManager()
	sm.SetSessionRepo(newTestStore(t).Sessions())
	for _, m := range msgs {
		sm.AddFullMessage(key, m)
	}
	return sm
}

// snapshotOf returns the currently published snapshot of key (nil when none).
func snapshotOf(t *testing.T, sm *SessionManager, key string) *messageSnapshot {
	t.Helper()
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[key]
	if !ok {
		t.Fatalf("session %q not resident", key)
	}
	return session.viewSnapshot.Load()
}

// assertViewFresh fails when the published snapshot is nil or stale, i.e. when
// the next GetHistoryView would have to rebuild (and copy) it.
func assertViewFresh(t *testing.T, sm *SessionManager, key, writer string) {
	t.Helper()
	sm.mu.RLock()
	session, ok := sm.sessions[key]
	if !ok {
		sm.mu.RUnlock()
		t.Fatalf("%s: session %q not resident", writer, key)
	}
	snap := session.viewSnapshot.Load()
	epoch := session.saveEpoch
	sm.mu.RUnlock()

	if snap == nil {
		t.Errorf("%s: no view published; GetHistoryView pays a full copy per read", writer)
		return
	}
	if snap.epoch != epoch {
		t.Errorf("%s: published view is stale (epoch %d, session %d); GetHistoryView pays a full copy per read",
			writer, snap.epoch, epoch)
	}
}

// viewHasText reports whether any message in view carries substr — in Content
// or in ReasoningContent (thinking chunks land there).
func viewHasText(view []providers.Message, substr string) bool {
	for _, m := range view {
		if strings.Contains(m.Content, substr) || strings.Contains(m.ReasoningContent, substr) {
			return true
		}
	}
	return false
}

// assertViewMatchesSession checks the read-only view against the live slice.
func assertViewMatchesSession(t *testing.T, sm *SessionManager, key, writer string) {
	t.Helper()
	view := sm.GetHistoryView(key)

	sm.mu.RLock()
	session := sm.sessions[key]
	live := cloneMessages(session.Messages)
	sm.mu.RUnlock()

	if len(view) != len(live) {
		t.Errorf("%s: view len = %d, want %d", writer, len(view), len(live))
		return
	}
	for i := range live {
		if view[i].Role != live[i].Role || view[i].Content != live[i].Content ||
			view[i].ExcludeFromContext != live[i].ExcludeFromContext {
			t.Errorf("%s: view[%d] = {%s %q excluded=%v}, want {%s %q excluded=%v}",
				writer, i, view[i].Role, view[i].Content, view[i].ExcludeFromContext,
				live[i].Role, live[i].Content, live[i].ExcludeFromContext)
		}
	}
}

// TestHistoryView_ReflectsEveryMutation walks every writer of session.Messages
// and asserts GetHistoryView sees the change immediately.
func TestHistoryView_ReflectsEveryMutation(t *testing.T) {
	const key = "tui:chat:view-mutations"
	user := func(s string) providers.Message { return providers.Message{Role: "user", Content: s} }
	assistant := func(s string) providers.Message { return providers.Message{Role: "assistant", Content: s} }

	cases := []struct {
		name     string
		seed     []providers.Message
		mutate   func(t *testing.T, sm *SessionManager)
		wantLen  int
		contains string
	}{
		{
			name:     "AddFullMessage appends",
			seed:     []providers.Message{user("one")},
			mutate:   func(_ *testing.T, sm *SessionManager) { sm.AddFullMessage(key, assistant("two")) },
			wantLen:  2,
			contains: "two",
		},
		{
			name: "AddFullMessage replaces the streaming message in place",
			seed: []providers.Message{user("one"), {Role: "assistant", Content: "partial", Streaming: true}},
			mutate: func(_ *testing.T, sm *SessionManager) {
				sm.AddFullMessage(key, assistant("final"))
			},
			wantLen:  2,
			contains: "final",
		},
		{
			name: "AppendAssistantChunk mutates the last message in place",
			seed: []providers.Message{user("one")},
			mutate: func(_ *testing.T, sm *SessionManager) {
				sm.AppendAssistantChunk(key, "hello ")
				sm.AppendAssistantChunk(key, "world")
			},
			wantLen:  2,
			contains: "hello world",
		},
		{
			name:     "AppendReasoningChunk creates the streaming message",
			seed:     []providers.Message{user("one")},
			mutate:   func(_ *testing.T, sm *SessionManager) { sm.AppendReasoningChunk(key, "thinking") },
			wantLen:  2,
			contains: "thinking",
		},
		{
			name: "AttachFilesToLastAssistant mutates the last message",
			seed: []providers.Message{user("one"), assistant("two")},
			mutate: func(_ *testing.T, sm *SessionManager) {
				sm.AttachFilesToLastAssistant(key, []providers.MessageAttachment{{Path: "/tmp/a.png"}})
			},
			wantLen:  2,
			contains: "two",
		},
		{
			name: "RemoveLastMessage truncates",
			seed: []providers.Message{user("one"), assistant("two")},
			mutate: func(t *testing.T, sm *SessionManager) {
				if !sm.RemoveLastMessage(key) {
					t.Fatal("RemoveLastMessage returned false")
				}
			},
			wantLen:  1,
			contains: "one",
		},
		{
			name: "TruncateHistory keeps the tail",
			seed: []providers.Message{user("one"), assistant("two"), user("three")},
			mutate: func(_ *testing.T, sm *SessionManager) {
				sm.TruncateHistory(key, 1)
			},
			wantLen:  1,
			contains: "three",
		},
		{
			name: "SetHistory replaces everything",
			seed: []providers.Message{user("one"), assistant("two")},
			mutate: func(_ *testing.T, sm *SessionManager) {
				sm.SetHistory(key, []providers.Message{user("replaced")})
			},
			wantLen:  1,
			contains: "replaced",
		},
		{
			name: "ExcludeOldMessagesFromContext flips the flags in place",
			seed: []providers.Message{user("one"), assistant("two"), user("three"), assistant("four")},
			mutate: func(_ *testing.T, sm *SessionManager) {
				sm.ExcludeOldMessagesFromContext(key, 1)
			},
			wantLen:  4,
			contains: "one",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := newSnapshotTestManager(t, key, tc.seed...)
			// Warm the published view so a stale one would be visible below.
			if got := len(sm.GetHistoryView(key)); got != len(tc.seed) {
				t.Fatalf("seed view len = %d, want %d", got, len(tc.seed))
			}

			tc.mutate(t, sm)

			view := sm.GetHistoryView(key)
			if len(view) != tc.wantLen {
				t.Fatalf("view len = %d, want %d", len(view), tc.wantLen)
			}
			if !viewHasText(view, tc.contains) {
				t.Errorf("view does not contain %q: %+v", tc.contains, view)
			}
			assertViewMatchesSession(t, sm, key, tc.name)
		})
	}

	// Eviction needs a store and an excluded prefix; exercised separately
	// because the seeding differs.
	t.Run("EvictExcludedMessages drops the evicted prefix", func(t *testing.T) {
		sm := newSnapshotTestManager(t, key, user("one"), assistant("two"), user("three"), assistant("four"))
		sm.ExcludeOldMessagesFromContext(key, 1)
		if err := sm.Save(key); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if n := sm.EvictExcludedMessages(key); n == 0 {
			t.Fatal("EvictExcludedMessages evicted nothing")
		}
		view := sm.GetHistoryView(key)
		if len(view) == 0 || len(view) >= 4 {
			t.Fatalf("view len after eviction = %d, want a non-empty suffix shorter than 4", len(view))
		}
		assertViewMatchesSession(t, sm, key, "EvictExcludedMessages")
	})
}

// TestHistoryView_FallbackWhenSnapshotStale covers the safety net: a writer
// that mutates Messages and bumps the epoch WITHOUT publishing (exactly what a
// forgotten publishViewLocked() call looks like) must not corrupt the read —
// the epoch check turns it into a rebuild.
func TestHistoryView_FallbackWhenSnapshotStale(t *testing.T) {
	const key = "tui:chat:view-stale"
	user := func(s string) providers.Message { return providers.Message{Role: "user", Content: s} }

	sm := newSnapshotTestManager(t, key, user("one"))
	if got := len(sm.GetHistoryView(key)); got != 1 {
		t.Fatalf("seeded view len = %d, want 1", got)
	}

	// Simulate a writer that forgot to publish: mutate + bump the epoch only.
	sm.mu.Lock()
	session := sm.sessions[key]
	session.Messages = append(session.Messages, user("unpublished"))
	session.saveEpoch++
	sm.mu.Unlock()

	snap := snapshotOf(t, sm, key)
	if snap == nil || snap.epoch == session.saveEpoch {
		t.Fatal("test setup: snapshot should be stale after an unpublished mutation")
	}

	view := sm.GetHistoryView(key)
	if len(view) != 2 || view[1].Content != "unpublished" {
		t.Fatalf("stale-snapshot fallback returned %+v, want the 2 current messages", view)
	}
	// The fallback must also republish, so the next read is O(1) again.
	assertViewFresh(t, sm, key, "GetHistoryView fallback")
	if got := len(sm.GetHistoryView(key)); got != 2 {
		t.Fatalf("re-read after fallback len = %d, want 2", got)
	}
}

// TestHistoryView_PublishContract asserts the performance half of the contract:
// every structural writer publishes a fresh snapshot (so reads stay O(1)),
// while AppendAssistantChunk deliberately leaves it stale (coalescing).
//
// This is the RED test for a writer that drops its publishViewLocked() call:
// the correctness tests keep passing (the epoch check hides the mistake) but
// this one fails.
func TestHistoryView_PublishContract(t *testing.T) {
	const key = "tui:chat:view-publish"
	user := func(s string) providers.Message { return providers.Message{Role: "user", Content: s} }
	assistant := func(s string) providers.Message { return providers.Message{Role: "assistant", Content: s} }

	seed := func(t *testing.T) *SessionManager {
		t.Helper()
		sm := newSnapshotTestManager(t, key, user("one"), assistant("two"), user("three"), assistant("four"))
		sm.GetHistoryView(key) // warm the published view
		return sm
	}

	cases := []struct {
		name   string
		mutate func(t *testing.T, sm *SessionManager)
	}{
		{"AddFullMessage append", func(_ *testing.T, sm *SessionManager) { sm.AddFullMessage(key, assistant("five")) }},
		{"AddFullMessage in-place", func(_ *testing.T, sm *SessionManager) {
			sm.AppendAssistantChunk(key, "partial")
			sm.AddFullMessage(key, assistant("final"))
		}},
		{"AttachFilesToLastAssistant", func(_ *testing.T, sm *SessionManager) {
			sm.AttachFilesToLastAssistant(key, []providers.MessageAttachment{{Path: "/tmp/a.png"}})
		}},
		{"RemoveLastMessage", func(_ *testing.T, sm *SessionManager) { sm.RemoveLastMessage(key) }},
		{"TruncateHistory", func(_ *testing.T, sm *SessionManager) { sm.TruncateHistory(key, 2) }},
		{"SetHistory", func(_ *testing.T, sm *SessionManager) { sm.SetHistory(key, []providers.Message{user("only")}) }},
		{"ExcludeOldMessagesFromContext", func(_ *testing.T, sm *SessionManager) { sm.ExcludeOldMessagesFromContext(key, 1) }},
		{"EvictExcludedMessages", func(t *testing.T, sm *SessionManager) {
			sm.ExcludeOldMessagesFromContext(key, 1)
			if err := sm.Save(key); err != nil {
				t.Fatalf("Save: %v", err)
			}
			sm.EvictExcludedMessages(key)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := seed(t)
			tc.mutate(t, sm)
			assertViewFresh(t, sm, key, tc.name)
			assertViewMatchesSession(t, sm, key, tc.name)
		})
	}

	t.Run("AppendAssistantChunk is coalesced on purpose", func(t *testing.T) {
		sm := seed(t)
		sm.AppendAssistantChunk(key, "chunk")
		if snap := snapshotOf(t, sm, key); snap != nil && snap.epoch == sm.sessions[key].saveEpoch {
			t.Error("streaming chunk published a snapshot: publishing is O(n) per token and must be coalesced")
		}
		// Correctness must not depend on the coalescing.
		assertViewMatchesSession(t, sm, key, "AppendAssistantChunk")
	})
}

// TestHistoryView_NotAliasedByMutations is the parity check with the old
// copy-per-read implementation: a view handed out BEFORE a mutation must not
// change when the session mutates afterwards — including in-place rewrites of
// the message the view holds, which a snapshot aliasing the live array would
// corrupt.
func TestHistoryView_NotAliasedByMutations(t *testing.T) {
	const key = "tui:chat:view-alias"
	user := func(s string) providers.Message { return providers.Message{Role: "user", Content: s} }

	sm := newSnapshotTestManager(t, key, user("first"))

	held := sm.GetHistoryView(key)
	heldCopy := cloneMessages(held)

	// Every kind of mutation after the view was handed out.
	sm.AppendAssistantChunk(key, "streamed")
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "final answer"})
	sm.AddFullMessage(key, user("second"))
	sm.ExcludeOldMessagesFromContext(key, 1)
	sm.RemoveLastMessage(key)
	sm.TruncateHistory(key, 1)

	if len(held) != len(heldCopy) {
		t.Fatalf("held view len changed from %d to %d", len(heldCopy), len(held))
	}
	for i := range heldCopy {
		if !reflect.DeepEqual(held[i], heldCopy[i]) {
			t.Errorf("held view[%d] mutated by later writes: %+v (was %+v)", i, held[i], heldCopy[i])
		}
	}
	if heldCopy[0].Content != "first" {
		t.Fatalf("test setup: held view = %+v", heldCopy)
	}
	// The live view reflects the mutations; the held one did not follow them.
	live := sm.GetHistoryView(key)
	if len(live) != 1 || live[0].Content != "final answer" {
		t.Errorf("live view = %+v, want the single surviving %q turn", live, "final answer")
	}
}

// TestHistoryView_ConcurrentReadersAndWriters runs the race detector over every
// writer while readers hold the returned views. Run with -race: it must report
// no data race, no panic and no torn view.
func TestHistoryView_ConcurrentReadersAndWriters(t *testing.T) {
	const key = "tui:chat:view-race"

	sm := NewSessionManager()
	sm.SetSessionRepo(newTestStore(t).Sessions())
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "seed"})

	const writers = 4
	const readers = 6
	const iterations = 60

	var writersWG, readersWG sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < writers; w++ {
		writersWG.Add(1)
		go func(id int) {
			defer writersWG.Done()
			for i := 0; i < iterations; i++ {
				switch (id + i) % 6 {
				case 0:
					sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: fmt.Sprintf("a%d", i)})
				case 1:
					sm.AppendAssistantChunk(key, fmt.Sprintf("chunk %d ", i))
				case 2:
					sm.RemoveLastMessage(key)
				case 3:
					sm.ExcludeOldMessagesFromContext(key, 2)
				case 4:
					sm.SetHistory(key, []providers.Message{{Role: "user", Content: "reset"}})
				case 5:
					if err := sm.Save(key); err != nil {
						t.Errorf("Save: %v", err)
						return
					}
					sm.EvictExcludedMessages(key)
				}
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				view := sm.GetHistoryView(key)
				// Read every field of every message: a torn or aliased
				// snapshot would surface as an inconsistent content/role pair
				// under -race, or as a panic from a mutated slice header.
				for i := range view {
					_ = view[i].Role
					_ = view[i].Content
					_ = view[i].ExcludeFromContext
				}
			}
		}()
	}

	// Readers run until the (bounded) writers are done; the release goroutine
	// keeps the wait hang-free.
	go func() { writersWG.Wait(); close(stop) }()
	writersWG.Wait()
	readersWG.Wait()

	// The final view must still be internally consistent.
	assertViewMatchesSession(t, sm, key, "final view after concurrent writes")
}
