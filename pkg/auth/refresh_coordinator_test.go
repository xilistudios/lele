package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func setupRefreshTestStore(t *testing.T) {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	s := openSQLiteStore(t)
	useTestAuthRepo(t, s.Auth())
}

type refreshTestServer struct {
	*httptest.Server
	hits   atomic.Int32
	status int
}

func newRefreshTestServer(t *testing.T, status int) *refreshTestServer {
	t.Helper()
	rs := &refreshTestServer{status: status}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := rs.hits.Add(1)
		if rs.status != http.StatusOK {
			w.WriteHeader(rs.status)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"fresh-%d","refresh_token":"rot-%d","expires_in":3600}`, n, n)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func withServer(cfg OAuthProviderConfig, rs *refreshTestServer) OAuthProviderConfig {
	cfg.HTTPClient = rs.Client()
	cfg.TokenURL = rs.URL
	return cfg
}

func expiredCred(provider string, refreshToken string) *AuthCredential {
	return &AuthCredential{
		AccessToken:  "old-access",
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(-time.Hour),
		Provider:     provider,
		AuthMethod:   "oauth",
	}
}

func TestEnsureFreshCredentialSingleFlight(t *testing.T) {
	setupRefreshTestStore(t)
	rs := newRefreshTestServer(t, http.StatusOK)
	cfg := withServer(OpenAIOAuthConfig(), rs)

	if err := SetCredential("openai", expiredCred("openai", "rot-0")); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	const callers = 16
	var wg sync.WaitGroup
	tokens := make([]string, callers)
	errs := make([]error, callers)
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			cred, err := EnsureFreshCredential("openai", cfg, RefreshPolicy{})
			errs[i] = err
			if cred != nil {
				tokens[i] = cred.AccessToken
			}
		}(i)
	}
	wg.Wait()

	if got := rs.hits.Load(); got != 1 {
		t.Errorf("token endpoint hits = %d, want exactly 1", got)
	}
	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d error: %v", i, err)
		}
	}
	for i, tok := range tokens {
		if tok != "fresh-1" {
			t.Errorf("caller %d token = %q, want %q", i, tok, "fresh-1")
		}
	}

	stored, err := GetCredential("openai")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if stored == nil {
		t.Fatal("GetCredential() returned nil")
	}
	if stored.AccessToken != "fresh-1" {
		t.Errorf("stored AccessToken = %q, want %q", stored.AccessToken, "fresh-1")
	}
	if stored.RefreshToken != "rot-1" {
		t.Errorf("stored RefreshToken = %q, want %q", stored.RefreshToken, "rot-1")
	}
}

func TestEnsureFreshCredentialSecondCallUsesStoredToken(t *testing.T) {
	setupRefreshTestStore(t)
	rs := newRefreshTestServer(t, http.StatusOK)
	cfg := withServer(OpenAIOAuthConfig(), rs)

	if err := SetCredential("openai", expiredCred("openai", "rot-0")); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	if _, err := EnsureFreshCredential("openai", cfg, RefreshPolicy{}); err != nil {
		t.Fatalf("first EnsureFreshCredential() error: %v", err)
	}
	if got := rs.hits.Load(); got != 1 {
		t.Fatalf("token endpoint hits after first call = %d, want 1", got)
	}

	cred, err := EnsureFreshCredential("openai", cfg, RefreshPolicy{})
	if err != nil {
		t.Fatalf("second EnsureFreshCredential() error: %v", err)
	}
	if cred == nil || cred.AccessToken != "fresh-1" {
		t.Errorf("second call credential = %+v, want AccessToken %q", cred, "fresh-1")
	}
	if got := rs.hits.Load(); got != 1 {
		t.Errorf("token endpoint hits after second call = %d, want 0 more (total 1)", got)
	}
}

func TestEnsureFreshCredentialReturnsNilWhenNoCredential(t *testing.T) {
	setupRefreshTestStore(t)
	rs := newRefreshTestServer(t, http.StatusOK)
	cfg := withServer(OpenAIOAuthConfig(), rs)

	cred, err := EnsureFreshCredential("openai", cfg, RefreshPolicy{})
	if err != nil {
		t.Fatalf("EnsureFreshCredential() error: %v", err)
	}
	if cred != nil {
		t.Errorf("EnsureFreshCredential() = %+v, want nil", cred)
	}
	if got := rs.hits.Load(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0", got)
	}
}

func TestEnsureFreshCredentialWithoutRefreshTokenReturnsStored(t *testing.T) {
	setupRefreshTestStore(t)
	rs := newRefreshTestServer(t, http.StatusOK)
	cfg := withServer(OpenAIOAuthConfig(), rs)

	original := expiredCred("openai", "")
	if err := SetCredential("openai", original); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	cred, err := EnsureFreshCredential("openai", cfg, RefreshPolicy{})
	if err != nil {
		t.Fatalf("EnsureFreshCredential() error: %v", err)
	}
	if cred == nil {
		t.Fatal("EnsureFreshCredential() returned nil")
	}
	if cred.AccessToken != "old-access" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "old-access")
	}
	if got := rs.hits.Load(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0", got)
	}
}

func TestEnsureFreshCredentialRefreshErrorKeepsStoreIntact(t *testing.T) {
	setupRefreshTestStore(t)
	rs := newRefreshTestServer(t, http.StatusBadRequest)
	cfg := withServer(OpenAIOAuthConfig(), rs)

	if err := SetCredential("openai", expiredCred("openai", "rot-0")); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	cred, err := EnsureFreshCredential("openai", cfg, RefreshPolicy{})
	if err == nil {
		t.Fatal("EnsureFreshCredential() error = nil, want non-nil")
	}
	if cred != nil {
		t.Errorf("EnsureFreshCredential() = %+v, want nil", cred)
	}

	stored, err := GetCredential("openai")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if stored == nil {
		t.Fatal("GetCredential() returned nil")
	}
	if stored.AccessToken != "old-access" {
		t.Errorf("stored AccessToken = %q, want %q", stored.AccessToken, "old-access")
	}
	if stored.RefreshToken != "rot-0" {
		t.Errorf("stored RefreshToken = %q, want %q", stored.RefreshToken, "rot-0")
	}
}

func TestEnsureFreshCredentialPreservesMetadata(t *testing.T) {
	setupRefreshTestStore(t)
	rs := newRefreshTestServer(t, http.StatusOK)
	cfg := withServer(OpenAIOAuthConfig(), rs)

	original := expiredCred("openai", "rot-0")
	original.Email = "user@example.com"
	original.ProjectID = "proj-1"
	original.AccountID = "acct-1"
	if err := SetCredential("openai", original); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	cred, err := EnsureFreshCredential("openai", cfg, RefreshPolicy{})
	if err != nil {
		t.Fatalf("EnsureFreshCredential() error: %v", err)
	}
	if cred == nil {
		t.Fatal("EnsureFreshCredential() returned nil")
	}
	if cred.Email != "user@example.com" {
		t.Errorf("Email = %q, want %q", cred.Email, "user@example.com")
	}
	if cred.ProjectID != "proj-1" {
		t.Errorf("ProjectID = %q, want %q", cred.ProjectID, "proj-1")
	}
	if cred.AccountID != "acct-1" {
		t.Errorf("AccountID = %q, want %q", cred.AccountID, "acct-1")
	}
	if cred.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", cred.Provider, "openai")
	}
	if cred.AuthMethod != "oauth" {
		t.Errorf("AuthMethod = %q, want %q", cred.AuthMethod, "oauth")
	}
	if cred.AccessToken != "fresh-1" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "fresh-1")
	}

	stored, err := GetCredential("openai")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if stored.Email != "user@example.com" || stored.ProjectID != "proj-1" || stored.AccountID != "acct-1" {
		t.Errorf("stored metadata lost: %+v", stored)
	}
}

func TestEnsureFreshCredentialPolicySkipsNonOAuth(t *testing.T) {
	setupRefreshTestStore(t)
	rs := newRefreshTestServer(t, http.StatusOK)
	cfg := withServer(OpenAIOAuthConfig(), rs)

	apiKey := &AuthCredential{
		AccessToken:  "api-key-token",
		RefreshToken: "api-key-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour),
		Provider:     "openai",
		AuthMethod:   "apikey",
	}
	if err := SetCredential("openai", apiKey); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	cred, err := EnsureFreshCredential("openai", cfg, RefreshPolicy{RequireOAuthMethod: true})
	if err != nil {
		t.Fatalf("EnsureFreshCredential(require oauth) error: %v", err)
	}
	if cred == nil || cred.AccessToken != "api-key-token" {
		t.Errorf("EnsureFreshCredential(require oauth) = %+v, want stored api-key credential", cred)
	}
	if got := rs.hits.Load(); got != 0 {
		t.Errorf("token endpoint hits with RequireOAuthMethod = %d, want 0", got)
	}

	cred, err = EnsureFreshCredential("openai", cfg, RefreshPolicy{})
	if err != nil {
		t.Fatalf("EnsureFreshCredential(zero policy) error: %v", err)
	}
	if cred == nil || cred.AccessToken != "fresh-1" {
		t.Errorf("EnsureFreshCredential(zero policy) = %+v, want AccessToken %q", cred, "fresh-1")
	}
	if got := rs.hits.Load(); got != 1 {
		t.Errorf("token endpoint hits with zero policy = %d, want 1", got)
	}
}

func TestEnsureFreshCredentialKeysAreIndependent(t *testing.T) {
	setupRefreshTestStore(t)
	rs := newRefreshTestServer(t, http.StatusOK)

	openaiCfg := withServer(OpenAIOAuthConfig(), rs)
	googleCfg := withServer(GoogleAntigravityOAuthConfig(), rs)

	if err := SetCredential("openai", expiredCred("openai", "rot-0")); err != nil {
		t.Fatalf("SetCredential(openai) error: %v", err)
	}
	if err := SetCredential("google-antigravity", expiredCred("google-antigravity", "grot-0")); err != nil {
		t.Fatalf("SetCredential(google-antigravity) error: %v", err)
	}

	const perKey = 8
	var wg sync.WaitGroup
	var failures atomic.Int32
	type job struct {
		provider string
		cfg      OAuthProviderConfig
	}
	jobs := []job{
		{"openai", openaiCfg},
		{"google-antigravity", googleCfg},
	}
	wg.Add(perKey * len(jobs))
	for _, j := range jobs {
		for i := 0; i < perKey; i++ {
			go func(j job) {
				defer wg.Done()
				cred, err := EnsureFreshCredential(j.provider, j.cfg, RefreshPolicy{})
				if err != nil || cred == nil {
					failures.Add(1)
				}
			}(j)
		}
	}
	wg.Wait()

	if n := failures.Load(); n != 0 {
		t.Errorf("failed callers = %d, want 0", n)
	}
	if got := rs.hits.Load(); got != 2 {
		t.Errorf("token endpoint hits = %d, want 2 (one per key)", got)
	}
}

func assertProviderErrorParity(t *testing.T, got error, prefix string, rawErr error) {
	t.Helper()
	if rawErr == nil {
		t.Fatal("rawErr = nil: cannot rebuild the old provider message")
	}
	want := fmt.Errorf("%s: %w", prefix, rawErr).Error()
	if got == nil {
		t.Fatalf("EnsureFreshCredential() error = nil, want %q", want)
	}
	if got.Error() != want {
		t.Errorf("error = %q\n  want (c993e50e provider wrapping) %q", got.Error(), want)
	}
	if !strings.Contains(got.Error(), prefix+":") {
		t.Errorf("error = %q, want it to contain %q", got.Error(), prefix+":")
	}
}

func TestEnsureFreshCredentialErrorContextLoadingCredentials(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	s := openSQLiteStore(t)
	useTestAuthRepo(t, s.Auth())

	if _, err := s.DB().Exec(`DROP TABLE auth_credentials`); err != nil {
		t.Fatalf("DROP TABLE: %v", err)
	}

	rs := newRefreshTestServer(t, http.StatusOK)
	cfg := withServer(OpenAIOAuthConfig(), rs)

	cred, err := EnsureFreshCredential("openai", cfg, RefreshPolicy{})
	if err == nil {
		t.Fatal("EnsureFreshCredential() error = nil, want non-nil")
	}
	if cred != nil {
		t.Errorf("EnsureFreshCredential() = %+v, want nil", cred)
	}

	_, rawErr := GetCredential("openai")
	assertProviderErrorParity(t, err, "loading auth credentials", rawErr)

	if got := rs.hits.Load(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0 (store read failed first)", got)
	}
}

func TestEnsureFreshCredentialErrorContextRefreshingToken(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	s := openSQLiteStore(t)
	useTestAuthRepo(t, s.Auth())

	rs := newRefreshTestServer(t, http.StatusBadRequest)
	cfg := withServer(OpenAIOAuthConfig(), rs)

	if err := SetCredential("openai", expiredCred("openai", "rot-0")); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	cred, err := EnsureFreshCredential("openai", cfg, RefreshPolicy{})
	if err == nil {
		t.Fatal("EnsureFreshCredential() error = nil, want non-nil")
	}
	if cred != nil {
		t.Errorf("EnsureFreshCredential() = %+v, want nil", cred)
	}

	_, rawErr := RefreshAccessToken(expiredCred("openai", "rot-0"), cfg)
	assertProviderErrorParity(t, err, "refreshing token", rawErr)
}

func TestEnsureFreshCredentialErrorContextSavingRefreshedToken(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	s := openSQLiteStore(t)
	useTestAuthRepo(t, s.Auth())

	rs := newRefreshTestServer(t, http.StatusOK)
	cfg := withServer(OpenAIOAuthConfig(), rs)

	if err := SetCredential("openai", expiredCred("openai", "rot-0")); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	if _, err := s.DB().Exec(
		`CREATE TRIGGER t_fail_set BEFORE UPDATE ON auth_credentials
		 BEGIN SELECT RAISE(ABORT, 'injected set failure'); END`,
	); err != nil {
		t.Fatalf("CREATE TRIGGER: %v", err)
	}

	cred, err := EnsureFreshCredential("openai", cfg, RefreshPolicy{})
	if err == nil {
		t.Fatal("EnsureFreshCredential() error = nil, want non-nil")
	}
	if cred != nil {
		t.Errorf("EnsureFreshCredential() = %+v, want nil", cred)
	}

	rawErr := SetCredential("openai", expiredCred("openai", "rot-1"))
	assertProviderErrorParity(t, err, "saving refreshed token", rawErr)

	if got := rs.hits.Load(); got != 1 {
		t.Errorf("token endpoint hits = %d, want 1", got)
	}
}

func claudeGateCfg(srv *httptest.Server) OAuthProviderConfig {
	return OAuthProviderConfig{
		Name:                  "anthropic",
		ClientID:              "cid",
		RequireInferenceScope: true,
		TokenRequestStyle:     TokenRequestJSON,
		TokenURL:              srv.URL,
		HTTPClient:            srv.Client(),
	}
}

func TestEnsureFreshCredentialPersistsRotatedTokenOnInferenceGateFailure(t *testing.T) {
	setupRefreshTestStore(t)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"rotated","expires_in":3600,"scope":"org:create_api_key user:profile"}`))
	}))
	t.Cleanup(srv.Close)

	cfg := claudeGateCfg(srv)

	if err := SetCredential("anthropic", expiredCred("anthropic", "old-refresh")); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}
	before, err := GetCredential("anthropic")
	if err != nil {
		t.Fatalf("GetCredential() before error: %v", err)
	}
	t.Logf("stored credential before the call: access=%q refresh=%q expires_at=%v provider=%q auth_method=%q",
		before.AccessToken, before.RefreshToken, before.ExpiresAt, before.Provider, before.AuthMethod)

	cred, err := EnsureFreshCredential("anthropic", cfg, RefreshPolicy{RequireOAuthMethod: true})
	if err == nil {
		t.Fatal("EnsureFreshCredential() error = nil, want the inference-scope gate failure")
	}
	if !strings.Contains(err.Error(), "user:inference") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "user:inference")
	}
	if cred != nil {
		t.Errorf("EnsureFreshCredential() = %+v, want nil", cred)
	}

	stored, err := GetCredential("anthropic")
	if err != nil {
		t.Fatalf("GetCredential() after error: %v", err)
	}
	if stored == nil {
		t.Fatal("GetCredential() after = nil, want the rotated credential")
	}
	t.Logf("stored credential after the call:  access=%q refresh=%q expires_at=%v provider=%q auth_method=%q scopes=%v",
		stored.AccessToken, stored.RefreshToken, stored.ExpiresAt, stored.Provider, stored.AuthMethod, stored.Scopes)

	if stored.RefreshToken != "rotated" {
		t.Errorf("persisted RefreshToken = %q, want %q (rotated token was lost)", stored.RefreshToken, "rotated")
	}
	if stored.AccessToken != "new-access" {
		t.Errorf("persisted AccessToken = %q, want %q", stored.AccessToken, "new-access")
	}
	if stored.Provider != "anthropic" {
		t.Errorf("persisted Provider = %q, want %q", stored.Provider, "anthropic")
	}
	if stored.AuthMethod != "oauth" {
		t.Errorf("persisted AuthMethod = %q, want %q", stored.AuthMethod, "oauth")
	}

	if got := hits.Load(); got != 1 {
		t.Errorf("token endpoint hits = %d, want exactly 1", got)
	}
}

func TestEnsureFreshCredentialKeepsStoredTokenOnNonGateRefreshError(t *testing.T) {
	setupRefreshTestStore(t)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(srv.Close)

	cfg := claudeGateCfg(srv)

	if err := SetCredential("anthropic", expiredCred("anthropic", "old-refresh")); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	cred, err := EnsureFreshCredential("anthropic", cfg, RefreshPolicy{RequireOAuthMethod: true})
	if err == nil {
		t.Fatal("EnsureFreshCredential() error = nil, want non-nil")
	}
	if cred != nil {
		t.Errorf("EnsureFreshCredential() = %+v, want nil", cred)
	}
	if strings.Contains(err.Error(), "user:inference") {
		t.Errorf("error = %q, want the plain refresh failure (gate not reached)", err.Error())
	}

	stored, err := GetCredential("anthropic")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if stored == nil {
		t.Fatal("GetCredential() = nil, want the untouched stored credential")
	}
	if stored.RefreshToken != "old-refresh" {
		t.Errorf("stored RefreshToken = %q, want %q (non-gate failure must not overwrite)", stored.RefreshToken, "old-refresh")
	}
	if stored.AccessToken != "old-access" {
		t.Errorf("stored AccessToken = %q, want %q", stored.AccessToken, "old-access")
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("token endpoint hits = %d, want 1", got)
	}
}
