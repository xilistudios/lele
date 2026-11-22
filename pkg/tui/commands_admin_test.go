package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func commandsTestModel(t *testing.T) *Model {
	t.Helper()
	cfg := testModelConfig(t)
	cfg.Commands = map[string]config.CommandDefinition{
		"review": {Description: "Review the diff", Agent: "coder", Model: "openai/gpt-5", Template: "review this:\n$ARGUMENTS"},
		"deploy": {Description: "Deploy the app", AllowShell: true, Template: "deploy $ARGUMENTS"},
	}
	return newTestModelWithConfig(t, cfg, true)
}

func TestLoadCommandsList_PopulatesRowsAndKeys(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")

	m.resetModal(ModalCommands)
	m.loadCommandsList()

	if len(m.modalItems) < 2 {
		t.Fatalf("expected at least 2 rows, got %d: %v", len(m.modalItems), m.modalItems)
	}
	if len(m.commandsModalKeys) != len(m.modalItems) || len(m.commandsRows) != len(m.modalItems) {
		t.Fatalf("items/keys/rows out of sync: %d/%d/%d",
			len(m.modalItems), len(m.commandsModalKeys), len(m.commandsRows))
	}

	idx := commandRowIndexByKey(t, m, "config:deploy")
	row := m.commandsRows[idx]
	if row.Name != "deploy" || row.Source != "config" {
		t.Errorf("row = %+v, want deploy/config", row)
	}
	if !row.Effective || row.ShadowedBy != "" {
		t.Errorf("row must be effective with no shadow, got %+v", row)
	}
	if !row.AllowShell {
		t.Error("AllowShell must resolve to true for the deploy command")
	}
	if row.Path != "" {
		t.Errorf("config row Path = %q, want empty", row.Path)
	}
	for _, want := range []string{"●", "deploy", "[config]", "Deploy the app"} {
		if !strings.Contains(m.modalItems[idx], want) {
			t.Errorf("item %q missing %q", m.modalItems[idx], want)
		}
	}

	reviewIdx := commandRowIndexByKey(t, m, "config:review")
	if got := m.commandsRows[reviewIdx]; got.Agent != "coder" || got.Model != "openai/gpt-5" {
		t.Errorf("review row agent/model = %q/%q, want coder/openai/gpt-5", got.Agent, got.Model)
	}
}

func TestLoadCommandsList_EmptyState(t *testing.T) {
	cfg := testModelConfig(t)
	tmp := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	m := newTestModelWithConfig(t, cfg, true)
	m.executeCommand("/new")

	m.resetModal(ModalCommands)
	m.loadCommandsList()

	if len(m.modalItems) != 3 {
		t.Fatalf("expected empty-state row + separator + new action, got %d: %v", len(m.modalItems), m.modalItems)
	}
	if m.modalItems[0] != i18n.T("tui.commands.none") {
		t.Errorf("empty state = %q, want %q", m.modalItems[0], i18n.T("tui.commands.none"))
	}
	if m.commandsModalKeys[0] != "" {
		t.Errorf("empty-state key = %q, want \"\"", m.commandsModalKeys[0])
	}
	if m.commandsModalKeys[2] != commandsNewKey {
		t.Errorf("last key = %q, want %q", m.commandsModalKeys[2], commandsNewKey)
	}
	if cmd := m.handleCommandsEnter(); cmd != nil {
		t.Error("Enter on the empty state must be a no-op")
	}
}

func TestLoadCommandsList_NilLoopIsSafe(t *testing.T) {
	m := &Model{}
	m.resetModal(ModalCommands)
	m.loadCommandsList()

	if len(m.modalItems) < 1 || m.modalItems[0] != i18n.T("tui.commands.none") {
		t.Errorf("nil loop list = %v, want the empty-state row first", m.modalItems)
	}
	if got := m.commandsWorkspace(); got != "" {
		t.Errorf("commandsWorkspace() with nil loop = %q, want \"\"", got)
	}
	if out := m.renderCommandDetail(); out == "" {
		t.Error("renderCommandDetail with a nil loop must still render the list")
	}
}

func TestCommandsEnter_OpensDetailAndRenders(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	if updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40}); updated != nil {
		m = updated.(*Model)
	}

	m.resetModal(ModalCommands)
	m.loadCommandsList()
	m.modalSelectedIdx = commandRowIndexByKey(t, m, "config:review")

	if cmd := m.handleCommandsEnter(); cmd == nil {
		t.Fatal("Enter on a command row must return a tick command")
	}
	if m.modalMode != ModalCommandDetail || !m.commandsDetailMode {
		t.Fatalf("after Enter: mode=%v detail=%v, want ModalCommandDetail/true", m.modalMode, m.commandsDetailMode)
	}
	if m.commandsDetailKey != "config:review" {
		t.Errorf("commandsDetailKey = %q, want config:review", m.commandsDetailKey)
	}

	out := ansi.Strip(m.renderCommandDetail())
	for _, want := range []string{
		"review",
		i18n.T("tui.commands.source"),
		"[config]",
		i18n.T("tui.commands.path"),
		"config.json",
		i18n.T("tui.commands.agent") + ": coder",
		i18n.T("tui.commands.model") + ": openai/gpt-5",
		i18n.T("tui.commands.template"),
		"review this:",
		i18n.T("tui.commands.detailHints"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "inherit") {
		t.Errorf("detail output missing the inherit marker for allow_absolute_files:\n%s", out)
	}
}

func TestCommandsDetail_ESCReturnsToList(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.loadCommandsList()
	m.modalSelectedIdx = commandRowIndexByKey(t, m, "config:deploy")
	m.handleCommandsEnter()

	m.modalSelectedIdx = commandRowIndexByKey(t, m, "config:review")

	updated, _ := m.handleModalKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(*Model)

	if m.modalMode != ModalCommands {
		t.Fatalf("mode after ESC = %v, want ModalCommands", m.modalMode)
	}
	if m.commandsDetailMode || m.commandsDetailKey != "" {
		t.Errorf("detail state not cleared: mode=%v key=%q", m.commandsDetailMode, m.commandsDetailKey)
	}
	if got := m.selectedCommandKey(); got != "config:deploy" {
		t.Errorf("cursor after returning = %q, want config:deploy", got)
	}

	updated, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(*Model)
	if m.modalMode != ModalNone {
		t.Errorf("mode after the second ESC = %v, want ModalNone", m.modalMode)
	}
}

func TestRenderCommandDetail_MissingRowFallsBackToList(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.loadCommandsList()
	m.commandsDetailMode = true
	m.commandsDetailKey = "workspace:gone"

	out := ansi.Strip(m.renderCommandDetail())
	if m.modalMode != ModalCommands || m.commandsDetailMode {
		t.Errorf("state after rendering a vanished row: mode=%v detail=%v, want ModalCommands/false",
			m.modalMode, m.commandsDetailMode)
	}
	if !strings.Contains(out, i18n.T("tui.commands.listHints")) {
		t.Errorf("fallback output is not the list:\n%s", out)
	}
}

func TestCommandsWorkspace_FallsBackToDefaults(t *testing.T) {
	m := commandsTestModel(t)
	want := filepath.Clean(m.cfg.WorkspacePath())
	if got := m.commandsWorkspace(); got != want {
		t.Fatalf("commandsWorkspace() = %q, want %q", got, want)
	}

	m.executeCommand("/new")
	if got := m.commandsWorkspace(); got == "" {
		t.Error("commandsWorkspace() with an active session = \"\", want the defaults workspace")
	}
}

func commandRowIndexByKey(t *testing.T, m *Model, key string) int {
	t.Helper()
	for i, k := range m.commandsModalKeys {
		if k == key {
			return i
		}
	}
	t.Fatalf("key %q not in %v", key, m.commandsModalKeys)
	return -1
}

func TestLoadCommandsList_MarksShadowedRows(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")

	commandsDir := filepath.Join(m.cfg.WorkspacePath(), "commands")
	if err := os.MkdirAll(commandsDir, 0o755); err != nil {
		t.Fatalf("mkdir commands dir: %v", err)
	}
	md := "---\ndescription: Workspace deploy\n---\ndeploy from the workspace\n"
	if err := os.WriteFile(filepath.Join(commandsDir, "deploy.md"), []byte(md), 0o644); err != nil {
		t.Fatalf("write command file: %v", err)
	}
	m.agentLoop.InvalidateHarnessWorkspace(m.commandsWorkspace())

	m.resetModal(ModalCommands)
	m.loadCommandsList()

	wsIdx := commandRowIndexByKey(t, m, "workspace:deploy")
	cfgIdx := commandRowIndexByKey(t, m, "config:deploy")

	winner := m.commandsRows[wsIdx]
	if !winner.Effective || winner.ShadowedBy != "" {
		t.Errorf("workspace row must win, got %+v", winner)
	}
	if winner.Path == "" {
		t.Error("file-backed row must carry its path")
	}
	if !strings.Contains(m.modalItems[wsIdx], "●") {
		t.Errorf("effective row must use the ● marker: %q", m.modalItems[wsIdx])
	}

	shadowed := m.commandsRows[cfgIdx]
	if shadowed.Effective {
		t.Errorf("config row must not be effective once a workspace file exists: %+v", shadowed)
	}
	if shadowed.ShadowedBy != "workspace" {
		t.Errorf("config row ShadowedBy = %q, want workspace", shadowed.ShadowedBy)
	}
	label := m.modalItems[cfgIdx]
	if !strings.Contains(label, "○") {
		t.Errorf("shadowed row must use the ○ marker: %q", label)
	}
	if !strings.Contains(label, i18n.T("tui.commands.shadowedBy")+": workspace") {
		t.Errorf("shadowed row must name its winner: %q", label)
	}

	m.commandsDetailKey = "config:deploy"
	out := ansi.Strip(m.renderCommandDetail())
	if !strings.Contains(out, i18n.T("tui.commands.shadowedBy")+": workspace") {
		t.Errorf("detail of a shadowed row must show the winner:\n%s", out)
	}
}

func TestPreviewTemplate_Truncates(t *testing.T) {
	long := strings.Repeat("word ", 400)
	if got := previewTemplate(long, 3, 100); ansi.StringWidth(got) > 102 || !strings.HasSuffix(got, "…") {
		t.Errorf("cell-bounded preview = %q, want <= ~100 cells with an ellipsis", got)
	}

	manyLines := strings.Repeat("line\n", 40)
	got := previewTemplate(manyLines, 15, 600)
	if n := len(strings.Split(got, "\n")); n > 16 {
		t.Errorf("line-bounded preview has %d lines, want <= 16", n)
	}
	if !strings.Contains(got, "line") {
		t.Errorf("preview lost its content: %q", got)
	}

	if got := previewTemplate("  short one  \n\n", 15, 600); got != "short one" {
		t.Errorf("preview = %q, want the trimmed text with no ellipsis", got)
	}
	if got := previewTemplate("   ", 15, 600); got != "" {
		t.Errorf("blank template preview = %q, want \"\"", got)
	}
}

func TestRenderActiveModal_RoutesCommandModes(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	if updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40}); updated != nil {
		m = updated.(*Model)
	}

	if cmd := m.executeCommand("/commands"); cmd != nil {
		t.Errorf("/commands must be handled locally, got %v", cmd)
	}
	if m.modalMode != ModalCommands {
		t.Fatalf("modalMode = %v, want ModalCommands", m.modalMode)
	}
	list := ansi.Strip(m.renderActiveModal())
	if !strings.Contains(list, i18n.T("tui.commands")) {
		t.Errorf("list view missing the modal title:\n%s", list)
	}
	if !strings.Contains(list, i18n.T("tui.commands.listHints")) {
		t.Errorf("list view missing the hint line:\n%s", list)
	}
	if !strings.Contains(list, "deploy") {
		t.Errorf("list view missing the command rows:\n%s", list)
	}

	updated, cmd := m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a tick command from Enter on a command row")
	}
	m = updated.(*Model)
	if m.modalMode != ModalCommandDetail {
		t.Fatalf("modalMode after Enter = %v, want ModalCommandDetail", m.modalMode)
	}
	detail := ansi.Strip(m.renderActiveModal())
	for _, want := range []string{i18n.T("tui.commands.template"), i18n.T("tui.commands.detailHints")} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail view missing %q:\n%s", want, detail)
		}
	}
}

func TestAutocomplete_OffersCommands(t *testing.T) {
	m := commandsTestModel(t)

	m.filterAutocomplete("/comm")
	names := autocompleteNames(m.autocompleteItems)
	if len(names) != 1 || names[0] != "/commands" {
		t.Fatalf("autocomplete for /comm = %v, want [/commands]", names)
	}
	for _, it := range m.autocompleteItems {
		if it.name == "/commands" && it.description == "" {
			t.Error("/commands needs a description in the palette")
		}
	}
}
