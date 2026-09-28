package tui

// R3.2 — a transient (tool-suppressed) render must never reach the per-message
// cache.
//
// buildRenderedHistoryLines suppresses the tool-call rows of the LAST message
// while a tool is executing (isExecutingMessage: m.processing &&
// m.currentToolAction != "" && msg.Role == "assistant" && i == totalMsgs-1)
// because the active call is already shown in the overlay. The per-message
// render cache is keyed on messageFingerprint alone (utils.go), which cannot see
// the suppression, so a rebuild landing inside that window used to store the
// tool-row-less variant under the message's only key — and the hit path serves
// that entry for as long as the fingerprint, the width and the session hold,
// i.e. the rows stayed invisible until a width/session/theme change.
//
// Two flows reach that state:
//
//   - /compact runs entirely inside the window (/compact sets processing +
//     currentToolAction before the backend call), which T14 patched with a
//     one-entry cache drop (dropTransientLastMessageRender). It is gone now: the
//     compact flow is re-pinned in compact_invalidation_test.go against the new
//     mechanism.
//   - a message appended during the window was NOT covered by that drop: the
//     previously-last message kept its poisoned entry, and the rebuild the
//     append triggers (it changes the message count) served the entry from the
//     cache instead of re-rendering the now non-last message with its tool row.
//     That is the case below.
//
// The fix is at the source: a render produced under suppression is simply not
// stored, so every cached entry is a function of the fingerprint alone and no
// invalidation hook is needed.

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// toolCallMessage is an assistant turn carrying a tool call: the shape the
// suppression branch of buildRenderedHistoryLines is about.
func toolCallMessage(name string) providers.Message {
	return providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{
			{Name: name, Function: &providers.FunctionCall{Name: name, Arguments: `{"path":"x"}`}},
		},
	}
}

// TestRenderCache_SuppressedRenderNotCachedOnAppend is the R3.2 regression test.
//
// The tool-call turn is committed while the tool is executing, so the first
// rebuild renders it with its tool row suppressed. A message appended inside the
// same window then makes it the previous-to-last message, and the rebuild that
// follows must show its tool row: with the suppressed render cached that rebuild
// is a cache hit and the row stays invisible for the rest of the session.
func TestRenderCache_SuppressedRenderNotCachedOnAppend(t *testing.T) {
	m := newTestModel(t)
	const key = "tui:chat:suppressed-append"
	seedLazySession(t, m, key, 3)
	renderLazyModel(t, m)
	m.updateViewport()

	// The committed turn arrives while the tool it calls is executing — the
	// exact state a rebuild inside the suppression window sees.
	m.processing = true
	m.currentToolAction = "read_file: x"
	toolMsg := toolCallMessage("read_file")
	m.sessionMgr.AddFullMessage(key, toolMsg)
	m.updateViewport()

	fpTool := messageFingerprint(toolMsg, m.viewport.Width)
	if cached, ok := m.msgRenderCacheLines[fpTool]; ok {
		t.Errorf("the suppressed render of the executing message was cached (%d lines): the tool-call rows can never come back until the entry is dropped by a width/session/theme change", len(cached))
	}
	// The frame itself still shows the suppressed variant: not caching must not
	// disable the suppression.
	if base := viewportBaseText(m); strings.Contains(base, "read_file") {
		t.Fatalf("the executing message's tool-call row must stay out of the frame while the tool runs:\n%s", base)
	}

	// A new message lands inside the window, so the tool-call turn is no longer
	// the last one: its row has to be rendered now.
	m.sessionMgr.AddMessage(key, "user", "follow-up question while the tool runs")
	m.updateViewport()

	base := viewportBaseText(m)
	if !strings.Contains(base, "read_file") {
		t.Fatalf("the previous-to-last message's tool-call row is missing from the frame: a suppressed render was cached under its fingerprint and the rebuild served it instead of re-rendering:\n%s", base)
	}
	// And the rendered variant is now the cached one, so the next rebuilds are
	// hits again (the guard costs exactly one re-render of that message).
	if _, ok := m.msgRenderCacheLines[fpTool]; !ok {
		t.Error("the tool-call turn is not cached after it rendered as a regular message: the guard must skip only the suppressed render")
	}
}
