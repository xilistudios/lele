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

func mcpAuthNoRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

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

func mcpAuthPollUntilSettled(t *testing.T, m *Model) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for m.mcpAuthState == mcpAuthPending {
		if time.Now().After(deadline) {
			t.Fatalf("the flow never settled (state %d, error %q)", m.mcpAuthState, m.mcpAuthError)
		}
		m.Update(mcpAuthPollMsg{seq: m.mcpAuthSeq})
		if m.mcpAuthState == mcpAuthPending {
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestMCPAuthEngine_LoopbackFlowCompletesThroughTheTUI(t *testing.T) {
	doc, resourceURL := mcpAuthE2EServer(t)
	syncs := countMCPSyncs(t)
	m := mcpAuthListModel(t, doc)
	opened := mcpAuthRecordingOpener(m, nil)
	mcpAuthOpenDetail(t, m, "remote-srv")

	cmd, handled := m.handleMCPKey(mcpKeyRunes('a'))
	if !handled || cmd == nil {
		t.Fatal(`"a" on a remote entry produced no start command`)
	}
	runCmd(t, m, cmd)

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
	if !strings.HasPrefix(authURL.Query().Get("redirect_uri"), "http://127.0.0.1:") {
		t.Errorf("redirect_uri = %q, want a loopback URI", authURL.Query().Get("redirect_uri"))
	}
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
	store := mcp.NewAuthStore(config.GetLeleDir())
	if !store.Authenticated("remote-srv", resourceURL) {
		t.Fatal("the store has no usable credential after the flow reported success")
	}
	if m.mcpAuthFlow == nil || !m.mcpAuthFlow.Done() {
		t.Error("the flow is not Done() after a successful authorization")
	}
}

func TestMCPAuthEngine_CancelReleasesTheLoopbackListener(t *testing.T) {
	doc, _ := mcpAuthE2EServer(t)

	m := mcpAuthListModel(t, doc)
	mcpAuthRecordingOpener(m, nil)
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

	status, ferr := flow.Status()
	if status != mcpAuthStatusError || !errors.Is(ferr, mcp.ErrAuthCanceled) {
		t.Errorf("Status = (%q, %v), want (\"error\", ErrAuthCanceled)", status, ferr)
	}
	if !flow.Done() {
		t.Error("Done = false after a cancel")
	}
	if err := mcpAuthDialLoopback(uri.Host); err == nil {
		t.Error("the loopback listener still accepts connections after the cancel")
	}
}

func mcpAuthDialLoopback(hostPort string) error {
	conn, err := net.DialTimeout("tcp", hostPort, time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}
