// Package authtest_test drives the fake authorization server from OUTSIDE the
// package (authtest_test), which is itself part of the deliverable: it proves
// that a normal test package can import pkg/mcp/authtest without pulling in
// anything test-only.
//
// Where possible the tests drive mcp-go's REAL code — transport.NewOAuthHandler,
// GetServerMetadata, RegisterClient, GetAuthorizationURL,
// ProcessAuthorizationResponse, GenerateCodeVerifier/GenerateCodeChallenge —
// against the fake, rather than re-implementing the client side. That is what
// turns "the JSON we serve looks right" into "the library parses it and the
// flow completes".
package authtest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/xilistudios/lele/pkg/mcp/authtest"
)

// ---------------------------------------------------------------------------
// fixture helpers
// ---------------------------------------------------------------------------

type fixture struct {
	t *testing.T
	// noRedirect is an http.Client that STOPS at the /authorize 302 instead of
	// chasing the redirect_uri (which points nowhere).
	noRedirect *http.Client
	srv        *httptest.Server
	s          *authtest.Server
}

func newFixture(t *testing.T, opts ...authtest.Option) *fixture {
	t.Helper()
	s := authtest.New(opts...)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	// The test hands the fake its own public URL once the listener is up.
	s.SetIssuer(srv.URL)
	return &fixture{
		t:   t,
		srv: srv,
		s:   s,
		noRedirect: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// request performs one HTTP request and returns the response plus its fully
// read body.
func request(t *testing.T, hc *http.Client, method, target, contentType, body string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, target, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %s", method, target, redactError(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body of %s %s: %v", method, target, err)
	}
	return resp, raw
}

// jsonMap decodes a JSON body and echoes it verbatim if that fails, which is
// the useful diagnostic for a discovery document. Do NOT use it on a
// token-endpoint RESPONSE: that body carries an access token and a refresh
// token even when the test only meant to read its `error` field — use
// tokenJSONMap, which redacts.
func jsonMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode JSON body %q: %v", raw, err)
	}
	return m
}

func decodeInto(t *testing.T, raw []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("decode %q into %T: %v", raw, v, err)
	}
}

func tokenForm(fields map[string]string) (string, string) {
	values := url.Values{}
	for k, v := range fields {
		values.Set(k, v)
	}
	return values.Encode(), "application/x-www-form-urlencoded"
}

func containsAll(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", what, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// redaction helpers (display only)
//
// The observations this fake hands back carry REAL credentials: a PKCE
// verifier, an access token, a refresh token, a client secret. A test that
// FAILS must still be safe to paste into a CI log, so nothing that can carry a
// credential is printed raw. These all go through one of the renderers below:
//
//   - the observation structs: AuthorizeRequest, RegisterRequest,
//     TokenRequest (and slices of them), ResourceRequest;
//   - the transport.Token the fake issues;
//   - every token-endpoint RESPONSE body, whether the test holds it as bytes
//     (redactTokenBody) or mcp-go has embedded it in an error string
//     (redactError) — extractOAuthError echoes the body verbatim whenever
//     the body is not a well-formed OAuth error (mcp-go oauth.go:353);
//   - the registration-response body, which can carry a client_secret;
//   - a URL that can carry an authorization `code`: the redirect Location
//     (its query through redactFormValues) and every error string that embeds
//     a Location — the transport error of a redirect-stopping client, and the
//     url.Error of a failed url.Parse (both through redactError).
//
// Credential values become "[redacted]" while the structural fields that give
// the message its diagnostic value (grant type, which keys were present, token
// type, expiry, scope) stay legible. Printed verbatim: the fake's OWN documents
// (discovery metadata and the protected-resource body), which are built from
// the issuer, the scopes and constants and never from a request — so they hold
// no credential to hide. Nothing here is asserted on; it only shapes what is
// printed.
// ---------------------------------------------------------------------------

// redactedValue replaces a non-empty credential; an empty one stays visibly
// empty so "the field was absent" is still distinguishable from "hidden".
const redactedValue = "[redacted]"

// secretFormKeys is the denylist of parameter names whose VALUES are
// credentials, applied to every url.Values that gets printed. It is kept in
// step with pkg/mcp's tokenCredentialKeys: an authorization `code` IS redacted
// in both, because it is single-use bearer material — while it lives it can be
// exchanged for a token, exactly like the refresh token beside it — and the
// parameter NAME is enough to make a failure message useful. The two denylists
// must not disagree about it.
var secretFormKeys = map[string]bool{
	"code":             true,
	"code_verifier":    true,
	"client_secret":    true,
	"access_token":     true,
	"refresh_token":    true,
	"assertion":        true,
	"client_assertion": true,
}

// hideSecret hides a non-empty credential and keeps an empty value legible.
func hideSecret(s string) string {
	if s == "" {
		return `""`
	}
	return redactedValue
}

// redactFormValues renders url.Values with every denylisted value blanked, in
// sorted key order so the message is stable.
func redactFormValues(v url.Values) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if secretFormKeys[k] {
			parts = append(parts, k+":"+redactedValue)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%v", k, v[k]))
	}
	return "map[" + strings.Join(parts, " ") + "]"
}

// redactToken renders a transport.Token without its credentials.
func redactToken(tok *transport.Token) string {
	if tok == nil {
		return "Token(nil)"
	}
	return fmt.Sprintf("Token{AccessToken:%s TokenType:%q RefreshToken:%s ExpiresIn:%d Scope:%q ExpiresAt:%v}",
		hideSecret(tok.AccessToken), tok.TokenType, hideSecret(tok.RefreshToken),
		tok.ExpiresIn, tok.Scope, tok.ExpiresAt)
}

// redactTokenRequest renders one token-endpoint observation without its
// credentials — the mirrored fields AND the Raw form. The authorization `code`
// is hidden like every other credential (see secretFormKeys).
func redactTokenRequest(r authtest.TokenRequest) string {
	return fmt.Sprintf("TokenRequest{GrantType:%q Code:%s CodeVerifier:%s RedirectURI:%q ClientID:%q ClientSecret:%s RefreshToken:%s Resource:%q Raw:%s}",
		r.GrantType, hideSecret(r.Code), hideSecret(r.CodeVerifier), r.RedirectURI, r.ClientID,
		hideSecret(r.ClientSecret), hideSecret(r.RefreshToken), r.Resource, redactFormValues(r.Raw))
}

// redactTokenRequests renders a slice of token-endpoint observations.
func redactTokenRequests(reqs []authtest.TokenRequest) string {
	parts := make([]string, len(reqs))
	for i, r := range reqs {
		parts[i] = redactTokenRequest(r)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// redactAuthorizeRequest renders one /authorize observation. Its Raw is a copy
// of the whole query, so the struct is re-rendered field by field rather than
// %+v-printed: a field added to it later then becomes a deliberate decision
// instead of an accidental leak.
func redactAuthorizeRequest(r authtest.AuthorizeRequest) string {
	return fmt.Sprintf("AuthorizeRequest{Method:%q ClientID:%q RedirectURI:%q ResponseType:%q Scope:%q State:%q CodeChallenge:%q CodeChallengeMethod:%q Raw:%s}",
		r.Method, r.ClientID, r.RedirectURI, r.ResponseType, r.Scope, r.State,
		r.CodeChallenge, r.CodeChallengeMethod, redactFormValues(r.Raw))
}

// redactRegisterRequest renders one dynamic-client-registration observation.
// Raw is the decoded request body, and a client is free to put a client_secret
// in it, so it goes through the same denylist as everything else.
func redactRegisterRequest(r authtest.RegisterRequest) string {
	return fmt.Sprintf("RegisterRequest{Method:%q ContentType:%q ClientID:%q RedirectURIs:%v Raw:%s Form:%s}",
		r.Method, r.ContentType, r.ClientID, r.RedirectURIs, redactObjectMap(r.Raw), redactFormValues(r.Form))
}

// redactResourceRequest renders one protected-resource observation. The struct
// stores the Authorization SCHEME, never the credential — and the credential
// half of a `Bearer <token>` value is dropped here as well, so even a fake that
// recorded the whole header cannot print a token.
func redactResourceRequest(r authtest.ResourceRequest) string {
	scheme, _, _ := strings.Cut(r.Authorization, " ")
	return fmt.Sprintf("ResourceRequest{Method:%q Authorization:%q Accept:%q HasBearer:%t}",
		r.Method, scheme, r.Accept, r.HasBearer)
}

// redactResourceRequests renders a slice of protected-resource observations.
func redactResourceRequests(reqs []authtest.ResourceRequest) string {
	parts := make([]string, len(reqs))
	for i, r := range reqs {
		parts[i] = redactResourceRequest(r)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// redactObjectMap renders a decoded JSON object — a registration body or a
// token-endpoint response body — with every denylisted value blanked, in sorted
// key order so the message is stable.
func redactObjectMap(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if secretFormKeys[k] {
			parts = append(parts, k+":"+redactedValue)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%v", k, m[k]))
	}
	return "map[" + strings.Join(parts, " ") + "]"
}

// redactTokenBody renders a raw JSON token-endpoint response with every
// credential value blanked, so a failure can still show the body's SHAPE (which
// keys came back, the `error` field) without printing an access or refresh
// token. The denylist is the same secretFormKeys used everywhere else. A body
// that does not parse is shown as its length alone: a JSON array or a bare
// string would decode into no map either, and showing the length leaks nothing.
func redactTokenBody(raw []byte) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Sprintf("<%d bytes, not JSON>", len(raw))
	}
	return redactObjectMap(m)
}

// credentialValue matches a credential-looking key/value pair in free-form text,
// in both the JSON spelling ("access_token":"…") and the form spelling
// (access_token=…). The keys mirror secretFormKeys, and the leading group stops
// the match from starting inside a word, so `authorization_code=` is left alone.
var credentialValue = regexp.MustCompile(
	`(?i)(^|[^A-Za-z0-9_])((?:"?(?:code_verifier|client_assertion|client_secret|access_token|refresh_token|assertion|code)"?))\s*[:=]\s*("[^"]*"|[^\s,;&}]+)`,
)

// tokenJSONMap decodes a token-endpoint RESPONSE body. It is jsonMap with the
// one difference that matters: a decode failure reports the body through
// redactTokenBody instead of echoing it, because a body that fails to parse can
// still carry a live access token — this branch runs precisely when the fake
// returned something the test did not expect.
func tokenJSONMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode token response body: %v: %s", err, redactTokenBody(raw))
	}
	return m
}

// decodeTokenInto decodes a token-endpoint response. Unlike decodeInto it never
// echoes the raw body on failure: this body carries the access and refresh
// tokens, so a decode error would print them. redactTokenBody keeps the
// diagnostic shape instead.
func decodeTokenInto(t *testing.T, raw []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("decode token response into %T: %v: %s", v, err, redactTokenBody(raw))
	}
}

// redactError renders an error from an HTTP call in a form that is safe to
// paste. Two unrelated mechanisms make that necessary:
//
//   - mcp-go's extractOAuthError embeds the RAW response body in its message
//     whenever that body is not a well-formed OAuth error (oauth.go:353), so
//     `%v` on an error from the token endpoint or the registration endpoint can
//     print an access token or a client secret;
//   - the transport error of a redirect-stopping client embeds the Location
//     header (net/http client.go:653), and a Location is where an authorization
//     `code` travels.
//
// The values of the credential keys are blanked; the status, the endpoint and
// the OAuth error code stay legible. This matters because these branches run
// exactly when the fake returned something the test did not expect — the case
// that must stay safe to paste into a CI log.
func redactError(err error) string {
	if err == nil {
		return "<nil>"
	}
	return credentialValue.ReplaceAllString(err.Error(), "${1}${2}:[redacted]")
}

// ---------------------------------------------------------------------------
// (1)+(2) discovery: which URLs mcp-go asks for, and the types it decodes into
// ---------------------------------------------------------------------------

// TestDiscoveryDocumentsDecodeIntoRealTransportTypes asserts the served
// documents against the REAL mcp-go metadata structs. A wrong json tag in the
// fake would leave the field zero and fail here, so this is a field-name
// compatibility proof and not just a smoke test.
func TestDiscoveryDocumentsDecodeIntoRealTransportTypes(t *testing.T) {
	f := newFixture(t, authtest.WithScopes("mcp", "offline_access"))

	// The path-suffixed protected-resource URL is the one mcp-go builds for a
	// base URL with a path (oauth.go:762, RFC 9728 path insertion). The bare
	// one is the URL this fake advertises in its 401. Both must serve the same
	// document.
	for _, path := range []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
	} {
		resp, raw := request(t, f.srv.Client(), http.MethodGet, f.srv.URL+path, "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("GET %s: Content-Type %q, want application/json", path, ct)
		}

		var prm transport.OAuthProtectedResource
		decodeInto(t, raw, &prm)
		if prm.Resource != f.srv.URL+"/mcp" {
			t.Errorf("GET %s: resource = %q, want %q", path, prm.Resource, f.srv.URL+"/mcp")
		}
		if len(prm.AuthorizationServers) != 1 || prm.AuthorizationServers[0] != f.srv.URL {
			t.Errorf("GET %s: authorization_servers = %v, want [%s]", path, prm.AuthorizationServers, f.srv.URL)
		}

		// transport.OAuthProtectedResource has no field for these, so they are
		// asserted on the raw JSON.
		doc := jsonMap(t, raw)
		if got := doc["bearer_methods_supported"]; !equalStrings(got, []string{"header"}) {
			t.Errorf("GET %s: bearer_methods_supported = %v, want [header]", path, got)
		}
		if got := doc["scopes_supported"]; !equalStrings(got, []string{"mcp", "offline_access"}) {
			t.Errorf("GET %s: scopes_supported = %v, want [mcp offline_access]", path, got)
		}
	}

	for _, path := range []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/openid-configuration",
	} {
		resp, raw := request(t, f.srv.Client(), http.MethodGet, f.srv.URL+path, "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
		}

		var md transport.AuthServerMetadata
		decodeInto(t, raw, &md)
		if md.Issuer != f.srv.URL {
			t.Errorf("GET %s: issuer = %q, want %q", path, md.Issuer, f.srv.URL)
		}
		if md.AuthorizationEndpoint != f.srv.URL+"/authorize" {
			t.Errorf("GET %s: authorization_endpoint = %q", path, md.AuthorizationEndpoint)
		}
		if md.TokenEndpoint != f.srv.URL+"/token" {
			t.Errorf("GET %s: token_endpoint = %q", path, md.TokenEndpoint)
		}
		if md.RegistrationEndpoint != f.srv.URL+"/register" {
			t.Errorf("GET %s: registration_endpoint = %q", path, md.RegistrationEndpoint)
		}
		if !equalStrings(md.ResponseTypesSupported, []string{"code"}) {
			t.Errorf("GET %s: response_types_supported = %v", path, md.ResponseTypesSupported)
		}
		if !equalStrings(md.CodeChallengeMethodsSupported, []string{"S256"}) {
			t.Errorf("GET %s: code_challenge_methods_supported = %v", path, md.CodeChallengeMethodsSupported)
		}
		if !contains(md.GrantTypesSupported, "authorization_code") || !contains(md.GrantTypesSupported, "refresh_token") {
			t.Errorf("GET %s: grant_types_supported = %v", path, md.GrantTypesSupported)
		}
		if !contains(md.TokenEndpointAuthMethodsSupported, "none") {
			t.Errorf("GET %s: token_endpoint_auth_methods_supported = %v", path, md.TokenEndpointAuthMethodsSupported)
		}
	}
}

// TestDiscoveryDrivenByRealOAuthHandler drives mcp-go's OWN discovery code and
// asserts the exact well-known paths it requested, in order. This is the
// executable form of finding (1): the suffix IS appended to the path.
func TestDiscoveryDrivenByRealOAuthHandler(t *testing.T) {
	f := newFixture(t, authtest.WithScopes("mcp"))

	h := transport.NewOAuthHandler(transport.OAuthConfig{
		RedirectURI: "http://127.0.0.1:9/callback",
		Scopes:      []string{"mcp"},
		PKCEEnabled: true,
	})
	// This is what NewStreamableHTTP does internally (streamable_http.go:178-185):
	// the FULL server URL, path included, becomes the OAuth handler base URL.
	h.SetBaseURL(f.srv.URL + "/mcp")

	md, err := h.GetServerMetadata(context.Background())
	if err != nil {
		t.Fatalf("GetServerMetadata: %v", err)
	}
	if md.Issuer != f.srv.URL || md.TokenEndpoint != f.srv.URL+"/token" || md.RegistrationEndpoint != f.srv.URL+"/register" {
		t.Fatalf("discovered metadata = %+v, want issuer %s", md, f.srv.URL)
	}

	got := f.s.WellKnownRequests()
	want := []string{
		"/.well-known/oauth-protected-resource/mcp",
		"/.well-known/oauth-authorization-server",
	}
	if len(got) != len(want) {
		t.Fatalf("well-known requests = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("well-known request %d = %q, want %q (full order: %v)", i, got[i], want[i], got)
		}
	}
}

// TestAdvertisedResourceMetadataURL exercises the OTHER discovery entry point:
// a 401 whose WWW-Authenticate header advertises the protected-resource
// metadata URL. mcp-go origin-validates that URL and then REQUIRES the
// document's `resource` to equal the base URL, so this test also pins the
// most surprising part of the real contract.
func TestAdvertisedResourceMetadataURL(t *testing.T) {
	f := newFixture(t)

	h := transport.NewOAuthHandler(transport.OAuthConfig{
		RedirectURI: "http://127.0.0.1:9/callback",
		Scopes:      []string{"mcp"},
		PKCEEnabled: true,
	})
	h.SetBaseURL(f.srv.URL + "/mcp")

	resp, _ := request(t, f.srv.Client(), http.MethodGet, f.srv.URL+"/mcp", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("resource status = %d, want 401", resp.StatusCode)
	}
	h.HandleUnauthorizedResponse(resp)

	md, err := h.GetServerMetadata(context.Background())
	if err != nil {
		t.Fatalf("GetServerMetadata after advertised 401: %v", err)
	}
	if md.TokenEndpoint != f.srv.URL+"/token" {
		t.Fatalf("token endpoint = %q", md.TokenEndpoint)
	}
	// The advertised (un-suffixed) URL is what got fetched this time.
	if got := f.s.WellKnownRequests(); len(got) == 0 || got[0] != "/.well-known/oauth-protected-resource" {
		t.Fatalf("well-known requests = %v, want the advertised un-suffixed URL first", got)
	}
}

// TestAdvertisedResourceMetadataMismatchIsFatal is the falsifiability guard for
// the `resource` field: when the metadata is reached through an advertised URL,
// a `resource` that is not the MCP endpoint URL must make discovery FAIL.
func TestAdvertisedResourceMetadataMismatchIsFatal(t *testing.T) {
	// The resource lives at /other, but the client is configured for /mcp.
	f := newFixture(t, authtest.WithResourcePath("/other"))

	h := transport.NewOAuthHandler(transport.OAuthConfig{
		RedirectURI: "http://127.0.0.1:9/callback",
		Scopes:      []string{"mcp"},
		PKCEEnabled: true,
	})
	h.SetBaseURL(f.srv.URL + "/mcp")

	resp, _ := request(t, f.srv.Client(), http.MethodGet, f.srv.URL+"/other", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("resource status = %d, want 401", resp.StatusCode)
	}
	h.HandleUnauthorizedResponse(resp)

	if _, err := h.GetServerMetadata(context.Background()); err == nil {
		t.Fatal("GetServerMetadata succeeded, want a resource/base-URL mismatch error")
	} else if !strings.Contains(err.Error(), "does not match base URL") {
		t.Fatalf("error = %v, want it to mention the base URL mismatch", err)
	}
}

// ---------------------------------------------------------------------------
// PKCE round trip, driven by the real handler
// ---------------------------------------------------------------------------

// TestAuthorizationCodePKCERoundTrip runs the whole flow through mcp-go:
// GetAuthorizationURL → GET /authorize → ProcessAuthorizationResponse. The
// falsifiability guard is the wrong-verifier attempt: it must be rejected with
// invalid_grant AND must leave the code redeemable, which the immediately
// following correct attempt proves.
func TestAuthorizationCodePKCERoundTrip(t *testing.T) {
	f := newFixture(t, authtest.WithScopes("mcp", "offline_access"))

	const redirectURI = "http://127.0.0.1:12345/callback"
	store := transport.NewMemoryTokenStore()
	h := transport.NewOAuthHandler(transport.OAuthConfig{
		ClientID:    "lele-test-client",
		RedirectURI: redirectURI,
		Scopes:      []string{"mcp", "offline_access"},
		TokenStore:  store,
		PKCEEnabled: true,
	})
	h.SetBaseURL(f.srv.URL + "/mcp")

	// The library's own PKCE helpers.
	verifier, err := transport.GenerateCodeVerifier()
	if err != nil {
		t.Fatalf("GenerateCodeVerifier: %v", err)
	}
	challenge := transport.GenerateCodeChallenge(verifier)
	state, err := transport.GenerateState()
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}

	authorizeURL, err := h.GetAuthorizationURL(context.Background(), state, challenge)
	if err != nil {
		t.Fatalf("GetAuthorizationURL: %v", err)
	}

	// Stop at the 302: the redirect target does not exist, and a client that
	// followed it would hide the code.
	resp, _ := request(t, f.noRedirect, http.MethodGet, authorizeURL, "", "")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("/authorize status = %d, want 302", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	redirected, err := url.Parse(location)
	if err != nil {
		// Never echo the raw Location -- and never the url.Error either: it
		// embeds the URL it failed on, which is where the authorization code
		// travels.
		t.Fatalf("parse Location (len %d): %s", len(location), redactError(err))
	}
	base := redirected.Scheme + "://" + redirected.Host + redirected.Path
	if base != redirectURI {
		t.Errorf("redirected to %q, want %q", base, redirectURI)
	}
	if got := redirected.Query().Get("state"); got != state {
		t.Fatalf("redirected state = %q, want the echoed %q", got, state)
	}
	code := redirected.Query().Get("code")
	if code == "" {
		// Never echo the raw Location: it is where an authorization code
		// would be, in the query or in a fragment. The rendered query keeps
		// the diagnostic (which parameters came back) without the value.
		t.Fatalf("no code in redirect: %s", redactFormValues(redirected.Query()))
	}

	// The authorize request as the fake saw it.
	observed := f.s.LastAuthorize()
	if observed.Method != http.MethodGet {
		t.Errorf("observed method = %q, want GET", observed.Method)
	}
	if observed.ClientID != "lele-test-client" || observed.ResponseType != "code" {
		t.Errorf("observed authorize = %s", redactAuthorizeRequest(observed))
	}
	if observed.CodeChallenge != challenge || observed.CodeChallengeMethod != "S256" {
		t.Errorf("observed PKCE = (%q, %q), want (%q, S256)", observed.CodeChallenge, observed.CodeChallengeMethod, challenge)
	}
	if observed.Scope != "mcp offline_access" {
		t.Errorf("observed scope = %q, want %q", observed.Scope, "mcp offline_access")
	}

	// Wrong verifier ⇒ invalid_grant, and the code must survive.
	if err := h.ProcessAuthorizationResponse(context.Background(), code, state, verifier+"x"); err == nil {
		t.Fatal("ProcessAuthorizationResponse with a wrong verifier succeeded, want invalid_grant")
	} else if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("wrong-verifier error = %s, want invalid_grant", redactError(err))
	}

	// ProcessAuthorizationResponse CONSUMES the expected state on the first
	// call (oauth.go:1086-1088), so a retry has to re-arm it. This is a real
	// contract detail, not a quirk of the fake.
	h.SetExpectedState(state)
	if err := h.ProcessAuthorizationResponse(context.Background(), code, state, verifier); err != nil {
		t.Fatalf("ProcessAuthorizationResponse with the correct verifier: %s", redactError(err))
	}

	// The handle token must be the token the resource endpoint accepts.
	token, err := store.GetToken(context.Background())
	if err != nil {
		t.Fatalf("TokenStore.GetToken: %v", err)
	}
	if token.TokenType != "Bearer" {
		t.Errorf("stored token_type = %q, want Bearer", token.TokenType)
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		t.Fatalf("stored token is missing credentials: %s", redactToken(token))
	}
	if token.ExpiresAt.IsZero() || token.IsExpired() {
		t.Errorf("stored token expiry = %v (expires_in was not honoured?)", token.ExpiresAt)
	}
	if !strings.Contains(token.Scope, "offline_access") {
		t.Errorf("stored token scope = %q, want the scope echoed by /authorize", token.Scope)
	}

	resourceReq, _ := http.NewRequest(http.MethodGet, f.srv.URL+"/mcp", nil)
	resourceReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	resourceResp, err := f.srv.Client().Do(resourceReq)
	if err != nil {
		t.Fatalf("resource request: %v", err)
	}
	defer resourceResp.Body.Close()
	if resourceResp.StatusCode != http.StatusOK {
		t.Fatalf("resource status with the stored token = %d, want 200", resourceResp.StatusCode)
	}

	// Two token requests: the failed one and the successful one, with the
	// verifiers the client actually sent.
	tokens := f.s.TokenRequests()
	if len(tokens) != 2 {
		t.Fatalf("token requests = %d, want 2: %s", len(tokens), redactTokenRequests(tokens))
	}
	for i, want := range []string{verifier + "x", verifier} {
		if tokens[i].GrantType != "authorization_code" {
			t.Errorf("token request %d grant_type = %q", i, tokens[i].GrantType)
		}
		if tokens[i].Code != code || tokens[i].CodeVerifier != want {
			t.Errorf("token request %d = %s, want (code len %d, verifier len %d)", i, redactTokenRequest(tokens[i]), len(code), len(want))
		}
		if tokens[i].RedirectURI != redirectURI {
			t.Errorf("token request %d redirect_uri = %q, want %q", i, tokens[i].RedirectURI, redirectURI)
		}
		if tokens[i].ClientID != "lele-test-client" {
			t.Errorf("token request %d client_id = %q", i, tokens[i].ClientID)
		}
	}

	// A code is single use.
	body, contentType := tokenForm(map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"code_verifier": verifier,
		"client_id":     "lele-test-client",
		"redirect_uri":  redirectURI,
	})
	resp, raw := request(t, f.srv.Client(), http.MethodPost, f.srv.URL+"/token", contentType, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("replaying the code: status %d, want 400", resp.StatusCode)
	}
	if got := tokenJSONMap(t, raw)["error"]; got != "invalid_grant" {
		t.Fatalf("replayed code error = %v, want invalid_grant", got)
	}
}

// TestAuthorizeRejectsMissingPKCE proves the fake catches a client that
// skipped PKCE instead of silently issuing a code.
func TestAuthorizeRejectsMissingPKCE(t *testing.T) {
	f := newFixture(t)

	cases := []struct {
		name  string
		query url.Values
	}{
		{"no challenge", url.Values{"response_type": {"code"}, "redirect_uri": {"http://127.0.0.1:1/cb"}, "state": {"s1"}}},
		{"plain method", url.Values{
			"response_type": {"code"}, "redirect_uri": {"http://127.0.0.1:1/cb"}, "state": {"s2"},
			"code_challenge": {"abc"}, "code_challenge_method": {"plain"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := request(t, f.noRedirect, http.MethodGet, f.srv.URL+"/authorize?"+tc.query.Encode(), "", "")
			if resp.StatusCode != http.StatusFound {
				t.Fatalf("status = %d, want 302", resp.StatusCode)
			}
			location, err := url.Parse(resp.Header.Get("Location"))
			if err != nil {
				// Same rule as above: the url.Error embeds the Location, and
				// a Location is where a `code` travels.
				t.Fatalf("parse Location: %s", redactError(err))
			}
			if got := location.Query().Get("error"); got != "invalid_request" {
				t.Errorf("error = %q, want invalid_request", got)
			}
			if location.Query().Get("code") != "" {
				t.Errorf("a code was issued despite the failure")
			}
			if got := location.Query().Get("state"); got != tc.query.Get("state") {
				t.Errorf("state = %q, want %q", got, tc.query.Get("state"))
			}
		})
	}
}

// TestAuthorizeScriptedErrorAndScriptedCode covers the scripting knobs.
func TestAuthorizeScriptedErrorAndScriptedCode(t *testing.T) {
	f := newFixture(t)
	redirectURI := "http://127.0.0.1:1/cb"
	params := url.Values{
		"response_type": {"code"}, "client_id": {"cid"}, "redirect_uri": {redirectURI},
		"state": {"st"}, "code_challenge": {"chal"}, "code_challenge_method": {"S256"},
	}

	f.s.SetAuthorizeError("access_denied")
	resp, _ := request(t, f.noRedirect, http.MethodGet, f.srv.URL+"/authorize?"+params.Encode(), "", "")
	location, _ := url.Parse(resp.Header.Get("Location"))
	if got := location.Query().Get("error"); got != "access_denied" {
		t.Fatalf("error = %q, want access_denied", got)
	}

	// One-shot: the next authorize is back to normal.
	f.s.SetAuthorizationCode("fixed-code")
	resp, _ = request(t, f.noRedirect, http.MethodGet, f.srv.URL+"/authorize?"+params.Encode(), "", "")
	location, _ = url.Parse(resp.Header.Get("Location"))
	if got := location.Query().Get("code"); got != "fixed-code" {
		t.Fatalf("code mismatch (got len %d, want len %d)", len(got), len("fixed-code"))
	}

	// A scripted code is redeemable even without a matching verifier only when
	// no challenge was bound to it; here /authorize bound "chal", so the token
	// endpoint must refuse the exchange without the right verifier.
	body, contentType := tokenForm(map[string]string{"grant_type": "authorization_code", "code": "fixed-code"})
	resp, raw := request(t, f.srv.Client(), http.MethodPost, f.srv.URL+"/token", contentType, body)
	if resp.StatusCode != http.StatusBadRequest || tokenJSONMap(t, raw)["error"] != "invalid_grant" {
		t.Fatalf("unbound-verifier exchange: status %d body %s", resp.StatusCode, redactTokenBody(raw))
	}

	if got := len(f.s.AuthorizeRequests()); got != 2 {
		t.Fatalf("authorize observations = %d, want 2", got)
	}
}

// ---------------------------------------------------------------------------
// dynamic client registration
// ---------------------------------------------------------------------------

func TestDynamicClientRegistration(t *testing.T) {
	f := newFixture(t)

	h := transport.NewOAuthHandler(transport.OAuthConfig{
		RedirectURI: "http://127.0.0.1:9/callback",
		Scopes:      []string{"mcp"},
		PKCEEnabled: true,
	})
	h.SetBaseURL(f.srv.URL)

	if err := h.RegisterClient(context.Background(), "lele"); err != nil {
		t.Fatalf("RegisterClient #1: %s", redactError(err))
	}
	firstID := h.GetClientID()
	if err := h.RegisterClient(context.Background(), "lele"); err != nil {
		t.Fatalf("RegisterClient #2: %s", redactError(err))
	}
	secondID := h.GetClientID()

	if firstID == "" || secondID == "" {
		t.Fatalf("registration returned empty ids: %q / %q", firstID, secondID)
	}
	if firstID == secondID {
		t.Fatalf("both registrations returned %q; a persisted client must not be re-registered", firstID)
	}
	if got := f.s.RegisterCount(); got != 2 {
		t.Fatalf("RegisterCount() = %d, want 2", got)
	}

	observed := f.s.LastRegister()
	if observed.Method != http.MethodPost {
		t.Errorf("registration method = %q, want POST", observed.Method)
	}
	if !strings.Contains(observed.ContentType, "application/json") {
		t.Errorf("registration Content-Type = %q, want application/json", observed.ContentType)
	}
	if got := observed.Raw["client_name"]; got != "lele" {
		t.Errorf("client_name = %v, want lele", got)
	}
	if got := observed.Raw["token_endpoint_auth_method"]; got != "none" {
		t.Errorf("token_endpoint_auth_method = %v, want none", got)
	}
	if got := observed.Raw["scope"]; got != "mcp" {
		t.Errorf("scope = %v, want mcp", got)
	}
	if len(observed.RedirectURIs) != 1 || observed.RedirectURIs[0] != "http://127.0.0.1:9/callback" {
		t.Errorf("redirect_uris = %v", observed.RedirectURIs)
	}

	// A copy: mutating what the observer handed back must not corrupt the log.
	observed.Raw["client_name"] = "tampered"
	observed.RedirectURIs[0] = "tampered"
	if got := f.s.LastRegister(); got.Raw["client_name"] != "lele" || got.RedirectURIs[0] != "http://127.0.0.1:9/callback" {
		t.Fatalf("observations are not isolated copies: %s", redactRegisterRequest(got))
	}
}

func TestRegistrationFailureAndClientSecret(t *testing.T) {
	f := newFixture(t, authtest.WithResourcePath("/rpc"))
	f.s.SetClientSecretSecret("s3cr3t")

	h := transport.NewOAuthHandler(transport.OAuthConfig{
		RedirectURI:  "http://127.0.0.1:9/callback",
		Scopes:       []string{"mcp"},
		PKCEEnabled:  true,
		ClientSecret: "s3cr3t",
	})
	h.SetBaseURL(f.srv.URL + "/rpc")

	f.s.FailRegistrationOnce("invalid_redirect_uri")
	if err := h.RegisterClient(context.Background(), "lele"); err == nil {
		t.Fatal("RegisterClient succeeded despite FailRegistrationOnce")
	} else if !strings.Contains(err.Error(), "invalid_redirect_uri") {
		t.Fatalf("registration error = %s, want it to carry invalid_redirect_uri", redactError(err))
	}
	if got := f.s.RegisterCount(); got != 1 {
		t.Fatalf("RegisterCount() = %d after the failure, want 1 (attempts are recorded)", got)
	}

	// One-shot: the retry works, and the client secret is picked up.
	if err := h.RegisterClient(context.Background(), "lele"); err != nil {
		t.Fatalf("RegisterClient after the scripted failure: %s", redactError(err))
	}
	if h.GetClientSecret() != "s3cr3t" {
		t.Fatalf("client secret mismatch (len got %d, want len %d)", len(h.GetClientSecret()), len("s3cr3t"))
	}
	if got := f.s.RegisterCount(); got != 2 {
		t.Fatalf("RegisterCount() = %d, want 2", got)
	}
}

// ---------------------------------------------------------------------------
// token endpoint scripting
// ---------------------------------------------------------------------------

func TestTokenEndpointScriptingAndGrants(t *testing.T) {
	f := newFixture(t)
	f.s.SetAuthorizationCode("scripted-code")

	// 1. A scripted error, consumed once. Status is deliberately checked.
	f.s.SetTokenError("invalid_grant", http.StatusBadRequest)
	body, contentType := tokenForm(map[string]string{"grant_type": "authorization_code", "code": "scripted-code"})
	resp, raw := request(t, f.srv.Client(), http.MethodPost, f.srv.URL+"/token", contentType, body)
	if resp.StatusCode != http.StatusBadRequest || tokenJSONMap(t, raw)["error"] != "invalid_grant" {
		t.Fatalf("scripted token error: status %d body %s", resp.StatusCode, redactTokenBody(raw))
	}

	// 2. The scripted code itself is redeemable (no PKCE was bound to it).
	resp, raw = request(t, f.srv.Client(), http.MethodPost, f.srv.URL+"/token", contentType, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scripted code exchange: status %d body %s", resp.StatusCode, redactTokenBody(raw))
	}
	var issued transport.Token
	decodeTokenInto(t, raw, &issued)
	if issued.AccessToken == "" || issued.RefreshToken == "" || issued.TokenType != "Bearer" {
		t.Fatalf("token response decoded to %s", redactToken(&issued))
	}
	// mcp-go computes ExpiresAt itself from expires_in (oauth.go:1159), so a
	// bare decode of the response legitimately has a zero ExpiresAt; the
	// end-to-end test asserts the computed value through the token store.
	if issued.ExpiresIn <= 0 {
		t.Errorf("expires_in = %d, want > 0", issued.ExpiresIn)
	}
	if expiresAt := time.Now().Add(time.Duration(issued.ExpiresIn) * time.Second); !expiresAt.After(time.Now()) {
		t.Errorf("expires_in %d does not put the token in the future", issued.ExpiresIn)
	}

	// 3. Unknown code ⇒ invalid_grant.
	unknown, unknownCT := tokenForm(map[string]string{"grant_type": "authorization_code", "code": "nope"})
	resp, raw = request(t, f.srv.Client(), http.MethodPost, f.srv.URL+"/token", unknownCT, unknown)
	if resp.StatusCode != http.StatusBadRequest || tokenJSONMap(t, raw)["error"] != "invalid_grant" {
		t.Fatalf("unknown code: status %d body %s", resp.StatusCode, redactTokenBody(raw))
	}

	// 4. Unsupported grant type.
	bad, badCT := tokenForm(map[string]string{"grant_type": "client_credentials"})
	resp, raw = request(t, f.srv.Client(), http.MethodPost, f.srv.URL+"/token", badCT, bad)
	if resp.StatusCode != http.StatusBadRequest || tokenJSONMap(t, raw)["error"] != "unsupported_grant_type" {
		t.Fatalf("unsupported grant: status %d body %s", resp.StatusCode, redactTokenBody(raw))
	}

	// 5. JSON clients work too (the endpoint accepts application/json).
	jsonBody := `{"grant_type":"refresh_token","refresh_token":"` + issued.RefreshToken + `"}`
	resp, raw = request(t, f.srv.Client(), http.MethodPost, f.srv.URL+"/token", "application/json", jsonBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("JSON refresh: status %d body %s", resp.StatusCode, redactTokenBody(raw))
	}
	var refreshed transport.Token
	decodeTokenInto(t, raw, &refreshed)
	if refreshed.AccessToken == "" || refreshed.AccessToken == issued.AccessToken {
		t.Fatalf("refresh did not mint a new access token: %s", redactToken(&refreshed))
	}
	if refreshed.RefreshToken != issued.RefreshToken {
		t.Errorf("refresh rotated the refresh token (len %d → len %d) although the fake keeps it", len(issued.RefreshToken), len(refreshed.RefreshToken))
	}

	// 6. Unknown refresh token ⇒ invalid_grant.
	bogus, bogusCT := tokenForm(map[string]string{"grant_type": "refresh_token", "refresh_token": "not-issued-here"})
	resp, raw = request(t, f.srv.Client(), http.MethodPost, f.srv.URL+"/token", bogusCT, bogus)
	if resp.StatusCode != http.StatusBadRequest || tokenJSONMap(t, raw)["error"] != "invalid_grant" {
		t.Fatalf("unknown refresh token: status %d body %s", resp.StatusCode, redactTokenBody(raw))
	}

	// 7. The refresh grant is also reachable through the real handler.
	h := transport.NewOAuthHandler(transport.OAuthConfig{
		ClientID:              "cid",
		RedirectURI:           "http://127.0.0.1:9/callback",
		TokenStore:            transport.NewMemoryTokenStore(),
		PKCEEnabled:           true,
		AuthServerMetadataURL: f.srv.URL + "/.well-known/oauth-authorization-server",
	})
	viaHandler, err := h.RefreshToken(context.Background(), issued.RefreshToken)
	if err != nil {
		t.Fatalf("handler.RefreshToken: %s", redactError(err))
	}
	if viaHandler.AccessToken == "" || viaHandler.TokenType != "Bearer" {
		t.Fatalf("handler.RefreshToken returned %s", redactToken(viaHandler))
	}
}

// ---------------------------------------------------------------------------
// protected resource
// ---------------------------------------------------------------------------

func TestResourceEndpointChallengeAndTokens(t *testing.T) {
	f := newFixture(t, authtest.WithScopes("mcp", "offline_access"))

	// No credential at all.
	resp, raw := request(t, f.srv.Client(), http.MethodGet, f.srv.URL+"/mcp", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	containsAll(t, "WWW-Authenticate", challenge,
		`Bearer`,
		`resource_metadata="`+f.srv.URL+`/.well-known/oauth-protected-resource"`,
		`scope="mcp offline_access"`,
	)
	_ = raw

	// An unknown bearer.
	req, _ := http.NewRequest(http.MethodGet, f.srv.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer not-issued-by-the-fake")
	unknownResp, err := f.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request with unknown bearer: %v", err)
	}
	unknownResp.Body.Close()
	if unknownResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown bearer status = %d, want 401", unknownResp.StatusCode)
	}

	// A registered token.
	f.s.NewAccessToken("live-token", time.Hour)
	req, _ = http.NewRequest(http.MethodPost, f.srv.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer live-token")
	req.Header.Set("Accept", "application/json, text/event-stream")
	liveResp, err := f.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request with live bearer: %v", err)
	}
	liveBody, _ := io.ReadAll(liveResp.Body)
	liveResp.Body.Close()
	if liveResp.StatusCode != http.StatusOK {
		t.Fatalf("live bearer status = %d, body %s", liveResp.StatusCode, liveBody)
	}
	var payload map[string]any
	decodeInto(t, liveBody, &payload)
	if payload["authenticated"] != true {
		t.Fatalf("resource body = %s", liveBody)
	}

	// Expiry.
	f.s.ExpireAllTokens()
	resp, _ = request(t, f.srv.Client(), http.MethodGet, f.srv.URL+"/mcp", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired token status = %d, want 401", resp.StatusCode)
	}

	// Observations, and the promise that the fake is not a secret sink.
	observed := f.s.ResourceRequests()
	if len(observed) != 4 {
		t.Fatalf("resource observations = %d, want 4: %s", len(observed), redactResourceRequests(observed))
	}
	if observed[0].HasBearer || observed[0].Authorization != "" {
		t.Errorf("unauthenticated request recorded as %s", redactResourceRequest(observed[0]))
	}
	if !observed[2].HasBearer || observed[2].Authorization != "Bearer" {
		t.Errorf("live request recorded as %s", redactResourceRequest(observed[2]))
	}
	if observed[2].Accept != "application/json, text/event-stream" {
		t.Errorf("Accept = %q", observed[2].Accept)
	}
	if observed[2].Method != http.MethodPost {
		t.Errorf("method = %q, want POST", observed[2].Method)
	}
	for i, obs := range observed {
		encoded, err := json.Marshal(obs)
		if err != nil {
			t.Fatalf("marshal observation %d: %v", i, err)
		}
		if strings.Contains(string(encoded), "live-token") || strings.Contains(string(encoded), "not-issued-by-the-fake") {
			// Never echo `encoded` here: this branch fires exactly when the
			// observation did carry the credential, so printing it would leak
			// the very value under test.
			t.Fatalf("observation %d leaked a credential", i)
		}
	}
}

// ---------------------------------------------------------------------------
// unconfigured issuer
// ---------------------------------------------------------------------------

func TestEndpointsWithoutIssuerFail(t *testing.T) {
	s := authtest.New()
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, _ := request(t, srv.Client(), http.MethodGet, srv.URL+"/.well-known/oauth-protected-resource", "", "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status before SetIssuer = %d, want 500", resp.StatusCode)
	}
	if s.Issuer() != "" {
		t.Fatalf("Issuer() = %q before SetIssuer", s.Issuer())
	}

	// A trailing slash on the issuer must not produce doubled slashes.
	s.SetIssuer(srv.URL + "/")
	if got, want := s.Issuer(), srv.URL; got != want {
		t.Fatalf("Issuer() = %q, want %q", got, want)
	}
	_, raw := request(t, srv.Client(), http.MethodGet, srv.URL+"/.well-known/oauth-authorization-server", "", "")
	var md transport.AuthServerMetadata
	decodeInto(t, raw, &md)
	if md.TokenEndpoint != srv.URL+"/token" {
		t.Fatalf("token_endpoint = %q, want %q", md.TokenEndpoint, srv.URL+"/token")
	}
}

// ---------------------------------------------------------------------------
// race
// ---------------------------------------------------------------------------

// TestConcurrentObserversAndEndpoints hammers every endpoint from one set of
// goroutines while another set calls every observer. Under -race this is the
// guard that the fake's own bookkeeping is sound.
func TestConcurrentObserversAndEndpoints(t *testing.T) {
	f := newFixture(t, authtest.WithScopes("mcp"))

	// Seed one refresh token and one access token so the concurrent work only
	// touches success paths (the one-shot scripts are exercised sequentially
	// elsewhere).
	f.s.SetAuthorizationCode("seed-code")
	body, contentType := tokenForm(map[string]string{"grant_type": "authorization_code", "code": "seed-code"})
	resp, raw := request(t, f.srv.Client(), http.MethodPost, f.srv.URL+"/token", contentType, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed token: status %d body %s", resp.StatusCode, redactTokenBody(raw))
	}
	var seeded transport.Token
	decodeTokenInto(t, raw, &seeded)

	const workers = 8
	const rounds = 20

	authorizeParams := url.Values{
		"response_type":         {"code"},
		"client_id":             {"concurrent"},
		"redirect_uri":          {"http://127.0.0.1:1/cb"},
		"state":                 {"st"},
		"code_challenge":        {transport.GenerateCodeChallenge(seeded.AccessToken)},
		"code_challenge_method": {"S256"},
	}.Encode()
	refreshBody, refreshCT := tokenForm(map[string]string{"grant_type": "refresh_token", "refresh_token": seeded.RefreshToken})

	var wg sync.WaitGroup
	client := f.srv.Client()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				doQuiet(t, client, http.MethodGet, f.srv.URL+"/.well-known/oauth-protected-resource/mcp", "", "")
				doQuiet(t, client, http.MethodGet, f.srv.URL+"/.well-known/oauth-authorization-server", "", "")
				doQuiet(t, client, http.MethodPost, f.srv.URL+"/register", "application/json", `{"client_name":"c","redirect_uris":["http://127.0.0.1:1/cb"]}`)
				doQuiet(t, f.noRedirectAdapter(), http.MethodGet, f.srv.URL+"/authorize?"+authorizeParams, "", "")
				doQuiet(t, client, http.MethodPost, f.srv.URL+"/token", refreshCT, refreshBody)
				doQuiet(t, client, http.MethodGet, f.srv.URL+"/mcp", "", "")
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				_ = f.s.Issuer()
				_ = f.s.LastAuthorize()
				_ = f.s.AuthorizeRequests()
				_ = f.s.LastRegister()
				_ = f.s.RegisterCount()
				_ = f.s.TokenRequests()
				_ = f.s.ResourceRequests()
				_ = f.s.WellKnownRequests()
				f.s.SetScopes("mcp")
				f.s.NewAccessToken("", time.Minute)
			}
		}()
	}
	wg.Wait()

	if got := f.s.RegisterCount(); got != workers*rounds {
		t.Fatalf("RegisterCount() = %d, want %d", got, workers*rounds)
	}
	if got := len(f.s.AuthorizeRequests()); got != workers*rounds {
		t.Fatalf("authorize observations = %d, want %d", got, workers*rounds)
	}
	if got := len(f.s.ResourceRequests()); got != workers*rounds {
		t.Fatalf("resource observations = %d, want %d", got, workers*rounds)
	}
	if got := len(f.s.WellKnownRequests()); got != 2*workers*rounds {
		t.Fatalf("well-known observations = %d, want %d", got, 2*workers*rounds)
	}
}

// noRedirectAdapter returns a redirect-stopping client sharing the fixture's
// transport, so the concurrency test does not spawn a connection pool per call.
func (f *fixture) noRedirectAdapter() *http.Client {
	return &http.Client{
		Transport: f.srv.Client().Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// doQuiet performs a request and discards the response, failing the test only
// on transport errors.
func doQuiet(t *testing.T, hc *http.Client, method, target, contentType, body string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		t.Errorf("build %s %s: %v", method, target, err)
		return
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := hc.Do(req)
	if err != nil {
		// The error of a redirect-stopping client embeds the Location header
		// (net/http client.go:653), i.e. the URL a `code` would travel on.
		t.Errorf("%s %s: %s", method, target, redactError(err))
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// small assertions
// ---------------------------------------------------------------------------

func equalStrings(got any, want []string) bool {
	var items []string
	switch typed := got.(type) {
	case []string:
		items = typed
	case []any:
		for _, item := range typed {
			s, ok := item.(string)
			if !ok {
				return false
			}
			items = append(items, s)
		}
	default:
		return false
	}
	if len(items) != len(want) {
		return false
	}
	for i, item := range items {
		if item != want[i] {
			return false
		}
	}
	return true
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
