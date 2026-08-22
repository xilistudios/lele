package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestLoginPasteToken(t *testing.T) {
	cred, err := LoginPasteToken("anthropic", strings.NewReader("sk-ant-abc123\n"))
	if err != nil {
		t.Fatalf("LoginPasteToken() error: %v", err)
	}
	if cred.AccessToken != "sk-ant-abc123" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "sk-ant-abc123")
	}
	if cred.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", cred.Provider, "anthropic")
	}
	if cred.AuthMethod != "token" {
		t.Errorf("AuthMethod = %q, want %q", cred.AuthMethod, "token")
	}
}

func TestLoginPasteTokenTrimsWhitespace(t *testing.T) {
	cred, err := LoginPasteToken("openai", strings.NewReader("  sk-openai-xyz  \n"))
	if err != nil {
		t.Fatalf("LoginPasteToken() error: %v", err)
	}
	if cred.AccessToken != "sk-openai-xyz" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "sk-openai-xyz")
	}
}

func TestLoginPasteTokenEmpty(t *testing.T) {
	_, err := LoginPasteToken("openai", strings.NewReader("   \n"))
	if err == nil {
		t.Fatal("LoginPasteToken() expected error for empty token")
	}
	if !strings.Contains(err.Error(), "token cannot be empty") {
		t.Errorf("error = %q, want empty token error", err.Error())
	}
}

func TestLoginPasteTokenNoInput(t *testing.T) {
	_, err := LoginPasteToken("anthropic", strings.NewReader(""))
	if err == nil {
		t.Fatal("LoginPasteToken() expected error for no input")
	}
	if !strings.Contains(err.Error(), "no input") {
		t.Errorf("error = %q, want no input error", err.Error())
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failure") }

func TestLoginPasteTokenReadError(t *testing.T) {
	_, err := LoginPasteToken("openai", errReader{})
	if err == nil {
		t.Fatal("LoginPasteToken() expected error for read failure")
	}
	if !strings.Contains(err.Error(), "reading token") {
		t.Errorf("error = %q, want reading token error", err.Error())
	}
}

func TestProviderDisplayName(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "console.anthropic.com"},
		{"openai", "platform.openai.com"},
		{"unknown-provider", "unknown-provider"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := providerDisplayName(tt.provider); got != tt.want {
			t.Errorf("providerDisplayName(%q) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}
