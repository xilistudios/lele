package tui

// T14 tests: minimal invalidation after a compaction.
//
// Context: the compaction result handler used to wipe the per-message render
// cache (m.msgRenderCacheLines = nil) and the rendered base, so EVERY /compact
// re-rendered the whole render window (~200 messages) through glamour even
// though compaction only flips ExcludeFromContext flags and evicts the excluded
// prefix: the content — and therefore the messageFingerprint — of every
// surviving message is untouched. With several compactions per session that cost
// was O(compactions × window × glamour), with glamour measured at ~20 % of the
// rebuild CPU.
//
// The contract pinned here:
//
//  1. dispatching compactResultMsg re-renders NOTHING whose fingerprint did not
//     change (TestCompactResult_ReusesUnchangedMessageRenders). The count is
//     taken through the cache itself, with the same poisoning trick
//     cache_eviction_test.go uses: every cached entry is replaced by a unique
//     marker line, so an entry that is re-rendered (cache miss) loses its marker
//     while a reused one (cache hit) carries it into the new base. Zero missing
//     markers ⇒ zero glamour calls for those messages.
//  2. the frame after a compaction is still correct: the refreshed archived
//     prefix is painted, the surviving residents are unchanged, the
//     "↑ N earlier messages" banner matches the new archive
//     (TestCompactResult_PaintsRefreshedArchivePrefix), and the next frame
//     rebuilds nothing (TestCompactResult_LeavesViewportUpToDate, the T3
//     invariant: the rebuild records the materialized fingerprint).
//  3. the ONE render the fingerprint cannot vouch for — the last message's, whose
//     tool-call rows are suppressed while a tool executes — cannot be served from
//     the per-message cache at all, because a suppressed render is never stored
//     (buildRenderedHistoryLines, isExecutingMessage; see
//     render_cache_suppression_test.go for the append case), so the rows are back
//     in the frame once the tool stops executing
//     (TestCompactResult_ReRendersTransientLastMessage).
//
// Red-checks. Each one is a single mutation; the listed test is the only one that
// has to fail, and it was run both ways (mutation ⇒ fail, restored ⇒ pass):
//
//	a) restoring `m.msgRenderCacheLines = nil` ⇒ (1) fails: "re-rendered 24 of
//	   24 cached messages", i.e. the whole window went through glamour again —
//	   the regression T14 exists to remove;
//	b) removing the `if !isExecutingMessage` guard around the cache store in
//	   buildRenderedHistoryLines (viewport.go) ⇒ (3) fails: the suppressed render
//	   of the last message is stored under its fingerprint and then served from
//	   the cache after the compaction, so its tool-call rows stay invisible. This
//	   guard replaced the handler-side cache drop (R3.2), which could only heal an
//	   already-poisoned rebuild of the LAST message and not a message that was
//	   appended during the suppression window;
//	c) dropping the m.reloadSessions() call ⇒ (2) fails ("did not load the
//	   archived prefix: total=0 prefix=0"): the reload is the Update-path half of
//	   the invalidation, and with the cache wipe gone it is what the refreshed
//	   archive rides on.

import (
	"sort"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// frameHasText reports whether a rendered frame contains want, comparing both
// sides with collapsed whitespace: glamour re-flows markdown paragraphs, so the
// raw newlines of a message never survive into the frame.
func frameHasText(frame, want string) bool {
	collapse := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	return strings.Contains(collapse(frame), collapse(want))
}

// renderCacheMarkers replaces every cached per-message render with a unique
// marker line and returns fingerprint → marker. A rebuild that re-renders a
// message (cache miss) drops its marker from the base; a rebuild served from the
// cache (hit) carries it over (buildRenderedHistoryLines copies hit entries into
// the pruned cache).
func renderCacheMarkers(m *Model) map[string]string {
	markers := make(map[string]string, len(m.msgRenderCacheLines))
	for fp := range m.msgRenderCacheLines {
		marker := "RENDER-CACHE-HIT:" + fp
		markers[fp] = marker
		m.msgRenderCacheLines[fp] = []string{marker}
	}
	return markers
}

// reRenderedMarkers returns, sorted, the fingerprints whose marker is missing
// from the materialized base — i.e. the messages the last rebuild re-rendered.
func reRenderedMarkers(m *Model, markers map[string]string) []string {
	base := strings.Join(m.viewport.baseLines, "\n")
	var rerendered []string
	for fp, marker := range markers {
		if !strings.Contains(base, marker) {
			rerendered = append(rerendered, fp)
		}
	}
	sort.Strings(rerendered)
	return rerendered
}

// compactSecondTimeForTest evicts the older resident messages of key the way a
// real compaction does (ExcludeOldMessagesFromContext + EvictExcludedMessages),
// keeping `keep` messages resident.
func compactSecondTimeForTest(t *testing.T, m *Model, key string, keep int) {
	t.Helper()
	m.sessionMgr.ExcludeOldMessagesFromContext(key, keep)
	if err := m.sessionMgr.Save(key); err != nil {
		t.Fatalf("save after exclude: %v", err)
	}
	if evicted := m.sessionMgr.EvictExcludedMessages(key); evicted <= 0 {
		t.Fatalf("second compaction evicted %d messages, want > 0", evicted)
	}
}

// TestCompactResult_ReusesUnchangedMessageRenders is the core T14 contract: a
// compaction must not re-render a single message whose fingerprint did not
// change. Markers are poisoned into the cache for the whole render window, then
// a second compaction is applied and the real compactResultMsg is dispatched
// through Update; every marker must still be in the repainted base.
func TestCompactResult_ReusesUnchangedMessageRenders(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:t14-reuse"

	// 12 pairs = 24 messages; keep 5 → 19 evicted, 5 resident. This is exactly
	// the state a first /compact leaves behind, with the archived prefix loaded.
	seedEvictionSession(t, m, key, 12, 5)
	m.currentKey = key
	m.showWelcome = false
	m.refreshArchivedHistory()
	renderLazyModel(t, m)
	m.updateViewport() // warm: the cache holds the whole live window

	if len(m.archivedPrefix) == 0 {
		t.Fatal("precondition: the archived prefix must be loaded")
	}
	if len(m.msgRenderCacheLines) == 0 {
		t.Fatal("precondition: the render cache must be populated")
	}
	markers := renderCacheMarkers(m)
	baseLinesBefore := m.viewport.baseLines

	// Second compaction: 5 resident → 2 kept, 3 more evicted into the archive.
	compactSecondTimeForTest(t, m, key, 2)

	if _, cmd := m.Update(compactResultMsg{sessionKey: key, result: "✅ Compacted session"}); cmd != nil {
		_ = cmd() // drain any trailing command
	}

	rerendered := reRenderedMarkers(m, markers)
	if len(rerendered) != 0 {
		t.Fatalf("compactResultMsg re-rendered %d of %d cached messages, want 0 — the cache must be reused for every fingerprint that did not change (fingerprints: %v)",
			len(rerendered), len(markers), rerendered)
	}
	// Zero missing markers also proves the rebuild really happened: the markers
	// only reach the frame through a rebuilt base (the warm base still held the
	// real render), and both halves of the window are served from the cache —
	// the rows that were already archived and the ones this compaction just
	// moved there (an archived row reuses the fingerprint of the resident render
	// it had, same content, same width).
	if &m.viewport.baseLines[0] == &baseLinesBefore[0] {
		t.Fatal("the compaction did not rebuild the base: the refresh would never reach the frame")
	}
	if got, want := len(m.msgRenderCacheLines), len(markers); got != want {
		t.Fatalf("render cache holds %d entries after the compaction, want %d (live window only, no orphans)", got, want)
	}
	if m.archivedTotal != m.sessionMgr.GetEvictedMessageCount(key) {
		t.Fatalf("archivedTotal = %d, want the session's evicted count %d", m.archivedTotal, m.sessionMgr.GetEvictedMessageCount(key))
	}
}

// TestCompactResult_PaintsRefreshedArchivePrefix is the correctness half: the
// frame after a compaction must show the refreshed archived prefix, the surviving
// residents and the "↑ N earlier messages" banner that matches the new archive —
// and it must reach the frame through the Update path only (the archive load is
// refreshArchivedHistory, which never runs from View()).
//
// The scenario is the real one: a warm, fully-resident chat is compacted behind
// the TUI's back (ExcludeOldMessagesFromContext + EvictExcludedMessages, exactly
// what summarizeSession does), and only the result message is dispatched. The
// archived prefix was never loaded, so its rows can only appear if the handler's
// reloadSessions refresh ran and the base was rebuilt for the new archive key.
func TestCompactResult_PaintsRefreshedArchivePrefix(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:t14-correct"

	// 12 pairs = 24 resident messages, no eviction yet.
	seedLazySession(t, m, key, 12)
	m.currentKey = key
	m.showWelcome = false
	m.maxRenderedMessages = 10
	// -1 lets the render window be recomputed for that cap (a value >= 0 is kept
	// as-is by buildRenderedHistoryLines), so the history starts folded behind
	// the "↑ N earlier messages" banner.
	m.renderStartIdx = -1
	renderLazyModel(t, m)
	m.updateViewport()

	if len(m.archivedPrefix) != 0 || m.archivedTotal != 0 {
		t.Fatalf("precondition: nothing may be archived yet (prefix=%d total=%d)", len(m.archivedPrefix), m.archivedTotal)
	}
	baseLinesBefore := m.viewport.baseLines
	if header := headerCount(renderedBase(m)); header < 0 {
		t.Fatal("precondition: the long pre-compaction history must show the banner")
	}

	// The compaction: keep 5 → 19 evicted. No TUI call in between.
	compactSecondTimeForTest(t, m, key, 5)
	if _, cmd := m.Update(compactResultMsg{sessionKey: key, result: "✅ Compacted session"}); cmd != nil {
		_ = cmd()
	}

	// The archive was refreshed from the Update path (reloadSessions →
	// refreshArchivedHistory) and NOT from View(): nothing else ran.
	evicted := m.sessionMgr.GetEvictedMessageCount(key)
	if evicted != 19 {
		t.Fatalf("evicted = %d, want 19", evicted)
	}
	if m.archivedTotal != evicted || len(m.archivedPrefix) == 0 {
		t.Fatalf("compaction result did not load the archived prefix: total=%d prefix=%d (evicted=%d)",
			m.archivedTotal, len(m.archivedPrefix), evicted)
	}

	// The base was rebuilt for the new archive key (a stale base would still hold
	// the pre-compaction window, where no archived row exists).
	if &m.viewport.baseLines[0] == &baseLinesBefore[0] {
		t.Fatal("compactResultMsg left the base untouched: the refreshed archived prefix never reached the frame")
	}
	rendered := renderedBase(m)
	plain := stripAnsi(rendered)

	// Banner accounting: it counts the in-memory window plus the archived rows
	// still not loaded (both halves moved with the compaction).
	if got := headerCount(rendered); got != m.renderStartIdx+m.archivedHiddenCount() {
		t.Fatalf("banner count = %d, want renderStartIdx(%d)+archivedHidden(%d)",
			got, m.renderStartIdx, m.archivedHiddenCount())
	}
	// The oldest row of the loaded archive is painted: it is only reachable
	// through the archived prefix (its resident counterpart was evicted), so its
	// presence proves the refresh reached the frame.
	if len(m.archivedPrefix) == 0 {
		t.Fatal("expected a non-empty archived prefix after the compaction")
	}
	if want := m.archivedPrefix[0].Content; !frameHasText(plain, want) {
		t.Fatalf("archived row %q missing from the repainted base:\n%s", want, plain)
	}
	// …and every surviving resident message is unchanged in the frame.
	for _, msg := range m.sessionMgr.GetHistoryView(key) {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		if !frameHasText(plain, msg.Content) {
			t.Fatalf("resident message %q missing from the repainted base:\n%s", msg.Content, plain)
		}
	}

	// Nothing in the frame is stale: the next frame rebuilds nothing (T3).
	if !m.viewportContentUpToDate(m.frameCount) {
		t.Fatal("the post-compaction frame is not considered up to date")
	}
}

// TestCompactResult_LeavesViewportUpToDate pins the T3 side of the change: the
// rebuild the compaction triggers records the materialized fingerprint, so the
// frames that follow must be quiet — no second rebuild for content that is
// already on screen. (The old invalidation forced one extra full rebuild in the
// frame after the handler.)
func TestCompactResult_LeavesViewportUpToDate(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:t14-t3"

	seedEvictionSession(t, m, key, 12, 5)
	m.currentKey = key
	m.showWelcome = false
	m.refreshArchivedHistory()
	renderLazyModel(t, m)
	m.updateViewport()

	compactSecondTimeForTest(t, m, key, 2)

	rebuilds := viewportRebuildCounter(t, m)
	if _, cmd := m.Update(compactResultMsg{sessionKey: key, result: "✅ Compacted session"}); cmd != nil {
		_ = cmd()
	}
	if got := rebuilds(); len(got) != 1 {
		t.Fatalf("compactResultMsg rebuilt the viewport %v times, want exactly 1 (the reloadSessions rebuild)", got)
	}
	if !m.viewportContentUpToDate(m.frameCount) {
		t.Fatal("the post-compaction rebuild did not record the materialized fingerprint: the next frame would rebuild again")
	}

	rebuilds = viewportRebuildCounter(t, m)
	for i := 0; i < 3; i++ {
		if frame := m.View(); frame == "" {
			t.Fatal("View() returned an empty frame after the compaction")
		}
	}
	if got := rebuilds(); len(got) != 0 {
		t.Fatalf("frames after the compaction rebuilt %v, want 0", got)
	}

	// And the Update-side guard agrees (one fingerprint, T3).
	if !m.shouldSkipViewportUpdate() {
		t.Fatal("the Update-side skip guard would rebuild again after the compaction")
	}
}

// TestCompactResult_ReRendersTransientLastMessage covers the one render the
// per-message fingerprint cannot vouch for. buildRenderedHistoryLines suppresses
// the tool-call rows of the LAST message while a tool is executing, and /compact
// runs exactly in that transient state (it sets processing + a tool action before
// the backend call, and clears both only when this result message arrives), so a
// rebuild that lands between the eviction and the result message must not have
// stored the suppressed variant: R3.2 skips the cache store for a suppressed
// render, so the rebuild the result triggers re-renders the message in the state
// that is now on screen and the rows are back in the frame. The test used to
// poison the cache entry by hand and assert the handler's cache drop removed it
// (dropTransientLastMessageRender, now deleted as dead code).
func TestCompactResult_ReRendersTransientLastMessage(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:t14-transient"

	// Resident history whose LAST message is an assistant with tool calls: the
	// exact shape that reaches the suppression.
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	for i := 0; i < 6; i++ {
		m.sessionMgr.AddMessage(key, "user", "Evict question "+string(rune('0'+i))+"?")
		m.sessionMgr.AddMessage(key, "assistant", "Evict answer "+string(rune('0'+i))+".")
	}
	lastWithToolCall := providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{
			{Name: "read", Function: &providers.FunctionCall{Name: "read", Arguments: `{"path":"x"}`}},
		},
	}

	m.currentKey = key
	m.showWelcome = false
	m.maxRenderedMessages = 200
	m.refreshArchivedHistory()
	renderLazyModel(t, m)
	m.updateViewport()

	// The turn commits while the tool it calls is executing: the message was
	// never rendered unsuppressed, which is the state /compact finds.
	m.processing = true
	m.currentToolAction = "read: x"
	m.sessionMgr.AddFullMessage(key, lastWithToolCall)
	m.updateViewport()

	last := m.sessionMgr.GetHistoryView(key)
	lastFp := messageFingerprint(last[len(last)-1], m.viewport.Width)
	if cached, ok := m.msgRenderCacheLines[lastFp]; ok {
		t.Fatalf("precondition: the suppressed render of the last message must not be cached in the first place (R3.2), got %d lines", len(cached))
	}
	if base := viewportBaseText(m); strings.Contains(base, "read:") {
		t.Fatalf("precondition: the tool-call row must be suppressed while the tool executes:\n%s", base)
	}

	// Compaction evicts part of the prefix and the result message arrives; the
	// handler clears processing/currentToolAction, so the suppression window
	// closes with the message still last.
	compactSecondTimeForTest(t, m, key, 2)
	if _, cmd := m.Update(compactResultMsg{sessionKey: key, result: "✅ Compacted session"}); cmd != nil {
		_ = cmd()
	}

	base := viewportBaseText(m)
	if !strings.Contains(base, "read") {
		t.Fatalf("the last message's tool-call row is missing from the frame after the compaction:\n%s", base)
	}
	// The unsuppressed render is what got cached: the guard skips one store, not
	// the entry for the message.
	last = m.sessionMgr.GetHistoryView(key)
	lastFp = messageFingerprint(last[len(last)-1], m.viewport.Width)
	if _, ok := m.msgRenderCacheLines[lastFp]; !ok {
		t.Error("the last message's unsuppressed render is missing from the cache after the compaction")
	}
}

// BenchmarkCompactResultRebuild measures the Update+frame work a /compact result
// costs on a 400-message chat with an already-warm render window (T14 harness).
// Run it before/after the invalidation change:
//
//	go test -run '^$' -bench BenchmarkCompactResultRebuild -benchtime=20x ./pkg/tui/
func BenchmarkCompactResultRebuild(b *testing.B) {
	m := newBenchModel(b)
	key := "tui:chat:bench-compact"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	for i := 0; i < 200; i++ {
		m.sessionMgr.AddMessage(key, "user", "Question with some text about the feature?")
		m.sessionMgr.AddMessage(key, "assistant", "Answer with **markdown** and a `code` span to force glamour work.")
	}
	if err := m.sessionMgr.Save(key); err != nil {
		b.Fatalf("seed save: %v", err)
	}
	m.currentKey = key
	m.showWelcome = false
	m.width, m.height = 200, 50
	_ = m.View()
	m.updateViewport() // warm the whole window

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Re-arm the state a /compact result finds: the prefix is evicted, the
		// render caches are exactly what the previous frame left behind.
		m.sessionMgr.ExcludeOldMessagesFromContext(key, 2)
		m.sessionMgr.EvictExcludedMessages(key)
		m.msgRenderCacheLines = nil
		m.renderedBaseValid = false
		m.renderedBaseKey = ""
		m.renderedBaseMsgCount = -1
		m.renderedBaseHistoryLen = -1
		m.renderedBaseArchiveKey = ""
		m.updateViewport() // first compaction render, as in the real flow
		b.StartTimer()

		updated, cmd := m.Update(compactResultMsg{sessionKey: key, result: "✅ Compacted session"})
		m = updated.(*Model)
		if cmd != nil {
			_ = cmd()
		}
		viewSink = m.View()
	}
}
