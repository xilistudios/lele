// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

// Mechanical guard for the COW snapshot contract at the package boundary (R1,
// reviewer finding M2).
//
// Since the T1 copy-on-write work, SessionManager.GetHistoryView returns a
// slice SHARED by every reader (the published, immutable snapshot). That was
// harmless while each call handed back its own copy; now an external caller
// that mutated it in place would poison the snapshot served to everyone
// (including the TUI and the WebUI), and because only pkg/session writers bump
// saveEpoch, the poisoned snapshot would never be invalidated — stale content
// served as current forever.
//
// agentProvidableImpl.GetSessionHistory (the alias pkg/channels consumes for
// the WebUI history endpoint) therefore returns a defensive COPY. These tests
// pin both halves of the split:
//   - GetSessionHistory: caller-owned copy, mutation cannot leak;
//   - GetHistoryView: still the shared O(1) snapshot, still non-aliasing with
//     respect to later session mutations (T1 semantics preserved).

import (
	"testing"

	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/providers"
)

// newSessionHistoryCopyLoop builds an AgentLoop with a default agent, seeds the
// given session through the same public write path the channels use and returns
// the providable facade plus the session key.
func newSessionHistoryCopyLoop(t *testing.T, msgs ...providers.Message) (channels.AgentProvidable, string) {
	t.Helper()

	const key = "native:history-copy"
	al, _ := newThinkLevelTestLoop(t, "")
	// Exercise the interface pkg/channels actually consumes, not the concrete
	// implementation: the contract under test lives on the boundary.
	prov := al.GetProvidable()

	for _, msg := range msgs {
		if err := prov.AddSessionMessage(key, msg); err != nil {
			t.Fatalf("AddSessionMessage(%q, %q): %v", key, msg.Content, err)
		}
	}
	return prov, key
}

// seedHistory is the three-message fixture used by most tests.
func seedHistory(t *testing.T) (channels.AgentProvidable, string) {
	t.Helper()
	return newSessionHistoryCopyLoop(t,
		providers.Message{Role: "user", Content: "hello"},
		providers.Message{Role: "assistant", Content: "hi there"},
		providers.Message{Role: "user", Content: "how are you?"},
	)
}

// TestGetSessionHistory_DoesNotAliasSharedSnapshot pins the boundary itself:
// the slice crossing into pkg/channels must not be the session's published
// snapshot. Without the copy, an external caller would hold the shared backing
// array — the M2 finding.
func TestGetSessionHistory_DoesNotAliasSharedSnapshot(t *testing.T) {
	prov, key := seedHistory(t)

	hist := prov.GetSessionHistory(key)
	view := prov.GetHistoryView(key)
	if len(hist) == 0 || len(view) == 0 {
		t.Fatalf("unexpected empty slices: len(hist)=%d len(view)=%d", len(hist), len(view))
	}
	if &hist[0] == &view[0] {
		t.Fatal("GetSessionHistory aliases the shared GetHistoryView snapshot; it must return a copy")
	}
	if &hist[len(hist)-1] == &view[len(view)-1] {
		t.Fatal("GetSessionHistory tail aliases the shared GetHistoryView snapshot; it must return a copy")
	}
}

// TestGetSessionHistory_MutationDoesNotPoisonReaders is the regression test for
// M2: the copy returned across the package boundary may be mangled in every way
// an unsuspecting caller could, and neither later GetSessionHistory calls nor
// GetHistoryView may observe it.
func TestGetSessionHistory_MutationDoesNotPoisonReaders(t *testing.T) {
	prov, key := seedHistory(t)

	hist := prov.GetSessionHistory(key)
	if len(hist) != 3 {
		t.Fatalf("GetSessionHistory len = %d, want 3", len(hist))
	}

	// Poison the returned slice: element rewrite, field rewrite, append, replace.
	hist[0].Role = "system"
	hist[0].Content = "POISONED"
	hist[0].Streaming = true
	hist[1] = providers.Message{Role: "assistant", Content: "POISONED"}
	hist = append(hist, providers.Message{Role: "user", Content: "POISONED"})

	// A later copy must still show the real history, untouched.
	again := prov.GetSessionHistory(key)
	wantHistory(t, again, "GetSessionHistory after mutation")

	// The shared snapshot must be untouched too (this is what the TUI and every
	// other reader sees).
	viewAfter := prov.GetHistoryView(key)
	wantHistory(t, viewAfter, "GetHistoryView after mutation")
}

// wantHistory asserts the canonical three-message fixture.
func wantHistory(t *testing.T, msgs []providers.Message, what string) {
	t.Helper()

	want := []providers.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "how are you?"},
	}
	if len(msgs) != len(want) {
		t.Fatalf("%s: len = %d, want %d (%v)", what, len(msgs), len(want), msgs)
	}
	for i := range want {
		if msgs[i].Role != want[i].Role || msgs[i].Content != want[i].Content {
			t.Errorf("%s: msg[%d] = {%q, %q}, want {%q, %q}",
				what, i, msgs[i].Role, msgs[i].Content, want[i].Role, want[i].Content)
		}
		if msgs[i].Streaming {
			t.Errorf("%s: msg[%d].Streaming = true, want false", what, i)
		}
	}
}

// TestGetSessionHistory_EachCallReturnsAFreshCopy pins the per-call ownership:
// two calls never share a backing array, so one caller cannot corrupt another.
func TestGetSessionHistory_EachCallReturnsAFreshCopy(t *testing.T) {
	prov, key := seedHistory(t)

	first := prov.GetSessionHistory(key)
	second := prov.GetSessionHistory(key)
	if &first[0] == &second[0] {
		t.Fatal("two GetSessionHistory calls share a backing array; each call must return its own copy")
	}

	first[0].Content = "POISONED"
	if second[0].Content != "hello" {
		t.Errorf("second copy saw %q, want %q", second[0].Content, "hello")
	}
	if hist := prov.GetSessionHistory(key); hist[0].Content != "hello" {
		t.Errorf("third copy saw %q, want %q", hist[0].Content, "hello")
	}
}

// TestGetHistoryView_StillSharedSnapshot asserts the hot path did NOT start
// copying: two consecutive reads with no intervening write must hand out the
// very same backing array (identical element addresses).
func TestGetHistoryView_StillSharedSnapshot(t *testing.T) {
	prov, key := seedHistory(t)

	a := prov.GetHistoryView(key)
	b := prov.GetHistoryView(key)
	if len(a) == 0 || len(b) == 0 {
		t.Fatalf("unexpected empty view: len(a)=%d len(b)=%d", len(a), len(b))
	}
	if &a[0] != &b[0] {
		t.Fatal("GetHistoryView no longer returns the shared snapshot: consecutive reads must alias (O(1), no copy)")
	}

	// Structural identity of the whole slice, not just its head.
	if len(a) != len(b) || &a[len(a)-1] != &b[len(b)-1] {
		t.Fatal("GetHistoryView returned different slices for the same unchanged session")
	}
}

// TestGetHistoryView_RetainedViewUnaffectedByWrites preserves the T1 semantics:
// a view already handed out keeps showing the content it was taken at, while a
// fresh read sees the write. This is what makes the shared snapshot safe.
func TestGetHistoryView_RetainedViewUnaffectedByWrites(t *testing.T) {
	prov, key := seedHistory(t)

	retained := prov.GetHistoryView(key)
	retainedLen := len(retained)
	retainedContent := retained[0].Content

	if err := prov.AddSessionMessage(key, providers.Message{Role: "assistant", Content: "a fourth"}); err != nil {
		t.Fatalf("AddSessionMessage: %v", err)
	}

	if len(retained) != retainedLen {
		t.Errorf("retained view len = %d, want %d (view must not be mutated in place)", len(retained), retainedLen)
	}
	if retained[0].Content != retainedContent {
		t.Errorf("retained view[0].Content = %q, want %q", retained[0].Content, retainedContent)
	}

	fresh := prov.GetHistoryView(key)
	if len(fresh) != retainedLen+1 {
		t.Fatalf("fresh view len = %d, want %d", len(fresh), retainedLen+1)
	}
	if last := fresh[len(fresh)-1].Content; last != "a fourth" {
		t.Errorf("fresh view last content = %q, want %q", last, "a fourth")
	}
}

// TestGetSessionHistory_UnknownSessionIsEmpty pins the empty-session shape the
// channels rely on: a non-nil empty slice is safe to range over.
func TestGetSessionHistory_UnknownSessionIsEmpty(t *testing.T) {
	prov, _ := seedHistory(t)

	hist := prov.GetSessionHistory("native:does-not-exist")
	if hist == nil {
		t.Error("GetSessionHistory returned nil for an unknown session; want empty slice")
	}
	if len(hist) != 0 {
		t.Errorf("GetSessionHistory len = %d, want 0", len(hist))
	}
}
