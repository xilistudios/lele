package tools

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func ledgerTestManager() *SubagentManager {
	return NewSubagentManager(nil, "test-model", "/tmp/test", nil, 10)
}

func setTestLedgerLister(sm *SubagentManager, lister func(parentPrefixes []string) []PersistedSubagentTask) {
	sm.SetLedgerWiring(lister, func(string) bool { return false })
}

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

func ledgerByKey(entries []SubagentLedgerEntry) map[string]SubagentLedgerEntry {
	out := make(map[string]SubagentLedgerEntry, len(entries))
	for _, entry := range entries {
		out[entry.SessionKey] = entry
	}
	return out
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

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
				"uuid-A:chat:2", "chat:2",
			},
		},
		{
			name:       "bare uuid",
			sessionKey: "uuid-A",
			want:       []string{"uuid-A", "native:uuid-A", "native"},
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
			seen := make(map[string]bool, len(got))
			for _, prefix := range got {
				if seen[prefix] {
					t.Errorf("SubagentScopePrefixes(%q) repeats %q", tt.sessionKey, prefix)
				}
				seen[prefix] = true
			}
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

func TestSubagentScopePrefixes_Empty(t *testing.T) {
	if got := SubagentScopePrefixes(""); got != nil {
		t.Errorf("SubagentScopePrefixes(\"\") = %v, want nil", got)
	}
}

func TestSubagentScopePrefixes_EquivalentToRelated(t *testing.T) {
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
				continue
			}
			if !SessionKeysRelated(origin, scope) {
				t.Errorf("%q is in SubagentScopePrefixes(%q) but the memory filter rejects it: "+
					"the disk side would show a row the merge drops", origin, scope)
			}
		}
	}
}

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

func TestLedgerEntries_RoutedChannelScope(t *testing.T) {
	const (
		runtimeKey = "agent:main:telegram:direct:12345"
		originKey  = "telegram:12345"
	)
	sm := ledgerTestManager()
	addRoutedLedgerTask(sm, "subagent-1", runtimeKey, originKey, SubagentStatusRunning, 2000)
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
		entries := sm.LedgerEntries([]string{originKey + ":subagent-1"})
		if len(entries) != 1 || entries[0].SessionKey != originKey+":subagent-1" {
			t.Fatalf("child-scoped ledger = %+v, want the parent's task", entries)
		}
	})

	t.Run("default DM scope", func(t *testing.T) {
		sm2 := ledgerTestManager()
		addRoutedLedgerTask(sm2, "subagent-1", "agent:main:main", originKey, SubagentStatusRunning, 1000)
		entries := sm2.LedgerEntries([]string{"agent:main:main"})
		if len(entries) != 1 {
			t.Fatalf("ledger = %d rows, want 1 (%+v)", len(entries), entries)
		}
	})
}

func TestLedgerEntries_DiskRowsNeedOriginKey(t *testing.T) {
	const (
		runtimeKey = "agent:main:telegram:direct:12345"
		originKey  = "telegram:12345"
	)
	sm := ledgerTestManager()
	setTestLedgerLister(sm, func(prefixes []string) []PersistedSubagentTask {
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

	sm2 := ledgerTestManager()
	addRoutedLedgerTask(sm2, "subagent-1", runtimeKey, originKey, SubagentStatusRunning, 1000)
	if entries := sm2.LedgerEntries([]string{runtimeKey}); len(entries) != 1 {
		t.Fatalf("live ledger = %+v, want the running task", entries)
	}
}

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

func TestLedgerEntries_DedupesBySessionKeyNotID(t *testing.T) {
	sm := ledgerTestManager()
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

func TestLedgerEntries_DiskOnlyAfterCleanup(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	finished := time.Now().Add(-time.Hour).UnixMilli()
	addLedgerTask(sm, "subagent-1", origin, SubagentStatusCompleted, finished)
	sm.SetRetentionPeriod(time.Millisecond)

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

func TestLedgerEntries_TotalOrderOnCreatedTies(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	const created = int64(1_700_000_000_000)
	for _, id := range []string{"subagent-2", "subagent-10", "subagent-1", "subagent-9"} {
		addLedgerTask(sm, id, origin, SubagentStatusCompleted, created)
	}
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

	var nilManager *SubagentManager
	if entries := nilManager.LedgerEntries(nil); entries != nil {
		t.Errorf("nil manager ledger = %+v, want nil", entries)
	}
}

func TestLedgerEntries_ListerRunsOutsideManagerLock(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	addLedgerTask(sm, "subagent-1", origin, SubagentStatusCompleted, 1000)

	var listerSawTasks int
	setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
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

func TestLedgerEntries_ScopingHidesOtherSessions(t *testing.T) {
	sm := ledgerTestManager()
	addLedgerTask(sm, "subagent-1", "native:chat-a", SubagentStatusCompleted, 3000)
	addLedgerTask(sm, "subagent-2", "native:chat-b", SubagentStatusCompleted, 2000)
	addLedgerTask(sm, "subagent-3", "native:chat-c", SubagentStatusCompleted, 500)

	scoped := sm.LedgerEntries([]string{"native:chat-a"})
	if len(scoped) != 1 {
		t.Fatalf("scoped ledger = %d rows, want 1 (%+v)", len(scoped), scoped)
	}
	for _, entry := range scoped {
		if entry.SessionKey == "native:chat-b:subagent-2" {
			t.Errorf("chat B's task leaked into chat A's ledger: %+v", entry)
		}
	}

	aliased := sm.LedgerEntries([]string{"native:chat-a:chat:3"})
	if len(aliased) != 1 || aliased[0].SessionKey != "native:chat-a:subagent-1" {
		t.Errorf("aliased ledger = %+v, want only native:chat-a:subagent-1", aliased)
	}

	child := sm.LedgerEntries([]string{"native:chat-a:subagent-1"})
	if len(child) != 1 || child[0].SessionKey != "native:chat-a:subagent-1" {
		t.Errorf("child ledger = %+v, want the parent's task", child)
	}

	if all := sm.LedgerEntries(nil); len(all) != 3 {
		t.Errorf("unscoped ledger = %d rows, want 3 (%+v)", len(all), all)
	}
}

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

	got = nil
	sm.LedgerEntries(nil)
	if got != nil {
		t.Errorf("lister prefixes = %v, want nil for an unscoped ledger", got)
	}
}

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
