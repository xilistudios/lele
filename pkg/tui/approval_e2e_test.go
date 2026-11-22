package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

type execCallProvider struct {
	calls int
}

func (m *execCallProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	m.calls++
	if m.calls == 1 {
		return &providers.LLMResponse{
			Content: "",
			ToolCalls: []providers.ToolCall{
				{
					ID:   "call-exec-1",
					Name: "exec",
					Arguments: map[string]interface{}{
						"command": "rm -rf /tmp/approval-e2e",
					},
				},
			},
			Usage: &providers.UsageInfo{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		}, nil
	}
	return &providers.LLMResponse{
		Content:   "Command executed",
		ToolCalls: []providers.ToolCall{},
		Usage:     &providers.UsageInfo{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}, nil
}

func (m *execCallProvider) GetDefaultModel() string {
	return "mock-model"
}

func TestApproval_E2EExecDangerousCommand(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

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
		Tools: config.ToolsConfig{
			Exec: config.ExecConfig{
				EnableDenyPatterns: true,
				TimeoutSeconds:     60,
			},
		},
	}
	if err := config.SaveConfig(joinPath(tmpDir, "config.json"), cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	msgBus := bus.NewMessageBus()
	al := agent.NewAgentLoop(cfg, msgBus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go al.Run(ctx)

	defaultAgent := al.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("no default agent")
	}
	defaultAgent.Provider = &execCallProvider{}

	sessionMgr := al.SessionManager()
	if sessionMgr == nil {
		t.Fatal("session manager not initialized")
	}

	m := NewModel(cfg, al, sessionMgr)
	m.onboardingActive = false
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:approval-e2e"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.reloadSessions()
	m.viewport.Width = 118

	m.agentLoop.MessageBus().PublishInbound(bus.InboundMessage{
		Channel:    "native",
		SenderID:   "tui",
		ChatID:     key,
		Content:    "run the dangerous command",
		SessionKey: key,
	})

	deadline := time.Now().Add(8 * time.Second)
	gotApproval := false
	for time.Now().Before(deadline) {
		cmd := m.startOutboundListener()
		if cmd == nil {
			break
		}
		msg := cmd()
		if msg == nil {
			break
		}
		om, ok := msg.(outboundMsg)
		if !ok {
			continue
		}
		m2, _ := m.Update(om)
		m = m2.(*Model)
		if m.pendingApprovalID != "" {
			gotApproval = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !gotApproval {
		t.Fatal("BUG: TUI never received approval.request for dangerous command")
	}

	m.updateViewport()
	overlayText := strings.Join(m.viewport.overlayLines, "\n")
	if !strings.Contains(overlayText, "rm -rf /tmp/approval-e2e") {
		t.Fatalf("BUG: approval prompt missing command. overlay:\n%s", overlayText)
	}

	m = sendKeys(m, "y")
	if m.pendingApprovalID != "" {
		t.Fatalf("pendingApprovalID not cleared after y: %q", m.pendingApprovalID)
	}
}
