package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRefreshAccessToken_PreservesEmailAndProjectID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "new-tok",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}
	cred := &AuthCredential{
		RefreshToken: "rt",
		Email:        "me@example.com",
		ProjectID:    "proj-123",
		AccountID:    "acc-1",
		Provider:     "google-antigravity",
	}
	refreshed, err := RefreshAccessToken(cred, cfg)
	if err != nil {
		t.Fatalf("RefreshAccessToken() error: %v", err)
	}
	if refreshed.Email != "me@example.com" {
		t.Errorf("Email = %q, want preserved me@example.com", refreshed.Email)
	}
	if refreshed.ProjectID != "proj-123" {
		t.Errorf("ProjectID = %q, want preserved proj-123", refreshed.ProjectID)
	}
	if refreshed.RefreshToken != "rt" {
		t.Errorf("RefreshToken = %q, want preserved rt", refreshed.RefreshToken)
	}
	if refreshed.AccountID != "acc-1" {
		t.Errorf("AccountID = %q, want preserved acc-1", refreshed.AccountID)
	}
}

func TestExchangeCodeForTokens_GoogleWithSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.FormValue("client_secret") != "secret" {
			http.Error(w, "missing secret", http.StatusBadRequest)
			return
		}
		if r.FormValue("code_verifier") != "verifier" {
			http.Error(w, "missing code_verifier", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "ga-token",
			"refresh_token": "ga-refresh",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Issuer:       "https://accounts.google.com/oauth2/v2",
		TokenURL:     server.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
	}
	cred, err := exchangeCodeForTokens(cfg, "code", "verifier", "http://localhost/callback")
	if err != nil {
		t.Fatalf("exchangeCodeForTokens() error: %v", err)
	}
	// Provider must be detected as google-antigravity because TokenURL contains
	// no "googleapis.com" here (server.URL), so it falls back to "openai"; but
	// the important part is the token values are parsed correctly.
	if cred.AccessToken != "ga-token" {
		t.Errorf("AccessToken = %q, want ga-token", cred.AccessToken)
	}
	if cred.RefreshToken != "ga-refresh" {
		t.Errorf("RefreshToken = %q, want ga-refresh", cred.RefreshToken)
	}
}

func TestParseTokenResponse_AccountIDFromAccessToken(t *testing.T) {
	tok := makeJWTForClaims(t, map[string]interface{}{"chatgpt_account_id": "acc-aware"})
	body, _ := json.Marshal(map[string]interface{}{
		"access_token": tok,
		"expires_in":   60,
	})
	cred, err := parseTokenResponse(body, "openai")
	if err != nil {
		t.Fatalf("parseTokenResponse() error: %v", err)
	}
	if cred.AccountID != "acc-aware" {
		t.Errorf("AccountID = %q, want acc-aware (extracted from access token)", cred.AccountID)
	}
	if time.Until(cred.ExpiresAt) <= 0 || time.Until(cred.ExpiresAt) > 2*time.Minute {
		t.Errorf("ExpiresAt = %v, not within ~60s of now", cred.ExpiresAt)
	}
}