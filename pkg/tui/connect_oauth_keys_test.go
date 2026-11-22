package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func qKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

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

func TestConnectOAuthKey_QIsTextNotClose(t *testing.T) {
	m, _ := connectOAuthEnterPending(t, "openai")

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

func TestConnectOAuthKey_EnterIsInertWithoutAPendingFlow(t *testing.T) {
	states := map[string]int{
		"idle":   connectOAuthIdle,
		"failed": connectOAuthFailed,
	}
	for name, state := range states {
		t.Run(name, func(t *testing.T) {
			m := &Model{modalMode: ModalConnectOAuth, connectOAuthState: state}
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
