package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	agentcommands "github.com/xilistudios/lele/pkg/agent/commands"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/harness"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// Part B of the /commands panel: create, edit and delete custom slash commands
// from the TUI. The write semantics mirror pkg/channels/rest_agent_commands.go
// on purpose — the REST API and this panel are two front doors to the same
// catalog, and a rule enforced by one but not the other would let the user
// store a command the backend then refuses to load. The helpers below are
// deliberately small copies (name grammar, size cap, atomic write) instead of
// exported REST internals: pkg/channels must not become a TUI dependency.

// commandNamePattern is the command-name grammar, byte-identical to
// agentCommandNamePattern in pkg/channels (lowercase start; letters, digits,
// dots, hyphens, underscores).
var commandNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

const (
	maxCommandNameLen   = 64
	maxCommandFileSize  = 256 * 1024 // same cap as the REST layer
	commandNameRequired = 0          // wizard step indices
	commandTemplateStep = 6
	commandFormSteps    = 7
)

// commandsDeleteConfirmWindow is how long the "press d again" confirmation
// stays armed after the first press (mirrors the double-confirm pattern used
// by force-send while busy).
const commandsDeleteConfirmWindow = 5 * time.Second

// commandReservedExts would double up once ".md" is appended by the writer
// ("review.md" -> the command "review.md.md"); rejected, never normalised.
var commandReservedExts = []string{".md", ".markdown"}

// validateCommandName mirrors channels.agentCommandName exactly.
func validateCommandName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", fmt.Errorf("%s", i18n.T("tui.commands.nameRequired"))
	case len(name) > maxCommandNameLen:
		return "", fmt.Errorf("%s", i18n.T("tui.commands.nameTooLong"))
	case !commandNamePattern.MatchString(name):
		return "", fmt.Errorf("%s", i18n.T("tui.commands.invalidName"))
	}
	for _, ext := range commandReservedExts {
		if strings.HasSuffix(name, ext) {
			return "", fmt.Errorf("%s", i18n.T("tui.commands.invalidName"))
		}
	}
	return name, nil
}

// commandNameIsReserved reports whether the dispatcher (or a channel) always
// answers this name first, so a custom command with it would silently never
// run. Same source of truth as the REST layer: the registry's lists, not a
// local copy.
func commandNameIsReserved(name string) bool {
	for _, r := range agentcommands.DispatcherReserved() {
		if r == name {
			return true
		}
	}
	for _, c := range agentcommands.WebUICommands() {
		if strings.ToLower(strings.TrimLeft(c.Name, "/")) == name {
			return true
		}
	}
	return false
}

// serializeCommandMarkdown produces the exact file format the WebUI editor
// writes (web/src/lib/commandMarkdown.ts): front matter only for the fields
// that are set, and no "---" block at all when nothing needs declaring.
func serializeCommandMarkdown(description, agentName, model string, allowShell bool, allowAbsFiles *bool, template string) string {
	lines := make([]string, 0, 5)
	if description != "" {
		lines = append(lines, "description: "+markdownFieldValue(description))
	}
	if agentName != "" {
		lines = append(lines, "agent: "+markdownFieldValue(agentName))
	}
	if model != "" {
		lines = append(lines, "model: "+markdownFieldValue(model))
	}
	if allowShell {
		lines = append(lines, "allow_shell: true")
	}
	if allowAbsFiles != nil {
		lines = append(lines, fmt.Sprintf("allow_absolute_files: %v", *allowAbsFiles))
	}
	if len(lines) == 0 {
		return template
	}
	return "---\n" + strings.Join(lines, "\n") + "\n---\n" + strings.TrimSpace(template)
}

// markdownFieldValue quotes a scalar that would otherwise break the simple
// "key: value" grammar the harness parser understands.
func markdownFieldValue(v string) string {
	// Newlines can never be represented (the parser reads one key: value per
	// line), so they are flattened to spaces before the quoting decision.
	v = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(v)
	if strings.Contains(v, ": ") || strings.Contains(v, `"`) || strings.Contains(v, `\\`) ||
		strings.HasPrefix(v, " ") || strings.HasSuffix(v, " ") {
		v = strings.ReplaceAll(v, `\\`, `\\\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		return `"` + v + `"`
	}
	return v
}

// writeCommandFileAtomic is the TUI twin of channels.writeAgentCommandFile:
// temp file in the same directory, then rename, so a crash can never leave a
// half-written command that the loader would reject.
func writeCommandFileAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create commands directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".command-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to write command file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeded
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write command file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write command file: %w", err)
	}
	// CreateTemp is 0600; command files are plain prompts, and 0644 keeps them
	// editable by hand from a shell (same choice as the REST writer).
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("failed to write command file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to write command file: %w", err)
	}
	return nil
}

// commandScopeDir resolves the folder one write scope targets. "config" has no
// directory (its source of truth is config.json) and returns ok=false.
func (m *Model) commandScopeDir(scope string) (string, bool) {
	switch scope {
	case string(harness.SourceWorkspace):
		ws := m.commandsWorkspace()
		if ws == "" {
			return "", false
		}
		return filepath.Join(ws, "commands"), true
	case string(harness.SourceGlobal):
		return filepath.Join(config.GetLeleDir(), "commands"), true
	default:
		return "", false
	}
}

// commandTemplateKeyMap mirrors the chat composer's KeyMap so the template
// step behaves identically: plain ENTER is captured by the modal handler
// (save), alt+enter inserts a newline. It is a copy, not a shared value, so
// the two widgets can diverge later without coupling chat to the wizard.
func commandTemplateKeyMap() textarea.KeyMap {
	return textarea.KeyMap{
		CharacterForward:        key.NewBinding(key.WithKeys("right"), key.WithHelp("right", "character forward")),
		CharacterBackward:       key.NewBinding(key.WithKeys("left"), key.WithHelp("left", "character backward")),
		WordForward:             key.NewBinding(key.WithKeys("alt+right", "alt+f"), key.WithHelp("alt+right", "word forward")),
		WordBackward:            key.NewBinding(key.WithKeys("alt+left", "alt+b"), key.WithHelp("alt+left", "word backward")),
		InsertNewline:           key.NewBinding(key.WithKeys("alt+enter"), key.WithHelp("alt+enter", "insert newline")),
		DeleteCharacterBackward: key.NewBinding(key.WithKeys("backspace"), key.WithHelp("backspace", "delete character backward")),
		DeleteCharacterForward:  key.NewBinding(key.WithKeys("delete"), key.WithHelp("delete", "delete character forward")),
		DeleteWordBackward:      key.NewBinding(key.WithKeys("alt+backspace", "ctrl+w"), key.WithHelp("alt+backspace", "delete word backward")),
		DeleteWordForward:       key.NewBinding(key.WithKeys("alt+delete", "alt+d"), key.WithHelp("alt+delete", "delete word forward")),
		DeleteAfterCursor:       key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("ctrl+k", "delete after cursor")),
		DeleteBeforeCursor:      key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "delete before cursor")),
		Paste:                   key.NewBinding(key.WithKeys("ctrl+v"), key.WithHelp("ctrl+v", "paste")),
	}
}

// newCommandTemplateInput builds the wizard's multi-line template widget. The
// styling mirrors applyThemeToInputs' chatInput treatment: foreground-only
// styles, because the stock textarea defaults emit raw ANSI backgrounds that
// paintFrame re-emits as black patches inside the modal box.
func newCommandTemplateInput() textarea.Model {
	ta := textarea.New()
	ta.CharLimit = 0
	ta.SetWidth(56)
	ta.SetHeight(6)
	ta.Prompt = " "
	ta.ShowLineNumbers = false
	ta.EndOfBufferCharacter = ' '
	ta.KeyMap = commandTemplateKeyMap()
	fg := lipgloss.NewStyle().Foreground(Foreground)
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.Text = fg
	ta.FocusedStyle.CursorLine = fg
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(CommentColor)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(CommentColor)
	ta.FocusedStyle.EndOfBuffer = lipgloss.NewStyle()
	ta.BlurredStyle = ta.FocusedStyle
	return ta
}

// --- wizard open -----------------------------------------------------------

// startCommandCreate opens the wizard for a brand-new workspace-scoped command
// (the agent-private level, same default as a REST POST without scope).
func (m *Model) startCommandCreate() {
	m.openCommandWizard("", string(harness.SourceWorkspace), [commandFormSteps]string{})
}

// startCommandEdit opens the wizard pre-filled with one definition. Directory
// level commands are refused: they belong to the checked-out project, not to
// this machine's config or workspace, and the REST layer refuses to write them
// for the same reason.
func (m *Model) startCommandEdit(key string) {
	row, mgr, ok := m.lookupCommandRow(key)
	if !ok {
		m.commandsFeedback = i18n.T("tui.commands.gone")
		// The row vanished under the cursor: drop back to the list so the
		// feedback line (only rendered there) is actually visible.
		if m.modalMode == ModalCommandDetail {
			m.commandsDetailMode = false
			m.commandsDetailKey = ""
			m.modalMode = ModalCommands
			m.loadCommandsList()
		}
		return
	}
	if row.Source == string(harness.SourceDirectory) {
		m.commandsFeedback = i18n.T("tui.commands.readonlyDirectory")
		return
	}

	var values [commandFormSteps]string
	values[0] = row.Name
	values[1] = row.Description
	values[2] = row.Agent
	values[3] = row.Model
	values[4] = boolYesNo(row.AllowShell)
	values[5] = triStateYesNo(row.AllowAbsoluteFiles)
	values[6] = m.commandLevelTemplate(row, mgr)

	m.openCommandWizard(key, row.Source, values)
}

// openCommandWizard is the shared entry: it sets the wizard state WITHOUT
// resetModal, because resetModal would wipe the list under the wizard
// (commandsRows/commandsModalKeys) and break the return path.
func (m *Model) openCommandWizard(editKey, scope string, values [commandFormSteps]string) {
	m.modalMode = ModalAddCommand
	m.formStepIndex = commandNameRequired
	m.formValues = make([]string, commandFormSteps)
	copy(m.formValues, values[:])
	m.formError = ""
	m.formConfirmMode = false
	m.commandsEditKey = editKey
	m.commandsEditScope = scope
	m.commandsDetailMode = false
	m.commandsFeedback = ""

	m.textInput.SetValue(values[0])
	m.textInput.Placeholder = i18n.T("tui.commands.namePlaceholder")
	m.textInput.Focus()

	m.templateInput.SetValue(values[6])
	m.templateInput.Blur()
	m.modalScrollOffset = 0
}

// commandLevelTemplate returns the template of THIS level's definition (not
// the merged winner), so editing a shadowed command edits its own text. The
// registry only keeps the winner, hence the file fallback for file levels.
func (m *Model) commandLevelTemplate(row commandRow, mgr *harness.Manager) string {
	if row.Source == string(harness.SourceConfig) {
		if m.cfg != nil {
			return m.cfg.Commands[row.Name].Template
		}
		return ""
	}
	if mgr != nil {
		if cmd, ok := mgr.Registry().Get(row.Name); ok && cmd != nil && cmd.Source == harness.Source(row.Source) {
			return cmd.Template
		}
	}
	if row.Path != "" {
		if raw, err := os.ReadFile(row.Path); err == nil {
			if cmd, err := harness.ParseCommandMarkdown(row.Name, row.Path, raw); err == nil && cmd != nil {
				return cmd.Template
			}
		}
	}
	return ""
}

// --- wizard step advance ---------------------------------------------------

// advanceCommandWizard validates one single-line step and moves to the next,
// or runs the save when the user finishes the template step. It is called from
// the "enter" case of handleModalKey with the trimmed textInput value.
func (m *Model) advanceCommandWizard(val string) tea.Cmd {
	switch m.formStepIndex {
	case 0: // name
		name, err := validateCommandName(val)
		if err != nil {
			m.formError = err.Error()
			return nil
		}
		// Reserved names are only fatal when the command would actually take
		// effect: creating a new name, or renaming. Editing a command under
		// its own (already existing) name must keep working.
		oldName := ""
		if i := strings.IndexByte(m.commandsEditKey, ':'); i >= 0 {
			oldName = m.commandsEditKey[i+1:]
		}
		if name != oldName && commandNameIsReserved(name) {
			m.formError = fmt.Sprintf(i18n.T("tui.commands.reserved"), name)
			return nil
		}
		val = name
	case 4: // allow_shell
		yn, ok := parseWizardYesNo(val)
		if !ok {
			if strings.TrimSpace(val) == "" {
				yn = "no" // empty = the harness default (shell off)
			} else {
				m.formError = i18n.T("tui.commands.yesNoOnly")
				return nil
			}
		}
		val = yn
	case 5: // allow_absolute_files (tri-state)
		yn, ok := parseWizardYesNo(val)
		if !ok && strings.TrimSpace(val) != "" {
			m.formError = i18n.T("tui.commands.yesNoInheritOnly")
			return nil
		}
		if strings.TrimSpace(val) == "" {
			yn = "inherit"
		}
		val = yn
	default: // description, agent, model: optional free text
	}

	m.formError = ""
	m.formValues[m.formStepIndex] = val
	m.formStepIndex++

	if m.formStepIndex == commandTemplateStep {
		// Hand over to the multi-line widget; it carries the (possibly
		// prefilled) template and gets the keystrokes from here on.
		// Focus() returns the blink command — it must reach bubbletea or
		// the cursor stays frozen (same contract as syncChatInputFocus).
		m.textInput.Blur()
		m.templateInput.SetValue(m.formValues[commandTemplateStep])
		return m.templateInput.Focus()
	}

	nextVal := ""
	if m.formStepIndex < len(m.formValues) {
		nextVal = m.formValues[m.formStepIndex]
	}
	m.textInput.SetValue(nextVal)
	m.textInput.Placeholder = m.commandStepPlaceholder(m.formStepIndex)
	m.textInput.Focus()
	return nil
}

// parseWizardYesNo accepts the yes/no spellings of every TUI language (the
// placeholders are translated, so validation must be too) and normalizes to
// the canonical "yes"/"no"/"inherit" the save flow switches on.
func parseWizardYesNo(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "y", "yes", "si", "s\u00ed", "sim":
		return "yes", true
	case "n", "no", "nao", "n\u00e3o":
		return "no", true
	case "inherit", "heredar", "heranca", "heran\u00e7a":
		return "inherit", true
	default:
		return "", false
	}
}

// commandStepPlaceholder returns the hint text of one wizard step.
func (m *Model) commandStepPlaceholder(step int) string {
	switch step {
	case 1:
		return i18n.T("tui.commands.descPlaceholder")
	case 2:
		return i18n.T("tui.commands.agentPlaceholder")
	case 3:
		return i18n.T("tui.commands.modelPlaceholder")
	case 4:
		return i18n.T("tui.commands.shellPlaceholder")
	case 5:
		return i18n.T("tui.commands.absPlaceholder")
	default:
		return ""
	}
}

// --- save ------------------------------------------------------------------

// saveCommandForm validates the whole wizard payload and persists it to the
// scope the wizard was opened for. The work is synchronous on purpose: these
// are tiny local writes (one file, or one config.json rewrite) and the skills
// flow only went async for network calls. Returns nil when validation kept the
// user inside the wizard (formError set).
func (m *Model) saveCommandForm() tea.Cmd {
	template := m.templateInput.Value()
	m.formValues[commandTemplateStep] = template

	name, err := validateCommandName(m.formValues[0])
	if err != nil {
		return m.commandFormBackToName(err.Error())
	}
	// A body that opens with the frontmatter delimiter would sit right after
	// the closing --- of the real block and confuse every future reader of
	// the file, including strict YAML parsers.
	if t := strings.TrimLeft(template, " \t\r\n"); strings.HasPrefix(t, "---") {
		m.formError = i18n.T("tui.commands.templateStartsFrontmatter")
		return nil
	}
	oldName := ""
	if i := strings.IndexByte(m.commandsEditKey, ':'); i >= 0 {
		oldName = m.commandsEditKey[i+1:]
	}
	if name != oldName && commandNameIsReserved(name) {
		return m.commandFormBackToName(fmt.Sprintf(i18n.T("tui.commands.reserved"), name))
	}
	if strings.TrimSpace(template) == "" {
		m.formError = i18n.T("tui.commands.templateRequired")
		return nil
	}
	description := strings.TrimSpace(m.formValues[1])
	// File levels must carry a description: the loader tolerates its absence,
	// but the palette and the catalog rows would render blank (same rule the
	// REST validator enforces before storing). Config commands may omit it.
	if m.commandsEditScope != string(harness.SourceConfig) && description == "" {
		m.formError = i18n.T("tui.commands.descriptionRequired")
		return nil
	}

	allowShell := m.formValues[4] == "yes"
	var allowAbs *bool
	switch m.formValues[5] {
	case "yes":
		t := true
		allowAbs = &t
	case "no":
		f := false
		allowAbs = &f
	}
	agentName := strings.TrimSpace(m.formValues[2])
	model := strings.TrimSpace(m.formValues[3])

	switch m.commandsEditScope {
	case string(harness.SourceConfig):
		return m.saveCommandConfig(name, oldName, description, agentName, model, allowShell, allowAbs, template)
	case string(harness.SourceWorkspace), string(harness.SourceGlobal):
		return m.saveCommandFile(name, oldName, description, agentName, model, allowShell, allowAbs, template)
	default:
		m.commandsFeedback = i18n.T("tui.commands.readonlyDirectory")
		m.exitCommandWizard()
		return m.tickCmd()
	}
}

// commandFormBackToName parks the user on the name step with the offending
// value still in the input, so the fix is one keystroke away.
func (m *Model) commandFormBackToName(msg string) tea.Cmd {
	m.formError = msg
	m.formStepIndex = commandNameRequired
	m.templateInput.Blur()
	m.textInput.SetValue(m.formValues[0])
	m.textInput.Placeholder = i18n.T("tui.commands.namePlaceholder")
	m.textInput.Focus()
	return nil
}

func (m *Model) saveCommandConfig(name, oldName, description, agentName, model string, allowShell bool, allowAbs *bool, template string) tea.Cmd {
	if m.cfg == nil {
		m.formError = i18n.T("tui.commands.noConfig")
		return nil
	}
	// Collision check on create/rename: any existing config command under the
	// target name (other than the one being edited) would be silently lost.
	if _, exists := m.cfg.Commands[name]; exists && name != oldName {
		m.formError = i18n.T("tui.commands.nameTaken")
		return m.commandFormBackToName(i18n.T("tui.commands.nameTaken"))
	}
	if m.cfg.Commands == nil {
		m.cfg.Commands = map[string]config.CommandDefinition{}
	}
	m.cfg.Commands[name] = config.CommandDefinition{
		Description:        description,
		Agent:              agentName,
		Model:              model,
		Template:           template,
		AllowShell:         allowShell,
		AllowAbsoluteFiles: allowAbs,
	}
	if oldName != "" && oldName != name {
		delete(m.cfg.Commands, oldName)
	}
	if err := m.saveConfigToDisk(); err != nil {
		m.formError = err.Error()
		return nil
	}
	// The config fingerprint rebuilds every manager lazily; no explicit
	// harness invalidation is needed for this scope.
	m.finishCommandWizard(name, string(harness.SourceConfig))
	return m.tickCmd()
}

func (m *Model) saveCommandFile(name, oldName, description, agentName, model string, allowShell bool, allowAbs *bool, template string) tea.Cmd {
	if m.agentLoop == nil {
		m.formError = i18n.T("tui.commands.noBackend")
		return nil
	}
	dir, ok := m.commandScopeDir(m.commandsEditScope)
	if !ok {
		m.formError = i18n.T("tui.commands.noWorkspace")
		return nil
	}
	path := filepath.Join(dir, name+".md")

	// A rename must not clobber a sibling definition at the same level.
	if m.commandsEditKey != "" && oldName != name {
		if _, err := os.Stat(path); err == nil {
			m.formError = i18n.T("tui.commands.nameTaken")
			return m.commandFormBackToName(i18n.T("tui.commands.nameTaken"))
		}
	}
	if m.commandsEditKey == "" {
		// Create: refuse to overwrite anything already at this level, file or
		// row (the row check also catches the config level shadowing case).
		if _, err := os.Stat(path); err == nil {
			m.formError = i18n.T("tui.commands.nameTaken")
			return m.commandFormBackToName(i18n.T("tui.commands.nameTaken"))
		}
		for _, row := range buildCommandRows(m.commandsManager(m.commandsWorkspace())) {
			if row.Name == name && row.Source == m.commandsEditScope {
				m.formError = i18n.T("tui.commands.nameTaken")
				return m.commandFormBackToName(i18n.T("tui.commands.nameTaken"))
			}
		}
	}

	content := serializeCommandMarkdown(description, agentName, model, allowShell, allowAbs, template)
	if len(content) > maxCommandFileSize {
		m.formError = i18n.T("tui.commands.tooLarge")
		return nil
	}
	// Validate before writing: the parser is the loader's contract, and a
	// stored file that fails it would silently disappear from the catalog.
	if _, err := harness.ParseCommandMarkdown(name, path, []byte(content)); err != nil {
		m.formError = strings.TrimPrefix(err.Error(), "harness: ")
		return nil
	}
	if err := writeCommandFileAtomic(path, content); err != nil {
		m.formError = err.Error()
		return nil
	}
	// Rename: the old file must go, or the same name lingers as a stale
	// definition at this level.
	if m.commandsEditKey != "" && oldName != name {
		if oldRow, _, ok := m.lookupCommandRow(m.commandsEditKey); ok && oldRow.Path != "" &&
			commandFileWithinScope(oldRow, m.commandsEditScope, dir) == nil {
			_ = os.Remove(oldRow.Path)
		}
	}
	m.invalidateCommandScope(m.commandsEditScope)
	m.finishCommandWizard(name, m.commandsEditScope)
	return m.tickCmd()
}

// invalidateCommandScope drops the caches that could still serve the old
// content of a level the wizard just wrote.
func (m *Model) invalidateCommandScope(scope string) {
	if m.agentLoop == nil {
		return
	}
	switch scope {
	case string(harness.SourceWorkspace):
		m.agentLoop.InvalidateHarnessWorkspace(m.commandsWorkspace())
	case string(harness.SourceGlobal):
		// The global level is merged into every agent's catalog, so every
		// cached manager is now potentially stale.
		m.agentLoop.InvalidateAllHarnessWorkspaces()
	}
}

// --- delete ----------------------------------------------------------------

// requestCommandDelete arms the double-confirm: the first "d" only shows the
// warning line, the second one within commandsDeleteConfirmWindow actually
// removes the definition. Deleting a command file is irreversible from the
// TUI, so a stray keystroke must never do it.
func (m *Model) requestCommandDelete(key string) tea.Cmd {
	if m.commandsDeleteKey == key && time.Since(m.commandsDeleteArmed) < commandsDeleteConfirmWindow {
		m.commandsDeleteKey = ""
		return m.deleteCommandRow(key)
	}
	m.commandsDeleteKey = key
	m.commandsDeleteArmed = time.Now()
	m.commandsFeedback = i18n.T("tui.commands.confirmDelete")
	return m.tickCmd()
}

// clearCommandDeleteConfirm disarms a pending delete. Called from every path
// that moves the cursor or changes the view so an armed delete can never
// follow its user to a different row.
func (m *Model) clearCommandDeleteConfirm() {
	m.commandsDeleteKey = ""
	m.commandsDeleteArmed = time.Time{}
}

// commandFileWithinScope is the defense-in-depth gate in front of every
// os.Remove of a command file: the path the loader discovered must be a
// direct markdown child of the scope's own commands directory. Symlinks,
// future loader changes or corrupt rows can then never aim a delete at an
// arbitrary file.
func commandFileWithinScope(row commandRow, scope string, dir string) error {
	if row.Path == "" {
		return fmt.Errorf("%s", i18n.T("tui.commands.noPath"))
	}
	if dir == "" {
		return fmt.Errorf("%s", i18n.T("tui.commands.noWorkspace"))
	}
	expected := filepath.Join(dir, filepath.Base(row.Path))
	if filepath.Clean(row.Path) != filepath.Clean(expected) {
		return fmt.Errorf("%s", i18n.T("tui.commands.pathOutsideScope"))
	}
	if ext := strings.ToLower(filepath.Ext(row.Path)); ext != ".md" && ext != ".markdown" {
		return fmt.Errorf("%s", i18n.T("tui.commands.pathOutsideScope"))
	}
	return nil
}

// deleteCommandRow removes one definition. The composite key identifies the
// exact level, so deleting a shadowed workspace file never touches the config
// entry that wins over it.
func (m *Model) deleteCommandRow(key string) tea.Cmd {
	row, _, ok := m.lookupCommandRow(key)
	if !ok {
		m.commandsFeedback = i18n.T("tui.commands.gone")
		return m.tickCmd()
	}
	if row.Source == string(harness.SourceDirectory) {
		m.commandsFeedback = i18n.T("tui.commands.readonlyDirectory")
		return m.tickCmd()
	}

	switch row.Source {
	case string(harness.SourceConfig):
		if m.cfg == nil {
			m.commandsFeedback = i18n.T("tui.commands.noConfig")
			return m.tickCmd()
		}
		delete(m.cfg.Commands, row.Name)
		if err := m.saveConfigToDisk(); err != nil {
			m.commandsFeedback = err.Error()
			return m.tickCmd()
		}
	default: // workspace / global
		if row.Path == "" {
			m.commandsFeedback = i18n.T("tui.commands.noPath")
			return m.tickCmd()
		}
		dir, ok := m.commandScopeDir(row.Source)
		if !ok {
			m.commandsFeedback = i18n.T("tui.commands.noWorkspace")
			return m.tickCmd()
		}
		if err := commandFileWithinScope(row, row.Source, dir); err != nil {
			m.commandsFeedback = err.Error()
			return m.tickCmd()
		}
		if err := os.Remove(row.Path); err != nil {
			m.commandsFeedback = err.Error()
			return m.tickCmd()
		}
		m.invalidateCommandScope(row.Source)
	}

	m.commandsFeedback = fmt.Sprintf(i18n.T("tui.commands.deleted"), row.Name)
	m.commandsDetailMode = false
	m.commandsDetailKey = ""
	m.modalMode = ModalCommands
	m.loadCommandsList()
	return m.tickCmd()
}

// --- exit paths ------------------------------------------------------------

// finishCommandWizard closes the wizard back to the list, reloads the catalog
// and puts the cursor on the row that was just written.
func (m *Model) finishCommandWizard(name, scope string) {
	m.commandsFeedback = fmt.Sprintf(i18n.T("tui.commands.saved"), name)
	m.exitCommandWizard()
	m.loadCommandsList()
	m.reselectCommandRow(scope + ":" + name)
}

// exitCommandWizard resets the wizard-only state but keeps the list intact.
func (m *Model) exitCommandWizard() {
	m.modalMode = ModalCommands
	m.formValues = nil
	m.formError = ""
	m.commandsEditKey = ""
	m.commandsEditScope = ""
	m.textInput.SetValue("")
	m.textInput.Placeholder = ""
	m.templateInput.Blur()
	m.templateInput.SetValue("")
	m.clearCommandDeleteConfirm()
}

// boolYesNo / triStateYesNo are the wizard-side inverses of the detail view's
// boolDisplay/triStateDisplay: they seed form steps 4 and 5.
func boolYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func triStateYesNo(v *bool) string {
	if v == nil {
		return "inherit"
	}
	return boolYesNo(*v)
}

// --- template step rendering -----------------------------------------------

// renderCommandTemplateStep renders the wizard's multi-line step with the same
// chrome as the single-line steps (step list, error line) but a textarea
// instead of the text input, so the user can write a real prompt body.
func (m *Model) renderCommandTemplateStep(title string) string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(title) + "\n\n")
	sb.WriteString(m.renderFormSteps(m.formStepNames(), false))
	sb.WriteString("\n")
	if m.formError != "" {
		sb.WriteString(lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n")
	}
	sb.WriteString("\n")

	// The textarea must fit the modal: bubbles' default height plus the
	// chrome above it stays inside the tallest modal the layout allows.
	m.templateInput.SetWidth(56)
	sb.WriteString(m.templateInput.View() + "\n\n")
	sb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.commands.templateHints")) + "\n")

	return m.paintFrame(ModalContainer.Render(sb.String()))
}
