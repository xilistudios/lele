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
			got := calculateViewportHeight(tt.contentHeight, tt.statusHeight, tt.autocomplete, tt.inputHeight, tt.bottomHeight)
			if got != tt.want {
				t.Fatalf("calculateViewportHeight() = %d, want %d", got, tt.want)
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
