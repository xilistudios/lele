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

// ── fixtures and seams ─────────────────────────────────────────────────

// mcpAuthFixtureDoc is one mcp.json holding the two shapes that matter: a
// remote entry (authorizable) and a stdio entry (not).
const mcpAuthFixtureDoc = `{"mcpServers":{` +
	`"remote-srv":{"url":"https://mcp.example.test/mcp","description":"remote"},` +
	`"local-srv":{"command":"true"}}}`

// mcpAuthListModel builds a Model the way the app does (newTestModel: real
// widgets, real agent loop, onboarding off) whose global MCP layer is a temp
// dir holding doc, and opens /mcp on it. Driving the real path (paths →
// inventory → Discover) is the point: the modal must see exactly what the
// visible rows were built from.
func mcpAuthListModel(t *testing.T, doc string) *Model {
	t.Helper()
	m := newTestModel(t)
	// The global layer is <LELE_CONFIG_DIR>/mcp.json — the same root
	// pkg/agent's MCP fixtures use.
	mcpWriteDoc(t, config.GetLeleDir(), doc)
	m.width, m.height = 140, 40

	m.executeCommand("/mcp") // the real entry point: resetModal + loadMCPList
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

// mcpAuthOpenDetail opens the detail modal of name through the real key path
// (enter on the row under the cursor).
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

// fakeMCPAuthFlow is a scripted mcpAuthFlow: it records the submits and the
// cancels and reports a status the test controls, so no engine — and no
// network — is involved anywhere in these tests. done mirrors the engine's
// Done(): "error" is ambiguous unless it is set.
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
		// A failed exchange is recorded as a retryable error: the flow stays
		// open exactly like the engine leaves it.
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

// mcpAuthStartCapture substitutes the startMCPAuthFlow seam for one test and records
// what the TUI asked the engine for.
type mcpAuthStartCapture struct {
	names []string
	srvs  []mcp.ServerConfig
	opts  []mcp.AuthOptions
	flow  mcpAuthFlow
	err   error
}

func (c *mcpAuthStartCapture) install(t *testing.T) {
	t.Helper()
	prev := startMCPAuthFlow
	startMCPAuthFlow = func(_ context.Context, name string, srv mcp.ServerConfig, _ *mcp.AuthStore, opts mcp.AuthOptions) (mcpAuthFlow, error) {
		c.names = append(c.names, name)
		c.srvs = append(c.srvs, srv)
		c.opts = append(c.opts, opts)
		if c.err != nil {
			return nil, c.err
		}
		return c.flow, nil
	}
	t.Cleanup(func() { startMCPAuthFlow = prev })
}

// countMCPSyncs substitutes the SyncMCPServers seam and returns the counter.
// A bare &Model{} cannot observe the real call (a bare loop's pass is a no-op),
// so the counted seam is what makes "exactly once" assertable; the option
// handled and the list reload are asserted separately.
func countMCPSyncs(t *testing.T) *int {
	t.Helper()
	prev := syncMCPServersFor
	n := 0
	syncMCPServersFor = func(*agent.AgentLoop) { n++ }
	t.Cleanup(func() { syncMCPServersFor = prev })
	return &n
}

// runCmd executes a command the model returned and applies its message through
// the real Update path — what bubbletea would do with it.
func runCmd(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command, got nil")
	}
	m.Update(cmd())
}

// mcpAuthSubmitKey presses ENTER on the auth modal and returns the submit
// command it produced.
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

// mcpAuthEnterFlow drives the whole real path to a live pending flow: list →
// detail of the remote server → "a" → the start command's message. It returns
// the model and the fake flow the substituted seam handed back.
func mcpAuthEnterFlow(t *testing.T, capture *mcpAuthStartCapture, flow *fakeMCPAuthFlow) *Model {
	t.Helper()
	capture.flow = flow
	capture.install(t)

	m := mcpAuthListModel(t, mcpAuthFixtureDoc)
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

// ── 1. the "a" entry point ─────────────────────────────────────────────

// TestMCPAuthKey_AOpensFlowOnRemoteEntryAndRefusesStdio drives both entry
// cases through handleMCPKey: a remote entry switches to ModalMCPAuth and asks
// the engine for exactly that server, a stdio entry stays on the detail and
// gets an inline hint with no engine call.
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
		// The TUI must NOT hand the engine a browser opener: it displays the
		// URL and offers CTRL+Y instead (a remote terminal has no browser).
		if capture.opts[0].OpenURL != nil {
			t.Error("AuthOptions.OpenURL is set; the TUI must display, not launch")
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
		// The hint must actually reach the screen (and the "A" key must be
		// discoverable, i.e. the detail hints still render).
		out := ansi.Strip(m.renderMCPDetail())
		if !strings.Contains(out, want) {
			t.Errorf("detail does not render the refusal hint %q:\n%s", want, out)
		}
		if !strings.Contains(out, i18n.T("tui.mcpDetailHints")) {
			t.Errorf("detail lost its hints line:\n%s", out)
		}
	})
}

// TestMCPAuthKey_AOnEntryMissingFromDiscoverShowsInlineError covers the
// disabled/invalid case: the row can exist in the list (a disabled entry is a
// row) while Discover excludes it, and the flow must not start.
func TestMCPAuthKey_AOnEntryMissingFromDiscoverShowsInlineError(t *testing.T) {
	capture := &mcpAuthStartCapture{}
	capture.install(t)
	// "off-srv" is remote-shaped but DISABLED: it is a list row, yet it is
	// absent from Discover.Servers.
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
	// The start command reports it instead: no engine call, inline error.
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

// ── 2. the happy path: start → poll → done (one sync, list reloaded) ───

func TestMCPAuthFlow_StartPollDoneSyncsOnceAndReloadsList(t *testing.T) {
	syncs := countMCPSyncs(t)
	flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize?state=s", status: mcpAuthStatusPending}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)

	// The pending modal shows the URL (wrapped, but present) and the hints.
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

	// A pending tick re-arms (the poll chain stays alive).
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

	// The flow completes: the tick notices it, reports, syncs once, reloads.
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

	// The list really was reloaded from the resolvable roots (observable
	// equivalent of the sync): the rows are back and still hold the server.
	found := false
	for _, key := range m.mcpModalKeys {
		if key == "remote-srv" {
			found = true
		}
	}
	if !found {
		t.Errorf("list not reloaded after the flow completed: keys = %v", m.mcpModalKeys)
	}

	// Terminal state: the poll chain is NOT re-armed.
	if _, cmd := m.Update(mcpAuthPollMsg{seq: seq}); cmd != nil {
		t.Error("a poll was re-armed after a terminal state")
	}
	// A stale duplicate of the success can never sync twice — neither through a
	// repeated poll...
	m.Update(mcpAuthPollMsg{seq: seq})
	if *syncs != 1 {
		t.Errorf("SyncMCPServers calls = %d after a duplicate poll, want 1", *syncs)
	}
	// ...nor through a duplicated delivery of the submit result, which passes
	// the generation check because it belongs to the live generation.
	m.Update(mcpAuthSubmitMsg{seq: m.mcpAuthSeq})
	if *syncs != 1 {
		t.Errorf("SyncMCPServers calls = %d after a duplicate success, want 1", *syncs)
	}
	if m.modalMode != ModalMCPAuth || m.mcpAuthState != mcpAuthSucceeded {
		t.Errorf("a duplicate success changed the modal: mode=%v state=%d", m.modalMode, m.mcpAuthState)
	}

	// The rendered done state carries the feedback line.
	if got := ansi.Strip(m.renderMCPAuth()); !strings.Contains(got, want) {
		t.Errorf("done modal does not render %q:\n%s", want, got)
	}
}

// TestMCPAuthFlow_TerminalErrorStopsTicking: an engine error that ENDED the
// flow (Done) leaves the modal failed with a value-free message, and the tick
// chain stops.
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
	// Never the raw engine string: the mapped sentinel must be used.
	if strings.Contains(m.mcpAuthError, mcp.ErrAuthTimedOut.Error()) {
		t.Errorf("terminal error leaked the engine text: %q", m.mcpAuthError)
	}
}

// TestMCPAuthFlow_RetryableErrorStaysPendingAndKeepsTicking pins the Done()
// distinction the engine documents: a FAILED TOKEN EXCHANGE records "error"
// while leaving the flow OPEN and retryable. A poll that saw "error" and
// stopped there would kill the flow's retry affordance (and let a still-valid
// ENTER fight a modal that has already declared failure).
func TestMCPAuthFlow_RetryableErrorStaysPendingAndKeepsTicking(t *testing.T) {
	flow := &fakeMCPAuthFlow{
		url:       "https://auth.example.test/authorize",
		status:    mcpAuthStatusPending,
		submitErr: mcp.ErrAuthNotSupported,
	}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)

	// The user submits a code and the exchange fails (the flow stays open).
	m.textInput.SetValue("A-CODE")
	runCmd(t, m, mcpAuthSubmitKey(t, m))
	if flow.Done() {
		t.Fatal("precondition: the failed exchange must leave the flow open")
	}

	// The tick that was already in flight lands now.
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
	// And ENTER still works: the retry succeeds on the same flow.
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

// TestMCPAuthFlow_StartErrorIsLocalizedPerSentinel: the start path translates
// the engine's sentinels in the Update handler (the Cmd goroutine never calls
// i18n) and never shows the raw text.
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

// TestMCPAuthSentinelText_ProbeFailedIsLocalizedInEveryLocale is the C6
// regression: ErrAuthProbeFailed is the sentinel an unreachable / DNS-failed
// MCP server reports, and it was the ONE sentinel missing from
// mcpAuthSentinelText — so an es/pt user read the engine's ENGLISH sentence
// ("authorization probe failed: the server did not complete the handshake").
//
// The wording deliberately does NOT say "authentication required" (the exact
// conflation the CLI's own comment warns about): the server did not answer, so
// nothing happened and the sign-in is still owed.
func TestMCPAuthSentinelText_ProbeFailedIsLocalizedInEveryLocale(t *testing.T) {
	prev := i18n.GetLanguage()
	t.Cleanup(func() { i18n.SetLanguage(prev) })

	// The error exactly as a surface sees it: the engine WRAPS the sentinel.
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
	// es/pt must really be translated, not a copy of the en sentence.
	for _, lang := range []string{"es", "pt"} {
		if seen[lang] == seen["en"] {
			t.Errorf("[%s] mcpAuthErrProbeFailed carries the English text: %q", lang, seen[lang])
		}
	}

	// And it reaches the screen through the start-error path, which is where
	// an unreachable server used to print the engine's English sentence.
	i18n.SetLanguage("es")
	if want, got := i18n.T("tui.mcpAuthErrProbeFailed"), mcpAuthStartErrorText("api", err); got != want {
		t.Errorf("mcpAuthStartErrorText = %q, want the es sentence %q", got, want)
	}
}

// TestMCPAuthFlow_LateStartResultIsCancelledNotAdopted is the leak guard of the
// in-flight start: leaving the modal while the engine is starting must cancel
// the flow that appears afterwards (its loopback listener is otherwise pinned
// for the whole budget) and must not resurrect the modal.
func TestMCPAuthFlow_LateStartResultIsCancelledNotAdopted(t *testing.T) {
	capture := &mcpAuthStartCapture{}
	flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize", status: mcpAuthStatusPending}
	capture.install(t)
	// The start command is slow: it is still in flight when ESC happens, so
	// no command has produced a flow yet.
	startMCPAuthFlow = func(_ context.Context, name string, _ mcp.ServerConfig, _ *mcp.AuthStore, _ mcp.AuthOptions) (mcpAuthFlow, error) {
		return flow, nil
	}

	m := mcpAuthListModel(t, mcpAuthFixtureDoc)
	mcpAuthOpenDetail(t, m, "remote-srv")
	cmd, _ := m.handleMCPKey(mcpKeyRunes('a'))
	if cmd == nil {
		t.Fatal("no start command")
	}
	// ESC first: the flow is still in flight, so there is nothing to cancel.
	if _, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEsc}); !handled {
		t.Fatal("esc was not handled")
	}
	if m.modalMode != ModalMCPDetail {
		t.Fatalf("mode after esc = %v, want ModalMCPDetail", m.modalMode)
	}
	// The start result lands now: the flow it carries must be released.
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

// ── 3. manual completion (paste) ───────────────────────────────────────

// TestMCPAuthManualSubmit_AcceptsEveryPasteShape drives ENTER through the real
// key path for each shape a redirect can produce, asserting the (code, state)
// the engine receives and that the widget is cleared.
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
			// The code must not linger in the widget while the exchange runs.
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
			// A successful submit completes the flow.
			if m.mcpAuthState != mcpAuthSucceeded {
				t.Errorf("state = %d, want mcpAuthSucceeded", m.mcpAuthState)
			}
		})
	}
}

// TestMCPAuthManualSubmit_EmptyAndMalformedInputNeverReachTheEngine: an empty
// field (or an unreadable paste the engine refuses) is an inline error with no
// engine call.
//
// The expected message is a CLOSURE, evaluated after the model exists: building
// the strings in the table would freeze the ambient locale, and opening the
// modal resets i18n to the configured default — the same reason the sibling
// refusal test does it this way.
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
		// The two shapes the OLD private TUI parser submitted as codes (it had
		// no shape check): an address-bar page of prose and a
		// parameter-shaped paste holding none of code/state/error. Both are
		// refusals, and nothing may reach the engine.
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

// TestMCPAuthManualSubmit_ExchangeFailureIsRetryableWithoutRestart: the engine
// keeps the flow open on a token-exchange failure, so the modal stays pending,
// shows the engine's (value-free) error and accepts another ENTER.
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

	// Retry (no restart, same flow) now succeeds.
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

// ── 4. CTRL+Y copies the URL ───────────────────────────────────────────

// TestMCPAuthKey_CtrlYCopiesTheAuthorizationURL: the key hands the URL to the
// shared clipboard helper (OSC 52 payload asserted on stdout, since the
// platform fallback runs in a goroutine) and reports it on the feedback line.
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

// TestMCPAuthKey_CtrlYWithNoURLReportsFailure mirrors copyLastAssistantMessage's
// "nothing to copy" reporting.
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

// ── 5. ESC cancels and returns to the detail ───────────────────────────

// TestMCPAuthKey_EscCancelsAndReturnsToTheDetail: the cancel key is ESC and
// only ESC ("q" is covered by TestMCPAuthKey_QInThePasteBoxDoesNotCancel).
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
		// The detail's own esc/q still goes back to the list.
		t.Errorf("second esc: mode=%v cmd=%v handled=%v, want ModalMCP with no cmd",
			m.modalMode, cmd, handled)
	}
}

// TestMCPAuthKey_QInThePasteBoxDoesNotCancel is the F2 regression: "q" is a
// REGULAR CHARACTER while the paste box is on screen (the rule every form
// modal follows). Before this, `case "esc", "q"` cancelled a live flow and
// freed its loopback port, so a hand-typed or pasted code or URL containing a
// "q" aborted the authorization in the middle of the paste.
func TestMCPAuthKey_QInThePasteBoxDoesNotCancel(t *testing.T) {
	flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize?state=s", status: mcpAuthStatusPending}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)

	// The key is NOT owned by the auth modal: that is what lets the generic
	// modal switch forward it to the shared text input.
	if cmd, handled := m.handleMCPKey(mcpKeyRunes('q')); handled || cmd != nil {
		t.Fatalf(`"q": handled=%v cmd=%v, want it passed through to the input`, handled, cmd)
	}

	// Type a paste containing "q" through the real key path (handleKeyMsg is
	// what Update calls, so this is the whole chain: handleMCPKey → generic
	// form-modal forwarding → textinput).
	m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q7-Zxq")}, nil)
	if got := m.textInput.Value(); got != "q7-Zxq" {
		t.Fatalf("text input = %q, want the typed paste including its q", got)
	}

	// The flow is still live: no cancel, no freed loopback port, no mode change.
	if flow.cancelCall != 0 {
		t.Errorf("flow cancels = %d, want 0 — a typed q must not abort the flow", flow.cancelCall)
	}
	if m.mcpAuthFlow == nil {
		t.Error("the flow was released by a keystroke")
	}
	if m.modalMode != ModalMCPAuth || m.mcpAuthState != mcpAuthPending {
		t.Fatalf("a typed q left the modal: mode=%v state=%d", m.modalMode, m.mcpAuthState)
	}

	// And the paste still submits normally: the q is part of the code.
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

// ── 6. leak guard: reopening /mcp releases the flow ────────────────────

// TestMCPAuthResetModalCancelsLiveFlow is the leak test: /mcp (which calls
// resetModal) must cancel the flow still alive behind ModalMCPAuth. Without the
// resetModal hook this test fails — the loopback listener would stay pinned for
// the flow's whole five-minute budget.
func TestMCPAuthResetModalCancelsLiveFlow(t *testing.T) {
	flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize", status: mcpAuthStatusPending}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)
	if m.mcpAuthFlow == nil {
		t.Fatal("precondition: no live flow")
	}
	// An unparsed paste is sitting in the input when the modal is abandoned.
	m.textInput.SetValue("UNPARSED-PASTED-CODE")

	m.executeCommand("/mcp") // the real entry point of resetModal

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
	// A message of the cancelled flow can never be adopted afterwards.
	old := m.mcpAuthSeq
	m.Update(mcpAuthStartMsg{seq: old - 1, name: "remote-srv", flow: flow})
	if m.mcpAuthFlow != nil || m.modalMode != ModalMCP {
		t.Errorf("a stale start message was adopted: flow=%v mode=%v", m.mcpAuthFlow, m.modalMode)
	}
}

// ── 7. rendering: no panic, honest height ──────────────────────────────

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
			out := m.renderMCPAuth() // must not panic
			if got := ansi.Strip(out); strings.Count(got, "\n")+1 > h {
				t.Errorf("height %d state %d: rendered frame has %d lines, want <= %d",
					h, state, strings.Count(got, "\n")+1, h)
			}
		}
	}
}

// TestRenderMCPAuth_WrapsTheURLInsideTheBox: an authorization URL has no
// spaces, so it must be broken by CELLS (the word-based wrapText would leave it
// one oversized line and the box would be clipped by the frame).
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
	// Every chunk survives: dropping the box frame and the whitespace the wrap
	// introduced reproduces the URL as one contiguous run.
	var joined strings.Builder
	for _, r := range box {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		if r >= 0x2500 && r <= 0x257F { // box drawing: the frame
			continue
		}
		joined.WriteRune(r)
	}
	if !strings.Contains(joined.String(), url) {
		t.Errorf("the wrapped URL was truncated or reordered:\n%s", box)
	}
}

// TestRenderMCPAuth_CloseHintMatchesWhatEscCanDo is the C8 regression: "ESC:
// cancel and go back to the server" was rendered in EVERY state, but once the
// flow succeeded or failed Cancel() is a no-op — no cancel can happen, the key
// only closes the modal, so the cancel wording over-promised. The split is the
// WebUI's (Cancel while the flow is pending, Close otherwise); the starting
// state keeps the cancel wording because ESC really does abort the in-flight
// attempt (it invalidates the generation, so the late flow cancels itself).
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

// ── 8. key routing (isFormModal / isListModal choices) ─────────────────

// TestMCPAuthKey_TypingReachesTheInputAndNeverMovesTheListCursor pins WHY
// ModalMCPAuth is a form modal and not a list modal: what consumes these keys is
// the generic modal switch (navigation, gated by isListModal) and the
// text-input forwarding at its tail (gated by isFormModal) — handleMCPAuthKey
// only owns ENTER/CTRL+Y/ESC/Q and must pass the rest through so a pasted code
// can be typed. It also pins the FOCUS: textinput.Update drops every key while
// the widget is blurred, so opening the modal must focus it.
func TestMCPAuthKey_TypingReachesTheInputAndNeverMovesTheListCursor(t *testing.T) {
	flow := &fakeMCPAuthFlow{url: "https://auth.example.test/authorize", status: mcpAuthStatusPending}
	m := mcpAuthEnterFlow(t, &mcpAuthStartCapture{}, flow)
	if !m.textInput.Focused() {
		t.Fatal("the code input is not focused: every keystroke would be dropped")
	}

	// A printable key reaches the shared text input (the paste area).
	m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("CO")}, nil)
	if got := m.textInput.Value(); got != "CO" {
		t.Errorf("text input = %q, want the typed keys to reach it", got)
	}
	// Navigation keys do not move the invisible list cursor behind the modal
	// (the cursor is preserved for the list the modal returns to).
	m.modalSelectedIdx = 1
	before := m.modalSelectedIdx
	for _, msg := range []tea.KeyMsg{{Type: tea.KeyUp}, {Type: tea.KeyDown}, mcpKeyRunes('j'), mcpKeyRunes('k')} {
		m.handleKeyMsg(msg, nil)
		if m.modalSelectedIdx != before {
			t.Fatalf("%q moved the hidden list cursor to %d", msg.String(), m.modalSelectedIdx)
		}
	}
	// And the typing is visible in the rendered modal.
	if out := ansi.Strip(m.renderMCPAuth()); !strings.Contains(out, "CO") {
		t.Errorf("the pasted text is not rendered:\n%s", out)
	}
}

// TestMCPAuthKey_EnterIsInertWhileStarting: ENTER before the URL exists must be
// swallowed (no engine call, no panic).
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

// ── 9. paste parsing: the engine's canonical grammar, no TUI copy ──────
//
// The TUI used to carry its OWN parser, and a client review proved it
// disagreed with the CLI and the WebUI: it had no code-shape check at all, so
// `!!!` or a page of browser prose was POSTed to the token endpoint as the
// authorization code, while `a b` parsed as (code, state) and `foo=bar` was
// submitted verbatim as a code. The TUI now delegates to mcp.ParseAuthPaste —
// the ONE canonical rule every surface compiles against — so this package
// keeps no private copy that could drift again.
//
// The table below is the frozen example table of
// .lele/tasks/mcp-oauth-paste-grammar.md, asserted THROUGH the engine helper
// the submit site calls (handleMCPAuthKey → mcp.ParseAuthPaste). Each row also
// pins the TUI's remaining half of the rule — which localized sentence the
// outcome maps to (mcpAuthInputErrorText) — and that a refused paste yields NO
// code, so the flow can never be fed a fabricated one.

// mcpAuthDeniedExpr is the "a refusal carrying exactly this sanitized code"
// expectation. A pointer distinguishes "a refusal with nothing safe to echo"
// ("" — its own sentence) from "not a refusal at all" (nil).
func mcpAuthDeniedExpr(code string) *string { return &code }

// TestMCPAuthParseInput_MatchesTheEngineGrammar is the pure table behind the
// delegation: the accepted shapes keep working, the refused shapes are refused
// by the ENGINE (not by a local rule), and the code echoed in a refusal is
// bounded by the engine's sanitizer.
func TestMCPAuthParseInput_MatchesTheEngineGrammar(t *testing.T) {
	// A real, opaque, JWE-length code: RFC 6749 §A.11 makes a code opaque, so
	// NOTHING here may impose a length rule (only the 8 KiB input bound).
	longCode := strings.Repeat("a", 1200)

	cases := []struct {
		name       string
		paste      string
		wantOK     bool
		wantCode   string
		wantState  string
		wantErr    error // the engine sentinel when !wantOK
		wantDenied *string
	}{
		// ── accepted ──
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
		// ── refused: a denial is the SERVER's own refusal ──
		{
			name: "denial redirect", paste: "http://127.0.0.1:8080/callback?error=access_denied&state=s",
			wantErr: mcp.ErrAuthPasteDenied, wantDenied: mcpAuthDeniedExpr("access_denied"),
		},
		{name: "denial query string", paste: "error=access_denied", wantErr: mcp.ErrAuthPasteDenied, wantDenied: mcpAuthDeniedExpr("access_denied")},
		{name: "denial with an RFC 6749 code", paste: "http://127.0.0.1:8080/callback?error=invalid_request", wantErr: mcp.ErrAuthPasteDenied, wantDenied: mcpAuthDeniedExpr("invalid_request")},
		{name: "denial with a hostile code", paste: "http://127.0.0.1:8080/callback?error=%1B%5B31mhax", wantErr: mcp.ErrAuthPasteDenied, wantDenied: mcpAuthDeniedExpr("")},
		{name: "error wins over a code", paste: "http://127.0.0.1:8080/cb?error=access_denied&code=abc", wantErr: mcp.ErrAuthPasteDenied, wantDenied: mcpAuthDeniedExpr("access_denied")},
		// ── refused: nothing was pasted ──
		{name: "empty", paste: "", wantErr: mcp.ErrAuthPasteEmpty},
		{name: "whitespace only", paste: "   ", wantErr: mcp.ErrAuthPasteEmpty},
		// ── refused: garbage, never submitted (the C1 divergence) ──
		{name: "punctuation", paste: "!!!", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "punctuation inside a token", paste: "abc!def", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "parameter with a space", paste: "code=abc def", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "three tokens", paste: "a b c", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "two tokens, one invalid", paste: "abc! xyz", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "parameter-shaped garbage", paste: "foo=bar", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "malformed query", paste: "code=%zz", wantErr: mcp.ErrAuthPasteUnrecognized},
		{name: "paste over the input bound", paste: strings.Repeat("a", 8200), wantErr: mcp.ErrAuthPasteUnrecognized},
		// ── refused: a response arrived, but carries no code ──
		{name: "malformed query in a redirect", paste: "http://127.0.0.1:8080/callback?code=%zz", wantErr: mcp.ErrAuthPasteNoCode},
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
			// The engine's contract: a refusal carries NOTHING to submit, so
			// no code can reach the flow by accident.
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
					// unrecognized and no-code share ONE sentence, which the
					// grammar explicitly allows.
					want = i18n.T("tui.mcpAuthBadInput")
				}
			}
			// The user-visible half: exactly this sentence, bounded, and never
			// the raw paste (a hostile `error` value must not reach the
			// terminal, not even as an escape sequence).
			if got := mcpAuthInputErrorText(err); got != want {
				t.Errorf("rendered message = %q, want %q", got, want)
			}
			if msg := mcpAuthInputErrorText(err); len(msg) > 200 || strings.ContainsRune(msg, '\x1b') {
				t.Errorf("rendered message is not bounded/clean: %q", msg)
			}
		})
	}
}

// TestMCPAuthManualSubmit_RefusalsNeverReachTheEngine drives the same shapes
// through the real key path: a refusal is an inline error, no Submit is made,
// the modal stays pending and the raw paste is never echoed unbounded.
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
			// The refusal is bounded: it never repeats the paste.
			if len(m.mcpAuthError) > 200 {
				t.Errorf("refusal message is %d bytes long (the paste was echoed)", len(m.mcpAuthError))
			}
			if m.mcpAuthState != mcpAuthPending {
				t.Errorf("state = %d, want to stay pending (the flow is still live)", m.mcpAuthState)
			}
			// The refused paste stays in the widget so the user can fix it
			// (the code-clearing branch is only for a PARSED code).
			if got := m.textInput.Value(); got != tc.paste {
				t.Errorf("input = %q, want the refused paste to stay for editing", got)
			}
		})
	}
}

// ── 10. auth visibility (list marker + detail block) ───────────────────

// mcpAuthVisibilityDoc is the fixture of the visibility and sign-out tests:
// a remote entry that DECLARES OAuth, a plain remote entry (flagged only once
// a 401 was recorded) and a stdio entry (never auth-bearing).
const mcpAuthVisibilityDoc = `{"mcpServers":{` +
	`"oauth-srv":{"url":"https://oauth.example.test/mcp","auth":{"type":"oauth"}},` +
	`"challenged-srv":{"url":"https://challenged.example.test/mcp"},` +
	`"local-srv":{"command":"true"}}}`

// mcpAuthVisibilityURL is the URL the fixture entry carries (records are bound
// to it: a record saved for another URL is deliberately ignored by the store).
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

// mcpTestToken is a usable stored token (unexpired, so the store reports it).
func mcpTestToken() *transport.Token {
	return &transport.Token{
		AccessToken: "stored-access-token",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
}

// mcpAuthSeedRecord writes one record through the real store.
func mcpAuthSeedRecord(t *testing.T, dir, name string, rec mcp.AuthRecord) {
	t.Helper()
	if err := mcp.NewAuthStore(dir).Save(name, rec); err != nil {
		t.Fatalf("seed auth record for %s in %s: %v", name, dir, err)
	}
}

// mcpAuthRow returns the rendered row of name and its index in the list.
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

// mcpAuthSelectRow puts the cursor on name.
func mcpAuthSelectRow(t *testing.T, m *Model, name string) {
	t.Helper()
	_, idx := mcpAuthRow(t, m, name)
	m.modalSelectedIdx = idx
}

func TestFormatMCPItemAuthSuffix(t *testing.T) {
	pad := strings.Repeat(" ", 12) // %-15s of "srv"
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
			// The pre-existing shape is untouched: the marker is APPENDED,
			// never interleaved with the glyph/padding/tag.
			if !strings.HasPrefix(got, base) {
				t.Errorf("row %q lost its base shape %q", got, base)
			}
		})
	}
}

// TestMCPAuthSuffixPerVerdict pins the marker of every verdict the list can
// hold, including the two that must stay silent (no auth required and no
// credential stored, no verdict at all).
//
// The rule is the WebUI's one: `authenticated` FIRST, then `required`, else
// nothing — `authenticated` is "a usable credential exists, no sign-in is
// needed" (mcp.CheckAuth) and is orthogonal to `required`, so a verdict with a
// token and no requirement MUST say "authenticated" instead of rendering
// nothing while the WebUI shows its "Authenticated" chip for the same DTO.
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

// TestMCPAuthVerdictsRemoteOAuthAndChallenge covers the two ways an entry
// becomes auth-bearing (a declared OAuth block, a recorded 401) and the URL
// binding of the stored record.
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

	// No store record at all: the declared OAuth block requires a sign-in…
	if st := verdict("oauth-srv"); !st.Required || st.Authenticated || st.Method != "oauth" {
		t.Errorf("oauth-srv without a record = %+v, want required oauth, not authenticated", st)
	}
	// …a plain remote entry is NOT flagged (Required is never inferred)…
	if st := verdict("challenged-srv"); st.Required || st.Authenticated || st.Method != "" {
		t.Errorf("challenged-srv without a challenge = %+v, want the zero verdict", st)
	}
	// …and a stdio entry is never auth-bearing.
	if st := verdict("local-srv"); st != (mcp.AuthStatus{}) {
		t.Errorf("local-srv = %+v, want the zero verdict", st)
	}

	// A token bound to the entry's URL authenticates it.
	mcpAuthSeedRecord(t, paths.LeleDir, "oauth-srv",
		mcp.AuthRecord{URL: mcpAuthVisibilityURL("oauth-srv"), Token: mcpTestToken()})
	if st := verdict("oauth-srv"); !st.Required || !st.Authenticated || st.Method != "oauth" {
		t.Errorf("oauth-srv with a matching token = %+v, want required + authenticated", st)
	}

	// A record bound to ANOTHER url is deliberately ignored: it belongs to a
	// different resource and presenting it would be a protocol error.
	mcpAuthSeedRecord(t, paths.LeleDir, "oauth-srv",
		mcp.AuthRecord{URL: "https://other.example.test/mcp", Token: mcpTestToken()})
	if st := verdict("oauth-srv"); st.Authenticated {
		t.Errorf("oauth-srv with a mismatched-url record = %+v, want not authenticated", st)
	}

	// A RECORDED 401 marks a plain remote entry (no auth block declared).
	mcpAuthSeedRecord(t, paths.LeleDir, "challenged-srv",
		mcp.AuthRecord{URL: mcpAuthVisibilityURL("challenged-srv"), AuthRequired: true})
	if st := verdict("challenged-srv"); !st.Required || st.Authenticated || st.Method != "" {
		t.Errorf("challenged-srv with a recorded 401 = %+v, want required, no carrier named", st)
	}
	// …and a token for it authenticates it, with Method still "" (the remedy
	// was the zero-config login: nothing declares a carrier).
	mcpAuthSeedRecord(t, paths.LeleDir, "challenged-srv",
		mcp.AuthRecord{URL: mcpAuthVisibilityURL("challenged-srv"), AuthRequired: true, Token: mcpTestToken()})
	if st := verdict("challenged-srv"); !st.Required || !st.Authenticated || st.Method != "" {
		t.Errorf("challenged-srv with a token = %+v, want required + authenticated, no carrier", st)
	}
}

// TestMCPAuthVerdictsHeadersAndStdio: a header carrier is NAMED but is not
// itself evidence of auth (MCP headers are routinely non-credential), and a
// stdio entry gets no verdict at all.
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
	// A recorded 401 turns the same entry into a required one, with the
	// header carrier still named.
	mcpAuthSeedRecord(t, paths.LeleDir, "hdr-srv",
		mcp.AuthRecord{URL: "https://hdr.example.test/mcp", AuthRequired: true})
	if st := mcpAuthVerdicts(paths, mcp.ReadInventory(paths))["hdr-srv"]; !st.Required || st.Method != "headers" {
		t.Errorf("hdr-srv with a recorded 401 = %+v, want required + method=headers", st)
	}
}

// TestMCPAuthVerdictsEmptyLeleDir: with no store (an empty LeleDir) there is
// nothing to report, no panic and no verdict — the modal must keep working.
func TestMCPAuthVerdictsEmptyLeleDir(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{AgentWorkspace: root}
	mcpWriteDoc(t, root, mcpAuthVisibilityDoc)

	verdicts := mcpAuthVerdicts(paths, mcp.ReadInventory(paths))
	if len(verdicts) != 0 {
		t.Errorf("verdicts with a disabled store = %v, want an empty map", verdicts)
	}
}

// TestMCPListShowsAuthSuffix is the end-to-end marker: the row of the remote
// entry carries it, the stdio row does not, and a stored token flips it.
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

	// A stored credential flips the marker to "authenticated" (the store is
	// re-read with the list, exactly like the inventory).
	mcpAuthSeedRecord(t, config.GetLeleDir(), "oauth-srv",
		mcp.AuthRecord{URL: mcpAuthVisibilityURL("oauth-srv"), Token: mcpTestToken()})
	m.loadMCPList()
	row, _ = mcpAuthRow(t, m, "oauth-srv")
	if !strings.HasSuffix(row, " "+authed) {
		t.Errorf("authenticated oauth-srv row = %q, want the %q marker", row, authed)
	}
}

// TestMCPDetailRendersAuthBlock: the detail names the carrier and the state,
// advertises the sign-out, and renders NEITHER for an entry with no auth.
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

	// A stored credential: the state flips, the block (and the hint) stay.
	mcpAuthSeedRecord(t, config.GetLeleDir(), "oauth-srv",
		mcp.AuthRecord{URL: mcpAuthVisibilityURL("oauth-srv"), Token: mcpTestToken()})
	m.loadMCPList()
	out = ansi.Strip(m.renderMCPDetail())
	if want := i18n.T("tui.mcpDetailAuthState") + ": " + i18n.T("tui.mcpAuthOK"); !strings.Contains(out, want) {
		t.Errorf("detail missing %q:\n%s", want, out)
	}

	// A stdio entry: neither line, and no sign-out hint either (there is no
	// credential to take back).
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

// TestMCPListHintsAdvertiseSignOut: the LIST footer must advertise the
// sign-out key like every other action — "d" is wired in the list branch too,
// so a user scanning the list has to be able to discover it — and it must be
// the same action label the detail shows, in every builtin locale.
func TestMCPListHintsAdvertiseSignOut(t *testing.T) {
	m := mcpAuthListModel(t, mcpAuthVisibilityDoc)

	// (1) EVERY builtin locale must carry the fragment, in the sibling
	// fragments' capitalised style and order: after the toggle, before the
	// filter. Checking all three (not just the active one) is deliberate —
	// T() falls back to the default locale, so a fragment dropped from one
	// file would otherwise stay invisible.
	prev := i18n.GetLanguage()
	t.Cleanup(func() { i18n.SetLanguage(prev) })
	for _, lang := range []string{"en", "es", "pt"} {
		i18n.SetLanguage(lang)
		hints, logout := i18n.T("tui.mcpListHints"), i18n.T("tui.mcpLogoutHint")
		if !strings.Contains(hints, logout) {
			t.Errorf("[%s] mcpListHints = %q, does not advertise the sign-out %q", lang, hints, logout)
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
	}
	i18n.SetLanguage(prev)

	// (2) The footer the user actually sees is built by renderModal; assert on
	// it too, so a fragment wired only into the string but not the footer —
	// or a footer that stopped reading the key — fails here.
	logout := i18n.T("tui.mcpLogoutHint")
	out := ansi.Strip(m.renderModal(m.modalTitleFor(ModalMCP)))
	if !strings.Contains(out, logout) {
		t.Errorf("the MCP list footer does not advertise the sign-out (%q):\n%s", logout, out)
	}
	if !strings.Contains(out, i18n.T("tui.mcpListHints")) {
		t.Errorf("the MCP list footer lost its hints line:\n%s", out)
	}
}

// TestMCPAuthMethodLabel: a server that answered 401 without declaring a
// carrier still gets an honest value instead of an invented "oauth".
func TestMCPAuthMethodLabel(t *testing.T) {
	if got := mcpAuthMethodLabel("oauth"); got != "oauth" {
		t.Errorf("mcpAuthMethodLabel(oauth) = %q, want oauth", got)
	}
	if got := mcpAuthMethodLabel(""); got != i18n.T("tui.mcpDetailNone") {
		t.Errorf("mcpAuthMethodLabel(\"\") = %q, want %q", got, i18n.T("tui.mcpDetailNone"))
	}
}

// TestMCPAuthTokenWithoutRequirementRendersAuthenticated is the C7 regression,
// driven through the REAL store: a plain remote entry (no declared auth block,
// no recorded 401) holding a usable credential is `{Required:false,
// Authenticated:true}` — the residue pkg/mcp documents (a stored credential
// whose AuthRequired was never written). Both TUI surfaces keyed on Required
// and rendered NOTHING for it, while the WebUI painted its "Authenticated"
// chip + Sign out for the very same DTO. One verdict, one rule, on every
// surface:
//
//	authenticated ⇒ authenticated; else required ⇒ needs-auth; else nothing
func TestMCPAuthTokenWithoutRequirementRendersAuthenticated(t *testing.T) {
	m := mcpAuthListModel(t, mcpAuthVisibilityDoc)
	// A token with NO recorded challenge: the store answers Authenticated,
	// mcp.json declares nothing and no 401 was ever seen.
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

	// The list row says it…
	m.loadMCPList()
	row, _ := mcpAuthRow(t, m, "challenged-srv")
	if !strings.HasSuffix(row, authed) {
		t.Errorf("row = %q, want the %q marker", row, authed)
	}

	// …and so does the detail: the state line, plus the sign-out that is
	// exactly what takes that credential back.
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

// ── 11. sign out ───────────────────────────────────────────────────────

// TestMCPAuthLogoutCmdForClearsTheStoredCredential drives the command itself:
// it runs the same in-process Logout the CLI runs, and the credential is
// really gone from a freshly built store (what another process would read).
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

// TestMCPAuthLogoutCmdForWithoutCredentialRefuses: the store's Clear is
// idempotent, so a sign-out of an entry with nothing stored must be refused
// here — reporting success would claim a deletion that never happened.
func TestMCPAuthLogoutCmdForWithoutCredentialRefuses(t *testing.T) {
	t.Run("no record at all", func(t *testing.T) {
		root := t.TempDir()
		paths := mcp.Paths{LeleDir: filepath.Join(root, "global")}
		mcpWriteDoc(t, paths.LeleDir, mcpAuthVisibilityDoc)

		msg := mcpAuthLogoutCmdFor(paths, "oauth-srv")().(mcpToggleResultMsg)
		if !errors.Is(msg.err, errMCPAuthNoCredential) {
			t.Errorf("err = %v, want errMCPAuthNoCredential", msg.err)
		}
		// Nothing was created: a refusal must not leave a store file behind.
		if _, err := os.Stat(filepath.Join(paths.LeleDir, "mcp-auth.json")); err == nil {
			t.Error("a refused sign-out created mcp-auth.json")
		}
	})

	t.Run("record without a token", func(t *testing.T) {
		root := t.TempDir()
		paths := mcp.Paths{LeleDir: filepath.Join(root, "global")}
		mcpWriteDoc(t, paths.LeleDir, mcpAuthVisibilityDoc)
		// A 401 was recorded, but no login ever completed: there is no
		// credential to take back.
		mcpAuthSeedRecord(t, paths.LeleDir, "challenged-srv",
			mcp.AuthRecord{URL: mcpAuthVisibilityURL("challenged-srv"), AuthRequired: true})

		msg := mcpAuthLogoutCmdFor(paths, "challenged-srv")().(mcpToggleResultMsg)
		if !errors.Is(msg.err, errMCPAuthNoCredential) {
			t.Errorf("err = %v, want errMCPAuthNoCredential", msg.err)
		}
		// The recorded challenge is untouched.
		if !mcp.NewAuthStore(paths.LeleDir).AuthRequired("challenged-srv", mcpAuthVisibilityURL("challenged-srv")) {
			t.Error("a refused sign-out dropped the recorded challenge")
		}
	})
}

// TestMCPAuthLogoutCmdWithoutLeleDir: no store at all is its own refusal, not
// pkg/mcp's "store disabled" engine error.
func TestMCPAuthLogoutCmdWithoutLeleDir(t *testing.T) {
	paths := mcp.Paths{AgentWorkspace: t.TempDir()}

	msg := mcpAuthLogoutCmdFor(paths, "srv")().(mcpToggleResultMsg)
	if !errors.Is(msg.err, errMCPAuthUnavailableStore) {
		t.Errorf("err = %v, want errMCPAuthUnavailableStore", msg.err)
	}
	if errors.Is(msg.err, mcp.ErrAuthStoreDisabled) {
		t.Error("the raw engine sentinel leaked to the UI")
	}

	// And the model-level builder reports the unresolvable roots instead of
	// panicking (a bare loop resolves no MCP paths).
	m := &Model{}
	got := m.mcpAuthLogoutCmd("srv")().(mcpToggleResultMsg)
	if !errors.Is(got.err, errMCPUnavailable) {
		t.Errorf("err = %v, want errMCPUnavailable", got.err)
	}
}

// TestMCPAuthLogoutResultCarriesTheOutcomeThroughUpdate is the routing test:
// the sign-out result reaches the handler through update()'s real dispatch
// (the same one the toggle uses), lands on the feedback line and never leaks
// into the chat input.
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

	// Every refusal is localized, never a raw sentinel.
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

// TestMCPAuthLogoutKeyInTheListSignsOutAndRefreshesTheRow is the whole feature
// end to end on the real model: "d" on a row with a stored credential deletes
// it, re-wires the loop once, reloads the list (the marker is gone) and
// reports on the list's feedback line.
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

	// The credential is gone for a fresh store (another process would agree).
	if mcp.NewAuthStore(dir).Authenticated("oauth-srv", url) {
		t.Error("the token is still usable after the sign-out")
	}
	if *syncs != 1 {
		t.Errorf("SyncMCPServers calls = %d, want exactly 1 (the live connection must see it)", *syncs)
	}
	if want := fmt.Sprintf(i18n.T("tui.mcpAuthLogoutDone"), "oauth-srv"); m.mcpFeedback != want {
		t.Errorf("feedback = %q, want %q", m.mcpFeedback, want)
	}
	// The list was reloaded and the row lost its marker (it needs a sign-in
	// again: the entry declares OAuth).
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

// TestMCPAuthLogoutKeyInTheDetailStaysOnTheDetail: a sign-out fired from the
// detail reports on the detail's own auth line (the list's feedback line is not
// rendered there) and keeps the detail on screen so the refreshed state is
// visible.
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
	// The marker is gone from the refreshed detail too.
	if out := ansi.Strip(m.renderMCPDetail()); !strings.Contains(out, i18n.T("tui.mcpAuthRequired")) {
		t.Errorf("the refreshed detail still claims a credential:\n%s", out)
	}
	if *syncs != 1 {
		t.Errorf("SyncMCPServers calls = %d, want exactly 1", *syncs)
	}
}

// TestMCPAuthLogoutKeyWithoutCredentialIsAClearRefusal: "d" on a row with
// nothing stored is not a silent no-op — the outcome line says so, and the
// store is untouched (no file is even created).
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

// TestMCPAuthLogoutKeyOnTheEmptyStateRowIsASilentNoOp: the synthetic
// ""/unavailable rows have no server, so "d" does nothing at all (no command,
// no feedback) — exactly like enter.
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

// TestResetModalClearsMCPAuth: the verdict map belongs to the inventory
// snapshot and goes with it, so a stale marker can never survive a reopen.
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
	// The same holds for the unresolvable-paths branch of loadMCPList.
	m.loadMCPListWith(paths)
	m = &Model{agentLoop: &agent.AgentLoop{}, modalMode: ModalMCP}
	m.mcpAuth = mcpAuthVerdicts(paths, mcp.ReadInventory(paths))
	m.loadMCPList()
	if m.mcpAuth != nil {
		t.Error("the MCP-unavailable branch kept a stale auth map")
	}
}
