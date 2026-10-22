package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// ── Message type for the async MCP add result ─────────────────────────

// mcpAddResultMsg is the result of one MCP entry creation through the
// /mcp wizard (mcpAddCmdFor). It is a separate message from
// mcpToggleResultMsg on purpose (plan D11): the add result carries a
// layer, an entry-level outcome (created/replaced/changed) and a shadow
// note, none of which belong to a toggle. The result handler mirrors
// handleMCPToggleResult's tail (feedback line, list reload, tick) but is
// deliberately forbidden from touching wizard state — a second wizard may
// already be on screen when an earlier write lands.
type mcpAddResultMsg struct {
	name     string
	layer    string // canonical layer that was targeted
	path     string // the layer file that was targeted ("" when unresolvable)
	created  bool   // file/envelope/entry created
	replaced bool   // an existing entry was overwritten
	changed  bool   // bytes were written
	shadow   string // higher layer whose copy shadows the new entry ("" = none)
	err      error
}

// ── mcpAddCmd / mcpAddCmdFor: the async write path ────────────────────

// mcpAddCmd resolves the paths NOW (cheap, no I/O) and captures them by
// value into the closure — the documented race rule (mcp_auth.go): the
// command goroutine runs concurrently with Update, so it must never read a
// Model field. Unresolvable paths yield a refusal message instead of
// touching the disk.
func (m *Model) mcpAddCmd(layer, name string, entry mcp.ServerConfig) tea.Cmd {
	paths, ok := m.mcpPaths()
	if !ok {
		return func() tea.Msg {
			return mcpAddResultMsg{name: name, layer: layer, err: errMCPUnavailable}
		}
	}
	return mcpAddCmdFor(paths, layer, name, entry)
}

// mcpAddCmdFor writes name into the given layer's mcp.json, refusing (with
// no write) when the layer resolves to no file (empty root ⇒ layer
// disabled), the target file is not an mcp.json, or it sits outside the
// allowed workspace roots (the REST guard, 403 mcp_path_not_allowed). All
// I/O happens inside the returned tea.Cmd. Before the write it reads the
// inventory once (read-only, no guard needed) to detect whether a HIGHER
// layer already defines `name` — the new entry will be shadowed there, and
// the feedback line says so (the TUI equivalent of the CLI's
// `⊘ note: … shadows this entry …`).
func mcpAddCmdFor(paths mcp.Paths, layer, name string, entry mcp.ServerConfig) tea.Cmd {
	return func() tea.Msg {
		file := mcp.LayerFile(paths, layer)
		if file == "" {
			return mcpAddResultMsg{name: name, layer: layer, err: errMCPOwnerNoPath}
		}
		// Belt-and-braces: LayerFile only ever yields <root>/mcp.json (or
		// ""), but the CLI guard checks both halves of the rule, so this
		// cmd does too — a future LayerFile change must not silently widen
		// what this path accepts.
		if filepath.Base(file) != "mcp.json" {
			return mcpAddResultMsg{name: name, layer: layer, path: file, err: errMCPPathNotAllowed}
		}
		// Same guard REST applies BEFORE writing (rest_mcp.go
		// mcpGuardLayerFile → 403 mcp_path_not_allowed): the directory of
		// the target file must sit in an allowed workspace root. One
		// predicate, exported from pkg/channels — never a copy of the rule.
		if !channels.IsAllowedWorkspacePath(filepath.Dir(file)) {
			return mcpAddResultMsg{name: name, layer: layer, path: file, err: errMCPPathNotAllowed}
		}
		// Read-only pre-write inventory: report a higher-layer shadow in the
		// same style as the CLI, so the user learns why the entry may not
		// take effect even though the write succeeded.
		inv := mcp.ReadInventory(paths)
		shadow := mcpAddShadowLayer(inv, layer, name)
		res, err := mcp.UpsertEntry(file, name, entry)
		if err != nil {
			return mcpAddResultMsg{name: name, layer: layer, path: file, shadow: shadow, err: err}
		}
		return mcpAddResultMsg{
			name: name, layer: layer, path: file,
			created: res.Created, replaced: res.Replaced, changed: res.Changed,
			shadow: shadow,
		}
	}
}

// ── Layer ranking: the merge precedence of the three layers ───────────

// mcpAddLayerRank is the layer's index in the merge order mcp.Layers()
// declares (low → high). -1 for an unknown layer. Implemented THROUGH
// mcp.Layers() so it cannot drift from the merge precedence. (The comment
// in mcp_add_form.go points here: the helper lives next to its write-path
// consumer, mcpAddShadowLayer.)
func mcpAddLayerRank(layer string) int {
	for i, l := range mcp.Layers() {
		if l == layer {
			return i
		}
	}
	return -1
}

// ── Shadow detection: the highest layer ABOVE the target ───────────────

// mcpAddShadowLayer returns the highest-ranked layer ABOVE layer that
// already defines name ("" = none). Read-only — callers pass the inventory
// they already hold or one fresh from mcp.ReadInventory(paths). Ranking goes
// through mcpAddLayerRank (same file, built on mcp.Layers() so it cannot
// drift from the merge precedence).
func mcpAddShadowLayer(inv mcp.Inventory, layer, name string) string {
	rank := mcpAddLayerRank(layer)
	best, bestRank := "", -1
	for _, st := range inv.ByName[name] {
		r := mcpAddLayerRank(st.Layer)
		if r <= rank || r <= bestRank {
			continue
		}
		best, bestRank = st.Layer, r
	}
	return best
}

// ── handleMCPAddResult processes an add result ────────────────────────

// mcpAddErrorKind reduces an arbitrary write-path error to the part the
// feedback line may show (review m4). Every pkg/mcp writer error is shaped
// `mcp: <verb> <path>[: context]` — the PATH is fixed, but the trailing
// context may one day embed file content or an env value, so only the verb
// travels to the screen. Sanitized on the way out (control/bidi chars can
// never reach the frame), and a shaped error that matches nothing degrades
// to its first word so the line stays honest instead of printing %v raw.
func mcpAddErrorKind(err error) string {
	if err == nil {
		return ""
	}
	msg := sanitizeDisplayText(err.Error())
	switch {
	case !strings.HasPrefix(msg, "mcp: "):
		// Not a writer-shaped error: show only the first token (the verb
		// of "permission denied: …") rather than the whole string.
		kind, _, _ := strings.Cut(msg, " ")
		return kind
	default:
		rest := strings.TrimPrefix(msg, "mcp: ")
		verb, _, _ := strings.Cut(rest, " ")
		return verb
	}
}

// handleMCPAddResult writes the feedback line and, on success (and on a
// byte-identical no-op alike, like the toggle), calls the agent loop's
// SyncMCPServers — mcp.json is not config-watched, so that pass is what
// makes the write visible to the tool/prompt surface — then reloads the
// list and puts the cursor on the new row. When the active source filter
// would hide the row (the write landed in another layer), the filter resets
// to "all" first, so the reselect is always achievable.
//
// It NEVER touches wizard state (formValues/formStepIndex/textInput): by
// the time this result lands in Update, the wizard that fired the write has
// already closed optimistically (plan D12) and a SECOND wizard may already
// be on screen — clobbering its slots would corrupt an in-flight entry.
func (m *Model) handleMCPAddResult(msg mcpAddResultMsg) tea.Cmd {
	switch {
	case msg.err == nil:
		switch {
		case msg.created:
			m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpAddSuccess"), msg.name, msg.layer, msg.path)
		case msg.replaced:
			m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpAddReplaced"), msg.name, msg.layer, msg.path)
		default:
			m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpAddNoChange"), msg.name, msg.layer)
		}
		if msg.shadow != "" {
			m.mcpFeedback += " " + fmt.Sprintf(i18n.T("tui.mcpAddShadowSuffix"), msg.shadow)
		}
		// Idempotent pass; fires for a no-op result too — the loop can only
		// end up matching the disk it already matched.
		syncMCPServersFor(m.agentLoop)
		// Reset the filter when it would hide the row the user just created
		// (the write may have targeted a different layer than the filter).
		if want := mcpFilterLayer(m.mcpFilter); want != "" && want != msg.layer {
			m.mcpFilter = mcpFilterAll
		}
	case errors.Is(msg.err, errMCPUnavailable):
		m.mcpFeedback = i18n.T("tui.mcpUnavailable")
	case errors.Is(msg.err, errMCPOwnerNoPath):
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpAddLayerNoPath"), msg.layer)
	case errors.Is(msg.err, errMCPPathNotAllowed):
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpAddPathNotAllowed"), msg.name, msg.path)
	default:
		// m4: a non-sentinel error is an arbitrary string that could carry
		// file content (secrecy invariant DoD 8 — never echo a value). Two
		// guards: print only the error KIND (what the user can act on), and
		// sanitize what remains so control/bidi chars cannot corrupt the
		// feedback line (the same pass every feedback line gets).
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpAddFailed"), msg.name, mcpAddErrorKind(msg.err))
	}
	m.loadMCPList()
	if msg.err == nil {
		m.reselectMCPRow(msg.name)
	}
	return m.tickCmd()
}

// reselectMCPRow restores the cursor to the row with the given key after
// the list has been reloaded (the add flow's post-write tail). A missing
// key (or a "" empty-state row) leaves the cursor where the reload clamped
// it — mirroring reselectCommandRow's "no match ⇒ cursor as-is" contract.
func (m *Model) reselectMCPRow(name string) {
	if name == "" {
		return
	}
	for i, k := range m.mcpModalKeys {
		if k == name {
			m.modalSelectedIdx = i
			m.clampModalCursor()
			return
		}
	}
	m.clampModalCursor()
}

// ── The wizard state machine (start/open/advance/exit) ────────────────
//
// No new Model fields: the wizard lives entirely in the generic form state
// (formStepIndex stores the SLOT constant, formValues is indexed BY SLOT,
// formError) plus the shared textinput — exactly like the command wizard
// (openCommandWizard) and the add-secret form (startAddSecret).

// startMCPAdd is the "n" entry point of the /mcp list. It resolves the MCP
// paths FIRST: when the loop cannot serve them the wizard would be a dead
// end (its write path answers "MCP unavailable"), so the key must say so on
// the feedback line instead of opening a modal that cannot finish. On
// success it returns the focus blink command of the shared text input.
func (m *Model) startMCPAdd() tea.Cmd {
	if _, ok := m.mcpPaths(); !ok {
		m.mcpFeedback = i18n.T("tui.mcpUnavailable")
		return nil
	}
	return m.openMCPAddWizard()
}

// openMCPAddWizard arms the wizard WITHOUT resetModal — resetModal would
// wipe the list (mcpModalKeys/modalItems) the wizard must return to, the
// openCommandWizard rule (commands_admin_write.go:274-296). Returns the
// textinput.Focus() blink cmd so the cursor blinks on the first frame.
func (m *Model) openMCPAddWizard() tea.Cmd {
	m.modalMode = ModalMCPAdd
	m.formStepIndex = mcpAddStepName
	m.formValues = make([]string, mcpAddStepCount)
	m.formError = ""
	m.formConfirmMode = false
	m.mcpFeedback = ""
	m.modalScrollOffset = 0

	m.textInput.SetValue("")
	m.textInput.Placeholder = m.mcpAddStepPlaceholder(mcpAddStepName)
	// Audit M2: sync echo before the first frame renders the widget (the
	// forwarding block only runs on the next keystroke).
	m.syncTextInputEcho()
	return m.textInput.Focus()
}

// mcpAddNextSlot returns the slot AFTER pos in the current kind's visible
// order, or -1 when pos is the last visible slot (review).
func mcpAddNextSlot(kind string, pos int) int {
	slots := mcpAddVisibleSlots(kind)
	if pos < 0 || pos+1 >= len(slots) {
		return -1
	}
	return slots[pos+1]
}

// mcpAddPosition resolves the POSITION (index into the visible-slot set) of
// the cursor slot. Returns -1 when the slot is not visible (a stale slot
// left over from a previous flow) — the callers fall back to the first
// position in that case.
func mcpAddPosition(kind string, slot int) int {
	slots := mcpAddVisibleSlots(kind)
	for i, s := range slots {
		if s == slot {
			return i
		}
	}
	return -1
}

// advanceMCPAdd validates the typed value of the CURRENT slot with that
// slot's parser, stores the canonical value, moves the cursor to the next
// VISIBLE slot and re-arms the input (value + placeholder + focus). On the
// review slot it drives the confirm gate: "yes" builds the entry and fires
// the async write, "no" cancels, anything else is refused with the localized
// mcpAddErrConfirm. Every error keeps the user on the offending slot with
// the raw text still in the input.
func (m *Model) advanceMCPAdd(val string) tea.Cmd {
	kind := m.mcpAddTransport()

	// The cursor is a SLOT, but a stale formStepIndex from a previous flow
	// may not be in this kind's visible set — fall back to the first slot
	// instead of silently walking the wrong set.
	pos := mcpAddPosition(kind, m.formStepIndex)
	if pos < 0 {
		m.formStepIndex = mcpAddVisibleSlots(kind)[0]
		m.textInput.SetValue(m.formValue(m.formStepIndex))
		m.textInput.Placeholder = m.mcpAddStepPlaceholder(m.formStepIndex)
		return nil
	}
	slot := m.formStepIndex

	// The review slot is the confirm gate — it has no stored value.
	if slot == mcpAddStepReview {
		switch yn, ok := parseWizardYesNo(val); {
		case !ok || yn == "":
			m.formError = i18n.T("tui.mcpAddErrConfirm")
			return nil
		case yn == "no":
			m.exitMCPAddWizard()
			return nil
		default: // "yes"
			entry, err := m.mcpAddEntry()
			if err != nil {
				// The whole-entry build failed (a pair parser, say): park
				// the user on the offending slot with the raw text still
				// in the input (mcpAddBackToName mirrors this for the
				// command wizard's name step).
				return m.mcpAddBackToSlot(err.Error())
			}
			layer := m.formValue(mcpAddStepLayer)
			name := strings.TrimSpace(m.formValue(mcpAddStepName))
			m.exitMCPAddWizard()
			return m.mcpAddCmd(layer, name, entry)
		}
	}

	// Per-slot validation.
	switch slot {
	case mcpAddStepName:
		name, err := mcpAddValidateName(val)
		if err != nil {
			m.formError = err.Error()
			return nil
		}
		val = name
	case mcpAddStepKind:
		kindVal, err := mcpAddParseTransport(val)
		if err != nil {
			m.formError = err.Error()
			return nil
		}
		val = kindVal
	case mcpAddStepEndpoint:
		endpoint, err := mcpAddParseEndpoint(m.mcpAddTransport(), val)
		if err != nil {
			m.formError = err.Error()
			return nil
		}
		val = endpoint
	case mcpAddStepType:
		typ, err := mcpAddParseType(val)
		if err != nil {
			m.formError = err.Error()
			return nil
		}
		val = typ
	case mcpAddStepEnv, mcpAddStepHeaders:
		// The pair parse is the VALIDATION (its error never carries the
		// value); the RAW text is what gets stored, because the display
		// masks it (mcpMaskPairs) and mcpAddEntry re-parses the stored
		// text to build the map. Storing "" here silently dropped every
		// env/header value the user typed (review B2).
		if _, err := mcpAddParsePairs(val); err != nil {
			m.formError = err.Error()
			return nil
		}
	case mcpAddStepLayer:
		layer, err := mcpAddParseLayer(val)
		if err != nil {
			m.formError = err.Error()
			return nil
		}
		// Early path/root guard (D10): learn before the review, not after
		// the write. The same predicates the write cmd applies — one rule,
		// never a copy (channels.IsAllowedWorkspacePath).
		paths, ok := m.mcpPaths()
		if !ok {
			m.formError = i18n.T("tui.mcpUnavailable")
			return nil
		}
		if file := mcp.LayerFile(paths, layer); file == "" {
			m.formError = fmt.Sprintf(i18n.T("tui.mcpAddErrLayerNoPath"), layer)
			return nil
		} else if !channels.IsAllowedWorkspacePath(filepath.Dir(file)) {
			m.formError = fmt.Sprintf(i18n.T("tui.mcpAddErrPathNotAllowed"), layer, file)
			return nil
		}
		val = layer
	}

	m.formError = ""
	if slot < len(m.formValues) {
		m.formValues[slot] = val
	}

	// Move to the next visible slot. mcpAddNextSlot returns a SLOT CONSTANT
	// (slots[pos+1]), NOT a position — indexing the visible set with it
	// double-counted (review B1: skipped env/layer, then panicked with
	// "index out of range"). And the kind must be re-read HERE: on the
	// transport step itself the value was stored by this very ENTER, so the
	// kind captured at function entry was still the OLD one and the walk
	// stalled (review B3: a double ENTER was needed to leave the step).
	nextKind := m.mcpAddTransport()
	nextSlot := mcpAddNextSlot(nextKind, mcpAddPosition(nextKind, slot))
	if nextSlot < 0 {
		// Already on the last visible slot (review) — nothing to advance
		// to; the confirm gate above owns this transition.
		return nil
	}
	m.formStepIndex = nextSlot
	m.textInput.SetValue(m.formValue(m.formStepIndex))
	m.textInput.Placeholder = m.mcpAddStepPlaceholder(m.formStepIndex)
	return m.textInput.Focus()
}

// mcpAddBackToSlot parks the user on the FIRST visible slot (name) with the
// offending value still in the input, mirroring commandFormBackToName
// (commands_admin_write.go:499-507). The whole-entry build at the review
// step can fail on a slot the user already passed (a pair parser, say);
// there is no per-slot back navigation in v1, so the recovery point is the
// first slot — the user re-walks the flow with the error in view.
func (m *Model) mcpAddBackToSlot(msg string) tea.Cmd {
	m.formError = msg
	m.formStepIndex = mcpAddStepName
	m.textInput.SetValue(m.formValue(mcpAddStepName))
	m.textInput.Placeholder = m.mcpAddStepPlaceholder(mcpAddStepName)
	return m.textInput.Focus()
}

// exitMCPAddWizard tears the wizard down and returns to the /mcp list. It
// never touches mcpModalKeys/modalItems/modalSelectedIdx — the list is
// still there, exactly as the user left it.
func (m *Model) exitMCPAddWizard() {
	m.modalMode = ModalMCP
	m.formValues = nil
	m.formStepIndex = 0
	m.formError = ""
	m.formConfirmMode = false
	m.textInput.SetValue("")
	m.textInput.Placeholder = ""
	// Audit M2: the next frame must not render a stale password echo from a
	// secret step (there is none here, but the echo mode is a shared widget
	// field and the next modal may expect the normal mode).
	m.syncTextInputEcho()
}

// handleMCPAddKey owns the wizard's keys while ModalMCPAdd is open. ENTER
// advances the current slot (or confirms at the review step) and ESC cancels
// back to the list. Every other key — "q", "n", up/down included — is
// forwarded (handled=false) to the generic modal switch, where the
// isFormModal(ModalMCPAdd) registration types it into the shared text input
// instead of treating it as a modal shortcut or list navigation.
func (m *Model) handleMCPAddKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "enter":
		return m.advanceMCPAdd(strings.TrimSpace(m.textInput.Value())), true
	case "esc":
		m.exitMCPAddWizard()
		return nil, true
	}
	return nil, false
}
