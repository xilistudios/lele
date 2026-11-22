package tui

import (
	"sort"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func frameHasText(frame, want string) bool {
	collapse := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	return strings.Contains(collapse(frame), collapse(want))
}

func renderCacheMarkers(m *Model) map[string]string {
	markers := make(map[string]string, len(m.msgRenderCacheLines))
	for fp := range m.msgRenderCacheLines {
		marker := "RENDER-CACHE-HIT:" + fp
		markers[fp] = marker
		m.msgRenderCacheLines[fp] = []string{marker}
	}
	return markers
}

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

func TestCompactResult_ReusesUnchangedMessageRenders(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:t14-reuse"

	seedEvictionSession(t, m, key, 12, 5)
	m.currentKey = key
	m.showWelcome = false
	m.refreshArchivedHistory()
	renderLazyModel(t, m)
	m.updateViewport()

	if len(m.archivedPrefix) == 0 {
		t.Fatal("precondition: the archived prefix must be loaded")
	}
	if len(m.msgRenderCacheLines) == 0 {
		t.Fatal("precondition: the render cache must be populated")
	}
	markers := renderCacheMarkers(m)
	baseLinesBefore := m.viewport.baseLines

	compactSecondTimeForTest(t, m, key, 2)

	if _, cmd := m.Update(compactResultMsg{sessionKey: key, result: "✅ Compacted session"}); cmd != nil {
		_ = cmd()
	}

	rerendered := reRenderedMarkers(m, markers)
	if len(rerendered) != 0 {
		t.Fatalf("compactResultMsg re-rendered %d of %d cached messages, want 0 — the cache must be reused for every fingerprint that did not change (fingerprints: %v)",
			len(rerendered), len(markers), rerendered)
	}
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

func TestCompactResult_PaintsRefreshedArchivePrefix(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:t14-correct"

	seedLazySession(t, m, key, 12)
	m.currentKey = key
	m.showWelcome = false
	m.maxRenderedMessages = 10
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

	compactSecondTimeForTest(t, m, key, 5)
	if _, cmd := m.Update(compactResultMsg{sessionKey: key, result: "✅ Compacted session"}); cmd != nil {
		_ = cmd()
	}

	evicted := m.sessionMgr.GetEvictedMessageCount(key)
	if evicted != 19 {
		t.Fatalf("evicted = %d, want 19", evicted)
	}
	if m.archivedTotal != evicted || len(m.archivedPrefix) == 0 {
		t.Fatalf("compaction result did not load the archived prefix: total=%d prefix=%d (evicted=%d)",
			m.archivedTotal, len(m.archivedPrefix), evicted)
	}

	if &m.viewport.baseLines[0] == &baseLinesBefore[0] {
		t.Fatal("compactResultMsg left the base untouched: the refreshed archived prefix never reached the frame")
	}
	rendered := renderedBase(m)
	plain := stripAnsi(rendered)

	if got := headerCount(rendered); got != m.renderStartIdx+m.archivedHiddenCount() {
		t.Fatalf("banner count = %d, want renderStartIdx(%d)+archivedHidden(%d)",
			got, m.renderStartIdx, m.archivedHiddenCount())
	}
	if len(m.archivedPrefix) == 0 {
		t.Fatal("expected a non-empty archived prefix after the compaction")
	}
	if want := m.archivedPrefix[0].Content; !frameHasText(plain, want) {
		t.Fatalf("archived row %q missing from the repainted base:\n%s", want, plain)
	}
	for _, msg := range m.sessionMgr.GetHistoryView(key) {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		if !frameHasText(plain, msg.Content) {
			t.Fatalf("resident message %q missing from the repainted base:\n%s", msg.Content, plain)
		}
	}

	if !m.viewportContentUpToDate(m.frameCount) {
		t.Fatal("the post-compaction frame is not considered up to date")
	}
}

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

	if !m.shouldSkipViewportUpdate() {
		t.Fatal("the Update-side skip guard would rebuild again after the compaction")
	}
}

func TestCompactResult_ReRendersTransientLastMessage(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:t14-transient"

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

	compactSecondTimeForTest(t, m, key, 2)
	if _, cmd := m.Update(compactResultMsg{sessionKey: key, result: "✅ Compacted session"}); cmd != nil {
		_ = cmd()
	}

	base := viewportBaseText(m)
	if !strings.Contains(base, "read") {
		t.Fatalf("the last message's tool-call row is missing from the frame after the compaction:\n%s", base)
	}
	last = m.sessionMgr.GetHistoryView(key)
	lastFp = messageFingerprint(last[len(last)-1], m.viewport.Width)
	if _, ok := m.msgRenderCacheLines[lastFp]; !ok {
		t.Error("the last message's unsuppressed render is missing from the cache after the compaction")
	}
}

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
	m.updateViewport()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		m.sessionMgr.ExcludeOldMessagesFromContext(key, 2)
		m.sessionMgr.EvictExcludedMessages(key)
		m.msgRenderCacheLines = nil
		m.renderedBaseValid = false
		m.renderedBaseKey = ""
		m.renderedBaseMsgCount = -1
		m.renderedBaseHistoryLen = -1
		m.renderedBaseArchiveKey = ""
		m.updateViewport()
		b.StartTimer()

		updated, cmd := m.Update(compactResultMsg{sessionKey: key, result: "✅ Compacted session"})
		m = updated.(*Model)
		if cmd != nil {
			_ = cmd()
		}
		viewSink = m.View()
	}
}
