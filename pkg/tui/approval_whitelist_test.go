package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func pendingApprovalModel(t *testing.T, command string) *Model {
	t.Helper()
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:approval-keys-vw"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.reloadSessions()

	am := m.agentLoop.GetApprovalManager()
	if am == nil {
		t.Fatal("approval manager not initialized")
	}
	pa := am.CreateApproval(key, command, "deny pattern", 0)
	m.pendingApprovalID = pa.ID
	m.pendingApprovalCmd = command
	m.pendingApprovalReason = "deny pattern"
	m.processing = true
	m.updateViewport()
	return m
}

func execWhitelisted(m *Model, command string) bool {
	agent := m.agentLoop.GetDefaultAgent()
	if agent == nil || agent.Tools == nil {
		return false
	}
	tool, ok := agent.Tools.Get("exec")
	if !ok {
		return false
	}
	et, ok := tool.(*tools.ExecTool)
	if !ok {
		return false
	}
	return et.Whitelisted(command)
}

func TestApproval_VKeyTogglesFullView(t *testing.T) {
	longCmd := "echo one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen"
	m := pendingApprovalModel(t, longCmd)

	view := m.View()
	if strings.Contains(view, "fifteen") {
		t.Fatal("full command visible before pressing v")
	}

	m = sendKeys(m, "v")
	if !m.approvalShowFull {
		t.Fatal("v did not enable full view")
	}
	if m.pendingApprovalID == "" {
		t.Fatal("v must not answer the approval")
	}
	if !strings.Contains(m.View(), "fifteen") {
		t.Fatal("full command not visible after v")
	}

	m = sendKeys(m, "v")
	if m.approvalShowFull {
		t.Fatal("second v did not return to preview")
	}
	if strings.Contains(m.View(), "fifteen") {
		t.Fatal("full command still visible after toggling back")
	}

	m = sendKeys(m, "v")
	m = sendKeys(m, "y")
	if m.approvalShowFull {
		t.Error("approvalShowFull not reset after answering")
	}
}

func TestApproval_WKeyPersistsWhitelist(t *testing.T) {
	command := "rm -rf /tmp/always-allow-me"
	m := pendingApprovalModel(t, command)

	if execWhitelisted(m, command) {
		t.Fatal("command already whitelisted before pressing w")
	}

	m = sendKeys(m, "w")

	if m.pendingApprovalID != "" {
		t.Error("w did not clear the pending approval (should approve)")
	}
	if !strings.Contains(m.approvalResult, i18n.T("tui.approvalWhitelisted")) {
		t.Errorf("w did not surface whitelisted feedback, got %q", m.approvalResult)
	}

	var found bool
	for _, c := range m.cfg.Tools.Exec.WhitelistCommands {
		if tools.NormalizeWhitelistKey(c) == tools.NormalizeWhitelistKey(command) {
			found = true
		}
	}
	if !found {
		t.Fatalf("command missing from cfg whitelist: %v", m.cfg.Tools.Exec.WhitelistCommands)
	}

	data, err := os.ReadFile(filepath.Join(config.DefaultConfigPath()))
	if err != nil {
		t.Fatalf("reading saved config: %v", err)
	}
	var saved config.Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("parsing saved config: %v", err)
	}
	found = false
	for _, c := range saved.Tools.Exec.WhitelistCommands {
		if tools.NormalizeWhitelistKey(c) == tools.NormalizeWhitelistKey(command) {
			found = true
		}
	}
	if !found {
		t.Fatalf("command missing from on-disk whitelist: %v", saved.Tools.Exec.WhitelistCommands)
	}

	if !execWhitelisted(m, command) {
		t.Fatal("live exec tool does not know the whitelisted command")
	}
}

func TestApproval_WKeyDeduplicates(t *testing.T) {
	m := pendingApprovalModel(t, "git push origin main")
	m = sendKeys(m, "w")

	am := m.agentLoop.GetApprovalManager()
	pa := am.CreateApproval(m.currentKey, "GIT   PUSH ORIGIN MAIN", "deny pattern", 0)
	m.pendingApprovalID = pa.ID
	m.pendingApprovalCmd = pa.Command
	m = sendKeys(m, "w")

	var count int
	for _, c := range m.cfg.Tools.Exec.WhitelistCommands {
		if tools.NormalizeWhitelistKey(c) == "git push origin main" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("whitelist entry duplicated: %d copies in %v", count, m.cfg.Tools.Exec.WhitelistCommands)
	}
}

func TestApproval_WKeyKeepsPromptOnSaveFailure(t *testing.T) {
	command := "echo persist-me"
	m := pendingApprovalModel(t, command)

	if err := os.Remove(config.DefaultConfigPath()); err != nil {
		t.Fatalf("removing config file: %v", err)
	}
	if err := os.Mkdir(config.DefaultConfigPath(), 0o755); err != nil {
		t.Fatalf("blocking config path: %v", err)
	}

	m = sendKeys(m, "w")

	if m.pendingApprovalID == "" {
		t.Fatal("approval consumed despite failed persist")
	}
	for _, c := range m.cfg.Tools.Exec.WhitelistCommands {
		if c == command {
			t.Fatal("config mutated despite save failure — persist-first ordering broken")
		}
	}
	if !strings.Contains(m.approvalResult, "⚠️") {
		t.Errorf("expected warning feedback on failure, got %q", m.approvalResult)
	}

	m = sendKeys(m, "y")
	if m.pendingApprovalID != "" {
		t.Error("y did not answer the approval after a failed w")
	}
}
