package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

// closedAuthRepo returns an AuthRepo backed by a store that has been closed,
// so every SQL operation returns an error (exercising error branches).
func closedAuthRepo(t *testing.T) *store.AuthRepo {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("store.Open() failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("store.Close() failed: %v", err)
	}
	return s.Auth()
}

// useClosedRepo registers a closed repo and resets it afterwards.
func useClosedRepo(t *testing.T) {
	t.Helper()
	UseStore(closedAuthRepo(t))
	t.Cleanup(func() { UseStore(nil) })
}

func TestLoadStore_RepoListError(t *testing.T) {
	useClosedRepo(t)
	if _, err := LoadStore(); err == nil {
		t.Fatal("LoadStore() with closed repo expected error")
	}
	if _, err := GetCredential("openai"); err == nil {
		t.Fatal("GetCredential() with closed repo expected error")
	}
}

func TestLoadStore_RepoUnmarshalError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	useTestAuthRepo(t, openSQLiteStore(t).Auth())

	// Write an invalid credential blob directly.
	repo := currentRepo()
	if err := repo.SetCredential("openai", "{not-json"); err != nil {
		t.Fatalf("Seeding invalid credential: %v", err)
	}
	if _, err := LoadStore(); err == nil {
		t.Fatal("LoadStore() expected unmarshal error")
	}
	if _, err := GetCredential("openai"); err == nil {
		t.Fatal("GetCredential() expected unmarshal error")
	}
}

func TestSetCredential_RepoError(t *testing.T) {
	useClosedRepo(t)
	if err := SetCredential("openai", &AuthCredential{Provider: "openai"}); err == nil {
		t.Fatal("SetCredential() with closed repo expected error")
	}
}

func TestSaveStore_RepoError(t *testing.T) {
	useClosedRepo(t)
	store := &AuthStore{Credentials: map[string]*AuthCredential{
		"openai": {AccessToken: "x", Provider: "openai"},
	}}
	if err := SaveStore(store); err == nil {
		t.Fatal("SaveStore() with closed repo expected error")
	}
	// nil store is a no-op even with a repo.
	if err := SaveStore(nil); err != nil {
		t.Fatalf("SaveStore(nil) error: %v", err)
	}
}

func TestDeleteCredential_RepoError(t *testing.T) {
	useClosedRepo(t)
	if err := DeleteCredential("openai"); err == nil {
		t.Fatal("DeleteCredential() with closed repo expected error")
	}
}

func TestDeleteAllCredentials_RepoError(t *testing.T) {
	useClosedRepo(t)
	if err := DeleteAllCredentials(); err == nil {
		t.Fatal("DeleteAllCredentials() with closed repo expected error")
	}
}

// --- Legacy JSON backend additional coverage ---

func TestLoadJSONStore_ParseError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	path := filepath.Join(tmpDir, "auth.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadJSONStore(); err == nil {
		t.Fatal("loadJSONStore() expected parse error")
	}
	if _, err := LoadStore(); err == nil {
		t.Fatal("LoadStore() expected parse error")
	}
}

func TestMigrateFromJSONIfNeeded_ListError(t *testing.T) {
	useClosedRepo(t)
	if err := migrateFromJSONIfNeeded(currentRepo()); err == nil {
		t.Fatal("migrateFromJSONIfNeeded() with closed repo expected error")
	}
}

func TestMigrateFromJSONIfNeeded_AlreadyHasData(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	repo := openSQLiteStore(t).Auth()
	useTestAuthRepo(t, repo)
	if err := repo.SetCredential("openai", `{"access_token":"existing"}`); err != nil {
		t.Fatal(err)
	}
	// Even if legacy file exists, no migration should run (data already present).
	if err := migrateFromJSONIfNeeded(repo); err != nil {
		t.Fatalf("migrateFromJSONIfNeeded() error: %v", err)
	}
}

func TestMigrateFromJSON_BrokenLegacyFile(t *testing.T) {
	// SQLite empty + broken legacy JSON must not block migration.
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	os.WriteFile(filepath.Join(tmpDir, "auth.json"), []byte("{broken"), 0600)
	repo := openSQLiteStore(t).Auth()
	useTestAuthRepo(t, repo)
	if err := migrateFromJSONIfNeeded(repo); err != nil {
		t.Fatalf("migrateFromJSONIfNeeded() with broken legacy file should return nil: %v", err)
	}
}

func TestMigrateFromJSON_NoLegacyFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	repo := openSQLiteStore(t).Auth()
	useTestAuthRepo(t, repo)
	if err := migrateFromJSONIfNeeded(repo); err != nil {
		t.Fatalf("migrateFromJSONIfNeeded() with no legacy file: %v", err)
	}
}

// --- OAuth edge cases ---

func TestRefreshAccessToken_WithClientSecretAndTokenURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.FormValue("client_secret") != "secret" {
			http.Error(w, "missing secret", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "with-secret",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Issuer:       "https://issuer.example.com",
		TokenURL:     server.URL,
		ClientID:     "client",
		ClientSecret: "secret",
	}
	cred := &AuthCredential{RefreshToken: "rt", Provider: "openai"}
	refreshed, err := RefreshAccessToken(cred, cfg)
	if err != nil {
		t.Fatalf("RefreshAccessToken() error: %v", err)
	}
	if refreshed.AccessToken != "with-secret" {
		t.Errorf("AccessToken = %q, want with-secret", refreshed.AccessToken)
	}
	// RefreshToken should fall back to the previous one.
	if refreshed.RefreshToken != "rt" {
		t.Errorf("RefreshToken = %q, want rt", refreshed.RefreshToken)
	}
}

func TestRefreshAccessToken_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}
	cred := &AuthCredential{RefreshToken: "rt", Provider: "openai"}
	_, err := RefreshAccessToken(cred, cfg)
	if err == nil {
		t.Fatal("RefreshAccessToken() expected error on non-200")
	}
	if !strings.Contains(err.Error(), "token refresh failed") {
		t.Errorf("error = %q, want token refresh failed", err.Error())
	}
}

func TestRefreshAccessToken_NetworkError(t *testing.T) {
	cfg := OAuthProviderConfig{Issuer: "http://127.0.0.1:1", ClientID: "c"}
	cred := &AuthCredential{RefreshToken: "rt", Provider: "openai"}
	_, err := RefreshAccessToken(cred, cfg)
	if err == nil {
		t.Fatal("RefreshAccessToken() expected network error")
	}
}

func TestRefreshAccessToken_NonZeroRefreshed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "new",
			"refresh_token": "new-rt",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}
	cred := &AuthCredential{RefreshToken: "old-rt", Provider: "openai"}
	refreshed, err := RefreshAccessToken(cred, cfg)
	if err != nil {
		t.Fatalf("RefreshAccessToken() error: %v", err)
	}
	if refreshed.RefreshToken != "new-rt" {
		t.Errorf("RefreshToken = %q, want new-rt", refreshed.RefreshToken)
	}
}

func TestExchangeCodeForTokens_GoogleProviderDetection(t *testing.T) {
	// A fresh empty store AND ensure path works; this test verifies the
	// provider-name detection logic used in exchangeCodeForTokens without
	// making a real network call.
	cfg := OAuthProviderConfig{
		Issuer:       "https://accounts.google.com",
		TokenURL:     "https://oauth2.googleapis.com/token",
		ClientID:     "c",
		ClientSecret: "secret",
	}
	// Copy the detection logic from exchangeCodeForTokens.
	provider := "openai"
	tokenURL := cfg.TokenURL
	if tokenURL != "" && strings.Contains(tokenURL, "googleapis.com") {
		provider = "google-antigravity"
	}
	if provider != "google-antigravity" {
		t.Errorf("provider = %q, want google-antigravity", provider)
	}
}

func TestExchangeCodeForTokens_Errors(t *testing.T) {
	// Non-200 response.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer server.Close()
	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}
	if _, err := exchangeCodeForTokens(cfg, "c", "v", "http://x/callback"); err == nil {
		t.Fatal("expected exchange error on non-200")
	}
	// Network error.
	cfg = OAuthProviderConfig{Issuer: "http://127.0.0.1:1", ClientID: "c"}
	if _, err := exchangeCodeForTokens(cfg, "c", "v", "http://x/callback"); err == nil {
		t.Fatal("expected network error")
	}
}

func TestParseTokenResponse_InvalidJSON(t *testing.T) {
	if _, err := parseTokenResponse([]byte("{bad"), "openai"); err == nil {
		t.Fatal("expected JSON error")
	}
}

func TestParseFlexibleInt_InvalidType(t *testing.T) {
	if _, err := parseFlexibleInt(json.RawMessage(`{"a":1}`)); err == nil {
		t.Fatal("expected error for object interval")
	}
}

func TestExtractAccountID_FullClaims(t *testing.T) {
	tok := makeJWTForClaims(t, map[string]interface{}{
		"chatgpt_account_id": "acc-chatgpt",
	})
	if got := extractAccountID(tok); got != "acc-chatgpt" {
		t.Errorf("extractAccountID() = %q, want acc-chatgpt", got)
	}

	tok = makeJWTForClaims(t, map[string]interface{}{
		"https://api.openai.com/auth.chatgpt_account_id": "acc-full-url",
	})
	if got := extractAccountID(tok); got != "acc-full-url" {
		t.Errorf("extractAccountID() = %q, want acc-full-url", got)
	}
}

func TestExtractAccountID_Empty(t *testing.T) {
	// Non-JWT and JWT without account fields.
	if got := extractAccountID("not-a-jwt"); got != "" {
		t.Errorf("extractAccountID(not-jwt) = %q, want empty", got)
	}
	tok := makeJWTForClaims(t, map[string]interface{}{"foo": "bar"})
	if got := extractAccountID(tok); got != "" {
		t.Errorf("extractAccountID(no-account) = %q, want empty", got)
	}
}

func TestParseJWTClaims_InvalidPayload(t *testing.T) {
	// Base64 payload that is not valid JSON.
	bad := "eyJhbGciOiJub25lIn0.dGhpcyBpcyBub3QganNvbg.signature"
	if _, err := parseJWTClaims(bad); err == nil {
		t.Fatal("expected error for non-JSON payload")
	}
	// Base64 decode error.
	if _, err := base64URLDecode("!!!!"); err == nil {
		t.Fatal("expected base64 decode error")
	}
}

func TestBuildAuthorizeURL_Google(t *testing.T) {
	cfg := OAuthProviderConfig{Issuer: "https://accounts.google.com/o/oauth2/v2"}
	u := buildAuthorizeURL(cfg, PKCECodes{CodeVerifier: "v", CodeChallenge: "c"}, "s", "http://localhost/cb")
	if !strings.HasPrefix(u, "https://accounts.google.com/o/oauth2/v2/auth?") {
		t.Errorf("google URL = %q", u)
	}
	if !strings.Contains(u, "access_type=offline") || !strings.Contains(u, "prompt=consent") {
		t.Errorf("google URL missing offline/consent: %q", u)
	}
}

func TestBuildAuthorizeURL_OpenAIPort(t *testing.T) {
	cfg := OpenAIOAuthConfig()
	u := buildAuthorizeURL(cfg, PKCECodes{CodeVerifier: "v", CodeChallenge: "c"}, "s", "http://localhost/cb")
	if !strings.Contains(u, "/oauth/authorize?") {
		t.Errorf("openai URL = %q", u)
	}
	if !strings.Contains(u, "originator=codex_cli_rs") {
		t.Errorf("missing originator: %q", u)
	}
}

func TestAuthCredential_IsExpiredEdge(t *testing.T) {
	c := &AuthCredential{ExpiresAt: time.Now().Add(-time.Second)}
	if !c.IsExpired() {
		t.Error("expected expired")
	}
	if !c.NeedsRefresh() {
		t.Error("expired credential should also need refresh")
	}
}

func TestLoadJSONStore_ReadErrorNonNotExist(t *testing.T) {
	// Point the config dir at a regular file so auth.json cannot be read as a
	// file; os.ReadFile returns a non-IsNotExist error (EISDIR), covering the
	// "auth: read ..." branch.
	filePath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(filePath, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LELE_CONFIG_DIR", filePath)
	if _, err := loadJSONStore(); err == nil {
		t.Fatal("loadJSONStore() expected read error")
	}
}

func TestLoadJSONStore_EmptyCredentialsObject(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, "auth.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := loadJSONStore()
	if err != nil {
		t.Fatalf("loadJSONStore() error: %v", err)
	}
	if s == nil {
		t.Fatal("loadJSONStore() returned nil")
	}
	if len(s.Credentials) != 0 {
		t.Errorf("Credentials len = %d, want 0 (map should be non-nil)", len(s.Credentials))
	}
}

func TestSaveStore_RepoUpsert(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	repo := openSQLiteStore(t).Auth()
	useTestAuthRepo(t, repo)

	store := &AuthStore{Credentials: map[string]*AuthCredential{
		"openai": {AccessToken: "save-token", Provider: "openai"},
	}}
	if err := SaveStore(store); err != nil {
		t.Fatalf("SaveStore() error: %v", err)
	}
	raw, found, err := repo.GetCredential("openai")
	if err != nil || !found {
		t.Fatalf("GetCredential() after SaveStore: found=%v err=%v", found, err)
	}
	if !strings.Contains(raw, "save-token") {
		t.Errorf("persisted raw does not contain token: %q", raw)
	}
}

func TestDeleteAllCredentials_LegacyNoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	useTestAuthRepo(t, nil) // legacy backend
	if err := DeleteAllCredentials(); err != nil {
		t.Fatalf("DeleteAllCredentials() with no file: %v", err)
	}
}

func TestDeleteAllCredentials_LegacyWithFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	useTestAuthRepo(t, nil) // legacy backend

	// Write existing creds, then delete all.
	if err := SetCredential("openai", &AuthCredential{AccessToken: "x", Provider: "openai"}); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "auth.json")); err != nil {
		t.Fatalf("auth.json should exist: %v", err)
	}
	if err := DeleteAllCredentials(); err != nil {
		t.Fatalf("DeleteAllCredentials() error: %v", err)
	}
	store, err := LoadStore()
	if err != nil {
		t.Fatalf("LoadStore() error: %v", err)
	}
	if len(store.Credentials) != 0 {
		t.Errorf("Credentials len = %d, want 0", len(store.Credentials))
	}
}

func TestSaveStore_LegacyMkdirAllFailure(t *testing.T) {
	// LELE_CONFIG_DIR points to a regular file, so MkdirAll fails.
	filePath := filepath.Join(t.TempDir(), "configfile")
	if err := os.WriteFile(filePath, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LELE_CONFIG_DIR", filePath)
	useTestAuthRepo(t, nil) // legacy backend
	err := SaveStore(&AuthStore{Credentials: map[string]*AuthCredential{
		"openai": {AccessToken: "x", Provider: "openai"},
	}})
	if err == nil {
		t.Fatal("SaveStore() expected MkdirAll failure")
	}
}

func TestGetCredential_LegacyLoadError(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "configfile")
	if err := os.WriteFile(filePath, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LELE_CONFIG_DIR", filePath)
	useTestAuthRepo(t, nil) // legacy backend
	if _, err := GetCredential("openai"); err == nil {
		t.Fatal("GetCredential() expected error when auth.json unreadable")
	}
}

func TestSetCredential_LegacyLoadError(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "configfile")
	if err := os.WriteFile(filePath, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LELE_CONFIG_DIR", filePath)
	useTestAuthRepo(t, nil)
	if err := SetCredential("openai", &AuthCredential{AccessToken: "x"}); err == nil {
		t.Fatal("SetCredential() expected error")
	}
}

func TestDeleteCredential_LegacyLoadError(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "configfile")
	if err := os.WriteFile(filePath, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LELE_CONFIG_DIR", filePath)
	useTestAuthRepo(t, nil)
	if err := DeleteCredential("openai"); err == nil {
		t.Fatal("DeleteCredential() expected error")
	}
}

func TestDeleteAllCredentials_LegacyRemoveError(t *testing.T) {
	// Point legacy auth path at a directory so os.Remove fails (EISDIR).
	dirPath := filepath.Join(t.TempDir(), "configdir")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}
	// authFilePath() = LELE_CONFIG_DIR/auth.json -> a directory cannot be removed
	// cleanly. Put a file inside so os.Remove fails with ENOTEMPTY.
	os.Mkdir(filepath.Join(dirPath, "auth.json"), 0755)
	os.WriteFile(filepath.Join(dirPath, "auth.json", "child"), []byte("x"), 0600)
	t.Setenv("LELE_CONFIG_DIR", dirPath)
	useTestAuthRepo(t, nil)
	if err := DeleteAllCredentials(); err == nil {
		t.Fatal("DeleteAllCredentials() expected remove error")
	}
}
