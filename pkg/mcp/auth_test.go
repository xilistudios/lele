package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/xilistudios/lele/pkg/mcp/authtest"
)

type authFixture struct {
	t          *testing.T
	as         *authtest.Server
	asrv       *httptest.Server
	store      *AuthStore
	mcpURL     string
	noRedirect *http.Client
}

func newAuthFixture(t *testing.T, opts ...authtest.Option) *authFixture {
	t.Helper()
	as := authtest.New(opts...)
	asrv := httptest.NewServer(as.Handler())
	t.Cleanup(asrv.Close)
	as.SetIssuer(asrv.URL)
	return &authFixture{
		t:      t,
		as:     as,
		asrv:   asrv,
		store:  NewAuthStore(t.TempDir()),
		mcpURL: asrv.URL + authtest.DefaultResourcePath,
		noRedirect: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (f *authFixture) serverConfig() ServerConfig {
	return ServerConfig{URL: f.mcpURL, Type: typeHTTP, Auth: &AuthSpec{Type: typeAuthOAuth}}
}

func authorizeRedirect(t *testing.T, hc *http.Client, authURL string) *url.URL {
	t.Helper()
	resp, err := hc.Get(authURL)
	if err != nil {
		t.Fatalf("GET authorize URL: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("/authorize status = %d, want 302", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	return loc
}

func completeLoopback(t *testing.T, hc *http.Client, flow *AuthFlow) {
	t.Helper()
	loc := authorizeRedirect(t, hc, flow.URL())
	code, state := loc.Query().Get("code"), loc.Query().Get("state")
	if code == "" {
		t.Fatalf("authorization redirect carried no code: %s", loc.String())
	}
	target := flow.RedirectURI() + "?" + url.Values{"code": {code}, "state": {state}}.Encode()
	resp, err := hc.Get(target)
	if err != nil {
		t.Fatalf("GET loopback callback: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("loopback callback status = %d, want 200", resp.StatusCode)
	}
}

func TestStartAuthCompletesLoopbackFlowEndToEnd(t *testing.T) {
	f := newAuthFixture(t, authtest.WithScopes("mcp", "offline_access"))
	ctx := context.Background()

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flow.Cancel()

	authURL, err := url.Parse(flow.URL())
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	q := authURL.Query()
	if q.Get("client_id") == "" {
		t.Error("authorization URL has no client_id")
	}
	if q.Get("state") == "" || q.Get("state") != flow.state {
		t.Errorf("authorization URL state = %q, want %q", q.Get("state"), flow.state)
	}
	if q.Get("code_challenge") == "" {
		t.Error("authorization URL has no code_challenge")
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}

	completeLoopback(t, f.noRedirect, flow)
	if err := flow.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if len(f.as.AuthorizeRequests()) == 0 {
		t.Fatal("no authorization request reached the AS")
	}

	if !f.store.Authenticated("srv", f.mcpURL) {
		t.Fatal("store.Authenticated = false, want true after a completed flow")
	}

	if state, ferr := flow.Status(); state != authStatusDone || ferr != nil {
		t.Fatalf("Status = (%q, %v), want (%q, nil)", state, ferr, authStatusDone)
	}
	if !flow.Done() {
		t.Fatal("Done = false after a successful flow")
	}

	if !f.store.AuthRequired("srv", f.mcpURL) {
		t.Fatal("store.AuthRequired = false after a flow, want true")
	}
	zeroConfig := ServerConfig{URL: f.mcpURL, Type: typeHTTP}
	if rt := buildAuthRuntime(f.store, "srv", zeroConfig); rt == nil {
		t.Fatal("buildAuthRuntime = nil for a zero-config server the store knows needs auth")
	}
	if rt := AttachAuthRuntime("srv", zeroConfig, f.store).authRuntime; rt == nil {
		t.Fatal("AttachAuthRuntime attached no runtime to a zero-config server that needs auth")
	}

	rec, ok := f.store.Record("srv")
	if !ok || rec.Token == nil || rec.Token.AccessToken == "" {
		t.Fatal("no token in the store after the flow")
	}
	req, err := http.NewRequest(http.MethodGet, f.mcpURL, nil)
	if err != nil {
		t.Fatalf("build resource request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+rec.Token.AccessToken)
	resp, err := f.asrv.Client().Do(req)
	if err != nil {
		t.Fatalf("resource request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resource status = %d, want 200", resp.StatusCode)
	}
	observations := f.as.ResourceRequests()
	if len(observations) == 0 {
		t.Fatal("no resource request observed")
	}
	sawBearer := false
	for _, obs := range observations {
		if strings.Contains(fmt.Sprintf("%+v", obs), rec.Token.AccessToken) {
			t.Error("the AS observation leaked the access token value")
		}
		if !obs.HasBearer {
			continue
		}
		sawBearer = true
		if obs.Authorization != "Bearer" {
			t.Errorf("recorded Authorization scheme = %q, want Bearer (the value must never be recorded)", obs.Authorization)
		}
	}
	if !sawBearer {
		t.Fatal("no authenticated resource request observed")
	}
}

func TestStartAuthOnSSEServerCompletesEndToEnd(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()

	srv := f.serverConfig()
	srv.Type = typeSSE

	flow, err := StartAuth(ctx, "srv", srv, f.store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth (sse): %v", err)
	}
	defer flow.Cancel()

	if flow.URL() == "" {
		t.Fatal("the sse flow returned no authorization URL")
	}
	if _, ferr := flow.Status(); ferr != nil {
		t.Fatalf("a fresh sse flow reports %v", ferr)
	}

	completeLoopback(t, f.noRedirect, flow)
	if err := flow.Wait(ctx); err != nil {
		t.Fatalf("Wait (sse): %v", err)
	}
	if got := len(f.as.AuthorizeRequests()); got == 0 {
		t.Fatal("no authorization request reached the AS for the sse flow")
	}
	if !f.store.Authenticated("srv", f.mcpURL) {
		t.Fatal("the sse flow did not store a usable token")
	}
	if state, ferr := flow.Status(); state != authStatusDone || ferr != nil {
		t.Fatalf("Status (sse) = (%q, %v), want done", state, ferr)
	}
}

func TestStartAuthKeepsTheCredentialOfAnAbandonedFlow(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	if err := f.store.Save("srv", AuthRecord{
		URL:          f.mcpURL,
		ClientID:     "pre-registered-client",
		RedirectURI:  redirectURI,
		AuthRequired: true,
		Token: &transport.Token{
			AccessToken: "still-valid-access-token",
			TokenType:   "Bearer",
			ExpiresAt:   time.Now().Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	before := f.store.Revision("srv")
	if !f.store.Authenticated("srv", f.mcpURL) {
		t.Fatal("seed token is not usable")
	}

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{Port: port})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	if flow.Done() {
		t.Fatal("a freshly started flow is already Done")
	}

	if !f.store.Authenticated("srv", f.mcpURL) {
		t.Fatal("StartAuth logged the server out: store.Authenticated = false")
	}
	rec, ok := f.store.Record("srv")
	if !ok || rec.Token == nil || rec.Token.AccessToken != "still-valid-access-token" {
		t.Fatalf("StartAuth rewrote the token: %s", redactedRecord(rec))
	}
	if got := f.store.Revision("srv"); got != before {
		t.Fatalf("StartAuth changed the revision (%d → %d): a registration-only write must not retire the connection", before, got)
	}
	if !f.store.AuthRequired("srv", f.mcpURL) {
		t.Fatal("startAuth cleared the auth_required flag")
	}

	if err := flow.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if !flow.Done() {
		t.Fatal("Done = false after Cancel")
	}
	if !f.store.Authenticated("srv", f.mcpURL) {
		t.Fatal("Cancel logged the server out: store.Authenticated = false")
	}
	rec, ok = f.store.Record("srv")
	if !ok || rec.Token == nil || rec.Token.AccessToken != "still-valid-access-token" {
		t.Fatalf("an abandoned flow rewrote the token: %s", redactedRecord(rec))
	}
	if got := f.store.Revision("srv"); got != before {
		t.Fatalf("an abandoned flow changed the revision (%d → %d)", before, got)
	}
}

func TestStartAuthDoesNotCarryCredentialAcrossResourceURL(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()

	asB := authtest.New()
	asrvB := httptest.NewServer(asB.Handler())
	t.Cleanup(asrvB.Close)
	asB.SetIssuer(asrvB.URL)
	urlA := f.mcpURL
	urlB := asrvB.URL + authtest.DefaultResourcePath
	if urlA == urlB {
		t.Fatal("the two resource URLs must differ for this test to mean anything")
	}

	if err := f.store.Save("srv", AuthRecord{
		URL:          urlA,
		ClientID:     "client-for-a",
		RedirectURI:  "http://127.0.0.1:1/callback",
		AuthRequired: true,
		Token: &transport.Token{
			AccessToken: "token-bound-to-a",
			TokenType:   "Bearer",
			ExpiresAt:   time.Now().Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	if !f.store.Authenticated("srv", urlA) {
		t.Fatal("seed token for urlA is not usable")
	}

	flow, err := StartAuth(ctx, "srv", ServerConfig{URL: urlB, Type: typeHTTP, Auth: &AuthSpec{Type: typeAuthOAuth}}, f.store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth for urlB: %v", err)
	}
	defer flow.Cancel()

	if f.store.Authenticated("srv", urlB) {
		t.Fatal("StartAuth carried a token bound to another resource URL over to urlB")
	}
	rec, ok := f.store.Record("srv")
	if !ok {
		t.Fatal("no record for srv after StartAuth")
	}
	if rec.URL != urlB {
		t.Fatalf("record URL = %q, want the flow's %q", rec.URL, urlB)
	}
	if rec.Token != nil {
		t.Fatalf("StartAuth stored another resource's token: %s", redactedRecord(rec))
	}
}

func TestStartAuthForcesFreshAuthorization(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()

	const oldToken = "pre-existing-access-token"
	f.as.NewAccessToken(oldToken, time.Hour)
	if err := f.store.Save("srv", AuthRecord{
		URL:          f.mcpURL,
		ClientID:     "pre-registered-client",
		AuthRequired: true,
		Token: &transport.Token{
			AccessToken: oldToken,
			TokenType:   "Bearer",
			ExpiresAt:   time.Now().Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	if !f.store.Authenticated("srv", f.mcpURL) {
		t.Fatal("seed token is not usable")
	}

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flow.Cancel()

	completeLoopback(t, f.noRedirect, flow)
	if err := flow.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if len(f.as.AuthorizeRequests()) == 0 {
		t.Fatal("the flow reused the stored token instead of starting an authorization")
	}
	rec, ok := f.store.Record("srv")
	if !ok || rec.Token == nil {
		t.Fatal("no token stored after the fresh flow")
	}
	if rec.Token.AccessToken == oldToken {
		t.Fatal("the stored token was not replaced by the fresh authorization")
	}
	if rec.ClientID != "pre-registered-client" {
		t.Errorf("client id = %q, want the pre-registered one to be preserved", rec.ClientID)
	}
}

func TestSubmitManualFlowWithoutLoopback(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	const manualRedirect = "https://client.example/cb"

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{RedirectURI: manualRedirect})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flow.Cancel()

	if got := flow.RedirectURI(); got != manualRedirect {
		t.Fatalf("RedirectURI = %q, want the configured %q (no listener)", got, manualRedirect)
	}

	loc := authorizeRedirect(t, f.noRedirect, flow.URL())
	if base := loc.Scheme + "://" + loc.Host + loc.Path; base != "https://client.example/cb" {
		t.Fatalf("redirected to %q, want the configured redirect", base)
	}
	code, state := loc.Query().Get("code"), loc.Query().Get("state")
	if code == "" {
		t.Fatalf("no code in redirect %q", loc.String())
	}

	if err := flow.Submit(ctx, code, state); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if state, ferr := flow.Status(); state != authStatusDone || ferr != nil {
		t.Fatalf("Status = (%q, %v), want done", state, ferr)
	}
	if !f.store.Authenticated("srv", f.mcpURL) {
		t.Fatal("token not stored after a manual Submit")
	}
	tokens := f.as.TokenRequests()
	if len(tokens) == 0 || tokens[len(tokens)-1].RedirectURI != manualRedirect {
		t.Fatalf("token request redirect_uri = %s, want %q", redactTokenRequests(tokens), manualRedirect)
	}
}

func TestSubmitRejectsStateMismatch(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flow.Cancel()

	err = flow.Submit(ctx, "some-code", "not-the-state")
	if !errors.Is(err, errAuthStateMismatch) {
		t.Fatalf("Submit with a wrong state = %v, want %v", err, errAuthStateMismatch)
	}
	if state, _ := flow.Status(); state != authStatusPending {
		t.Fatalf("Status after a rejected submit = %q, want pending", state)
	}
}

func TestSubmitAfterFailureCanRetry(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	const manualRedirect = "https://client.example/cb"

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{RedirectURI: manualRedirect})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flow.Cancel()

	loc := authorizeRedirect(t, f.noRedirect, flow.URL())
	code, state := loc.Query().Get("code"), loc.Query().Get("state")

	f.as.SetTokenError("invalid_grant", http.StatusBadRequest)
	if err := flow.Submit(ctx, code, state); err == nil {
		t.Fatal("first Submit succeeded, want the scripted token failure")
	}
	if got, _ := flow.Status(); got != authStatusError {
		t.Fatalf("Status after a failed exchange = %q, want error", got)
	}

	if err := flow.Submit(ctx, code, state); err != nil {
		t.Fatalf("retry Submit: %v (the expected state was not re-armed?)", err)
	}
	if got, ferr := flow.Status(); got != authStatusDone || ferr != nil {
		t.Fatalf("Status after retry = (%q, %v), want done", got, ferr)
	}
	if !f.store.Authenticated("srv", f.mcpURL) {
		t.Fatal("token not stored after a successful retry")
	}
}

func TestDoneDistinguishesRetryableFromTerminal(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	const manualRedirect = "https://client.example/cb"

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{RedirectURI: manualRedirect})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flow.Cancel()
	if flow.Done() {
		t.Fatal("Done = true for a pending flow")
	}

	loc := authorizeRedirect(t, f.noRedirect, flow.URL())
	code, state := loc.Query().Get("code"), loc.Query().Get("state")

	f.as.SetTokenError("invalid_grant", http.StatusBadRequest)
	if err := flow.Submit(ctx, code, state); err == nil {
		t.Fatal("Submit succeeded, want the scripted token failure")
	}
	if got, _ := flow.Status(); got != authStatusError {
		t.Fatalf("Status = %q, want %q", got, authStatusError)
	}
	if flow.Done() {
		t.Fatal("Done = true after a RETRYABLE token-exchange failure (Submit is still valid)")
	}
	if err := flow.Submit(ctx, code, state); err != nil {
		t.Fatalf("retry Submit: %v", err)
	}
	if !flow.Done() {
		t.Fatal("Done = false after a successful Submit")
	}

	terminal, err := StartAuth(ctx, "srv-terminal", f.serverConfig(), f.store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth (terminal): %v", err)
	}
	if err := terminal.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if !terminal.Done() {
		t.Fatal("Done = false after Cancel")
	}
	if state, ferr := terminal.Status(); state != authStatusError || !errors.Is(ferr, ErrAuthCanceled) {
		t.Fatalf("Status after Cancel = (%q, %v), want (error, ErrAuthCanceled)", state, ferr)
	}
}

func TestDCRIsPersistedAndReused(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()

	flow1, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth #1: %v", err)
	}
	if got := f.as.RegisterCount(); got != 1 {
		t.Fatalf("RegisterCount after the first flow = %d, want 1", got)
	}
	rec, ok := f.store.Record("srv")
	if !ok || rec.ClientID == "" {
		t.Fatal("the dynamically registered client id was not persisted")
	}
	flow1.Cancel()

	flow2, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth #2: %v", err)
	}
	defer flow2.Cancel()
	if got := f.as.RegisterCount(); got != 1 {
		t.Fatalf("RegisterCount after the second flow = %d, want 1 (registration reuse)", got)
	}
	if got := flow2.handler.GetClientID(); got != rec.ClientID {
		t.Fatalf("second flow client id = %q, want the persisted %q", got, rec.ClientID)
	}
}

func TestCancelFreesThePort(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{Port: port})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	if err := flow.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if state, ferr := flow.Status(); state != authStatusError || !errors.Is(ferr, ErrAuthCanceled) {
		t.Fatalf("Status = (%q, %v), want (error, ErrAuthCanceled)", state, ferr)
	}
	ln2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("the port was not released after Cancel: %v", err)
	}
	_ = ln2.Close()
	if err := flow.Cancel(); err != nil {
		t.Fatalf("second Cancel: %v", err)
	}
}

func TestTimeoutIsBounded(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flow.Cancel()

	wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := flow.Wait(wctx); !errors.Is(err, ErrAuthTimedOut) {
		t.Fatalf("Wait = %v, want %v", err, ErrAuthTimedOut)
	}
	if state, ferr := flow.Status(); state != authStatusError || !errors.Is(ferr, ErrAuthTimedOut) {
		t.Fatalf("Status = (%q, %v), want (error, ErrAuthTimedOut)", state, ferr)
	}
}

func TestStartAuthRefusesDisabledStore(t *testing.T) {
	_, err := StartAuth(context.Background(), "srv", ServerConfig{URL: "https://example.test/mcp"}, NewAuthStore(""), AuthOptions{})
	if !errors.Is(err, ErrAuthStoreDisabled) {
		t.Fatalf("StartAuth with a disabled store = %v, want %v", err, ErrAuthStoreDisabled)
	}
}

func TestStartAuthOnUnreachableServerReportsProbeFailure(t *testing.T) {
	store := NewAuthStore(t.TempDir())

	_, err := StartAuth(context.Background(), "dead", ServerConfig{URL: "http://127.0.0.1:1/mcp"}, store, AuthOptions{})
	if err == nil {
		t.Fatal("StartAuth against a closed port succeeded")
	}
	if errors.Is(err, ErrAuthNotRequired) {
		t.Fatalf("StartAuth = %v, want a probe failure, NOT %v (a dead server is not 'no login needed')", err, ErrAuthNotRequired)
	}
	if !errors.Is(err, ErrAuthProbeFailed) {
		t.Fatalf("StartAuth = %v, want %v", err, ErrAuthProbeFailed)
	}
	if got, want := err.Error(), `mcp auth "dead": probe: `+ErrAuthProbeFailed.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error %q leaks the URL", err.Error())
	}
}

func TestStartAuthOnServerThatNeedsNoAuth(t *testing.T) {
	srv := newNoAuthMCPServer(t)
	store := NewAuthStore(t.TempDir())

	_, err := StartAuth(context.Background(), "srv", ServerConfig{URL: srv.URL + "/mcp"}, store, AuthOptions{})
	if !errors.Is(err, ErrAuthNotRequired) {
		t.Fatalf("StartAuth against a server that needs no auth = %v, want %v", err, ErrAuthNotRequired)
	}
}

func TestNoSecretsInFlowErrors(t *testing.T) {
	const (
		clientSecretCanary     = "CANARY_CLIENT_SECRET"
		accessTokenCanary      = "CANARY_ACCESS_TOKEN"
		codeCanary             = "CANARY_AUTH_CODE"
		redirectQuery          = "CANARY_REDIRECT_QUERY"
		errorDescriptionCanary = "CANARY_ERROR_DESCRIPTION"
	)
	canaries := []string{clientSecretCanary, accessTokenCanary, codeCanary, redirectQuery, errorDescriptionCanary}

	as := authtest.New()
	as.SetClientSecretSecret(clientSecretCanary)
	asrv := httptest.NewServer(canaryBodyHandler{next: as.Handler(), canary: accessTokenCanary})
	t.Cleanup(asrv.Close)
	as.SetIssuer(asrv.URL)

	store := NewAuthStore(t.TempDir())
	ctx := context.Background()
	mcpURL := asrv.URL + authtest.DefaultResourcePath
	manualRedirect := "https://client.example/cb?leak=" + redirectQuery

	var collected []error
	collect := func(err error) {
		if err != nil {
			collected = append(collected, err)
		}
	}

	flowA, err := StartAuth(ctx, "srv", ServerConfig{URL: mcpURL, Auth: &AuthSpec{Type: typeAuthOAuth}}, store, AuthOptions{RedirectURI: manualRedirect})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flowA.Cancel()
	as.SetAuthorizationCode(codeCanary)
	as.SetTokenError("invalid_grant", http.StatusBadRequest)

	loc := authorizeRedirect(t, &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, flowA.URL())
	code, state := loc.Query().Get("code"), loc.Query().Get("state")
	if code != codeCanary {
		t.Fatalf("code = %q, want the scripted canary", code)
	}
	collect(flowA.Submit(ctx, code, state))
	collect(flowA.Submit(ctx, code, "wrong-state"))

	as.SetAuthorizeError("access_denied")
	flowB, err := StartAuth(ctx, "srv-b", ServerConfig{URL: mcpURL, Auth: &AuthSpec{Type: typeAuthOAuth}}, store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth B: %v", err)
	}
	defer flowB.Cancel()
	locB := authorizeRedirect(t, &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, flowB.URL())
	cb := flowB.RedirectURI() + "?" + locB.RawQuery
	resp, err := asrv.Client().Get(cb)
	if err != nil {
		t.Fatalf("loopback error callback: %v", err)
	}
	_ = resp.Body.Close()
	wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	collect(flowB.Wait(wctx))

	flowC, err := StartAuth(ctx, "srv-c", ServerConfig{URL: mcpURL, Auth: &AuthSpec{Type: typeAuthOAuth}}, store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth C: %v", err)
	}
	defer flowC.Cancel()
	cbC := flowC.RedirectURI() + "?" + url.Values{
		"error":             {"access_denied"},
		"error_description": {errorDescriptionCanary},
		"state":             {flowC.state},
	}.Encode()
	respC, err := asrv.Client().Get(cbC)
	if err != nil {
		t.Fatalf("loopback canaried callback: %v", err)
	}
	_ = respC.Body.Close()
	collect(flowC.Wait(wctx))

	as.FailRegistrationOnce("invalid_client_metadata")
	_, err = StartAuth(ctx, "srv-d", ServerConfig{URL: mcpURL, Auth: &AuthSpec{Type: typeAuthOAuth}}, store, AuthOptions{})
	collect(err)

	if len(collected) == 0 {
		t.Fatal("no errors collected, the test is vacuous")
	}
	for _, err := range collected {
		for _, canary := range canaries {
			if strings.Contains(err.Error(), canary) {
				t.Errorf("error %q leaked the canary %q", err.Error(), canary)
			}
		}
	}
}

func TestLogoutClearsEverything(t *testing.T) {
	store := NewAuthStore(t.TempDir())
	const url = "https://example.test/mcp"
	if err := store.Save("srv", AuthRecord{
		URL:          url,
		ClientID:     "client",
		ClientSecret: "secret",
		RedirectURI:  "http://127.0.0.1:1/cb",
		AuthRequired: true,
		Token:        &transport.Token{AccessToken: "tok", TokenType: "Bearer"},
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	if !store.Authenticated("srv", url) {
		t.Fatal("seed token is not usable")
	}

	if err := Logout(store, "srv"); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if rec, ok := store.Record("srv"); ok {
		t.Fatalf("record still present after Logout: %s", redactedRecord(rec))
	}
	if store.Authenticated("srv", url) || store.AuthRequired("srv", url) {
		t.Fatal("token or flag survived Logout")
	}
	if store.Revision("srv") != 0 {
		t.Fatal("revision is not 0 after Logout")
	}
	if err := Logout(nil, "srv"); !errors.Is(err, ErrAuthStoreDisabled) {
		t.Fatalf("Logout(nil) = %v, want %v", err, ErrAuthStoreDisabled)
	}
}

func TestCheckAuthReportsRequiredAuthenticatedAndMethod(t *testing.T) {
	const url = "https://example.test/mcp"

	headers := ServerConfig{URL: url, Headers: map[string]string{"Authorization": "Bearer api-key"}}
	plain := ServerConfig{URL: url}
	oauth := ServerConfig{URL: url, Auth: &AuthSpec{Type: typeAuthOAuth}}

	tests := []struct {
		name       string
		srv        ServerConfig
		challenged bool
		token      bool
		want       AuthStatus
	}{
		{
			name: "empty entry with no challenge",
			srv:  plain,
			want: AuthStatus{Required: false, Authenticated: false, Method: ""},
		},
		{
			name: "headers only, no challenge",
			srv:  headers,
			want: AuthStatus{Required: false, Authenticated: false, Method: "headers"},
		},
		{
			name:       "headers plus persisted challenge",
			srv:        headers,
			challenged: true,
			want:       AuthStatus{Required: true, Authenticated: false, Method: "headers"},
		},
		{
			name:       "no headers, no auth block, persisted challenge",
			srv:        plain,
			challenged: true,
			want:       AuthStatus{Required: true, Authenticated: false, Method: ""},
		},
		{
			name: "oauth block without a token",
			srv:  oauth,
			want: AuthStatus{Required: true, Authenticated: false, Method: "oauth"},
		},
		{
			name:  "oauth block after SaveToken",
			srv:   oauth,
			token: true,
			want:  AuthStatus{Required: true, Authenticated: true, Method: "oauth"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewAuthStore(t.TempDir())
			name := "srv"
			if tc.challenged || tc.token {
				rec := AuthRecord{URL: url, AuthRequired: tc.challenged}
				if tc.token {
					rec.AuthRequired = true
					rec.Token = &transport.Token{
						AccessToken: "tok",
						TokenType:   "Bearer",
						ExpiresAt:   time.Now().Add(time.Hour),
					}
				}
				if err := store.Save(name, rec); err != nil {
					t.Fatalf("seed store: %v", err)
				}
			}

			if got := CheckAuth(store, name, tc.srv); got != tc.want {
				t.Fatalf("CheckAuth = %+v, want %+v", got, tc.want)
			}
		})
	}

	if got := CheckAuth(nil, "srv", oauth); got != (AuthStatus{Required: true, Authenticated: false, Method: "oauth"}) {
		t.Fatalf("CheckAuth(nil store, oauth) = %+v, want oauth required and not authenticated", got)
	}
	if got := CheckAuth(nil, "srv", headers); got != (AuthStatus{Required: false, Authenticated: false, Method: "headers"}) {
		t.Fatalf("CheckAuth(nil store, headers) = %+v, want headers-only, not required", got)
	}
}

func TestListenerSeam(t *testing.T) {
	f := newAuthFixture(t)

	old := defaultListen
	defaultListen = func(string, string) (net.Listener, error) {
		return nil, errors.New("bind refused by the seam")
	}
	t.Cleanup(func() { defaultListen = old })

	_, err := StartAuth(context.Background(), "srv", f.serverConfig(), f.store, AuthOptions{})
	if err == nil {
		t.Fatal("StartAuth succeeded with a failing listener, want an error")
	}
	if errors.Is(err, ErrAuthNotRequired) {
		t.Fatalf("StartAuth = %v, want the bind error, not ErrAuthNotRequired", err)
	}
	if !strings.Contains(err.Error(), "bind refused by the seam") {
		t.Fatalf("error = %v, want it to wrap the seam failure", err)
	}
}

func TestValueFreeErrorMatchesPointerOAuthError(t *testing.T) {
	if got := valueFreeError(&transport.OAuthError{ErrorCode: "invalid_grant"}); got.Error() != "authorization failed: invalid_grant" {
		t.Errorf("pointer OAuthError = %q, want the sanitized code", got.Error())
	}
	if got := valueFreeError(transport.OAuthError{ErrorCode: "invalid_grant"}); got.Error() != "authorization failed: invalid_grant" {
		t.Errorf("value OAuthError = %q, want the sanitized code", got.Error())
	}
	unsafe := "invalid grant <script>"
	if got := valueFreeError(&transport.OAuthError{ErrorCode: unsafe}); !errors.Is(got, errAuthExchangeFailed) {
		t.Errorf("unsafe pointer code = %q, want the fixed message", got.Error())
	}
	var nilPtr *transport.OAuthError
	if got := valueFreeError(nilPtr); !errors.Is(got, errAuthExchangeFailed) {
		t.Errorf("nil *OAuthError = %v, want the fixed message", got)
	}
	if got := valueFreeError(nil); got != nil {
		t.Errorf("valueFreeError(nil) = %v, want nil", got)
	}
}

func TestStartAuthRejectsRelativeRedirectURI(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()

	for _, bad := range []string{
		"/callback",
		"client.example/cb",
		"ftp://client.example/cb",
		"https:///cb",
		"://nope",
		"urn:ietf:wg:oauth:2.0:oob",
		"javascript:alert(1)",
		"//evil.example/cb",
		"http://",
		"https://#frag",
	} {
		_, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{RedirectURI: bad})
		if !errors.Is(err, errAuthBadRedirectURI) {
			t.Errorf("RedirectURI %q: StartAuth = %v, want %v", bad, err, errAuthBadRedirectURI)
		}
		if err != nil && strings.Contains(err.Error(), bad) {
			t.Errorf("RedirectURI %q: the error %q echoes the value", bad, err.Error())
		}
	}

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{RedirectURI: "https://client.example/cb"})
	if err != nil {
		t.Fatalf("StartAuth with a valid redirect uri: %v", err)
	}
	defer flow.Cancel()
	if got := flow.RedirectURI(); got != "https://client.example/cb" {
		t.Fatalf("RedirectURI = %q, want the override", got)
	}
}

func TestLoopbackServesOnlyGetAndPostOnTheCallbackPath(t *testing.T) {
	lb, err := startLoopback("http://127.0.0.1:0/callback", nil)
	if err != nil {
		t.Fatalf("startLoopback: %v", err)
	}
	defer lb.Close()

	req, err := http.NewRequest(http.MethodGet, lb.RedirectURI(), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.URL.Path = "/other"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /other: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /other status = %d, want 404", resp.StatusCode)
	}

	for _, method := range []string{http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req, err := http.NewRequest(method, lb.RedirectURI(), nil)
		if err != nil {
			t.Fatalf("build %s request: %v", method, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s callback: %v", method, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s callback status = %d, want 405", method, resp.StatusCode)
		}
		if got := resp.Header.Get("Allow"); got != "GET, POST" {
			t.Errorf("%s Allow = %q, want \"GET, POST\"", method, got)
		}
	}
	noResultCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := lb.Await(noResultCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Await after the rejected methods = %v, want a timeout (no result was delivered)", err)
	}

	resp, err = http.DefaultClient.Get(lb.RedirectURI())
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("GET callback status = %d, want 400 (no code in the query)", resp.StatusCode)
	}
	if _, _, err := lb.Await(context.Background()); !errors.Is(err, errAuthNoCallbackCode) {
		t.Fatalf("Await after a code-less callback = %v, want %v", err, errAuthNoCallbackCode)
	}
}

func TestLoopbackAcceptsFormPostCallbackBody(t *testing.T) {
	lb, err := startLoopback("http://127.0.0.1:0/callback", nil)
	if err != nil {
		t.Fatalf("startLoopback: %v", err)
	}
	defer lb.Close()

	const (
		code  = "form-post-code"
		state = "form-post-state"
	)
	resp, err := http.PostForm(lb.RedirectURI(), url.Values{"code": {code}, "state": {state}})
	if err != nil {
		t.Fatalf("POST callback: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST callback status = %d, want 200", resp.StatusCode)
	}
	gotCode, gotState, aerr := lb.Await(context.Background())
	if aerr != nil {
		t.Fatalf("Await after the form_post callback = %v, want no error", aerr)
	}
	if gotCode != code || gotState != state {
		t.Fatalf("Await = (%q, %q), want (%q, %q)", gotCode, gotState, code, state)
	}
}

func TestLoopbackBoundsTheCallbackBody(t *testing.T) {
	post := func(t *testing.T, lb *loopbackServer, body string) *http.Response {
		t.Helper()
		resp, err := http.Post(lb.RedirectURI(), "application/x-www-form-urlencoded", strings.NewReader(body))
		if err != nil {
			return nil
		}
		return resp
	}

	t.Run("oversized body is refused", func(t *testing.T) {
		lb, err := startLoopback("http://127.0.0.1:0/callback", nil)
		if err != nil {
			t.Fatalf("startLoopback: %v", err)
		}
		defer lb.Close()

		body := "code=oversized-code&state=s&padding=" + strings.Repeat("x", 1<<20)
		if resp := post(t, lb, body); resp != nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("oversized callback status = %d, want 400", resp.StatusCode)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		code, _, aerr := lb.Await(ctx)
		if aerr == nil || code != "" {
			t.Fatalf("oversized callback = (%q, %v), want no code and a refusal", code, aerr)
		}
	})

	t.Run("body just under the cap still works", func(t *testing.T) {
		lb, err := startLoopback("http://127.0.0.1:0/callback", nil)
		if err != nil {
			t.Fatalf("startLoopback: %v", err)
		}
		defer lb.Close()

		const code = "under-cap-code"
		body := "code=" + code + "&state=s&padding=" + strings.Repeat("x", 60000)
		if len(body) >= 64<<10 {
			t.Fatalf("test body is %d bytes, want it under the %d-byte cap", len(body), 64<<10)
		}
		resp := post(t, lb, body)
		if resp == nil {
			t.Fatal("under-cap callback: transport error although the body fits the cap")
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("under-cap callback status = %d, want 200", resp.StatusCode)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gotCode, gotState, aerr := lb.Await(ctx)
		if aerr != nil || gotCode != code || gotState != "s" {
			t.Fatalf("under-cap callback = (%q, %q, %v), want (%q, %q, nil)", gotCode, gotState, aerr, code, "s")
		}
	})
}

func TestLoopbackNeverSurfacesErrorDescription(t *testing.T) {
	const canary = "CANARY_ERROR_DESCRIPTION"
	lb, err := startLoopback("http://127.0.0.1:0/callback", nil)
	if err != nil {
		t.Fatalf("startLoopback: %v", err)
	}
	defer lb.Close()

	target := lb.RedirectURI() + "?" + url.Values{
		"error":             {"access_denied"},
		"error_description": {canary},
	}.Encode()
	resp, err := http.Get(target)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read callback page: %v", err)
	}
	if strings.Contains(string(body), canary) {
		t.Errorf("the callback page echoed error_description")
	}

	_, _, aerr := lb.Await(context.Background())
	if aerr == nil {
		t.Fatal("Await on an error callback = nil, want the rejection")
	}
	if strings.Contains(aerr.Error(), canary) {
		t.Fatalf("Await error %q leaked error_description", aerr.Error())
	}
	if !strings.Contains(aerr.Error(), "access_denied") {
		t.Fatalf("Await error = %q, want the sanitized OAuth error code", aerr.Error())
	}
}

func TestAwaitReturnsPromptlyAfterClose(t *testing.T) {
	lb, err := startLoopback("http://127.0.0.1:0/callback", nil)
	if err != nil {
		t.Fatalf("startLoopback: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := lb.Await(context.Background())
		done <- err
	}()

	if err := lb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, errAuthListenerClosed) {
			t.Fatalf("Await after Close = %v, want %v", err, errAuthListenerClosed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Await did not return after Close")
	}
	if err := lb.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestPackageDoesNotImportAuth(t *testing.T) {
	forbidden := map[string]bool{
		"github.com/xilistudios/lele/pkg/auth":   true,
		"github.com/xilistudios/lele/pkg/config": true,
		"github.com/xilistudios/lele/pkg/agent":  true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if forbidden[path] {
				t.Errorf("%s imports forbidden package %s", e.Name(), path)
			}
		}
	}
}

var tokenCredentialKeys = map[string]bool{
	"code_verifier":    true,
	"client_secret":    true,
	"code":             true,
	"refresh_token":    true,
	"assertion":        true,
	"client_assertion": true,
}

func redactTokenRequests(reqs []authtest.TokenRequest) string {
	out := make([]authtest.TokenRequest, len(reqs))
	for i, r := range reqs {
		if r.Code != "" {
			r.Code = "[redacted]"
		}
		if r.CodeVerifier != "" {
			r.CodeVerifier = "[redacted]"
		}
		if r.ClientSecret != "" {
			r.ClientSecret = "[redacted]"
		}
		if r.RefreshToken != "" {
			r.RefreshToken = "[redacted]"
		}
		if r.Raw != nil {
			raw := make(url.Values, len(r.Raw))
			for k, v := range r.Raw {
				if tokenCredentialKeys[k] {
					raw[k] = []string{"[redacted]"}
					continue
				}
				raw[k] = v
			}
			r.Raw = raw
		}
		out[i] = r
	}
	return fmt.Sprintf("%+v", out)
}

func TestRedactTokenRequestsHidesCredentials(t *testing.T) {
	const clientSecret = "real-client-secret-from-dcr"
	f := newAuthFixture(t)
	f.as.SetClientSecretSecret(clientSecret)
	ctx := context.Background()

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flow.Cancel()
	completeLoopback(t, f.noRedirect, flow)
	if err := flow.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	tokens := f.as.TokenRequests()
	if len(tokens) == 0 {
		t.Fatal("no token requests observed, the test would be vacuous")
	}
	verifier := tokens[0].CodeVerifier
	if verifier == "" {
		t.Fatal("token request carried no code_verifier")
	}
	if got := tokens[0].ClientSecret; got != clientSecret {
		t.Fatalf("token request client_secret mismatch (len got %d, want %d)", len(got), len(clientSecret))
	}
	for key, want := range map[string]string{"code_verifier": verifier, "client_secret": clientSecret} {
		if got := tokens[0].Raw.Get(key); got != want {
			t.Fatalf("Raw[%s] does not carry the field value (len got %d, want %d)", key, len(got), len(want))
		}
	}

	got := redactTokenRequests(tokens)
	t.Logf("real credentials: code_verifier(len=%d sha256=%s) client_secret(len=%d sha256=%s)",
		len(verifier), shortHash(verifier), len(clientSecret), shortHash(clientSecret))
	for name, secret := range map[string]string{"code_verifier": verifier, "client_secret": clientSecret} {
		if strings.Contains(got, secret) {
			t.Errorf("redacted output still contains the real %s", name)
		}
	}
	if !strings.Contains(got, "grant_type") || !strings.Contains(got, "authorization_code") {
		t.Errorf("redacted output dropped the diagnostic grant_type")
	}
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

func redactedRecord(rec AuthRecord) string {
	return fmt.Sprintf("AuthRecord{URL:%q ClientID:%q ClientSecret:%s ResourceMetadataURL:%q RedirectURI:%q AuthRequired:%t Token:%s}",
		rec.URL, rec.ClientID, redactValue(rec.ClientSecret), rec.ResourceMetadataURL, rec.RedirectURI, rec.AuthRequired, redactedToken(rec.Token))
}

func redactedToken(tok *transport.Token) string {
	if tok == nil {
		return "nil"
	}
	return fmt.Sprintf("Token{AccessToken:%s TokenType:%q RefreshToken:%s ExpiresAt:%s Scope:%q}",
		redactValue(tok.AccessToken), tok.TokenType, redactValue(tok.RefreshToken), tok.ExpiresAt, tok.Scope)
}

func redactValue(s string) string {
	if s == "" {
		return `""(empty)`
	}
	return "[redacted]"
}

func newNoAuthMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		switch {
		case req.Method == "initialize":
			id := "null"
			if len(req.ID) > 0 {
				id = string(req.ID)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":%q,"capabilities":{},"serverInfo":{"name":"noauth","version":"0"}}}`,
				id, mcp.LATEST_LEGACY_PROTOCOL_VERSION)
		case strings.HasPrefix(req.Method, "notifications/"):
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

type canaryBodyHandler struct {
	next   http.Handler
	canary string
}

func (h canaryBodyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/token" && r.URL.Path != "/register" {
		h.next.ServeHTTP(w, r)
		return
	}
	rec := httptest.NewRecorder()
	h.next.ServeHTTP(rec, r)
	for k, vs := range rec.Header() {
		w.Header()[k] = vs
	}
	w.WriteHeader(rec.Code)
	body := strings.Replace(rec.Body.String(), "scripted", "scripted "+h.canary, 1)
	_, _ = io.WriteString(w, body)
}
