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

// pendingApprovalModel builds a model with a live pending approval and a
// matching real entry in the approval manager (so handleApproval succeeds).
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

// TestApproval_VKeyTogglesFullView covers the new "v" shortcut: it flips the
// full-command view without answering the request, and the flag is reset when
// the request is answered.
func TestApproval_VKeyTogglesFullView(t *testing.T) {
	longCmd := "echo one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen"
	m := pendingApprovalModel(t, longCmd)

	// Preview mode: the tail of the command must NOT be visible.
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

	// Answering the request must reset the view flag.
	m = sendKeys(m, "v")
	m = sendKeys(m, "y")
	if m.approvalShowFull {
		t.Error("approvalShowFull not reset after answering")
	}
}

// TestApproval_WKeyPersistsWhitelist covers the "always allow" flow end to
// end: approve the pending request, append the command to
// tools.exec.whitelist_commands on disk, and hot-apply it to the live agents.
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

	// In-memory config updated...
	var found bool
	for _, c := range m.cfg.Tools.Exec.WhitelistCommands {
		if tools.NormalizeWhitelistKey(c) == tools.NormalizeWhitelistKey(command) {
			found = true
		}
	}
	if !found {
		t.Fatalf("command missing from cfg whitelist: %v", m.cfg.Tools.Exec.WhitelistCommands)
	}

	// ...persisted to disk...
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

	// ...and hot-applied to the running exec tool.
	if !execWhitelisted(m, command) {
		t.Fatal("live exec tool does not know the whitelisted command")
	}
}

// TestApproval_WKeyDeduplicates ensures repeated "always allow" presses for
// the same command (modulo whitespace/case) do not grow the config forever.
func TestApproval_WKeyDeduplicates(t *testing.T) {
	m := pendingApprovalModel(t, "git push origin main")
	m = sendKeys(m, "w")

	// A second, differently-cased request for the same command on the same
	// model must not add a duplicate entry.
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

// TestApproval_WKeyKeepsPromptOnSaveFailure verifies a failed persist does not
// silently consume the approval: the prompt must stay pending so y/n still work.
func TestApproval_WKeyKeepsPromptOnSaveFailure(t *testing.T) {
	command := "echo persist-me"
	m := pendingApprovalModel(t, command)

	// Make the config path unwritable: config.json replaced by a directory.
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

	// y must still work afterwards.
	m = sendKeys(m, "y")
	if m.pendingApprovalID != "" {
		t.Error("y did not answer the approval after a failed w")
	}
}
