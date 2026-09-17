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

// commandsTestModel builds a model whose backend loop exposes two config-level
// custom commands. config.Commands is the lowest discovery level and needs no
// fixture files, so the list is deterministic regardless of what happens to live
// in ~/.lele/commands or ./.lele/commands.
func commandsTestModel(t *testing.T) *Model {
	t.Helper()
	cfg := testModelConfig(t)
	cfg.Commands = map[string]config.CommandDefinition{
		"review": {Description: "Review the diff", Agent: "coder", Model: "openai/gpt-5", Template: "review this:\n$ARGUMENTS"},
		"deploy": {Description: "Deploy the app", AllowShell: true, Template: "deploy $ARGUMENTS"},
	}
	return newTestModelWithConfig(t, cfg, true)
}

// --- loadCommandsList -------------------------------------------------------

func TestLoadCommandsList_PopulatesRowsAndKeys(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")

	m.resetModal(ModalCommands)
	m.loadCommandsList()

	// The two config commands must be present; higher levels (global/.lele) may
	// legitimately add more, so assert on containment, not exact length.
	if len(m.modalItems) < 2 {
		t.Fatalf("expected at least 2 rows, got %d: %v", len(m.modalItems), m.modalItems)
	}
	// The three parallel slices must never drift apart, or Enter indexes wrong.
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
	// Config commands have no file of their own.
	if row.Path != "" {
		t.Errorf("config row Path = %q, want empty", row.Path)
	}
	// The label carries the marker, the name, the source badge and the description.
	for _, want := range []string{"●", "deploy", "[config]", "Deploy the app"} {
		if !strings.Contains(m.modalItems[idx], want) {
			t.Errorf("item %q missing %q", m.modalItems[idx], want)
		}
	}

	// Agent/model overrides survive onto the row.
	reviewIdx := commandRowIndexByKey(t, m, "config:review")
	if got := m.commandsRows[reviewIdx]; got.Agent != "coder" || got.Model != "openai/gpt-5" {
		t.Errorf("review row agent/model = %q/%q, want coder/openai/gpt-5", got.Agent, got.Model)
	}
}

func TestLoadCommandsList_EmptyState(t *testing.T) {
	cfg := testModelConfig(t) // no cfg.Commands at all
	// Point the process directory level at a throwaway folder so no checkout
	// .lele/commands can leak into the empty-state assertion.
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

	// Part B appends the separator + "+ New command" action to every list,
	// including the empty state (creating is the way OUT of it).
	if len(m.modalItems) != 3 {
		t.Fatalf("expected empty-state row + separator + new action, got %d: %v", len(m.modalItems), m.modalItems)
	}
	if m.modalItems[0] != i18n.T("tui.commands.none") {
		t.Errorf("empty state = %q, want %q", m.modalItems[0], i18n.T("tui.commands.none"))
	}
	// The empty-state row must be inert: key "" so Enter cannot open a detail.
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
	m.loadCommandsList() // must not panic

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

// --- Enter / detail view ----------------------------------------------------

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
	// The name and the structured fields must reach the rendered frame.
	for _, want := range []string{
		"review",
		i18n.T("tui.commands.source"),
		"[config]",
		i18n.T("tui.commands.path"),
		"config.json", // config commands have no file of their own
		i18n.T("tui.commands.agent") + ": coder",
		i18n.T("tui.commands.model") + ": openai/gpt-5",
		i18n.T("tui.commands.template"),
		"review this:", // the template preview
		i18n.T("tui.commands.detailHints"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail output missing %q:\n%s", want, out)
		}
	}
	// The tri-state flag must say "inherit" when the command does not set it.
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

	// While the detail screen is open the hidden list cursor can still be moved
	// (up/down are not consumed by the detail view); leaving must land back on
	// the row the detail was opened from, not wherever the cursor drifted.
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

	// ESC on the list closes the modal (generic fallthrough, like /skills).
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
	m.commandsDetailKey = "workspace:gone" // no such row

	out := ansi.Strip(m.renderCommandDetail())
	if m.modalMode != ModalCommands || m.commandsDetailMode {
		t.Errorf("state after rendering a vanished row: mode=%v detail=%v, want ModalCommands/false",
			m.modalMode, m.commandsDetailMode)
	}
	if !strings.Contains(out, i18n.T("tui.commands.listHints")) {
		t.Errorf("fallback output is not the list:\n%s", out)
	}
}

// --- commandWorkspace -------------------------------------------------------

func TestCommandsWorkspace_FallsBackToDefaults(t *testing.T) {
	m := commandsTestModel(t)
	// No session yet: the defaults workspace of the config answers.
	want := filepath.Clean(m.cfg.WorkspacePath())
	if got := m.commandsWorkspace(); got != want {
		t.Fatalf("commandsWorkspace() = %q, want %q", got, want)
	}

	// With a session but no explicit agent override, the routed agent's raw
	// workspace is used; the defaults agent has none set, so the answer stays
	// the defaults workspace.
	m.executeCommand("/new")
	if got := m.commandsWorkspace(); got == "" {
		t.Error("commandsWorkspace() with an active session = \"\", want the defaults workspace")
	}
}

// --- helpers ----------------------------------------------------------------

// commandRowIndexByKey returns the index of a composite key in the loaded list,
// failing the test when it is absent.
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

// --- shadowing --------------------------------------------------------------

// TestLoadCommandsList_MarksShadowedRows writes <workspace>/commands/deploy.md,
// which outranks the config-level "deploy" command (config < global < workspace
// < directory). Both rows must be listed: the winner as effective and the config
// one tagged as hidden, so the panel explains why editing config.json would
// change nothing.
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
	// The manager caches per workspace behind a refresh TTL; drop it so the new
	// file is visible immediately, exactly as the REST writers do.
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

	// The detail view of the shadowed row reports the shadow instead of the
	// "effective" badge.
	m.commandsDetailKey = "config:deploy"
	out := ansi.Strip(m.renderCommandDetail())
	if !strings.Contains(out, i18n.T("tui.commands.shadowedBy")+": workspace") {
		t.Errorf("detail of a shadowed row must show the winner:\n%s", out)
	}
}

// --- template preview -------------------------------------------------------

func TestPreviewTemplate_Truncates(t *testing.T) {
	long := strings.Repeat("word ", 400) // 2000 cells on one line
	if got := previewTemplate(long, 3, 100); ansi.StringWidth(got) > 102 || !strings.HasSuffix(got, "…") {
		t.Errorf("cell-bounded preview = %q, want <= ~100 cells with an ellipsis", got)
	}

	manyLines := strings.Repeat("line\n", 40)
	got := previewTemplate(manyLines, 15, 600)
	if n := len(strings.Split(got, "\n")); n > 16 { // 15 lines + the ellipsis tail
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

// --- modal dispatch ---------------------------------------------------------

// TestRenderActiveModal_RoutesCommandModes pins the dispatcher wiring: both new
// modal types must reach their own renderer through renderActiveModal (a missing
// case would silently fall back to the generic list and show the detail as rows).
func TestRenderActiveModal_RoutesCommandModes(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	if updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40}); updated != nil {
		m = updated.(*Model)
	}

	// The /commands command opens the list and loads it.
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

	// Enter through the key handler opens the detail, which must render through
	// the dispatcher as a detail screen (template + back hint).
	updated, cmd := m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		// Enter returns a tick on the detail path; a nil here means the row was
		// inert (separator/empty state) and the routing cannot be verified.
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

// TestAutocomplete_OffersCommands pins the palette entry: /commands must be a
// built-in suggestion (so it wins over any custom command called "commands").
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
