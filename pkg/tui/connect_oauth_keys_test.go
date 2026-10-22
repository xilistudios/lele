package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// This file pins the KEYBOARD and RENDER-ROUTING contract of the native
// /connect OAuth modal (ModalConnectOAuth). Unlike connect_oauth_test.go
// (which drives the engine seam and the state machine), every test here goes
// through the REAL modal key dispatcher `m.handleModalKey(...)` — the point is
// to prove the generic modal switch does NOT steal a key that belongs to the
// modal (or that belongs to the shared text input), and that renderActiveModal
// dispatches to the OAuth surface rather than the generic wizard form.
//
// Invariants pinned here (each maps to a guard in production):
//   1. ctrl+y copies the authorization URL / reports failure with no URL.
//   2. "q" is TEXT, not a close key (isFormModal(ModalConnectOAuth) == true).
//   3. ENTER is inert unless a live flow is pending — and it NEVER clears the
//      input in that case (the SetValue("") sits BELOW the state guard).
//   4. ESC cancels, releases the listener and returns to the /connect picker.
//   5. renderActiveModal routes to renderConnectOAuth, not renderFormModal.
//   6. up/down/k/j never move the invisible list cursor
//      (isListModal(ModalConnectOAuth) == false).

// qKey builds a rune key message ("q") the way bubbletea does.
func qKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// ── 1. ctrl+y copies the authorization URL (OSC 52 on stdout) ─────────

// TestConnectOAuthKey_CtrlYCopiesTheAuthorizationURL pins the copy branch of
// handleConnectOAuthKey as reached through handleModalKey: handled, no cmd,
// localized "copied" feedback, no error, and the OSC 52 payload carrying the
// URL (clipboard.go writes the payload synchronously to os.Stdout).
func TestConnectOAuthKey_CtrlYCopiesTheAuthorizationURL(t *testing.T) {
	m, flow := connectOAuthEnterPending(t, "anthropic")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prev := os.Stdout
	os.Stdout = w
	_, cmd := m.handleModalKey(tea.KeyMsg{Type: tea.KeyCtrlY})
	os.Stdout = prev
	_ = w.Close()
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	_ = r.Close()

	if cmd != nil {
		t.Errorf("ctrl+y returned a command, want none: %v", cmd)
	}
	if m.connectOAuthFeedback != i18n.T("tui.connectOAuthCopied") {
		t.Errorf("feedback = %q, want %q", m.connectOAuthFeedback, i18n.T("tui.connectOAuthCopied"))
	}
	if m.connectOAuthError != "" {
		t.Errorf("error line set on a successful copy: %q", m.connectOAuthError)
	}
	if want := buildOSC52(flow.url); !strings.Contains(string(buf[:n]), want) {
		t.Errorf("clipboard payload does not carry the authorization URL (got %q)", string(buf[:n]))
	}
}

// ── 2. ctrl+y with no URL reports failure ─────────────────────────────

// TestConnectOAuthKey_CtrlYWithNoURLReportsFailure pins the empty-URL branch:
// handled, no cmd, no success feedback and the localized "copy failed" error.
// The model is built bare (no pending flow) so connectOAuthURL is "".
func TestConnectOAuthKey_CtrlYWithNoURLReportsFailure(t *testing.T) {
	m := &Model{modalMode: ModalConnectOAuth, connectOAuthState: connectOAuthIdle}

	_, cmd := m.handleModalKey(tea.KeyMsg{Type: tea.KeyCtrlY})
	if cmd != nil {
		t.Errorf("ctrl+y with no URL returned a command, want none: %v", cmd)
	}
	if m.connectOAuthFeedback != "" {
		t.Errorf("success feedback set with no URL: %q", m.connectOAuthFeedback)
	}
	if m.connectOAuthError != i18n.T("tui.connectOAuthCopyFailed") {
		t.Errorf("error = %q, want %q", m.connectOAuthError, i18n.T("tui.connectOAuthCopyFailed"))
	}
}

// ── 3. "q" is TEXT, not a close key ───────────────────────────────────

// TestConnectOAuthKey_QIsTextNotClose pins isFormModal(ModalConnectOAuth)
// == true. If the generic switch ever treated ModalConnectOAuth as a
// non-form modal, `case "esc", "q"` would close it — so this asserts the
// modal stays open, the flow survives, and "q" reaches the shared input.
func TestConnectOAuthKey_QIsTextNotClose(t *testing.T) {
	m, _ := connectOAuthEnterPending(t, "openai")

	// The returned cmd may be the textinput's blink command (that is what
	// "forwarded to the widget" looks like); the invariants that matter are
	// the three below.
	_, _ = m.handleModalKey(qKey('q'))
	if m.modalMode != ModalConnectOAuth {
		t.Errorf("mode = %v, want ModalConnectOAuth (\"q\" must not close the form)", m.modalMode)
	}
	if m.connectOAuthFlow == nil || m.connectOAuthFlow.(*fakeConnectOAuthFlow).cancelCall != 0 {
		t.Errorf("typing \"q\" cancelled the flow: flow=%v cancelCall=%v",
			m.connectOAuthFlow, m.connectOAuthFlow != nil)
	}
	if got := m.textInput.Value(); got != "q" {
		t.Errorf("input = %q, want \"q\" to reach the shared text widget", got)
	}
}

// ── 4. ENTER is inert when there is no live pending flow ──────────────

// TestConnectOAuthKey_EnterIsInertWithoutAPendingFlow pins that ENTER only
// emits a submit command when state == pending AND a flow is live. Both
// sub-cases pre-fill the input with "SIN-USAR" and assert it is NOT cleared:
// the production SetValue("") sits BELOW the state guard, so moving it above
// (or dropping the guard) would be caught here (input cleared / submit fired).
func TestConnectOAuthKey_EnterIsInertWithoutAPendingFlow(t *testing.T) {
	states := map[string]int{
		"idle":   connectOAuthIdle,
		"failed": connectOAuthFailed,
	}
	for name, state := range states {
		t.Run(name, func(t *testing.T) {
			m := &Model{modalMode: ModalConnectOAuth, connectOAuthState: state}
			// A fake flow with NO live engine underneath: if ENTER were NOT
			// inert it would call flow.Submit and record it.
			flow := &fakeConnectOAuthFlow{}
			m.connectOAuthFlow = flow
			m.textInput.SetValue("SIN-USAR")

			_, cmd := m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})
			if cmd != nil {
				t.Errorf("ENTER in state %q returned a command, want none: %v", name, cmd)
			}
			if len(flow.submits) != 0 {
				t.Errorf("ENTER in state %q submitted to the engine: %v", name, flow.submits)
			}
			if got := m.textInput.Value(); got != "SIN-USAR" {
				t.Errorf("input = %q, want it untouched (no live pending flow)", got)
			}
		})
	}
}

// ── 5. ENTER with an empty box still hands the input to the engine ────

// TestConnectOAuthKey_EnterOnEmptyBoxSubmitsEmptyInput pins that the modal
// does NOT pre-validate the box: ENTER emits the submit command (the engine
// decides the input is empty) and clears the credential-shaped widget.
func TestConnectOAuthKey_EnterOnEmptyBoxSubmitsEmptyInput(t *testing.T) {
	m, flow := connectOAuthEnterPending(t, "anthropic")
	m.textInput.SetValue("")

	cmd := connectOAuthPressEnter(t, m)
	if cmd == nil {
		t.Fatal("ENTER on a pending flow with an empty box produced no submit command")
	}
	if got := m.textInput.Value(); got != "" {
		t.Errorf("input = %q, want cleared the instant it is handed to the command", got)
	}
	if m.connectOAuthFeedback != "" || m.connectOAuthError != "" {
		t.Errorf("feedback/error not cleared on submit: %q / %q",
			m.connectOAuthFeedback, m.connectOAuthError)
	}
	runCmd(t, m, cmd)
	if len(flow.submits) != 1 || flow.submits[0] != "" {
		t.Errorf("engine submits = %v, want exactly one empty submit", flow.submits)
	}
}

// ── 6. ESC cancels and returns to the /connect picker ─────────────────

// TestConnectOAuthKey_EscCancelsAndReturnsToThePicker pins the ESC branch of
// handleConnectOAuthKey: it cancels (releasing the loopback listener), clears
// every auth field, and re-opens the /connect picker — WITHOUT persisting any
// credential.
func TestConnectOAuthKey_EscCancelsAndReturnsToThePicker(t *testing.T) {
	m, flow := connectOAuthEnterPending(t, "anthropic")

	_, cmd := m.handleModalKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Errorf("ESC returned a command, want none: %v", cmd)
	}
	if flow.cancelCall < 1 {
		t.Errorf("flow cancels = %d, want >= 1 (loopback listener released)", flow.cancelCall)
	}
	if m.connectOAuthFlow != nil {
		t.Error("ESC left a live flow on the model")
	}
	if m.connectOAuthState != connectOAuthIdle {
		t.Errorf("state = %d, want connectOAuthIdle", m.connectOAuthState)
	}
	if m.connectOAuthURL != "" || m.connectOAuthName != "" {
		t.Errorf("ESC left auth fields behind: url=%q name=%q", m.connectOAuthURL, m.connectOAuthName)
	}
	if m.connectOAuthFeedback != "" || m.connectOAuthError != "" {
		t.Errorf("ESC left feedback/error behind: %q / %q", m.connectOAuthFeedback, m.connectOAuthError)
	}
	if m.modalMode != ModalAddProvider || !m.providerTypePicker {
		t.Errorf("mode = %v picker = %v, want the /connect picker (ModalAddProvider + providerTypePicker)",
			m.modalMode, m.providerTypePicker)
	}
	if got := authGetCredential(t, "anthropic"); got != nil {
		t.Errorf("ESC persisted a credential: %+v", got)
	}
}

// ── 7. render routing: the OAuth surface, not the wizard form ─────────

// TestConnectOAuthRender_RoutesToTheOAuthSurface pins the renderActiveModal
// dispatcher: ModalConnectOAuth must render connectOAuthBox (title, paste
// placeholder, ESC hint) and NEVER the generic provider wizard form (whose
// title is tui.addProvider). The second phase flips modalMode to
// ModalAddProvider and asserts the opposite, proving the test discriminates
// and cannot pass by accident.
func TestConnectOAuthRender_RoutesToTheOAuthSurface(t *testing.T) {
	m, _ := connectOAuthEnterPending(t, "anthropic")
	m.width, m.height = 120, 40

	out := ansi.Strip(m.renderActiveModal())
	if !strings.Contains(out, i18n.T("tui.connectOAuthTitle")) {
		t.Errorf("render did not show the OAuth title:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.connectOAuthPlaceholder")) {
		t.Errorf("render did not show the paste-box placeholder:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.connectOAuthHintClose")) {
		t.Errorf("render did not show the ESC hint:\n%s", out)
	}
	if strings.Contains(out, i18n.T("tui.addProvider")) {
		t.Errorf("render fell through to the generic provider wizard form:\n%s", out)
	}
	if strings.Contains(out, "▶ Provider name") {
		t.Errorf("render fell through to the generic wizard step marker:\n%s", out)
	}

	// Discriminating flip: the SAME model with a different modalMode must
	// render the wizard form (title + step marker) and NOT the OAuth title.
	m.modalMode = ModalAddProvider
	out = ansi.Strip(m.renderActiveModal())
	if !strings.Contains(out, i18n.T("tui.addProvider")) {
		t.Errorf("renderActiveModal(ModalAddProvider) did not show the wizard title:\n%s", out)
	}
	if !strings.Contains(out, "▶ Provider name") {
		t.Errorf("renderActiveModal(ModalAddProvider) did not show a wizard step marker:\n%s", out)
	}
	if strings.Contains(out, i18n.T("tui.connectOAuthTitle")) {
		t.Errorf("renderActiveModal(ModalAddProvider) leaked the OAuth title:\n%s", out)
	}
}

// ── 8. navigation keys never move the invisible list cursor ───────────

// TestConnectOAuthKey_NavigationDoesNotMoveTheListCursor pins
// isListModal(ModalConnectOAuth) == false. The modal has no rows, so up/k/
// down/j must not move the shared list cursor behind it.
//
// The UP branch is the discriminating one: the production "up" arm has no
// secondary bounds guard, so it is gated ONLY by isListModal. (The "down" arm
// is additionally bounded by `modalSelectedIdx < len(modalItems)-1`, which is
// false for an empty modalItems and would mask an isListModal regression — a
// defensive redundancy, not something this test can force red.) The direct
// isListModal assertion below is the primary pin and fails immediately on the
// same mutation.
func TestConnectOAuthKey_NavigationDoesNotMoveTheListCursor(t *testing.T) {
	if isListModal(ModalConnectOAuth) {
		t.Fatal("isListModal(ModalConnectOAuth) = true, want false (the modal has no rows)")
	}
	m, _ := connectOAuthEnterPending(t, "anthropic")
	m.modalSelectedIdx = 3

	_, cmd := m.handleModalKey(tea.KeyMsg{Type: tea.KeyUp})
	if cmd != nil {
		t.Errorf("up returned a command, want none: %v", cmd)
	}
	if m.modalSelectedIdx != 3 {
		t.Errorf("modalSelectedIdx = %d, want it unmoved (no list cursor behind the modal)", m.modalSelectedIdx)
	}
}
