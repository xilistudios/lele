package tui

// T3 regression tests: the render path does not rebuild the viewport.
//
// Context: bubbletea calls View() after EVERY Update — every streaming chunk,
// spinner tick, mouse move and keypress. pkg/tui rebuilt the whole viewport
// (base lines + ephemeral overlay, O(history) with glamour over the render
// window) inside View(), which made the streaming throttle pointless: the
// 32 ms window was defeated by the very next frame, and a long chat paid
// O(history) on every frame.
//
// The contract pinned here:
//
//  1. a frame whose model state has not changed since the last materialization
//     does ZERO rebuilds (TestView_UpToDateFrameDoesNotRebuild);
//  2. streaming chunks are coalesced: inside the throttle window a frame keeps
//     serving what the burst's leading edge materialized
//     (TestView_StreamingChunksAreCoalesced), the scheduled tick materializes
//     the whole burst (TestView_StreamingTickMaterializesBurst) and the window
//     is bounded, so content can never be older than streamThrottleInterval
//     even if that tick is lost (TestView_StaleContentIsNeverDeferred);
//  3. state that no Update handler routes through a rebuild still reaches the
//     render path when it can change what is drawn: a geometry change is never
//     deferred (TestView_GeometryChangeIsNeverDeferred), an in-place history
//     finalization (TestView_ObservesInPlaceHistoryFinalization), an appended
//     non-counted message (TestView_ObservesAppendedToolMessage) and a pending
//     forced scroll (TestView_HonorsPendingForcedScroll) are observed;
//  4. the Update-side skip guard and the render-path freshness check are ONE
//     fingerprint (TestShouldSkipViewportUpdate_UsesMaterializedFingerprint),
//     so a rebuild decision cannot drift between the two sides.
//
// Red-checks — each one was verified by reverting that single piece of the T3
// change and confirming the test fails, then restoring it:
//
//	a) View() back to `m.updateViewportWithHistory(history)` (unconditional
//	   rebuild) ⇒ (1) fails (every frame rebuilds) and (2) fails (the throttle
//	   window is defeated);
//	b) dropping the viewportRebuildDeferred() check from syncViewportForFrame ⇒
//	   (2) fails;
//	c) dropping the viewportGeometryChanged() bypass ⇒ (3)-geometry fails;
//	d) dropping frameLastStreaming from viewportContentKey ⇒ (3)-finalization
//	   fails (the frame keeps rendering the pre-finalization base);
//	e) dropping frameHistoryLen (and the matching base term) ⇒ (3)-appended-tool
//	   fails;
//	f) restoring the old Update-side-only getViewportContentKey body ⇒ (4) fails
//	   as soon as a rebuild has recorded its own fingerprint;
//	g) dropping forceGotoBottom from the fingerprint ⇒
//	   TestView_HonorsPendingForcedScroll fails (the frame leaves the forced
//	   jump pending) and the "forced scroll" case of (4) fails.

import (
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// viewportRebuildCounter installs the model's viewport-rebuild hook and returns
// a function reporting the inFrame flag of every rebuild since. The hook fires
// in updateViewportWithHistory — the single O(lines) rebuild — so the count is
// exhaustive for both the render and the Update path.
func viewportRebuildCounter(t *testing.T, m *Model) func() []bool {
	t.Helper()
	var frames []bool
	m.onViewportRebuild = func(inFrame bool) { frames = append(frames, inFrame) }
	t.Cleanup(func() { m.onViewportRebuild = nil })
	return func() []bool { return append([]bool(nil), frames...) }
}

// viewportOverlayText returns the ephemeral overlay currently materialized in
// the viewport with ANSI stripped, so assertions can look at text only.
func viewportOverlayText(m *Model) string {
	return stripAnsi(strings.Join(m.viewport.overlayLines, "\n"))
}

// viewportBaseText returns the materialized base lines with ANSI stripped.
func viewportBaseText(m *Model) string {
	return stripAnsi(strings.Join(m.viewport.baseLines, "\n"))
}

// startStreamingChunk puts the model in the state a stream chunk leaves behind:
// local processing state within its startup grace period — without startTime the
// status line classifies the local processing as stale and clears it
// (isSessionProcessing) — plus the accumulated stream text.
func startStreamingChunk(m *Model, text string) {
	m.processing = true
	m.startTime = time.Now()
	m.currentStream = text
}

// TestView_UpToDateFrameDoesNotRebuild is the core T3 contract: once the
// materialized content matches the current state, View() must not rebuild it —
// no matter how many frames are rendered (spinner ticks, mouse moves, redraws
// all call View()).
func TestView_UpToDateFrameDoesNotRebuild(t *testing.T) {
	const key = "tui:chat:t3-idle"
	m := newFrameReadTestModel(t, key, 3)

	_ = m.View()       // cold frame: materializes dimensions and content
	m.updateViewport() // exactly what every Update handler does before rendering

	rebuilds := viewportRebuildCounter(t, m)
	for i := 0; i < 5; i++ {
		if frame := m.View(); frame == "" {
			t.Fatal("View() returned an empty frame")
		}
	}
	if got := rebuilds(); len(got) != 0 {
		t.Fatalf("5 idle frames rebuilt the viewport %d times (inFrame=%v), want 0 — View() must serve the materialized content",
			len(got), got)
	}

	// A state change that an Update handler would route through the rebuild
	// must still be served correctly by the NEXT frame (the frame rebuilds it
	// once, then goes quiet again).
	m.sessionMgr.AddMessage(key, "user", "a brand new question")
	if frame := stripAnsi(m.View()); !strings.Contains(frame, "a brand new question") {
		t.Fatalf("frame after a history change does not render the new message:\n%s", frame)
	}
	// 5 idle frames + the change + the verification frame: exactly one rebuild.
	if got := rebuilds(); len(got) != 1 || got[0] != true {
		t.Fatalf("rebuilds = %v, want exactly one in-frame rebuild (the changed state)", got)
	}
}

// TestView_StreamingChunksAreCoalesced is the throttle half of T3: the burst's
// leading edge renders immediately, and every chunk inside the window must be
// served from that materialization instead of forcing a new rebuild — which is
// exactly what View() rebuilding unconditionally used to prevent.
func TestView_StreamingChunksAreCoalesced(t *testing.T) {
	const key = "tui:chat:t3-throttle"
	m := newFrameReadTestModel(t, key, 2)
	m.streamThrottleInterval = 200 * time.Millisecond
	_ = m.View()

	// First chunk of a turn: the leading edge materializes it immediately.
	startStreamingChunk(m, "first chunk")
	if cmd := m.throttledUpdateViewport(); cmd == nil {
		t.Fatal("the leading edge of a burst must materialize immediately and schedule the tick")
	}
	if got := viewportOverlayText(m); !strings.Contains(got, "first chunk") {
		t.Fatalf("leading edge did not render the first chunk:\n%s", got)
	}

	// Second chunk inside the window: coalesced.
	m.currentStream = "first chunk and second chunk"
	if cmd := m.throttledUpdateViewport(); cmd != nil {
		t.Fatal("a chunk inside the throttle window must not schedule a second tick")
	}
	if !m.streamPendingUpdate {
		t.Fatal("coalesced chunk did not mark the pending rebuild")
	}

	rebuilds := viewportRebuildCounter(t, m)
	for i := 0; i < 3; i++ {
		if frame := m.View(); frame == "" {
			t.Fatal("View() returned an empty frame while streaming")
		}
	}
	if got := rebuilds(); len(got) != 0 {
		t.Fatalf("frames inside the throttle window rebuilt the viewport %d times, want 0", len(got))
	}
	overlay := viewportOverlayText(m)
	if strings.Contains(overlay, "second chunk") {
		t.Fatalf("throttle window defeated: the frame rendered a coalesced chunk:\n%s", overlay)
	}
	if !strings.Contains(overlay, "first chunk") {
		t.Fatalf("frame inside the window lost the leading-edge content:\n%s", overlay)
	}
}

// TestView_StreamingTickMaterializesBurst covers the other end of the throttle:
// when the scheduled tick arrives, the whole coalesced burst is materialized
// once and the pending flag is cleared, so the frames that follow are quiet
// again (the rebuild owns the flag, see noteViewportMaterialized).
func TestView_StreamingTickMaterializesBurst(t *testing.T) {
	const key = "tui:chat:t3-throttle-tick"
	m := newFrameReadTestModel(t, key, 2)
	m.streamThrottleInterval = 200 * time.Millisecond
	_ = m.View()

	startStreamingChunk(m, "first chunk")
	_ = m.throttledUpdateViewport()
	m.currentStream = "first chunk and second chunk"
	_ = m.throttledUpdateViewport()

	// The tick is what materializes the burst.
	updated, _ := m.Update(streamThrottleMsg{})
	m = updated.(*Model)
	if m.streamPendingUpdate {
		t.Fatal("the throttled rebuild did not clear the pending flag")
	}
	if got := viewportOverlayText(m); !strings.Contains(got, "second chunk") {
		t.Fatalf("the scheduled tick did not materialize the coalesced burst:\n%s", got)
	}

	rebuilds := viewportRebuildCounter(t, m)
	_ = m.View()
	if got := rebuilds(); len(got) != 0 {
		t.Fatalf("frame after the throttled rebuild rebuilt again (%d), want 0", len(got))
	}
}

// TestView_StaleContentIsNeverDeferred is the safety bound of the throttle: the
// deferral is only legal while the pending tick can still arrive within
// streamThrottleInterval. If the tick is lost (early-returned handler, dropped
// command), the render path must materialize the content itself instead of
// serving it forever.
func TestView_StaleContentIsNeverDeferred(t *testing.T) {
	const key = "tui:chat:t3-throttle-bound"
	m := newFrameReadTestModel(t, key, 2)
	m.streamThrottleInterval = 50 * time.Millisecond
	_ = m.View()

	startStreamingChunk(m, "first chunk")
	_ = m.throttledUpdateViewport()
	m.currentStream = "first chunk and second chunk"
	_ = m.throttledUpdateViewport() // pending, tick scheduled, then lost

	// Rewind the materialization beyond the window: the frame may no longer
	// assume the tick is coming.
	m.viewportBuiltAt = time.Now().Add(-2 * m.streamThrottleInterval)

	rebuilds := viewportRebuildCounter(t, m)
	if frame := m.View(); frame == "" {
		t.Fatal("View() returned an empty frame while streaming")
	}
	if got := rebuilds(); len(got) != 1 {
		t.Fatalf("a frame past the throttle window rebuilt %d times, want 1", len(got))
	}
	if got := viewportOverlayText(m); !strings.Contains(got, "second chunk") {
		t.Fatalf("content older than streamThrottleInterval was served:\n%s", got)
	}
	if m.streamPendingUpdate {
		t.Fatal("the render path's rebuild left the pending flag set")
	}
}

// TestView_GeometryChangeIsNeverDeferred pins the one stale state only the
// render path can see: the base lines were wrapped for the viewport dimensions
// of a previous frame. That must rebuild immediately — a base wrapped for other
// dimensions is visibly wrong, not 32 ms stale — even while the streaming
// throttle owns the content rebuild.
func TestView_GeometryChangeIsNeverDeferred(t *testing.T) {
	const key = "tui:chat:t3-geometry"
	m := newFrameReadTestModel(t, key, 2)
	m.streamThrottleInterval = 200 * time.Millisecond
	_ = m.View()

	// A pending, still-fresh streaming rebuild: the deferral IS legal here…
	startStreamingChunk(m, "first chunk")
	_ = m.throttledUpdateViewport()
	m.currentStream = "first chunk and second chunk"
	_ = m.throttledUpdateViewport()
	if !m.viewportRebuildDeferred() {
		t.Fatal("precondition: the streaming rebuild should be deferred in this window")
	}

	// …but the terminal resized, so the materialized base no longer matches the
	// layout and must be rebuilt for the new geometry.
	widthBefore := m.viewport.Width
	m.width -= 40

	rebuilds := viewportRebuildCounter(t, m)
	_ = m.View()
	if m.viewport.Width == widthBefore {
		t.Fatalf("precondition: the layout change did not propagate to viewport.Width (%d)", m.viewport.Width)
	}
	if got := rebuilds(); len(got) != 1 || got[0] != true {
		t.Fatalf("geometry change rebuilt %v, want exactly one in-frame rebuild", got)
	}
	if m.viewportBuiltWidth != m.viewport.Width || m.viewportBuiltHeight != m.viewport.Height {
		t.Fatalf("materialized dimensions = %dx%d, want %dx%d",
			m.viewportBuiltWidth, m.viewportBuiltHeight, m.viewport.Width, m.viewport.Height)
	}
}

// TestView_ObservesInPlaceHistoryFinalization locks down the frameLastStreaming
// term of the fingerprint. A finalized assistant message keeps the message count
// and the model state identical — only the history content (and the streaming
// flag that marks it) changes in place — so the fingerprint has to carry that
// flag or the frame would keep serving the base built before finalization.
func TestView_ObservesInPlaceHistoryFinalization(t *testing.T) {
	const key = "tui:chat:t3-finalize"
	m := newFrameReadTestModel(t, key, 0)
	m.sessionMgr.SetHistory(key, []providers.Message{
		{Role: "user", Content: "Question"},
		{Role: "assistant", Content: "partial answer", Streaming: true},
	})

	if frame := stripAnsi(m.View()); !strings.Contains(frame, "partial answer") {
		t.Fatalf("cold frame does not render the history:\n%s", frame)
	}

	// Same count (2), same model state: the message is finalized in place.
	m.sessionMgr.SetHistory(key, []providers.Message{
		{Role: "user", Content: "Question"},
		{Role: "assistant", Content: "final answer"},
	})

	rebuilds := viewportRebuildCounter(t, m)
	frame := stripAnsi(m.View())
	if got := rebuilds(); len(got) != 1 {
		t.Fatalf("in-place finalization rebuilt %d times, want 1", len(got))
	}
	if !strings.Contains(frame, "final answer") {
		t.Fatalf("frame served the pre-finalization base (stale streaming content):\n%s", frame)
	}
	if strings.Contains(frame, "partial answer") {
		t.Fatalf("frame still renders the replaced message content:\n%s", frame)
	}
}

// TestView_ObservesAppendedToolMessage covers the frameHistoryLen term. A tool
// result is rendered in the base but is deliberately NOT part of the counted
// user/assistant total, so without a length term an append that only adds such a
// message would look like "nothing changed" to the fingerprint — and the frame
// would keep the base built before it arrived.
func TestView_ObservesAppendedToolMessage(t *testing.T) {
	const key = "tui:chat:t3-tool-append"
	m := newFrameReadTestModel(t, key, 0)
	m.sessionMgr.SetHistory(key, []providers.Message{
		{Role: "user", Content: "Question"},
		{Role: "assistant", Content: "answer"},
	})
	_ = m.View()

	// Append a tool result: the counted total stays 2.
	m.sessionMgr.SetHistory(key, []providers.Message{
		{Role: "user", Content: "Question"},
		{Role: "assistant", Content: "answer"},
		{Role: "tool", Content: "command output here"},
	})

	rebuilds := viewportRebuildCounter(t, m)
	_ = m.View()
	if got := rebuilds(); len(got) != 1 {
		t.Fatalf("appended tool message rebuilt %d times, want 1", len(got))
	}
	if got := viewportBaseText(m); !strings.Contains(got, "command output here") {
		t.Fatalf("base does not contain the appended tool result:\n%s", got)
	}
}

// TestView_HonorsPendingForcedScroll pins the forceGotoBottom term. The forced
// scroll is consumed BY the rebuild, so a frame that considers its content up to
// date must still rebuild to honor it — otherwise a session switch / new chat /
// compaction that only flags the jump would leave the view at the old offset
// until some unrelated change happened to arrive.
func TestView_HonorsPendingForcedScroll(t *testing.T) {
	const key = "tui:chat:t3-forced-scroll"
	m := newFrameReadTestModel(t, key, 30) // long enough to be scrollable
	_ = m.View()
	if m.viewport.totalLines() <= m.viewport.Height {
		t.Fatalf("precondition: the chat must overflow the viewport (lines=%d height=%d)",
			m.viewport.totalLines(), m.viewport.Height)
	}

	m.viewport.YOffset = 0 // user scrolled to the top
	_ = m.View()
	if m.viewport.AtBottom() {
		t.Fatal("precondition: the viewport should not be at the bottom")
	}

	m.forceGotoBottom = true
	_ = m.View()
	if m.forceGotoBottom {
		t.Fatal("the frame did not consume the pending forced scroll")
	}
	if !m.viewport.AtBottom() {
		t.Fatalf("a pending forced scroll was not honored (YOffset=%d, max=%d)",
			m.viewport.YOffset, m.viewport.maxYOffset())
	}
}

// TestShouldSkipViewportUpdate_UsesMaterializedFingerprint pins the DRY half of
// the design: the Update-side skip guard compares the very fingerprint a rebuild
// records (noteViewportMaterialized), so "up to date" cannot mean two different
// things on the two sides. With a separate Update-side key the guard would
// rebuild on every event even though the render path considers the content
// fresh — and, worse, the two sides could disagree about what is visible.
func TestShouldSkipViewportUpdate_UsesMaterializedFingerprint(t *testing.T) {
	const key = "tui:chat:t3-shared-key"
	m := newFrameReadTestModel(t, key, 2)

	m.updateViewport() // materialize, as an event handler does
	if m.lastViewportKey != m.getViewportContentKey() {
		t.Fatalf("Update-side fingerprint %q differs from the materialized one %q",
			m.getViewportContentKey(), m.lastViewportKey)
	}
	if !m.shouldSkipViewportUpdate() {
		t.Fatal("an unchanged state must be skipped by the Update-side guard after a rebuild")
	}

	// Any input the viewport draws from must invalidate the guard — checked one
	// at a time so a missing term is reported by name. The two history terms
	// (frameHistoryLen/frameLastStreaming) are absent here on purpose: they are
	// re-derived from the history on every read (frameHistoryTerms), so a direct
	// mutation is overwritten before it could be compared. Their effect on the
	// render path is asserted by TestRenderPath_HistoryTermsAreFingerprinted.
	cases := []struct {
		name   string
		mutate func()
		undo   func()
	}{
		{"streaming length", func() { m.currentStream = "chunk" }, func() { m.currentStream = "" }},
		{"tool action", func() { m.currentToolAction = "exec: ls" }, func() { m.currentToolAction = "" }},
		{"pending user message", func() { m.pendingUserMessage = "queued" }, func() { m.pendingUserMessage = "" }},
		{"pending approval", func() { m.pendingApprovalID = "ap-1" }, func() { m.pendingApprovalID = "" }},
		{"approval result", func() { m.approvalResult = "approved" }, func() { m.approvalResult = "" }},
		{"processing", func() { m.processing = true }, func() { m.processing = false }},
		{"compact feedback", func() { m.compactFeedback = "compacted" }, func() { m.compactFeedback = "" }},
		{"status feedback", func() { m.statusFeedback = "status" }, func() { m.statusFeedback = "" }},
		{"render window", func() { m.renderStartIdx-- }, func() { m.renderStartIdx++ }},
		{"viewport height", func() { m.viewport.Height-- }, func() { m.viewport.Height++ }},
		{"max rendered messages", func() { m.maxRenderedMessages++ }, func() { m.maxRenderedMessages-- }},
		{"selection mode", func() { m.selecting = true }, func() { m.selecting = false }},
		{"forced scroll", func() { m.forceGotoBottom = true }, func() { m.forceGotoBottom = false }},
		{"approval command", func() { m.pendingApprovalCmd = "rm -rf /" }, func() { m.pendingApprovalCmd = "" }},
		{"approval full view", func() { m.approvalShowFull = true }, func() { m.approvalShowFull = false }},
		{"modal mode", func() { m.modalMode = ModalModel }, func() { m.modalMode = ModalNone }},
		{"subagent progress", func() { m.subagentProgress = map[string]string{"s1": "working"} }, func() { m.subagentProgress = nil }},
		{"welcome screen", func() { m.showWelcome = true }, func() { m.showWelcome = false }},
		{"onboarding", func() { m.onboardingActive = true }, func() { m.onboardingActive = false }},
	}

	for _, tc := range cases {
		tc.mutate()
		changed := m.getViewportContentKey() != m.lastViewportKey
		skipped := m.shouldSkipViewportUpdate()
		tc.undo()
		if !changed {
			t.Fatalf("mutating %s did not change the viewport fingerprint: the render path could serve stale content", tc.name)
		}
		if skipped {
			t.Fatalf("Update-side guard skipped a rebuild after mutating %s", tc.name)
		}
	}

	if !m.shouldSkipViewportUpdate() {
		t.Fatal("the undo did not restore the skipped state")
	}
}

// TestRenderPath_HistoryTermsAreFingerprinted covers the two history terms the
// render path cannot re-derive from the counted total: the snapshot's length
// (an appended tool result) and its last-message streaming flag (an in-place
// finalization). Both are asserted through viewportContentUpToDate — the O(1)
// check that decides whether a frame rebuilds — because the Update-side
// fingerprint always re-reads them from the history.
func TestRenderPath_HistoryTermsAreFingerprinted(t *testing.T) {
	const key = "tui:chat:t3-history-terms"
	m := newFrameReadTestModel(t, key, 0)
	m.sessionMgr.SetHistory(key, []providers.Message{
		{Role: "user", Content: "Question"},
		{Role: "assistant", Content: "answer"},
	})
	m.updateViewport()

	if !m.viewportContentUpToDate(m.frameCount) {
		t.Fatal("precondition: a just-materialized state must be considered up to date")
	}

	m.frameLastStreaming = !m.frameLastStreaming
	if m.viewportContentUpToDate(m.frameCount) {
		t.Fatal("the last-message streaming flag is not part of the fingerprint")
	}
	m.frameLastStreaming = !m.frameLastStreaming

	m.frameHistoryLen++
	if m.viewportContentUpToDate(m.frameCount) {
		t.Fatal("the history length is not part of the fingerprint")
	}
	m.frameHistoryLen--

	if !m.viewportContentUpToDate(m.frameCount) {
		t.Fatal("restoring the history terms did not restore the up-to-date state")
	}
}
