package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// This file is the TUI face of pkg/auth's non-blocking OAuth 2.0 provider
// flow (pkg/auth/oauth_flow.go). A native /connect OAuth row starts
// auth.StartProviderFlow, displays the authorization URL, polls the flow to
// completion and accepts a pasted callback/code. It deliberately mirrors the
// proven ModalMCPAuth state machine and rendering while remaining a separate
// engine seam: the MCP flow submits (code, state), whereas the provider flow
// owns its state and accepts one complete input string.
//
// The same three invariants apply:
//
//  1. Update never blocks: only ProviderFlow.URL/Status/Credential and
//     Cancel run here. Submit performs the token exchange and therefore runs
//     inside the returned tea.Cmd.
//  2. Nothing is orphaned: the flow owns a loopback listener in loopback
//     mode, so every exit cancels it. A late message of an abandoned attempt
//     cancels its flow instead of resurrecting the modal.
//  3. No credential-shaped input is retained: the pasted callback/code is
//     cleared from the shared input as soon as it has been handed to Submit.

// The ModalConnectOAuth states mirror the engine's observable lifecycle.
const (
	connectOAuthIdle = iota
	connectOAuthStarting
	connectOAuthPending
	connectOAuthSucceeded
	connectOAuthFailed
)

// The engine status strings. pkg/auth exports these constants, but spelling
// the comparison locally keeps the handler compatible with the same textual
// contract as MCP flows.
const (
	connectOAuthStatusPending = "pending"
	connectOAuthStatusDone    = "done"
	connectOAuthStatusError   = "error"
)

// connectOAuthPollInterval is how often a pending provider flow is checked.
const connectOAuthPollInterval = time.Second

// connectOAuthModalOverhead is the border and vertical padding ModalContainer
// adds around the modal body.
const connectOAuthModalOverhead = 4

// ── Async messages ─────────────────────────────────────────────────────

// connectOAuthStartMsg is the result of one start attempt. flow is nil on
// error; err is kept value-free where possible and translated in Update.
type connectOAuthStartMsg struct {
	seq  int
	name string
	flow connectOAuthFlow
	err  error
}

// connectOAuthPollMsg is one poll tick of a live provider flow.
type connectOAuthPollMsg struct{ seq int }

// connectOAuthSubmitMsg is the result of a pasted-code token exchange.
type connectOAuthSubmitMsg struct {
	seq  int
	name string
	err  error
}

// ── Engine seam ────────────────────────────────────────────────────────

// connectOAuthFlow is the subset of *auth.ProviderFlow this modal drives.
// Unlike the MCP flow, Submit takes the whole pasted input (code, callback
// URL or code#state) because the provider engine owns its state grammar.
//
// Credential() is deliberately part of the interface: a successful poll must
// retrieve the concrete token in the Update handler before the flow can be
// released, and the returned pointer is stable per the engine contract.
type connectOAuthFlow interface {
	URL() string
	Status() (string, error)
	Done() bool
	Credential() *auth.AuthCredential
	Submit(input string) error
	Cancel() error
}

// startConnectOAuthFlow is substituted by unit tests so the modal can be
// driven without a browser, listener, network or credential store.
var startConnectOAuthFlow = func(ctx context.Context, cfg auth.OAuthProviderConfig, opts auth.FlowOptions) (connectOAuthFlow, error) {
	return auth.StartProviderFlow(ctx, cfg, opts)
}

// ── Entry point: OAuth row in /connect ─────────────────────────────────

// connectOAuthStartFromRow starts the selected provider OAuth row. The
// registry config is resolved in Update (it is local and cheap); only the
// flow's synchronous setup runs inside the returned command. Manual fallback
// is intentionally not selected automatically: this modal always binds a
// loopback callback and shows that URL, and a provider preset that lacks the
// hosted redirect reports that at start rather than silently changing modes.
func (m *Model) connectOAuthStartFromRow(row connectRow) tea.Cmd {
	if row.kind != connectRowOAuth {
		return nil
	}
	// The row already carries the canonical registry name; the catalog is
	// consulted for the PERSISTENCE metadata (config name/type, auth method)
	// and, crucially, to reject a row whose provider has no catalog entry.
	meta, ok := connectOAuthCatalog[row.typ]
	if !ok {
		return m.connectOAuthStartUnavailable("tui.connectOAuthErrUnknownProvider")
	}
	if row.oauthName == "" || row.oauthName != meta.provider {
		return m.connectOAuthStartUnavailable("tui.connectOAuthErrInconsistentRow")
	}
	cfg, ok := auth.OAuthProviderConfigByName(row.oauthName)
	if !ok {
		return m.connectOAuthStartUnavailable("tui.connectOAuthErrUnregisteredProvider")
	}

	m.connectOAuthSeq++
	m.connectOAuthName = cfg.Name
	m.connectOAuthState = connectOAuthStarting
	m.connectOAuthFlow = nil
	m.connectOAuthURL = ""
	m.connectOAuthFeedback = ""
	m.connectOAuthError = ""
	// The shared input is the paste area for this flow too. Focus is
	// load-bearing: textinput.Update drops every key while blurred.
	m.textInput.SetValue("")
	m.textInput.Placeholder = i18n.T("tui.connectOAuthPlaceholder")
	m.syncTextInputEcho()
	m.textInput.Focus()
	m.modalMode = ModalConnectOAuth
	return m.connectOAuthStartCmd(cfg)
}

// connectOAuthStartCmd starts the engine flow inside a command. The config is
// copied by value; no Model field is read from the goroutine.
func (m *Model) connectOAuthStartCmd(cfg auth.OAuthProviderConfig) tea.Cmd {
	seq := m.connectOAuthSeq
	return func() tea.Msg {
		flow, err := startConnectOAuthFlow(context.Background(), cfg, auth.FlowOptions{Mode: auth.FlowModeLoopback})
		if err != nil {
			return connectOAuthStartMsg{seq: seq, name: cfg.Name, err: err}
		}
		return connectOAuthStartMsg{seq: seq, name: cfg.Name, flow: flow}
	}
}

// connectOAuthSubmitCmd performs the synchronous token exchange inside a
// command. The input travels as an argument and never returns in a message.
func (m *Model) connectOAuthSubmitCmd(input string) tea.Cmd {
	flow, seq, name := m.connectOAuthFlow, m.connectOAuthSeq, m.connectOAuthName
	return func() tea.Msg {
		return connectOAuthSubmitMsg{seq: seq, name: name, err: flow.Submit(input)}
	}
}

// connectOAuthPollCmd schedules the next status check.
func (m *Model) connectOAuthPollCmd() tea.Cmd {
	seq := m.connectOAuthSeq
	return tea.Tick(connectOAuthPollInterval, func(time.Time) tea.Msg {
		return connectOAuthPollMsg{seq: seq}
	})
}

// ── Message handlers ───────────────────────────────────────────────────

func (m *Model) handleConnectOAuthStart(msg connectOAuthStartMsg) tea.Cmd {
	if msg.seq != m.connectOAuthSeq || m.modalMode != ModalConnectOAuth ||
		m.connectOAuthState != connectOAuthStarting {
		// A flow that arrives after ESC/reset belongs to an abandoned
		// attempt: release its listener and never adopt it.
		if msg.flow != nil {
			_ = msg.flow.Cancel()
		}
		return nil
	}
	if msg.err != nil {
		m.connectOAuthState = connectOAuthFailed
		m.connectOAuthError = connectOAuthStartErrorText(msg.name, msg.err)
		return nil
	}
	m.connectOAuthFlow = msg.flow
	m.connectOAuthURL = msg.flow.URL()
	m.connectOAuthState = connectOAuthPending
	m.connectOAuthFeedback = ""
	m.connectOAuthError = ""
	return m.connectOAuthPollCmd()
}

// handleConnectOAuthPoll checks a pending flow. A terminal error is shown
// without leaking provider token material; an exchange failure is rendered
// through its value-free error text.
func (m *Model) handleConnectOAuthPoll(msg connectOAuthPollMsg) tea.Cmd {
	if msg.seq != m.connectOAuthSeq || m.modalMode != ModalConnectOAuth ||
		m.connectOAuthState != connectOAuthPending || m.connectOAuthFlow == nil {
		return nil
	}
	status, err := m.connectOAuthFlow.Status()
	switch status {
	case connectOAuthStatusDone:
		m.connectOAuthFinishDone()
		return nil
	case connectOAuthStatusError:
		if m.connectOAuthFlow.Done() {
			m.connectOAuthState = connectOAuthFailed
			m.connectOAuthError = connectOAuthTerminalErrorText(err)
			return nil
		}
		m.connectOAuthError = connectOAuthSubmitErrorText(err)
	}
	return m.connectOAuthPollCmd()
}

// handleConnectOAuthSubmit applies a manual completion. A failed exchange
// leaves the flow open whenever the engine does so, so ENTER remains a retry.
func (m *Model) handleConnectOAuthSubmit(msg connectOAuthSubmitMsg) tea.Cmd {
	if msg.seq != m.connectOAuthSeq || m.modalMode != ModalConnectOAuth {
		return nil
	}
	if msg.err != nil {
		m.connectOAuthError = connectOAuthSubmitErrorText(msg.err)
		return nil
	}
	m.connectOAuthFinishDone()
	return nil
}

// connectOAuthFinishDone performs the one-shot success transition: retrieve
// the credential from the still-live flow, persist it under the canonical
// provider key, write auth_method where the provider transport needs it, and
// hand the user the existing model wizard so the subscription has a model.
//
// The one-shot guard matters because a loopback callback and a pasted Enter
// can both observe the same completed generation.
func (m *Model) connectOAuthFinishDone() bool {
	if m.connectOAuthState != connectOAuthPending || m.connectOAuthFlow == nil {
		return false
	}
	cred := m.connectOAuthFlow.Credential()
	if cred == nil {
		// Status said done but the engine returned no credential: treat as a
		// terminal failure rather than persisting an unusable provider.
		// The flow owns a loopback callback listener until cancelled, and a
		// Failed state short-circuits ENTER/poll, so it MUST be released
		// here: otherwise the port (e.g. OpenAI's 1455) stays bound until the
		// user presses ESC or another modal replaces this one, and the next
		// login cannot bind it.
		m.connectOAuthFlow.Cancel()
		m.connectOAuthFlow = nil
		m.connectOAuthState = connectOAuthFailed
		m.connectOAuthError = i18n.T("tui.connectOAuthFailed")
		return false
	}
	if err := auth.SetCredential(m.connectOAuthName, cred); err != nil {
		// Same release rule as the nil-credential branch above: terminal
		// failure ⇒ the callback listener must not survive it.
		m.connectOAuthFlow.Cancel()
		m.connectOAuthFlow = nil
		m.connectOAuthState = connectOAuthFailed
		m.connectOAuthError = fmt.Sprintf(i18n.T("tui.connectOAuthSaveFailed"), err)
		return false
	}
	meta, ok := connectOAuthCatalog[m.oauthRowType(m.connectOAuthName)]
	if !ok {
		meta = connectOAuthMetaByName(m.connectOAuthName)
	}
	if err := m.connectOAuthPersistProvider(meta); err != nil {
		// The credential is already stored and remains usable through the
		// CLI, but the model wizard cannot be entered without a named
		// provider, so this is a terminal failure with an actionable error.
		// Release the flow too: nothing more can be done in this modal.
		m.connectOAuthFlow.Cancel()
		m.connectOAuthFlow = nil
		m.connectOAuthState = connectOAuthFailed
		m.connectOAuthError = fmt.Sprintf(i18n.T("tui.connectOAuthSaveFailed"), err)
		return false
	}

	m.connectOAuthState = connectOAuthSucceeded
	m.connectOAuthFlow.Cancel() // release the callback port now that it is no longer needed
	m.connectOAuthFlow = nil
	m.connectOAuthFeedback = fmt.Sprintf(i18n.T("tui.connectOAuthSuccess"), m.connectOAuthName)
	// Route into the ordinary model wizard. The provider entry was already
	// saved, so the existing "already usable" rules keep models untouched
	// when this is a re-login; a fresh login proceeds to model alias step.
	m.enterConnectOAuthModelWizard(meta)
	return true
}

// oauthRowType resolves the /connect row type for a canonical OAuth name.
func (m *Model) oauthRowType(name string) string {
	for typ, meta := range connectOAuthCatalog {
		if meta.provider == name {
			return typ
		}
	}
	return ""
}

// connectOAuthMetaByName is a fallback for a canonical provider name that has
// no providerPreset row. It constructs just enough metadata to persist the
// credential and add a named provider; no API base or model hint exists.
func connectOAuthMetaByName(name string) connectOAuthRowMeta {
	return connectOAuthRowMeta{
		provider:   name,
		configType: name,
		configName: name,
		authMethod: "oauth",
	}
}

// connectOAuthPersistProvider creates/updates the named provider entry and
// writes the typed auth_method where one exists. API-key-only persistence is
// deliberately not used: this flow is not a developer API key, and the named
// entry with auth_method is sufficient for pkg/providers to select the OAuth
// transport.
func (m *Model) connectOAuthPersistProvider(meta connectOAuthRowMeta) error {
	if m.cfg == nil {
		return errors.New("config is nil")
	}
	if m.cfg.Providers == nil {
		m.cfg.Providers = &config.ProvidersConfig{}
	}
	if m.cfg.Providers.Named == nil {
		m.cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	key := strings.ToLower(strings.TrimSpace(meta.configName))
	if key == "" {
		key = strings.ToLower(strings.TrimSpace(meta.provider))
	}
	if key == "" {
		return errors.New("provider name cannot be empty")
	}

	// Existing API-key provider: keep its models, proxy and WebSearch, but
	// switch its credential source to OAuth.
	//
	// The API key is cleared ONLY when the catalog entry declares
	// requiresEmptyAPIKey (today: gemini). pkg/providers requires
	// auth_method == "oauth" AND an empty API key for the Gemini Code Assist
	// transport (factory.go: the typed Providers.Gemini pre-check
	// `Gemini.AuthMethod == "oauth" && Gemini.APIKey == ""`, and the
	// Named-entry case `named.AuthMethod == "oauth" && sel.apiKey == ""`),
	// so a stale key MUST be gone there. For openai (Codex) and anthropic
	// (Claude subscription) the transport is selected from auth_method
	// ALONE — factory.go returns providerTypeCodexAuth /
	// providerTypeClaudeAuth on `auth_method == "oauth" || "token"` with no
	// api-key condition — so clearing a developer key would be pure data
	// loss (after logout the user's key would be gone).
	entry, exists := m.cfg.Providers.Named[key]
	if !exists || entry.IsEmpty() {
		entry = config.NamedProviderConfig{Type: meta.configType}
	}
	if entry.Type == "" {
		entry.Type = meta.configType
	}
	if meta.requiresEmptyAPIKey {
		entry.APIKey = ""
	}
	entry.AuthMethod = meta.authMethod
	// The account email lives on the stored AuthCredential (SetCredential
	// persists it, and pkg/providers reads it back from there). There is no
	// Email field on the config entry to mirror it into.
	m.cfg.Providers.Named[key] = entry

	// Mirror the typed provider where one exists. It is needed because
	// ProvidersConfig.MarshalJSON early-returns to the named copy whenever a
	// named entry carries data; without the mirror the typed field could be
	// reloaded from stale state in config paths that do not round-trip the
	// named map. For google-antigravity there is no typed field.
	switch key {
	case "openai", "anthropic", "gemini":
		// When the API key must go, BOTH copies must lose it: they are the
		// same provider, so a stale key left in the typed field would make the
		// two copies disagree — and Providers.Gemini is what factory.go's
		// FIRST-evaluated pre-check (auth_method == "oauth" && APIKey == "")
		// reads, so an obsolete key there fails that condition and the
		// transport silently falls back to the Named-entry path. No-op for
		// openai/anthropic, which have no requiresEmptyAPIKey entry today.
		setProviderTypedAuthMethod(m.cfg.Providers, key, meta.authMethod,
			meta.requiresEmptyAPIKey /* clearAPIKey */)
	}
	return m.saveConfigToDisk()
}

// connectOAuthModelPrefill is the model metadata the wizard prefills after a
// successful OAuth login: the providerPreset values overridden by the catalog
// entry when it declares any (an OAuth transport whose model space differs
// from the developer-API one, e.g. openai → Codex / gpt-5.2). Empty fields
// fall back to the preset, so providers without overrides keep them
// byte-identically.
func connectOAuthModelPrefill(meta connectOAuthRowMeta) (modelHint, defaultModelAlias, defaultModel string) {
	modelHint, defaultModelAlias, defaultModel = "", "", ""
	if p := providerPresetByType(meta.configType); p != nil {
		modelHint, defaultModelAlias, defaultModel = p.modelHint, p.defaultModelAlias, p.defaultModel
	}
	if meta.modelHint != "" {
		modelHint = meta.modelHint
	}
	if meta.defaultModelAlias != "" {
		defaultModelAlias = meta.defaultModelAlias
	}
	if meta.defaultModel != "" {
		defaultModel = meta.defaultModel
	}
	return modelHint, defaultModelAlias, defaultModel
}

// enterConnectOAuthModelWizard starts the existing model steps for the named
// provider. The prefill comes from the matching providerPreset row when one
// exists, so the user can continue with the familiar Enter-through flow.
func (m *Model) enterConnectOAuthModelWizard(meta connectOAuthRowMeta) {
	m.modalMode = ModalAddProvider
	// The success feedback is set by connectOAuthFinishDone just before this
	// hand-off; clearing the auth fields below would wipe it, so hold it and
	// re-apply it so the user still sees the "signed in as X" confirmation on
	// the first wizard frame.
	successFeedback := m.connectOAuthFeedback

	m.connectOAuthClearFields()
	m.providerSavedInFlow = true
	m.connectAlreadyUsable = len(m.listProviderModels(meta.configName)) > 0
	m.providerSelectedName = meta.configName
	m.connectRowIdx = -1
	m.connectRowPicker = false
	m.providerTypePicker = false
	m.formStepIndex = 4
	m.formValues = make([]string, 11)
	m.formValues[0] = meta.configName
	m.formValues[1] = meta.configType
	modelHint, defaultModelAlias, defaultModel := connectOAuthModelPrefill(meta)
	if p := providerPresetByType(meta.configType); p != nil {
		m.formValues[3] = p.apiBase
		m.formValues[6] = "128000"
		m.formValues[7] = "4096"
	}
	m.formValues[4] = defaultModelAlias
	m.formValues[5] = defaultModel
	m.textInput.SetValue(m.formValues[4])
	m.textInput.Placeholder = "Model alias (e.g. gpt-4o)"
	if modelHint != "" {
		m.textInput.Placeholder = "Model alias (" + modelHint + ")"
	}
	m.syncTextInputEcho()
	m.connectOAuthFeedback = successFeedback
}

// connectOAuthFail renders a pre-flow failure (unknown row or registry name)
// without starting a modal. It is used only from the entry point.
func (m *Model) connectOAuthFail(text string) {
	m.formError = text
}

// connectOAuthStartUnavailable reports a row the flow cannot be started for.
// reasonKey is an i18n KEY for the reason, not a rendered string: it is
// substituted into the tui.connectOAuthStartFailed template, which carries the
// %v placeholder (the same template connectOAuthStartErrorText uses for engine
// errors). Gluing a hard-coded reason onto the translated template would both
// print the literal %v and leave the sentence half-translated.
func (m *Model) connectOAuthStartUnavailable(reasonKey string) tea.Cmd {
	m.connectOAuthFail(fmt.Sprintf(i18n.T("tui.connectOAuthStartFailed"), i18n.T(reasonKey)))
	return nil
}

// setProviderTypedAuthMethod writes auth_method into the typed provider copy.
// It mirrors cmd/lele's setNamedAuthMethod split: the named copy is what gets
// serialized once it has data, while the typed copy is what some load paths
// still seed from.
//
// clearAPIKey also wipes api_key in the same typed copy. The two copies are
// one provider, so when requiresEmptyAPIKey demands a keyless entry they must
// agree: Providers.Gemini is what factory.go's FIRST-evaluated pre-check
// (auth_method == "oauth" && APIKey == "") reads, so a stale key left in the
// typed field fails that condition and the transport would have to rely on the
// Named-entry case instead. Callers that must keep the developer key (openai,
// anthropic) pass false.
func setProviderTypedAuthMethod(p *config.ProvidersConfig, name, method string, clearAPIKey bool) {
	if p == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "openai":
		p.OpenAI.AuthMethod = method
		if clearAPIKey {
			p.OpenAI.APIKey = ""
		}
	case "anthropic":
		p.Anthropic.AuthMethod = method
		if clearAPIKey {
			p.Anthropic.APIKey = ""
		}
	case "gemini":
		p.Gemini.AuthMethod = method
		if clearAPIKey {
			p.Gemini.APIKey = ""
		}
	}
}

// ── Error text (i18n happens only here) ───────────────────────────────

func connectOAuthSentinelText(err error) string {
	switch {
	case errors.Is(err, auth.ErrFlowCanceled):
		return i18n.T("tui.connectOAuthErrCanceled")
	case errors.Is(err, auth.ErrFlowTimeout):
		return i18n.T("tui.connectOAuthErrTimedOut")
	case errors.Is(err, auth.ErrFlowClosed):
		return i18n.T("tui.connectOAuthErrClosed")
	case errors.Is(err, auth.ErrFlowNoManualRedirect):
		return i18n.T("tui.connectOAuthErrNoManualRedirect")
	case errors.Is(err, auth.ErrFlowNoCode):
		return i18n.T("tui.connectOAuthEmptyInput")
	case errors.Is(err, auth.ErrFlowStateMismatch):
		return i18n.T("tui.connectOAuthErrStateMismatch")
	}
	return ""
}

func connectOAuthStartErrorText(name string, err error) string {
	if t := connectOAuthSentinelText(err); t != "" {
		return t
	}
	return fmt.Sprintf(i18n.T("tui.connectOAuthStartFailed"), err)
}

func connectOAuthSubmitErrorText(err error) string {
	if t := connectOAuthSentinelText(err); t != "" {
		return t
	}
	return fmt.Sprintf(i18n.T("tui.connectOAuthSubmitFailed"), err)
}

func connectOAuthTerminalErrorText(err error) string {
	if t := connectOAuthSentinelText(err); t != "" {
		return t
	}
	return i18n.T("tui.connectOAuthFailed")
}

// ── Keys ───────────────────────────────────────────────────────────────

// handleConnectOAuthKey owns ModalConnectOAuth. ENTER submits a pasted
// callback/code; CTRL+Y copies the authorization URL; ESC cancels the flow
// and returns to the /connect picker. Every other key is passed through so
// the shared input can receive a paste ("q" is a regular character).
func (m *Model) handleConnectOAuthKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		m.connectOAuthCancel()
		m.startConnectFlow(nil)
		return nil, true

	case "enter":
		if m.connectOAuthState != connectOAuthPending || m.connectOAuthFlow == nil {
			return nil, true
		}
		input := m.textInput.Value()
		// A callback/code is credential-shaped: clear it from the widget as
		// soon as it has been handed to the command. The engine parser keeps
		// it only as a function argument, never in a message.
		m.textInput.SetValue("")
		m.connectOAuthError = ""
		m.connectOAuthFeedback = ""
		return m.connectOAuthSubmitCmd(input), true

	case "ctrl+y":
		if m.connectOAuthURL == "" {
			m.connectOAuthError = i18n.T("tui.connectOAuthCopyFailed")
			return nil, true
		}
		copyToClipboard(m.connectOAuthURL)
		m.connectOAuthError = ""
		m.connectOAuthFeedback = i18n.T("tui.connectOAuthCopied")
		return nil, true
	}
	return nil, false
}

// connectOAuthCancel releases the flow and clears every auth field. It is the
// one release point (ESC and resetModal).
func (m *Model) connectOAuthCancel() {
	if m.connectOAuthFlow != nil {
		_ = m.connectOAuthFlow.Cancel()
		m.connectOAuthFlow = nil
	}
	m.connectOAuthSeq++
	if m.connectOAuthState != connectOAuthIdle {
		m.textInput.SetValue("")
		m.textInput.Placeholder = ""
		m.textInput.Blur()
	}
	m.connectOAuthClearFields()
}

func (m *Model) connectOAuthClearFields() {
	m.connectOAuthState = connectOAuthIdle
	m.connectOAuthName = ""
	m.connectOAuthURL = ""
	m.connectOAuthFeedback = ""
	m.connectOAuthError = ""
}

// ── Rendering ─────────────────────────────────────────────────────────

var connectOAuthErrorStyle = lipgloss.NewStyle().Foreground(PrimaryColor)

// connectOAuthURLWidth is the cell budget for a wrapped authorization URL in
// the modal box (same budget as the MCP flow).
func (m *Model) connectOAuthURLWidth() int {
	w := m.width - 16
	if w < 20 {
		w = 20
	}
	return w
}

func (m *Model) connectOAuthFeedbackLine() string {
	var sb strings.Builder
	if m.connectOAuthFeedback != "" {
		sb.WriteString(SuccessStyle.Render("  "+m.connectOAuthFeedback) + "\n")
	}
	if m.connectOAuthError != "" {
		sb.WriteString(connectOAuthErrorStyle.Render("  ✗ " + m.connectOAuthError))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// connectOAuthBody builds the modal body.
//
// urlBudget caps the wrapped authorization-URL lines (-1 = no cap) and, when
// it cuts, replaces the tail of the URL with a one-line marker so the user
// still learns how to recover the full URL. compact drops the optional lines
// (the instructions sentence and the ENTER/CTRL+Y hints): they are the first
// thing the height budget can afford to lose, because the paste box and the
// ESC hint are the only way out of a pending flow.
func (m *Model) connectOAuthBody(urlBudget int, compact bool) []string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(i18n.T("tui.connectOAuthTitle")) + "\n")
	sb.WriteString(TitleStyle.Render(m.connectOAuthName) + "\n\n")
	switch m.connectOAuthState {
	case connectOAuthStarting:
		sb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.connectOAuthStarting")) + "\n")
	case connectOAuthSucceeded, connectOAuthFailed:
		sb.WriteString(m.connectOAuthFeedbackLine() + "\n")
	default:
		sb.WriteString(connectOAuthErrorStyle.Render(i18n.T("tui.connectOAuthURLLabel")+":") + "\n")
		urlLines := hardWrapCells(m.connectOAuthURL, m.connectOAuthURLWidth())
		if urlBudget >= 0 && len(urlLines) > urlBudget {
			keep := urlBudget
			if keep > 1 {
				// One of the granted lines is the marker itself.
				keep--
			}
			urlLines = append(urlLines[:keep:keep], i18n.T("tui.connectOAuthURLTruncated"))
		}
		for _, line := range urlLines {
			sb.WriteString("  " + line + "\n")
		}
		if compact {
			sb.WriteString("\n")
		} else {
			sb.WriteString("\n" + i18n.T("tui.connectOAuthInstructions") + "\n\n")
		}
		sb.WriteString(m.textInputView() + "\n")
		if line := m.connectOAuthFeedbackLine(); line != "" {
			sb.WriteString("\n" + line + "\n")
		}
	}
	sb.WriteString("\n")
	if m.connectOAuthState == connectOAuthPending && !compact {
		sb.WriteString(HelpStyle.Render("  "+i18n.T("tui.connectOAuthHintEnter")) + "\n")
		sb.WriteString(HelpStyle.Render("  "+i18n.T("tui.connectOAuthHintCopy")) + "\n")
	}
	hint := i18n.T("tui.connectOAuthHintClose")
	if m.connectOAuthState == connectOAuthSucceeded || m.connectOAuthState == connectOAuthFailed {
		hint = i18n.T("tui.connectOAuthHintClosed")
	}
	sb.WriteString(HelpStyle.Render("  " + hint))
	return strings.Split(sb.String(), "\n")
}

// connectOAuthBox renders the modal box within the terminal height.
//
// The budget is spent on the wrapped authorization URL, NOT on the whole
// body: a realistic Google authorize URL is 300-500 cells (≈10 wrapped
// lines), so clamping the body from the top would push the paste box and
// every key hint off a compact terminal — losing the one affordance (CTRL+Y)
// that recovers the full URL. Order of surrender as the frame shrinks:
// optional lines → URL lines (down to one, plus the copy marker) → and only
// for a frame too small for even the controls, the head.
func (m *Model) connectOAuthBox() string {
	maxBody := m.height - connectOAuthModalOverhead
	if maxBody < 1 {
		maxBody = 1
	}
	lines := m.connectOAuthBody(-1, false)
	if len(lines) > maxBody {
		lines = m.connectOAuthBody(-1, true)
	}
	if len(lines) > maxBody {
		excess := len(lines) - maxBody
		budget := len(hardWrapCells(m.connectOAuthURL, m.connectOAuthURLWidth())) - excess
		if budget < 1 {
			budget = 1
		}
		lines = m.connectOAuthBody(budget, true)
	}
	if len(lines) > maxBody {
		// Extreme frame: the head (title, provider, URL) is the part that can
		// be rebuilt from context, so keep the tail with the controls.
		lines = lines[len(lines)-maxBody:]
	}
	width := m.width - 10
	if width < 20 {
		width = 20
	}
	return clampLineCount(ModalContainer.Width(width).Render(strings.Join(lines, "\n")), m.height)
}

func (m *Model) renderConnectOAuth() string {
	return m.paintFrame(m.connectOAuthBox())
}
