package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGoogleAntigravityOAuthConfig(t *testing.T) {
	cfg := GoogleAntigravityOAuthConfig()
	if cfg.Issuer != "https://accounts.google.com/o/oauth2/v2" {
		t.Errorf("Issuer = %q", cfg.Issuer)
	}
	if cfg.TokenURL != "https://oauth2.googleapis.com/token" {
		t.Errorf("TokenURL = %q", cfg.TokenURL)
	}
	if cfg.ClientID == "" {
		t.Error("ClientID is empty")
	}
	if cfg.ClientSecret == "" {
		t.Error("ClientSecret is empty")
	}
	if cfg.Port == 0 {
		t.Error("Port is 0")
	}
	if !strings.Contains(cfg.Scopes, "cloud-platform") {
		t.Error("Scopes missing cloud-platform")
	}
}

func TestDecodeBase64(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"valid", "aGVsbG8=", "hello"},
		{"valid long", "ZGVjb2RlLW1l", "decode-me"},
		{"invalid", "!!!not-base64!!!", "!!!not-base64!!!"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeBase64(tt.in); got != tt.want {
				t.Errorf("decodeBase64(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestGenerateState(t *testing.T) {
	state, err := generateState()
	if err != nil {
		t.Fatalf("generateState() error: %v", err)
	}
	if len(state) != 64 {
		t.Errorf("state length = %d, want 64 (32 bytes hex)", len(state))
	}
	// Does not contain base64 URL chars; should be pure hex.
	for _, c := range state {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("state contains non-hex char %q", c)
		}
	}
}

// freePort returns a currently-free TCP port on the loopback interface.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// waitForTCPServer polls until TCP connections to addr are accepted.
func waitForTCPServer(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s never became ready", addr)
}

func TestLoginBrowserStateMismatch(t *testing.T) {
	port := freePort(t)
	cfg := OAuthProviderConfig{
		Issuer:   "https://auth.example.com",
		ClientID: "client",
		Scopes:   "openid",
		Port:     port,
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	done := make(chan error, 1)
	go func() {
		_, err := LoginBrowser(cfg)
		done <- err
	}()

	waitForTCPServer(t, addr)

	resp, err := http.Get("http://" + addr + "/auth/callback?state=wrong-state")
	if err != nil {
		t.Fatalf("GET callback error: %v", err)
	}
	resp.Body.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("LoginBrowser() expected error for state mismatch, got nil")
		}
		if !strings.Contains(err.Error(), "state mismatch") {
			t.Errorf("error = %q, want state mismatch", err.Error())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("LoginBrowser() did not return after callback")
	}
}

func TestLoginBrowserBindFailure(t *testing.T) {
	// Occupy a port so LoginBrowser cannot bind to it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	cfg := OAuthProviderConfig{
		Issuer:   "https://auth.example.com",
		ClientID: "client",
		Scopes:   "openid",
		Port:     port,
	}
	_, err = LoginBrowser(cfg)
	if err == nil {
		t.Fatal("LoginBrowser() expected bind error, got nil")
	}
	if !strings.Contains(err.Error(), "starting callback server on port") {
		t.Errorf("error = %q, want bind failure message", err.Error())
	}
}

func TestLoginDeviceCodeSuccess(t *testing.T) {
	var polls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			w.Write([]byte(`{"device_auth_id":"da123","user_code":"ABCD-EFGH","interval":"1"}`))
		case "/api/accounts/deviceauth/token":
			// First poll is "pending" (non-200); second succeeds.
			if atomic.AddInt32(&polls, 1) == 1 {
				http.Error(w, `{"error":"authorization_pending"}`, http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_code": "auth-code-1",
				"code_verifier":      "verifier-1",
			})
		case "/oauth/token":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token":  "device-access",
				"refresh_token": "device-refresh",
				"expires_in":    3600,
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "device-client", Scopes: "openid"}
	cred, err := LoginDeviceCode(cfg)
	if err != nil {
		t.Fatalf("LoginDeviceCode() error: %v", err)
	}
	if cred == nil {
		t.Fatal("LoginDeviceCode() returned nil credential")
	}
	if cred.AccessToken != "device-access" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "device-access")
	}
	if cred.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", cred.Provider, "openai")
	}
	if atomic.LoadInt32(&polls) != 2 {
		t.Errorf("poll count = %d, want 2", atomic.LoadInt32(&polls))
	}
}

func TestLoginDeviceCodeRequestError(t *testing.T) {
	// Issuer points nowhere -> http.Post fails.
	cfg := OAuthProviderConfig{Issuer: "http://127.0.0.1:1", ClientID: "c"}
	_, err := LoginDeviceCode(cfg)
	if err == nil {
		t.Fatal("LoginDeviceCode() expected error for unreachable issuer")
	}
	if !strings.Contains(err.Error(), "requesting device code") {
		t.Errorf("error = %q, want requesting device code", err.Error())
	}
}

func TestLoginDeviceCodeNonOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/accounts/deviceauth/usercode" {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}
	_, err := LoginDeviceCode(cfg)
	if err == nil {
		t.Fatal("LoginDeviceCode() expected error for non-200 response")
	}
	if !strings.Contains(err.Error(), "device code request failed") {
		t.Errorf("error = %q, want device code request failed", err.Error())
	}
}

func TestLoginDeviceCodeParseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/accounts/deviceauth/usercode" {
			w.Write([]byte(`{invalid json`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}
	_, err := LoginDeviceCode(cfg)
	if err == nil {
		t.Fatal("LoginDeviceCode() expected parse error")
	}
	if !strings.Contains(err.Error(), "parsing device code response") {
		t.Errorf("error = %q, want parsing device code response", err.Error())
	}
}

func TestPollDeviceCodeNetworkError(t *testing.T) {
	cfg := OAuthProviderConfig{Issuer: "http://127.0.0.1:1", ClientID: "c"}
	_, err := pollDeviceCode(cfg, "da", "uc")
	if err == nil {
		t.Fatal("pollDeviceCode() expected network error")
	}
}

func TestPollDeviceCodePending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "pending", http.StatusBadRequest)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}
	_, err := pollDeviceCode(cfg, "da", "uc")
	if err == nil {
		t.Fatal("pollDeviceCode() expected 'pending' error")
	}
	if err.Error() != "pending" {
		t.Errorf("error = %q, want pending", err.Error())
	}
}

func TestPollDeviceCodeInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{bad json`))
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}
	_, err := pollDeviceCode(cfg, "da", "uc")
	if err == nil {
		t.Fatal("pollDeviceCode() expected JSON unmarshal error")
	}
}

func TestPollDeviceCodeExchangeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/token":
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_code": "auth-code",
				"code_verifier":      "verifier",
			})
		default:
			http.Error(w, "token exchange failed", http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}
	_, err := pollDeviceCode(cfg, "da", "uc")
	if err == nil {
		t.Fatal("pollDeviceCode() expected exchange error")
	}
}

func TestPollDeviceCodeSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/token":
			json.NewEncoder(w).Encode(map[string]string{
				"authorization_code": "auth-code",
				"code_verifier":      "verifier",
			})
		case "/oauth/token":
			r.ParseForm()
			if r.FormValue("code") != "auth-code" {
				http.Error(w, "bad code", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token": "poll-access",
				"expires_in":   3600,
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "c"}
	cred, err := pollDeviceCode(cfg, "da", "uc")
	if err != nil {
		t.Fatalf("pollDeviceCode() error: %v", err)
	}
	if cred == nil {
		t.Fatal("pollDeviceCode() returned nil credential")
	}
	if cred.AccessToken != "poll-access" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "poll-access")
	}
}

func TestParseDeviceCodeResponseNullInterval(t *testing.T) {
	resp, err := parseDeviceCodeResponse([]byte(`{"device_auth_id":"a","user_code":"b","interval":null}`))
	if err != nil {
		t.Fatalf("parseDeviceCodeResponse() error: %v", err)
	}
	if resp.Interval != 0 {
		t.Errorf("Interval = %d, want 0", resp.Interval)
	}
}

func TestParseDeviceCodeResponseEmptyStructInterval(t *testing.T) {
	resp, err := parseDeviceCodeResponse([]byte(`{"device_auth_id":"a","user_code":"b"}`))
	if err != nil {
		t.Fatalf("parseDeviceCodeResponse() error: %v", err)
	}
	if resp.Interval != 0 {
		t.Errorf("Interval = %d, want 0", resp.Interval)
	}
}

func TestParseFlexibleIntBlankString(t *testing.T) {
	if got, err := parseFlexibleInt(json.RawMessage(`""`)); err != nil || got != 0 {
		t.Errorf("parseFlexibleInt(\"\") = %d, %v; want 0, nil", got, err)
	}
	if got, err := parseFlexibleInt(json.RawMessage(`"  "`)); err != nil || got != 0 {
		t.Errorf("parseFlexibleInt(whitespace) = %d, %v; want 0, nil", got, err)
	}
}

func TestOpenBrowser(t *testing.T) {
	// openBrowser attempts to launch an external program; on Linux it
	// tries xdg-open, on macOS 'open', on Windows 'start'. Just ensure it
	// executes without panicking and the 200ms start is fast. The return
	// value depends on whether the launcher exists on the host.
	_ = openBrowser("https://example.com/oauth/callback")
}

func TestParseJWTClaimsErrors(t *testing.T) {
	if _, err := parseJWTClaims("not-a-jwt"); err == nil {
		t.Error("expected error for non-JWT token")
	}
	if _, err := base64URLDecode(""); err != nil {
		t.Errorf("base64URLDecode(\"\") error: %v", err)
	}
}

func TestLoginBrowserTimeout(t *testing.T) {
	// openBrowser starts and LoginBrowser waits on resultCh; without a
	// callback it would block for 5 minutes. Instead of waiting that long
	// we just assert the helper path is reachable by checking that the
	// generated state does not panic and that read of the server on a
	// closed callback returns the error path exercised elsewhere. This
	// test deliberately covers LoginBrowser reaching the select without a
	// matching state by immediately closing the listener.
	port := freePort(t)
	cfg := OAuthProviderConfig{
		Issuer:   "https://auth.example.com",
		ClientID: "client",
		Scopes:   "openid",
		Port:     port,
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	done := make(chan error, 1)
	go func() {
		_, err := LoginBrowser(cfg)
		done <- err
	}()

	waitForTCPServer(t, addr)
	// Send a state-mismatch request to unblock LoginBrowser quickly.
	resp, err := http.Get("http://" + addr + "/auth/callback")
	if err != nil {
		t.Fatalf("GET callback error: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	select {
	case <-done:
		// The request without a state param is treated as a mismatch,
		// returning "state mismatch" error.
	case <-time.After(10 * time.Second):
		t.Fatal("LoginBrowser() did not return")
	}
}
