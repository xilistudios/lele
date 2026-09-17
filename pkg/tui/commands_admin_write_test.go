package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/harness"
)

// Part B tests: the create/edit/delete write flow of the /commands panel.
// testModelConfig points LELE_CONFIG_DIR and the defaults workspace at temp
// dirs, so config.json rewrites and command files can never touch the real
// user state.

// keyMsg builds a tea.KeyMsg with a single rune key (the same shape bubbletea
// produces for typed characters).
func keyMsg(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// runWizardKeys drives handleModalKey with a list of key strings, mirroring how
// the real event loop would deliver them.
func runWizardKeys(t *testing.T, m *Model, keys ...string) {
	t.Helper()
	for _, k := range keys {
		msg := tea.KeyMsg{Type: tea.KeyEnter}
		switch k {
		case "enter":
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEscape}
		default:
			if len([]rune(k)) > 1 {
				t.Fatalf("runWizardKeys takes one key per argument, got %q", k)
			}
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		_, cmd := m.handleModalKey(msg)
		_ = cmd // commands are tick/side-effect hints; state is what we assert
	}
}

// fillWizard types each step's text and presses enter, ending with the
// template step (typed into the textarea directly, then enter to save). It
// tolerates a wizard that stops early (validation tests) — the save is only
// attempted when the template step is actually reached.
func fillWizard(t *testing.T, m *Model, name, description, template string) {
	t.Helper()
	m.textInput.SetValue(name)
	runWizardKeys(t, m, "enter") // step 0 -> 1
	m.textInput.SetValue(description)
	runWizardKeys(t, m, "enter") // 1 -> 2 (agent)
	runWizardKeys(t, m, "enter") // 2 -> 3 (model)
	runWizardKeys(t, m, "enter") // 3 -> 4 (allow_shell)
	runWizardKeys(t, m, "enter") // 4 default "no" -> 5
	runWizardKeys(t, m, "enter") // 5 default "inherit" -> 6 (template)
	if m.formStepIndex != commandTemplateStep || m.modalMode != ModalAddCommand {
		return // the wizard stopped on a validation error; caller asserts
	}
	m.templateInput.SetValue(template)
	runWizardKeys(t, m, "enter") // save
}

// --- serialization ----------------------------------------------------------

func TestSerializeCommandMarkdown_RoundTrip(t *testing.T) {
	abs := false
	md := serializeCommandMarkdown("Review the diff", "coder", "openai/gpt-5", true, &abs, "review this:\n$ARGUMENTS")
	cmd, err := harness.ParseCommandMarkdown("review", "review.md", []byte(md))
	if err != nil {
		t.Fatalf("parse: %v (md=%q)", err, md)
	}
	if cmd.Description != "Review the diff" || cmd.Agent != "coder" || cmd.Model != "openai/gpt-5" {
		t.Errorf("frontmatter round-trip lost fields: %+v", cmd)
	}
	if !cmd.AllowShell {
		t.Error("allow_shell did not survive the round-trip")
	}
	if cmd.AllowAbsoluteFiles == nil || *cmd.AllowAbsoluteFiles != false {
		t.Errorf("allow_absolute_files = %v, want explicit false", cmd.AllowAbsoluteFiles)
	}
	if strings.TrimSpace(cmd.Template) != "review this:\n$ARGUMENTS" {
		t.Errorf("template = %q", cmd.Template)
	}
}

func TestSerializeCommandMarkdown_NoFrontmatterWhenEmpty(t *testing.T) {
	md := serializeCommandMarkdown("", "", "", false, nil, "just a body")
	if md != "just a body" {
		t.Errorf("expected bare template, got %q", md)
	}
}

func TestSerializeCommandMarkdown_QuotesColonValues(t *testing.T) {
	md := serializeCommandMarkdown("does: things", "", "", false, nil, "body")
	cmd, err := harness.ParseCommandMarkdown("x", "x.md", []byte(md))
	if err != nil {
		t.Fatalf("parse: %v (md=%q)", err, md)
	}
	if cmd.Description != "does: things" {
		t.Errorf("description = %q, want %q", cmd.Description, "does: things")
	}
}

// --- name validation ---------------------------------------------------------

func TestValidateCommandName(t *testing.T) {
	for _, good := range []string{"review", "my.cmd", "a_b-1", "deploy"} {
		if _, err := validateCommandName(good); err != nil {
			t.Errorf("%q rejected: %v", good, err)
		}
	}
	for _, bad := range []string{"", "Review", "-x", "review.md", strings.Repeat("a", 65), "has space", "../escape", "foo/bar", "a\\b", "..", "."} {
		if _, err := validateCommandName(bad); err == nil {
			t.Errorf("%q accepted, want rejection", bad)
		}
	}
}

// --- create (workspace scope) -------------------------------------------------

func TestCommandsCreate_WritesWorkspaceFile(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	ws := m.commandsWorkspace()
	if ws == "" {
		t.Fatal("no workspace resolved")
	}

	m.resetModal(ModalCommands)
	m.loadCommandsList()

	// The create action must be the last row.
	if got := m.commandsModalKeys[len(m.commandsModalKeys)-1]; got != commandsNewKey {
		t.Fatalf("last row key = %q, want %q", got, commandsNewKey)
	}
	m.modalSelectedIdx = len(m.modalItems) - 1
	runWizardKeys(t, m, "enter") // open the wizard via the "+ New command" row
	if m.modalMode != ModalAddCommand || m.commandsEditScope != "workspace" {
		t.Fatalf("wizard not open: mode=%v scope=%q", m.modalMode, m.commandsEditScope)
	}

	fillWizard(t, m, "greet", "Say hi", "say hello to $ARGUMENTS")

	if m.modalMode != ModalCommands {
		t.Fatalf("wizard did not close after save: mode=%v err=%q", m.modalMode, m.formError)
	}
	path := filepath.Join(ws, "commands", "greet.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("command file missing: %v", err)
	}
	if !strings.Contains(string(raw), "description: Say hi") || !strings.Contains(string(raw), "say hello to $ARGUMENTS") {
		t.Errorf("unexpected file content:\n%s", raw)
	}
	// The saved command must be immediately visible and effective in the list.
	if _, _, ok := m.lookupCommandRow("workspace:greet"); !ok {
		t.Errorf("row not in list: %v", m.modalItems)
	}
	if m.commandsFeedback == "" {
		t.Error("save must surface a feedback line")
	}
	// ...and immediately runnable: the manager was invalidated, so the merged
	// registry contains it (this is what /greet would expand from).
	mgr := m.commandsManager(ws)
	if cmd, ok := mgr.Registry().Get("greet"); !ok || cmd == nil {
		t.Fatal("registry miss after create — cache invalidation broken")
	}
}

func TestCommandsCreate_RejectsReservedName(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.startCommandCreate()

	fillWizard(t, m, "help", "shadow the built-in", "body")

	if m.formError == "" {
		t.Fatal("reserved name accepted")
	}
	if m.formStepIndex != commandNameRequired {
		t.Errorf("user should be parked on the name step, got %d", m.formStepIndex)
	}
	if _, err := os.Stat(filepath.Join(m.commandsWorkspace(), "commands", "help.md")); !os.IsNotExist(err) {
		t.Error("file written despite validation failure")
	}
}

func TestCommandsCreate_RejectsEmptyTemplate(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.startCommandCreate()

	m.textInput.SetValue("x")
	runWizardKeys(t, m, "enter")
	m.textInput.SetValue("desc")
	runWizardKeys(t, m, "enter", "enter", "enter", "enter", "enter") // -> template step
	m.templateInput.SetValue("   ")
	runWizardKeys(t, m, "enter") // save attempt

	if m.formError == "" || m.modalMode != ModalAddCommand {
		t.Fatalf("empty template accepted: err=%q mode=%v", m.formError, m.modalMode)
	}
}

// --- edit ---------------------------------------------------------------------

func TestCommandsEdit_ConfigPersists(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.loadCommandsList()

	m.startCommandEdit("config:review")
	if m.modalMode != ModalAddCommand || m.commandsEditScope != "config" {
		t.Fatalf("edit wizard not open: mode=%v scope=%q err=%q", m.modalMode, m.commandsEditScope, m.commandsFeedback)
	}
	// The wizard must be pre-filled from the definition being edited.
	if m.formValues[0] != "review" || m.formValues[1] != "Review the diff" {
		t.Errorf("prefill wrong: %q/%q", m.formValues[0], m.formValues[1])
	}
	if !strings.Contains(m.formValues[6], "review this:") {
		t.Errorf("template prefill wrong: %q", m.formValues[6])
	}

	m.textInput.SetValue("review")
	runWizardKeys(t, m, "enter") // 0 -> 1 (name unchanged)
	m.textInput.SetValue("Tweak")
	runWizardKeys(t, m, "enter") // 1 -> 2 (agent)
	runWizardKeys(t, m, "enter") // 2 -> 3 (model)
	runWizardKeys(t, m, "enter") // 3 -> 4 (allow_shell)
	runWizardKeys(t, m, "enter") // 4 -> 5 (abs files)
	runWizardKeys(t, m, "enter") // 5 -> 6 (template)
	m.templateInput.SetValue("new body")
	runWizardKeys(t, m, "enter") // save

	def, ok := m.cfg.Commands["review"]
	if !ok {
		t.Fatalf("config command vanished: %v", m.cfg.Commands)
	}
	if def.Description != "Tweak" || def.Template != "new body" {
		t.Errorf("config not updated: %+v", def)
	}
	// saveConfigToDisk must have persisted to the (temp) config.json.
	raw, err := os.ReadFile(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(raw), "Tweak") {
		t.Error("config.json does not contain the new description")
	}
}

func TestCommandsEdit_RenameWorkspaceFile(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.startCommandCreate()
	fillWizard(t, m, "oldname", "desc", "body")
	ws := m.commandsWorkspace()
	oldPath := filepath.Join(ws, "commands", "oldname.md")
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	m.startCommandEdit("workspace:oldname")
	m.textInput.SetValue("newname")
	runWizardKeys(t, m, "enter") // 0 -> 1 (name accepted)
	m.textInput.SetValue("desc")
	runWizardKeys(t, m, "enter")          // 1 -> 2
	runWizardKeys(t, m, "enter", "enter") // agent, model
	runWizardKeys(t, m, "enter", "enter") // shell, abs
	m.templateInput.SetValue("body")
	runWizardKeys(t, m, "enter") // save

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old file survives a rename")
	}
	if _, err := os.Stat(filepath.Join(ws, "commands", "newname.md")); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}
}

func TestCommandsEdit_DirectoryReadOnly(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	// A directory row cannot be produced without polluting the package cwd, so
	// inject one directly into the cached slices and exercise the guard.
	m.resetModal(ModalCommands)
	m.loadCommandsList()
	m.modalItems = append(m.modalItems, "● dir-cmd [directory]")
	key := string(harness.SourceDirectory) + ":dir-cmd"
	m.commandsModalKeys = append(m.commandsModalKeys, key)
	m.commandsRows = append(m.commandsRows, commandRow{Name: "dir-cmd", Source: string(harness.SourceDirectory)})

	m.startCommandEdit(key)
	if m.modalMode == ModalAddCommand {
		t.Error("wizard opened for a directory command")
	}
	if m.commandsFeedback == "" {
		t.Error("no feedback for the refused edit")
	}
	runWizardKeys(t, m, "d") // delete attempt on the same row
	if m.commandsFeedback == "" {
		t.Error("no feedback for the refused delete")
	}
}

// --- delete -------------------------------------------------------------------

func TestCommandsDelete_WorkspaceFile(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.startCommandCreate()
	fillWizard(t, m, "temp", "desc", "body")

	ws := m.commandsWorkspace()
	path := filepath.Join(ws, "commands", "temp.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	idx := commandRowIndexByKey(t, m, "workspace:temp")
	m.modalSelectedIdx = idx
	runWizardKeys(t, m, "d")

	// The first press only arms the confirmation; the file must survive.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file removed by a single unconfirmed 'd': %v", err)
	}
	if m.commandsDeleteKey != "workspace:temp" {
		t.Errorf("delete not armed after first press: key=%q", m.commandsDeleteKey)
	}
	runWizardKeys(t, m, "d")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file not removed")
	}
	if _, _, ok := m.lookupCommandRow("workspace:temp"); ok {
		t.Error("row still listed after delete")
	}
	if m.commandsFeedback == "" {
		t.Error("delete must surface a feedback line")
	}
}

func TestCommandsDelete_ConfigEntry(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.loadCommandsList()

	idx := commandRowIndexByKey(t, m, "config:deploy")
	m.modalSelectedIdx = idx
	runWizardKeys(t, m, "d") // arm
	runWizardKeys(t, m, "d") // confirm

	if _, ok := m.cfg.Commands["deploy"]; ok {
		t.Error("config entry still present after delete")
	}
	raw, err := os.ReadFile(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if strings.Contains(string(raw), "Deploy the app") {
		t.Error("deleted command persisted in config.json")
	}
}

// --- navigation ---------------------------------------------------------------

func TestCommandsWizard_ESCReturnsToList(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.loadCommandsList()
	m.startCommandEdit("config:review")
	runWizardKeys(t, m, "esc")

	if m.modalMode != ModalCommands {
		t.Fatalf("ESC did not return to the list: %v", m.modalMode)
	}
	if m.commandsEditKey != "" || m.formValues != nil {
		t.Error("wizard state not cleared")
	}
	// The list must be intact and the cursor back on the edited row.
	if _, _, ok := m.lookupCommandRow("config:review"); !ok {
		t.Fatal("list lost the row")
	}
	if key := m.selectedCommandKey(); key != "config:review" {
		t.Errorf("cursor = %q, want config:review", key)
	}
}

func TestCommandsList_NewKeyOpensWizard(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.loadCommandsList()
	runWizardKeys(t, m, "n")
	if m.modalMode != ModalAddCommand || m.commandsEditKey != "" {
		t.Fatalf("n did not open the create wizard: mode=%v editKey=%q", m.modalMode, m.commandsEditKey)
	}
	// While the wizard is open, "n" is just a character: the modal must not
	// re-open or reset.
	before := m.formStepIndex
	runWizardKeys(t, m, "n")
	if m.formStepIndex != before || m.modalMode != ModalAddCommand {
		t.Error("n re-triggered inside the wizard")
	}
}

func TestCommandsWizard_RejectsBadShellAnswer(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.startCommandCreate()
	m.textInput.SetValue("ok")
	runWizardKeys(t, m, "enter") // name
	m.textInput.SetValue("d")
	runWizardKeys(t, m, "enter") // description
	runWizardKeys(t, m, "enter") // agent
	runWizardKeys(t, m, "enter") // model
	m.textInput.SetValue("maybe")
	runWizardKeys(t, m, "enter") // allow_shell — invalid
	if m.formError == "" || m.formStepIndex != 4 {
		t.Fatalf("bad shell answer accepted: err=%q step=%d", m.formError, m.formStepIndex)
	}
	m.textInput.SetValue("yes")
	runWizardKeys(t, m, "enter")
	if m.formValues[4] != "yes" {
		t.Errorf("normalized shell value = %q", m.formValues[4])
	}
}

func TestCommandsWizard_ShellCommandPersists(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	ws := m.commandsWorkspace()
	m.resetModal(ModalCommands)
	m.startCommandCreate()
	m.textInput.SetValue("run")
	runWizardKeys(t, m, "enter")
	m.textInput.SetValue("runs stuff")
	runWizardKeys(t, m, "enter")
	runWizardKeys(t, m, "enter", "enter") // agent, model
	m.textInput.SetValue("yes")
	runWizardKeys(t, m, "enter") // allow_shell
	runWizardKeys(t, m, "enter") // abs files -> inherit
	m.templateInput.SetValue("do it")
	runWizardKeys(t, m, "enter") // save

	raw, err := os.ReadFile(filepath.Join(ws, "commands", "run.md"))
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	if !strings.Contains(string(raw), "allow_shell: true") {
		t.Errorf("allow_shell missing:\n%s", raw)
	}
	if strings.Contains(string(raw), "allow_absolute_files") {
		t.Errorf("inherit must not write a line:\n%s", raw)
	}
}

func TestCommandsWizard_RendersAllSteps(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.startCommandCreate()
	for step := 0; step <= commandTemplateStep; step++ {
		m.formStepIndex = step
		out := m.renderActiveModal()
		if out == "" {
			t.Fatalf("step %d rendered empty", step)
		}
		if step == commandTemplateStep {
			if !strings.Contains(out, "alt+enter") && !strings.Contains(out, "ENTER") {
				t.Errorf("template step missing hints:\n%s", out)
			}
		}
		// advance so the next iteration has valid data
		m.textInput.SetValue("x")
		m.formValues[step] = "x"
	}
}

// --- review hardening (C1/M1/M2/C2) -------------------------------------------

func TestSerializeCommandMarkdown_FlattensNewlineValues(t *testing.T) {
	// C1: a value carrying a newline must never become a second frontmatter
	// line — that is how allow_shell could be smuggled in.
	md := serializeCommandMarkdown("evil\ndescription: x\nallow_shell: true", "", "", false, nil, "body")
	cmd, err := harness.ParseCommandMarkdown("x", "x.md", []byte(md))
	if err != nil {
		t.Fatalf("parse: %v (md=%q)", err, md)
	}
	if cmd.AllowShell {
		t.Errorf("injected allow_shell took effect; md=%q", md)
	}
	if strings.Contains(cmd.Description, "\n") {
		t.Errorf("description kept a newline: %q", cmd.Description)
	}
	if !strings.Contains(md, "description: ") {
		t.Errorf("missing description line: %q", md)
	}
}

func TestSerializeCommandMarkdown_EscapesBackslashes(t *testing.T) {
	// M1: backslashes must be escaped so the closing quote is not swallowed.
	md := serializeCommandMarkdown(`he said "hi"\path`, "", "", false, nil, "body")
	cmd, err := harness.ParseCommandMarkdown("x", "x.md", []byte(md))
	if err != nil {
		t.Fatalf("parse: %v (md=%q)", err, md)
	}
	if strings.Contains(cmd.Description, "\n") || cmd.Description == "" {
		t.Errorf("description round-trip broke: %q", cmd.Description)
	}
	// The serialized line must still be a single "description: ..." entry.
	if lines := strings.Split(md, "\n"); len(lines) < 2 || !strings.HasPrefix(lines[1], "description: ") {
		t.Errorf("frontmatter shape broke: %q", md)
	}
}

func TestCommandsCreate_RejectsTemplateStartingWithFrontmatter(t *testing.T) {
	// M2: a body that opens with --- would sit next to the real block.
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.startCommandCreate()
	fillWizard(t, m, "fmstart", "desc", "---\nagent: victim\n---\nbody")
	if m.formError == "" {
		t.Fatal("template starting with --- must be rejected")
	}
	if _, err := os.Stat(filepath.Join(m.commandsWorkspace(), "commands", "fmstart.md")); !os.IsNotExist(err) {
		t.Error("file written despite rejected template")
	}
}

func TestCommandFileWithinScope(t *testing.T) {
	// C2: deletes are gated to direct markdown children of the scope dir.
	dir := filepath.Join(string(filepath.Separator), "ws", "commands")
	okRow := commandRow{Path: filepath.Join(dir, "review.md")}
	if err := commandFileWithinScope(okRow, "workspace", dir); err != nil {
		t.Errorf("valid path rejected: %v", err)
	}
	for _, bad := range []commandRow{
		{Path: ""},
		{Path: "/etc/passwd"},
		{Path: filepath.Join(dir, "..", "..", "secrets.md")},
		{Path: filepath.Join(dir, "notes", "deep.md")},
		{Path: filepath.Join(dir, "script.sh")},
	} {
		if err := commandFileWithinScope(bad, "workspace", dir); err == nil {
			t.Errorf("path %q accepted, want rejection", bad.Path)
		}
	}
}

func TestCommandsDelete_ArmingIsClearedByNavigation(t *testing.T) {
	m := commandsTestModel(t)
	m.executeCommand("/new")
	m.resetModal(ModalCommands)
	m.loadCommandsList()

	idx := commandRowIndexByKey(t, m, "config:deploy")
	m.modalSelectedIdx = idx
	m.requestCommandDelete("config:deploy")
	if m.commandsDeleteKey != "config:deploy" {
		t.Fatalf("delete not armed")
	}
	m.clearCommandDeleteConfirm()
	if m.commandsDeleteKey != "" {
		t.Error("clear must disarm the pending delete")
	}
	// After disarming, a single press only re-arms: nothing is deleted.
	m.modalSelectedIdx = idx
	runWizardKeys(t, m, "d")
	if _, ok := m.cfg.Commands["deploy"]; !ok {
		t.Error("config entry deleted without confirmation")
	}
}
