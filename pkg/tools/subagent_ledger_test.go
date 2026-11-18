package tools

// Tests for the subagent ledger model: scope-prefix derivation, the memory/disk
// merge, dedupe, the total order and the lock discipline.
//
// The ledger is the durable view of a session's subagents — it is what keeps
// finished tasks visible after retention reaped them and after a restart, so
// most of these tests are about the two sources agreeing rather than about
// formatting.

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// ledgerTestManager builds a manager with no provider: the ledger never runs a
// task, it only reads the task map and the wired lister.
func ledgerTestManager() *SubagentManager {
	return NewSubagentManager(nil, "test-model", "/tmp/test", nil, 10)
}

// setTestLedgerLister wires the disk side of the ledger on a standalone test
// manager. SetLedgerWiring requires the task-ID collision probe as well, so a
// test that only cares about listing passes a probe that never matches: these
// managers never spawn, so claimTaskID is not exercised here and the probe is
// inert (the probe's own behaviour is pinned by subagent_id_reuse_test.go).
func setTestLedgerLister(sm *SubagentManager, lister func(parentPrefixes []string) []PersistedSubagentTask) {
	sm.SetLedgerWiring(lister, func(string) bool { return false })
}

// addLedgerTask registers a live task the way a finished run leaves one behind.
func addLedgerTask(sm *SubagentManager, id, origin, status string, created int64) *SubagentTask {
	task := &SubagentTask{
		ID:               id,
		Task:             "task " + id,
		Label:            "label " + id,
		AgentID:          "coder",
		OriginChannel:    "cli",
		OriginChatID:     "direct",
		OriginSessionKey: origin,
		Status:           status,
		Summary:          "summary " + id,
		Created:          created,
		Updated:          created,
	}
	sm.AddTaskForTest(task, func() {})
	return task
}

// ledgerByKey indexes rows by session key for assertions.
func ledgerByKey(entries []SubagentLedgerEntry) map[string]SubagentLedgerEntry {
	out := make(map[string]SubagentLedgerEntry, len(entries))
	for _, entry := range entries {
		out[entry.SessionKey] = entry
	}
	return out
}

// sortedStrings returns the sorted copy of a string slice, so prefix-set
// assertions do not depend on emission order.
func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// TestSubagentScopePrefixes_Table pins the worked examples of the derivation:
// the key itself, every ':'-aligned prefix, every ':'-aligned suffix that
// contains a ':' (a bare single-segment suffix is deliberately excluded), and
// the native-qualified form of a bare uuid.
//
// The single-segment prefixes ("telegram", "agent", "native") are kept on
// purpose: they are the historical behaviour and they are what makes an
// ancestor key find a deeper parent's tasks. They cannot leak another session's
// rows because the disk scan only ever asks for "{prefix}:subagent-", and no
// task is ever recorded under a bare channel name.
func TestSubagentScopePrefixes_Table(t *testing.T) {
	tests := []struct {
		name       string
		sessionKey string
		want       []string
	}{
		{
			name:       "channel and chat",
			sessionKey: "telegram:123",
			want:       []string{"telegram", "telegram:123"},
		},
		{
			name:       "native alias",
			sessionKey: "native:uuid-A:chat:2",
			want: []string{
				"native", "native:uuid-A", "native:uuid-A:chat", "native:uuid-A:chat:2",
				// suffixes containing a ':'
				"uuid-A:chat:2", "chat:2",
			},
		},
		{
			name:       "bare uuid",
			sessionKey: "uuid-A",
			// The native-qualified form is load-bearing: without it a bare-uuid
			// caller (the WebUI) finds no persisted row at all.
			want: []string{"uuid-A", "native:uuid-A", "native"},
		},
		{
			name:       "subagent child",
			sessionKey: "native:uuid-A:subagent-3",
			want: []string{
				"native", "native:uuid-A", "native:uuid-A:subagent-3",
				"uuid-A:subagent-3",
			},
		},
		{
			// The routed runtime key a Telegram turn carries. Its suffixes are
			// what reach the origin key the task is recorded under; "12345"
			// alone is NOT a prefix (a bare chat ID is a namespace nothing is
			// written under).
			name:       "routed agent key",
			sessionKey: "agent:main:telegram:direct:12345",
			want: []string{
				"agent", "agent:main", "agent:main:telegram", "agent:main:telegram:direct",
				"agent:main:telegram:direct:12345",
				"main:telegram:direct:12345", "telegram:direct:12345", "direct:12345",
			},
		},
		{
			name:       "routed default DM scope",
			sessionKey: "agent:main:main",
			want:       []string{"agent", "agent:main", "agent:main:main", "main:main"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SubagentScopePrefixes(tt.sessionKey)
			if want, have := sortedStrings(tt.want), sortedStrings(got); !equalStrings(want, have) {
				t.Errorf("SubagentScopePrefixes(%q) = %v, want %v", tt.sessionKey, have, want)
			}
			// Deduplicated: no prefix may appear twice.
			seen := make(map[string]bool, len(got))
			for _, prefix := range got {
				if seen[prefix] {
					t.Errorf("SubagentScopePrefixes(%q) repeats %q", tt.sessionKey, prefix)
				}
				seen[prefix] = true
			}
			// A bare SINGLE-SEGMENT suffix is never a prefix: "12345" is a chat
			// ID with no channel, a namespace nothing is written under. (A bare
			// prefix such as "telegram" is a different thing and is kept.)
			for _, prefix := range got {
				if prefix == tt.sessionKey || strings.Contains(prefix, ":") {
					continue
				}
				if strings.Contains(tt.sessionKey, ":"+prefix) {
					t.Errorf("SubagentScopePrefixes(%q) yielded the bare suffix %q", tt.sessionKey, prefix)
				}
			}
		})
	}
}

// TestSubagentScopePrefixes_Empty pins the no-key case: nil, not a slice
// containing "".
func TestSubagentScopePrefixes_Empty(t *testing.T) {
	if got := SubagentScopePrefixes(""); got != nil {
		t.Errorf("SubagentScopePrefixes(\"\") = %v, want nil", got)
	}
}

// TestSubagentScopePrefixes_EquivalentToRelated is the property test the whole
// design rests on: the disk prefix set and the memory filter must admit exactly
// the same rows.
//
// The two sides are not symmetric in shape, and that is the point:
//
//   - the memory filter is the symmetric SessionKeysRelated, so a task recorded
//     under ANY key of the session family is found;
//   - the disk scan can only ask "is this stored key {prefix}:subagent-N", i.e.
//     it matches a stored key that STARTS WITH one of the scope's prefixes. It
//     therefore cannot reach a stored key that is a strict EXTENSION of the
//     scope (a ":chat:N" alias, a ":subagent-N" child, a deeper routed key):
//     those are keys the scope is a prefix of, and no prefix set can enumerate
//     them.
//
// The property pinned here is the one the merge depends on: every stored key
// the disk side can return is also admitted by the memory filter. A divergence
// in that direction would mean the tool shows a row the digest drops (or the
// reverse) for the same session.
func TestSubagentScopePrefixes_EquivalentToRelated(t *testing.T) {
	// Keys a task can actually be recorded under: a channel+chatID pair
	// (BuildOriginSessionKey) or a native chat ID, plus the child keys the
	// ledger dedupes on.
	origins := []string{
		"",
		"telegram:123",
		"telegram:123:chat:2",
		"native:uuid-A",
		"native:uuid-A:chat:2",
		"native:uuid-A:subagent-3",
		"agent:main:telegram:123",
		"agent:main:telegram:123:chat:7",
		"cli:direct",
		"cli:direct:subagent-1",
		"other:999",
		"native:uuid-B",
		"unrelated",
		"direct:12345",
	}
	scopes := []string{
		"telegram:123",
		"telegram:123:chat:2",
		"native:uuid-A",
		"native:uuid-A:chat:2",
		"native:uuid-A:subagent-3",
		"agent:main:telegram:123",
		"agent:main:telegram:123:chat:7",
		"cli:direct",
		"cli:direct:subagent-1",
		"native:uuid-B:chat:1",
		"agent:main:telegram:direct:12345",
		"agent:main:main",
		"uuid-A",
	}

	for _, scope := range scopes {
		prefixes := make(map[string]bool)
		for _, prefix := range SubagentScopePrefixes(scope) {
			prefixes[prefix] = true
		}
		for _, origin := range origins {
			if !prefixes[origin] {
				continue // the disk side cannot reach it; nothing to compare
			}
			if !SessionKeysRelated(origin, scope) {
				t.Errorf("%q is in SubagentScopePrefixes(%q) but the memory filter rejects it: "+
					"the disk side would show a row the merge drops", origin, scope)
			}
		}
	}
}

// addRoutedLedgerTask registers a live task the way a spawn from a routed
// conversation leaves one behind: the runtime session key the agent loop holds
// (SpawnerSessionKey) and the "<channel>:<chatID>" key the task is recorded
// under (OriginSessionKey) are DIFFERENT strings.
func addRoutedLedgerTask(sm *SubagentManager, id, spawnerKey, originKey, status string, created int64) *SubagentTask {
	task := &SubagentTask{
		ID:                id,
		Task:              "task " + id,
		Label:             "label " + id,
		AgentID:           "coder",
		OriginChannel:     "telegram",
		OriginChatID:      "12345",
		OriginSessionKey:  originKey,
		SpawnerSessionKey: spawnerKey,
		Status:            status,
		Summary:           "summary " + id,
		Created:           created,
		Updated:           created,
	}
	sm.AddTaskForTest(task, func() {})
	return task
}

// TestLedgerEntries_RoutedChannelScope is the B1 regression: a task spawned from
// a routed conversation is recorded under BuildOriginSessionKey(channel, chatID)
// ("telegram:12345") while the runtime session key the prompt digest holds is
// "agent:main:telegram:direct:12345". The ledger must match the task through its
// SPAWNER key — the identity the rest of the codebase already uses
// (taskBelongsToSession) — or the digest renders nothing on every routed
// channel (Telegram, Discord, Slack, …).
func TestLedgerEntries_RoutedChannelScope(t *testing.T) {
	const (
		runtimeKey = "agent:main:telegram:direct:12345"
		originKey  = "telegram:12345"
	)
	sm := ledgerTestManager()
	addRoutedLedgerTask(sm, "subagent-1", runtimeKey, originKey, SubagentStatusRunning, 2000)
	// A task of a DIFFERENT chat, spawned from a different conversation.
	addRoutedLedgerTask(sm, "subagent-2", "agent:main:telegram:direct:99999", "telegram:99999", SubagentStatusRunning, 1000)

	t.Run("scoped by the runtime key", func(t *testing.T) {
		entries := sm.LedgerEntries([]string{runtimeKey})
		if len(entries) != 1 {
			t.Fatalf("ledger = %d rows, want 1 (%+v)", len(entries), entries)
		}
		entry := entries[0]
		if entry.ID != "subagent-1" {
			t.Errorf("ID = %q, want subagent-1 (another chat's task leaked or ours was dropped)", entry.ID)
		}
		if entry.SpawnerKey != runtimeKey || entry.OriginKey != originKey {
			t.Errorf("row identities = spawner %q / origin %q, want %q / %q",
				entry.SpawnerKey, entry.OriginKey, runtimeKey, originKey)
		}
	})

	t.Run("another chat stays out", func(t *testing.T) {
		for _, entry := range sm.LedgerEntries([]string{runtimeKey}) {
			if entry.ID == "subagent-2" {
				t.Errorf("chat 99999's task leaked into chat 12345's ledger: %+v", entry)
			}
		}
	})

	t.Run("scoped by the origin key", func(t *testing.T) {
		entries := sm.LedgerEntries([]string{originKey})
		if len(entries) != 1 || entries[0].ID != "subagent-1" {
			t.Fatalf("ledger = %+v, want only subagent-1", entries)
		}
	})

	t.Run("scoped by the child session key", func(t *testing.T) {
		// Documented behaviour: a subagent's own session key lists its parent's
		// ledger, so a stop/listing issued against the child reaches the task.
		entries := sm.LedgerEntries([]string{originKey + ":subagent-1"})
		if len(entries) != 1 || entries[0].SessionKey != originKey+":subagent-1" {
			t.Fatalf("child-scoped ledger = %+v, want the parent's task", entries)
		}
	})

	t.Run("default DM scope", func(t *testing.T) {
		// "agent:main:main" is the runtime key when the DM scope is the default;
		// it shares no segment with "telegram:12345", so only the spawner key
		// can bridge it.
		sm2 := ledgerTestManager()
		addRoutedLedgerTask(sm2, "subagent-1", "agent:main:main", originKey, SubagentStatusRunning, 1000)
		entries := sm2.LedgerEntries([]string{"agent:main:main"})
		if len(entries) != 1 {
			t.Fatalf("ledger = %d rows, want 1 (%+v)", len(entries), entries)
		}
	})
}

// TestLedgerEntries_DiskRowsNeedOriginKey pins the documented consequence of
// scoping a persisted row: it carries no spawner key (the session row does not
// store one), so a caller holding only the routed runtime key finds the LIVE
// task but not the reaped one. Callers that need the full history must pass the
// origin form too — that is the contract the follow-up digest task relies on.
func TestLedgerEntries_DiskRowsNeedOriginKey(t *testing.T) {
	const (
		runtimeKey = "agent:main:telegram:direct:12345"
		originKey  = "telegram:12345"
	)
	sm := ledgerTestManager()
	setTestLedgerLister(sm, func(prefixes []string) []PersistedSubagentTask {
		// Stand-in for ListSubagentSessionMeta: the row is stored under the
		// ORIGIN key, and the scan only answers for "{prefix}:subagent-".
		for _, prefix := range prefixes {
			if prefix == originKey {
				return []PersistedSubagentTask{{
					TaskID:     "subagent-1",
					SessionKey: originKey + ":subagent-1",
					ParentKey:  originKey,
					Label:      "reaped",
					Status:     SubagentStatusCompleted,
					Created:    1000,
				}}
			}
		}
		return nil
	})

	// Runtime key alone: the disk row is unreachable. This is the half that
	// makes the origin key mandatory for history — and it only holds because
	// the prefix set carries the ':'-aligned SUFFIXES of the runtime key. With
	// the prefix closure alone the scan would ask for
	// "agent:main:telegram:direct:12345:subagent-", which no row is stored
	// under, so the assertion below is what pins the suffix direction.
	//
	// The lister is a faithful stand-in for ListSubagentSessionMeta: it answers
	// only for the exact "{prefix}:subagent-" keys it is handed.
	scanned := func(prefixes []string) []string {
		out := make([]string, 0, len(prefixes))
		for _, prefix := range prefixes {
			out = append(out, prefix+":subagent-")
		}
		return out
	}
	if entries := sm.LedgerEntries([]string{runtimeKey}); len(entries) != 0 {
		t.Fatalf("runtime-key-scoped ledger = %+v, want none (a persisted row has no spawner key)", entries)
	}
	// The suffix direction is what makes the scan reach a row stored under a
	// key that is a ':'-aligned SUFFIX of the runtime key. The origin key
	// itself is not a prefix or a suffix of the runtime key, so it is never in
	// the set — which is exactly why the digest must pass it explicitly.
	var sawSuffixProbe bool
	for _, probe := range scanned(SubagentScopePrefixes(runtimeKey)) {
		if probe == "telegram:direct:12345:subagent-" {
			sawSuffixProbe = true
		}
	}
	if !sawSuffixProbe {
		t.Errorf("the disk scan for %q never probes the suffix form %q (prefixes: %v)",
			runtimeKey, "telegram:direct:12345:subagent-", SubagentScopePrefixes(runtimeKey))
	}

	// Origin key in the scope set: the row appears.
	entries := sm.LedgerEntries([]string{runtimeKey, originKey})
	if len(entries) != 1 {
		t.Fatalf("ledger = %d rows, want 1 (%+v)", len(entries), entries)
	}
	if !entries[0].Persisted || entries[0].SessionKey != originKey+":subagent-1" {
		t.Errorf("entry = %+v, want the persisted row", entries[0])
	}
	if entries[0].SpawnerKey != "" {
		t.Errorf("SpawnerKey = %q, want empty (a persisted row has no spawner key)", entries[0].SpawnerKey)
	}

	// And the live task is still found by the runtime key alone, which is the
	// asymmetry the two halves above describe.
	sm2 := ledgerTestManager()
	addRoutedLedgerTask(sm2, "subagent-1", runtimeKey, originKey, SubagentStatusRunning, 1000)
	if entries := sm2.LedgerEntries([]string{runtimeKey}); len(entries) != 1 {
		t.Fatalf("live ledger = %+v, want the running task", entries)
	}
}

// TestLedgerEntries_MemoryWinsOverDisk pins R3/D3: a task that is both alive
// and persisted is reported once, with the live (authoritative) fields.
func TestLedgerEntries_MemoryWinsOverDisk(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	addLedgerTask(sm, "subagent-1", origin, SubagentStatusRunning, 1000)

	setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
		return []PersistedSubagentTask{{
			TaskID:     "subagent-1",
			SessionKey: origin + ":subagent-1",
			ParentKey:  origin,
			Label:      "disk label",
			Status:     SubagentStatusCompleted,
			Created:    999,
		}}
	})

	entries := sm.LedgerEntries([]string{origin})
	if len(entries) != 1 {
		t.Fatalf("ledger = %d rows, want 1 (%+v)", len(entries), entries)
	}
	entry := entries[0]
	if entry.Persisted {
		t.Error("Persisted = true, want false (memory wins)")
	}
	if entry.Status != SubagentStatusRunning {
		t.Errorf("Status = %q, want %q (memory is authoritative)", entry.Status, SubagentStatusRunning)
	}
	if entry.Label != "label subagent-1" || entry.Created != 1000 {
		t.Errorf("entry = %+v, want the live label and Created", entry)
	}
}

// TestLedgerEntries_DedupesBySessionKeyNotID pins R4: two chats both have
// "subagent-1", and an unscoped ledger must show both. Deduplicating by bare ID
// (as the spec suggested) would silently drop one of them.
func TestLedgerEntries_DedupesBySessionKeyNotID(t *testing.T) {
	sm := ledgerTestManager()
	// addLedgerTask registers by bare ID in the manager's map (one task per ID),
	// so the two origins are modelled as two managers' worth of rows: distinct
	// IDs, identical numeric suffix is not required for the collision.
	addLedgerTask(sm, "subagent-1", "native:chat-a", SubagentStatusCompleted, 2000)
	addLedgerTask(sm, "subagent-2", "native:chat-b", SubagentStatusCompleted, 1000)

	entries := sm.LedgerEntries(nil)
	if len(entries) != 2 {
		t.Fatalf("ledger = %d rows, want 2 (same ID, different origins) (%+v)", len(entries), entries)
	}
	byKey := ledgerByKey(entries)
	for _, key := range []string{"native:chat-a:subagent-1", "native:chat-b:subagent-2"} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("row %q missing from the ledger (%+v)", key, entries)
		}
	}

	// The same holds for disk rows: two origins, one ID.
	sm2 := ledgerTestManager()
	setTestLedgerLister(sm2, func([]string) []PersistedSubagentTask {
		return []PersistedSubagentTask{
			{TaskID: "subagent-1", SessionKey: "native:chat-a:subagent-1", ParentKey: "native:chat-a"},
			{TaskID: "subagent-1", SessionKey: "native:chat-b:subagent-1", ParentKey: "native:chat-b"},
		}
	})
	if entries := sm2.LedgerEntries(nil); len(entries) != 2 {
		t.Fatalf("disk ledger = %d rows, want 2 (%+v)", len(entries), entries)
	}
}

// TestLedgerEntries_DiskOnlyAfterCleanup is the feature's core promise: a task
// that retention reaped from memory is still listed, from disk.
func TestLedgerEntries_DiskOnlyAfterCleanup(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	finished := time.Now().Add(-time.Hour).UnixMilli()
	addLedgerTask(sm, "subagent-1", origin, SubagentStatusCompleted, finished)
	sm.SetRetentionPeriod(time.Millisecond)

	// The lister stands in for the session layer: it reports what the session
	// store still knows about the key.
	setTestLedgerLister(sm, func(prefixes []string) []PersistedSubagentTask {
		for _, prefix := range prefixes {
			if prefix == origin {
				return []PersistedSubagentTask{{
					TaskID:     "subagent-1",
					SessionKey: origin + ":subagent-1",
					ParentKey:  origin,
					Label:      "persisted label",
					Status:     SubagentStatusCompleted,
					Created:    finished,
				}}
			}
		}
		return nil
	})

	if removed := sm.CleanupTerminalTasks(); removed != 1 {
		t.Fatalf("CleanupTerminalTasks removed %d tasks, want 1", removed)
	}
	if tasks := sm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("memory still holds %d tasks, want 0", len(tasks))
	}

	entries := sm.LedgerEntries([]string{origin})
	if len(entries) != 1 {
		t.Fatalf("ledger = %d rows after cleanup, want 1 (%+v)", len(entries), entries)
	}
	entry := entries[0]
	if !entry.Persisted {
		t.Error("Persisted = false, want true (the row came from disk)")
	}
	if entry.ID != "subagent-1" || entry.Status != SubagentStatusCompleted || entry.Label != "persisted label" {
		t.Errorf("entry = %+v, want the persisted row", entry)
	}
}

// TestLedgerEntries_PersistedStatusNotHardcoded pins C3: the session layer
// persists the real terminal status, so a failed/cancelled/not_done task must
// not be reported as completed.
func TestLedgerEntries_PersistedStatusNotHardcoded(t *testing.T) {
	for _, status := range []string{
		SubagentStatusFailed,
		SubagentStatusCancelled,
		SubagentStatusNotDone,
		SubagentStatusCompleted,
	} {
		t.Run(status, func(t *testing.T) {
			sm := ledgerTestManager()
			origin := "cli:direct"
			setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
				return []PersistedSubagentTask{{
					TaskID:     "subagent-1",
					SessionKey: origin + ":subagent-1",
					ParentKey:  origin,
					Status:     status,
				}}
			})
			entries := sm.LedgerEntries([]string{origin})
			if len(entries) != 1 {
				t.Fatalf("ledger = %d rows, want 1", len(entries))
			}
			if entries[0].Status != status {
				t.Errorf("Status = %q, want %q (the persisted status must survive)", entries[0].Status, status)
			}
		})
	}
}

// TestLedgerEntries_PersistedEmptyStatusIsUnknown pins M2: an empty persisted
// status is NOT a terminal outcome. The session row is created at spawn
// (SetSubagentAgentID) and subagent_status is written only on a terminal
// transition, so "" means "interrupted by a restart" or "the live row now lives
// in another agent's manager after a re-pin". Reporting it as completed told the
// model the work was finished while wait_for_subagent still blocked.
func TestLedgerEntries_PersistedEmptyStatusIsUnknown(t *testing.T) {
	t.Run("disk only", func(t *testing.T) {
		sm := ledgerTestManager()
		origin := "cli:direct"
		setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
			return []PersistedSubagentTask{{
				TaskID:     "subagent-1",
				SessionKey: origin + ":subagent-1",
				ParentKey:  origin,
				Status:     "",
			}}
		})
		entries := sm.LedgerEntries([]string{origin})
		if len(entries) != 1 {
			t.Fatalf("ledger = %d rows, want 1", len(entries))
		}
		if entries[0].Status != SubagentStatusUnknown {
			t.Errorf("Status = %q, want %q (an empty status is not terminal)", entries[0].Status, SubagentStatusUnknown)
		}
		if entries[0].Status == SubagentStatusCompleted {
			t.Error("an interrupted task must never be reported as completed")
		}
	})

	// The second half of the invariant: when a live memory row exists for the
	// same session key, it wins the dedupe and keeps its REAL status — the
	// unknown fallback never masks a task that is still running.
	t.Run("memory row wins the dedupe", func(t *testing.T) {
		sm := ledgerTestManager()
		origin := "cli:direct"
		addLedgerTask(sm, "subagent-1", origin, SubagentStatusRunning, 1000)
		setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
			return []PersistedSubagentTask{{
				TaskID:     "subagent-1",
				SessionKey: origin + ":subagent-1",
				ParentKey:  origin,
				Status:     "",
			}}
		})
		entries := sm.LedgerEntries([]string{origin})
		if len(entries) != 1 {
			t.Fatalf("ledger = %d rows, want 1 (%+v)", len(entries), entries)
		}
		if entries[0].Persisted {
			t.Error("Persisted = true, want false (the live row must win)")
		}
		if entries[0].Status != SubagentStatusRunning {
			t.Errorf("Status = %q, want %q (the live status must survive)", entries[0].Status, SubagentStatusRunning)
		}
	})
}

// TestLedgerEntries_TotalOrderOnCreatedTies pins R6/D8: several spawns in one
// turn share a Created millisecond, so the order must fall through to the
// numeric task ID and then the session key. Without the tie-breaks, pagination
// duplicates or drops rows.
func TestLedgerEntries_TotalOrderOnCreatedTies(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	const created = int64(1_700_000_000_000)
	// Inserted out of order on purpose.
	for _, id := range []string{"subagent-2", "subagent-10", "subagent-1", "subagent-9"} {
		addLedgerTask(sm, id, origin, SubagentStatusCompleted, created)
	}
	// A newer row sorts first regardless of ID.
	addLedgerTask(sm, "subagent-3", origin, SubagentStatusCompleted, created+1)

	entries := sm.LedgerEntries([]string{origin})
	var got []string
	for _, entry := range entries {
		got = append(got, entry.ID)
	}
	want := []string{"subagent-3", "subagent-10", "subagent-9", "subagent-2", "subagent-1"}
	if !equalStrings(got, want) {
		t.Fatalf("order = %v, want %v (Created desc, numeric ID desc)", got, want)
	}

	// The order is total: rows that tie on Created and on the numeric ID
	// (different origins, same task ID) are still ordered deterministically, by
	// session key. Disk rows are used because the manager's map is keyed by the
	// bare task ID and cannot hold two rows with the same ID.
	sm2 := ledgerTestManager()
	setTestLedgerLister(sm2, func([]string) []PersistedSubagentTask {
		return []PersistedSubagentTask{
			{TaskID: "subagent-1", SessionKey: "native:chat-b:subagent-1", ParentKey: "native:chat-b", Created: created},
			{TaskID: "subagent-1", SessionKey: "native:chat-a:subagent-1", ParentKey: "native:chat-a", Created: created},
		}
	})
	again := sm2.LedgerEntries(nil)
	if len(again) != 2 {
		t.Fatalf("ledger = %d rows, want 2", len(again))
	}
	if again[0].SessionKey != "native:chat-a:subagent-1" || again[1].SessionKey != "native:chat-b:subagent-1" {
		t.Errorf("tie order = %q, %q; want session-key ascending", again[0].SessionKey, again[1].SessionKey)
	}
}

// TestLedgerEntries_NilListerIsMemoryOnly pins D13: a manager with no lister
// (every standalone manager in the tests) reports its live tasks and nothing
// else, with no panic.
func TestLedgerEntries_NilListerIsMemoryOnly(t *testing.T) {
	sm := ledgerTestManager()
	addLedgerTask(sm, "subagent-1", "cli:direct", SubagentStatusRunning, 1000)

	entries := sm.LedgerEntries([]string{"cli:direct"})
	if len(entries) != 1 || entries[0].Persisted {
		t.Fatalf("ledger = %+v, want one memory row", entries)
	}

	empty := ledgerTestManager()
	if entries := empty.LedgerEntries(nil); len(entries) != 0 {
		t.Errorf("empty manager ledger = %+v, want none", entries)
	}

	// A nil manager must not panic either (defensive: callers hold one from a
	// map that may not have the agent).
	var nilManager *SubagentManager
	if entries := nilManager.LedgerEntries(nil); entries != nil {
		t.Errorf("nil manager ledger = %+v, want nil", entries)
	}
}

// TestLedgerEntries_ListerRunsOutsideManagerLock pins R7 / the lock-order
// hazard: the lister reaches the SessionManager, whose reverse path takes
// SubagentManager.mu. If LedgerEntries called it with sm.mu held, a lister that
// touches the manager (here: ListTasks, which needs sm.mu.RLock) would
// deadlock. The timeout turns a deadlock into a failure instead of a hang.
func TestLedgerEntries_ListerRunsOutsideManagerLock(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	addLedgerTask(sm, "subagent-1", origin, SubagentStatusCompleted, 1000)

	var listerSawTasks int
	setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
		// Re-entering the manager is exactly what the session layer does when
		// it resolves a key.
		listerSawTasks = len(sm.ListTasks())
		return nil
	})

	done := make(chan []SubagentLedgerEntry, 1)
	go func() { done <- sm.LedgerEntries([]string{origin}) }()

	select {
	case entries := <-done:
		if len(entries) != 1 {
			t.Fatalf("ledger = %d rows, want 1", len(entries))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("LedgerEntries deadlocked: the lister ran while sm.mu was held")
	}
	if listerSawTasks != 1 {
		t.Errorf("lister saw %d tasks, want 1 (it must observe a consistent snapshot)", listerSawTasks)
	}
}

// TestLedgerEntries_ScopingHidesOtherSessions pins the isolation contract: a
// scoped ledger shows only the rows of that session (and its children), while
// an unscoped one shows everything.
func TestLedgerEntries_ScopingHidesOtherSessions(t *testing.T) {
	sm := ledgerTestManager()
	addLedgerTask(sm, "subagent-1", "native:chat-a", SubagentStatusCompleted, 3000)
	addLedgerTask(sm, "subagent-2", "native:chat-b", SubagentStatusCompleted, 2000)
	// A task of a completely different chat.
	addLedgerTask(sm, "subagent-3", "native:chat-c", SubagentStatusCompleted, 500)

	// Scoped to chat A: only its own task.
	scoped := sm.LedgerEntries([]string{"native:chat-a"})
	if len(scoped) != 1 {
		t.Fatalf("scoped ledger = %d rows, want 1 (%+v)", len(scoped), scoped)
	}
	for _, entry := range scoped {
		if entry.SessionKey == "native:chat-b:subagent-2" {
			t.Errorf("chat B's task leaked into chat A's ledger: %+v", entry)
		}
	}

	// The alias form of the same chat resolves to the same rows: tasks are
	// recorded under the base key, which /new never rewrites.
	aliased := sm.LedgerEntries([]string{"native:chat-a:chat:3"})
	if len(aliased) != 1 || aliased[0].SessionKey != "native:chat-a:subagent-1" {
		t.Errorf("aliased ledger = %+v, want only native:chat-a:subagent-1", aliased)
	}

	// A subagent's own session key lists its parent's ledger (the prefix branch
	// of sameSessionKey).
	child := sm.LedgerEntries([]string{"native:chat-a:subagent-1"})
	if len(child) != 1 || child[0].SessionKey != "native:chat-a:subagent-1" {
		t.Errorf("child ledger = %+v, want the parent's task", child)
	}

	// Unscoped: everything.
	if all := sm.LedgerEntries(nil); len(all) != 3 {
		t.Errorf("unscoped ledger = %d rows, want 3 (%+v)", len(all), all)
	}
}

// TestLedgerEntries_PrefixesPassedToLister pins the contract between the two
// halves: the lister receives the union of SubagentScopePrefixes over the scope
// keys (deduplicated), so the disk scan and the memory filter admit the same
// rows by construction.
func TestLedgerEntries_PrefixesPassedToLister(t *testing.T) {
	sm := ledgerTestManager()
	var got []string
	setTestLedgerLister(sm, func(prefixes []string) []PersistedSubagentTask {
		got = append([]string(nil), prefixes...)
		return nil
	})

	sm.LedgerEntries([]string{"native:uuid-A:chat:2", "native:uuid-A"})
	want := []string{
		"native", "native:uuid-A", "native:uuid-A:chat", "native:uuid-A:chat:2",
		// the ':'-aligned suffixes of the first key
		"uuid-A:chat:2", "chat:2",
	}
	if !equalStrings(sortedStrings(got), sortedStrings(want)) {
		t.Errorf("lister prefixes = %v, want %v", got, want)
	}
	seen := make(map[string]bool, len(got))
	for _, prefix := range got {
		if seen[prefix] {
			t.Errorf("lister prefixes repeat %q: %v", prefix, got)
		}
		seen[prefix] = true
	}

	// No scoping means no disk scan at all.
	got = nil
	sm.LedgerEntries(nil)
	if got != nil {
		t.Errorf("lister prefixes = %v, want nil for an unscoped ledger", got)
	}
}

// TestLedgerEntries_ConcurrentWithMutation is the stand-in for `-race` (unusable
// on this host): the ledger is read while tasks are added and the lister is
// swapped, which would trip the race detector if any field were read outside
// the manager lock.
func TestLedgerEntries_ConcurrentWithMutation(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
		return []PersistedSubagentTask{{
			TaskID:     "subagent-99",
			SessionKey: origin + ":subagent-99",
			ParentKey:  origin,
			Status:     SubagentStatusCompleted,
		}}
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			addLedgerTask(sm, fmt.Sprintf("subagent-%d", i%5), origin, SubagentStatusRunning, int64(i))
			setTestLedgerLister(sm, func([]string) []PersistedSubagentTask { return nil })
			setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
				return []PersistedSubagentTask{{
					TaskID:     "subagent-99",
					SessionKey: origin + ":subagent-99",
					ParentKey:  origin,
				}}
			})
		}
	}()

	for i := 0; i < 200; i++ {
		entries := sm.LedgerEntries([]string{origin})
		// Every row must be unique by session key, whatever the interleaving.
		seen := make(map[string]bool, len(entries))
		for _, entry := range entries {
			if seen[entry.SessionKey] {
				t.Errorf("duplicate session key %q in one ledger snapshot", entry.SessionKey)
			}
			seen[entry.SessionKey] = true
			if !strings.HasPrefix(entry.SessionKey, origin) {
				t.Errorf("row %q is out of scope for %q", entry.SessionKey, origin)
			}
		}
	}
	close(stop)
	wg.Wait()
}

// equalStrings reports whether two string slices are element-wise equal.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSetLedgerWiring_AllOrNothing pins the invariant review R1 (NIT-2) asked
// for structurally: the disk lister and the task-ID collision probe are one
// call, so no caller can list persisted rows without the probe that stops a
// later spawn from reusing a task ID whose session still exists on disk.
//
// The regression this guards is not "the setter forgot a field" — it is "a
// future change split the setter back into two", which is why every assertion is
// on the observable state (LedgerWiringEnabled) plus the fields, never on the
// call shape.
func TestSetLedgerWiring_AllOrNothing(t *testing.T) {
	lister := func([]string) []PersistedSubagentTask { return nil }
	probe := func(string) bool { return false }

	t.Run("both installed", func(t *testing.T) {
		sm := ledgerTestManager()
		sm.SetLedgerWiring(lister, probe)
		if !sm.LedgerWiringEnabled() {
			t.Fatal("LedgerWiringEnabled() = false after wiring both callbacks")
		}
	})

	t.Run("nil probe disables the pair", func(t *testing.T) {
		sm := ledgerTestManager()
		sm.SetLedgerWiring(lister, nil)
		if sm.LedgerWiringEnabled() {
			t.Fatal("half-wired: lister installed without the collision probe")
		}
		if sm.persistedLister != nil || sm.sessionExists != nil {
			t.Fatalf("half-wired fields reached: lister=%v probe=%v",
				sm.persistedLister != nil, sm.sessionExists != nil)
		}
	})

	t.Run("nil lister disables the pair", func(t *testing.T) {
		sm := ledgerTestManager()
		sm.SetLedgerWiring(nil, probe)
		if sm.LedgerWiringEnabled() {
			t.Fatal("half-wired: collision probe installed without the lister")
		}
		if sm.persistedLister != nil || sm.sessionExists != nil {
			t.Fatalf("half-wired fields reached: lister=%v probe=%v",
				sm.persistedLister != nil, sm.sessionExists != nil)
		}
	})

	t.Run("typed nil is normalised", func(t *testing.T) {
		sm := ledgerTestManager()
		var typedNilProbe func(string) bool
		sm.SetLedgerWiring(lister, typedNilProbe)
		if sm.LedgerWiringEnabled() {
			t.Fatal("a nil func behind a non-nil interface value enabled the ledger")
		}
	})

	t.Run("nil both unwires", func(t *testing.T) {
		sm := ledgerTestManager()
		sm.SetLedgerWiring(lister, probe)
		sm.SetLedgerWiring(nil, nil)
		if sm.LedgerWiringEnabled() {
			t.Fatal("SetLedgerWiring(nil, nil) left the ledger enabled")
		}
	})
}
