package tui

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/mcp/authtest"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// This file drives ModalMCPAuth against the REAL engine (pkg/mcp.StartAuth and
// pkg/mcp/authtest's in-process authorization server): no seam is substituted,
// so it is the test that proves the TUI is wired to the engine's actual
// contract — the ServerConfig it hands over, the "pending"/"done"/"error"
// strings it compares against, the loopback listener it must release, and the
// credential that ends up in the real store.
//
// The two browser-side steps (GET the authorization URL, follow its 302 to the
// loopback redirect) are done with plain HTTP here: that is exactly what the
// user's browser does, and it keeps the test free of engine internals.

// mcpAuthE2EServer starts the in-process authorization server and returns the
// mcp.json entry that points a remote server at it.
func mcpAuthE2EServer(t *testing.T) (string, string) {
	t.Helper()
	as := authtest.New(authtest.WithScopes("mcp"))
	srv := httptest.NewServer(as.Handler())
	t.Cleanup(srv.Close)
	as.SetIssuer(srv.URL)

	resourceURL := srv.URL + authtest.DefaultResourcePath
	doc := fmt.Sprintf(`{"mcpServers":{"remote-srv":{"url":%q,"type":"http"}}}`, resourceURL)
	return doc, resourceURL
}

// mcpAuthNoRedirectClient stops at the authorization server's 302 (the test
// must see the code it carries, not chase it into the loopback server).
func mcpAuthNoRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// mcpAuthPlayBrowser runs the browser half of the flow: it opens the
// authorization URL, reads the 302 the authorization server answers with and
// delivers the code/state it carries to the loopback redirect URI the flow
// registered.
func mcpAuthPlayBrowser(t *testing.T, authURL string) {
	t.Helper()
	hc := mcpAuthNoRedirectClient()

	resp, err := hc.Get(authURL)
	if err != nil {
		t.Fatalf("GET authorization URL: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorization status = %d, want 302", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse authorization redirect: %v", err)
	}
	if loc.Query().Get("code") == "" {
		t.Fatalf("authorization redirect carried no code: %s", loc)
	}

	cb, err := hc.Get(loc.String())
	if err != nil {
		t.Fatalf("GET loopback callback: %v", err)
	}
	_ = cb.Body.Close()
	if cb.StatusCode != http.StatusOK {
		t.Fatalf("loopback callback status = %d, want 200", cb.StatusCode)
	}
}

// mcpAuthPollUntilSettled drives the modal's own poll until it leaves the
// pending state (the completion is observed by a tick, exactly like in the
// running TUI). It is bounded so a regression fails instead of hanging.
func mcpAuthPollUntilSettled(t *testing.T, m *Model) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for m.mcpAuthState == mcpAuthPending {
		if time.Now().After(deadline) {
			t.Fatalf("the flow never settled (state %d, error %q)", m.mcpAuthState, m.mcpAuthError)
		}
		// The command returned by the poll is the next tick: the test drives
		// the chain itself instead of waiting a real second per tick.
		m.Update(mcpAuthPollMsg{seq: m.mcpAuthSeq})
		if m.mcpAuthState == mcpAuthPending {
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// TestMCPAuthEngine_LoopbackFlowCompletesThroughTheTUI is the end-to-end
// proof: "a" on the detail of a real remote server starts the real flow, the
// authorization URL completes through the real loopback redirect, the poll
// notices "done", the credential lands in the real AuthStore and the agent
// loop is re-wired exactly once.
func TestMCPAuthEngine_LoopbackFlowCompletesThroughTheTUI(t *testing.T) {
	doc, resourceURL := mcpAuthE2EServer(t)
	syncs := countMCPSyncs(t)
	// This test drives the REAL StartAuth, so the opener it injects is the real
	// launcher unless it is replaced here: the recorder also proves the engine
	// received the authorization URL it must launch.
	m := mcpAuthListModel(t, doc)
	opened := mcpAuthRecordingOpener(m, nil)
	mcpAuthOpenDetail(t, m, "remote-srv")

	cmd, handled := m.handleMCPKey(mcpKeyRunes('a'))
	if !handled || cmd == nil {
		t.Fatal(`"a" on a remote entry produced no start command`)
	}
	runCmd(t, m, cmd) // the REAL StartAuth: probe, register, listen, generate

	if m.mcpAuthState != mcpAuthPending {
		t.Fatalf("state = %d (error %q), want mcpAuthPending", m.mcpAuthState, m.mcpAuthError)
	}
	authURL, err := url.Parse(m.mcpAuthURL)
	if err != nil {
		t.Fatalf("parse authorization URL %q: %v", m.mcpAuthURL, err)
	}
	if authURL.Query().Get("code_challenge_method") != "S256" {
		t.Errorf("authorization URL = %q, want a PKCE S256 challenge", m.mcpAuthURL)
	}
	// The TUI shows a loopback URL: this is what makes the modal's own copy of
	// the URL usable by a browser on the same machine.
	if !strings.HasPrefix(authURL.Query().Get("redirect_uri"), "http://127.0.0.1:") {
		t.Errorf("redirect_uri = %q, want a loopback URI", authURL.Query().Get("redirect_uri"))
	}
	// The engine launched the browser automatically with the very URL the
	// modal displays — one launch, not zero (the old display-only behaviour)
	// and not two.
	if got := *opened; len(got) != 1 || got[0] != m.mcpAuthURL {
		t.Errorf("browser launches = %v, want exactly [%s]", got, m.mcpAuthURL)
	}
	if m.mcpAuthFeedback != i18n.T("tui.mcpAuthBrowserOpened") {
		t.Errorf("pending feedback = %q, want the browser-opened line %q",
			m.mcpAuthFeedback, i18n.T("tui.mcpAuthBrowserOpened"))
	}

	mcpAuthPlayBrowser(t, m.mcpAuthURL)
	mcpAuthPollUntilSettled(t, m)

	if m.mcpAuthState != mcpAuthSucceeded {
		t.Fatalf("state = %d (error %q), want mcpAuthSucceeded", m.mcpAuthState, m.mcpAuthError)
	}
	want := strings.Replace(i18n.T("tui.mcpAuthSuccess"), "%s", "remote-srv", 1)
	if m.mcpAuthFeedback != want {
		t.Errorf("feedback = %q, want %q", m.mcpAuthFeedback, want)
	}
	if *syncs != 1 {
		t.Errorf("SyncMCPServers calls = %d, want exactly 1", *syncs)
	}
	// The credential really is in the store the TUI would hand the agent loop.
	store := mcp.NewAuthStore(config.GetLeleDir())
	if !store.Authenticated("remote-srv", resourceURL) {
		t.Fatal("the store has no usable credential after the flow reported success")
	}
	// The flow is released by the success itself (its listener did its job).
	if m.mcpAuthFlow == nil || !m.mcpAuthFlow.Done() {
		t.Error("the flow is not Done() after a successful authorization")
	}
}

// TestMCPAuthEngine_CancelReleasesTheLoopbackListener is the leak proof against
// the REAL engine: ESC must cancel the flow AND free the loopback port, so a
// connection to the redirect URI is refused afterwards. A TUI that only
// switched modal state would leave the port pinned for the flow's budget.
func TestMCPAuthEngine_CancelReleasesTheLoopbackListener(t *testing.T) {
	doc, _ := mcpAuthE2EServer(t)

	m := mcpAuthListModel(t, doc)
	mcpAuthRecordingOpener(m, nil) // never spawn the real browser
	mcpAuthOpenDetail(t, m, "remote-srv")
	cmd, _ := m.handleMCPKey(mcpKeyRunes('a'))
	runCmd(t, m, cmd)
	if m.mcpAuthState != mcpAuthPending {
		t.Fatalf("state = %d (error %q), want mcpAuthPending", m.mcpAuthState, m.mcpAuthError)
	}

	authURL, err := url.Parse(m.mcpAuthURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	redirectURI := authURL.Query().Get("redirect_uri")
	flow := m.mcpAuthFlow

	// The listener is bound while the flow is live. A plain TCP connect is
	// used on purpose: an HTTP request carrying a (bogus) code would be a real
	// completion attempt, and its failure would race the cancel for the flow's
	// one-shot terminal outcome.
	uri, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("parse redirect_uri %q: %v", redirectURI, err)
	}
	if err := mcpAuthDialLoopback(uri.Host); err != nil {
		t.Fatalf("the loopback listener is not accepting connections before the cancel: %v", err)
	}

	if _, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEsc}); !handled {
		t.Fatal("esc was not handled")
	}
	if m.mcpAuthFlow != nil || m.modalMode != ModalMCPDetail {
		t.Fatalf("cancel left the modal in mode=%v flow=%v", m.modalMode, m.mcpAuthFlow)
	}

	// The engine flow itself is terminal and reports the cancel.
	status, ferr := flow.Status()
	if status != mcpAuthStatusError || !errors.Is(ferr, mcp.ErrAuthCanceled) {
		t.Errorf("Status = (%q, %v), want (\"error\", ErrAuthCanceled)", status, ferr)
	}
	if !flow.Done() {
		t.Error("Done = false after a cancel")
	}
	// And the port is gone: the same connect now fails.
	if err := mcpAuthDialLoopback(uri.Host); err == nil {
		t.Error("the loopback listener still accepts connections after the cancel")
	}
}

// mcpAuthDialLoopback opens (and immediately closes) a TCP connection to a
// loopback host:port. It observes the LISTENER without talking to it.
func mcpAuthDialLoopback(hostPort string) error {
	conn, err := net.DialTimeout("tcp", hostPort, time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}
