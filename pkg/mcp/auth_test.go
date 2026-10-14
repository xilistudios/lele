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

// This file drives the interactive flow against pkg/mcp/authtest. Compile-time
// invariant (mirrored by TestPackageDoesNotImportAuth below): pkg/mcp must NOT
// import pkg/auth, pkg/config or pkg/agent — the browser opener is injected by
// the caller and this package never reaches for agent machinery.

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

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

// serverConfig builds the ServerConfig the CLI would hand StartAuth: a remote
// entry that opts into the OAuth flow.
func (f *authFixture) serverConfig() ServerConfig {
	return ServerConfig{URL: f.mcpURL, Type: typeHTTP, Auth: &AuthSpec{Type: typeAuthOAuth}}
}

// authorizeRedirect stops at the authorization server's 302 and returns the
// parsed Location (the redirect_uri with code/state or error appended).
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

// completeLoopback drives a loopback flow: it follows the authorization
// redirect by hand (the client must NOT follow it) and fetches the loopback
// callback with the code and state the AS issued.
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

// ---------------------------------------------------------------------------
// end to end
// ---------------------------------------------------------------------------

// TestStartAuthCompletesLoopbackFlowEndToEnd runs the whole interactive flow:
// start, visit the authorization URL, deliver the callback to the loopback
// server, and observe the token land in the REAL store.
func TestStartAuthCompletesLoopbackFlowEndToEnd(t *testing.T) {
	f := newAuthFixture(t, authtest.WithScopes("mcp", "offline_access"))
	ctx := context.Background()

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flow.Cancel()

	// (a) the authorization URL carries the public client id, the CSRF state
	// and the PKCE S256 challenge.
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

	// (b) falsifiability: the token exchange only succeeds if the S256
	// transformation of the verifier matches the challenge /authorize recorded
	// (authtest rejects the code otherwise). Reaching "done" therefore proves
	// PKCE was actually performed end to end.
	if len(f.as.AuthorizeRequests()) == 0 {
		t.Fatal("no authorization request reached the AS")
	}

	// (c) the token landed in the real store.
	if !f.store.Authenticated("srv", f.mcpURL) {
		t.Fatal("store.Authenticated = false, want true after a completed flow")
	}

	// (d) the flow reports done.
	if state, ferr := flow.Status(); state != authStatusDone || ferr != nil {
		t.Fatalf("Status = (%q, %v), want (%q, nil)", state, ferr, authStatusDone)
	}
	if !flow.Done() {
		t.Fatal("Done = false after a successful flow")
	}

	// (e) the server is now recorded as REQUIRING auth, which is what makes the
	// zero-config next dial attach OAuth. This is the flag mutation M5 clears
	// (rec.AuthRequired = false) while the rest of the suite stays green, so it
	// is pinned here: the entry below has NO auth block, which leaves the
	// store's flag as the only thing that can opt the server in (case (b) of
	// buildAuthRuntime).
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

	// The authtest fake never records a credential: it records the scheme only.
	// Drive one authenticated resource request and prove the observation holds
	// the scheme without the token value.
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
			continue // the plain probe hits the resource without a credential
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

// TestStartAuthOnSSEServerCompletesEndToEnd is the SSE guard (finding F1): the
// SSE transport dials its event stream INSIDE Start, so mcp-go reports the
// authorization challenge from cli.Start (transport/sse.go:270-283) and NOT
// from Initialize. An engine that only inspects the Initialize error leaves
// handler == nil and answers ErrAuthNotRequired — an SSE server could then
// never be logged into. The streamable-HTTP path is unaffected (its Start is a
// no-op).
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

// TestStartAuthKeepsTheCredentialOfAnAbandonedFlow is the finding-F2 guard: the
// registration write stores a whole AuthRecord, so a flow that forgets the
// existing token logs out a working server — and bumps the revision, which
// makes the manager retire the healthy connection and makes buildAuthRuntime
// attach OAuth with no token. An abandoned or failed login must be a no-op for
// a logged-in server; the "fresh authorization" guarantee lives in
// flowTokenStore.GetToken, not in the absence of a stored token.
func TestStartAuthKeepsTheCredentialOfAnAbandonedFlow(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()

	// The flow must bind the very port already on file, so its registration
	// write is field-for-field identical to the stored record — which is what
	// lets the revision stay put.
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

	// Starting a login must not disturb the working credential.
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

	// Abandoning the flow is just as harmless.
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

// TestStartAuthDoesNotCarryCredentialAcrossResourceURL pins the URL-mismatch
// branch of the credential carry-over in StartAuth. The registration write
// carries the existing token over only when the stored record belongs to the
// SAME resource URL (`current.URL == srv.URL`); a record bound to another URL is
// a different server's credential and is not ours to keep.
//
// Before this test existed, a reviewer proved that DELETING the
// `&& current.URL == srv.URL` clause left every ./pkg/mcp/... and
// ./pkg/channels/... suite green: the branch was correct but unpinned. THIS test
// is that pin — it fails as soon as the clause goes, which is why the old
// "every suite green" claim no longer holds. It seeds an AUTHENTICATED record
// for (name, urlA) and starts a flow for the SAME name at urlB: the flow's
// by-name registration write must NOT drag urlA's token onto urlB.
func TestStartAuthDoesNotCarryCredentialAcrossResourceURL(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()

	// An independent authorization server + resource, so urlB is a real,
	// auth-required URL the flow can dial (a different PATH on f's server would
	// not be the protected resource).
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

	// The record now belongs to urlB (the flow rewrites it by name) and must NOT
	// have inherited urlA's token. Authenticated(name, urlB) is exactly the
	// predicate that flips when the URL clause is removed.
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

// TestStartAuthForcesFreshAuthorization proves the flow never silently reuses a
// stored credential: even with a live token present, an authorization request
// is issued and the stored token is REPLACED.
//
// FALSIFIABILITY: the seeded token is registered with the fake authorization
// server. Without that, the AS would 401 an unknown bearer token and the flow
// would be forced to authorize no matter what the flow token store does — so
// mutation M9 (flow store delegating GetToken to the real store) left this test
// green. With the token known to the AS, a flow that reused it would complete
// the handshake and never issue an authorization request.
func TestStartAuthForcesFreshAuthorization(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()

	const oldToken = "pre-existing-access-token"
	// The AS now accepts the seeded token: it is a LIVE credential, so a flow
	// that read it from the real store would not need to authorize at all.
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

// TestSubmitManualFlowWithoutLoopback covers the non-loopback redirect: no
// listener is created and the caller completes the flow with the code it
// obtained itself.
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
	// The token request must carry the configured redirect URI verbatim.
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

// TestSubmitAfterFailureCanRetry is the finding-3 guard: mcp-go consumes the
// expected state on the first ProcessAuthorizationResponse call regardless of
// the outcome, so a retry must re-arm it. A scripted token-endpoint failure
// followed by the SAME code/state must succeed.
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

	// First attempt: the token endpoint fails once.
	f.as.SetTokenError("invalid_grant", http.StatusBadRequest)
	if err := flow.Submit(ctx, code, state); err == nil {
		t.Fatal("first Submit succeeded, want the scripted token failure")
	}
	if got, _ := flow.Status(); got != authStatusError {
		t.Fatalf("Status after a failed exchange = %q, want error", got)
	}

	// Second attempt: same code and state, must succeed (the state was re-armed).
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

// TestDoneDistinguishesRetryableFromTerminal is the finding-F9 guard: Status's
// "error" means two different things — a retryable token-exchange failure that
// leaves the flow OPEN, and a terminal cancel/timeout/failed callback — and a
// consumer that cannot tell them apart and then calls Wait blocks until the
// five minute deadline. Done() is the missing bit.
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

	// Cancel/timeout are the OTHER meaning of "error": terminal.
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

// TestDCRIsPersistedAndReused proves a dynamic client registration survives the
// flow and is reused by the next one (a single registration for two flows).
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
	flow1.Cancel() // release the loopback so the next flow can rebind the port

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

// TestCancelFreesThePort proves Cancel is terminal and releases the listener.
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
	// The port must be free again.
	ln2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("the port was not released after Cancel: %v", err)
	}
	_ = ln2.Close()
	// Cancel is idempotent.
	if err := flow.Cancel(); err != nil {
		t.Fatalf("second Cancel: %v", err)
	}
}

// TestTimeoutIsBounded proves a never-completed flow ends on its budget instead
// of hanging.
func TestTimeoutIsBounded(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flow.Cancel()

	// context.WithTimeout is a belt-and-braces guard: a regression that made
	// the flow hang would fail the test instead of blocking forever.
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

// TestStartAuthOnUnreachableServerReportsProbeFailure is the finding-F4 guard:
// the probe must tell three cases apart. A server that completes the handshake
// needs no login (ErrAuthNotRequired); one that answers an authorization
// challenge is the case the flow exists for; one that never answers at all
// (DNS, refused connection, a typo'd URL) must NOT be reported as "the server
// did not request authentication", which sends the operator looking in the
// wrong place.
func TestStartAuthOnUnreachableServerReportsProbeFailure(t *testing.T) {
	store := NewAuthStore(t.TempDir())

	// Port 1 is closed on a Linux host: the connection is refused before any
	// OAuth exchange happens.
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
	// The report names the server, and nothing else: no URL, no token.
	if got, want := err.Error(), `mcp auth "dead": probe: `+ErrAuthProbeFailed.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error %q leaks the URL", err.Error())
	}
}

// TestStartAuthOnServerThatNeedsNoAuth proves the probe distinguishes a healthy
// server from one demanding credentials: an OAuth-attached transport would fail
// before any HTTP call (contract fact 6), so StartAuth would never be able to
// report ErrAuthNotRequired.
func TestStartAuthOnServerThatNeedsNoAuth(t *testing.T) {
	srv := newNoAuthMCPServer(t)
	store := NewAuthStore(t.TempDir())

	_, err := StartAuth(context.Background(), "srv", ServerConfig{URL: srv.URL + "/mcp"}, store, AuthOptions{})
	if !errors.Is(err, ErrAuthNotRequired) {
		t.Fatalf("StartAuth against a server that needs no auth = %v, want %v", err, ErrAuthNotRequired)
	}
}

// TestNoSecretsInFlowErrors collects every error a failing authorization server
// produces and proves none of them echoes a secret. The canaries are injected
// so that a regression which surfaces mcp-go's raw error (it embeds the token
// response body) is caught.
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
	// DCR returns a client secret we must never surface.
	as.SetClientSecretSecret(clientSecretCanary)
	// The token AND registration endpoints' error bodies carry a canary:
	// mcp-go would embed it verbatim in its error.
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

	// Flow A: manual completion whose token exchange fails with the injected
	// canary body.
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
	collect(flowA.Submit(ctx, code, state))         // token-endpoint failure
	collect(flowA.Submit(ctx, code, "wrong-state")) // state mismatch

	// Flow B: the authorization server rejects the request (the loopback error
	// path), and Wait surfaces the terminal error.
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

	// Flow C: a HOSTILE authorization server redirects the browser with a
	// canaried error_description. What a flow-level canary CAN prove is the
	// reduction step: valueFreeError replaces the canaried AS body/code before
	// the error reaches the caller, so the description cannot survive into a
	// collected error. It does NOT pin the loopback itself: run() launders
	// whatever the callback returned, so a loopback that echoed
	// error_description (mutation M11) leaves THIS test green — that property
	// is pinned at the loopback by TestLoopbackNeverSurfacesErrorDescription.
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

	// Flow D: dynamic client registration fails. mcp-go embeds the registrar's
	// response body in its error; the engine replaces it with a fixed sentinel,
	// so the canary the hostile registrar smuggled into the description must
	// not survive. (The metadata-discovery failure path is not driven: the fake
	// serves every discovery variant, and scripting a half-broken one would
	// test mcp-go's JSON handling, not this engine's error reduction.)
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

// TestCheckAuthReportsRequiredAuthenticatedAndMethod pins the THREE-field
// contract of CheckAuth for every combination of "declared carrier" and
// "recorded challenge".
//
// The authority for each expectation:
//
//   - the frozen design decision (plan C1,
//     .lele/tasks/mcp-oauth-plan.md:145-147):
//     auth.required == auth.type == "oauth" || store.auth_required, with the
//     note that a 401 is recorded IN THE STORE ONLY;
//   - the AuthStatus.Required doc comment, which restates exactly that rule;
//   - AuthStatus.Method's doc comment: "oauth" | "headers" | "" (none).
//
// The headers-only row is the regression this table exists for: an entry that
// merely carries headers (frequently non-credential: Accept, User-Agent,
// X-Client-Version) must NOT report "authentication required", while Method
// stays "headers" because that is still the useful carrier hint.
func TestCheckAuthReportsRequiredAuthenticatedAndMethod(t *testing.T) {
	const url = "https://example.test/mcp"

	// headers is the non-credential-headers entry: no auth block, no
	// challenge. A credential-looking Authorization header is used on
	// purpose — even THAT is not evidence of anything the store does not
	// know, because lele never reads header VALUES here.
	headers := ServerConfig{URL: url, Headers: map[string]string{"Authorization": "Bearer api-key"}}
	plain := ServerConfig{URL: url}
	oauth := ServerConfig{URL: url, Auth: &AuthSpec{Type: typeAuthOAuth}}

	tests := []struct {
		name string
		srv  ServerConfig
		// challenged seeds a RECORDED 401 (store.auth_required) for (name,url)
		// before CheckAuth runs, i.e. "the store holds a real challenge".
		challenged bool
		// token seeds a usable stored token (only meaningful with oauth).
		token bool
		want  AuthStatus
	}{
		{
			// plan C1 + doc comment: nothing declared, nothing recorded ⇒ zero
			// value.
			name: "empty entry with no challenge",
			srv:  plain,
			want: AuthStatus{Required: false, Authenticated: false, Method: ""},
		},
		{
			// THE regression: headers are not evidence of auth (doc comment:
			// Required is "declared auth or a 401 was recorded"), and no 401
			// is recorded here.
			name: "headers only, no challenge",
			srv:  headers,
			want: AuthStatus{Required: false, Authenticated: false, Method: "headers"},
		},
		{
			// plan C1: store.auth_required true ⇒ required; the carrier is
			// still headers (Method doc comment), and there is no token.
			name:       "headers plus persisted challenge",
			srv:        headers,
			challenged: true,
			want:       AuthStatus{Required: true, Authenticated: false, Method: "headers"},
		},
		{
			// plan C1: store.auth_required true ⇒ required. No declared
			// carrier, so Method is "" — the zero-config OAuth login is the
			// remedy.
			name:       "no headers, no auth block, persisted challenge",
			srv:        plain,
			challenged: true,
			want:       AuthStatus{Required: true, Authenticated: false, Method: ""},
		},
		{
			// plan C1: auth.type == "oauth" ⇒ required. Usable only after a
			// login, so Authenticated is false until a token is stored.
			name: "oauth block without a token",
			srv:  oauth,
			want: AuthStatus{Required: true, Authenticated: false, Method: "oauth"},
		},
		{
			// Same as above plus a usable stored token: Authenticated flips,
			// Required and Method do not.
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

	// A nil store is the "no lele directory" case: nothing can be recorded, so
	// only the declared side of the rule can fire, and it must not panic.
	if got := CheckAuth(nil, "srv", oauth); got != (AuthStatus{Required: true, Authenticated: false, Method: "oauth"}) {
		t.Fatalf("CheckAuth(nil store, oauth) = %+v, want oauth required and not authenticated", got)
	}
	if got := CheckAuth(nil, "srv", headers); got != (AuthStatus{Required: false, Authenticated: false, Method: "headers"}) {
		t.Fatalf("CheckAuth(nil store, headers) = %+v, want headers-only, not required", got)
	}
}

// TestListenerSeam proves startLoopback uses the injected listener: a failing
// bind surfaces as an error instead of touching the network.
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

// TestValueFreeErrorMatchesPointerOAuthError covers the pointer form of
// mcp-go's OAuthError. Today's library only wraps the value type, but a pointer
// is one of the shapes the same type can arrive as, and without this branch a
// specific OAuth code would silently degrade to the generic
// "authorization token exchange failed" message.
func TestValueFreeErrorMatchesPointerOAuthError(t *testing.T) {
	if got := valueFreeError(&transport.OAuthError{ErrorCode: "invalid_grant"}); got.Error() != "authorization failed: invalid_grant" {
		t.Errorf("pointer OAuthError = %q, want the sanitized code", got.Error())
	}
	if got := valueFreeError(transport.OAuthError{ErrorCode: "invalid_grant"}); got.Error() != "authorization failed: invalid_grant" {
		t.Errorf("value OAuthError = %q, want the sanitized code", got.Error())
	}
	// A code that is not a safe token still falls back, whichever form it has.
	unsafe := "invalid grant <script>"
	if got := valueFreeError(&transport.OAuthError{ErrorCode: unsafe}); !errors.Is(got, errAuthExchangeFailed) {
		t.Errorf("unsafe pointer code = %q, want the fixed message", got.Error())
	}
	// A typed nil pointer must not panic and must not be read as a code.
	var nilPtr *transport.OAuthError
	if got := valueFreeError(nilPtr); !errors.Is(got, errAuthExchangeFailed) {
		t.Errorf("nil *OAuthError = %v, want the fixed message", got)
	}
	if got := valueFreeError(nil); got != nil {
		t.Errorf("valueFreeError(nil) = %v, want nil", got)
	}
}

// TestStartAuthRejectsRelativeRedirectURI covers the F10 redirect-URI guard:
// mcp-go passes OAuthConfig.RedirectURI verbatim to /authorize and to the token
// request and never validates it (see authtest's contract note (5)), so an
// override must be an absolute http/https URL with a host or the flow would
// build an authorization URL nobody can follow.
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
		// The two dangerous shapes: a script-scheme URI the AS must never be
		// handed, and a scheme-relative URL (no scheme) that would resolve
		// against the AS's own host.
		"javascript:alert(1)",
		"//evil.example/cb",
		// No host: a scheme with nothing to redirect to, and a bare fragment.
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

	// A conforming override still flows (and binds no listener: the URI is not
	// a loopback http one).
	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{RedirectURI: "https://client.example/cb"})
	if err != nil {
		t.Fatalf("StartAuth with a valid redirect uri: %v", err)
	}
	defer flow.Cancel()
	if got := flow.RedirectURI(); got != "https://client.example/cb" {
		t.Fatalf("RedirectURI = %q, want the override", got)
	}
}

// TestLoopbackServesOnlyGetAndPostOnTheCallbackPath covers the F10 hardening:
// the callback listener answers 404 elsewhere and 405 on the callback path for
// any method other than GET/POST. Only the GET/POST request may deliver a
// result, so the rejected methods cannot drive a token exchange.
func TestLoopbackServesOnlyGetAndPostOnTheCallbackPath(t *testing.T) {
	lb, err := startLoopback("http://127.0.0.1:0/callback", nil)
	if err != nil {
		t.Fatalf("startLoopback: %v", err)
	}
	defer lb.Close()

	// A wrong path is a 404, whatever the method.
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

	// Every other method on the callback path is a 405 with an Allow header.
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
	// None of the rejected requests delivered a result to the flow.
	noResultCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := lb.Await(noResultCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Await after the rejected methods = %v, want a timeout (no result was delivered)", err)
	}

	// A GET on the callback path IS served (400: the request carries no code,
	// but it is neither a 404 nor a 405) and delivers the flow's result.
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

// TestLoopbackAcceptsFormPostCallbackBody pins the form_post half of the same
// contract the test above covers for GET: the handler's doc says GET (the
// ordinary redirect) and POST (response_mode=form_post) are accepted, and a
// POSTing authorization server puts code/state in the urlencoded BODY, not the
// query string. Reading only r.URL.Query() would answer 400 "authorization
// callback carried no code" and the login could never complete — so this test
// drives a real PostForm and demands the exact code and state back through
// Await. It is the falsifier for the r.Form-vs-r.URL.Query() read.
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
	// PostForm sends Content-Type: application/x-www-form-urlencoded and puts
	// code/state in the BODY, i.e. nowhere the query string can see.
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

// TestLoopbackBoundsTheCallbackBody pins the 64 KiB cap the form_post path puts
// on a callback body. The handler reads r.Form, and ParseForm's own default
// would let a single unauthenticated localhost POST allocate up to 10 MiB
// before any code was looked at. An oversized body must therefore NOT be
// honoured: the exchange still refuses with a value-free 'no code', exactly as
// before the cap existed. A body comfortably under the cap must still complete,
// so the bound is proved to be a bound and not a blanket refusal.
func TestLoopbackBoundsTheCallbackBody(t *testing.T) {
	// The request body is written by a big reader; the server can answer (and
	// close) before it is drained, so a transport error is tolerated here — it
	// is the DELIVERED result, not the HTTP status, that decides the test.
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

		// 1 MiB of urlencoded form data carrying a VALID code: the cap has to
		// win over the code, which is the whole point. Well under ParseForm's
		// old 10 MiB default, so only the new cap can refuse it.
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

// TestLoopbackNeverSurfacesErrorDescription is the finding-F7/F8 guard at the
// only level where it is falsifiable: `error_description` is attacker-controlled
// prose (the authorization server may echo back whatever the client was sent),
// so it must never appear in the error Await returns nor in the page the
// browser gets.
//
// A FLOW-level assertion cannot see this: run() launders whatever Await returns
// through valueFreeError before it becomes the flow's error, which is defence in
// depth — but it also means a mutation that started echoing the description
// would stay invisible to a canary collected from the flow. Checking the
// loopback directly closes that gap.
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

// TestAwaitReturnsPromptlyAfterClose covers the F10 liveness guard: Close must
// wake a waiting Await instead of leaving it blocked until the flow's five
// minute deadline (or forever, with a ctx that has no deadline at all).
func TestAwaitReturnsPromptlyAfterClose(t *testing.T) {
	lb, err := startLoopback("http://127.0.0.1:0/callback", nil)
	if err != nil {
		t.Fatalf("startLoopback: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := lb.Await(context.Background()) // deliberately NO deadline
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
	// Close stays idempotent.
	if err := lb.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestPackageDoesNotImportAuth is the import guard: pkg/mcp must stay free of
// pkg/auth, pkg/config and pkg/agent (the opts injection points exist precisely
// so it does not need them).
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

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// tokenCredentialKeys is the denylist of token-endpoint parameter names whose
// VALUES are credentials, so a failure message may name the key but must never
// print what it carried: code_verifier is the PKCE secret, client_secret the
// confidential-client secret, code and refresh_token are single-use bearer
// material, and assertion/client_assertion are signed client assertions.
//
// `code` IS redacted, and that is the policy shared with the fake's own
// redaction helpers (pkg/mcp/authtest's secretFormKeys): an authorization code
// is single-use bearer material — while it lives it can be exchanged for a
// token, exactly like the refresh token beside it — and a name is enough to
// make a failure message useful. The two denylists must not disagree about it.
var tokenCredentialKeys = map[string]bool{
	"code_verifier":    true,
	"client_secret":    true,
	"code":             true,
	"refresh_token":    true,
	"assertion":        true,
	"client_assertion": true,
}

// redactTokenRequests renders token observations for a failure message with
// every credential replaced by "[redacted]".
//
// Blanking the credential FIELDS alone is not enough: authtest.TokenRequest
// also mirrors the parsed form into Raw, which holds the SAME secrets under
// their wire keys (code_verifier, client_secret, code, ...), so a helper that
// only touched the fields still printed a live PKCE verifier and client secret
// into CI logs. Both are covered here; the Raw map is copied, never mutated,
// and a nil Raw is fine. Non-credential keys (grant_type, scope, redirect_uri,
// ...) stay readable, which is what keeps the message useful for debugging.
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

// TestRedactTokenRequestsHidesCredentials guards redactTokenRequests against a
// regression it already had: blanking the observed FIELDS is not enough because
// authtest.TokenRequest also mirrors the parsed form into Raw, where the very
// same secrets sit under their wire keys (code_verifier, client_secret, ...).
//
// The verifier and the client secret asserted on below are the values the REAL
// flow produced against the fake AS (the client secret comes from dynamic
// client registration), not hand-made strings, so a helper that leaks anything
// the engine really sent fails here. A non-secret key (grant_type) must stay
// legible: the redaction has to keep the message useful for debugging.
//
// NO LIVE CREDENTIAL IS EVER PRINTED BY THIS TEST, on success or on failure:
// the verifier and the secret appear only as lengths, a short hash and a
// yes/no, and the failure messages name the field, never its value. That is the
// whole point — a redaction test that logs the very secret it protects would
// leak it into CI logs exactly when it is -v or failing.
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
		// Lengths only: the mismatch is diagnosable without echoing either
		// secret into the log.
		t.Fatalf("token request client_secret mismatch (len got %d, want %d)", len(got), len(clientSecret))
	}
	// The secrets are ALSO in Raw under their wire keys: that is the map the
	// helper used to print verbatim.
	for key, want := range map[string]string{"code_verifier": verifier, "client_secret": clientSecret} {
		if got := tokens[0].Raw.Get(key); got != want {
			t.Fatalf("Raw[%s] does not carry the field value (len got %d, want %d)", key, len(got), len(want))
		}
	}

	got := redactTokenRequests(tokens)
	// Non-secret evidence only: lengths plus a short hash, so the assertion
	// below stays identifiable without the value ever reaching the log. `got`
	// itself is deliberately NOT logged or echoed in a failure message: when the
	// helper regresses it is exactly where the live credentials reappear, so
	// printing it would leak them at the very moment the test is failing.
	t.Logf("real credentials: code_verifier(len=%d sha256=%s) client_secret(len=%d sha256=%s)",
		len(verifier), shortHash(verifier), len(clientSecret), shortHash(clientSecret))
	for name, secret := range map[string]string{"code_verifier": verifier, "client_secret": clientSecret} {
		if strings.Contains(got, secret) {
			// Name the field, never the value it carried, and never `got`.
			t.Errorf("redacted output still contains the real %s", name)
		}
	}
	if !strings.Contains(got, "grant_type") || !strings.Contains(got, "authorization_code") {
		t.Errorf("redacted output dropped the diagnostic grant_type")
	}
}

// shortHash is a short, non-reversible fingerprint of a value, so a test can
// keep a failure log identifiable WITHOUT ever printing the value itself.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// redactedRecord renders a stored record for a failure message without any
// credential: URL, client id and flags are safe to print, the token is not.
func redactedRecord(rec AuthRecord) string {
	return fmt.Sprintf("AuthRecord{URL:%q ClientID:%q ClientSecret:%s ResourceMetadataURL:%q RedirectURI:%q AuthRequired:%t Token:%s}",
		rec.URL, rec.ClientID, redactValue(rec.ClientSecret), rec.ResourceMetadataURL, rec.RedirectURI, rec.AuthRequired, redactedToken(rec.Token))
}

// redactedToken renders a stored token for a failure message without its
// values.
func redactedToken(tok *transport.Token) string {
	if tok == nil {
		return "nil"
	}
	return fmt.Sprintf("Token{AccessToken:%s TokenType:%q RefreshToken:%s ExpiresAt:%s Scope:%q}",
		redactValue(tok.AccessToken), tok.TokenType, redactValue(tok.RefreshToken), tok.ExpiresAt, tok.Scope)
}

// redactValue hides a non-empty secret and keeps "empty" legible.
func redactValue(s string) string {
	if s == "" {
		return `""(empty)`
	}
	return "[redacted]"
}

// newNoAuthMCPServer serves just enough of the MCP handshake to complete
// initialize without credentials: server/discover is refused (so the client
// falls back to legacy initialize), initialize answers, and the initialized
// notification is accepted.
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
			// Refuse server/discover so the client falls back to initialize.
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// canaryBodyHandler rewrites the /token and /register response bodies to carry
// a canary, so a regression that surfaces mcp-go's raw error (which embeds the
// response body — and with it whatever description the server chose) is
// detectable on both credential paths.
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
	// Both scripted failures (the token one and the registration one) contain
	// the word "scripted"; a successful body does not, so only a failing
	// response is ever annotated.
	body := strings.Replace(rec.Body.String(), "scripted", "scripted "+h.canary, 1)
	_, _ = io.WriteString(w, body)
}
