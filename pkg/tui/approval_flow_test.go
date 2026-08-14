package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// TestApproval_FullFlowWithAgent tests the complete approval flow:
// TUI publishes an inbound message → agent runs exec tool (dangerous command)
// → approval.request published → TUI receives it → TUI approves → agent
// continues.
func TestApproval_FullFlowWithAgent(t *testing.T) {
	m := newTestModelWithDenyPatterns(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	// Create a session and switch to it
	key := "tui:chat:approval-full"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.reloadSessions()
	m.viewport.Width = 118

	// Register a mock provider so the agent loop can respond
	// (agent loop already runs from newTestModel)

	// Publish an inbound message that triggers the exec tool.
	// Since we can't easily run the full LLM loop here, directly simulate
	// the agent-side flow: create an approval via the shared ApprovalManager
	// and publish approval.request — this is what executeWithApproval does.
	am := m.agentLoop.GetApprovalManager()
	if am == nil {
		t.Fatal("approval manager is nil")
	}

	approval := am.CreateApproval(key, "rm -rf /tmp/danger", "deny pattern matched", 0)

	m.agentLoop.MessageBus().PublishOutbound(bus.OutboundMessage{
		Channel: "native",
		ChatID:  key,
		Event:   "approval.request",
		Metadata: map[string]string{
			"id":      approval.ID,
			"command": "rm -rf /tmp/danger",
			"reason":  "deny pattern matched",
		},
	})

	// Wait for approval in a goroutine (simulates executeWithApproval blocking)
	approvalResult := make(chan bool, 1)
	go func() {
		approved, err := approval.WaitForResponse(context.Background(), 10*time.Second)
		if err != nil {
			approvalResult <- false
			return
		}
		approvalResult <- approved
	}()

	// Deliver the approval.request to the TUI
	got := false
	for i := 0; i < 5; i++ {
		cmd := m.startOutboundListener()
		msg := cmd()
		om, ok := msg.(outboundMsg)
		if !ok {
			continue
		}
		m2, _ := m.Update(om)
		m = m2.(*Model)
		if m.pendingApprovalID != "" {
			got = true
			break
		}
	}
	if !got {
		t.Fatal("BUG: TUI did not receive approval.request")
	}

	// Verify the approval prompt shows the command
	m.updateViewport()
	overlayText := strings.Join(m.viewport.overlayLines, "\n")
	if !strings.Contains(overlayText, "rm -rf /tmp/danger") {
		t.Fatalf("BUG: approval prompt missing command. overlay:\n%s", overlayText)
	}

	// Press 'y' to approve
	m = sendKeys(m, "y")

	// The approval goroutine should be unblocked with approved=true
	select {
	case approved := <-approvalResult:
		if !approved {
			t.Fatal("approval was rejected, want approved")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("approval goroutine was not unblocked after 'y'")
	}
}

// TestApproval_RejectFlow verifies the reject path.
func TestApproval_RejectFlow(t *testing.T) {
	m := newTestModelWithDenyPatterns(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:approval-reject"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.reloadSessions()
	m.viewport.Width = 118

	am := m.agentLoop.GetApprovalManager()
	approval := am.CreateApproval(key, "sudo rm -rf /", "sudo command", 0)

	m.agentLoop.MessageBus().PublishOutbound(bus.OutboundMessage{
		Channel: "native",
		ChatID:  key,
		Event:   "approval.request",
		Metadata: map[string]string{
			"id":      approval.ID,
			"command": "sudo rm -rf /",
			"reason":  "sudo command",
		},
	})

	approvalResult := make(chan bool, 1)
	go func() {
		approved, err := approval.WaitForResponse(context.Background(), 10*time.Second)
		if err != nil {
			approvalResult <- false
			return
		}
		approvalResult <- approved
	}()

	// Deliver to TUI
	for i := 0; i < 5; i++ {
		cmd := m.startOutboundListener()
		msg := cmd()
		om, ok := msg.(outboundMsg)
		if !ok {
			continue
		}
		m2, _ := m.Update(om)
		m = m2.(*Model)
		if m.pendingApprovalID != "" {
			break
		}
	}

	// Press 'n' to reject
	m = sendKeys(m, "n")

	select {
	case approved := <-approvalResult:
		if approved {
			t.Fatal("approval was approved, want rejected")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("approval goroutine was not unblocked after 'n'")
	}
}

// newTestModelWithDenyPatterns builds a TUI model with deny patterns enabled
// so the exec tool triggers the approval flow.
func newTestModelWithDenyPatterns(t *testing.T) *Model {
	t.Helper()
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
		t.Fatalf("saving initial config: %v", err)
	}
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	msgBus := bus.NewMessageBus()
	al := agent.NewAgentLoop(cfg, msgBus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go al.Run(ctx)

	sessionMgr := al.SessionManager()
	if sessionMgr == nil {
		t.Fatal("session manager not initialized")
	}

	return NewModel(cfg, al, sessionMgr)
}

func joinPath(dir, name string) string {
	return fmt.Sprintf("%s/%s", dir, name)
}
