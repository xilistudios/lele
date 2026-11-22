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

const (
	connectOAuthIdle = iota
	connectOAuthStarting
	connectOAuthPending
	connectOAuthSucceeded
	connectOAuthFailed
)

const (
	connectOAuthStatusPending = "pending"
	connectOAuthStatusDone    = "done"
	connectOAuthStatusError   = "error"
)

const connectOAuthPollInterval = time.Second

const connectOAuthModalOverhead = 4

type connectOAuthStartMsg struct {
	seq  int
	name string
	flow connectOAuthFlow
	err  error
}

type connectOAuthPollMsg struct{ seq int }

type connectOAuthSubmitMsg struct {
	seq  int
	name string
	err  error
}

type connectOAuthFlow interface {
	URL() string
	Status() (string, error)
	Done() bool
	Credential() *auth.AuthCredential
	Submit(input string) error
	Cancel() error
}

var startConnectOAuthFlow = func(ctx context.Context, cfg auth.OAuthProviderConfig, opts auth.FlowOptions) (connectOAuthFlow, error) {
	return auth.StartProviderFlow(ctx, cfg, opts)
}

func (m *Model) connectOAuthStartFromRow(row connectRow) tea.Cmd {
	if row.kind != connectRowOAuth {
		return nil
	}
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
	m.textInput.SetValue("")
	m.textInput.Placeholder = i18n.T("tui.connectOAuthPlaceholder")
	m.syncTextInputEcho()
	m.textInput.Focus()
	m.modalMode = ModalConnectOAuth
	return m.connectOAuthStartCmd(cfg)
}

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

func (m *Model) connectOAuthSubmitCmd(input string) tea.Cmd {
	flow, seq, name := m.connectOAuthFlow, m.connectOAuthSeq, m.connectOAuthName
	return func() tea.Msg {
		return connectOAuthSubmitMsg{seq: seq, name: name, err: flow.Submit(input)}
	}
}

func (m *Model) connectOAuthPollCmd() tea.Cmd {
	seq := m.connectOAuthSeq
	return tea.Tick(connectOAuthPollInterval, func(time.Time) tea.Msg {
		return connectOAuthPollMsg{seq: seq}
	})
}

func (m *Model) handleConnectOAuthStart(msg connectOAuthStartMsg) tea.Cmd {
	if msg.seq != m.connectOAuthSeq || m.modalMode != ModalConnectOAuth ||
		m.connectOAuthState != connectOAuthStarting {
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

func (m *Model) connectOAuthFinishDone() bool {
	if m.connectOAuthState != connectOAuthPending || m.connectOAuthFlow == nil {
		return false
	}
	cred := m.connectOAuthFlow.Credential()
	if cred == nil {
		m.connectOAuthFlow.Cancel()
		m.connectOAuthFlow = nil
		m.connectOAuthState = connectOAuthFailed
		m.connectOAuthError = i18n.T("tui.connectOAuthFailed")
		return false
	}
	if err := auth.SetCredential(m.connectOAuthName, cred); err != nil {
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
		m.connectOAuthFlow.Cancel()
		m.connectOAuthFlow = nil
		m.connectOAuthState = connectOAuthFailed
		m.connectOAuthError = fmt.Sprintf(i18n.T("tui.connectOAuthSaveFailed"), err)
		return false
	}

	m.connectOAuthState = connectOAuthSucceeded
	m.connectOAuthFlow.Cancel()
	m.connectOAuthFlow = nil
	m.connectOAuthFeedback = fmt.Sprintf(i18n.T("tui.connectOAuthSuccess"), m.connectOAuthName)
	m.enterConnectOAuthModelWizard(meta)
	return true
}

func (m *Model) oauthRowType(name string) string {
	for typ, meta := range connectOAuthCatalog {
		if meta.provider == name {
			return typ
		}
	}
	return ""
}

func connectOAuthMetaByName(name string) connectOAuthRowMeta {
	return connectOAuthRowMeta{
		provider:   name,
		configType: name,
		configName: name,
		authMethod: "oauth",
	}
}

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
	m.cfg.Providers.Named[key] = entry

	switch key {
	case "openai", "anthropic", "gemini":
		setProviderTypedAuthMethod(m.cfg.Providers, key, meta.authMethod,
			meta.requiresEmptyAPIKey)
	}
	return m.saveConfigToDisk()
}

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

func (m *Model) enterConnectOAuthModelWizard(meta connectOAuthRowMeta) {
	m.modalMode = ModalAddProvider
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

func (m *Model) connectOAuthFail(text string) {
	m.formError = text
}

func (m *Model) connectOAuthStartUnavailable(reasonKey string) tea.Cmd {
	m.connectOAuthFail(fmt.Sprintf(i18n.T("tui.connectOAuthStartFailed"), i18n.T(reasonKey)))
	return nil
}

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

var connectOAuthErrorStyle = lipgloss.NewStyle().Foreground(PrimaryColor)

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
