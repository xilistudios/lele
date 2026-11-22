package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

const mcpAuthFixtureDoc = `{"mcpServers":{` +
	`"remote-srv":{"url":"https://mcp.example.test/mcp","description":"remote"},` +
	`"local-srv":{"command":"true"}}}`

func mcpAuthListModel(t *testing.T, doc string) *Model {
	t.Helper()
	m := newTestModel(t)
	mcpWriteDoc(t, config.GetLeleDir(), doc)
	m.width, m.height = 140, 40
	m.mcpAuthOpener = func(string) error { return nil }

	m.executeCommand("/mcp")
	if m.modalMode != ModalMCP {
		t.Fatalf("mode = %v, want ModalMCP", m.modalMode)
	}
	paths, ok := m.mcpPaths()
	if !ok {
		t.Fatal("m.mcpPaths() = false, the fixture must resolve the MCP roots")
	}
	if paths.LeleDir != config.GetLeleDir() {
		t.Fatalf("global layer = %q, want %q", paths.LeleDir, config.GetLeleDir())
	}
	return m
}

func mcpAuthOpenDetail(t *testing.T, m *Model, name string) {
	t.Helper()
	idx := -1
	for i, key := range m.mcpModalKeys {
		if key == name {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("no row for %q in %v", name, m.mcpModalKeys)
	}
	m.modalSelectedIdx = idx
	if _, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter}); !handled {
		t.Fatal("enter was not handled by the MCP list")
	}
	if m.modalMode != ModalMCPDetail {
		t.Fatalf("mode = %v, want ModalMCPDetail", m.modalMode)
	}
}

type fakeMCPAuthFlow struct {
	url        string
	status     string
	done       bool
	statusErr  error
	submitErr  error
	submits    []mcpAuthSubmitArgs
	cancelCall int
}

type mcpAuthSubmitArgs struct{ code, state string }

func (f *fakeMCPAuthFlow) URL() string { return f.url }

func (f *fakeMCPAuthFlow) Status() (string, error) { return f.status, f.statusErr }

func (f *fakeMCPAuthFlow) Done() bool { return f.done || f.status == mcpAuthStatusDone }

func (f *fakeMCPAuthFlow) Submit(_ context.Context, code, state string) error {
	f.submits = append(f.submits, mcpAuthSubmitArgs{code: code, state: state})
	if f.submitErr != nil {
		f.status, f.statusErr = mcpAuthStatusError, f.submitErr
		return f.submitErr
	}
	f.status, f.statusErr = mcpAuthStatusDone, nil
	return nil
}

func (f *fakeMCPAuthFlow) Cancel() error {
	f.cancelCall++
	f.status = mcpAuthStatusError
	f.statusErr = mcp.ErrAuthCanceled
	f.done = true
	return nil
}

type mcpAuthStartCapture struct {
	names   []string
	srvs    []mcp.ServerConfig
	opts    []mcp.AuthOptions
	flow    mcpAuthFlow
	err     error
	openErr error
	opened  []string
}

func (c *mcpAuthStartCapture) install(t *testing.T) {
	t.Helper()
	prevStart := startMCPAuthFlow
	startMCPAuthFlow = func(_ context.Context, name string, srv mcp.ServerConfig, _ *mcp.AuthStore, opts mcp.AuthOptions) (mcpAuthFlow, error) {
		c.names = append(c.names, name)
		c.srvs = append(c.srvs, srv)
		c.opts = append(c.opts, opts)
		if c.err != nil {
			return nil, c.err
		}
		if opts.OpenURL != nil && c.flow != nil {
			_ = opts.OpenURL(c.flow.URL())
		}
		return c.flow, nil
	}
	t.Cleanup(func() { startMCPAuthFlow = prevStart })
}

func (c *mcpAuthStartCapture) attach(m *Model) *Model {
	m.mcpAuthOpener = func(u string) error {
		c.opened = append(c.opened, u)
		return c.openErr
	}
	return m
}

func mcpAuthRecordingOpener(m *Model, err error) *[]string {
	opened := new([]string)
	m.mcpAuthOpener = func(u string) error {
		*opened = append(*opened, u)
		return err
	}
	return opened
}

func countMCPSyncs(t *testing.T) *int {
	t.Helper()
	prev := syncMCPServersFor
	n := 0
	syncMCPServersFor = func(*agent.AgentLoop) { n++ }
	t.Cleanup(func() { syncMCPServersFor = prev })
	return &n
}

func runCmd(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command, got nil")
	}
	m.Update(cmd())
}

func mcpAuthSubmitKey(t *testing.T, m *Model) tea.Cmd {
	t.Helper()
	cmd, handled := m.handleMCPAuthKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("enter was not handled")
	}
	if cmd == nil {
		t.Fatal("expected a submit command, got nil")
	}
	return cmd
}

func mcpAuthEnterFlow(t *testing.T, capture *mcpAuthStartCapture, flow *fakeMCPAuthFlow) *Model {
	t.Helper()
	capture.flow = flow
	capture.install(t)

	m := capture.attach(mcpAuthListModel(t, mcpAuthFixtureDoc))
	mcpAuthOpenDetail(t, m, "remote-srv")

	cmd, handled := m.handleMCPKey(mcpKeyRunes('a'))
	if !handled {
		t.Fatal(`"a" was not handled in the detail modal`)
	}
	if m.modalMode != ModalMCPAuth {
		t.Fatalf("mode after \"a\" = %v, want ModalMCPAuth", m.modalMode)
	}
	if m.mcpAuthState != mcpAuthStarting {
		t.Fatalf("state after \"a\" = %d, want mcpAuthStarting", m.mcpAuthState)
	}
	runCmd(t, m, cmd)
	if m.mcpAuthState != mcpAuthPending {
		t.Fatalf("state after the start result = %d (error %q), want mcpAuthPending",
			m.mcpAuthState, m.mcpAuthError)
	}
	if m.mcpAuthFlow == nil {
		t.Fatal("no live flow on the model after a successful start")
	}
	return m
}

func TestMCPAuthKey_AOpensFlowOnRemoteEntryAndRefusesStdio(t *testing.T) {
	t.Run("remote entry starts the flow", func(t *testing.T) {
		capture := &mcpAuthStartCapture{}
		flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize?state=s", status: mcpAuthStatusPending}
		m := mcpAuthEnterFlow(t, capture, flow)

		if len(capture.names) != 1 || capture.names[0] != "remote-srv" {
			t.Fatalf("engine asked for %v, want [remote-srv]", capture.names)
		}
		if got := capture.srvs[0].URL; got != "https://mcp.example.test/mcp" {
			t.Errorf("ServerConfig.URL = %q, want the winning remote url", got)
		}
		if capture.opts[0].OpenURL == nil {
			t.Error("AuthOptions.OpenURL is nil; the TUI must hand the engine a browser opener")
		}
		if len(capture.opened) != 1 || capture.opened[0] != flow.url {
			t.Errorf("browser opener asked for %v, want exactly [%s]", capture.opened, flow.url)
		}
		if m.mcpAuthFeedback != i18n.T("tui.mcpAuthBrowserOpened") {
			t.Errorf("feedback = %q, want the browser-opened line %q",
				m.mcpAuthFeedback, i18n.T("tui.mcpAuthBrowserOpened"))
		}
		out := ansi.Strip(m.renderMCPAuth())
		if !strings.Contains(out, flow.url) {
			t.Errorf("pending modal lost the authorization URL:\n%s", out)
		}
		if !strings.Contains(out, i18n.T("tui.mcpAuthHintCopy")) {
			t.Errorf("pending modal lost the copy hint:\n%s", out)
		}
		if m.mcpAuthURL != flow.url {
			t.Errorf("mcpAuthURL = %q, want the flow's URL %q", m.mcpAuthURL, flow.url)
		}
		if m.mcpAuthName != "remote-srv" {
			t.Errorf("mcpAuthName = %q, want remote-srv", m.mcpAuthName)
		}
	})

	t.Run("stdio entry is refused inline", func(t *testing.T) {
		capture := &mcpAuthStartCapture{}
		capture.install(t)
		m := mcpAuthListModel(t, mcpAuthFixtureDoc)
		mcpAuthOpenDetail(t, m, "local-srv")

		cmd, handled := m.handleMCPKey(mcpKeyRunes('a'))
		if !handled {
			t.Fatal(`"a" was not handled in the detail modal`)
		}
		if cmd != nil {
			t.Error("a stdio entry produced a command; no engine call may be made")
		}
		if len(capture.names) != 0 {
			t.Errorf("the engine was asked to start %v for a stdio entry", capture.names)
		}
		if m.modalMode != ModalMCPDetail {
			t.Errorf("mode = %v, want to stay on ModalMCPDetail", m.modalMode)
		}
		want := strings.Replace(i18n.T("tui.mcpAuthNotApplicable"), "%s", "local-srv", 1)
		if m.mcpAuthError != want {
			t.Errorf("inline hint = %q, want %q", m.mcpAuthError, want)
		}
		out := ansi.Strip(m.renderMCPDetail())
		if !strings.Contains(out, want) {
			t.Errorf("detail does not render the refusal hint %q:\n%s", want, out)
		}
		if !strings.Contains(out, i18n.T("tui.mcpDetailHints")) {
			t.Errorf("detail lost its hints line:\n%s", out)
		}
	})
}

func TestMCPAuthKey_AOnEntryMissingFromDiscoverShowsInlineError(t *testing.T) {
	capture := &mcpAuthStartCapture{}
	capture.install(t)
	doc := `{"mcpServers":{"off-srv":{"url":"https://mcp.example.test/mcp","disabled":true}}}`
	m := mcpAuthListModel(t, doc)
	mcpAuthOpenDetail(t, m, "off-srv")

	cmd, handled := m.handleMCPKey(mcpKeyRunes('a'))
	if !handled || cmd == nil {
		t.Fatalf(`"a": handled=%v cmd=%v, want handled with a start command`, handled, cmd)
	}
	if len(capture.names) != 0 {
		t.Fatalf("the engine was called for a disabled server: %v", capture.names)
	}
	msg, ok := cmd().(mcpAuthStartMsg)
	if !ok {
		t.Fatalf("start command returned %T, want mcpAuthStartMsg", cmd())
	}
	if !errors.Is(msg.err, errMCPAuthNoServer) {
		t.Fatalf("start error = %v, want errMCPAuthNoServer", msg.err)
	}
	m.Update(msg)
	if m.mcpAuthState != mcpAuthFailed {
		t.Errorf("state = %d, want mcpAuthFailed", m.mcpAuthState)
	}
	want := strings.Replace(i18n.T("tui.mcpAuthServerUnknown"), "%s", "off-srv", 1)
	if m.mcpAuthError != want {
		t.Errorf("inline error = %q, want %q", m.mcpAuthError, want)
	}
}

func TestMCPAuthFlow_StartPollDoneSyncsOnceAndReloadsList(t *testing.T) {
	syncs := countMCPSyncs(t)
	flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize?state=s", status: mcpAuthStatusPending}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)

	out := ansi.Strip(m.renderMCPAuth())
	if !strings.Contains(out, "https://auth.example.test/authorize?state=s") {
		t.Errorf("pending modal does not show the authorization URL:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.mcpAuthHintEnter")) ||
		!strings.Contains(out, i18n.T("tui.mcpAuthHintCopy")) {
		t.Errorf("pending modal lacks the two hints:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.mcpAuth")) {
		t.Errorf("pending modal lacks its title:\n%s", out)
	}

	seq := m.mcpAuthSeq
	updated, cmd := m.Update(mcpAuthPollMsg{seq: seq})
	if updated.(*Model).modalMode != ModalMCPAuth {
		t.Fatal("a poll left the auth modal")
	}
	if cmd == nil {
		t.Error("a pending flow did not re-arm the poll")
	}
	if *syncs != 0 {
		t.Fatalf("syncs = %d while still pending, want 0", *syncs)
	}

	flow.status = mcpAuthStatusDone
	m.Update(mcpAuthPollMsg{seq: seq})

	if m.mcpAuthState != mcpAuthSucceeded {
		t.Fatalf("state = %d, want mcpAuthSucceeded", m.mcpAuthState)
	}
	want := strings.Replace(i18n.T("tui.mcpAuthSuccess"), "%s", "remote-srv", 1)
	if m.mcpAuthFeedback != want {
		t.Errorf("feedback = %q, want %q", m.mcpAuthFeedback, want)
	}
	if *syncs != 1 {
		t.Fatalf("SyncMCPServers calls = %d, want exactly 1", *syncs)
	}

	found := false
	for _, key := range m.mcpModalKeys {
		if key == "remote-srv" {
			found = true
		}
	}
	if !found {
		t.Errorf("list not reloaded after the flow completed: keys = %v", m.mcpModalKeys)
	}

	if _, cmd := m.Update(mcpAuthPollMsg{seq: seq}); cmd != nil {
		t.Error("a poll was re-armed after a terminal state")
	}
	m.Update(mcpAuthPollMsg{seq: seq})
	if *syncs != 1 {
		t.Errorf("SyncMCPServers calls = %d after a duplicate poll, want 1", *syncs)
	}
	m.Update(mcpAuthSubmitMsg{seq: m.mcpAuthSeq})
	if *syncs != 1 {
		t.Errorf("SyncMCPServers calls = %d after a duplicate success, want 1", *syncs)
	}
	if m.modalMode != ModalMCPAuth || m.mcpAuthState != mcpAuthSucceeded {
		t.Errorf("a duplicate success changed the modal: mode=%v state=%d", m.modalMode, m.mcpAuthState)
	}

	if got := ansi.Strip(m.renderMCPAuth()); !strings.Contains(got, want) {
		t.Errorf("done modal does not render %q:\n%s", want, got)
	}
}

func TestMCPAuthFlow_TerminalErrorStopsTicking(t *testing.T) {
	flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize", status: mcpAuthStatusPending}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)

	flow.status, flow.statusErr, flow.done = mcpAuthStatusError, mcp.ErrAuthTimedOut, true
	_, cmd := m.Update(mcpAuthPollMsg{seq: m.mcpAuthSeq})
	if cmd != nil {
		t.Error("the poll was re-armed after a terminal error")
	}
	if m.mcpAuthState != mcpAuthFailed {
		t.Fatalf("state = %d, want mcpAuthFailed", m.mcpAuthState)
	}
	if m.mcpAuthError != i18n.T("tui.mcpAuthErrTimedOut") {
		t.Errorf("error = %q, want the localized timeout sentinel", m.mcpAuthError)
	}
	if strings.Contains(m.mcpAuthError, mcp.ErrAuthTimedOut.Error()) {
		t.Errorf("terminal error leaked the engine text: %q", m.mcpAuthError)
	}
}

func TestMCPAuthFlow_RetryableErrorStaysPendingAndKeepsTicking(t *testing.T) {
	flow := &fakeMCPAuthFlow{
		url:       "https://auth.example.test/authorize",
		status:    mcpAuthStatusPending,
		submitErr: mcp.ErrAuthNotSupported,
	}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)

	m.textInput.SetValue("A-CODE")
	runCmd(t, m, mcpAuthSubmitKey(t, m))
	if flow.Done() {
		t.Fatal("precondition: the failed exchange must leave the flow open")
	}

	_, cmd := m.Update(mcpAuthPollMsg{seq: m.mcpAuthSeq})
	if m.mcpAuthState != mcpAuthPending {
		t.Fatalf("state = %d, want to stay mcpAuthPending (the flow is retryable)", m.mcpAuthState)
	}
	if cmd == nil {
		t.Error("the poll stopped ticking while the flow is still retryable")
	}
	if m.mcpAuthError != i18n.T("tui.mcpAuthErrNotSupported") {
		t.Errorf("error = %q, want the localized engine sentinel", m.mcpAuthError)
	}
	flow.submitErr = nil
	m.textInput.SetValue("A-BETTER-CODE")
	runCmd(t, m, mcpAuthSubmitKey(t, m))
	if m.mcpAuthState != mcpAuthSucceeded {
		t.Errorf("state = %d, want mcpAuthSucceeded after the retry", m.mcpAuthState)
	}
	if len(flow.submits) != 2 {
		t.Errorf("submits = %v, want the two attempts", flow.submits)
	}
}

func TestMCPAuthFlow_StartErrorIsLocalizedPerSentinel(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"not supported", mcp.ErrAuthNotSupported, i18n.T("tui.mcpAuthErrNotSupported")},
		{"not required", mcp.ErrAuthNotRequired, i18n.T("tui.mcpAuthErrNotRequired")},
		{"store disabled", mcp.ErrAuthStoreDisabled, i18n.T("tui.mcpAuthErrStoreDisabled")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := &mcpAuthStartCapture{err: tc.err}
			capture.install(t)
			m := mcpAuthListModel(t, mcpAuthFixtureDoc)
			mcpAuthOpenDetail(t, m, "remote-srv")

			cmd, _ := m.handleMCPKey(mcpKeyRunes('a'))
			if cmd == nil {
				t.Fatal("no start command")
			}
			m.Update(cmd())

			if m.mcpAuthState != mcpAuthFailed {
				t.Fatalf("state = %d, want mcpAuthFailed", m.mcpAuthState)
			}
			if m.mcpAuthError != tc.want {
				t.Errorf("error = %q, want %q", m.mcpAuthError, tc.want)
			}
		})
	}
}

func TestMCPAuthSentinelText_ProbeFailedIsLocalizedInEveryLocale(t *testing.T) {
	prev := i18n.GetLanguage()
	t.Cleanup(func() { i18n.SetLanguage(prev) })

	err := fmt.Errorf("mcp auth %q: probe: %w", "api", mcp.ErrAuthProbeFailed)

	seen := map[string]string{}
	for _, lang := range []string{"en", "es", "pt"} {
		i18n.SetLanguage(lang)
		got := mcpAuthSentinelText(err)
		if got == "" {
			t.Fatalf("[%s] the probe sentinel is not mapped: the engine's English text leaks through", lang)
		}
		if want := i18n.T("tui.mcpAuthErrProbeFailed"); got != want {
			t.Errorf("[%s] sentinel text = %q, want %q", lang, got, want)
		}
		if strings.Contains(got, mcp.ErrAuthProbeFailed.Error()) {
			t.Errorf("[%s] the engine's English sentence leaked: %q", lang, got)
		}
		if got == i18n.T("tui.mcpAuthErrNotRequired") {
			t.Errorf("[%s] an unreachable server is reported as needing no auth", lang)
		}
		seen[lang] = got
	}
	for _, lang := range []string{"es", "pt"} {
		if seen[lang] == seen["en"] {
			t.Errorf("[%s] mcpAuthErrProbeFailed carries the English text: %q", lang, seen[lang])
		}
	}

	i18n.SetLanguage("es")
	if want, got := i18n.T("tui.mcpAuthErrProbeFailed"), mcpAuthStartErrorText("api", err); got != want {
		t.Errorf("mcpAuthStartErrorText = %q, want the es sentence %q", got, want)
	}
}

func TestMCPAuthFlow_LateStartResultIsCancelledNotAdopted(t *testing.T) {
	capture := &mcpAuthStartCapture{}
	flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize", status: mcpAuthStatusPending}
	capture.install(t)
	startMCPAuthFlow = func(_ context.Context, name string, _ mcp.ServerConfig, _ *mcp.AuthStore, _ mcp.AuthOptions) (mcpAuthFlow, error) {
		return flow, nil
	}

	m := mcpAuthListModel(t, mcpAuthFixtureDoc)
	mcpAuthOpenDetail(t, m, "remote-srv")
	cmd, _ := m.handleMCPKey(mcpKeyRunes('a'))
	if cmd == nil {
		t.Fatal("no start command")
	}
	if _, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEsc}); !handled {
		t.Fatal("esc was not handled")
	}
	if m.modalMode != ModalMCPDetail {
		t.Fatalf("mode after esc = %v, want ModalMCPDetail", m.modalMode)
	}
	runCmd(t, m, cmd)
	if flow.cancelCall != 1 {
		t.Errorf("late flow cancels = %d, want 1 (a dropped flow pins its listener)", flow.cancelCall)
	}
	if m.mcpAuthFlow != nil {
		t.Error("a late flow was adopted after the modal was left")
	}
	if m.modalMode != ModalMCPDetail {
		t.Errorf("a late start result reopened the modal: mode = %v", m.modalMode)
	}
}

func TestMCPAuthManualSubmit_AcceptsEveryPasteShape(t *testing.T) {
	cases := []struct {
		name      string
		paste     string
		wantCode  string
		wantState string
	}{
		{
			name:      "bare code",
			paste:     "SplxlOBeZQQYbYS6WxSbIA",
			wantCode:  "SplxlOBeZQQYbYS6WxSbIA",
			wantState: "",
		},
		{
			name:      "full redirect URL",
			paste:     "http://127.0.0.1:8080/callback?code=REDIRECT-CODE&state=REDIRECT-STATE",
			wantCode:  "REDIRECT-CODE",
			wantState: "REDIRECT-STATE",
		},
		{
			name:      "redirect URL without its state",
			paste:     "http://127.0.0.1:8080/callback?code=ONLY-CODE",
			wantCode:  "ONLY-CODE",
			wantState: "",
		},
		{
			name:      "bare query string",
			paste:     "code=QUERY-CODE&state=QUERY-STATE",
			wantCode:  "QUERY-CODE",
			wantState: "QUERY-STATE",
		},
		{
			name:      "two-token code state paste",
			paste:     "TWO-TOKENS STATE-TOKEN",
			wantCode:  "TWO-TOKENS",
			wantState: "STATE-TOKEN",
		},
		{
			name:      "surrounding whitespace",
			paste:     "  PADDED-CODE  ",
			wantCode:  "PADDED-CODE",
			wantState: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize", status: mcpAuthStatusPending}
			m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)

			m.textInput.SetValue(tc.paste)
			cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter})
			if !handled || cmd == nil {
				t.Fatalf("enter: handled=%v cmd=%v, want a submit command", handled, cmd)
			}
			if got := m.textInput.Value(); got != "" {
				t.Errorf("text input not cleared after parsing: %q", got)
			}
			runCmd(t, m, cmd)

			if len(flow.submits) != 1 {
				t.Fatalf("submits = %v, want exactly one", flow.submits)
			}
			got := flow.submits[0]
			if got.code != tc.wantCode || got.state != tc.wantState {
				t.Errorf("Submit(%q, %q), want (%q, %q)", got.code, got.state, tc.wantCode, tc.wantState)
			}
			if m.mcpAuthState != mcpAuthSucceeded {
				t.Errorf("state = %d, want mcpAuthSucceeded", m.mcpAuthState)
			}
		})
	}
}

func TestMCPAuthManualSubmit_EmptyAndMalformedInputNeverReachTheEngine(t *testing.T) {
	cases := []struct {
		name  string
		paste string
		want  func() string
	}{
		{"empty", "", func() string { return i18n.T("tui.mcpAuthEmptyCode") }},
		{"blank", "   ", func() string { return i18n.T("tui.mcpAuthEmptyCode") }},
		{"three tokens", "a b c", func() string { return i18n.T("tui.mcpAuthBadInput") }},
		{"redirect without a code", "http://127.0.0.1:8080/callback?state=only-state", func() string { return i18n.T("tui.mcpAuthBadInput") }},
		{"punctuation only", "!!!", func() string { return i18n.T("tui.mcpAuthBadInput") }},
		{"parameter-shaped garbage", "foo=bar", func() string { return i18n.T("tui.mcpAuthBadInput") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize", status: mcpAuthStatusPending}
			m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)

			m.textInput.SetValue(tc.paste)
			cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter})
			if !handled {
				t.Fatal("enter was not handled")
			}
			if cmd != nil {
				t.Error("a command was returned; no engine call may be made")
			}
			if len(flow.submits) != 0 {
				t.Errorf("the engine received %v", flow.submits)
			}
			if want := tc.want(); m.mcpAuthError != want {
				t.Errorf("inline error = %q, want %q", m.mcpAuthError, want)
			}
			if m.mcpAuthState != mcpAuthPending {
				t.Errorf("state = %d, want to stay pending", m.mcpAuthState)
			}
		})
	}
}

func TestMCPAuthManualSubmit_ExchangeFailureIsRetryableWithoutRestart(t *testing.T) {
	flow := &fakeMCPAuthFlow{
		url:       "https://auth.example.test/authorize",
		status:    mcpAuthStatusPending,
		submitErr: mcp.ErrAuthNotSupported,
	}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)

	m.textInput.SetValue("FIRST-CODE")
	cmd, _ := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("no submit command")
	}
	runCmd(t, m, cmd)

	if m.mcpAuthState != mcpAuthPending {
		t.Fatalf("state after a failed exchange = %d, want to stay mcpAuthPending", m.mcpAuthState)
	}
	if m.mcpAuthError != i18n.T("tui.mcpAuthErrNotSupported") {
		t.Errorf("error = %q, want the localized sentinel", m.mcpAuthError)
	}
	if m.mcpAuthFlow == nil {
		t.Fatal("the flow was released; a retry would have to restart it")
	}

	flow.submitErr = nil
	m.textInput.SetValue("SECOND-CODE")
	cmd, _ = m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("no retry submit command")
	}
	runCmd(t, m, cmd)

	if len(flow.submits) != 2 {
		t.Fatalf("submits = %v, want the two attempts", flow.submits)
	}
	if got := flow.submits[1].code; got != "SECOND-CODE" {
		t.Errorf("retry submitted %q, want SECOND-CODE", got)
	}
	if m.mcpAuthState != mcpAuthSucceeded {
		t.Errorf("state = %d, want mcpAuthSucceeded", m.mcpAuthState)
	}
	if flow.cancelCall != 0 {
		t.Errorf("cancels = %d, want 0 (the flow was never restarted)", flow.cancelCall)
	}
}

func TestMCPAuthKey_CtrlYCopiesTheAuthorizationURL(t *testing.T) {
	flow := &fakeMCPAuthFlow{
		url:    "https://auth.example.test/authorize?client_id=abc&state=s",
		status: mcpAuthStatusPending,
	}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prev := os.Stdout
	os.Stdout = w
	cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyCtrlY})
	os.Stdout = prev
	_ = w.Close()
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	_ = r.Close()

	if !handled {
		t.Fatal("ctrl+y was not handled")
	}
	if cmd != nil {
		t.Error("ctrl+y returned a command, want none")
	}
	if m.mcpAuthFeedback != i18n.T("tui.mcpAuthCopied") {
		t.Errorf("feedback = %q, want %q", m.mcpAuthFeedback, i18n.T("tui.mcpAuthCopied"))
	}
	if m.mcpAuthError != "" {
		t.Errorf("error line set on a successful copy: %q", m.mcpAuthError)
	}
	if want := buildOSC52(flow.url); !strings.Contains(string(buf[:n]), want) {
		t.Errorf("clipboard payload does not carry the authorization URL (got %q)", string(buf[:n]))
	}
}

func TestMCPAuthKey_CtrlYWithNoURLReportsFailure(t *testing.T) {
	m := &Model{modalMode: ModalMCPAuth, mcpAuthState: mcpAuthStarting, mcpAuthName: "srv"}

	cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyCtrlY})
	if !handled || cmd != nil {
		t.Fatalf("ctrl+y: handled=%v cmd=%v, want handled with no cmd", handled, cmd)
	}
	if m.mcpAuthFeedback != "" {
		t.Errorf("success feedback set with no URL: %q", m.mcpAuthFeedback)
	}
	if m.mcpAuthError != i18n.T("tui.mcpAuthCopyFailed") {
		t.Errorf("error = %q, want %q", m.mcpAuthError, i18n.T("tui.mcpAuthCopyFailed"))
	}
}

func TestMCPAuthOpenURL_BrowserLaunchedWithTheFlowURL(t *testing.T) {
	capture := &mcpAuthStartCapture{}
	flow := &fakeMCPAuthFlow{
		url:    "https://auth.example.test/authorize?client_id=abc&state=s",
		status: mcpAuthStatusPending,
	}
	m := mcpAuthEnterFlow(t, capture, flow)

	if got := capture.opened; len(got) != 1 || got[0] != flow.url {
		t.Fatalf("browser launches = %v, want exactly [%s]", got, flow.url)
	}
	if m.mcpAuthError != "" {
		t.Errorf("a successful launch left an error line: %q", m.mcpAuthError)
	}
	out := ansi.Strip(m.renderMCPAuth())
	for _, want := range []string{
		i18n.T("tui.mcpAuthBrowserOpened"),
		flow.url,
		i18n.T("tui.mcpAuthHintCopy"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pending modal does not render %q:\n%s", want, out)
		}
	}
}

func TestMCPAuthOpenURL_LaunchFailureKeepsTheCopyPath(t *testing.T) {
	launchErr := errors.New("xdg-open: no display")
	capture := &mcpAuthStartCapture{openErr: launchErr}
	flow := &fakeMCPAuthFlow{
		url:    "https://auth.example.test/authorize?state=s",
		status: mcpAuthStatusPending,
	}
	m := mcpAuthEnterFlow(t, capture, flow)

	if m.mcpAuthState != mcpAuthPending {
		t.Fatalf("state = %d, want to stay mcpAuthPending (a launch failure is not terminal)", m.mcpAuthState)
	}
	if m.mcpAuthFeedback != "" {
		t.Errorf("feedback = %q, want it empty when the launch failed", m.mcpAuthFeedback)
	}
	if m.mcpAuthError != i18n.T("tui.mcpAuthBrowserFailed") {
		t.Errorf("error line = %q, want %q", m.mcpAuthError, i18n.T("tui.mcpAuthBrowserFailed"))
	}
	out := ansi.Strip(m.renderMCPAuth())
	for _, want := range []string{
		i18n.T("tui.mcpAuthBrowserFailed"),
		flow.url,
		i18n.T("tui.mcpAuthHintCopy"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pending modal does not render %q:\n%s", want, out)
		}
	}
	if _, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyCtrlY}); !handled {
		t.Fatal("ctrl+y was not handled after a failed launch")
	}
	if m.mcpAuthFeedback != i18n.T("tui.mcpAuthCopied") {
		t.Errorf("feedback after ctrl+y = %q, want %q", m.mcpAuthFeedback, i18n.T("tui.mcpAuthCopied"))
	}
	if m.mcpAuthError != "" {
		t.Errorf("copy did not clear the launch-failure line: %q", m.mcpAuthError)
	}
}

func TestMCPAuthOpenURL_NoOpenerClaimsNothing(t *testing.T) {
	capture := &mcpAuthStartCapture{}
	capture.install(t)
	m := mcpAuthListModel(t, mcpAuthFixtureDoc)
	m.mcpAuthOpener = nil
	capture.flow = &fakeMCPAuthFlow{
		url:    "https://auth.example.test/authorize?state=s",
		status: mcpAuthStatusPending,
	}
	mcpAuthOpenDetail(t, m, "remote-srv")

	cmd, handled := m.handleMCPKey(mcpKeyRunes('a'))
	if !handled || cmd == nil {
		t.Fatalf(`"a": handled=%v cmd=%v, want a start command`, handled, cmd)
	}
	runCmd(t, m, cmd)

	if m.mcpAuthState != mcpAuthPending {
		t.Fatalf("state = %d (error %q), want mcpAuthPending", m.mcpAuthState, m.mcpAuthError)
	}
	if len(capture.opts) != 1 || capture.opts[0].OpenURL != nil {
		t.Errorf("the engine was handed an opener by a model that has none: %v", capture.opts)
	}
	if m.mcpAuthFeedback != "" || m.mcpAuthError != "" {
		t.Errorf("feedback/error = (%q, %q), want both empty when nothing was attempted",
			m.mcpAuthFeedback, m.mcpAuthError)
	}
	if out := ansi.Strip(m.renderMCPAuth()); !strings.Contains(out, capture.flow.URL()) {
		t.Errorf("the authorization URL is not rendered without an opener:\n%s", out)
	}
}

func TestMCPAuthKey_EscCancelsAndReturnsToTheDetail(t *testing.T) {
	flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize", status: mcpAuthStatusPending}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)
	m.mcpDetailName = "remote-srv"

	cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !handled || cmd != nil {
		t.Fatalf("esc: handled=%v cmd=%v, want handled with no cmd", handled, cmd)
	}
	if flow.cancelCall != 1 {
		t.Errorf("flow cancels = %d, want 1 (the loopback listener must be released)", flow.cancelCall)
	}
	if m.modalMode != ModalMCPDetail {
		t.Errorf("mode = %v, want ModalMCPDetail (not the list)", m.modalMode)
	}
	if m.mcpAuthFlow != nil || m.mcpAuthState != mcpAuthIdle {
		t.Errorf("cancel left state behind: flow=%v state=%d", m.mcpAuthFlow, m.mcpAuthState)
	}
	if cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEsc}); !handled || cmd != nil || m.modalMode != ModalMCP {
		t.Errorf("second esc: mode=%v cmd=%v handled=%v, want ModalMCP with no cmd",
			m.modalMode, cmd, handled)
	}
}

func TestMCPAuthKey_QInThePasteBoxDoesNotCancel(t *testing.T) {
	flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize?state=s", status: mcpAuthStatusPending}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)

	if cmd, handled := m.handleMCPKey(mcpKeyRunes('q')); handled || cmd != nil {
		t.Fatalf(`"q": handled=%v cmd=%v, want it passed through to the input`, handled, cmd)
	}

	m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q7-Zxq")}, nil)
	if got := m.textInput.Value(); got != "q7-Zxq" {
		t.Fatalf("text input = %q, want the typed paste including its q", got)
	}

	if flow.cancelCall != 0 {
		t.Errorf("flow cancels = %d, want 0 — a typed q must not abort the flow", flow.cancelCall)
	}
	if m.mcpAuthFlow == nil {
		t.Error("the flow was released by a keystroke")
	}
	if m.modalMode != ModalMCPAuth || m.mcpAuthState != mcpAuthPending {
		t.Fatalf("a typed q left the modal: mode=%v state=%d", m.modalMode, m.mcpAuthState)
	}

	cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled || cmd == nil {
		t.Fatalf("enter after the paste: handled=%v cmd=%v, want a submit command", handled, cmd)
	}
	runCmd(t, m, cmd)
	if len(flow.submits) != 1 || flow.submits[0].code != "q7-Zxq" {
		t.Fatalf("submits = %v, want the pasted code with its q", flow.submits)
	}
	if m.mcpAuthState != mcpAuthSucceeded {
		t.Errorf("state = %d, want mcpAuthSucceeded", m.mcpAuthState)
	}
}

func TestMCPAuthResetModalCancelsLiveFlow(t *testing.T) {
	flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize", status: mcpAuthStatusPending}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)
	if m.mcpAuthFlow == nil {
		t.Fatal("precondition: no live flow")
	}
	m.textInput.SetValue("UNPARSED-PASTED-CODE")

	m.executeCommand("/mcp")

	if flow.cancelCall != 1 {
		t.Errorf("flow cancels = %d, want 1 — resetModal leaked the loopback listener", flow.cancelCall)
	}
	if m.mcpAuthFlow != nil {
		t.Error("resetModal left a live flow on the model")
	}
	if m.mcpAuthState != mcpAuthIdle || m.mcpAuthName != "" || m.mcpAuthURL != "" {
		t.Errorf("resetModal left auth state behind: state=%d name=%q url=%q",
			m.mcpAuthState, m.mcpAuthName, m.mcpAuthURL)
	}
	if m.modalMode != ModalMCP {
		t.Errorf("mode = %v, want ModalMCP", m.modalMode)
	}
	if m.textInput.Value() != "" || m.textInput.Focused() {
		t.Errorf("resetModal kept a pasted code in the shared input: %q (focused=%v)",
			m.textInput.Value(), m.textInput.Focused())
	}
	if m.textInput.Value() != "" {
		t.Errorf("resetModal kept the pasted code: %q", m.textInput.Value())
	}
	old := m.mcpAuthSeq
	m.Update(mcpAuthStartMsg{seq: old - 1, name: "remote-srv", flow: flow})
	if m.mcpAuthFlow != nil || m.modalMode != ModalMCP {
		t.Errorf("a stale start message was adopted: flow=%v mode=%v", m.mcpAuthFlow, m.modalMode)
	}
}

func TestRenderMCPAuth_HeightClampAndNoPanic(t *testing.T) {
	longURL := "https://auth.example.test/authorize?client_id=abc123&redirect_uri=" +
		"http%3A%2F%2F127.0.0.1%3A49152%2Fcallback&response_type=code&scope=mcp" +
		"&code_challenge=challenge-value-here&code_challenge_method=S256&state=state-value-here"

	states := []int{mcpAuthStarting, mcpAuthPending, mcpAuthSucceeded, mcpAuthFailed, mcpAuthIdle}
	for _, h := range []int{40, 10, 3, 1} {
		for _, state := range states {
			m := &Model{
				modalMode:       ModalMCPAuth,
				width:           120,
				height:          h,
				mcpAuthState:    state,
				mcpAuthName:     "remote-srv",
				mcpAuthURL:      longURL,
				mcpAuthFeedback: i18n.T("tui.mcpAuthCopied"),
				mcpAuthError:    i18n.T("tui.mcpAuthEmptyCode"),
			}
			boxLines := len(strings.Split(m.mcpAuthBox(), "\n"))
			if boxLines > h {
				t.Errorf("height %d state %d: box has %d lines, want <= %d (clamped)",
					h, state, boxLines, h)
			}
			out := m.renderMCPAuth()
			if got := ansi.Strip(out); strings.Count(got, "\n")+1 > h {
				t.Errorf("height %d state %d: rendered frame has %d lines, want <= %d",
					h, state, strings.Count(got, "\n")+1, h)
			}
		}
	}
}

func TestRenderMCPAuth_WrapsTheURLInsideTheBox(t *testing.T) {
	url := "https://auth.example.test/authorize?client_id=abc&state=" + strings.Repeat("s", 200)
	m := &Model{
		modalMode:    ModalMCPAuth,
		width:        120,
		height:       40,
		mcpAuthState: mcpAuthPending,
		mcpAuthName:  "remote-srv",
		mcpAuthURL:   url,
	}
	box := ansi.Strip(m.mcpAuthBox())
	for _, line := range strings.Split(box, "\n") {
		if got := ansi.StringWidth(line); got > m.width-6 {
			t.Errorf("box line is %d cells wide (terminal %d): %q", got, m.width, line)
		}
	}
	var joined strings.Builder
	for _, r := range box {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		if r >= 0x2500 && r <= 0x257F {
			continue
		}
		joined.WriteRune(r)
	}
	if !strings.Contains(joined.String(), url) {
		t.Errorf("the wrapped URL was truncated or reordered:\n%s", box)
	}
}

func TestRenderMCPAuth_CloseHintMatchesWhatEscCanDo(t *testing.T) {
	cases := []struct {
		state       int
		wantKey     string
		unwantedKey string
		why         string
	}{
		{mcpAuthStarting, "tui.mcpAuthHintClose", "tui.mcpAuthHintClosed", "ESC aborts the in-flight attempt"},
		{mcpAuthPending, "tui.mcpAuthHintClose", "tui.mcpAuthHintClosed", "ESC cancels the live flow and frees its listener"},
		{mcpAuthSucceeded, "tui.mcpAuthHintClosed", "tui.mcpAuthHintClose", "the flow is over: ESC only closes"},
		{mcpAuthFailed, "tui.mcpAuthHintClosed", "tui.mcpAuthHintClose", "the flow is over: ESC only closes"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("state=%d", tc.state), func(t *testing.T) {
			m := &Model{
				modalMode:    ModalMCPAuth,
				width:        120,
				height:       40,
				mcpAuthState: tc.state,
				mcpAuthName:  "remote-srv",
				mcpAuthURL:   "https://auth.example.test/authorize",
			}
			out := ansi.Strip(m.mcpAuthBox())
			if want := i18n.T(tc.wantKey); !strings.Contains(out, want) {
				t.Errorf("state %d (%s): box is missing %q:\n%s", tc.state, tc.why, want, out)
			}
			if unwanted := i18n.T(tc.unwantedKey); strings.Contains(out, unwanted) {
				t.Errorf("state %d (%s): box renders the wrong close hint %q:\n%s",
					tc.state, tc.why, unwanted, out)
			}
		})
	}
}

func TestMCPAuthKey_TypingReachesTheInputAndNeverMovesTheListCursor(t *testing.T) {
	flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize", status: mcpAuthStatusPending}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)
	if !m.textInput.Focused() {
		t.Fatal("the code input is not focused: every keystroke would be dropped")
	}

	m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("CO")}, nil)
	if got := m.textInput.Value(); got != "CO" {
		t.Errorf("text input = %q, want the typed keys to reach it", got)
	}
	m.modalSelectedIdx = 1
	before := m.modalSelectedIdx
	for _, msg := range []tea.KeyMsg{{Type: tea.KeyUp}, {Type: tea.KeyDown}, mcpKeyRunes('j'), mcpKeyRunes('k')} {
		m.handleKeyMsg(msg, nil)
		if m.modalSelectedIdx != before {
			t.Fatalf("%q moved the hidden list cursor to %d", msg.String(), m.modalSelectedIdx)
		}
	}
	if out := ansi.Strip(m.renderMCPAuth()); !strings.Contains(out, "CO") {
		t.Errorf("the pasted text is not rendered:\n%s", out)
	}
}

func TestMCPAuthKey_EnterIsInertWhileStarting(t *testing.T) {
	m := &Model{modalMode: ModalMCPAuth, mcpAuthState: mcpAuthStarting, mcpAuthName: "srv"}
	m.textInput.SetValue("some-code")

	cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled || cmd != nil {
		t.Fatalf("enter while starting: handled=%v cmd=%v, want handled with no cmd", handled, cmd)
	}
	if m.mcpAuthError != "" {
		t.Errorf("error set while starting: %q", m.mcpAuthError)
	}
}

func mcpAuthDeniedExpr(code string) *string { return &code }

func TestMCPAuthParseInput_MatchesTheEngineGrammar(t *testing.T) {
	longCode := strings.Repeat("a", 1200)

	cases := []struct {
		name       string
		paste      string
		wantOK     bool
		wantCode   string
		wantState  string
		wantErr    error
		wantDenied *string
	}{
		{name: "bare code", paste: "SplxlOBeZQQYbYS6WxSbIA", wantOK: true, wantCode: "SplxlOBeZQQYbYS6WxSbIA"},
		{name: "base64url alphabet", paste: "abc-12_3.x~y", wantOK: true, wantCode: "abc-12_3.x~y"},
		{name: "long opaque code", paste: longCode, wantOK: true, wantCode: longCode},
		{name: "padded by hand", paste: "  abc123  ", wantOK: true, wantCode: "abc123"},
		{name: "trailing newline", paste: "abc123\n", wantOK: true, wantCode: "abc123"},
		{
			name: "full redirect URL", wantOK: true, wantCode: "REDIRECT-CODE", wantState: "REDIRECT-STATE",
			paste: "http://127.0.0.1:8080/callback?code=REDIRECT-CODE&state=REDIRECT-STATE",
		},
		{name: "redirect URL without a state", paste: "http://127.0.0.1:8080/callback?code=ONLY-CODE", wantOK: true, wantCode: "ONLY-CODE"},
		{name: "percent-encoded code", paste: "http://127.0.0.1:8080/cb?code=a%2Bb&state=s", wantOK: true, wantCode: "a+b", wantState: "s"},
		{name: "stray fragment on the query", paste: "http://127.0.0.1:8080/cb?code=abc#frag", wantOK: true, wantCode: "abc"},
		{name: "fragment carries the response", paste: "http://127.0.0.1:8080/cb#code=abc&state=s", wantOK: true, wantCode: "abc", wantState: "s"},
		{name: "fragment rescues a code-less query", paste: "http://127.0.0.1:8080/cb?state=xyz#code=abc", wantOK: true, wantCode: "abc"},
		{name: "bare query string", paste: "code=X&state=Y", wantOK: true, wantCode: "X", wantState: "Y"},
		{name: "query with its question mark", paste: "?code=X&state=Y", wantOK: true, wantCode: "X", wantState: "Y"},
		{name: "single-token query", paste: "code=abc", wantOK: true, wantCode: "abc"},
		{name: "two tokens", paste: "A-CODE A-STATE", wantOK: true, wantCode: "A-CODE", wantState: "A-STATE"},
		{name: "two tokens, wide gap", paste: "abc   xyz", wantOK: true, wantCode: "abc", wantState: "xyz"},
		{
			name: "denial redirect", paste: "http://127.0.0.1:8080/callback?error=access_denied&state=s",
			wantErr: mcp.ErrAuthPasteDenied, wantDenied: mcpAuthDeniedExpr("access_denied"),
		},
		{name: "denial query string", paste: "error=access_denied", wantErr: mcp.ErrAuthPasteDenied, wantDenied: mcpAuthDeniedExpr("access_denied")},
		{name: "denial with an RFC 6749 code", paste: "http://127.0.0.1:8080/callback?error=invalid_request", wantErr: mcp.ErrAuthPasteDenied, wantDenied: mcpAuthDeniedExpr("invalid_request")},
		{name: "denial with a hostile code", paste: "http://127.0.0.1:8080/callback?error=%1B%5B31mhax", wantErr: mcp.ErrAuthPasteDenied, wantDenied: mcpAuthDeniedExpr("")},
		{name: "error wins over a code", paste: "http://127.0.0.1:8080/cb?error=access_denied&code=abc", wantErr: mcp.ErrAuthPasteDenied, wantDenied: mcpAuthDeniedExpr("access_denied")},
		{name: "empty", paste: "", wantErr: mcp.ErrAuthPasteEmpty},
		{name: "whitespace only", paste: "   ", wantErr: mcp.ErrAuthPasteEmpty},
		{name: "punctuation", paste: "!!!", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "punctuation inside a token", paste: "abc!def", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "parameter with a space", paste: "code=abc def", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "three tokens", paste: "a b c", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "two tokens, one invalid", paste: "abc! xyz", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "parameter-shaped garbage", paste: "foo=bar", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "malformed query", paste: "code=%zz", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "malformed query in a redirect", paste: "http://127.0.0.1:8080/callback?code=%zz", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "paste over the input bound", paste: strings.Repeat("a", 8200), wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "redirect without a code", paste: "http://127.0.0.1:8080/callback?state=only", wantErr: mcp.ErrAuthPasteNoCode},
		{name: "empty code parameter", paste: "http://127.0.0.1:8080/callback?code=", wantErr: mcp.ErrAuthPasteNoCode},
		{name: "state-only parameter", paste: "state=xyz", wantErr: mcp.ErrAuthPasteNoCode},
		{name: "state-only query", paste: "?state=xyz", wantErr: mcp.ErrAuthPasteNoCode},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, state, err := mcp.ParseAuthPaste(tc.paste)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("mcp.ParseAuthPaste(%q) = %v, want it accepted", tc.paste, err)
				}
				if code != tc.wantCode || state != tc.wantState {
					t.Errorf("mcp.ParseAuthPaste(%q) = (%q, %q), want (%q, %q)",
						tc.paste, code, state, tc.wantCode, tc.wantState)
				}
				return
			}
			if err == nil {
				t.Fatalf("mcp.ParseAuthPaste(%q) = (%q, %q), want it REFUSED", tc.paste, code, state)
			}
			if code != "" || state != "" {
				t.Errorf("a refused paste returned (%q, %q), want (\"\", \"\")", code, state)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("refusal = %v, want %v", err, tc.wantErr)
			}
			var want string
			if tc.wantDenied != nil {
				var denied *mcp.AuthPasteDeniedError
				if !errors.As(err, &denied) {
					t.Fatalf("refusal = %v (%T), want a *mcp.AuthPasteDeniedError", err, err)
				}
				if denied.Code != *tc.wantDenied {
					t.Errorf("echoable code = %q, want %q", denied.Code, *tc.wantDenied)
				}
				if *tc.wantDenied == "" {
					want = i18n.T("tui.mcpAuthRefusedUnknown")
				} else {
					want = fmt.Sprintf(i18n.T("tui.mcpAuthRefused"), *tc.wantDenied)
				}
			} else {
				var denied *mcp.AuthPasteDeniedError
				if errors.As(err, &denied) {
					t.Errorf("a non-denial paste was reported as a denial: %v", err)
				}
				switch {
				case errors.Is(err, mcp.ErrAuthPasteEmpty):
					want = i18n.T("tui.mcpAuthEmptyCode")
				default:
					want = i18n.T("tui.mcpAuthBadInput")
				}
			}
			if got := mcpAuthInputErrorText(err); got != want {
				t.Errorf("rendered message = %q, want %q", got, want)
			}
			if msg := mcpAuthInputErrorText(err); len(msg) > 200 || strings.ContainsRune(msg, '\x1b') {
				t.Errorf("rendered message is not bounded/clean: %q", msg)
			}
		})
	}
}

func TestMCPAuthManualSubmit_RefusalsNeverReachTheEngine(t *testing.T) {
	cases := []struct {
		name  string
		paste string
		want  func() string
	}{
		{
			name:  "denial redirect",
			paste: "http://127.0.0.1:8080/callback?error=access_denied&state=s",
			want:  func() string { return fmt.Sprintf(i18n.T("tui.mcpAuthRefused"), "access_denied") },
		},
		{
			name:  "denial query string",
			paste: "error=access_denied",
			want:  func() string { return fmt.Sprintf(i18n.T("tui.mcpAuthRefused"), "access_denied") },
		},
		{
			name:  "hostile denial code is not echoed",
			paste: "http://127.0.0.1:8080/callback?error=" + strings.Repeat("x", 200),
			want:  func() string { return i18n.T("tui.mcpAuthRefusedUnknown") },
		},
		{
			name:  "malformed query",
			paste: "code=%zz",
			want:  func() string { return i18n.T("tui.mcpAuthBadInput") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize", status: mcpAuthStatusPending}
			m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)

			m.textInput.SetValue(tc.paste)
			cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter})
			if !handled {
				t.Fatal("enter was not handled")
			}
			if cmd != nil {
				t.Error("a command was returned; a refused paste must not reach the engine")
			}
			if len(flow.submits) != 0 {
				t.Errorf("the engine received %v", flow.submits)
			}
			if want := tc.want(); m.mcpAuthError != want {
				t.Errorf("inline error = %q, want %q", m.mcpAuthError, want)
			}
			if len(m.mcpAuthError) > 200 {
				t.Errorf("refusal message is %d bytes long (the paste was echoed)", len(m.mcpAuthError))
			}
			if m.mcpAuthState != mcpAuthPending {
				t.Errorf("state = %d, want to stay pending (the flow is still live)", m.mcpAuthState)
			}
			if got := m.textInput.Value(); got != tc.paste {
				t.Errorf("input = %q, want the refused paste to stay for editing", got)
			}
		})
	}
}

const mcpAuthVisibilityDoc = `{"mcpServers":{` +
	`"oauth-srv":{"url":"https://oauth.example.test/mcp","auth":{"type":"oauth"}},` +
	`"challenged-srv":{"url":"https://challenged.example.test/mcp"},` +
	`"local-srv":{"command":"true"}}}`

func mcpAuthVisibilityURL(name string) string {
	switch name {
	case "oauth-srv":
		return "https://oauth.example.test/mcp"
	case "challenged-srv":
		return "https://challenged.example.test/mcp"
	default:
		return "https://unknown.example.test/mcp"
	}
}

func mcpTestToken() *transport.Token {
	return &transport.Token{
		AccessToken: "stored-access-token",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
}

func mcpAuthSeedRecord(t *testing.T, dir, name string, rec mcp.AuthRecord) {
	t.Helper()
	if err := mcp.NewAuthStore(dir).Save(name, rec); err != nil {
		t.Fatalf("seed auth record for %s in %s: %v", name, dir, err)
	}
}

func mcpAuthRow(t *testing.T, m *Model, name string) (string, int) {
	t.Helper()
	for i, key := range m.mcpModalKeys {
		if key == name {
			return m.modalItems[i], i
		}
	}
	t.Fatalf("no list row for %q in %v", name, m.mcpModalKeys)
	return "", -1
}

func mcpAuthSelectRow(t *testing.T, m *Model, name string) {
	t.Helper()
	_, idx := mcpAuthRow(t, m, name)
	m.modalSelectedIdx = idx
}

func TestFormatMCPItemAuthSuffix(t *testing.T) {
	pad := strings.Repeat(" ", 12)
	base := "● srv" + pad + " — [global]"
	cases := []struct {
		name       string
		suffix     string
		wantSuffix string
	}{
		{name: "no verdict", suffix: "", wantSuffix: ""},
		{name: "needs auth", suffix: " " + i18n.T("tui.mcpAuthRequired"), wantSuffix: " " + i18n.T("tui.mcpAuthRequired")},
		{name: "authenticated", suffix: " " + i18n.T("tui.mcpAuthOK"), wantSuffix: " " + i18n.T("tui.mcpAuthOK")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatMCPItem("srv", mcp.LayerGlobal, "enabled", tc.suffix)
			if want := base + tc.wantSuffix; got != want {
				t.Errorf("formatMCPItem(..., %q) = %q, want %q", tc.suffix, got, want)
			}
			if !strings.HasPrefix(got, base) {
				t.Errorf("row %q lost its base shape %q", got, base)
			}
		})
	}
}

func TestMCPAuthSuffixPerVerdict(t *testing.T) {
	cases := []struct {
		name string
		st   mcp.AuthStatus
		want string
	}{
		{"zero verdict", mcp.AuthStatus{}, ""},
		{"required, no token", mcp.AuthStatus{Required: true, Method: "oauth"}, " " + i18n.T("tui.mcpAuthRequired")},
		{"required, authenticated", mcp.AuthStatus{Required: true, Authenticated: true, Method: "oauth"}, " " + i18n.T("tui.mcpAuthOK")},
		{"headers only", mcp.AuthStatus{Method: "headers"}, ""},
		{"token without a requirement", mcp.AuthStatus{Authenticated: true}, " " + i18n.T("tui.mcpAuthOK")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpAuthSuffix(tc.st); got != tc.want {
				t.Errorf("mcpAuthSuffix(%+v) = %q, want %q", tc.st, got, tc.want)
			}
		})
	}
}

func TestMCPAuthVerdictsRemoteOAuthAndChallenge(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	mcpWriteDoc(t, paths.LeleDir, mcpAuthVisibilityDoc)

	verdict := func(name string) mcp.AuthStatus {
		t.Helper()
		return mcpAuthVerdicts(paths, mcp.ReadInventory(paths))[name]
	}

	if st := verdict("oauth-srv"); !st.Required || st.Authenticated || st.Method != "oauth" {
		t.Errorf("oauth-srv without a record = %+v, want required oauth, not authenticated", st)
	}
	if st := verdict("challenged-srv"); st.Required || st.Authenticated || st.Method != "" {
		t.Errorf("challenged-srv without a challenge = %+v, want the zero verdict", st)
	}
	if st := verdict("local-srv"); st != (mcp.AuthStatus{}) {
		t.Errorf("local-srv = %+v, want the zero verdict", st)
	}

	mcpAuthSeedRecord(t, paths.LeleDir, "oauth-srv",
		mcp.AuthRecord{URL: mcpAuthVisibilityURL("oauth-srv"), Token: mcpTestToken()})
	if st := verdict("oauth-srv"); !st.Required || !st.Authenticated || st.Method != "oauth" {
		t.Errorf("oauth-srv with a matching token = %+v, want required + authenticated", st)
	}

	mcpAuthSeedRecord(t, paths.LeleDir, "oauth-srv",
		mcp.AuthRecord{URL: "https://other.example.test/mcp", Token: mcpTestToken()})
	if st := verdict("oauth-srv"); st.Authenticated {
		t.Errorf("oauth-srv with a mismatched-url record = %+v, want not authenticated", st)
	}

	mcpAuthSeedRecord(t, paths.LeleDir, "challenged-srv",
		mcp.AuthRecord{URL: mcpAuthVisibilityURL("challenged-srv"), AuthRequired: true})
	if st := verdict("challenged-srv"); !st.Required || st.Authenticated || st.Method != "" {
		t.Errorf("challenged-srv with a recorded 401 = %+v, want required, no carrier named", st)
	}
	mcpAuthSeedRecord(t, paths.LeleDir, "challenged-srv",
		mcp.AuthRecord{URL: mcpAuthVisibilityURL("challenged-srv"), AuthRequired: true, Token: mcpTestToken()})
	if st := verdict("challenged-srv"); !st.Required || !st.Authenticated || st.Method != "" {
		t.Errorf("challenged-srv with a token = %+v, want required + authenticated, no carrier", st)
	}
}

func TestMCPAuthVerdictsHeadersAndStdio(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{LeleDir: filepath.Join(root, "global")}
	mcpWriteDoc(t, paths.LeleDir, `{"mcpServers":{`+
		`"hdr-srv":{"url":"https://hdr.example.test/mcp","headers":{"Authorization":"Bearer ${TOKEN}"}},`+
		`"local-srv":{"command":"true"}}}`)

	verdicts := mcpAuthVerdicts(paths, mcp.ReadInventory(paths))
	if st := verdicts["hdr-srv"]; st.Required || st.Authenticated || st.Method != "headers" {
		t.Errorf("hdr-srv = %+v, want method=headers, not required", st)
	}
	if st := verdicts["local-srv"]; st != (mcp.AuthStatus{}) {
		t.Errorf("local-srv = %+v, want the zero verdict", st)
	}
	mcpAuthSeedRecord(t, paths.LeleDir, "hdr-srv",
		mcp.AuthRecord{URL: "https://hdr.example.test/mcp", AuthRequired: true})
	if st := mcpAuthVerdicts(paths, mcp.ReadInventory(paths))["hdr-srv"]; !st.Required || st.Method != "headers" {
		t.Errorf("hdr-srv with a recorded 401 = %+v, want required + method=headers", st)
	}
}

func TestMCPAuthVerdictsEmptyLeleDir(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{AgentWorkspace: root}
	mcpWriteDoc(t, root, mcpAuthVisibilityDoc)

	verdicts := mcpAuthVerdicts(paths, mcp.ReadInventory(paths))
	if len(verdicts) != 0 {
		t.Errorf("verdicts with a disabled store = %v, want an empty map", verdicts)
	}
}

func TestMCPListShowsAuthSuffix(t *testing.T) {
	m := mcpAuthListModel(t, mcpAuthVisibilityDoc)
	needed, authed := i18n.T("tui.mcpAuthRequired"), i18n.T("tui.mcpAuthOK")

	row, _ := mcpAuthRow(t, m, "oauth-srv")
	if !strings.HasSuffix(row, " "+needed) {
		t.Errorf("oauth-srv row = %q, want the %q marker", row, needed)
	}
	if !strings.HasPrefix(row, formatMCPItem("oauth-srv", mcp.LayerGlobal, "enabled", "")) {
		t.Errorf("oauth-srv row = %q, want the plain row plus the marker", row)
	}
	for _, name := range []string{"challenged-srv", "local-srv"} {
		row, _ := mcpAuthRow(t, m, name)
		if strings.Contains(row, needed) || strings.Contains(row, authed) {
			t.Errorf("%s row = %q, want no auth marker", name, row)
		}
	}

	mcpAuthSeedRecord(t, config.GetLeleDir(), "oauth-srv",
		mcp.AuthRecord{URL: mcpAuthVisibilityURL("oauth-srv"), Token: mcpTestToken()})
	m.loadMCPList()
	row, _ = mcpAuthRow(t, m, "oauth-srv")
	if !strings.HasSuffix(row, " "+authed) {
		t.Errorf("authenticated oauth-srv row = %q, want the %q marker", row, authed)
	}
}

func TestMCPDetailRendersAuthBlock(t *testing.T) {
	m := mcpAuthListModel(t, mcpAuthVisibilityDoc)

	mcpAuthOpenDetail(t, m, "oauth-srv")
	out := ansi.Strip(m.renderMCPDetail())
	if want := i18n.T("tui.mcpDetailAuth") + ": oauth"; !strings.Contains(out, want) {
		t.Errorf("detail missing %q:\n%s", want, out)
	}
	if want := i18n.T("tui.mcpDetailAuthState") + ": " + i18n.T("tui.mcpAuthRequired"); !strings.Contains(out, want) {
		t.Errorf("detail missing %q:\n%s", want, out)
	}
	if !strings.Contains(out, i18n.T("tui.mcpLogoutHint")) {
		t.Errorf("detail does not advertise the sign-out (%q):\n%s", i18n.T("tui.mcpLogoutHint"), out)
	}
	if !strings.Contains(out, i18n.T("tui.mcpDetailHints")) {
		t.Errorf("detail lost its own hints line:\n%s", out)
	}

	mcpAuthSeedRecord(t, config.GetLeleDir(), "oauth-srv",
		mcp.AuthRecord{URL: mcpAuthVisibilityURL("oauth-srv"), Token: mcpTestToken()})
	m.loadMCPList()
	out = ansi.Strip(m.renderMCPDetail())
	if want := i18n.T("tui.mcpDetailAuthState") + ": " + i18n.T("tui.mcpAuthOK"); !strings.Contains(out, want) {
		t.Errorf("detail missing %q:\n%s", want, out)
	}

	if _, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEsc}); !handled {
		t.Fatal("esc was not handled")
	}
	mcpAuthOpenDetail(t, m, "local-srv")
	out = ansi.Strip(m.renderMCPDetail())
	if strings.Contains(out, i18n.T("tui.mcpDetailAuth")+":") {
		t.Errorf("a stdio detail renders an auth block:\n%s", out)
	}
	if strings.Contains(out, i18n.T("tui.mcpLogoutHint")) {
		t.Errorf("a stdio detail advertises a sign-out:\n%s", out)
	}
}

func TestMCPListHintsAdvertiseSignOutAndAdd(t *testing.T) {
	m := mcpAuthListModel(t, mcpAuthVisibilityDoc)

	prev := i18n.GetLanguage()
	t.Cleanup(func() { i18n.SetLanguage(prev) })
	for _, lang := range []string{"en", "es", "pt"} {
		i18n.SetLanguage(lang)
		hints, logout, add := i18n.T("tui.mcpListHints"), i18n.T("tui.mcpLogoutHint"), i18n.T("tui.mcpAddHint")
		if !strings.Contains(hints, logout) {
			t.Errorf("[%s] mcpListHints = %q, does not advertise the sign-out %q", lang, hints, logout)
		}
		if !strings.Contains(hints, add) {
			t.Errorf("[%s] mcpListHints = %q, does not advertise the add wizard %q", lang, hints, add)
		}
		filter, toggle := strings.Index(hints, "F:"), strings.Index(hints, "T:")
		if i := strings.Index(hints, logout); i >= 0 {
			if filter < 0 || i > filter {
				t.Errorf("[%s] mcpListHints = %q, want the sign-out before the filter (index %d)", lang, hints, filter)
			}
			if toggle < 0 || i < toggle {
				t.Errorf("[%s] mcpListHints = %q, want the sign-out after the toggle (index %d)", lang, hints, toggle)
			}
		}
		if i := strings.Index(hints, add); i >= 0 {
			if filter < 0 || i > filter {
				t.Errorf("[%s] mcpListHints = %q, want the add hint before the filter (index %d)", lang, hints, filter)
			}
			if logoutIdx := strings.Index(hints, logout); logoutIdx < 0 || i < logoutIdx {
				t.Errorf("[%s] mcpListHints = %q, want the add hint after the sign-out (index %d)", lang, hints, logoutIdx)
			}
		}
	}
	i18n.SetLanguage(prev)

	logout, add := i18n.T("tui.mcpLogoutHint"), i18n.T("tui.mcpAddHint")
	out := ansi.Strip(m.renderModal(m.modalTitleFor(ModalMCP)))
	if !strings.Contains(out, logout) {
		t.Errorf("the MCP list footer does not advertise the sign-out (%q):\n%s", logout, out)
	}
	if !strings.Contains(out, add) {
		t.Errorf("the MCP list footer does not advertise the add wizard (%q):\n%s", add, out)
	}
	if !strings.Contains(out, i18n.T("tui.mcpListHints")) {
		t.Errorf("the MCP list footer lost its hints line:\n%s", out)
	}
}

func TestMCPAuthMethodLabel(t *testing.T) {
	if got := mcpAuthMethodLabel("oauth"); got != "oauth" {
		t.Errorf("mcpAuthMethodLabel(oauth) = %q, want oauth", got)
	}
	if got := mcpAuthMethodLabel(""); got != i18n.T("tui.mcpDetailNone") {
		t.Errorf("mcpAuthMethodLabel(\"\") = %q, want %q", got, i18n.T("tui.mcpDetailNone"))
	}
}

func TestMCPAuthTokenWithoutRequirementRendersAuthenticated(t *testing.T) {
	m := mcpAuthListModel(t, mcpAuthVisibilityDoc)
	mcpAuthSeedRecord(t, config.GetLeleDir(), "challenged-srv",
		mcp.AuthRecord{URL: mcpAuthVisibilityURL("challenged-srv"), Token: mcpTestToken()})

	paths, ok := m.mcpPaths()
	if !ok {
		t.Fatal("m.mcpPaths() = false, the fixture must resolve the MCP roots")
	}
	verdict := mcpAuthVerdicts(paths, mcp.ReadInventory(paths))["challenged-srv"]
	if verdict.Required || !verdict.Authenticated {
		t.Fatalf("verdict = %+v, want {Required:false Authenticated:true}", verdict)
	}
	authed := " " + i18n.T("tui.mcpAuthOK")
	if got := mcpAuthSuffix(verdict); got != authed {
		t.Errorf("mcpAuthSuffix(%+v) = %q, want %q", verdict, got, authed)
	}

	m.loadMCPList()
	row, _ := mcpAuthRow(t, m, "challenged-srv")
	if !strings.HasSuffix(row, authed) {
		t.Errorf("row = %q, want the %q marker", row, authed)
	}

	mcpAuthOpenDetail(t, m, "challenged-srv")
	out := ansi.Strip(m.renderMCPDetail())
	if want := i18n.T("tui.mcpDetailAuthState") + ": " + i18n.T("tui.mcpAuthOK"); !strings.Contains(out, want) {
		t.Errorf("detail missing %q:\n%s", want, out)
	}
	if !strings.Contains(out, i18n.T("tui.mcpLogoutHint")) {
		t.Errorf("detail does not advertise the sign-out:\n%s", out)
	}
	if strings.Contains(out, i18n.T("tui.mcpAuthRequired")) {
		t.Errorf("detail claims a sign-in is still owed for a signed-in server:\n%s", out)
	}
}

func TestMCPAuthLogoutCmdForClearsTheStoredCredential(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{LeleDir: filepath.Join(root, "global")}
	mcpWriteDoc(t, paths.LeleDir, mcpAuthVisibilityDoc)
	url := mcpAuthVisibilityURL("oauth-srv")
	mcpAuthSeedRecord(t, paths.LeleDir, "oauth-srv", mcp.AuthRecord{URL: url, Token: mcpTestToken()})

	if !mcp.NewAuthStore(paths.LeleDir).Authenticated("oauth-srv", url) {
		t.Fatal("precondition: the seeded credential must be usable")
	}

	msg, ok := mcpAuthLogoutCmdFor(paths, "oauth-srv")().(mcpToggleResultMsg)
	if !ok {
		t.Fatalf("logout cmd returned %T, want the MCP mutation result message",
			mcpAuthLogoutCmdFor(paths, "oauth-srv")())
	}
	if !msg.logout {
		t.Error("the result is not marked as a sign-out")
	}
	if msg.name != "oauth-srv" || msg.err != nil {
		t.Fatalf("result = %+v, want a successful sign-out of oauth-srv", msg)
	}
	if mcp.NewAuthStore(paths.LeleDir).Authenticated("oauth-srv", url) {
		t.Error("the token is still usable after the sign-out")
	}
	if _, ok := mcp.NewAuthStore(paths.LeleDir).Record("oauth-srv"); ok {
		t.Error("the record is still stored after the sign-out")
	}
}

func TestMCPAuthLogoutCmdForWithoutCredentialRefuses(t *testing.T) {
	t.Run("no record at all", func(t *testing.T) {
		root := t.TempDir()
		paths := mcp.Paths{LeleDir: filepath.Join(root, "global")}
		mcpWriteDoc(t, paths.LeleDir, mcpAuthVisibilityDoc)

		msg := mcpAuthLogoutCmdFor(paths, "oauth-srv")().(mcpToggleResultMsg)
		if !errors.Is(msg.err, errMCPAuthNoCredential) {
			t.Errorf("err = %v, want errMCPAuthNoCredential", msg.err)
		}
		if _, err := os.Stat(filepath.Join(paths.LeleDir, "mcp-auth.json")); err == nil {
			t.Error("a refused sign-out created mcp-auth.json")
		}
	})

	t.Run("record without a token", func(t *testing.T) {
		root := t.TempDir()
		paths := mcp.Paths{LeleDir: filepath.Join(root, "global")}
		mcpWriteDoc(t, paths.LeleDir, mcpAuthVisibilityDoc)
		mcpAuthSeedRecord(t, paths.LeleDir, "challenged-srv",
			mcp.AuthRecord{URL: mcpAuthVisibilityURL("challenged-srv"), AuthRequired: true})

		msg := mcpAuthLogoutCmdFor(paths, "challenged-srv")().(mcpToggleResultMsg)
		if !errors.Is(msg.err, errMCPAuthNoCredential) {
			t.Errorf("err = %v, want errMCPAuthNoCredential", msg.err)
		}
		if !mcp.NewAuthStore(paths.LeleDir).AuthRequired("challenged-srv", mcpAuthVisibilityURL("challenged-srv")) {
			t.Error("a refused sign-out dropped the recorded challenge")
		}
	})
}

func TestMCPAuthLogoutCmdWithoutLeleDir(t *testing.T) {
	paths := mcp.Paths{AgentWorkspace: t.TempDir()}

	msg := mcpAuthLogoutCmdFor(paths, "srv")().(mcpToggleResultMsg)
	if !errors.Is(msg.err, errMCPAuthUnavailableStore) {
		t.Errorf("err = %v, want errMCPAuthUnavailableStore", msg.err)
	}
	if errors.Is(msg.err, mcp.ErrAuthStoreDisabled) {
		t.Error("the raw engine sentinel leaked to the UI")
	}

	m := &Model{}
	got := m.mcpAuthLogoutCmd("srv")().(mcpToggleResultMsg)
	if !errors.Is(got.err, errMCPUnavailable) {
		t.Errorf("err = %v, want errMCPUnavailable", got.err)
	}
}

func TestMCPAuthLogoutResultCarriesTheOutcomeThroughUpdate(t *testing.T) {
	m := &Model{chatInput: textarea.New()}
	m.chatInput.SetValue("typed")

	_, cmd := m.Update(mcpAuthLogoutResult("srv", nil))
	if cmd == nil {
		t.Error("the handler did not re-arm the tick chain")
	}
	want := fmt.Sprintf(i18n.T("tui.mcpAuthLogoutDone"), "srv")
	if m.mcpFeedback != want {
		t.Errorf("feedback = %q, want %q", m.mcpFeedback, want)
	}
	if got := m.chatInput.Value(); got != "typed" {
		t.Errorf("text input mutated by the msg: %q", got)
	}

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"no store", errMCPAuthUnavailableStore, i18n.T("tui.mcpAuthUnavailable")},
		{"no credential", errMCPAuthNoCredential, fmt.Sprintf(i18n.T("tui.mcpAuthLogoutNone"), "srv")},
		{"unavailable", errMCPUnavailable, i18n.T("tui.mcpUnavailable")},
		{"store error", errors.New("disk on fire"), fmt.Sprintf(i18n.T("tui.mcpAuthLogoutFailed"), "srv", "disk on fire")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{chatInput: textarea.New()}
			m.Update(mcpAuthLogoutResult("srv", tc.err))
			if m.mcpFeedback != tc.want {
				t.Errorf("feedback = %q, want %q", m.mcpFeedback, tc.want)
			}
		})
	}
}

func TestMCPAuthLogoutKeyInTheListSignsOutAndRefreshesTheRow(t *testing.T) {
	syncs := countMCPSyncs(t)
	m := mcpAuthListModel(t, mcpAuthVisibilityDoc)
	dir := config.GetLeleDir()
	url := mcpAuthVisibilityURL("oauth-srv")
	mcpAuthSeedRecord(t, dir, "oauth-srv", mcp.AuthRecord{URL: url, Token: mcpTestToken()})
	m.loadMCPList()

	if row, _ := mcpAuthRow(t, m, "oauth-srv"); !strings.HasSuffix(row, " "+i18n.T("tui.mcpAuthOK")) {
		t.Fatalf("precondition: row = %q, want the authenticated marker", row)
	}
	mcpAuthSelectRow(t, m, "oauth-srv")

	cmd, handled := m.handleMCPKey(mcpKeyRunes('d'))
	if !handled || cmd == nil {
		t.Fatalf("d: handled=%v cmd=%v, want the sign-out command", handled, cmd)
	}
	m.Update(cmd())

	if mcp.NewAuthStore(dir).Authenticated("oauth-srv", url) {
		t.Error("the token is still usable after the sign-out")
	}
	if *syncs != 1 {
		t.Errorf("SyncMCPServers calls = %d, want exactly 1 (the live connection must see it)", *syncs)
	}
	if want := fmt.Sprintf(i18n.T("tui.mcpAuthLogoutDone"), "oauth-srv"); m.mcpFeedback != want {
		t.Errorf("feedback = %q, want %q", m.mcpFeedback, want)
	}
	row, _ := mcpAuthRow(t, m, "oauth-srv")
	if strings.Contains(row, i18n.T("tui.mcpAuthOK")) {
		t.Errorf("row still says authenticated after the sign-out: %q", row)
	}
	if !strings.HasSuffix(row, " "+i18n.T("tui.mcpAuthRequired")) {
		t.Errorf("row = %q, want the %q marker", row, i18n.T("tui.mcpAuthRequired"))
	}
	if m.modalMode != ModalMCP {
		t.Errorf("mode = %v, want to stay on the list", m.modalMode)
	}
}

func TestMCPAuthLogoutKeyInTheDetailStaysOnTheDetail(t *testing.T) {
	syncs := countMCPSyncs(t)
	m := mcpAuthListModel(t, mcpAuthVisibilityDoc)
	dir := config.GetLeleDir()
	mcpAuthSeedRecord(t, dir, "oauth-srv",
		mcp.AuthRecord{URL: mcpAuthVisibilityURL("oauth-srv"), Token: mcpTestToken()})
	m.loadMCPList()
	mcpAuthOpenDetail(t, m, "oauth-srv")

	cmd, handled := m.handleMCPKey(mcpKeyRunes('d'))
	if !handled || cmd == nil {
		t.Fatalf("d in detail: handled=%v cmd=%v, want the sign-out command", handled, cmd)
	}
	m.Update(cmd())

	if m.modalMode != ModalMCPDetail {
		t.Errorf("mode = %v, want to stay on ModalMCPDetail", m.modalMode)
	}
	want := fmt.Sprintf(i18n.T("tui.mcpAuthLogoutDone"), "oauth-srv")
	if m.mcpAuthFeedback != want {
		t.Errorf("detail feedback = %q, want %q", m.mcpAuthFeedback, want)
	}
	if m.mcpAuthError != "" {
		t.Errorf("detail error line set on a successful sign-out: %q", m.mcpAuthError)
	}
	if out := ansi.Strip(m.renderMCPDetail()); !strings.Contains(out, want) {
		t.Errorf("the detail does not render the sign-out outcome %q:\n%s", want, out)
	}
	if out := ansi.Strip(m.renderMCPDetail()); !strings.Contains(out, i18n.T("tui.mcpAuthRequired")) {
		t.Errorf("the refreshed detail still claims a credential:\n%s", out)
	}
	if *syncs != 1 {
		t.Errorf("SyncMCPServers calls = %d, want exactly 1", *syncs)
	}
}

func TestMCPAuthLogoutKeyWithoutCredentialIsAClearRefusal(t *testing.T) {
	m := mcpAuthListModel(t, mcpAuthVisibilityDoc)
	storeFile := filepath.Join(config.GetLeleDir(), "mcp-auth.json")
	if _, err := os.Stat(storeFile); err == nil {
		t.Fatalf("precondition: %s must not exist", storeFile)
	}
	mcpAuthSelectRow(t, m, "challenged-srv")

	cmd, handled := m.handleMCPKey(mcpKeyRunes('d'))
	if !handled || cmd == nil {
		t.Fatalf("d: handled=%v cmd=%v, want the sign-out command", handled, cmd)
	}
	m.Update(cmd())

	want := fmt.Sprintf(i18n.T("tui.mcpAuthLogoutNone"), "challenged-srv")
	if m.mcpFeedback != want {
		t.Errorf("feedback = %q, want %q", m.mcpFeedback, want)
	}
	if lying := fmt.Sprintf(i18n.T("tui.mcpAuthLogoutDone"), "challenged-srv"); m.mcpFeedback == lying {
		t.Errorf("a no-op sign-out was rendered as success: %q", lying)
	}
	if _, err := os.Stat(storeFile); err == nil {
		t.Error("a refused sign-out created a credential store file")
	}
}

func TestMCPAuthLogoutKeyOnTheEmptyStateRowIsASilentNoOp(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	m := &Model{modalMode: ModalMCP}
	m.loadMCPListWith(paths)
	if len(m.mcpModalKeys) != 1 || m.mcpModalKeys[0] != "" {
		t.Fatalf("keys = %v, want the single empty-state row", m.mcpModalKeys)
	}

	cmd, handled := m.handleMCPKey(mcpKeyRunes('d'))
	if !handled || cmd != nil {
		t.Fatalf("d: handled=%v cmd=%v, want a silent no-op", handled, cmd)
	}
	if m.mcpFeedback != "" {
		t.Errorf("the empty-state row produced feedback: %q", m.mcpFeedback)
	}
}

func TestResetModalClearsMCPAuth(t *testing.T) {
	paths := mcpFixturePaths(t)
	m := &Model{modalMode: ModalMCP}
	m.loadMCPListWith(paths)
	if len(m.mcpAuth) == 0 {
		t.Fatal("precondition: loadMCPListWith stored no auth verdicts")
	}
	if !m.mcpInventoryValid {
		t.Fatal("precondition: loadMCPListWith left mcpInventoryValid = false")
	}

	m.resetModal(ModalMCP)

	if m.mcpAuth != nil {
		t.Errorf("resetModal left %d auth verdicts behind", len(m.mcpAuth))
	}
	if m.mcpInventoryValid {
		t.Error("resetModal left mcpInventoryValid = true")
	}
	m.loadMCPListWith(paths)
	m = &Model{agentLoop: &agent.AgentLoop{}, modalMode: ModalMCP}
	m.mcpAuth = mcpAuthVerdicts(paths, mcp.ReadInventory(paths))
	m.loadMCPList()
	if m.mcpAuth != nil {
		t.Error("the MCP-unavailable branch kept a stale auth map")
	}
}
