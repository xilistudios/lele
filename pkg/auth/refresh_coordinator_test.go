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

// setupRefreshTestStore points the auth package at a throwaway SQLite store
// (single-connection pool, so writes are serialized) and returns a fresh,
// isolated environment for each test.
func setupRefreshTestStore(t *testing.T) {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	s := openSQLiteStore(t)
	useTestAuthRepo(t, s.Auth())
}

// refreshTestServer is a fake token endpoint that counts requests and, on
// success, rotates the refresh token (rot-<n>) exactly like a real provider.
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

// withServer returns a copy of cfg pointed at the fake token endpoint.
func withServer(cfg OAuthProviderConfig, rs *refreshTestServer) OAuthProviderConfig {
	cfg.HTTPClient = rs.Client()
	cfg.TokenURL = rs.URL
	return cfg
}

// expiredCred builds a stored credential that NeedsRefresh() and carries the
// given refresh token.
func expiredCred(provider string, refreshToken string) *AuthCredential {
	return &AuthCredential{
		AccessToken:  "old-access",
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(-time.Hour),
		Provider:     provider,
		AuthMethod:   "oauth",
	}
}

// A — the central test: 16 concurrent callers sharing a key must produce
// exactly ONE token request; all of them observe the same fresh token.
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

// B — a follow-up call on an already-fresh credential must not touch the
// network (fast path).
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

// C — no stored credential yields (nil, nil) and no network call.
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

// D — NeedsRefresh but no refresh token: return the stored credential
// untouched and make no network call.
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

// E — a failed refresh returns an error and must leave the stored credential
// untouched (no persistence of a partially-consumed token).
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

// F — identity metadata survives a refresh whose response omits it.
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

// G — RequireOAuthMethod mirrors codex's guard: non-oauth credentials are
// returned untouched, while the zero policy refreshes them.
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

	// Zero policy: the refreshable credential is refreshed.
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

// H — locks are per key: concurrent refreshes of two different providers each
// hit the endpoint exactly once and both succeed.
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

// --- Error-context parity -------------------------------------------------
//
// Before the coordinator owned the error context, each provider built the
// message inline around the raw error returned by GetCredential /
// RefreshAccessToken / SetCredential, i.e. at c993e50e:
//
//	GetCredential:      fmt.Errorf("loading auth credentials: %w", err)
//	RefreshAccessToken: fmt.Errorf("refreshing token: %w", err)
//	SetCredential:      fmt.Errorf("saving refreshed token: %w", err)
//
// EnsureFreshCredential now applies exactly those wrappers itself, so the final
// strings the providers return must stay byte-for-byte identical. The tests
// below rebuild the OLD string from the very same raw error and assert full
// equality (not just a prefix), plus the required prefix assertion.

// assertProviderErrorParity asserts that got.Error() is byte-for-byte the
// message the providers used to produce at c993e50e for the same raw error,
// i.e. `<prefix>: <rawErr>`, and that the prefix is present.
func assertProviderErrorParity(t *testing.T, got error, prefix string, rawErr error) {
	t.Helper()
	if rawErr == nil {
		t.Fatal("rawErr = nil: cannot rebuild the old provider message")
	}
	want := fmt.Errorf("%s: %w", prefix, rawErr).Error()
	if got == nil {
		t.Fatalf("EnsureFreshCredential() error = nil, want %q", want)
	}
	// Literal parity: the new string must equal the old inline wrapping.
	if got.Error() != want {
		t.Errorf("error = %q\n  want (c993e50e provider wrapping) %q", got.Error(), want)
	}
	// Required explicit prefix assertion.
	if !strings.Contains(got.Error(), prefix+":") {
		t.Errorf("error = %q, want it to contain %q", got.Error(), prefix+":")
	}
}

// I1 — store read failure: EnsureFreshCredential must yield
// "loading auth credentials: <err>".
func TestEnsureFreshCredentialErrorContextLoadingCredentials(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	s := openSQLiteStore(t)
	useTestAuthRepo(t, s.Auth())

	// Force the store read to fail deterministically and network-free: drop the
	// table the repository reads from.
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

	// Rebuild the raw error the old provider wrapped.
	_, rawErr := GetCredential("openai")
	assertProviderErrorParity(t, err, "loading auth credentials", rawErr)

	if got := rs.hits.Load(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0 (store read failed first)", got)
	}
}

// I2 — refresh failure: EnsureFreshCredential must yield
// "refreshing token: <err>".
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

	// Rebuild the raw error the old provider wrapped (the 400 body is static, so
	// the message is identical on every call).
	_, rawErr := RefreshAccessToken(expiredCred("openai", "rot-0"), cfg)
	assertProviderErrorParity(t, err, "refreshing token", rawErr)
}

// I3 — persist failure: EnsureFreshCredential must yield
// "saving refreshed token: <err>".
func TestEnsureFreshCredentialErrorContextSavingRefreshedToken(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	s := openSQLiteStore(t)
	useTestAuthRepo(t, s.Auth())

	rs := newRefreshTestServer(t, http.StatusOK)
	cfg := withServer(OpenAIOAuthConfig(), rs)

	if err := SetCredential("openai", expiredCred("openai", "rot-0")); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	// Force the write to fail deterministically while leaving reads intact: a
	// BEFORE UPDATE trigger aborts every upsert.
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

	// Rebuild the raw error the old provider wrapped.
	rawErr := SetCredential("openai", expiredCred("openai", "rot-1"))
	assertProviderErrorParity(t, err, "saving refreshed token", rawErr)

	// The refresh did happen before the failed persist.
	if got := rs.hits.Load(); got != 1 {
		t.Errorf("token endpoint hits = %d, want 1", got)
	}
}
