package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func toolCallMessage(name string) providers.Message {
	return providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{
			{Name: name, Function: &providers.FunctionCall{Name: name, Arguments: `{"path":"x"}`}},
		},
	}
}

func TestRenderCache_SuppressedRenderNotCachedOnAppend(t *testing.T) {
	m := newTestModel(t)
	const key = "tui:chat:suppressed-append"
	seedLazySession(t, m, key, 3)
	renderLazyModel(t, m)
	m.updateViewport()

	m.processing = true
	m.currentToolAction = "read_file: x"
	toolMsg := toolCallMessage("read_file")
	m.sessionMgr.AddFullMessage(key, toolMsg)
	m.updateViewport()

	fpTool := messageFingerprint(toolMsg, m.viewport.Width)
	if cached, ok := m.msgRenderCacheLines[fpTool]; ok {
		t.Errorf("the suppressed render of the executing message was cached (%d lines): the tool-call rows can never come back until the entry is dropped by a width/session/theme change", len(cached))
	}
	if base := viewportBaseText(m); strings.Contains(base, "read_file") {
		t.Fatalf("the executing message's tool-call row must stay out of the frame while the tool runs:\n%s", base)
	}

	m.sessionMgr.AddMessage(key, "user", "follow-up question while the tool runs")
	m.updateViewport()

	base := viewportBaseText(m)
	if !strings.Contains(base, "read_file") {
		t.Fatalf("the previous-to-last message's tool-call row is missing from the frame: a suppressed render was cached under its fingerprint and the rebuild served it instead of re-rendering:\n%s", base)
	}
	if _, ok := m.msgRenderCacheLines[fpTool]; !ok {
		t.Error("the tool-call turn is not cached after it rendered as a regular message: the guard must skip only the suppressed render")
	}
}
