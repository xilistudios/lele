// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors
//
// Tests for the remote-tool usage counter: the usageStore itself (persistence,
// corruption tolerance, concurrency, file mode) and the Manager seam that feeds
// it (ToolUsage + CallRemote counting rules).

package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// --- usageStore ------------------------------------------------------------

// usagePath returns the counter file path inside a fresh temp dir.
func usagePath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "mcp_usage.json")
}

// readUsageFile returns the raw bytes of the counter file.
func readUsageFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read usage file %s: %v", path, err)
	}
	return string(data)
}

// TestUsageStorePersistsAcrossReload is the round trip: counters written by one
// store are visible to a second store built on the same path — that is what
// makes the ranking survive a restart.
func TestUsageStorePersistsAcrossReload(t *testing.T) {
	path := usagePath(t)

	first := loadUsageStore(path)
	first.inc("srv", "echo")
	first.inc("srv", "echo")
	first.inc("srv", "summary")
	first.inc("other", "ping")

	want := map[string]map[string]uint64{
		"srv":   {"echo": 2, "summary": 1},
		"other": {"ping": 1},
	}
	if got := countsOf(first); !reflect.DeepEqual(got, want) {
		t.Fatalf("in-memory counts = %v, want %v", got, want)
	}

	second := loadUsageStore(path)
	if got := countsOf(second); !reflect.DeepEqual(got, want) {
		t.Errorf("reloaded counts = %v, want %v", got, want)
	}

	// A further increment on the reloaded store must build on the loaded
	// values instead of resetting them.
	second.inc("srv", "echo")
	if got := second.countsFor("srv")["echo"]; got != 3 {
		t.Errorf("echo = %d after reload+inc, want 3", got)
	}
}

// countsOf renders every server's counts for whole-store comparisons.
func countsOf(s *usageStore) map[string]map[string]uint64 {
	out := make(map[string]map[string]uint64)
	s.mu.Lock()
	defer s.mu.Unlock()
	for server, tools := range s.counts {
		copied := make(map[string]uint64, len(tools))
		for remote, count := range tools {
			copied[remote] = count
		}
		out[server] = copied
	}
	return out
}

// TestUsageStoreMemoryOnly pins the empty-path contract: counters work, and no
// file is ever created.
func TestUsageStoreMemoryOnly(t *testing.T) {
	dir := t.TempDir()
	store := loadUsageStore("")
	if store.path != "" {
		t.Fatalf("memory-only store path = %q, want empty", store.path)
	}

	store.inc("srv", "echo")
	store.inc("srv", "echo")

	if got := store.countsFor("srv"); !reflect.DeepEqual(got, map[string]uint64{"echo": 2}) {
		t.Errorf("counts = %v, want echo:2", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("memory-only store created files: %v", entryNames(entries))
	}
}

// entryNames lists directory entry names for failure messages.
func entryNames(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TestUsageStoreLoad covers every start-up shape of the counter file: absent,
// unreadable-as-JSON, structurally odd and valid.
func TestUsageStoreLoad(t *testing.T) {
	cases := []struct {
		name    string
		content string // empty means "do not create the file"
		create  bool
		want    map[string]uint64 // counts expected for server "srv"
	}{
		{
			name: "missing file starts empty",
		},
		{
			name:    "corrupt json starts empty",
			content: "{not json at all",
			create:  true,
		},
		{
			name:    "empty file starts empty",
			content: "",
			create:  true,
		},
		{
			name:    "null server value is skipped",
			content: `{"srv": null}`,
			create:  true,
		},
		{
			name:    "empty server object is skipped",
			content: `{"srv": {}}`,
			create:  true,
		},
		{
			name:    "valid file is loaded",
			content: `{"srv": {"echo": 7, "summary": 2}}`,
			create:  true,
			want:    map[string]uint64{"echo": 7, "summary": 2},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := usagePath(t)
			if tc.create {
				writeFileAt(t, path, tc.content)
			}

			want := tc.want
			if want == nil {
				want = map[string]uint64{}
			}
			store := loadUsageStore(path)
			if got := store.countsFor("srv"); !reflect.DeepEqual(got, want) {
				t.Errorf("counts after load = %v, want %v", got, want)
			}

			// Whatever the file looked like, the store must be usable: an
			// increment works and rewrites the file as valid JSON.
			store.inc("srv", "echo")
			store.inc("srv", "probe")
			reloaded := loadUsageStore(path)
			wantEcho := tc.want["echo"] + 1
			if got := reloaded.countsFor("srv")["echo"]; got != wantEcho {
				t.Errorf("echo after inc+reload = %d, want %d", got, wantEcho)
			}
			if got := reloaded.countsFor("srv")["probe"]; got != 1 {
				t.Errorf("probe after inc+reload = %d, want 1", got)
			}
		})
	}
}

// TestUsageStoreCorruptFileOverwritten proves the corrupt file is replaced by a
// readable one on the next increment.
func TestUsageStoreCorruptFileOverwritten(t *testing.T) {
	path := usagePath(t)
	writeFileAt(t, path, "garbage{{{")

	store := loadUsageStore(path)
	store.inc("srv", "echo")

	want := "{\n  \"srv\": {\n    \"echo\": 1\n  }\n}\n"
	if got := readUsageFile(t, path); got != want {
		t.Errorf("usage file =\n%q\nwant\n%q", got, want)
	}
}

// TestUsageStorePerServerIsolation: one server's counts never leak into
// another's.
func TestUsageStorePerServerIsolation(t *testing.T) {
	path := usagePath(t)
	store := loadUsageStore(path)
	store.inc("a", "echo")
	store.inc("b", "summary")
	store.inc("b", "summary")

	if got := store.countsFor("a"); !reflect.DeepEqual(got, map[string]uint64{"echo": 1}) {
		t.Errorf("countsFor(a) = %v, want echo:1 only", got)
	}
	if got := store.countsFor("b"); !reflect.DeepEqual(got, map[string]uint64{"summary": 2}) {
		t.Errorf("countsFor(b) = %v, want summary:2 only", got)
	}
	if got := store.countsFor("nobody"); got == nil || len(got) != 0 {
		t.Errorf("countsFor(unknown) = %v, want an empty map", got)
	}

	// The isolation must also hold on disk.
	reloaded := loadUsageStore(path)
	if got := reloaded.countsFor("a"); len(got) != 1 || got["echo"] != 1 {
		t.Errorf("reloaded countsFor(a) = %v, want echo:1 only", got)
	}
}

// TestUsageStoreCountsForReturnsCopy: the caller owns the returned map.
func TestUsageStoreCountsForReturnsCopy(t *testing.T) {
	store := loadUsageStore(usagePath(t))
	store.inc("srv", "echo")

	got := store.countsFor("srv")
	got["echo"] = 999
	got["injected"] = 1

	if want := map[string]uint64{"echo": 1}; !reflect.DeepEqual(store.countsFor("srv"), want) {
		t.Errorf("store mutated through the returned map: %v, want %v", store.countsFor("srv"), want)
	}
}

// TestUsageStoreConcurrentInc hammers one key from many goroutines (run under
// -race): the count must be exact and the file must stay loadable.
func TestUsageStoreConcurrentInc(t *testing.T) {
	path := usagePath(t)
	store := loadUsageStore(path)

	const goroutines = 50
	const perGoroutine = 100

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < perGoroutine; j++ {
				store.inc("srv", "echo")
			}
		}()
	}
	close(start)
	wg.Wait()

	want := uint64(goroutines * perGoroutine)
	if got := store.countsFor("srv")["echo"]; got != want {
		t.Errorf("echo = %d, want %d", got, want)
	}
	if got := loadUsageStore(path).countsFor("srv")["echo"]; got != want {
		t.Errorf("persisted echo = %d, want %d", got, want)
	}
}

// TestUsageStoreConcurrentManagersKeepEveryIncrement is the CONCURRENT half of
// the shared-file contract, the case the sequential manager test cannot reach:
// several stores over the same path (two managers in one process, or two lele
// processes) each read the file, merge their own deltas and rename. Without a
// cross-process lock on the file, two of them can read the same snapshot and
// the last rename drops the other's increments — so the totals below are the
// assertion that the lock (see lockUsageFile) is what protects them.
func TestUsageStoreConcurrentManagersKeepEveryIncrement(t *testing.T) {
	path := usagePath(t)

	const writers = 6
	const perWriter = 20

	// One store per writer: separate snapshots and separate mu, exactly like
	// separate managers/processes sharing the file.
	stores := make([]*usageStore, writers)
	for i := range stores {
		stores[i] = loadUsageStore(path)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < perWriter; j++ {
				stores[i].inc("srv", fmt.Sprintf("tool%d", i))
			}
		}(i)
	}
	close(start)
	wg.Wait()

	// Every writer's own tool must have kept EVERY increment, and the file (the
	// restart view) must agree.
	persisted := loadUsageStore(path).countsFor("srv")
	for i := 0; i < writers; i++ {
		name := fmt.Sprintf("tool%d", i)
		if got := persisted[name]; got != perWriter {
			t.Errorf("persisted %s = %d, want %d (a concurrent write was lost)", name, got, perWriter)
		}
	}
	// No writer may have invented counts for another writer's tool.
	if got := len(persisted); got > writers {
		t.Errorf("%d distinct tools persisted, want at most %d: %v", got, writers, persisted)
	}
	// And the counters file must be gone with its sidecar (no stale lock left).
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Error("the usage lock sidecar must be released after the last write")
	}
}

// TestUsageLockReclaimsStaleSidecar: a sidecar left behind by a process that
// died while holding the lock must not degrade every later write to unlocked
// (or block it): an old sidecar is taken over, the counter still lands on disk
// and the sidecar is released again.
func TestUsageLockReclaimsStaleSidecar(t *testing.T) {
	path := usagePath(t)
	lockPath := path + ".lock"

	// A sidecar with an old mtime == a holder that crashed long ago.
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatalf("seed the sidecar: %v", err)
	}
	old := time.Now().Add(-2 * usageLockStale)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatalf("age the sidecar: %v", err)
	}

	store := loadUsageStore(path)
	store.inc("srv", "echo")

	if got := loadUsageStore(path).countsFor("srv")["echo"]; got != 1 {
		t.Errorf("persisted echo = %d, want 1 (the stale lock must not lose the write)", got)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("the stale sidecar must be released after the write")
	}
}

// TestUsageStoreRetriesFailedWrites pins the delta bookkeeping: a write that
// fails must keep its increments pending so the next call applies them, instead
// of silently dropping them (or, worse, re-applying them twice).
func TestUsageStoreRetriesFailedWrites(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_usage.json")
	store := loadUsageStore(path)

	store.inc("srv", "echo") // creates the file with echo=1

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir read-only: %v", err)
	}
	store.inc("srv", "echo") // the write fails; the increment stays in memory
	if got := store.countsFor("srv")["echo"]; got != 2 {
		t.Errorf("in-memory echo = %d after a failed write, want 2", got)
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("restore dir permissions: %v", err)
	}
	store.inc("srv", "echo") // retried: applies BOTH pending increments
	if got := loadUsageStore(path).countsFor("srv")["echo"]; got != 3 {
		t.Errorf("persisted echo = %d, want 3 (the failed increment must be retried exactly once)", got)
	}
}

// TestUsageStoreNilReceiver: a Manager built without a store still works.
func TestUsageStoreNilReceiver(t *testing.T) {
	var store *usageStore
	store.inc("srv", "echo") // must not panic
	if got := store.countsFor("srv"); got != nil {
		t.Errorf("nil store countsFor = %v, want nil", got)
	}
}

// TestUsageStoreFileMode: the counter file stays user-private and no temp file
// survives a write.
func TestUsageStoreFileMode(t *testing.T) {
	path := usagePath(t)
	store := loadUsageStore(path)
	store.inc("srv", "echo")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat usage file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("usage file mode = %o, want 600", got)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "mcp_usage.json" {
		t.Errorf("dir entries = %v, want only mcp_usage.json (no temp leftovers)", entryNames(entries))
	}
}

// TestToolUsageFileLocation pins the on-disk convention through the Manager:
// the counters live next to mcp.json inside the lele directory, exactly where a
// restarted process looks for them. (The empty-LeleDir case — memory-only
// counting, no file at all — is covered end to end by
// TestToolUsageMemoryOnlyWithoutLeleDir.)
func TestToolUsageFileLocation(t *testing.T) {
	paths := basicFixture(t)
	d := &fakeDialer{setup: func(n int, conn *fakeConn) { conn.callResult = okResult("ok") }}
	mgr := NewManager(paths, d)
	t.Cleanup(func() { _ = mgr.Close() })

	if _, err := mgr.CallRemote(context.Background(), "srv", "echo", nil); err != nil {
		t.Fatalf("CallRemote: %v", err)
	}

	want := filepath.Join(paths.LeleDir, "mcp_usage.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("the usage file must be created by a Manager call at %s: %v", want, err)
	}
	if got := readUsageFile(t, want); !strings.Contains(got, `"srv"`) || !strings.Contains(got, `"echo": 1`) {
		t.Errorf("usage file %s = %s, want srv.echo counted once", want, got)
	}
}

// --- Manager seam ----------------------------------------------------------

// okResult is a plain successful tool result (no application error).
func okResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Type: "text", Text: text}}}
}

// TestToolUsageCallOutcomes pins exactly which calls are counted: a call that
// reaches the server counts once (application errors included), while a
// failure that never produced a result counts nothing.
func TestToolUsageCallOutcomes(t *testing.T) {
	cases := []struct {
		name      string
		server    string // defaults to "srv"
		dialErrs  []error
		setup     func(n int, conn *fakeConn)
		cancelled bool
		want      uint64 // expected count of srv.echo
		wantDials int
	}{
		{
			name:      "successful call is counted",
			setup:     func(n int, conn *fakeConn) { conn.callResult = okResult("pong") },
			want:      1,
			wantDials: 1,
		},
		{
			name: "tool result with IsError still counts",
			setup: func(n int, conn *fakeConn) {
				conn.callResult = &mcp.CallToolResult{
					IsError: true,
					Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "boom"}},
				}
			},
			want:      1,
			wantDials: 1,
		},
		{
			name: "reconnect once then success counts exactly once",
			setup: func(n int, conn *fakeConn) {
				if n == 1 {
					conn.callErrs = []error{transportFail("connection lost")}
					return
				}
				conn.callResult = okResult("pong")
			},
			want:      1,
			wantDials: 2,
		},
		{
			name: "transport failure after retry is not counted",
			setup: func(n int, conn *fakeConn) {
				conn.callErrs = []error{transportFail("still dead")}
			},
			want:      0,
			wantDials: 2,
		},
		{
			name: "MCP error response is not counted",
			setup: func(n int, conn *fakeConn) {
				conn.callErrs = []error{errors.New("invalid arguments")}
			},
			want:      0,
			wantDials: 1,
		},
		{
			name:      "dial failure is not counted",
			dialErrs:  []error{errors.New("dial exploded")},
			want:      0,
			wantDials: 1,
		},
		{
			name:      "cancelled context is not counted",
			setup:     func(n int, conn *fakeConn) { conn.callErrs = []error{transportFail("context canceled")} },
			cancelled: true,
			want:      0,
			wantDials: 1,
		},
		{
			name:      "unknown server is not counted",
			server:    "ghost",
			want:      0,
			wantDials: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDialer{errs: tc.dialErrs, setup: tc.setup}
			mgr := NewManager(basicFixture(t), d)
			t.Cleanup(func() { _ = mgr.Close() })

			server := tc.server
			if server == "" {
				server = "srv"
			}
			ctx := context.Background()
			if tc.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, _ = mgr.CallRemote(ctx, server, "echo", nil)

			if got := mgr.ToolUsage("srv")["echo"]; got != tc.want {
				t.Errorf("ToolUsage(srv)[echo] = %d, want %d", got, tc.want)
			}
			if got := d.dialCount(); got != tc.wantDials {
				t.Errorf("dials = %d, want %d", got, tc.wantDials)
			}
		})
	}
}

// TestToolUsageKeysAreRemoteNames: counts are keyed by the REMOTE tool name,
// which is what a discovery ranking needs (the namespaced registry name is
// derived later).
func TestToolUsageKeysAreRemoteNames(t *testing.T) {
	d := &fakeDialer{setup: func(n int, conn *fakeConn) { conn.callResult = okResult("ok") }}
	mgr := NewManager(basicFixture(t), d)
	t.Cleanup(func() { _ = mgr.Close() })

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := mgr.CallRemote(ctx, "srv", "echo", map[string]interface{}{"q": "x"}); err != nil {
			t.Fatalf("CallRemote: %v", err)
		}
	}
	if _, err := mgr.CallRemote(ctx, "srv", "summary", nil); err != nil {
		t.Fatalf("CallRemote summary: %v", err)
	}

	want := map[string]uint64{"echo": 3, "summary": 1}
	if got := mgr.ToolUsage("srv"); !reflect.DeepEqual(got, want) {
		t.Errorf("ToolUsage(srv) = %v, want %v", got, want)
	}
	for name := range mgr.ToolUsage("srv") {
		if strings.HasPrefix(name, "mcp_") {
			t.Errorf("ToolUsage key %q is a registry name, want the remote name", name)
		}
	}
	if got := mgr.ToolUsage("other"); got == nil || len(got) != 0 {
		t.Errorf("ToolUsage(other) = %v, want an empty map", got)
	}
}

// TestToolUsagePersistsToLeleDir proves the Manager wires the store to
// <LeleDir>/mcp_usage.json and that a fresh Manager over the same directory
// sees the counts (restart survival).
func TestToolUsagePersistsToLeleDir(t *testing.T) {
	paths := basicFixture(t)
	d := &fakeDialer{setup: func(n int, conn *fakeConn) { conn.callResult = okResult("ok") }}
	mgr := NewManager(paths, d)

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := mgr.CallRemote(ctx, "srv", "echo", nil); err != nil {
			t.Fatalf("CallRemote: %v", err)
		}
	}
	_ = mgr.Close()

	file := filepath.Join(paths.LeleDir, "mcp_usage.json")
	if got := readUsageFile(t, file); !strings.Contains(got, `"echo": 2`) {
		t.Errorf("usage file %s = %s, want echo counted twice", file, got)
	}

	restarted := NewManager(paths, &fakeDialer{})
	t.Cleanup(func() { _ = restarted.Close() })
	if got := restarted.ToolUsage("srv")["echo"]; got != 2 {
		t.Errorf("ToolUsage after restart = %d, want 2", got)
	}
}

// TestToolUsageSharedLeleDirKeepsEveryManagerIncrements is THE shared-file
// test: two managers over the same lele directory (an agent's manager plus a
// probe, or a second process) both start with an empty snapshot, so writing a
// per-manager snapshot verbatim would make the second call overwrite the
// first one's counts. The merged write must keep both.
func TestToolUsageSharedLeleDirKeepsEveryManagerIncrements(t *testing.T) {
	paths := basicFixture(t)
	callOK := func(n int, conn *fakeConn) { conn.callResult = okResult("ok") }

	// Both managers open the directory BEFORE either records usage: neither
	// has the other's counts in memory.
	first := NewManager(paths, &fakeDialer{setup: callOK})
	second := NewManager(paths, &fakeDialer{setup: callOK})
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })

	ctx := context.Background()
	if _, err := first.CallRemote(ctx, "srv", "echo", nil); err != nil {
		t.Fatalf("first CallRemote: %v", err)
	}
	if _, err := second.CallRemote(ctx, "srv", "summary", nil); err != nil {
		t.Fatalf("second CallRemote: %v", err)
	}

	want := map[string]uint64{"echo": 1, "summary": 1}
	// The writing manager adopts the merged file, so its own view keeps both.
	if got := second.ToolUsage("srv"); !reflect.DeepEqual(got, want) {
		t.Errorf("ToolUsage(srv) after the merge = %v, want %v", got, want)
	}
	// And the merge is what survived on disk (the restart view).
	restarted := NewManager(paths, &fakeDialer{})
	t.Cleanup(func() { _ = restarted.Close() })
	if got := restarted.ToolUsage("srv"); !reflect.DeepEqual(got, want) {
		t.Errorf("persisted ToolUsage(srv) = %v, want %v (the first manager's count was lost)", got, want)
	}
}

// TestToolUsageMemoryOnlyWithoutLeleDir: a Manager with no lele directory (a
// probe or a manager built by hand) keeps counting in memory and writes nothing
// — the counters are served from the project layer only.
func TestToolUsageMemoryOnlyWithoutLeleDir(t *testing.T) {
	proj := t.TempDir()
	writeFileAt(t, filepath.Join(proj, ".lele", "mcp.json"),
		`{"mcpServers": {"srv": {"command": "srv-bin"}}}`)

	d := &fakeDialer{setup: func(n int, conn *fakeConn) { conn.callResult = okResult("ok") }}
	mgr := NewManager(Paths{Cwd: proj}, d)
	t.Cleanup(func() { _ = mgr.Close() })

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := mgr.CallRemote(ctx, "srv", "echo", nil); err != nil {
			t.Fatalf("CallRemote: %v", err)
		}
	}
	if got := mgr.ToolUsage("srv")["echo"]; got != 2 {
		t.Errorf("ToolUsage(srv)[echo] = %d, want 2 (in-memory counting)", got)
	}
	for _, dir := range []string{proj, filepath.Join(proj, ".lele")} {
		if _, err := os.Stat(filepath.Join(dir, "mcp_usage.json")); !os.IsNotExist(err) {
			t.Errorf("a usage file was written to %s, want memory-only (err=%v)", dir, err)
		}
	}
}
