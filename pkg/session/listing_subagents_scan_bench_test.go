package session

// Exactness coverage and the benchmark for ListSubagentSessionMeta's scan.
//
// That listing runs on the per-turn prompt path (2-4 times per turn: the digest,
// maybeSummarize's token estimate, each TUI/REST context-usage refresh, /status),
// so its cost is O(all sessions in the process). The scan is a plain prefix loop
// over "{parent}:subagent-" candidates.
//
// A ":subagent-" substring prefilter was added on the assumption that it would
// save prefix tests and then MEASURED as a pessimization, so it was removed:
//
//	without prefilter: 376-383 µs/op   (~26 KB/op, 21 allocs/op)
//	with prefilter:    441-457 µs/op   (+18%)
//
// on the fixture below (1050 sessions: 1000 ordinary chats + 50 subagents). The
// units are MICROseconds: the raw benchmark output is ~376000 ns/op, i.e.
// ~0.38 ms per call. The scan is linear in the TOTAL session count, not in the
// subagent count (1.84 ms at 5000 sessions), and the listing runs 2-4 times per
// turn (digest, maybeSummarize's token estimate, TUI/REST refresh, /status), so
// the per-turn budget is ~0.7-1.6 ms of CPU and ~50-100 KB of garbage at 1050
// sessions. The reason is that the prefix loop already rejects an ordinary
// session after about one byte — the first candidate is the key itself, the
// longest one — while strings.Contains must scan the whole key. The benchmark
// stays so the two numbers can be reproduced; do not re-add the prefilter.

import (
	"fmt"
	"testing"
	"time"
)

// TestListSubagentSessionMeta_PrefixScanIsExact pins that the prefix scan never
// changes the result: a key containing the marker but matching no prefix is
// still rejected, and a key matching a prefix is still reported. The assertions
// are prefilter-independent on purpose — they hold for any exact matcher.
func TestListSubagentSessionMeta_PrefixScanIsExact(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:prefilter"
	wantKey := recordSubagent(t, sm, parent, "subagent-1", "answer")
	// Another parent's subagent: contains the marker, matches no prefix.
	otherKey := recordSubagent(t, sm, "native:prefilter-other", "subagent-2", "answer")
	// An ordinary chat session: no marker at all.
	sm.AddMessage("native:plain-chat", "user", "hello")
	if err := sm.Save("native:plain-chat"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// A session whose NAME contains the marker but whose key does not: the scan
	// must look at the key, never at the name.
	sm.GetOrCreate("native:name-trap")
	if err := sm.SetName("native:name-trap", "parent:subagent-9"); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	if err := sm.Save("native:name-trap"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rows := sm.ListSubagentSessionMeta([]string{parent})
	if len(rows) != 1 {
		t.Fatalf("listing = %d rows, want 1 (%+v)", len(rows), rows)
	}
	if rows[0].Key != wantKey {
		t.Errorf("Key = %q, want %q", rows[0].Key, wantKey)
	}
	for _, row := range rows {
		if row.Key == otherKey {
			t.Errorf("another parent's subagent leaked: %+v", row)
		}
		if row.Key == "native:plain-chat" || row.Key == "native:name-trap" {
			t.Errorf("a non-subagent session leaked: %+v", row)
		}
	}

	// The scan must also hold for the metadata mirror (cold sessions).
	if !sm.EvictSession(wantKey) {
		t.Fatal("EvictSession = false")
	}
	if rows := sm.ListSubagentSessionMeta([]string{parent}); len(rows) != 1 || rows[0].Key != wantKey {
		t.Errorf("cold listing = %+v, want only %q", rows, wantKey)
	}
}

// TestListSubagentSessionMeta_NoSubagentPrefixReturnsEarly pins the other half:
// when no prefix can possibly be a subagent prefix the scan returns no rows. It
// is expressed as a behaviour (no rows, no panic) rather than a timing
// assertion; the benchmark below is the measurement.
func TestListSubagentSessionMeta_NoSubagentPrefixReturnsEarly(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	recordSubagent(t, sm, "native:early", "subagent-1", "answer")

	// A prefix that is not a parent of anything: no rows, and the call must not
	// degrade into a full scan of every subagent in the index.
	if rows := sm.ListSubagentSessionMeta([]string{"native:not-a-parent"}); len(rows) != 0 {
		t.Errorf("listing = %+v, want none", rows)
	}
}

// BenchmarkListSubagentSessionMeta measures the per-turn listing against a
// production-shaped index: 1000 ordinary chat sessions and 50 subagent sessions
// (the earlier fixture's 94 subagents / 17.3k messages is the worst case; this
// one isolates the scan, not the message volume).
//
// The ordinary sessions are the ones the scan must reject cheaply. Recorded
// numbers for the plain prefix loop: 376-383 µs/op (~26 KB/op, 21 allocs/op);
// with the removed ":subagent-" prefilter: 441-457 µs/op (+18%). Read them in
// MICROseconds — the raw output is ~376000 ns/op, ~0.38 ms per call — and note
// that the cost is linear in the total session count (~1.84 ms at 5000), so the
// per-turn budget (2-4 calls) is ~0.7-1.6 ms of CPU at 1050 sessions.
func BenchmarkListSubagentSessionMeta(b *testing.B) {
	sm := NewSessionManager()
	// No store: the listing is metadata-only, so the benchmark measures the
	// scan and nothing else.
	now := time.Now()
	sm.mu.Lock()
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("agent:main:telegram:direct:%d", i)
		sm.sessions[key] = &Session{Key: key, Created: now, Updated: now}
		sm.sessionMeta[key] = &sessionMetadata{Key: key, Created: now, Updated: now}
	}
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("telegram:%d:subagent-%d", 12345, i+1)
		sm.sessions[key] = &Session{Key: key, Created: now, Updated: now, SubagentStatus: "completed"}
		sm.sessionMeta[key] = &sessionMetadata{Key: key, Created: now, Updated: now, SubagentStatus: "completed"}
	}
	sm.mu.Unlock()
	// Mark the manager as loaded so the listing does not bootstrap from a store
	// it does not have.
	sm.loadOnce.Do(func() {})

	// The prefix set a routed Telegram turn produces for its own chat, as
	// tools.SubagentScopePrefixes derives it (pkg/session cannot import
	// pkg/tools, so the list is spelled out here).
	prefixes := []string{
		"agent:main:telegram:direct:12345",
		"agent", "agent:main", "agent:main:telegram", "agent:main:telegram:direct",
		"main:telegram:direct:12345", "telegram:direct:12345", "direct:12345",
		"telegram:12345",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows := sm.ListSubagentSessionMeta(prefixes)
		if len(rows) != 50 {
			b.Fatalf("rows = %d, want 50", len(rows))
		}
	}
}
