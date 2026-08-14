package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
)

// newBenchModel mirrors newTestModel but accepts testing.TB so it can be
// used from benchmarks.
func newBenchModel(tb testing.TB) *Model {
	tb.Helper()
	tmpDir := tb.TempDir()
	tb.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{},
	}
	if err := config.SaveConfig(filepath.Join(tmpDir, "config.json"), cfg); err != nil {
		tb.Fatalf("saving initial config: %v", err)
	}
	tb.Setenv("LELE_CONFIG_DIR", tmpDir)

	msgBus := bus.NewMessageBus()
	al := agent.NewAgentLoop(cfg, msgBus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go al.Run(ctx)

	sessionMgr := al.SessionManager()
	if sessionMgr == nil {
		tb.Fatal("session manager not initialized")
	}

	return NewModel(cfg, al, sessionMgr)
}

// buildBenchModel creates a model with a realistic conversation: N user and
// assistant message pairs with markdown-ish content.
func buildBenchModel(tb testing.TB, pairs int) *Model {
	tb.Helper()
	m := newBenchModel(tb)

	key := "tui:chat:bench"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	for i := 0; i < pairs; i++ {
		m.sessionMgr.AddMessage(key, "user", fmt.Sprintf("Question %d: how do I implement feature X with proper error handling and tests?", i))
		m.sessionMgr.AddMessage(key, "assistant",
			fmt.Sprintf("Answer %d:\nHere is an approach:\n```go\nfunc feature%d() error {\n\tif err := doWork(); err != nil {\n\t\treturn fmt.Errorf(\"feature %d: %%w\", err)\n\t}\n\treturn nil\n}\n```\nThis handles the error path and is covered by tests.", i, i, i))
	}

	m.currentKey = key
	m.showWelcome = false

	var subagents []channels.SubagentTaskInfo
	for i := 0; i < 5; i++ {
		subagents = append(subagents, channels.SubagentTaskInfo{
			TaskID:     fmt.Sprintf("subagent-task-%d", i),
			Label:      fmt.Sprintf("Implement Phase %d", i),
			Status:     "completed",
			SessionKey: fmt.Sprintf("subagent-session-%d", i),
		})
	}
	m.subagentsCacheKey = "native:" + key
	m.subagentsCacheTime = time.Now()
	m.subagentsCacheValue = subagents

	return m
}

var viewSink string

func BenchmarkView_TrueColor(b *testing.B) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	for _, tc := range []struct {
		w, h, pairs int
	}{
		{120, 30, 20},
		{200, 50, 20},
		{200, 50, 200},
	} {
		m := buildBenchModel(b, tc.pairs)
		m.width = tc.w
		m.height = tc.h

		b.Run(fmt.Sprintf("%dx%d_msgs%d", tc.w, tc.h, tc.pairs*2), func(b *testing.B) {
			b.ReportAllocs()
			// Warm the viewport cache so we measure steady-state per-frame
			// cost (what the user feels during streaming/scrolling), not the
			// one-time cold markdown render.
			_ = m.View()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				viewSink = m.View()
			}
		})
	}
}

// TestViewFrameSize reports the byte size of a rendered frame so the
// reapplyBackground benchmark can be correlated.
func TestViewFrameSize(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	m := buildBenchModel(t, 20)
	m.width = 200
	m.height = 50

	out := m.View()
	resetCount := strings.Count(out, "\x1b[0m")
	t.Logf("frame: %d bytes, %d lines, %d resets", len(out), strings.Count(out, "\n"), resetCount)
}
