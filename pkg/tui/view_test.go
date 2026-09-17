package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/channels"
)

func TestCalculateViewportHeight(t *testing.T) {
	tests := []struct {
		name          string
		contentHeight int
		statusHeight  int
		queueRow      int
		autocomplete  int
		inputHeight   int
		bottomHeight  int
		want          int
	}{
		{
			name:          "normal layout",
			contentHeight: 24,
			statusHeight:  3,
			inputHeight:   3,
			bottomHeight:  1,
			want:          14,
		},
		{
			name:          "autocomplete consumes its own lines",
			contentHeight: 24,
			statusHeight:  3,
			autocomplete:  5,
			inputHeight:   3,
			bottomHeight:  1,
			want:          9,
		},
		{
			name:          "queue row consumes exactly one line",
			contentHeight: 24,
			statusHeight:  3,
			queueRow:      1,
			inputHeight:   3,
			bottomHeight:  1,
			want:          13,
		},
		{
			name:          "small terminal keeps minimum viewport",
			contentHeight: 8,
			statusHeight:  3,
			inputHeight:   3,
			bottomHeight:  1,
			want:          1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateViewportHeight(tt.contentHeight, tt.statusHeight, tt.queueRow, tt.autocomplete, tt.inputHeight, tt.bottomHeight)
			if got != tt.want {
				t.Fatalf("calculateViewportHeight() = %d, want %d", got, tt.want)
			}
		})
	}
}

// The queue preview band adds a line to the left column, so it is a new way
// for the frame to overshoot the terminal height. This asserts the exact-height
// invariant with a full queue, the autocomplete overlay, or both on screen.
func TestView_HeightExactWithQueueAndAutocomplete(t *testing.T) {
	m := newTestModel(t)

	key := "tui:chat:view-height-queue"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddMessage(key, "user", "hi")
	m.sessionMgr.AddMessage(key, "assistant", "hello there")
	m.currentKey = key
	m.showWelcome = false

	states := []struct {
		name             string
		queue, autocompl bool
	}{
		{"plain", false, false},
		{"queue", true, false},
		{"autocomplete", false, true},
		{"queue+autocomplete", true, true},
	}

	for _, st := range states {
		t.Run(st.name, func(t *testing.T) {
			m.messageQueue = map[string][]queuedMessage{}
			m.showAutocomplete = false
			m.autocompleteItems = nil

			if st.queue {
				m.enqueueMessage("a queued message that is long enough to be truncated at narrow widths")
			}
			if st.autocompl {
				m.showAutocomplete = true
				m.autocompleteItems = []commandInfo{
					{name: "/help", description: "show help"},
					{name: "/new", description: "start a new session"},
				}
			}

			for _, size := range []struct{ w, h int }{
				{80, 24}, {120, 30}, {100, 20}, {160, 40}, {70, 15},
			} {
				m.width, m.height = size.w, size.h
				lines := strings.Split(m.View(), "\n")
				if len(lines) != size.h {
					t.Fatalf("%dx%d: m.View() returned %d lines, want exact %d", size.w, size.h, len(lines), size.h)
				}
			}
		})
	}
}

func TestView_HeightNeverExceedsTerminalHeight(t *testing.T) {
	m := newTestModel(t)

	key := "tui:chat:view-height-test"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	// Add messages with long lines, tool calls, markdown
	m.sessionMgr.AddMessage(key, "user", "Run some tools and write code")
	m.sessionMgr.AddMessage(key, "assistant",
		"Here is some thinking and tool execution:\n"+
			"```go\nfunc main() {\n\tprintln(\"Hello World from a very long line of code that spans across multiple columns\")\n}\n```\n"+
			"Now I've got the whole picture. The problem is as follows:\nRoot cause: handleChatHistory (the REST endpoint `/api/v1/chat/history`) reloads evicted messages.")

	m.currentKey = key
	m.showWelcome = false

	// Add subagents to sidebar
	var subagents []channels.SubagentTaskInfo
	for i := 0; i < 15; i++ {
		subagents = append(subagents, channels.SubagentTaskInfo{
			TaskID:     fmt.Sprintf("subagent-task-%d", i),
			Label:      fmt.Sprintf("Implement Phase %d (Task %d) of the plan", i, i*10),
			Status:     "completed",
			SessionKey: fmt.Sprintf("subagent-session-%d", i),
		})
	}
	m.subagentsCacheKey = "native:" + key
	m.subagentsCacheTime = time.Now()
	m.subagentsCacheValue = subagents

	sizes := []struct {
		w, h int
	}{
		{80, 24},
		{120, 30},
		{100, 20},
		{160, 40},
		{70, 15},
		{200, 50},
	}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			m.width = size.w
			m.height = size.h

			out := m.View()
			lines := strings.Split(out, "\n")
			if len(lines) > size.h {
				t.Fatalf("m.View() returned %d lines, want <= %d (exceeded by %d lines)", len(lines), size.h, len(lines)-size.h)
			}
			if len(lines) != size.h {
				t.Fatalf("m.View() returned %d lines, want exact terminal height %d", len(lines), size.h)
			}
		})
	}
}
