package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

func viewportRebuildCounter(t *testing.T, m *Model) func() []bool {
	t.Helper()
	var frames []bool
	m.onViewportRebuild = func(inFrame bool) { frames = append(frames, inFrame) }
	t.Cleanup(func() { m.onViewportRebuild = nil })
	return func() []bool { return append([]bool(nil), frames...) }
}

func viewportOverlayText(m *Model) string {
	return stripAnsi(strings.Join(m.viewport.overlayLines, "\n"))
}

func viewportBaseText(m *Model) string {
	return stripAnsi(strings.Join(m.viewport.baseLines, "\n"))
}

func startStreamingChunk(m *Model, text string) {
	m.processing = true
	m.startTime = time.Now()
	m.currentStream = text
}

func TestView_UpToDateFrameDoesNotRebuild(t *testing.T) {
	const key = "tui:chat:t3-idle"
	m := newFrameReadTestModel(t, key, 3)

	_ = m.View()
	m.updateViewport()

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

	m.sessionMgr.AddMessage(key, "user", "a brand new question")
	if frame := stripAnsi(m.View()); !strings.Contains(frame, "a brand new question") {
		t.Fatalf("frame after a history change does not render the new message:\n%s", frame)
	}
	if got := rebuilds(); len(got) != 1 || got[0] != true {
		t.Fatalf("rebuilds = %v, want exactly one in-frame rebuild (the changed state)", got)
	}
}

func TestView_StreamingChunksAreCoalesced(t *testing.T) {
	const key = "tui:chat:t3-throttle"
	m := newFrameReadTestModel(t, key, 2)
	m.streamThrottleInterval = 200 * time.Millisecond
	_ = m.View()

	startStreamingChunk(m, "first chunk")
	if cmd := m.throttledUpdateViewport(); cmd == nil {
		t.Fatal("the leading edge of a burst must materialize immediately and schedule the tick")
	}
	if got := viewportOverlayText(m); !strings.Contains(got, "first chunk") {
		t.Fatalf("leading edge did not render the first chunk:\n%s", got)
	}

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

func TestView_StreamingTickMaterializesBurst(t *testing.T) {
	const key = "tui:chat:t3-throttle-tick"
	m := newFrameReadTestModel(t, key, 2)
	m.streamThrottleInterval = 200 * time.Millisecond
	_ = m.View()

	startStreamingChunk(m, "first chunk")
	_ = m.throttledUpdateViewport()
	m.currentStream = "first chunk and second chunk"
	_ = m.throttledUpdateViewport()

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

func TestView_StaleContentIsNeverDeferred(t *testing.T) {
	const key = "tui:chat:t3-throttle-bound"
	m := newFrameReadTestModel(t, key, 2)
	m.streamThrottleInterval = 50 * time.Millisecond
	_ = m.View()

	startStreamingChunk(m, "first chunk")
	_ = m.throttledUpdateViewport()
	m.currentStream = "first chunk and second chunk"
	_ = m.throttledUpdateViewport()

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

func TestView_GeometryChangeIsNeverDeferred(t *testing.T) {
	const key = "tui:chat:t3-geometry"
	m := newFrameReadTestModel(t, key, 2)
	m.streamThrottleInterval = 200 * time.Millisecond
	_ = m.View()

	startStreamingChunk(m, "first chunk")
	_ = m.throttledUpdateViewport()
	m.currentStream = "first chunk and second chunk"
	_ = m.throttledUpdateViewport()
	if !m.viewportRebuildDeferred() {
		t.Fatal("precondition: the streaming rebuild should be deferred in this window")
	}

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

func TestView_ObservesAppendedToolMessage(t *testing.T) {
	const key = "tui:chat:t3-tool-append"
	m := newFrameReadTestModel(t, key, 0)
	m.sessionMgr.SetHistory(key, []providers.Message{
		{Role: "user", Content: "Question"},
		{Role: "assistant", Content: "answer"},
	})
	_ = m.View()

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

func TestView_HonorsPendingForcedScroll(t *testing.T) {
	const key = "tui:chat:t3-forced-scroll"
	m := newFrameReadTestModel(t, key, 30)
	_ = m.View()
	if m.viewport.totalLines() <= m.viewport.Height {
		t.Fatalf("precondition: the chat must overflow the viewport (lines=%d height=%d)",
			m.viewport.totalLines(), m.viewport.Height)
	}

	m.viewport.YOffset = 0
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

func TestShouldSkipViewportUpdate_UsesMaterializedFingerprint(t *testing.T) {
	const key = "tui:chat:t3-shared-key"
	m := newFrameReadTestModel(t, key, 2)

	m.updateViewport()
	if m.lastViewportKey != m.getViewportContentKey() {
		t.Fatalf("Update-side fingerprint %q differs from the materialized one %q",
			m.getViewportContentKey(), m.lastViewportKey)
	}
	if !m.shouldSkipViewportUpdate() {
		t.Fatal("an unchanged state must be skipped by the Update-side guard after a rebuild")
	}

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
