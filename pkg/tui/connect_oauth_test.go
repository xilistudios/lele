package tui

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// This file is the TUI test-suite for the native /connect OAuth flow
// (ModalConnectOAuth). It mirrors mcp_auth_test.go: the pkg/auth engine seam
// (startConnectOAuthFlow) is substituted with a scripted fake flow, so no
// browser, loopback listener, network or real token exchange is ever
// involved. The tests drive the REAL picker dispatch (applyConnectRow), the
// REAL message handlers (handleConnectOAuthStart/Poll/Submit) through the
// Update path, and the REAL renderer, asserting the three documented
// invariants: Update never blocks, nothing is orphaned, and no
// credential-shaped input is retained.

// ── fake engine ───────────────────────────────────────────────────────

// fakeConnectOAuthFlow is a scripted connectOAuthFlow. It records submits and
// cancels and reports a status the test controls. Credential() returns the
// pointer held by the model under test (set to cred once a start succeeds) so
// connectOAuthFinishDone can retrieve a concrete token without a real store.
type fakeConnectOAuthFlow struct {
	url        string
	status     string
	statusErr  error
	credential *auth.AuthCredential
	submits    []string
	cancelCall int
	flowDone   bool
	submitErr  error
}

func (f *fakeConnectOAuthFlow) URL() string { return f.url }

func (f *fakeConnectOAuthFlow) Status() (string, error) { return f.status, f.statusErr }

func (f *fakeConnectOAuthFlow) Done() bool { return f.flowDone || f.status == connectOAuthStatusDone }

func (f *fakeConnectOAuthFlow) Credential() *auth.AuthCredential { return f.credential }

func (f *fakeConnectOAuthFlow) Submit(input string) error {
	f.submits = append(f.submits, input)
	if f.submitErr != nil {
		// A failed exchange leaves the flow OPEN and retryable, exactly like
		// the engine documents (DONE() stays false so ENTER remains a retry).
		f.status, f.statusErr = connectOAuthStatusError, f.submitErr
		f.flowDone = false
		return f.submitErr
	}
	f.status, f.statusErr = connectOAuthStatusDone, nil
	f.flowDone = true
	return nil
}

func (f *fakeConnectOAuthFlow) Cancel() error {
	f.cancelCall++
	f.status = connectOAuthStatusError
	f.statusErr = auth.ErrFlowCanceled
	f.flowDone = true
	return nil
}

// connectOAuthStartCapture substitutes the startConnectOAuthFlow seam for one
// test and records what the TUI asked the engine for (config + FlowOptions).
type connectOAuthStartCapture struct {
	cfgs []auth.OAuthProviderConfig
	opts []auth.FlowOptions
	flow connectOAuthFlow
	err  error
}

func (c *connectOAuthStartCapture) install(t *testing.T) {
	t.Helper()
	prev := startConnectOAuthFlow
	startConnectOAuthFlow = func(_ context.Context, cfg auth.OAuthProviderConfig, opts auth.FlowOptions) (connectOAuthFlow, error) {
		c.cfgs = append(c.cfgs, cfg)
		c.opts = append(c.opts, opts)
		if c.err != nil {
			return nil, c.err
		}
		return c.flow, nil
	}
	t.Cleanup(func() { startConnectOAuthFlow = prev })
}

// ── model/row helpers ─────────────────────────────────────────────────

// connectOAuthRowIdx returns the catalogue index of the OAuth row for typ.
func connectOAuthRowIdx(t *testing.T, typ string) int {
	t.Helper()
	for i, r := range connectRows() {
		if r.kind == connectRowOAuth && r.typ == typ {
			return i
		}
	}
	t.Fatalf("no OAuth row for %q", typ)
	return -1
}

// connectOAuthPickRow drives the row-first /connect picker to the OAuth row
// for typ and presses Enter, then runs the returned start command through the
// real Update path. It returns the model and the capture the substituted seam
// recorded.
func connectOAuthPickRow(t *testing.T, m *Model, typ string, capture *connectOAuthStartCapture) *Model {
	t.Helper()
	capture.install(t)
	m.width, m.height = 140, 40
	m.executeCommand("/connect")
	if m.modalMode != ModalAddProvider || !m.providerTypePicker {
		t.Fatalf("expected the /connect picker, got mode=%v picker=%v", m.modalMode, m.providerTypePicker)
	}
	m.providerTypePickerIdx = connectOAuthRowIdx(t, typ)
	_, cmd := m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("the OAuth row produced no start command")
	}
	if m.modalMode != ModalConnectOAuth {
		t.Fatalf("mode after picking the OAuth row = %v, want ModalConnectOAuth", m.modalMode)
	}
	m.Update(cmd())
	return m
}

// pinEnglish fixes the render language for the rest of the test. It MUST be
// called AFTER newTestModel: the constructor runs
// i18n.InitWithLanguage(cfg.GetLanguage()) and an empty language falls back to
// Spanish, silently undoing a SetLanguage issued before it (so a "pin English
// so the assertions are stable" comment followed by newTestModel pinned
// nothing).
func pinEnglish(t *testing.T) {
	t.Helper()
	prev := i18n.GetLanguage()
	i18n.SetLanguage("en")
	t.Cleanup(func() { i18n.SetLanguage(prev) })
}

// connectOAuthEnterPending drives the full path to a live pending flow: it
// picks the OAuth row for typ, runs the start command, and asserts the modal
// reached pending with the fake flow adopted. It returns the model and the
// fake flow the substituted seam handed back.
func connectOAuthEnterPending(t *testing.T, typ string) (*Model, *fakeConnectOAuthFlow) {
	t.Helper()
	flow := &fakeConnectOAuthFlow{
		url:    "https://auth.example.test/authorize?state=s",
		status: connectOAuthStatusPending,
		credential: &auth.AuthCredential{
			AccessToken: "at-secret",
			Provider:    typ,
			AuthMethod:  "oauth",
		},
	}
	capture := &connectOAuthStartCapture{flow: flow}
	// The model must exist before the language is pinned (newTestModel
	// re-initialises i18n from the config; see pinEnglish).
	m := newTestModel(t)
	pinEnglish(t)
	m = connectOAuthPickRow(t, m, typ, capture)
	if m.connectOAuthState != connectOAuthPending {
		t.Fatalf("state after start = %d (err %q), want connectOAuthPending",
			m.connectOAuthState, m.connectOAuthError)
	}
	if m.connectOAuthFlow == nil {
		t.Fatal("no live flow on the model after a successful start")
	}
	if m.connectOAuthURL != flow.url {
		t.Errorf("connectOAuthURL = %q, want %q", m.connectOAuthURL, flow.url)
	}
	// The engine must be asked for a LOOPBACK flow (the TUI binds and shows
	// the callback URL; it never opens a browser itself).
	if len(capture.opts) != 1 || capture.opts[0].Mode != auth.FlowModeLoopback {
		t.Errorf("FlowOptions = %+v, want a single loopback request", capture.opts)
	}
	if len(capture.cfgs) != 1 || capture.cfgs[0].Name != typ {
		t.Errorf("engine asked for cfg %v, want one config named %q", capture.cfgs, typ)
	}
	return m, flow
}

// connectOAuthPress ENTER on the OAuth modal and return the submit command.
func connectOAuthPressEnter(t *testing.T, m *Model) tea.Cmd {
	t.Helper()
	_, cmd := m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})
	return cmd
}

// ── 1. entry point: the OAuth row starts the flow ─────────────────────

func TestConnectOAuthRow_StartsFlowAndShowsURL(t *testing.T) {
	m, flow := connectOAuthEnterPending(t, "openai")

	out := ansi.Strip(m.renderConnectOAuth())
	if !strings.Contains(out, flow.url) {
		t.Errorf("pending modal does not show the authorization URL:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.connectOAuthHintEnter")) ||
		!strings.Contains(out, i18n.T("tui.connectOAuthHintCopy")) {
		t.Errorf("pending modal lacks its hints:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.connectOAuthTitle")) {
		t.Errorf("pending modal lacks its title:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.connectOAuthInstructions")) {
		t.Errorf("pending modal lacks its instructions:\n%s", out)
	}
	// The input widget is the paste area and must be focused (a blurred
	// textinput drops every key, so paste would be impossible).
	if m.textInput.Value() != "" {
		t.Errorf("input must start empty, got %q", m.textInput.Value())
	}
}

func TestConnectOAuthRow_StartErrorIsLocalizedAndDoesNotAdoptFlow(t *testing.T) {
	capture := &connectOAuthStartCapture{err: auth.ErrFlowNoManualRedirect}
	m := newTestModel(t)
	pinEnglish(t)
	m = connectOAuthPickRow(t, m, "anthropic", capture)
	if m.connectOAuthState != connectOAuthFailed {
		t.Fatalf("state = %d, want connectOAuthFailed", m.connectOAuthState)
	}
	if m.connectOAuthFlow != nil {
		t.Error("a failed start must not adopt a flow")
	}
	want := connectOAuthStartErrorText("anthropic", auth.ErrFlowNoManualRedirect)
	if m.connectOAuthError != want {
		t.Errorf("error = %q, want %q", m.connectOAuthError, want)
	}
	if m.connectOAuthError != i18n.T("tui.connectOAuthErrNoManualRedirect") {
		t.Errorf("error = %q, want the localized sentinel", m.connectOAuthError)
	}
	// The raw engine text must never surface.
	if strings.Contains(m.connectOAuthError, auth.ErrFlowNoManualRedirect.Error()) &&
		m.connectOAuthError == auth.ErrFlowNoManualRedirect.Error() {
		t.Errorf("start error leaked the raw engine text: %q", m.connectOAuthError)
	}
}

// TestConnectOAuthStartFromRow_RejectsInconsistentRow pins the row↔catalog
// agreement the picker relies on: connectOAuthStartFromRow resolves the
// registry config from the ROW (row.oauthName), so a row whose name is empty
// or disagrees with connectOAuthCatalog[row.typ] must be refused instead of
// logging the user in to a provider the row does not represent.
func TestConnectOAuthStartFromRow_RejectsInconsistentRow(t *testing.T) {
	cases := []struct {
		name string
		row  connectRow
	}{
		{"empty", connectRow{kind: connectRowOAuth, typ: "anthropic"}},
		{"mismatched", connectRow{kind: connectRowOAuth, typ: "anthropic", oauthName: "openai"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Install the seam so a broken guard fails loudly here instead of
			// reaching the network; the guard must fire BEFORE any start.
			capture := &connectOAuthStartCapture{err: auth.ErrFlowNoManualRedirect}
			capture.install(t)

			m := newTestModel(t)
			pinEnglish(t)
			m.modalMode = ModalAddProvider
			if cmd := m.connectOAuthStartFromRow(tc.row); cmd != nil {
				t.Fatal("an inconsistent row must not produce a start command")
			}
			if len(capture.cfgs) != 0 {
				t.Fatalf("started a flow for an inconsistent row: %+v", capture.cfgs)
			}
			if m.modalMode != ModalAddProvider {
				t.Errorf("mode = %v, want the form to stay put (ModalAddProvider)", m.modalMode)
			}
			// The reason is substituted into the localized template: the
			// rendered line must carry the translated reason and NO literal
			// "%v" (gluing a hard-coded reason onto the template printed the
			// placeholder, see connectOAuthStartUnavailable).
			want := fmt.Sprintf(i18n.T("tui.connectOAuthStartFailed"),
				i18n.T("tui.connectOAuthErrInconsistentRow"))
			if m.formError != want {
				t.Errorf("formError = %q, want %q", m.formError, want)
			}
			if strings.Contains(m.formError, "%v") {
				t.Errorf("formError = %q still contains the raw %%v placeholder", m.formError)
			}
		})
	}
}

// ── 2. happy path: poll → done persists credential + enters wizard ────

func TestConnectOAuthFlow_PollDonePersistsAndOpensWizard(t *testing.T) {
	m, flow := connectOAuthEnterPending(t, "anthropic")

	// A pending poll re-arms (the chain stays alive) while nothing persists.
	seq := m.connectOAuthSeq
	_, cmd := m.Update(connectOAuthPollMsg{seq: seq})
	if cmd == nil {
		t.Fatal("a pending flow did not re-arm the poll")
	}
	if m.modalMode != ModalConnectOAuth {
		t.Fatal("a poll left the OAuth modal")
	}
	if got := authGetCredential(t, "anthropic"); got != nil {
		t.Fatalf("credential persisted while still pending: %+v", got)
	}

	// The flow completes: the next poll notices it and runs the one-shot
	// success transition (persist credential + provider entry + wizard).
	flow.status = connectOAuthStatusDone
	flow.flowDone = true
	m.Update(connectOAuthPollMsg{seq: seq})

	if m.connectOAuthFlow != nil {
		t.Error("the flow was not released after success (listener leaked)")
	}
	if flow.cancelCall == 0 {
		t.Error("the loopback listener was never cancelled after success")
	}
	// The one-shot success transition entered the wizard (the auth fields are
	// cleared by the hand-off, so state is Idle; success is carried by the
	// wizard hand-off + feedback, not a lingering succeeded state).
	if m.connectOAuthState != connectOAuthIdle {
		t.Errorf("state after success = %d, want connectOAuthIdle (wizard owns the screen)", m.connectOAuthState)
	}
	// Credential stored under the canonical provider name.
	cred := authGetCredential(t, "anthropic")
	if cred == nil || cred.AccessToken != "at-secret" {
		t.Fatalf("credential not persisted: %+v", cred)
	}
	// Provider entry written with auth_method=oauth and API key cleared.
	entry, ok := m.cfg.Providers.Named["anthropic"]
	if !ok {
		t.Fatal("named provider entry not persisted")
	}
	if entry.AuthMethod != "oauth" {
		t.Errorf("auth_method = %q, want oauth", entry.AuthMethod)
	}
	if entry.APIKey != "" {
		t.Errorf("APIKey = %q, want empty for an OAuth provider", entry.APIKey)
	}
	if entry.Type != "anthropic" {
		t.Errorf("Type = %q, want anthropic", entry.Type)
	}
	// Handed off to the ordinary model wizard on the model-alias step (4).
	if m.modalMode != ModalAddProvider {
		t.Fatalf("mode = %v, want ModalAddProvider (model wizard)", m.modalMode)
	}
	if m.formStepIndex != 4 {
		t.Errorf("formStepIndex = %d, want 4 (model alias)", m.formStepIndex)
	}
	if m.formValues[0] != "anthropic" {
		t.Errorf("formValues[0] = %q, want anthropic", m.formValues[0])
	}
	want := connectOAuthSuccessText("anthropic")
	if m.connectOAuthFeedback != want {
		t.Errorf("feedback = %q, want %q", m.connectOAuthFeedback, want)
	}
	// The success confirmation must actually reach the wizard screen (the
	// form's error slot does not render connectOAuthFeedback on its own).
	if rendered := ansi.Strip(m.renderFormModal(m.modalTitleFor(ModalAddProvider), m.formStepNames())); !strings.Contains(rendered, want) {
		t.Errorf("wizard does not render the OAuth success confirmation %q:\n%s", want, rendered)
	}

	// Terminal state: the poll chain stops (a stale/abandoned poll is dropped
	// because state is no longer pending) and a duplicate success can never
	// re-persist (one-shot guard: the credential is written exactly once).
	if _, cmd := m.Update(connectOAuthPollMsg{seq: seq}); cmd != nil {
		t.Error("a poll was re-armed after a terminal state")
	}
	before := authGetCredential(t, "anthropic")
	m.Update(connectOAuthSubmitMsg{seq: m.connectOAuthSeq, name: "anthropic"})
	after := authGetCredential(t, "anthropic")
	if before == nil || after == nil || before.AccessToken != after.AccessToken {
		t.Errorf("a duplicate success changed the persisted credential: before=%v after=%v", before, after)
	}
	if m.modalMode != ModalAddProvider {
		t.Errorf("a duplicate success changed the modal to %v", m.modalMode)
	}
}

// ── 3. manual paste path (ENTER) ──────────────────────────────────────

func TestConnectOAuthFlow_ManualSubmitSucceedsAndClearsInput(t *testing.T) {
	m, flow := connectOAuthEnterPending(t, "openai")

	m.textInput.SetValue("https://callback.example.test/?code=abc#state")
	cmd := connectOAuthPressEnter(t, m)
	if cmd == nil {
		t.Fatal("enter on a pending flow produced no submit command")
	}
	// Invariant 3: the credential-shaped input is cleared from the shared
	// widget the instant it is handed to the command.
	if got := m.textInput.Value(); got != "" {
		t.Errorf("input retained after submit: %q", got)
	}
	runCmd(t, m, cmd)

	if len(flow.submits) != 1 || flow.submits[0] != "https://callback.example.test/?code=abc#state" {
		t.Fatalf("engine submitted %v, want the pasted input", flow.submits)
	}
	// Success handed off to the wizard (auth fields cleared → Idle).
	if m.connectOAuthState != connectOAuthIdle {
		t.Fatalf("state = %d, want connectOAuthIdle (wizard owns the screen)", m.connectOAuthState)
	}
	if m.modalMode != ModalAddProvider {
		t.Fatalf("mode = %v, want ModalAddProvider (model wizard)", m.modalMode)
	}
	cred := authGetCredential(t, "openai")
	if cred == nil || cred.AccessToken != "at-secret" {
		t.Fatalf("credential not persisted after manual submit: %+v", cred)
	}
}

// ── 4. retryable exchange failure keeps the flow open ─────────────────

func TestConnectOAuthFlow_FailedExchangeStaysPendingAndRetries(t *testing.T) {
	m, flow := connectOAuthEnterPending(t, "gemini")
	flow.submitErr = auth.ErrFlowStateMismatch

	m.textInput.SetValue("BOGUS#STATE")
	runCmd(t, m, connectOAuthPressEnter(t, m))

	if flow.Done() {
		t.Fatal("precondition: a failed exchange must leave the flow open")
	}
	if m.connectOAuthState != connectOAuthPending {
		t.Fatalf("state = %d, want to stay connectOAuthPending (retryable)", m.connectOAuthState)
	}
	if m.connectOAuthError != i18n.T("tui.connectOAuthErrStateMismatch") {
		t.Errorf("error = %q, want the localized sentinel", m.connectOAuthError)
	}
	if got := m.textInput.Value(); got != "" {
		t.Errorf("input retained after a failed submit: %q", got)
	}

	// A poll while still failing re-arms the chain (nothing dies).
	if _, cmd := m.Update(connectOAuthPollMsg{seq: m.connectOAuthSeq}); cmd == nil {
		t.Error("the poll stopped ticking while the flow is still retryable")
	}

	// ENTER still works: the retry succeeds on the SAME flow.
	flow.submitErr = nil
	m.textInput.SetValue("GOOD#STATE")
	runCmd(t, m, connectOAuthPressEnter(t, m))
	if m.modalMode != ModalAddProvider {
		t.Errorf("mode = %v, want ModalAddProvider after the retry succeeded", m.modalMode)
	}
	if m.connectOAuthState != connectOAuthIdle {
		t.Errorf("state = %d, want connectOAuthIdle (success handed off to the wizard)", m.connectOAuthState)
	}
	if len(flow.submits) != 2 {
		t.Errorf("submits = %v, want the two attempts", flow.submits)
	}
	cred := authGetCredential(t, "gemini")
	if cred == nil || cred.AccessToken != "at-secret" {
		t.Errorf("credential not persisted after the retry: %+v", cred)
	}
}

// ── 5. terminal error stops ticking ───────────────────────────────────

func TestConnectOAuthFlow_TerminalErrorStopsTicking(t *testing.T) {
	m, flow := connectOAuthEnterPending(t, "openai")

	flow.status, flow.statusErr, flow.flowDone = connectOAuthStatusError, auth.ErrFlowTimeout, true
	_, cmd := m.Update(connectOAuthPollMsg{seq: m.connectOAuthSeq})
	if cmd != nil {
		t.Error("the poll was re-armed after a terminal error")
	}
	if m.connectOAuthState != connectOAuthFailed {
		t.Fatalf("state = %d, want connectOAuthFailed", m.connectOAuthState)
	}
	if m.connectOAuthError != i18n.T("tui.connectOAuthErrTimedOut") {
		t.Errorf("error = %q, want the localized timeout sentinel", m.connectOAuthError)
	}
	if strings.Contains(m.connectOAuthError, auth.ErrFlowTimeout.Error()) {
		t.Errorf("terminal error leaked the engine text: %q", m.connectOAuthError)
	}
}

// ── 6. orphan guards: a late message never resurrects the modal ───────

func TestConnectOAuthFlow_StaleMessagesNeverResurrectTheModal(t *testing.T) {
	m, flow := connectOAuthEnterPending(t, "openai")

	// ESC cancels: releases the listener, bumps the generation, returns to
	// the picker, and clears every auth field.
	seq := m.connectOAuthSeq
	cmd, handled := m.handleConnectOAuthKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !handled {
		t.Fatal("esc was not handled by the OAuth modal")
	}
	if cmd != nil {
		t.Error("esc produced a command; it must cancel synchronously")
	}
	if m.modalMode != ModalAddProvider {
		t.Fatalf("mode after esc = %v, want the /connect picker", m.modalMode)
	}
	if flow.cancelCall == 0 {
		t.Error("esc did not cancel the flow (listener leaked)")
	}
	if m.connectOAuthFlow != nil {
		t.Error("esc left a live flow on the model")
	}
	if m.connectOAuthState != connectOAuthIdle || m.connectOAuthURL != "" {
		t.Errorf("esc left state=%d url=%q, want idle and empty", m.connectOAuthState, m.connectOAuthURL)
	}
	if m.connectOAuthSeq == seq {
		t.Error("esc did not bump the generation (stale messages can resurrect)")
	}

	// A late start result belonging to the abandoned attempt is CANCELLED,
	// never adopted, and the modal stays closed.
	late := connectOAuthStartMsg{seq: seq, name: "openai", flow: flow}
	m.Update(late)
	if m.modalMode == ModalConnectOAuth {
		t.Error("a late start result reopened the OAuth modal")
	}
	if m.connectOAuthFlow != nil {
		t.Error("a late start result was adopted after cancel")
	}
	if flow.cancelCall < 2 {
		t.Errorf("the late flow was not cancelled: cancelCall=%d", flow.cancelCall)
	}

	// A late poll/submit of the abandoned generation is dropped outright.
	if _, cmd := m.Update(connectOAuthPollMsg{seq: seq}); cmd != nil {
		t.Error("a stale poll re-armed the chain")
	}
	if m.connectOAuthState != connectOAuthIdle {
		t.Errorf("a stale poll changed state to %d", m.connectOAuthState)
	}
}

// TestConnectOAuthFlow_ResetModalCancelsLiveFlow pins the modal_state.go
// contract: reopening ANY modal while a provider flow is live must cancel it,
// because it owns a loopback listener until released.
func TestConnectOAuthFlow_ResetModalCancelsLiveFlow(t *testing.T) {
	m, flow := connectOAuthEnterPending(t, "anthropic")
	m.resetModal(ModalCommands)
	if flow.cancelCall == 0 {
		t.Fatal("resetModal did not cancel the live provider flow (listener leaked)")
	}
	if m.connectOAuthFlow != nil || m.connectOAuthState != connectOAuthIdle {
		t.Errorf("resetModal left flow=%v state=%d, want nil/idle", m.connectOAuthFlow, m.connectOAuthState)
	}
}

// ── 7. i18n: every referenced key resolves in every locale ────────────

func TestConnectOAuthKeysResolveInEveryLocale(t *testing.T) {
	keys := []string{
		"tui.connectOAuthTitle", "tui.connectOAuthStarting", "tui.connectOAuthURLLabel",
		"tui.connectOAuthInstructions", "tui.connectOAuthPlaceholder", "tui.connectOAuthSuccess",
		"tui.connectOAuthSaveFailed", "tui.connectOAuthStartFailed", "tui.connectOAuthSubmitFailed",
		"tui.connectOAuthFailed", "tui.connectOAuthHintEnter", "tui.connectOAuthHintCopy",
		"tui.connectOAuthHintClose", "tui.connectOAuthHintClosed", "tui.connectOAuthCopied",
		"tui.connectOAuthCopyFailed", "tui.connectOAuthEmptyInput", "tui.connectOAuthErrCanceled",
		"tui.connectOAuthErrTimedOut", "tui.connectOAuthErrClosed", "tui.connectOAuthErrNoManualRedirect",
		"tui.connectOAuthErrStateMismatch",
	}
	prev := i18n.GetLanguage()
	t.Cleanup(func() { i18n.SetLanguage(prev) })
	for _, lang := range []string{"en", "es", "pt"} {
		i18n.SetLanguage(lang)
		for _, k := range keys {
			if got := i18n.T(k); got == "" || strings.HasPrefix(got, "missing") {
				t.Errorf("lang=%s key=%q unresolved: %q", lang, k, got)
			}
		}
	}
}

// ── local helpers ─────────────────────────────────────────────────────

// authGetCredential reads back a stored credential through pkg/auth (the
// store lives under LELE_CONFIG_DIR, which newTestModel points at a temp dir).
func authGetCredential(t *testing.T, provider string) *auth.AuthCredential {
	t.Helper()
	cred, err := auth.GetCredential(provider)
	if err != nil {
		t.Fatalf("auth.GetCredential(%q): %v", provider, err)
	}
	return cred
}

// connectOAuthSuccessText renders the localized success feedback the modal
// shows after a completed flow (kept in one place so the assertion matches
// the format string exactly).
func connectOAuthSuccessText(name string) string {
	return fmt.Sprintf(i18n.T("tui.connectOAuthSuccess"), name)
}

// ── render budget: the URL is clamped, never the controls ─────────────

// connectOAuthLongURL is a realistic Google authorize URL length (501 cells):
// the modal hard-wraps it, so at a compact frame it occupies ~10 body lines.
func connectOAuthLongURL() string {
	base := "https://accounts.google.com/o/oauth2/v2/auth?client_id=681255809395-oo8ft2opchk07e0usl4rmbsgv8ek0q3l.apps.googleusercontent.com&redirect_uri=http%3A%2F%2F127.0.0.1%3A42317%2Fauth%2Fcallback&response_type=code&scope=openid&state="
	for len(base) < 501 {
		base += "x"
	}
	return base[:501]
}

// flatRender collapses the rendered box into a single-spaced string: the modal
// hard-wraps the URL and word-wraps the sentences, so containment must be
// checked on the text as it reads, not on the wrapped lines.
func flatRender(s string) string {
	// Drop the box drawing first: the borders sit between wrapped URL pieces
	// and would otherwise split them.
	return strings.Join(strings.Fields(stripBoxDrawing(s)), " ")
}

// squashRender removes ALL whitespace as well, so a hard-wrapped URL (which is
// split mid-token with no space to preserve) can be matched whole.
func squashRender(s string) string {
	return strings.Join(strings.Fields(stripBoxDrawing(s)), "")
}

func stripBoxDrawing(s string) string {
	return strings.NewReplacer("│", " ", "─", "", "┌", "", "┐", "", "└", "", "┘", "").Replace(s)
}

// TestConnectOAuthRender_CollapsedFrameKeepsTheControls is the pin for the
// render budget: a wrapped authorization URL must never push the paste box or
// the ESC hint off the modal, because those are the only way to finish or
// leave a pending flow (CTRL+Y — the only way to recover a clipped URL — is
// the hint that used to disappear first).
func TestConnectOAuthRender_CollapsedFrameKeepsTheControls(t *testing.T) {
	m, _ := connectOAuthEnterPending(t, "gemini")
	m.connectOAuthURL = connectOAuthLongURL()

	frames := []struct {
		width, height    int
		wantInstructions bool // optional line: must survive while it fits
		wantFullURL      bool
	}{
		{140, 40, true, true},
		{100, 30, true, true},
		{80, 24, true, true},
		{80, 20, false, true},
		{60, 15, false, false},
	}
	for _, f := range frames {
		t.Run(fmt.Sprintf("%dx%d", f.width, f.height), func(t *testing.T) {
			m.width, m.height = f.width, f.height
			out := ansi.Strip(m.renderConnectOAuth())
			flat := flatRender(out)

			if strings.TrimSpace(out) == "" {
				t.Fatalf("%dx%d renders an empty modal", f.width, f.height)
			}
			// Controls: always present.
			if !strings.Contains(flat, i18n.T("tui.connectOAuthTitle")) {
				t.Errorf("%dx%d lost the title:\n%s", f.width, f.height, out)
			}
			if !strings.Contains(flat, i18n.T("tui.connectOAuthHintClose")) {
				t.Errorf("%dx%d lost the ESC hint:\n%s", f.width, f.height, out)
			}
			if !strings.Contains(flat, i18n.T("tui.connectOAuthPlaceholder")) {
				t.Errorf("%dx%d lost the paste box:\n%s", f.width, f.height, out)
			}
			// URL: either shown whole, or clipped WITH the recovery marker.
			shownWhole := strings.Contains(squashRender(out), connectOAuthLongURL())
			clipped := strings.Contains(flat, i18n.T("tui.connectOAuthURLTruncated"))
			if shownWhole == clipped {
				t.Errorf("%dx%d must either show the full URL or mark it clipped (whole=%v clipped=%v):\n%s",
					f.width, f.height, shownWhole, clipped, out)
			}
			if shownWhole != f.wantFullURL {
				t.Errorf("%dx%d full URL shown = %v, want %v:\n%s", f.width, f.height, shownWhole, f.wantFullURL, out)
			}
			if clipped && !strings.Contains(flat, "CTRL+Y") {
				t.Errorf("%dx%d clipped the URL without naming the recovery key:\n%s", f.width, f.height, out)
			}
			gotInstructions := strings.Contains(flat, i18n.T("tui.connectOAuthInstructions"))
			if f.wantInstructions && !gotInstructions {
				t.Errorf("%dx%d dropped the instructions although they fit:\n%s", f.width, f.height, out)
			}
			// Never exceed the frame height (border + body).
			if lines := strings.Count(out, "\n") + 1; lines > f.height {
				t.Errorf("%dx%d rendered %d lines, more than the frame", f.width, f.height, lines)
			}
		})
	}
}

// TestConnectOAuthRender_BudgetSpendsOnTheURLNotTheTail is the differential pin
// against the body-level clamp this modal used to have: at 80x20 the URL still
// fits once the optional lines go (so the box shows it WHOLE and drops the
// instructions sentence), and at 60x15 the URL is what gets clipped — with the
// recovery marker — while the paste box and the ESC hint survive. The old
// clamp did the opposite at 80x20: it kept the URL and cut the paste box and
// every hint.
func TestConnectOAuthRender_BudgetSpendsOnTheURLNotTheTail(t *testing.T) {
	m, _ := connectOAuthEnterPending(t, "openai")
	m.connectOAuthURL = connectOAuthLongURL()

	m.width, m.height = 80, 20
	out := ansi.Strip(m.renderConnectOAuth())
	flat := flatRender(out)
	if !strings.Contains(squashRender(out), connectOAuthLongURL()) {
		t.Errorf("80x20 should still show the whole URL once the optional lines are dropped:\n%s", out)
	}
	if strings.Contains(flat, i18n.T("tui.connectOAuthInstructions")) {
		t.Errorf("80x20 should have dropped the instructions sentence:\n%s", out)
	}
	if !strings.Contains(flat, i18n.T("tui.connectOAuthPlaceholder")) ||
		!strings.Contains(flat, i18n.T("tui.connectOAuthHintClose")) {
		t.Errorf("80x20 lost the paste box or the ESC hint:\n%s", out)
	}

	m.width, m.height = 60, 15
	out = ansi.Strip(m.renderConnectOAuth())
	flat = flatRender(out)
	if !strings.Contains(flat, i18n.T("tui.connectOAuthURLTruncated")) {
		t.Errorf("a 501-cell URL at 60x15 must be clipped and say so:\n%s", out)
	}
	if !strings.Contains(flat, "CTRL+Y") {
		t.Errorf("the clip marker must name the recovery key:\n%s", out)
	}
	if !strings.Contains(flat, i18n.T("tui.connectOAuthPlaceholder")) {
		t.Errorf("60x15 lost the paste box:\n%s", out)
	}
	if !strings.Contains(flat, i18n.T("tui.connectOAuthHintClose")) {
		t.Errorf("60x15 lost the ESC hint:\n%s", out)
	}
}

// TestConnectOAuthCatalogIsRegistered guards the seam between the /connect row
// catalogue and the OAuth engine registry: every catalog entry's canonical
// name must resolve in pkg/auth, because that is the name the modal logs in
// to. A drift (registry renamed, catalog not) is otherwise only visible as a
// "provider is not registered" failure at login time — i.e. the row would be
// offered and then refuse to work, with no test red.
func TestConnectOAuthCatalogIsRegistered(t *testing.T) {
	if len(connectOAuthCatalog) == 0 {
		t.Fatal("connectOAuthCatalog is empty")
	}
	// Pin the SET, not only its contents: deleting a catalog entry silently
	// removes that provider's OAuth row (its API-key row stays), and every
	// other test keeps passing — the row would simply no longer be offered.
	wantSet := []string{"anthropic", "gemini", "openai"}
	gotSet := make([]string, 0, len(connectOAuthCatalog))
	for typ := range connectOAuthCatalog {
		gotSet = append(gotSet, typ)
	}
	sort.Strings(gotSet)
	if !slices.Equal(gotSet, wantSet) {
		t.Errorf("connectOAuthCatalog keys = %v, want %v", gotSet, wantSet)
	}
	for typ, meta := range connectOAuthCatalog {
		if meta.provider == "" {
			t.Errorf("catalog entry %q has an empty provider name", typ)
			continue
		}
		if _, ok := auth.OAuthProviderConfigByName(meta.provider); !ok {
			t.Errorf("catalog entry %q wants provider %q, which is not registered in pkg/auth",
				typ, meta.provider)
		}
		// The route persists the provider under these names, so they must be
		// the same key the row and the credential store use.
		if meta.configName != meta.provider || meta.configType != meta.provider {
			t.Errorf("catalog entry %q name/type mismatch: provider=%q configName=%q configType=%q",
				typ, meta.provider, meta.configName, meta.configType)
		}
		if meta.authMethod != "oauth" {
			t.Errorf("catalog entry %q auth_method = %q, want %q (the transport is selected by it)",
				typ, meta.authMethod, "oauth")
		}
	}
}
