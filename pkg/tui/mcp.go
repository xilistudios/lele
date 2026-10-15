package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// ── Message types for async MCP operations ─────────────────────────────

// mcpToggleResultMsg is the result of one MCP state mutation: the enable
// toggle (mcpToggleCmdFor) or the sign-out (mcpAuthLogoutCmdFor). The two
// share the message because it is the ONLY MCP mutation result wired into
// update()'s dispatch, and both end with the same tail (feedback line, list
// reload, tick) instead of each growing its own routing path. `logout` tells
// them apart; enabled/changed/path are toggle-only.
type mcpToggleResultMsg struct {
	name    string
	enabled bool   // state AFTER the toggle
	changed bool   // bytes on disk changed
	path    string // toggle target (only set when the target is refused)
	err     error
	// logout marks a result produced by the sign-out action: only name and
	// err are meaningful then (a sign-out writes no layer file, so it has no
	// enabled/changed/path to report).
	logout bool
}

// Sentinel errors carried by mcpToggleResultMsg. The result handler maps
// them to localized feedback, so no i18n lookup happens inside the
// tea.Cmd goroutine.
var (
	errMCPUnavailable = errors.New("mcp unavailable")
	errMCPOwnerNoPath = errors.New("owning layer has no file")
	// errMCPInvalid: the winner's verdict is "invalid" — toggling is
	// refused with no write and the user is pointed at the file (the same
	// rule the WebUI applies: invalid rows are not toggleable anywhere).
	errMCPInvalid = errors.New("mcp entry is invalid")
	// errMCPPathNotAllowed: the toggle target is outside the allowed
	// workspace roots — the TUI face of REST's 403 mcp_path_not_allowed.
	errMCPPathNotAllowed = errors.New("mcp path not allowed")
)

// ── Source filter (/mcp "f" key): all → global → agent → project ──────

const (
	mcpFilterAll int = iota
	mcpFilterGlobal
	mcpFilterAgent
	mcpFilterProject
	mcpFilterCount // cycle modulus for the "f" key
)

// mcpFilterLayer returns the winner layer a filter selects ("" = all).
func mcpFilterLayer(filter int) string {
	switch filter {
	case mcpFilterGlobal:
		return mcp.LayerGlobal
	case mcpFilterAgent:
		return mcp.LayerAgent
	case mcpFilterProject:
		return mcp.LayerProject
	default:
		return ""
	}
}

// mcpFilterLabel is the localized name of the active filter.
func (m *Model) mcpFilterLabel() string {
	switch m.mcpFilter {
	case mcpFilterGlobal:
		return i18n.T("tui.mcpFilterGlobal")
	case mcpFilterAgent:
		return i18n.T("tui.mcpFilterAgent")
	case mcpFilterProject:
		return i18n.T("tui.mcpFilterProject")
	default:
		return i18n.T("tui.mcpFilterAll")
	}
}

// mcpFilterHint is the "Filter: <label>" line rendered under the list.
func (m *Model) mcpFilterHint() string {
	return fmt.Sprintf(i18n.T("tui.mcpFilter"), m.mcpFilterLabel())
}

// ── Paths / agent resolution ───────────────────────────────────────────

// mcpPaths resolves the mcp.json roots for the current session's agent,
// mirroring how view.go derives the agent ID: the session's agent, falling
// back to "main". ok=false means the loop cannot resolve them (no loop,
// bare loop, agent not live) — the zero Paths must not be used.
func (m *Model) mcpPaths() (mcp.Paths, bool) {
	if m.agentLoop == nil || m.agentLoop.MessageBus() == nil {
		// The bus is set by every real constructor; a bare &AgentLoop{}
		// has none — and its providable is a typed-nil behind a non-nil
		// interface, so calling into it would panic instead of reporting
		// the unavailability MCPPathsFor would report below.
		return mcp.Paths{}, false
	}
	agentID := "main"
	if prov := m.agentLoop.GetProvidable(); prov != nil {
		if id := prov.GetSessionAgent(m.currentKey); id != "" {
			agentID = id
		}
	}
	return m.agentLoop.MCPPathsFor(agentID)
}

// ── loadMCPList refreshes the MCP server list in the modal ─────────────

// loadMCPList resolves the paths and rebuilds the listing synchronously
// (like loadSkillsList). Unresolvable paths yield the "MCP unavailable"
// row instead of a panic.
func (m *Model) loadMCPList() {
	paths, ok := m.mcpPaths()
	if !ok {
		m.modalItems = []string{i18n.T("tui.mcpUnavailable")}
		m.mcpModalKeys = []string{""}
		// No list was built from an inventory: drop any previous snapshot so
		// an enter here (or a stale detail) can never render outdated data.
		m.mcpInventory = mcp.Inventory{}
		m.mcpInventoryValid = false
		m.mcpAuth = nil
		m.clampModalCursor()
		return
	}
	m.loadMCPListWith(paths)
}

// loadMCPListWith builds the filtered, name-sorted listing for explicit
// paths. m.mcpModalKeys stays 1:1 with m.modalItems ("" for the
// empty-state row) so index-based actions can never desync. The inventory
// the rows are built from is kept on the Model (m.mcpInventory) so the
// detail screen describes exactly what this list showed without re-reading
// the disk — a fresh read could show a different winner than the selected
// row if a file changed in between. m.mcpAuth (the per-name auth verdict)
// is resolved in the same pass and for the same reason.
func (m *Model) loadMCPListWith(paths mcp.Paths) {
	m.modalItems = nil
	m.mcpModalKeys = nil

	inv := mcp.ReadInventory(paths)
	m.mcpInventory = inv
	m.mcpInventoryValid = true
	// The auth verdict is resolved WITH the list, from the same three layers
	// Discover reads, so the row marker, the detail block and the sign-out
	// action all describe exactly the rows this list shows.
	m.mcpAuth = mcpAuthVerdicts(paths, inv)
	filterLayer := mcpFilterLayer(m.mcpFilter)
	names := make([]string, 0, len(inv.Winner))
	for name := range inv.Winner {
		if filterLayer != "" && inv.Winner[name] != filterLayer {
			continue
		}
		names = append(names, name)
	}

	if len(names) == 0 {
		// Empty inventory (or nothing under the active filter).
		m.modalItems = append(m.modalItems, i18n.T("tui.mcpNoServers"))
		m.mcpModalKeys = append(m.mcpModalKeys, "")
		m.clampModalCursor()
		return
	}

	// Sort by name for stable display (case-insensitive, like skills).
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	for _, name := range names {
		m.modalItems = append(m.modalItems, formatMCPItem(
			name, inv.Winner[name], inv.Effective[name], mcpAuthSuffix(m.mcpAuth[name])))
		m.mcpModalKeys = append(m.mcpModalKeys, name)
	}
	m.clampModalCursor()
}

// ── handleMCPKey owns the keys of the /mcp list and detail modals ──────
//
// List: space/t toggle the selected server, d signs it out, f cycles the
// source filter, enter opens ModalMCPDetail for the row under the cursor,
// esc/q close. Detail: esc/q return to the list (the cursor and the rows are
// untouched), "a" starts the interactive OAuth flow of the server being shown
// when it is a remote entry (ModalMCPAuth), "d" signs it out and every other
// key is swallowed so no list action or navigation can fire while the detail
// is on screen. Auth: ENTER/CTRL+Y/ESC are owned by handleMCPAuthKey
// (ModalMCPAuth). Navigation (up/down) and the remaining keys of the list fall
// through to the generic modal switch. Returns handled=false so the caller
// forwards un-owned keys.
func (m *Model) handleMCPKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.modalMode == ModalMCPDetail {
		// The detail is read-only for the ENTRY — no toggle from here by
		// design: it would have to re-derive the owning layer against a
		// possibly changed disk. A SIGN-OUT is allowed here because it does
		// not touch any layer file: it deletes a credential keyed by NAME, the
		// same in-process Logout the CLI runs. esc/q go back to the list;
		// m.mcpModalKeys/modalSelectedIdx are left untouched, so the cursor is
		// preserved. "a" delegates to the authorization entry point (remote
		// entries only — a stdio entry gets an inline hint and no flow), "d"
		// to the sign-out, and the detail stays on screen afterwards so the
		// user sees the refreshed auth state. Any other key is swallowed — it
		// must not reach the generic list switch, where it could move the
		// hidden cursor or act on the row behind the detail.
		if s := msg.String(); s == "esc" || s == "q" {
			m.modalMode = ModalMCP
		} else if s == "a" {
			return m.mcpAuthStartFromDetail(), true
		} else if s == "d" {
			return m.mcpAuthLogoutCmd(m.mcpDetailName), true
		}
		return nil, true
	}
	if m.modalMode == ModalMCPAuth {
		// ModalMCPAuth owns ENTER/CTRL+Y/ESC and passes every other key —
		// "q" included, it is a regular character in the paste box — through,
		// so a pasted code is typed into the shared text input (see
		// isFormModal / isListModal in modal_state.go).
		return m.handleMCPAuthKey(msg)
	}
	if m.modalMode != ModalMCP {
		return nil, false
	}
	switch msg.String() {
	case " ", "t":
		if m.modalSelectedIdx < 0 || m.modalSelectedIdx >= len(m.mcpModalKeys) {
			return nil, true
		}
		name := m.mcpModalKeys[m.modalSelectedIdx]
		if name == "" {
			return nil, true // empty-state row
		}
		return m.mcpToggleCmd(name), true
	case "d":
		// Sign out of the row under the cursor. The empty-state and "MCP
		// unavailable" rows carry the "" key and are a silent no-op (nothing
		// to sign out of); a REAL row without a stored credential is not
		// silent — the result line says so (mcpAuthLogoutCmdFor).
		if m.modalSelectedIdx < 0 || m.modalSelectedIdx >= len(m.mcpModalKeys) {
			return nil, true
		}
		name := m.mcpModalKeys[m.modalSelectedIdx]
		if name == "" {
			return nil, true
		}
		return m.mcpAuthLogoutCmd(name), true
	case "f":
		m.mcpFilter = (m.mcpFilter + 1) % mcpFilterCount
		m.loadMCPList()
		return nil, true
	case "enter":
		// Open the detail of the row under the cursor. The empty-state and
		// "MCP unavailable" rows carry the "" key and stay a no-op. A real
		// row without a stored inventory cannot happen (rows and inventory
		// are written together by loadMCPListWith/loadMCPList) and must not
		// open either: the detail would have nothing faithful to render.
		if m.modalSelectedIdx >= 0 && m.modalSelectedIdx < len(m.mcpModalKeys) {
			if name := m.mcpModalKeys[m.modalSelectedIdx]; name != "" && m.mcpInventoryValid {
				m.mcpDetailName = name
				m.modalMode = ModalMCPDetail
			}
		}
		return nil, true
	case "esc", "q":
		m.modalMode = ModalNone
		return nil, true
	}
	return nil, false
}

// syncMCPServersFor triggers the agent loop's MCP re-wiring pass. mcp.json is
// not config-watched, so this pass is what makes a write (a toggle, a stored
// OAuth credential) visible to the tool/prompt surface. It is a package-level
// var — the obVerifyHTTPClient precedent — so tests can count the calls with a
// bare Model, and it is nil-safe on a bare &AgentLoop{} (the loop's own
// contract), which is why it is the single seam every MCP surface calls
// instead of each one re-checking the loop itself.
var syncMCPServersFor = func(al *agent.AgentLoop) {
	if al != nil {
		al.SyncMCPServers()
	}
}

// ── mcpToggleCmd returns the command that flips one server ─────────────

// mcpToggleCmd resolves the paths NOW (cheap, no I/O); the inventory read
// and the write run inside the returned command — the only blocking I/O of
// the flow.
func (m *Model) mcpToggleCmd(name string) tea.Cmd {
	paths, ok := m.mcpPaths()
	if !ok {
		return func() tea.Msg {
			return mcpToggleResultMsg{name: name, err: errMCPUnavailable}
		}
	}
	return mcpToggleCmdFor(paths, name)
}

// mcpToggleCmdFor toggles name in the layer that OWNS the winning entry
// (inv.Winner[name]) — never a shadowed copy — and refuses with NO write
// when (in this order) that layer resolves to no file (empty root ⇒ layer
// disabled), the target file sits outside the allowed workspace roots (the
// REST guard, 403 mcp_path_not_allowed), or the winner's verdict is
// "invalid" (toggling an invalid row is refused everywhere — WebUI too).
func mcpToggleCmdFor(paths mcp.Paths, name string) tea.Cmd {
	return func() tea.Msg {
		inv := mcp.ReadInventory(paths)
		path := mcp.LayerFile(paths, inv.Winner[name])
		if path == "" {
			return mcpToggleResultMsg{name: name, err: errMCPOwnerNoPath}
		}
		// Same guard REST applies BEFORE decode and BEFORE writing
		// (rest_mcp.go mcpGuardLayerFile → 403 mcp_path_not_allowed): the
		// directory of the target file must sit in an allowed workspace
		// root. One predicate, exported from pkg/channels — never a copy of
		// the rule: this is the documented exception to the "pkg/channels must
		// not become a TUI dependency" note in commands_admin_write.go, taken
		// because path policy is exactly the rule that must not be copied.
		// LayerFile only ever yields <root>/mcp.json, so the
		// base-name half of the REST check cannot fail here.
		if !channels.IsAllowedWorkspacePath(filepath.Dir(path)) {
			return mcpToggleResultMsg{name: name, path: path, err: errMCPPathNotAllowed}
		}
		// An invalid winner is NOT toggleable (the rule the WebUI shares):
		// the flip below would either no-op while the UI claims a state
		// change, or — on an entry carrying a literal "disabled": false —
		// splice bytes of an already-broken entry, hiding the breakage
		// behind an intentional-looking verdict. Point at the file instead.
		if inv.Effective[name] == "invalid" {
			return mcpToggleResultMsg{name: name, err: errMCPInvalid}
		}
		enabled := inv.Effective[name] == "enabled"
		// SetDisabled(path, name, disabled): passing the CURRENT enabled
		// state as the "disabled" flag flips it.
		res, err := mcp.SetDisabled(path, name, enabled)
		if err != nil {
			return mcpToggleResultMsg{name: name, err: err}
		}
		return mcpToggleResultMsg{name: name, enabled: !enabled, changed: res.Changed}
	}
}

// ── handleMCPToggleResult processes a toggle result ────────────────────

// handleMCPToggleResult writes the feedback line and, on success, calls
// the agent loop's SyncMCPServers (mcp.json is not config-watched — that
// pass is what makes the write visible to the tool/prompt surface) and
// reloads the list.
func (m *Model) handleMCPToggleResult(msg mcpToggleResultMsg) tea.Cmd {
	if msg.logout {
		// The other mutation that travels on this message: the sign-out has
		// its own (auth-file) handler, and no enabled/changed/path to report.
		return m.handleMCPAuthLogoutResult(msg.name, msg.err)
	}
	switch {
	case msg.err == nil:
		if msg.changed {
			state := i18n.T("tui.mcpEnabled")
			if !msg.enabled {
				state = i18n.T("tui.mcpDisabled")
			}
			m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpToggleSuccess"), msg.name, state)
		} else {
			// changed==false: the writer reports no bytes moved — say "no
			// change" instead of claiming a flip that never happened.
			m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpToggleNoChange"), msg.name)
		}
		// Idempotent pass; fires for a no-op result too, like the
		// server's fireReloadMCP — the loop can only end up matching
		// the disk it already matched.
		syncMCPServersFor(m.agentLoop)
	case errors.Is(msg.err, errMCPUnavailable):
		m.mcpFeedback = i18n.T("tui.mcpUnavailable")
	case errors.Is(msg.err, errMCPOwnerNoPath):
		m.mcpFeedback = i18n.T("tui.mcpOwnerNoPath")
	case errors.Is(msg.err, errMCPPathNotAllowed):
		// Mirrors REST's 403 mcp_path_not_allowed detail: name the target.
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpPathNotAllowed"), msg.name, msg.path)
	case errors.Is(msg.err, errMCPInvalid):
		// The "fix it in the file" hint for the refused invalid row.
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpToggleInvalid"), msg.name)
	default:
		m.mcpFeedback = fmt.Sprintf(i18n.T("tui.mcpToggleFailed"), msg.name, msg.err)
	}
	m.loadMCPList()
	return m.tickCmd()
}

// ── Formatting helpers ─────────────────────────────────────────────────

// formatMCPItem renders one row: status glyph (● enabled / ○ disabled /
// ! invalid), the name padded with formatSkillItem's %-15s rule, the
// owning-layer tag, then the auth marker (authSuffix) verbatim AFTER the
// closing bracket. The glyph/padding/tag shape stays byte-stable — the
// ANSI-stability and formatting tests depend on it — and the localized text
// is the caller's business, which keeps this formatter pure and
// table-testable. authSuffix is "" for a server with no auth verdict.
func formatMCPItem(name, layer, effective, authSuffix string) string {
	status := "●"
	switch effective {
	case "disabled":
		status = "○"
	case "invalid":
		status = "!"
	}
	return fmt.Sprintf("%s %-15s — [%s]%s", status, name, layer, authSuffix)
}

// ── renderMCPDetail renders the detail screen of one MCP server ────────
//
// Everything on this screen comes from m.mcpInventory — the SAME snapshot
// the visible list rows were built from — so it always describes the
// selected row's winner, never a re-read one. mcp.ParseFile is never
// called here: command/url/description are the RAW stored bytes (${VAR}
// appears literally) and env/header VALUES never leave pkg/mcp (only the
// key NAMES, which ServerSummary already exposes sorted).
func (m *Model) renderMCPDetail() string {
	name := m.mcpDetailName
	inv := m.mcpInventory
	rows, known := inv.ByName[name]
	if !m.mcpInventoryValid || name == "" || !known || len(rows) == 0 {
		// No snapshot to describe from (modal was reset): fall back to the
		// list instead of rendering a stale or invented detail.
		m.modalMode = ModalMCP
		return m.renderModal(m.modalTitleFor(ModalMCP))
	}

	// Rows are low → high; the layer the verdict reports as owner holds the
	// winning copy. Every other row is a shadowed copy of the same name. The
	// winner (and its index, which is what tells the shadowed rows apart) is
	// resolved by the shared helper so the authorization entry point can read
	// the SAME copy this screen describes.
	w, winnerIdx, ok := m.mcpWinnerRow(name)
	if !ok {
		m.modalMode = ModalMCP
		return m.renderModal(m.modalTitleFor(ModalMCP))
	}
	s := w.Summary

	verdict, verdictLabel := inv.Effective[name], inv.Effective[name]
	switch verdict {
	case "enabled":
		verdictLabel = i18n.T("tui.mcpEnabled")
	case "disabled":
		verdictLabel = i18n.T("tui.mcpDisabled")
	case "invalid":
		verdictLabel = i18n.T("tui.mcpInvalid")
	}
	verdictColor := SecondaryColor
	if verdict != "enabled" {
		verdictColor = CommentColor
	}

	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(m.modalTitleFor(ModalMCPDetail)) + "\n")
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Center,
		TitleStyle.Render(name),
		"  ",
		lipgloss.NewStyle().Foreground(verdictColor).Render("["+verdictLabel+"]"),
	) + "\n\n")

	labelStyle := lipgloss.NewStyle().Foreground(AccentColor)
	addField := func(label, value string) {
		sb.WriteString(labelStyle.Render(label+": ") + value + "\n")
	}

	// Winning copy: which layer owns it, from which file, and whether it is
	// a real definition or a pure disable stub ({"disabled":true} only).
	addField(i18n.T("tui.mcpDetailLayer"), w.Layer)
	addField(i18n.T("tui.mcpDetailPath"), w.Path)
	definition := i18n.T("tui.mcpDetailStub")
	if w.Defines {
		definition = i18n.T("tui.mcpDetailReal")
	}
	addField(i18n.T("tui.mcpDetailDefinition"), definition)

	// Raw stored bytes: a ${VAR} reference is shown literally, never the
	// value it expands to. Fields a copy does not carry are omitted.
	if s.Kind != "" {
		addField(i18n.T("tui.mcpDetailKind"), s.Kind)
	}
	if s.Command != "" {
		addField(i18n.T("tui.mcpDetailCommand"), s.Command)
		addField(i18n.T("tui.mcpDetailArgs"), fmt.Sprintf("%d", s.Args))
	}
	if s.URL != "" {
		addField(i18n.T("tui.mcpDetailURL"), s.URL)
		if s.Type != "" {
			addField(i18n.T("tui.mcpDetailType"), s.Type)
		}
	}
	if s.Description != "" {
		addField(i18n.T("tui.mcpDetailDescription"), s.Description)
	}
	if w.Invalid != "" {
		addField(i18n.T("tui.mcpDetailInvalid"), w.Invalid)
	}
	// Key NAMES only — env/header values can hold secrets and never render.
	addField(i18n.T("tui.mcpDetailEnvKeys"), mcpDetailKeyList(s.EnvKeys))
	addField(i18n.T("tui.mcpDetailHeaderKeys"), mcpDetailKeyList(s.HeaderKeys))

	// Auth verdict of the WINNING copy: in which carrier the credential would
	// travel, and whether the sign-in the entry needs has been done (or one is
	// stored). Shown only for an auth-bearing entry — a stdio server, or a
	// remote one pkg/mcp does not report as auth-bearing, renders neither line
	// (no empty "Auth:" row). The values come from CheckAuth, which answers
	// booleans and names a carrier: no token, and no other field of the stored
	// record, is ever reachable from here.
	//
	// THE ONE RENDER RULE, the same one mcpAuthSuffix applies to the list row
	// and the WebUI's chip applies to its own row:
	//
	//	authenticated ⇒ show authenticated; else required ⇒ show needs-auth;
	//	else nothing
	//
	// Authenticated is tested FIRST because that is what CheckAuth's
	// Authenticated MEANS ("a usable credential exists, no sign-in is needed"),
	// and the two flags are ORTHOGONAL — pkg/mcp documents that Authenticated
	// is not derived from Required and anticipates a stored credential on an
	// entry whose AuthRequired is false. Keying the block on Required rendered
	// NOTHING for such a verdict here while the WebUI painted its
	// "Authenticated" chip for the very same DTO.
	auth, authKnown := m.mcpAuth[name]
	authShown := authKnown && (auth.Authenticated || auth.Required)
	if authShown {
		addField(i18n.T("tui.mcpDetailAuth"), mcpAuthMethodLabel(auth.Method))
		state := i18n.T("tui.mcpAuthRequired")
		if auth.Authenticated {
			state = i18n.T("tui.mcpAuthOK")
		}
		addField(i18n.T("tui.mcpDetailAuthState"), state)
	}

	// One line per shadowed copy: the whole stack stays visible, so the user
	// can see what the winner is shadowing (layer, file, defines, the copy's
	// own disabled flag and its raw command/url).
	shadowed := make([]mcp.EntryState, 0, len(rows)-1)
	for i, r := range rows {
		if i != winnerIdx {
			shadowed = append(shadowed, r)
		}
	}
	if len(shadowed) > 0 {
		sb.WriteString("\n")
		sb.WriteString(labelStyle.Render(i18n.T("tui.mcpDetailShadowed")+":") + "\n")
		for _, r := range shadowed {
			line := fmt.Sprintf("  - %s | %s | defines=%t | disabled=%t",
				r.Layer, r.Path, r.Defines, r.Disabled)
			if r.Summary.Command != "" {
				line += " | command=" + r.Summary.Command
			} else if r.Summary.URL != "" {
				line += " | url=" + r.Summary.URL
			}
			sb.WriteString(CommentColorStyle.Render(line) + "\n")
		}
	}

	sb.WriteString("\n")
	if verdict == "invalid" {
		// One-line pointer to the fix: invalid rows are not toggleable
		// (same rule as the WebUI) and must be repaired in the file.
		sb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.mcpInvalidHint")) + "\n")
	}
	// The auth line, shared with ModalMCPAuth: the inline "auth does not apply
	// to a stdio server" hint of a refused "a", the copy confirmation, the
	// outcome of the flow the user just came back from, or the result of a
	// sign-out.
	if line := m.mcpAuthFeedbackLine(); line != "" {
		sb.WriteString(line + "\n")
	}
	// The sign-out hint only appears for an auth-bearing entry: that is
	// exactly when "d" has something to do, so advertising it elsewhere would
	// promise an action that can only refuse. A stored credential counts as
	// auth-bearing too (authShown), because "d" is precisely what takes it
	// back.
	hints := i18n.T("tui.mcpDetailHints")
	if authShown {
		hints += " | " + i18n.T("tui.mcpLogoutHint")
	}
	sb.WriteString(CommentColorStyle.Render("  " + hints))

	box := ModalContainer.Width(m.width - 10).Render(sb.String())
	return m.paintFrame(box)
}

// mcpWinnerRow returns the winning copy of name out of the stored inventory
// snapshot (the layer mcpInventory.Winner names), its index inside
// ByName[name] — every other row is a shadowed copy — and whether a snapshot
// was there to read at all.
//
// It is the single resolver behind both the detail screen and the
// authorization entry point: reading the winner from anywhere else could
// describe a different copy than the one on screen. Rows are low → high and
// the highest copy wins when the winner layer names no row (which
// ReadInventory does not produce; the fallback mirrors the detail renderer).
func (m *Model) mcpWinnerRow(name string) (mcp.EntryState, int, bool) {
	inv := m.mcpInventory
	if !m.mcpInventoryValid || name == "" {
		return mcp.EntryState{}, 0, false
	}
	rows := inv.ByName[name]
	if len(rows) == 0 {
		return mcp.EntryState{}, 0, false
	}
	winnerIdx := -1
	for i := range rows {
		if rows[i].Layer == inv.Winner[name] {
			winnerIdx = i // last match wins (rows are low → high)
		}
	}
	if winnerIdx < 0 {
		winnerIdx = len(rows) - 1
	}
	return rows[winnerIdx], winnerIdx, true
}

// mcpDetailKeyList joins the sorted key NAMES of an env/header map; an
// empty map keeps the line present with the localized "(none)". Values are
// never available here — ServerSummary only ever carries the names.
func mcpDetailKeyList(keys []string) string {
	if len(keys) == 0 {
		return i18n.T("tui.mcpDetailNone")
	}
	return strings.Join(keys, ", ")
}
