package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func fingerprintsOf(msgs []providers.Message, width int) map[string]bool {
	out := make(map[string]bool, len(msgs))
	for _, msg := range msgs {
		if isCompactionSummary(msg) {
			continue
		}
		out[messageFingerprint(msg, width)] = true
	}
	return out
}

func TestMsgRenderCacheEvictedOnCompact(t *testing.T) {
	m := newTestModel(t)

	key := "tui:chat:compact-evict"
	seedLazySession(t, m, key, 10)
	renderLazyModel(t, m)

	if len(m.msgRenderCacheLines) == 0 {
		t.Fatal("precondition: render cache should be populated after first rebuild")
	}
	before := len(m.msgRenderCacheLines)

	hist := m.sessionMgr.GetHistory(key)
	trimmed := append([]providers.Message{
		{Role: "user", Content: "## Summary of Previous Conversation\n\ncompacted context for the test."},
	}, hist[len(hist)-2:]...)
	m.sessionMgr.SetHistory(key, trimmed)

	if _, cmd := m.Update(compactResultMsg{sessionKey: key, result: "ok"}); cmd != nil {
		_ = cmd()
	}

	if m.msgRenderCacheLines == nil {
		t.Fatal("compactResultMsg wiped the whole render cache; unchanged messages must keep their renders (T14)")
	}
	want := fingerprintsOf(m.sessionMgr.GetHistoryView(key), m.viewport.Width)
	if got := len(m.msgRenderCacheLines); got != len(want) {
		t.Fatalf("render cache holds %d entries right after compactResultMsg, want %d (live window only)",
			got, len(want))
	}
	for fp := range m.msgRenderCacheLines {
		if !want[fp] {
			t.Errorf("render cache holds fingerprint %s that is not in the compacted history", fp)
		}
	}
	m.renderedBaseValid = false
	m.updateViewport()

	base := stripAnsi(strings.Join(m.viewport.baseLines, "\n"))
	if !strings.Contains(base, "Answer 9.") {
		t.Errorf("after compact, rendered base should contain the surviving history, got:\n%s", base)
	}
	if strings.Contains(base, "Question number 0") {
		t.Errorf("after compact, rendered base must not contain dropped history")
	}

	after := m.msgRenderCacheLines
	if len(after) == 0 {
		t.Fatal("render cache should be repopulated after the post-compact rebuild")
	}
	if len(after) >= before {
		t.Fatalf("cache not pruned on compact: %d entries after (was %d)", len(after), before)
	}
	want = fingerprintsOf(m.sessionMgr.GetHistoryView(key), m.viewport.Width)
	for fp := range after {
		if !want[fp] {
			t.Errorf("render cache holds fingerprint %s that is not in the compacted history", fp)
		}
	}
	for _, msg := range hist[:len(hist)-2] {
		fp := messageFingerprint(msg, m.viewport.Width)
		if _, ok := after[fp]; ok {
			t.Errorf("dropped pre-compact message fingerprint %s still in render cache", fp)
		}
	}
	for _, msg := range hist[len(hist)-2:] {
		fp := messageFingerprint(msg, m.viewport.Width)
		if _, ok := after[fp]; !ok {
			t.Errorf("surviving message fingerprint %s missing from render cache", fp)
		}
	}
	if len(after) != len(want) {
		t.Fatalf("render cache size = %d, want %d (renderable messages in new history)", len(after), len(want))
	}
}

func TestMsgRenderCacheBounded(t *testing.T) {
	m := newTestModel(t)

	key := "tui:chat:cache-bound"
	const pairs = 160
	seedLazySession(t, m, key, pairs)
	renderLazyModel(t, m)

	m.updateViewport()

	if m.renderStartIdx != 120 {
		t.Fatalf("precondition: expected default render window startIdx=120, got %d", m.renderStartIdx)
	}
	if got, want := len(m.msgRenderCacheLines), m.maxRenderedMessages; got != want {
		t.Fatalf("render cache size = %d, want exactly %d (window size)", got, want)
	}

	hist := m.sessionMgr.GetHistoryView(key)
	inWindow := fingerprintsOf(hist[m.renderStartIdx:], m.viewport.Width)
	for fp := range m.msgRenderCacheLines {
		if !inWindow[fp] {
			t.Errorf("render cache holds out-of-window fingerprint %s", fp)
		}
	}

	m.viewport.GotoTop()
	if !m.maybeExpandRenderWindow() {
		t.Fatal("expected render window expansion on scroll-to-top")
	}
	if got, want := len(m.msgRenderCacheLines), len(hist)-m.renderStartIdx; got != want {
		t.Fatalf("after expansion: render cache size = %d, want %d (window size)", got, want)
	}

	orphansBefore := len(m.msgRenderCacheLines)
	trimmed := append([]providers.Message{}, hist[len(hist)-6:]...)
	m.sessionMgr.SetHistory(key, trimmed)
	m.renderedBaseValid = false
	m.updateViewport()

	if len(m.msgRenderCacheLines) >= orphansBefore {
		t.Fatalf("cache was not pruned after history shrink: %d entries (was %d)",
			len(m.msgRenderCacheLines), orphansBefore)
	}
	want := fingerprintsOf(m.sessionMgr.GetHistoryView(key), m.viewport.Width)
	if got := len(m.msgRenderCacheLines); got != len(want) {
		t.Fatalf("after shrink: render cache size = %d, want %d (remaining history)", got, len(want))
	}
	for fp := range m.msgRenderCacheLines {
		if !want[fp] {
			t.Errorf("orphaned fingerprint %s survived the pruning rebuild", fp)
		}
	}
}

func TestMsgRenderCacheIncrementalHitsPreserved(t *testing.T) {
	m := newTestModel(t)

	key := "tui:chat:cache-hits"
	seedLazySession(t, m, key, 3)
	renderLazyModel(t, m)

	hist := m.sessionMgr.GetHistory(key)
	fpFirst := messageFingerprint(hist[0], m.viewport.Width)
	if _, ok := m.msgRenderCacheLines[fpFirst]; !ok {
		t.Fatal("precondition: first message fingerprint missing from render cache")
	}

	const sentinel = "SENTINEL-FROM-CACHE-MUST-BE-REUSED"
	m.msgRenderCacheLines[fpFirst] = []string{sentinel}

	m.sessionMgr.AddMessage(key, "user", "a brand new question")
	m.updateViewport()

	base := strings.Join(m.viewport.baseLines, "\n")
	if !strings.Contains(base, sentinel) {
		t.Errorf("cached lines were not reused on rebuild — incremental hit semantics broken")
	}
	if got := m.msgRenderCacheLines[fpFirst]; len(got) != 1 || got[0] != sentinel {
		t.Errorf("cache hit entry was not carried into the pruned cache: %v", got)
	}
	hist = m.sessionMgr.GetHistory(key)
	fpNew := messageFingerprint(hist[len(hist)-1], m.viewport.Width)
	if _, ok := m.msgRenderCacheLines[fpNew]; !ok {
		t.Errorf("newly rendered message missing from cache after rebuild")
	}
	if got, want := len(m.msgRenderCacheLines), 7; got != want {
		t.Fatalf("render cache size = %d, want %d", got, want)
	}
}
