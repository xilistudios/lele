// Package authtest provides an in-process, scriptable FAKE OAuth 2.1
// authorization server plus a tiny protected-resource endpoint, so that the
// real lele ↔ mcp-go OAuth flow can be exercised end to end under
// httptest.NewServer with no real network, no browser and no authorization
// server.
//
// It is test support, but it is a normal (non-`_test`) package so that any
// test package can import it. Two rules shape it:
//
//  1. NO `testing` IMPORT. The package only depends on the standard library
//     plus github.com/mark3labs/mcp-go/client/transport. Importing the real
//     transport types is deliberate: it is the compile-time proof that the
//     JSON this fake serves is the JSON mcp-go parses.
//
//  2. IT MUST NOT BECOME A SECRET SINK. The fake never records the value of
//     an Authorization credential; ResourceRequest only says which scheme was
//     presented ("Bearer") and whether a credential came with it
//     (HasBearer). Access tokens it issues are opaque strings the test already
//     sees on the wire.
//
// The rest of this comment is the reverse-engineered mcp-go v1.1.1 OAuth
// contract, cited by file:line inside
// $(go env GOMODCACHE)/github.com/mark3labs/mcp-go@v1.1.1. It lives here (and
// not in a new markdown file) on purpose.
//
// # (1) Which well-known paths are requested, in what order, and is a suffix appended?
//
// Discovery is driven by OAuthHandler.fetchServerMetadata
// (client/transport/oauth.go:608). The order is:
//
//	a. If OAuthConfig.ProtectedResourceMetadataURL is set (it is filled by
//	   HandleUnauthorizedResponse, oauth.go:393 → SetProtectedResourceMetadataURL,
//	   oauth.go:363, from the 401's `WWW-Authenticate: Bearer
//	   resource_metadata="..."` parameter), that EXACT URL is fetched, with NO
//	   path insertion and NO fallback if it fails (oauth.go:634-643, 666-669).
//	b. Otherwise the protected-resource metadata URL is CONSTRUCTED by
//	   buildWellKnownURL(baseURL, "oauth-protected-resource") (oauth.go:639,
//	   implementation oauth.go:762): the `/.well-known/<suffix>` segment is
//	   INSERTED BETWEEN the authority and the path (RFC 8414 §3 / RFC 9728
//	   path-insertion), so it IS a suffix of the path:
//	     baseURL http://h/mcp      → http://h/.well-known/oauth-protected-resource/mcp
//	     baseURL http://h          → http://h/.well-known/oauth-protected-resource
//	     baseURL http://h/mcp/     → http://h/.well-known/oauth-protected-resource/mcp
//	   The "baseURL" here is the MCP server URL the transport was built with,
//	   minus query/fragment (streamable_http.go:178-185); for SSE see the
//	   equivalent in sse.go.
//	c. If (b) returns a non-200, mcp-go falls back to the authorization-server
//	   discovery URLs derived from the SAME base URL (oauth.go:670-677), and if
//	   those fail, to getDefaultEndpoints(baseURL) (oauth.go:679, 962:
//	   `<origin>/authorize`, `<origin>/token`, `<origin>/register`, issuer =
//	   origin with the path dropped).
//	d. When the PRM document IS returned, authorization_servers[0] is taken as
//	   the issuer and authorizationServerMetadataURLs(issuer) (oauth.go:906,
//	   loop at oauth.go:739-749) is tried in order:
//	     issuer with no path → /.well-known/oauth-authorization-server,
//	                           /.well-known/openid-configuration
//	     issuer with path p  → /.well-known/oauth-authorization-server/p,
//	                           /.well-known/openid-configuration/p,
//	                           /p/.well-known/openid-configuration
//	   then getDefaultEndpoints(issuer).
//
// Note the asymmetry: for the PRM the well-known segment goes BEFORE the path;
// for the OIDC variant it may also go AFTER it. This fake serves the same
// document for any path containing the well-known segment, so every variant
// above resolves.
//
// # (2) The Go types the metadata is unmarshalled into, and their fields
//
// Protected resource metadata → transport.OAuthProtectedResource
// (oauth.go:510-514), decoded with json.NewDecoder at oauth.go:688:
//
//	AuthorizationServers []string `json:"authorization_servers"`
//	Resource             string   `json:"resource"`
//	ResourceName         string   `json:"resource_name,omitempty"`
//
// There is NO scopes_supported and NO bearer_methods_supported field in the
// Go type. Unknown JSON keys are ignored (no DisallowUnknownFields), so this
// fake can — and does — serve them for human readers without breaking mcp-go.
//
// Authorization server metadata → transport.AuthServerMetadata (oauth.go:146-169),
// decoded with a 1 MiB-capped decoder at oauth.go:848-852. The fields mcp-go
// itself consumes are: issuer, authorization_endpoint, token_endpoint,
// registration_endpoint, scopes_supported, response_types_supported,
// grant_types_supported, token_endpoint_auth_methods_supported,
// code_challenge_methods_supported. After decoding, every URL-bearing field
// must be absolute http/https with a host (validateAuthServerMetadataURLs,
// oauth.go:866-899) — a relative issuer fails discovery.
//
// # (3) The registration request and response
//
// transport.OAuthHandler.RegisterClient (oauth.go:987):
//
//	POST <registration_endpoint>
//	Content-Type: application/json
//	Accept: application/json
//	{"client_name":..., "redirect_uris":[<OAuthConfig.RedirectURI>],
//	 "token_endpoint_auth_method":"none"|"client_secret_post",
//	 "grant_types":["authorization_code","refresh_token"],
//	 "response_types":["code"], "scope":"<space-joined Scopes>",
//	 "client_uri":<optional>, "resource":<optional RFC 8707>}
//	                          (oauth.go:998-1019)
//
// It reads back ONLY two fields (oauth.go:1050-1063):
//
//	{"client_id":"...", "client_secret":"..."}   // client_secret optional
//
// The status must be exactly 200 or 201 (oauth.go:1045); anything else is
// turned into an error via extractOAuthError (oauth.go:345). Nothing else in
// the DCR response is inspected — no `redirect_uris` echo check, no
// registration_access_token, no client_id_issued_at.
//
// # (4) The token request form fields and the token-response fields
//
// Authorization code (oauth.go:1095-1125): POST
// application/x-www-form-urlencoded, Accept: application/json, with
// grant_type=authorization_code, code, client_id, redirect_uri, optional
// client_secret, code_verifier (ONLY when PKCEEnabled && codeVerifier != "",
// oauth.go:1105) and optional resource (RFC 8707).
//
// Refresh (oauth.go:260-282): grant_type=refresh_token, refresh_token,
// client_id, optional client_secret, optional resource.
//
// ANY 2xx is accepted (oauth.go:293, 1135). The body is first unmarshalled
// into OAuthError and rejected when a non-empty `error` is present — even on
// 2xx (oauth.go:1148, the "GitHub returns 200 with an error body" case) — and
// then into transport.Token (oauth.go:77-90, decoded at oauth.go:1153/311):
//
//	access_token  string    `json:"access_token"`
//	token_type    string    `json:"token_type"`
//	refresh_token string    `json:"refresh_token,omitempty"`
//	expires_in    int64     `json:"expires_in,omitempty"`  // SECONDS
//	scope         string    `json:"scope,omitempty"`
//	expires_at    time.Time `json:"expires_at,omitzero"`
//
// mcp-go computes ExpiresAt = now + expires_in (oauth.go:1159) and saves the
// whole Token into the TokenStore. Therefore: a successful token body must
// never carry a top-level "error", and token_type should always be "Bearer"
// (a client with an empty token_type would emit the malformed Authorization
// header " <token>", oauth.go:214-227).
//
// # (5) How redirect_uri is validated
//
// It is NOT validated by mcp-go. transport.ValidateRedirectURI
// (oauth_utils.go:40) exists — https, or http with localhost/127.0.0.1/::1 —
// but the OAuth flow never calls it: OAuthConfig.RedirectURI is sent verbatim
// to /authorize (oauth.go:1184) and to /token (oauth.go:1099). Enforcing the
// match is the authorization server's job, so this fake deliberately does not
// either (it only requires the parameter to be present so it has somewhere to
// redirect). A test that wants the mismatch to be caught must script it.
//
// # (6) transport.TokenStore and transport.Token
//
//	// oauth.go:63-74
//	type TokenStore interface {
//	    GetToken(ctx context.Context) (*Token, error)
//	    SaveToken(ctx context.Context, token *Token) error
//	}
//
// GetToken must return ErrNoToken (oauth.go:24) — not a nil, nil — when no
// token is stored; getValidToken treats ErrNoToken as "run the authorization
// flow" (oauth.go:231-251). transport.Token is the struct quoted in (4).
//
// # Things that surprise people (read this before writing an OAuth test)
//
//   - The PRM `resource` field MUST equal the MCP endpoint URL exactly —
//     path included — whenever the PRM document is fetched from a URL that was
//     ADVERTISED in a WWW-Authenticate header. fetchServerMetadata checks it
//     (oauth.go:705-718) and fails discovery with "declares resource ... does
//     not match base URL ..." otherwise. resourceIdentifiersEqual (oauth.go:790)
//     tolerates one trailing slash and case-differences in scheme/host, but the
//     path, query, fragment and userinfo are significant. Serving the bare
//     origin as `resource` is the single most likely way to break a test.
//   - An ADVERTISED PRM URL is origin-validated against the transport base URL
//     (oauth.go:418-449): same scheme and host, or it is silently ignored.
//   - An explicit PRM URL has no fallback path (oauth.go:666-669): a non-200
//     there is a hard discovery failure.
//   - Discovery GETs carry `Accept: application/json` and
//     `MCP-Protocol-Version: <latest>` (oauth.go:649-654, 831-836).
//   - Attaching an OAuth handler with no usable token makes SendRequest return
//     OAuthAuthorizationRequiredError BEFORE any HTTP call is made
//     (streamable_http.go:734-748) — with an empty ResourceMetadataURL, because
//     there is no response to scrape it from.
package authtest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
)

const (
	// DefaultResourcePath is the path of the protected resource endpoint
	// ("the MCP server") this fake also serves. The resource URL is
	// Issuer()+DefaultResourcePath and is what the protected-resource
	// metadata document declares as `resource`.
	DefaultResourcePath = "/mcp"
	// DefaultTokenTTL is the lifetime of the access tokens /token issues.
	DefaultTokenTTL = time.Hour
	// maxBodyBytes caps how much of a request body the fake will read.
	maxBodyBytes = 1 << 20
)

// AuthorizeRequest is one observation of a GET/POST /authorize request.
//
// The json tags are not a wire format: they exist so a test can marshal an
// observation to prove it leaks no credential (see the package comment).
type AuthorizeRequest struct {
	Method              string `json:"method"`
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	ResponseType        string `json:"response_type"`
	Scope               string `json:"scope"`
	State               string `json:"state"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	// Raw is a copy of every parameter the request carried.
	Raw url.Values `json:"raw"`
}

// RegisterRequest is one observation of a request to the dynamic client
// registration endpoint.
type RegisterRequest struct {
	Method      string `json:"method"`
	ContentType string `json:"content_type"`
	// ClientID is normally empty: mcp-go's DCR request body carries no
	// client_id (see finding (3)). It is recorded for clients that do.
	ClientID     string   `json:"client_id"`
	RedirectURIs []string `json:"redirect_uris"`
	// Raw is the decoded JSON body (nil-safe: never nil).
	Raw map[string]any `json:"raw"`
	// Form holds the url-encoded parameters (query + form body). It is empty
	// for the JSON body mcp-go actually sends.
	Form url.Values `json:"form"`
}

// TokenRequest is one observation of a request to the token endpoint.
type TokenRequest struct {
	GrantType    string `json:"grant_type"`
	Code         string `json:"code"`
	CodeVerifier string `json:"code_verifier"`
	RedirectURI  string `json:"redirect_uri"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	Resource     string `json:"resource"`
	// Raw is a copy of every parameter the request carried.
	Raw url.Values `json:"raw"`
}

// ResourceRequest is one observation of a request to the protected resource
// endpoint. It deliberately CANNOT hold a credential: Authorization carries
// the scheme only ("Bearer"), never the token, and HasBearer says whether a
// credential accompanied it (see the package comment). Accept is recorded so a
// test can assert the transport asked for JSON or SSE.
type ResourceRequest struct {
	Method        string `json:"method"`
	Authorization string `json:"authorization"`
	Accept        string `json:"accept"`
	HasBearer     bool   `json:"has_bearer"`
}

// Server is an in-process OAuth 2.1 authorization server plus a tiny protected
// resource endpoint, scriptable from tests. Everything is stdlib + the real
// mcp-go transport types; no testing import, so any test package can use it.
//
// Every field is guarded by mu; every observer returns a copy.
type Server struct {
	mu sync.Mutex

	issuer       string
	resourcePath string
	scopes       []string
	tokenTTL     time.Duration

	// scripting
	clientSecret    string
	scriptedCode    string
	scriptedAuthErr string
	tokenErrCode    string
	tokenErrStatus  int
	regFailErr      string

	// state
	clientSeq     int
	codes         map[string]issuedCode
	accessTokens  map[string]time.Time
	refreshTokens map[string]bool

	// observations
	authorizeReqs []AuthorizeRequest
	registerReqs  []RegisterRequest
	tokenReqs     []TokenRequest
	resourceReqs  []ResourceRequest
	wellKnownReqs []string
}

// issuedCode is what /authorize remembers about a code it handed out.
type issuedCode struct {
	challenge string // "" when the authorize request carried no PKCE
	scope     string
}

// Option configures a Server at construction time.
type Option func(*Server)

// WithScopes sets the scopes the fake advertises and requests by default.
func WithScopes(scopes ...string) Option {
	return func(s *Server) { s.scopes = append([]string(nil), scopes...) }
}

// WithResourcePath sets the path of the protected resource endpoint, e.g.
// "/mcp". It must start with "/".
func WithResourcePath(path string) Option {
	return func(s *Server) { s.resourcePath = path }
}

// WithTokenTTL sets the lifetime of issued access tokens. ttl <= 0 means the
// tokens never expire until ExpireAllTokens is called.
func WithTokenTTL(ttl time.Duration) Option {
	return func(s *Server) { s.tokenTTL = ttl }
}

// New returns a ready-to-serve Server. Pass s.Handler() to
// httptest.NewServer and then s.SetIssuer(srv.URL) once the listener is up:
//
//	s := authtest.New()
//	srv := httptest.NewServer(s.Handler())
//	defer srv.Close()
//	s.SetIssuer(srv.URL)
func New(opts ...Option) *Server {
	s := &Server{
		resourcePath:  DefaultResourcePath,
		scopes:        []string{"mcp"},
		tokenTTL:      DefaultTokenTTL,
		codes:         make(map[string]issuedCode),
		accessTokens:  make(map[string]time.Time),
		refreshTokens: make(map[string]bool),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Handler returns the http.Handler to hand to httptest.NewServer. It is safe
// to call before SetIssuer: requests that need the issuer answer 500 until it
// is set.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

// SetIssuer records the public base URL of this fake (normally srv.URL). One
// trailing slash is trimmed so endpoint URLs never double up.
func (s *Server) SetIssuer(issuer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issuer = strings.TrimSuffix(issuer, "/")
}

// Issuer returns the configured public base URL, or "" if SetIssuer was never
// called.
func (s *Server) Issuer() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issuer
}

// SetScopes replaces the scopes this fake advertises and by default requests.
func (s *Server) SetScopes(scopes ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopes = append([]string(nil), scopes...)
}

// SetResourcePath sets the path of the protected resource endpoint.
func (s *Server) SetResourcePath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resourcePath = path
}

// SetAuthorizationCode makes the NEXT /authorize redirect carry this code
// instead of a generated one. The code is accepted by /token even if
// /authorize is skipped, but /authorize still re-binds it to the PKCE
// challenge it receives.
func (s *Server) SetAuthorizationCode(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scriptedCode = code
	if _, ok := s.codes[code]; !ok {
		s.codes[code] = issuedCode{}
	}
}

// SetAuthorizeError makes the NEXT /authorize redirect with ?error=<code>
// (and the echoed state) instead of a code.
func (s *Server) SetAuthorizeError(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scriptedAuthErr = code
}

// SetTokenError makes the NEXT /token request answer with the given OAuth
// error code and HTTP status (e.g. SetTokenError("invalid_grant", 400)). A
// non-positive status defaults to 400.
func (s *Server) SetTokenError(code string, status int) {
	if status <= 0 {
		status = http.StatusBadRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenErrCode = code
	s.tokenErrStatus = status
}

// FailRegistrationOnce makes the NEXT dynamic client registration answer with
// the given error code and HTTP 400.
func (s *Server) FailRegistrationOnce(err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.regFailErr = err
}

// SetClientSecretSecret makes the fake behave like a confidential client
// registrar: every registration response carries `client_secret` with this
// value. Passing "" restores the public-client behaviour.
func (s *Server) SetClientSecretSecret(secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clientSecret = secret
}

// NewAccessToken registers an access token the resource endpoint accepts.
// ttl <= 0 registers a token that never expires.
func (s *Server) NewAccessToken(tok string, ttl time.Duration) {
	if tok == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accessTokens[tok] = expiryOf(ttl)
}

// ExpireAllTokens makes every token the fake knows about expired, so the
// resource endpoint answers 401 again.
func (s *Server) ExpireAllTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	past := time.Now().Add(-time.Minute)
	for tok := range s.accessTokens {
		s.accessTokens[tok] = past
	}
}

// LastAuthorize returns a copy of the most recent /authorize observation, or
// the zero value when there has been none.
func (s *Server) LastAuthorize() AuthorizeRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.authorizeReqs) == 0 {
		return AuthorizeRequest{}
	}
	return copyAuthorize(s.authorizeReqs[len(s.authorizeReqs)-1])
}

// AuthorizeRequests returns copies of every /authorize observation, in order.
func (s *Server) AuthorizeRequests() []AuthorizeRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuthorizeRequest, 0, len(s.authorizeReqs))
	for _, r := range s.authorizeReqs {
		out = append(out, copyAuthorize(r))
	}
	return out
}

// LastRegister returns a copy of the most recent registration observation, or
// the zero value when there has been none.
func (s *Server) LastRegister() RegisterRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.registerReqs) == 0 {
		return RegisterRequest{}
	}
	return copyRegister(s.registerReqs[len(s.registerReqs)-1])
}

// RegisterCount returns how many requests have hit the registration endpoint,
// successful or not.
func (s *Server) RegisterCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.registerReqs)
}

// TokenRequests returns copies of every /token observation, in order.
func (s *Server) TokenRequests() []TokenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TokenRequest, 0, len(s.tokenReqs))
	for _, r := range s.tokenReqs {
		out = append(out, copyToken(r))
	}
	return out
}

// ResourceRequests returns copies of every protected-resource observation, in
// order. It never contains a credential (see the package comment).
func (s *Server) ResourceRequests() []ResourceRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ResourceRequest, 0, len(s.resourceReqs))
	return append(out, s.resourceReqs...)
}

// WellKnownRequests returns the paths of every discovery document served so
// far, in order. It is the observable proof of finding (1): which well-known
// URLs the client actually asked for.
func (s *Server) WellKnownRequests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.wellKnownReqs))
	return append(out, s.wellKnownReqs...)
}

// ---------------------------------------------------------------------------
// routing
// ---------------------------------------------------------------------------

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case wellKnownPath(path, "oauth-protected-resource"):
		s.handleProtectedResourceMetadata(w, r)
	case wellKnownPath(path, "oauth-authorization-server"), wellKnownPath(path, "openid-configuration"):
		s.handleAuthServerMetadata(w, r)
	case path == "/register":
		s.handleRegister(w, r)
	case path == "/authorize":
		s.handleAuthorize(w, r)
	case path == "/token":
		s.handleToken(w, r)
	case path == s.ResourcePath():
		s.handleResource(w, r)
	default:
		http.NotFound(w, r)
	}
}

// wellKnownPath reports whether path addresses a well-known document with the
// given suffix. It uses a containment test so that BOTH insertion forms work:
// the RFC 8414/RFC 9728 form "/.well-known/<suffix>/<issuer-path>" (which also
// satisfies a plain HasPrefix) and the OIDC form "/<issuer-path>/.well-known/
// <suffix>". Serving strictly more paths than mcp-go asks for is harmless and
// keeps the fake usable by other clients.
func wellKnownPath(path, suffix string) bool {
	return strings.Contains(path, "/.well-known/"+suffix)
}

// ResourcePath returns the configured protected-resource path.
func (s *Server) ResourcePath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resourcePath
}

// resourceURL returns Issuer()+resourcePath.
func (s *Server) resourceURL() string {
	return s.issuer + s.resourcePath
}

// ---------------------------------------------------------------------------
// discovery documents
// ---------------------------------------------------------------------------

func (s *Server) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "discovery is GET-only")
		return
	}
	s.mu.Lock()
	s.wellKnownReqs = append(s.wellKnownReqs, r.URL.Path)
	issuer := s.issuer
	scopes := append([]string(nil), s.scopes...)
	s.mu.Unlock()

	if issuer == "" {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "authtest: SetIssuer was not called")
		return
	}
	// The required fields are produced BY the real transport type, so the JSON
	// keys are whatever mcp-go itself declares. Only the keys it has no field
	// for (and therefore ignores) are added by hand.
	//
	// `resource` is the protected resource identifier. It must be the MCP
	// endpoint URL (path included) or mcp-go rejects the document — see the
	// package comment, finding (2)/surprises.
	doc := jsonObject(transport.OAuthProtectedResource{
		AuthorizationServers: []string{issuer},
		Resource:             s.resourceURL(),
	})
	doc["scopes_supported"] = scopes
	doc["bearer_methods_supported"] = []string{"header"}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleAuthServerMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "discovery is GET-only")
		return
	}
	s.mu.Lock()
	s.wellKnownReqs = append(s.wellKnownReqs, r.URL.Path)
	issuer := s.issuer
	scopes := append([]string(nil), s.scopes...)
	s.mu.Unlock()

	if issuer == "" {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "authtest: SetIssuer was not called")
		return
	}
	writeJSON(w, http.StatusOK, jsonObject(transport.AuthServerMetadata{
		Issuer:                            issuer,
		AuthorizationEndpoint:             issuer + "/authorize",
		TokenEndpoint:                     issuer + "/token",
		RegistrationEndpoint:              issuer + "/register",
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_post", "client_secret_basic"},
		ScopesSupported:                   scopes,
	}))
}

// ---------------------------------------------------------------------------
// dynamic client registration
// ---------------------------------------------------------------------------

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "register is GET/POST")
		return
	}
	form, body := decodeRequest(r)
	raw := decodeJSONObject(r, body)

	s.mu.Lock()
	req := RegisterRequest{
		Method:      r.Method,
		ContentType: r.Header.Get("Content-Type"),
		ClientID:    firstNonEmpty(jsonString(raw, "client_id"), form.Get("client_id")),
		Raw:         copyAnyMap(raw),
		Form:        copyValues(form),
	}
	req.RedirectURIs = jsonStringSlice(raw, "redirect_uris")
	if len(req.RedirectURIs) == 0 {
		req.RedirectURIs = append([]string(nil), form["redirect_uris"]...)
	}
	s.registerReqs = append(s.registerReqs, req)

	if s.regFailErr != "" {
		code := s.regFailErr
		s.regFailErr = ""
		s.mu.Unlock()
		writeOAuthError(w, http.StatusBadRequest, code, "scripted registration failure")
		return
	}

	s.clientSeq++
	clientID := fmt.Sprintf("authtest-client-%d", s.clientSeq)
	authMethod := firstNonEmpty(jsonString(raw, "token_endpoint_auth_method"), "none")
	redirectURIs := req.RedirectURIs
	secret := s.clientSecret
	s.mu.Unlock()

	resp := map[string]any{
		"client_id":                  clientID,
		"redirect_uris":              redirectURIs,
		"token_endpoint_auth_method": authMethod,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	}
	if secret != "" {
		resp["client_secret"] = secret
	}
	// 201 Created is what RFC 7591 asks for and what mcp-go accepts
	// (oauth.go:1045 accepts 200 and 201 alike).
	writeJSON(w, http.StatusCreated, resp)
}

// ---------------------------------------------------------------------------
// authorization endpoint
// ---------------------------------------------------------------------------

func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "authorize is GET/POST")
		return
	}
	params, _ := decodeRequest(r)
	obs := AuthorizeRequest{
		Method:              r.Method,
		ClientID:            params.Get("client_id"),
		RedirectURI:         params.Get("redirect_uri"),
		ResponseType:        params.Get("response_type"),
		Scope:               params.Get("scope"),
		State:               params.Get("state"),
		CodeChallenge:       params.Get("code_challenge"),
		CodeChallengeMethod: params.Get("code_challenge_method"),
		Raw:                 copyValues(params),
	}

	s.mu.Lock()
	s.authorizeReqs = append(s.authorizeReqs, obs)

	redirectURI := obs.RedirectURI
	state := obs.State
	scriptedErr := s.scriptedAuthErr
	scriptedCode := s.scriptedCode

	// PKCE is mandatory in this fake: a client that omits code_challenge, or
	// announces a method other than S256, gets an error redirect instead of a
	// code (finding (4) — mcp-go only sends these when PKCEEnabled).
	errCode := ""
	code := ""
	switch {
	case scriptedErr != "":
		errCode = scriptedErr
		s.scriptedAuthErr = ""
	case obs.CodeChallenge == "":
		errCode = "invalid_request"
	case obs.CodeChallengeMethod != "" && obs.CodeChallengeMethod != "S256":
		errCode = "invalid_request"
	default:
		if scriptedCode != "" {
			code = scriptedCode
			s.scriptedCode = ""
		} else {
			code = "authtest-code-" + randomString()
		}
		s.codes[code] = issuedCode{challenge: obs.CodeChallenge, scope: obs.Scope}
	}
	s.mu.Unlock()

	if redirectURI == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri is required")
		return
	}
	q := url.Values{}
	if errCode != "" {
		q.Set("error", errCode)
		if obs.State != "" {
			q.Set("state", state)
		}
	} else {
		q.Set("code", code)
		if obs.State != "" {
			q.Set("state", state)
		}
	}
	// http.Redirect writes the 302 but does not follow it: the client under
	// test is the one that must stop at the redirect.
	http.Redirect(w, r, redirectURI+separator(redirectURI)+q.Encode(), http.StatusFound)
}

// ---------------------------------------------------------------------------
// token endpoint
// ---------------------------------------------------------------------------

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "token is GET/POST")
		return
	}
	params, _ := decodeRequest(r)
	obs := TokenRequest{
		GrantType:    params.Get("grant_type"),
		Code:         params.Get("code"),
		CodeVerifier: params.Get("code_verifier"),
		RedirectURI:  params.Get("redirect_uri"),
		ClientID:     params.Get("client_id"),
		ClientSecret: params.Get("client_secret"),
		RefreshToken: params.Get("refresh_token"),
		Resource:     params.Get("resource"),
		Raw:          copyValues(params),
	}

	s.mu.Lock()
	s.tokenReqs = append(s.tokenReqs, obs)

	if s.tokenErrCode != "" {
		code, status := s.tokenErrCode, s.tokenErrStatus
		s.tokenErrCode, s.tokenErrStatus = "", 0
		s.mu.Unlock()
		writeOAuthError(w, status, code, "scripted token failure")
		return
	}

	scope := strings.Join(s.scopes, " ")
	switch obs.GrantType {
	case "authorization_code":
		issued, ok := s.codes[obs.Code]
		if !ok {
			s.mu.Unlock()
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "unknown or already-used authorization code")
			return
		}
		if issued.challenge != "" {
			// The falsifiable part: the code is only exchangeable with the
			// verifier that hashes to the challenge /authorize recorded.
			if obs.CodeVerifier == "" || s256(obs.CodeVerifier) != issued.challenge {
				s.mu.Unlock()
				writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
				return
			}
		}
		// Consume only on success, so a wrong-verifier probe can be followed
		// by the correct one with the same code.
		delete(s.codes, obs.Code)
		if issued.scope != "" {
			scope = issued.scope
		}
	case "refresh_token":
		if obs.RefreshToken == "" || !s.refreshTokens[obs.RefreshToken] {
			s.mu.Unlock()
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "unknown refresh token")
			return
		}
	default:
		s.mu.Unlock()
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
		return
	}

	access := "authtest-access-" + randomString()
	// The refresh grant does not rotate the refresh token: mcp-go keeps the
	// old one when the response omits it (oauth.go:322-324); not changing it
	// here keeps a test's second refresh working.
	refresh := obs.RefreshToken
	if obs.GrantType != "refresh_token" {
		refresh = "authtest-refresh-" + randomString()
		s.refreshTokens[refresh] = true
	}
	expiresIn := int64(0)
	if s.tokenTTL > 0 {
		expiresIn = int64(s.tokenTTL / time.Second)
	}
	s.accessTokens[access] = expiryOf(s.tokenTTL)
	s.mu.Unlock()

	// The response is generated from the real transport.Token type, so its
	// JSON tags (access_token / token_type / refresh_token / expires_in /
	// scope) are mcp-go's own.
	body := jsonObject(transport.Token{
		AccessToken:  access,
		TokenType:    "Bearer",
		RefreshToken: refresh,
		ExpiresIn:    expiresIn,
		Scope:        scope,
	})
	writeJSON(w, http.StatusOK, body)
}

// ---------------------------------------------------------------------------
// protected resource
// ---------------------------------------------------------------------------

func (s *Server) handleResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "resource is GET/POST")
		return
	}
	scheme, credential, isBearer := parseAuthorization(r.Header.Get("Authorization"))

	s.mu.Lock()
	s.resourceReqs = append(s.resourceReqs, ResourceRequest{
		Method:        r.Method,
		Authorization: scheme,
		Accept:        r.Header.Get("Accept"),
		HasBearer:     isBearer && credential != "",
	})
	issuer := s.issuer
	scopes := append([]string(nil), s.scopes...)
	valid := false
	if isBearer && credential != "" {
		if exp, known := s.accessTokens[credential]; known && (exp.IsZero() || time.Now().Before(exp)) {
			valid = true
		}
	}
	s.mu.Unlock()

	if !valid {
		s.writeUnauthorized(w, issuer, scopes)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"resource":      s.resourceURL(),
	})
}

// writeUnauthorized emits the RFC 9728 §5.1 challenge mcp-go scrapes for the
// advertised protected-resource metadata URL (streamable_http.go:603,
// oauth.go:393-406).
func (s *Server) writeUnauthorized(w http.ResponseWriter, issuer string, scopes []string) {
	challenge := "Bearer"
	if issuer != "" {
		challenge += fmt.Sprintf(` resource_metadata="%s/.well-known/oauth-protected-resource"`, issuer)
	}
	if len(scopes) > 0 {
		challenge += fmt.Sprintf(`, scope="%s"`, strings.Join(scopes, " "))
	}
	w.Header().Set("WWW-Authenticate", challenge)
	writeJSON(w, http.StatusUnauthorized, map[string]any{
		"error":             "invalid_token",
		"error_description": "authtest: a live bearer token is required",
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// expiryOf turns a ttl into the expiry the resource endpoint checks. A
// non-positive ttl means "never expires" (the zero time).
func expiryOf(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}

// s256 is the RFC 7636 §4.2 S256 transformation, computed here rather than
// borrowed from the library under test: the fake must not silently agree with
// a hypothetical bug in the client's derivation.
func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomString() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func separator(s string) string {
	if strings.Contains(s, "?") {
		return "&"
	}
	return "?"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// parseAuthorization splits an Authorization header into scheme, credential
// and "is it a bearer credential". The credential is returned to the caller
// but is never recorded.
func parseAuthorization(header string) (scheme, credential string, isBearer bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", "", false
	}
	parts := strings.SplitN(header, " ", 2)
	scheme = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		credential = strings.TrimSpace(parts[1])
	}
	return scheme, credential, strings.EqualFold(scheme, "Bearer")
}

// decodeRequest returns the url-encoded parameters of a request (query string
// plus, when the body is url-encoded, the body) and the raw body bytes. A JSON
// body is flattened into the values too, so a client that posts JSON instead of
// a form still works.
func decodeRequest(r *http.Request) (url.Values, []byte) {
	vals := url.Values{}
	for k, v := range r.URL.Query() {
		vals[k] = append([]string(nil), v...)
	}
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	}
	if len(body) == 0 {
		return vals, body
	}
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType == "application/json" {
		var m map[string]any
		if err := json.Unmarshal(body, &m); err == nil {
			flattenJSON(vals, m)
		}
		return vals, body
	}
	if form, err := url.ParseQuery(string(body)); err == nil {
		for k, v := range form {
			vals[k] = append(vals[k], v...)
		}
	}
	return vals, body
}

// decodeJSONObject returns the JSON body as a map, or an empty map when the
// body is not a JSON object.
func decodeJSONObject(r *http.Request, body []byte) map[string]any {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" || len(body) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// flattenJSON copies a decoded JSON object into url.Values, keeping the string
// and repeated-string shapes a form would have had.
func flattenJSON(vals url.Values, m map[string]any) {
	for k, v := range m {
		switch t := v.(type) {
		case string:
			vals.Set(k, t)
		case bool:
			vals.Set(k, strconv.FormatBool(t))
		case float64:
			vals.Set(k, strconv.FormatFloat(t, 'f', -1, 64))
		case []any:
			for _, e := range t {
				if s, ok := e.(string); ok {
					vals.Add(k, s)
				}
			}
		}
	}
}

func jsonString(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func jsonStringSlice(m map[string]any, key string) []string {
	items, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// copyValues returns a deep copy of url.Values. The standard library offers
// no such helper (url.Values.Clone does not exist), and the observers must
// hand out copies rather than the live recording.
func copyValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vals := range v {
		out[k] = append([]string(nil), vals...)
	}
	return out
}

func copyAnyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyAuthorize(r AuthorizeRequest) AuthorizeRequest {
	r.Raw = copyValues(r.Raw)
	return r
}

func copyRegister(r RegisterRequest) RegisterRequest {
	r.Raw = copyAnyMap(r.Raw)
	r.Form = copyValues(r.Form)
	r.RedirectURIs = append([]string(nil), r.RedirectURIs...)
	return r
}

func copyToken(r TokenRequest) TokenRequest {
	r.Raw = copyValues(r.Raw)
	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]string{
		"error":             code,
		"error_description": description,
	})
}

// jsonObject marshals v and returns its JSON object form, so a document can be
// built from a real transport type (keeping its json tags authoritative) and
// then extended with the keys that type has no field for.
func jsonObject(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}
