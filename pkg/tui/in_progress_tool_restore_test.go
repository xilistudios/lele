package tui

import (
	"testing"

	"github.com/xilistudios/lele/pkg/session"
)

func TestFormatInProgressToolAction(t *testing.T) {
	cases := []struct {
		name string
		tool *session.InProgressTool
		want string
	}{
		{name: "nil record", tool: nil, want: ""},
		{
			name: "prefers the pre-formatted action",
			tool: &session.InProgressTool{Tool: "exec", Action: "exec: sleep 600"},
			want: "exec: sleep 600",
		},
		{
			name: "falls back to the tool name when the action is empty",
			tool: &session.InProgressTool{Tool: "wait_for_subagent"},
			want: "wait_for_subagent",
		},
		{
			name: "strips ANSI escapes from the action",
			tool: &session.InProgressTool{Tool: "exec", Action: "exec: \x1b[31mrm -rf /\x1b[0m"},
			want: "exec: rm -rf /",
		},
		{
			name: "strips bidi overrides and zero-width marks from the action",
			tool: &session.InProgressTool{Tool: "exec", Action: "exec: \u202Ea\u200Bb"},
			want: "exec: ab",
		},
		{
			name: "strips controls from the tool name fallback",
			tool: &session.InProgressTool{Tool: "ex\x1b[2Jec"},
			want: "exec",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatInProgressToolAction(tc.tool); got != tc.want {
				t.Errorf("formatInProgressToolAction(%#v) = %q, want %q", tc.tool, got, tc.want)
			}
		})
	}
}

func TestInProgressToolAction_NoRecord(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")
	if m.currentKey == "" {
		t.Fatal("expected /new to create a session")
	}

	if got := m.inProgressToolAction(m.currentKey); got != "" {
		t.Errorf("inProgressToolAction(%q) = %q, want empty (no tool running)", m.currentKey, got)
	}
	if got := m.inProgressToolAction(""); got != "" {
		t.Errorf("inProgressToolAction(\"\") = %q, want empty", got)
	}
}

func TestClearStreamingState_DoesNotResurrectToolRowWhenIdle(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")
	m.currentToolAction = "exec: sleep 600"
	m.processing = true

	m.clearStreamingState()

	if m.currentToolAction != "" {
		t.Errorf("stale tool row survived clearStreamingState on an idle session: %q", m.currentToolAction)
	}
	if m.processing {
		t.Error("processing flag survived clearStreamingState on an idle session")
	}
}

func TestViewportContentKey_IncludesRestoredToolRow(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")

	withRow := m.currentKey
	if withRow == "" {
		t.Fatal("expected /new to create a session")
	}

	m.currentToolAction = ""
	idle := m.getViewportContentKey()
	m.currentToolAction = "exec: sleep 600"
	busy := m.getViewportContentKey()

	if idle == busy {
		t.Fatal("viewport content key ignores the running tool row: a restored row would not repaint")
	}
}
