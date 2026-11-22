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
	DefaultResourcePath = "/mcp"
	DefaultTokenTTL     = time.Hour
	maxBodyBytes        = 1 << 20
)

type AuthorizeRequest struct {
	Method              string     `json:"method"`
	ClientID            string     `json:"client_id"`
	RedirectURI         string     `json:"redirect_uri"`
	ResponseType        string     `json:"response_type"`
	Scope               string     `json:"scope"`
	State               string     `json:"state"`
	CodeChallenge       string     `json:"code_challenge"`
	CodeChallengeMethod string     `json:"code_challenge_method"`
	Raw                 url.Values `json:"raw"`
}

type RegisterRequest struct {
	Method       string         `json:"method"`
	ContentType  string         `json:"content_type"`
	ClientID     string         `json:"client_id"`
	RedirectURIs []string       `json:"redirect_uris"`
	Raw          map[string]any `json:"raw"`
	Form         url.Values     `json:"form"`
}

type TokenRequest struct {
	GrantType    string     `json:"grant_type"`
	Code         string     `json:"code"`
	CodeVerifier string     `json:"code_verifier"`
	RedirectURI  string     `json:"redirect_uri"`
	ClientID     string     `json:"client_id"`
	ClientSecret string     `json:"client_secret"`
	RefreshToken string     `json:"refresh_token"`
	Resource     string     `json:"resource"`
	Raw          url.Values `json:"raw"`
}

type ResourceRequest struct {
	Method        string `json:"method"`
	Authorization string `json:"authorization"`
	Accept        string `json:"accept"`
	HasBearer     bool   `json:"has_bearer"`
}

type Server struct {
	mu sync.Mutex

	issuer       string
	resourcePath string
	scopes       []string
	tokenTTL     time.Duration

	clientSecret    string
	scriptedCode    string
	scriptedAuthErr string
	tokenErrCode    string
	tokenErrStatus  int
	regFailErr      string

	clientSeq     int
	codes         map[string]issuedCode
	accessTokens  map[string]time.Time
	refreshTokens map[string]bool

	authorizeReqs []AuthorizeRequest
	registerReqs  []RegisterRequest
	tokenReqs     []TokenRequest
	resourceReqs  []ResourceRequest
	wellKnownReqs []string
}

type issuedCode struct {
	challenge string
	scope     string
}

type Option func(*Server)

func WithScopes(scopes ...string) Option {
	return func(s *Server) { s.scopes = append([]string(nil), scopes...) }
}

func WithResourcePath(path string) Option {
	return func(s *Server) { s.resourcePath = path }
}

func WithTokenTTL(ttl time.Duration) Option {
	return func(s *Server) { s.tokenTTL = ttl }
}

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

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

func (s *Server) SetIssuer(issuer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issuer = strings.TrimSuffix(issuer, "/")
}

func (s *Server) Issuer() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issuer
}

func (s *Server) SetScopes(scopes ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopes = append([]string(nil), scopes...)
}

func (s *Server) SetResourcePath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resourcePath = path
}

func (s *Server) SetAuthorizationCode(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scriptedCode = code
	if _, ok := s.codes[code]; !ok {
		s.codes[code] = issuedCode{}
	}
}

func (s *Server) SetAuthorizeError(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scriptedAuthErr = code
}

func (s *Server) SetTokenError(code string, status int) {
	if status <= 0 {
		status = http.StatusBadRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenErrCode = code
	s.tokenErrStatus = status
}

func (s *Server) FailRegistrationOnce(err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.regFailErr = err
}

func (s *Server) SetClientSecretSecret(secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clientSecret = secret
}

func (s *Server) NewAccessToken(tok string, ttl time.Duration) {
	if tok == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accessTokens[tok] = expiryOf(ttl)
}

func (s *Server) ExpireAllTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	past := time.Now().Add(-time.Minute)
	for tok := range s.accessTokens {
		s.accessTokens[tok] = past
	}
}

func (s *Server) LastAuthorize() AuthorizeRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.authorizeReqs) == 0 {
		return AuthorizeRequest{}
	}
	return copyAuthorize(s.authorizeReqs[len(s.authorizeReqs)-1])
}

func (s *Server) AuthorizeRequests() []AuthorizeRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuthorizeRequest, 0, len(s.authorizeReqs))
	for _, r := range s.authorizeReqs {
		out = append(out, copyAuthorize(r))
	}
	return out
}

func (s *Server) LastRegister() RegisterRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.registerReqs) == 0 {
		return RegisterRequest{}
	}
	return copyRegister(s.registerReqs[len(s.registerReqs)-1])
}

func (s *Server) RegisterCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.registerReqs)
}

func (s *Server) TokenRequests() []TokenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TokenRequest, 0, len(s.tokenReqs))
	for _, r := range s.tokenReqs {
		out = append(out, copyToken(r))
	}
	return out
}

func (s *Server) ResourceRequests() []ResourceRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ResourceRequest, 0, len(s.resourceReqs))
	return append(out, s.resourceReqs...)
}

func (s *Server) WellKnownRequests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.wellKnownReqs))
	return append(out, s.wellKnownReqs...)
}

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

func wellKnownPath(path, suffix string) bool {
	return strings.Contains(path, "/.well-known/"+suffix)
}

func (s *Server) ResourcePath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resourcePath
}

func (s *Server) resourceURL() string {
	return s.issuer + s.resourcePath
}

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
	writeJSON(w, http.StatusCreated, resp)
}

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
	http.Redirect(w, r, redirectURI+separator(redirectURI)+q.Encode(), http.StatusFound)
}

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
			if obs.CodeVerifier == "" || s256(obs.CodeVerifier) != issued.challenge {
				s.mu.Unlock()
				writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
				return
			}
		}
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

	body := jsonObject(transport.Token{
		AccessToken:  access,
		TokenType:    "Bearer",
		RefreshToken: refresh,
		ExpiresIn:    expiresIn,
		Scope:        scope,
	})
	writeJSON(w, http.StatusOK, body)
}

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

func expiryOf(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}

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
