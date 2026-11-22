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
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

const (
	mcpAuthIdle int = iota
	mcpAuthStarting
	mcpAuthPending
	mcpAuthSucceeded
	mcpAuthFailed
)

const (
	mcpAuthStatusPending = "pending"
	mcpAuthStatusDone    = "done"
	mcpAuthStatusError   = "error"
)

const mcpAuthPollInterval = time.Second

const mcpAuthModalOverhead = 4

type mcpAuthStartMsg struct {
	seq       int
	name      string
	flow      mcpAuthFlow
	err       error
	openTried bool
	openErr   error
}

type mcpAuthPollMsg struct{ seq int }

type mcpAuthSubmitMsg struct {
	seq int
	err error
}

var (
	errMCPAuthNoServer = errors.New("mcp auth: server is not a discoverable active server")
)

type mcpAuthFlow interface {
	URL() string
	Status() (string, error)
	Done() bool
	Submit(ctx context.Context, code, state string) error
	Cancel() error
}

var startMCPAuthFlow = func(ctx context.Context, name string, srv mcp.ServerConfig, store *mcp.AuthStore, opts mcp.AuthOptions) (mcpAuthFlow, error) {
	return mcp.StartAuth(ctx, name, srv, store, opts)
}

func mcpAuthBrowserOpener(u string) error {
	return auth.OpenBrowserConfirming(u, auth.DefaultBrowserConfirmWait)
}

func (m *Model) mcpAuthStartFromDetail() tea.Cmd {
	name := m.mcpDetailName
	w, _, ok := m.mcpWinnerRow(name)
	if !ok {
		return nil
	}
	s := w.Summary
	if s.Kind != "remote" && s.URL == "" {
		m.mcpAuthError = fmt.Sprintf(i18n.T("tui.mcpAuthNotApplicable"), name)
		return nil
	}

	m.mcpAuthSeq++
	m.mcpAuthName = name
	m.mcpAuthState = mcpAuthStarting
	m.mcpAuthFlow = nil
	m.mcpAuthURL = ""
	m.mcpAuthFeedback = ""
	m.mcpAuthError = ""
	m.textInput.SetValue("")
	m.textInput.Placeholder = i18n.T("tui.mcpAuthPlaceholder")
	m.syncTextInputEcho()
	m.textInput.Focus()
	m.modalMode = ModalMCPAuth
	return m.mcpAuthStartCmd(name)
}

func (m *Model) mcpAuthStartCmd(name string) tea.Cmd {
	paths, ok := m.mcpPaths()
	seq := m.mcpAuthSeq
	opener := m.mcpAuthOpener
	if !ok {
		return func() tea.Msg {
			return mcpAuthStartMsg{seq: seq, name: name, err: errMCPUnavailable}
		}
	}
	return func() tea.Msg {
		store := mcp.NewAuthStore(paths.LeleDir)
		disc, _ := mcp.Discover(paths)
		srv, found := disc.Servers[name]
		if !found {
			return mcpAuthStartMsg{seq: seq, name: name, err: errMCPAuthNoServer}
		}
		var openErr error
		opts := mcp.AuthOptions{}
		if opener != nil {
			opts.OpenURL = func(u string) error {
				openErr = opener(u)
				return openErr
			}
		}
		flow, err := startMCPAuthFlow(context.Background(), name, srv, store, opts)
		if err != nil {
			return mcpAuthStartMsg{seq: seq, name: name, err: err}
		}
		return mcpAuthStartMsg{
			seq:       seq,
			name:      name,
			flow:      flow,
			openTried: opener != nil,
			openErr:   openErr,
		}
	}
}

func (m *Model) mcpAuthSubmitCmd(code, state string) tea.Cmd {
	flow, seq := m.mcpAuthFlow, m.mcpAuthSeq
	return func() tea.Msg {
		return mcpAuthSubmitMsg{seq: seq, err: flow.Submit(context.Background(), code, state)}
	}
}

func (m *Model) mcpAuthPollCmd() tea.Cmd {
	seq := m.mcpAuthSeq
	return tea.Tick(mcpAuthPollInterval, func(time.Time) tea.Msg {
		return mcpAuthPollMsg{seq: seq}
	})
}

func mcpAuthSentinelText(err error) string {
	switch {
	case errors.Is(err, mcp.ErrAuthNotSupported):
		return i18n.T("tui.mcpAuthErrNotSupported")
	case errors.Is(err, mcp.ErrAuthNotRequired):
		return i18n.T("tui.mcpAuthErrNotRequired")
	case errors.Is(err, mcp.ErrAuthStoreDisabled):
		return i18n.T("tui.mcpAuthErrStoreDisabled")
	case errors.Is(err, mcp.ErrAuthProbeFailed):
		return i18n.T("tui.mcpAuthErrProbeFailed")
	case errors.Is(err, mcp.ErrAuthCanceled):
		return i18n.T("tui.mcpAuthErrCanceled")
	case errors.Is(err, mcp.ErrAuthTimedOut):
		return i18n.T("tui.mcpAuthErrTimedOut")
	}
	return ""
}

func mcpAuthStartErrorText(name string, err error) string {
	switch {
	case errors.Is(err, errMCPUnavailable):
		return i18n.T("tui.mcpUnavailable")
	case errors.Is(err, errMCPAuthNoServer):
		return fmt.Sprintf(i18n.T("tui.mcpAuthServerUnknown"), name)
	}
	if t := mcpAuthSentinelText(err); t != "" {
		return t
	}
	return fmt.Sprintf(i18n.T("tui.mcpAuthStartFailed"), err)
}

func mcpAuthSubmitErrorText(err error) string {
	if t := mcpAuthSentinelText(err); t != "" {
		return t
	}
	return fmt.Sprintf(i18n.T("tui.mcpAuthSubmitFailed"), err)
}

func mcpAuthTerminalErrorText(err error) string {
	if t := mcpAuthSentinelText(err); t != "" {
		return t
	}
	return i18n.T("tui.mcpAuthFailed")
}

func mcpAuthInputErrorText(err error) string {
	switch {
	case errors.Is(err, mcp.ErrAuthPasteEmpty):
		return i18n.T("tui.mcpAuthEmptyCode")
	case errors.Is(err, mcp.ErrAuthPasteUnrecognized), errors.Is(err, mcp.ErrAuthPasteNoCode):
		return i18n.T("tui.mcpAuthBadInput")
	}
	var denied *mcp.AuthPasteDeniedError
	if errors.As(err, &denied) {
		if denied.Code == "" {
			return i18n.T("tui.mcpAuthRefusedUnknown")
		}
		return fmt.Sprintf(i18n.T("tui.mcpAuthRefused"), denied.Code)
	}
	return i18n.T("tui.mcpAuthBadInput")
}

func (m *Model) handleMCPAuthStart(msg mcpAuthStartMsg) tea.Cmd {
	if msg.seq != m.mcpAuthSeq || m.modalMode != ModalMCPAuth || m.mcpAuthState != mcpAuthStarting {
		if msg.flow != nil {
			_ = msg.flow.Cancel()
		}
		return nil
	}
	if msg.err != nil {
		m.mcpAuthState = mcpAuthFailed
		m.mcpAuthError = mcpAuthStartErrorText(msg.name, msg.err)
		return nil
	}
	m.mcpAuthFlow = msg.flow
	m.mcpAuthURL = msg.flow.URL()
	m.mcpAuthState = mcpAuthPending
	m.mcpAuthFeedback = ""
	m.mcpAuthError = ""
	switch {
	case !msg.openTried:
	case msg.openErr != nil:
		m.mcpAuthError = i18n.T("tui.mcpAuthBrowserFailed")
	default:
		m.mcpAuthFeedback = i18n.T("tui.mcpAuthBrowserOpened")
	}
	return m.mcpAuthPollCmd()
}

func (m *Model) handleMCPAuthPoll(msg mcpAuthPollMsg) tea.Cmd {
	if msg.seq != m.mcpAuthSeq || m.modalMode != ModalMCPAuth ||
		m.mcpAuthState != mcpAuthPending || m.mcpAuthFlow == nil {
		return nil
	}
	status, err := m.mcpAuthFlow.Status()
	switch status {
	case mcpAuthStatusDone:
		m.mcpAuthFinishDone()
		return nil
	case mcpAuthStatusError:
		if m.mcpAuthFlow.Done() {
			m.mcpAuthState = mcpAuthFailed
			m.mcpAuthError = mcpAuthTerminalErrorText(err)
			return nil
		}
		m.mcpAuthError = mcpAuthSubmitErrorText(err)
	}
	return m.mcpAuthPollCmd()
}

func (m *Model) handleMCPAuthSubmit(msg mcpAuthSubmitMsg) tea.Cmd {
	if msg.seq != m.mcpAuthSeq || m.modalMode != ModalMCPAuth {
		return nil
	}
	if msg.err != nil {
		m.mcpAuthError = mcpAuthSubmitErrorText(msg.err)
		return nil
	}
	m.mcpAuthFinishDone()
	return nil
}

func (m *Model) mcpAuthFinishDone() bool {
	if m.mcpAuthState != mcpAuthPending {
		return false
	}
	m.mcpAuthState = mcpAuthSucceeded
	m.mcpAuthError = ""
	m.mcpAuthFeedback = fmt.Sprintf(i18n.T("tui.mcpAuthSuccess"), m.mcpAuthName)
	syncMCPServersFor(m.agentLoop)
	m.loadMCPList()
	return true
}

func (m *Model) mcpAuthCancel() {
	hadFlow := m.mcpAuthFlow != nil
	if hadFlow {
		_ = m.mcpAuthFlow.Cancel()
		m.mcpAuthFlow = nil
	}
	m.mcpAuthSeq++
	if hadFlow || m.mcpAuthState != mcpAuthIdle {
		m.textInput.SetValue("")
		m.textInput.Placeholder = ""
		m.textInput.Blur()
	}
	m.mcpAuthState = mcpAuthIdle
	m.mcpAuthName = ""
	m.mcpAuthURL = ""
	m.mcpAuthFeedback = ""
	m.mcpAuthError = ""
}

func (m *Model) handleMCPAuthKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		m.mcpAuthCancel()
		m.modalMode = ModalMCPDetail
		return nil, true

	case "enter":
		if m.mcpAuthState != mcpAuthPending || m.mcpAuthFlow == nil {
			return nil, true
		}
		code, state, err := mcp.ParseAuthPaste(m.textInput.Value())
		if err != nil {
			m.mcpAuthError = mcpAuthInputErrorText(err)
			return nil, true
		}
		m.textInput.SetValue("")
		m.mcpAuthError = ""
		m.mcpAuthFeedback = ""
		return m.mcpAuthSubmitCmd(code, state), true

	case "ctrl+y":
		if m.mcpAuthURL == "" {
			m.mcpAuthError = i18n.T("tui.mcpAuthCopyFailed")
			return nil, true
		}
		copyToClipboard(m.mcpAuthURL)
		m.mcpAuthError = ""
		m.mcpAuthFeedback = i18n.T("tui.mcpAuthCopied")
		return nil, true
	}
	return nil, false
}

var mcpAuthErrorStyle = lipgloss.NewStyle().Foreground(PrimaryColor)

func (m *Model) mcpAuthFeedbackLine() string {
	var sb strings.Builder
	if m.mcpAuthFeedback != "" {
		sb.WriteString(SuccessStyle.Render("  "+m.mcpAuthFeedback) + "\n")
	}
	if m.mcpAuthError != "" {
		sb.WriteString(mcpAuthErrorStyle.Render("  ✗ " + m.mcpAuthError))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m *Model) mcpAuthURLWidth() int {
	w := m.width - 16
	if w < 20 {
		w = 20
	}
	return w
}

func hardWrapCells(s string, cells int) []string {
	if cells <= 0 {
		return []string{s}
	}
	var out []string
	var cur strings.Builder
	width := 0
	forEachGraphemeCluster(s, func(cluster string, w int) {
		if width > 0 && width+w > cells {
			out = append(out, cur.String())
			cur.Reset()
			width = 0
		}
		cur.WriteString(cluster)
		width += w
	})
	if cur.Len() > 0 || len(out) == 0 {
		out = append(out, cur.String())
	}
	return out
}

func (m *Model) mcpAuthBody() []string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(m.modalTitleFor(ModalMCPAuth)) + "\n")
	sb.WriteString(TitleStyle.Render(m.mcpAuthName) + "\n\n")

	switch m.mcpAuthState {
	case mcpAuthStarting:
		sb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.mcpAuthStarting")) + "\n")
	case mcpAuthSucceeded, mcpAuthFailed:
		sb.WriteString(m.mcpAuthFeedbackLine() + "\n")
	default:
		sb.WriteString(mcpAuthErrorStyle.Render(i18n.T("tui.mcpAuthURLLabel")+":") + "\n")
		for _, line := range hardWrapCells(m.mcpAuthURL, m.mcpAuthURLWidth()) {
			sb.WriteString("  " + line + "\n")
		}
		sb.WriteString("\n" + i18n.T("tui.mcpAuthInstructions") + "\n\n")
		sb.WriteString(m.textInputView() + "\n")
		if line := m.mcpAuthFeedbackLine(); line != "" {
			sb.WriteString("\n" + line + "\n")
		}
	}
	sb.WriteString("\n")
	if m.mcpAuthState == mcpAuthPending {
		sb.WriteString(HelpStyle.Render("  "+i18n.T("tui.mcpAuthHintEnter")) + "\n")
		sb.WriteString(HelpStyle.Render("  "+i18n.T("tui.mcpAuthHintCopy")) + "\n")
	}
	hint := i18n.T("tui.mcpAuthHintClose")
	if m.mcpAuthState == mcpAuthSucceeded || m.mcpAuthState == mcpAuthFailed {
		hint = i18n.T("tui.mcpAuthHintClosed")
	}
	sb.WriteString(HelpStyle.Render("  " + hint))
	return strings.Split(sb.String(), "\n")
}

func (m *Model) mcpAuthBox() string {
	lines := m.mcpAuthBody()
	maxBody := m.height - mcpAuthModalOverhead
	if maxBody < 1 {
		maxBody = 1
	}
	if len(lines) > maxBody {
		lines = lines[:maxBody]
	}
	width := m.width - 10
	if width < 20 {
		width = 20
	}
	box := ModalContainer.Width(width).Render(strings.Join(lines, "\n"))
	return clampLineCount(box, m.height)
}

func clampLineCount(s string, max int) string {
	if max <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	return strings.Join(lines[:max], "\n")
}

func (m *Model) renderMCPAuth() string {
	return m.paintFrame(m.mcpAuthBox())
}

func mcpAuthVerdicts(paths mcp.Paths, inv mcp.Inventory) map[string]mcp.AuthStatus {
	verdicts := make(map[string]mcp.AuthStatus)
	if len(inv.Winner) == 0 {
		return verdicts
	}
	store := mcp.NewAuthStore(paths.LeleDir)
	if !store.Enabled() {
		return verdicts
	}
	disc, _ := mcp.Discover(paths)
	for name := range inv.Winner {
		srv, ok := disc.Servers[name]
		if !ok {
			continue
		}
		verdicts[name] = mcp.CheckAuth(store, name, srv)
	}
	return verdicts
}

func mcpAuthSuffix(st mcp.AuthStatus) string {
	switch {
	case st.Authenticated:
		return " " + i18n.T("tui.mcpAuthOK")
	case st.Required:
		return " " + i18n.T("tui.mcpAuthRequired")
	default:
		return ""
	}
}

func mcpAuthMethodLabel(method string) string {
	if method == "" {
		return i18n.T("tui.mcpDetailNone")
	}
	return method
}

var errMCPAuthUnavailableStore = errors.New("mcp auth: no credential store available")

var errMCPAuthNoCredential = errors.New("mcp auth: no stored credential")

func mcpAuthLogoutResult(name string, err error) mcpToggleResultMsg {
	return mcpToggleResultMsg{name: name, logout: true, err: err}
}

func (m *Model) mcpAuthLogoutCmd(name string) tea.Cmd {
	paths, ok := m.mcpPaths()
	if !ok {
		return func() tea.Msg {
			return mcpAuthLogoutResult(name, errMCPUnavailable)
		}
	}
	return mcpAuthLogoutCmdFor(paths, name)
}

func mcpAuthLogoutCmdFor(paths mcp.Paths, name string) tea.Cmd {
	return func() tea.Msg {
		store := mcp.NewAuthStore(paths.LeleDir)
		if !store.Enabled() {
			return mcpAuthLogoutResult(name, errMCPAuthUnavailableStore)
		}
		if rec, ok := store.Record(name); !ok || rec.Token == nil {
			return mcpAuthLogoutResult(name, errMCPAuthNoCredential)
		}
		return mcpAuthLogoutResult(name, mcp.Logout(store, name))
	}
}

func (m *Model) handleMCPAuthLogoutResult(name string, err error) tea.Cmd {
	var line string
	switch {
	case err == nil:
		line = fmt.Sprintf(i18n.T("tui.mcpAuthLogoutDone"), name)
		syncMCPServersFor(m.agentLoop)
	case errors.Is(err, errMCPUnavailable):
		line = i18n.T("tui.mcpUnavailable")
	case errors.Is(err, errMCPAuthUnavailableStore):
		line = i18n.T("tui.mcpAuthUnavailable")
	case errors.Is(err, errMCPAuthNoCredential):
		line = fmt.Sprintf(i18n.T("tui.mcpAuthLogoutNone"), name)
	default:
		line = fmt.Sprintf(i18n.T("tui.mcpAuthLogoutFailed"), name, err)
	}
	m.mcpFeedback = line
	if m.modalMode == ModalMCPDetail {
		m.mcpAuthFeedback, m.mcpAuthError = "", line
		if err == nil {
			m.mcpAuthFeedback, m.mcpAuthError = line, ""
		}
	}
	m.loadMCPList()
	return m.tickCmd()
}
