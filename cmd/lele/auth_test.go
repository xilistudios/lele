package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/config"
)

func stubLoginExit(t *testing.T) *[]int {
	t.Helper()
	codes := []int{}
	original := loginExit
	loginExit = func(code int) { codes = append(codes, code) }
	t.Cleanup(func() { loginExit = original })
	return &codes
}

func withLoginArgs(t *testing.T, args ...string) {
	t.Helper()
	original := os.Args
	os.Args = append([]string{"lele", "auth", "login"}, args...)
	t.Cleanup(func() { os.Args = original })
}

func assertExitCodes(t *testing.T, want []int, got []int) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loginExit called with %v, want %v", got, want)
	}
}

func TestParseAuthSubcommand_Login(t *testing.T) {
	sub := parseAuthSubcommand([]string{"login"})
	if sub != "login" {
		t.Errorf("subcommand = %q, want %q", sub, "login")
	}
}

func TestParseAuthSubcommand_Logout(t *testing.T) {
	sub := parseAuthSubcommand([]string{"logout"})
	if sub != "logout" {
		t.Errorf("subcommand = %q, want %q", sub, "logout")
	}
}

func TestParseAuthSubcommand_Status(t *testing.T) {
	sub := parseAuthSubcommand([]string{"status"})
	if sub != "status" {
		t.Errorf("subcommand = %q, want %q", sub, "status")
	}
}

func TestParseAuthSubcommand_Unknown(t *testing.T) {
	sub := parseAuthSubcommand([]string{"foobar"})
	if sub != "foobar" {
		t.Errorf("subcommand = %q, want %q", sub, "foobar")
	}
}

func TestParseAuthSubcommand_Empty(t *testing.T) {
	sub := parseAuthSubcommand([]string{})
	if sub != "" {
		t.Errorf("subcommand = %q, want empty", sub)
	}
}

func TestParseLoginArgs_ProviderLong(t *testing.T) {
	got := parseLoginArgs([]string{"--provider", "openai"})
	if got.Provider != "openai" {
		t.Errorf("provider = %q, want %q", got.Provider, "openai")
	}
	if got.DeviceCode {
		t.Error("deviceCode = true, want false")
	}
	if got.PasteCode {
		t.Error("pasteCode = true, want false")
	}
}

func TestParseLoginArgs_ProviderShort(t *testing.T) {
	got := parseLoginArgs([]string{"-p", "anthropic"})
	if got.Provider != "anthropic" {
		t.Errorf("provider = %q, want %q", got.Provider, "anthropic")
	}
}

func TestParseLoginArgs_DeviceCode(t *testing.T) {
	got := parseLoginArgs([]string{"--device-code"})
	if !got.DeviceCode {
		t.Error("deviceCode = false, want true")
	}
}

func TestParseLoginArgs_DeviceCodeWithProvider(t *testing.T) {
	got := parseLoginArgs([]string{
		"--provider", "openai", "--device-code",
	})
	if got.Provider != "openai" {
		t.Errorf("provider = %q, want %q", got.Provider, "openai")
	}
	if !got.DeviceCode {
		t.Error("deviceCode = false, want true")
	}
}

func TestParseLoginArgs_PasteCode(t *testing.T) {
	got := parseLoginArgs([]string{"--provider", "claude", "--paste-code"})
	if got.Provider != "claude" {
		t.Errorf("provider = %q, want %q", got.Provider, "claude")
	}
	if !got.PasteCode {
		t.Error("pasteCode = false, want true")
	}
	if got.DeviceCode {
		t.Error("deviceCode = true, want false")
	}
}

func TestParseLoginArgs_AllFlags(t *testing.T) {
	got := parseLoginArgs([]string{
		"--provider", "openai", "--device-code", "--paste-code",
	})
	if got.Provider != "openai" {
		t.Errorf("provider = %q, want %q", got.Provider, "openai")
	}
	if !got.DeviceCode {
		t.Error("deviceCode = false, want true")
	}
	if !got.PasteCode {
		t.Error("pasteCode = false, want true")
	}
}

func TestParseLoginArgs_Empty(t *testing.T) {
	got := parseLoginArgs([]string{})
	if got.Provider != "" {
		t.Errorf("provider = %q, want empty", got.Provider)
	}
	if got.DeviceCode {
		t.Error("deviceCode = true, want false")
	}
	if got.PasteCode {
		t.Error("pasteCode = true, want false")
	}
}

func TestParseLoginArgs_ProviderMissingValue(t *testing.T) {
	got := parseLoginArgs([]string{"--provider"})
	if got.Provider != "" {
		t.Errorf("provider = %q, want empty (no value to consume)", got.Provider)
	}
}

func TestParseLoginArgs_ProviderShortMissingValue(t *testing.T) {
	got := parseLoginArgs([]string{"-p"})
	if got.Provider != "" {
		t.Errorf("provider = %q, want empty (no value to consume)", got.Provider)
	}
}

func TestParseLoginArgs_MultipleProviders(t *testing.T) {
	got := parseLoginArgs([]string{
		"--provider", "openai", "--provider", "anthropic",
	})
	if got.Provider != "anthropic" {
		t.Errorf("provider = %q, want %q", got.Provider, "anthropic")
	}
}

func TestParseLoginArgs_DeviceCodeBeforeProvider(t *testing.T) {
	got := parseLoginArgs([]string{
		"--device-code", "--provider", "openai",
	})
	if got.Provider != "openai" {
		t.Errorf("provider = %q, want %q", got.Provider, "openai")
	}
	if !got.DeviceCode {
		t.Error("deviceCode = false, want true")
	}
}

func TestParseLoginArgs_UnknownFlagsIgnored(t *testing.T) {
	got := parseLoginArgs([]string{"--unknown", "value"})
	if got.Provider != "" {
		t.Errorf("provider = %q, want empty", got.Provider)
	}
	if got.DeviceCode {
		t.Error("deviceCode = true, want false")
	}
	if got.PasteCode {
		t.Error("pasteCode = true, want false")
	}
}

func TestParseLogoutArgs_ProviderLong(t *testing.T) {
	provider := parseLogoutArgs([]string{"--provider", "openai"})
	if provider != "openai" {
		t.Errorf("provider = %q, want %q", provider, "openai")
	}
}

func TestParseLogoutArgs_ProviderShort(t *testing.T) {
	provider := parseLogoutArgs([]string{"-p", "anthropic"})
	if provider != "anthropic" {
		t.Errorf("provider = %q, want %q", provider, "anthropic")
	}
}

func TestParseLogoutArgs_Empty(t *testing.T) {
	provider := parseLogoutArgs([]string{})
	if provider != "" {
		t.Errorf("provider = %q, want empty", provider)
	}
}

func TestParseLogoutArgs_ProviderMissingValue(t *testing.T) {
	provider := parseLogoutArgs([]string{"--provider"})
	if provider != "" {
		t.Errorf("provider = %q, want empty (no value to consume)", provider)
	}
}

func TestParseLogoutArgs_ProviderShortMissingValue(t *testing.T) {
	provider := parseLogoutArgs([]string{"-p"})
	if provider != "" {
		t.Errorf("provider = %q, want empty (no value to consume)", provider)
	}
}

func TestParseLogoutArgs_UnknownFlagIgnored(t *testing.T) {
	provider := parseLogoutArgs([]string{"--unknown", "value"})
	if provider != "" {
		t.Errorf("provider = %q, want empty", provider)
	}
}

func TestResolveLoginProvider(t *testing.T) {
	tests := []struct {
		name          string
		provider      string
		useDeviceCode bool
		wantKind      loginKind
		wantCfgName   string
		wantErr       string
	}{
		{
			name:        "openai browser flow is unchanged",
			provider:    "openai",
			wantKind:    loginKindOpenAI,
			wantCfgName: "openai",
		},
		{
			name:          "openai device code flow is allowed",
			provider:      "openai",
			useDeviceCode: true,
			wantKind:      loginKindOpenAI,
			wantCfgName:   "openai",
		},
		{
			name:        "alias codex resolves to openai",
			provider:    "codex",
			wantKind:    loginKindOpenAI,
			wantCfgName: "openai",
		},
		{
			name:        "provider names are normalized",
			provider:    "  OPENAI ",
			wantKind:    loginKindOpenAI,
			wantCfgName: "openai",
		},
		{
			name:        "anthropic keeps the paste-token flow",
			provider:    "anthropic",
			wantKind:    loginKindPasteToken,
			wantCfgName: "",
		},
		{
			name:        "uppercase anthropic pastes token",
			provider:    "  ANTHROPIC ",
			wantKind:    loginKindPasteToken,
			wantCfgName: "",
		},
		{
			name:        "claude -> claude oauth",
			provider:    "claude",
			wantKind:    loginKindClaudeOAuth,
			wantCfgName: "anthropic",
		},
		{
			name:        "CLAUDE normalized",
			provider:    "  CLAUDE ",
			wantKind:    loginKindClaudeOAuth,
			wantCfgName: "anthropic",
		},
		{
			name:        "gemini -> gemini oauth",
			provider:    "gemini",
			wantKind:    loginKindGeminiOAuth,
			wantCfgName: "gemini",
		},
		{
			name:        "gemini-cli alias normalizes",
			provider:    "  GEMINI-CLI ",
			wantKind:    loginKindGeminiOAuth,
			wantCfgName: "gemini",
		},
		{
			name:        "google-antigravity uses the browser flow with its preset",
			provider:    "google-antigravity",
			wantKind:    loginKindBrowserPreset,
			wantCfgName: "google-antigravity",
		},
		{
			name:        "alias antigravity resolves to google-antigravity",
			provider:    "antigravity",
			wantKind:    loginKindBrowserPreset,
			wantCfgName: "google-antigravity",
		},
		{
			name:        "normalization also applies to aliases",
			provider:    " AntiGravity ",
			wantKind:    loginKindBrowserPreset,
			wantCfgName: "google-antigravity",
		},
		{
			name:     "unknown provider falls back to no flow",
			provider: "grok",
			wantKind: loginKindUnsupported,
			wantErr: "Unsupported provider: grok\nSupported providers: " +
				supportedProviders(),
		},
		{
			name:     "empty provider falls back to no flow",
			provider: "",
			wantKind: loginKindUnsupported,
			wantErr: "Unsupported provider: \nSupported providers: " +
				supportedProviders(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kind, cfg, err := resolveLoginProvider(tc.provider, tc.useDeviceCode)

			if kind != tc.wantKind {
				t.Errorf("resolveLoginProvider(%q, %v) kind = %v, want %v", tc.provider, tc.useDeviceCode, kind, tc.wantKind)
			}
			if cfg.Name != tc.wantCfgName {
				t.Errorf("resolveLoginProvider(%q, %v) cfg.Name = %q, want %q", tc.provider, tc.useDeviceCode, cfg.Name, tc.wantCfgName)
			}

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("resolveLoginProvider(%q, %v) error = %v, want nil", tc.provider, tc.useDeviceCode, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("resolveLoginProvider(%q, %v) error = nil, want %q", tc.provider, tc.useDeviceCode, tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Errorf("resolveLoginProvider(%q, %v) error = %q, want %q", tc.provider, tc.useDeviceCode, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestResolveLoginProviderUnsupportedProviderKeepsName(t *testing.T) {
	_, _, err := resolveLoginProvider("GROK", false)
	if err == nil {
		t.Fatal("resolveLoginProvider(\"GROK\", false) error = nil, want an unsupported provider error")
	}
	if !strings.HasPrefix(err.Error(), "Unsupported provider: GROK\n") {
		t.Errorf("error = %q, want it to start with %q", err.Error(), "Unsupported provider: GROK\n")
	}
}

func TestResolveLoginProviderDeviceCodeUnavailable(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"google-antigravity", "device code flow is not available for google-antigravity"},
		{"antigravity", "device code flow is not available for google-antigravity"},
		{" AntiGravity ", "device code flow is not available for google-antigravity"},
		{"claude", "device code flow is not available for anthropic"},
		{"anthropic", "device code flow is not available for anthropic"},
		{"  ANTHROPIC ", "device code flow is not available for anthropic"},
		{"gemini", "device code flow is not available for gemini"},
		{"gemini-cli", "device code flow is not available for gemini"},
	}

	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			kind, cfg, err := resolveLoginProvider(tc.provider, true)

			if kind != loginKindUnsupported {
				t.Errorf("resolveLoginProvider(%q, true) kind = %v, want loginKindUnsupported", tc.provider, kind)
			}
			if cfg.Name != "" {
				t.Errorf("resolveLoginProvider(%q, true) cfg.Name = %q, want empty on failure", tc.provider, cfg.Name)
			}
			if err == nil {
				t.Fatalf("resolveLoginProvider(%q, true) error = nil, want %q", tc.provider, tc.want)
			}
			if err.Error() != tc.want {
				t.Errorf("resolveLoginProvider(%q, true) error = %q, want %q", tc.provider, err.Error(), tc.want)
			}
		})
	}
}

func TestCanonicalProviderName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"claude alias resolves to anthropic", "claude", "anthropic"},
		{"claude alias is normalized", " CLAUDE ", "anthropic"},
		{"codex alias resolves to openai", "codex", "openai"},
		{"antigravity alias resolves to google-antigravity", "antigravity", "google-antigravity"},
		{"gemini-cli alias resolves to gemini", "gemini-cli", "gemini"},
		{"openai stays openai", "openai", "openai"},
		{"unknown name is normalized but unresolved", "grok", "grok"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := canonicalProviderName(tc.in); got != tc.want {
				t.Errorf("canonicalProviderName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSupportedProvidersIsDerivedFromRegistry(t *testing.T) {
	names := auth.OAuthProviderNames()
	if len(names) < 2 {
		t.Fatalf("auth.OAuthProviderNames() = %v, want at least 2 presets for this test to be meaningful", names)
	}

	got := supportedProviders()
	for _, name := range names {
		if !strings.Contains(got, name) {
			t.Errorf("supportedProviders() = %q is missing registry name %q", got, name)
		}
	}
	if want := strings.Join(names, ", "); !strings.Contains(got, want) {
		t.Errorf("supportedProviders() = %q does not embed the registry list as returned (%q)", got, want)
	}
	if !strings.Contains(got, "anthropic") {
		t.Errorf("supportedProviders() = %q does not mention the paste-token provider anthropic", got)
	}
}

func TestSupportedProvidersListsAnthropicOnce(t *testing.T) {
	got := supportedProviders()
	if n := strings.Count(got, "anthropic"); n != 1 {
		t.Errorf("supportedProviders() = %q mentions anthropic %d times, want exactly 1", got, n)
	}
}

func TestUnsupportedProviderErrorListsRegistryNames(t *testing.T) {
	err := unsupportedProviderError("grok")
	if err == nil {
		t.Fatal("unsupportedProviderError(\"grok\") = nil, want an error")
	}

	lines := strings.Split(err.Error(), "\n")
	if len(lines) != 2 {
		t.Fatalf("unsupportedProviderError(\"grok\") = %q, want exactly 2 lines", err.Error())
	}
	if lines[0] != "Unsupported provider: grok" {
		t.Errorf("first line = %q, want %q", lines[0], "Unsupported provider: grok")
	}
	if lines[1] != "Supported providers: "+supportedProviders() {
		t.Errorf("second line = %q, want %q", lines[1], "Supported providers: "+supportedProviders())
	}
	for _, name := range auth.OAuthProviderNames() {
		if !strings.Contains(lines[1], name) {
			t.Errorf("supported providers line %q is missing registry name %q", lines[1], name)
		}
	}
	if lines[1] == "Supported providers: openai, anthropic" {
		t.Errorf("supported providers line %q is the legacy hardcoded list; it must be derived from auth.OAuthProviderNames()", lines[1])
	}
}

func TestAuthHelpDocumentsEveryRegistryProvider(t *testing.T) {
	out := captureOutput(authHelp)

	for _, name := range auth.OAuthProviderNames() {
		if !strings.Contains(out, name) {
			t.Errorf("authHelp() output is missing registry provider %q:\n%s", name, out)
		}
	}
	for _, want := range []string{
		"antigravity   (alias of google-antigravity)",
		"anthropic",
		"--device-code",
		"--paste-code",
		"lele auth login --provider claude",
		"lele auth login --provider claude --paste-code",
		"lele auth login --provider gemini",
		"lele auth login --provider gemini --paste-code",
		"gemini-cli   (alias of gemini)",
		"  --provider claude    Claude OAuth (alias of the anthropic preset; use --provider anthropic to paste a token)",
		"  --provider codex     OpenAI OAuth (alias of the openai preset; same flow as --provider openai)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("authHelp() output is missing %q:\n%s", want, out)
		}
	}
}

func TestAuthLoginCmdUnsupportedProviderPrintsRegistryList(t *testing.T) {
	codes := stubLoginExit(t)
	withLoginArgs(t, "--provider", "grok")

	out := captureOutput(authLoginCmd)

	if !strings.Contains(out, "Unsupported provider: grok") {
		t.Errorf("authLoginCmd() output = %q, want it to report the unsupported provider", out)
	}
	for _, name := range auth.OAuthProviderNames() {
		if !strings.Contains(out, name) {
			t.Errorf("authLoginCmd() output %q is missing registry provider %q", out, name)
		}
	}
	assertExitCodes(t, []int{1}, *codes)
}

func TestAuthLoginCmdMissingProvider(t *testing.T) {
	codes := stubLoginExit(t)
	withLoginArgs(t)

	out := captureOutput(authLoginCmd)

	if !strings.Contains(out, "Error: --provider is required") {
		t.Errorf("authLoginCmd() output = %q, want the required-provider error", out)
	}
	if !strings.Contains(out, "Supported providers: "+supportedProviders()) {
		t.Errorf("authLoginCmd() output = %q, want the registry-derived provider list", out)
	}
	assertExitCodes(t, []int{1}, *codes)
}

func TestAuthLoginCmdMissingProviderExitsNonZero(t *testing.T) {
	codes := stubLoginExit(t)
	withLoginArgs(t)

	out := captureOutput(authLoginCmd)

	want := "Error: --provider is required\nSupported providers: " + supportedProviders() + "\n"
	if out != want {
		t.Errorf("authLoginCmd() output = %q, want %q", out, want)
	}
	assertExitCodes(t, []int{1}, *codes)
}

func TestAuthLoginCmdUnsupportedProviderExitsNonZero(t *testing.T) {
	codes := stubLoginExit(t)
	withLoginArgs(t, "--provider", "grok")

	out := captureOutput(authLoginCmd)

	want := "Unsupported provider: grok\nSupported providers: " + supportedProviders() + "\n"
	if out != want {
		t.Errorf("authLoginCmd() output = %q, want %q", out, want)
	}
	assertExitCodes(t, []int{1}, *codes)
}

func TestAuthLoginCmdDeviceCodeRejectionExitsNonZero(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		want     string
	}{
		{
			name:     "registry browser preset",
			provider: "antigravity",
			want:     "device code flow is not available for google-antigravity\n",
		},
		{
			name:     "anthropic intercept",
			provider: "anthropic",
			want:     "device code flow is not available for anthropic\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			codes := stubLoginExit(t)
			withLoginArgs(t, "--provider", tc.provider, "--device-code")

			originalLoginBrowser, originalPasteToken := loginBrowser, pasteToken
			loginBrowser = func(auth.OAuthProviderConfig) (*auth.AuthCredential, error) {
				return nil, errors.New("unexpected browser flow")
			}
			pasteToken = func(string, io.Reader) (*auth.AuthCredential, error) {
				return nil, errors.New("unexpected paste-token flow")
			}
			t.Cleanup(func() {
				loginBrowser, pasteToken = originalLoginBrowser, originalPasteToken
			})

			out := captureOutput(authLoginCmd)

			if out != tc.want {
				t.Errorf("authLoginCmd() output = %q, want %q", out, tc.want)
			}
			assertExitCodes(t, []int{1}, *codes)
		})
	}
}

func TestAuthLoginCmdPassesNormalizedNameToPasteToken(t *testing.T) {
	tests := []struct {
		name string
		arg  string
	}{
		{name: "canonical spelling", arg: "anthropic"},
		{name: "uppercase with padding", arg: "  ANTHROPIC "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			codes := stubLoginExit(t)
			withLoginArgs(t, "--provider", tc.arg)

			originalPasteToken := pasteToken
			var gotProvider string
			pasteToken = func(provider string, _ io.Reader) (*auth.AuthCredential, error) {
				gotProvider = provider
				return nil, errors.New("stop before storing")
			}
			t.Cleanup(func() { pasteToken = originalPasteToken })

			out := captureOutput(authLoginCmd)

			if gotProvider != "anthropic" {
				t.Errorf("pasteToken received provider %q, want %q", gotProvider, "anthropic")
			}
			want := "Login failed: stop before storing\n"
			if out != want {
				t.Errorf("authLoginCmd() output = %q, want %q", out, want)
			}
			assertExitCodes(t, []int{1}, *codes)
		})
	}
}

func TestAuthLoginBrowserPresetStoresCanonicalKey(t *testing.T) {
	for _, provider := range []string{"google-antigravity", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			cfg, ok := auth.OAuthProviderConfigByName(provider)
			if !ok {
				t.Fatalf("auth.OAuthProviderConfigByName(%q) not found", provider)
			}
			if cfg.Name != "google-antigravity" {
				t.Fatalf("registry cfg.Name = %q, want %q", cfg.Name, "google-antigravity")
			}

			originalLoginBrowser, originalSetCredential := loginBrowser, setCredential
			t.Cleanup(func() {
				loginBrowser, setCredential = originalLoginBrowser, originalSetCredential
			})

			var gotCfg auth.OAuthProviderConfig
			cred := &auth.AuthCredential{AccessToken: "tok", Provider: cfg.Name, AccountID: "acct-42"}
			loginBrowser = func(c auth.OAuthProviderConfig) (*auth.AuthCredential, error) {
				gotCfg = c
				return cred, nil
			}

			var gotKey string
			var gotCred *auth.AuthCredential
			setCredential = func(name string, c *auth.AuthCredential) error {
				gotKey, gotCred = name, c
				return nil
			}

			out := captureOutput(func() { authLoginBrowserPreset(cfg) })

			if gotCfg.Name != cfg.Name {
				t.Errorf("loginBrowser received cfg.Name = %q, want %q", gotCfg.Name, cfg.Name)
			}
			if gotKey != "google-antigravity" {
				t.Errorf("setCredential key = %q, want %q", gotKey, "google-antigravity")
			}
			if gotCred != cred {
				t.Errorf("setCredential credential = %p, want %p", gotCred, cred)
			}
			for _, want := range []string{"Login successful!", "Account: acct-42"} {
				if !strings.Contains(out, want) {
					t.Errorf("authLoginBrowserPreset() output = %q, want it to contain %q", out, want)
				}
			}
		})
	}
}

func TestAuthLoginBrowserPresetPropagatesErrors(t *testing.T) {
	cfg, ok := auth.OAuthProviderConfigByName("antigravity")
	if !ok {
		t.Fatal("auth.OAuthProviderConfigByName(\"antigravity\") not found")
	}

	t.Run("login flow fails", func(t *testing.T) {
		codes := stubLoginExit(t)

		originalLoginBrowser, originalSetCredential := loginBrowser, setCredential
		t.Cleanup(func() {
			loginBrowser, setCredential = originalLoginBrowser, originalSetCredential
		})

		loginBrowser = func(auth.OAuthProviderConfig) (*auth.AuthCredential, error) {
			return nil, errors.New("oauth exploded")
		}

		setCredentialCalled := false
		setCredential = func(string, *auth.AuthCredential) error {
			setCredentialCalled = true
			return nil
		}

		out := captureOutput(func() { authLoginBrowserPreset(cfg) })

		want := "Login failed: oauth exploded\n"
		if out != want {
			t.Errorf("authLoginBrowserPreset() output = %q, want %q", out, want)
		}
		if setCredentialCalled {
			t.Error("setCredential was called after a failed login, want it to be skipped")
		}
		assertExitCodes(t, []int{1}, *codes)
	})

	t.Run("saving credentials fails", func(t *testing.T) {
		codes := stubLoginExit(t)

		originalLoginBrowser, originalSetCredential := loginBrowser, setCredential
		t.Cleanup(func() {
			loginBrowser, setCredential = originalLoginBrowser, originalSetCredential
		})

		loginBrowser = func(auth.OAuthProviderConfig) (*auth.AuthCredential, error) {
			return &auth.AuthCredential{AccessToken: "tok"}, nil
		}
		setCredential = func(string, *auth.AuthCredential) error {
			return errors.New("store read-only")
		}

		out := captureOutput(func() { authLoginBrowserPreset(cfg) })

		want := "Failed to save credentials: store read-only\n"
		if out != want {
			t.Errorf("authLoginBrowserPreset() output = %q, want %q", out, want)
		}
		assertExitCodes(t, []int{1}, *codes)
	})
}

func TestAuthLoginClaudeOAuthStoresCanonicalKeyAndWritesAuthMethod(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())

	cfg, ok := auth.OAuthProviderConfigByName("claude")
	if !ok {
		t.Fatal("auth.OAuthProviderConfigByName(\"claude\") not found")
	}
	if cfg.Name != "anthropic" {
		t.Fatalf("registry cfg.Name = %q, want %q", cfg.Name, "anthropic")
	}

	originalLoginClaudeBrowser, originalSetCredential := loginClaudeBrowser, setCredential
	t.Cleanup(func() {
		loginClaudeBrowser, setCredential = originalLoginClaudeBrowser, originalSetCredential
	})

	var gotCfg auth.OAuthProviderConfig
	cred := &auth.AuthCredential{AccessToken: "tok", Provider: cfg.Name, AccountID: "acct-9"}
	loginClaudeBrowser = func(c auth.OAuthProviderConfig, _ io.Reader) (*auth.AuthCredential, error) {
		gotCfg = c
		return cred, nil
	}

	var gotKey string
	var gotCred *auth.AuthCredential
	setCredential = func(name string, c *auth.AuthCredential) error {
		gotKey, gotCred = name, c
		return nil
	}

	out := captureOutput(func() { authLoginClaudeOAuth(cfg, false) })

	if gotCfg.Name != cfg.Name {
		t.Errorf("loginClaudeBrowser received cfg.Name = %q, want %q", gotCfg.Name, cfg.Name)
	}
	if gotKey != "anthropic" {
		t.Errorf("setCredential key = %q, want %q", gotKey, "anthropic")
	}
	if gotCred != cred {
		t.Errorf("setCredential credential = %p, want %p", gotCred, cred)
	}
	for _, want := range []string{"Login successful!", "Account: acct-9"} {
		if !strings.Contains(out, want) {
			t.Errorf("authLoginClaudeOAuth() output = %q, want it to contain %q", out, want)
		}
	}
	if strings.Contains(out, "Warning: could not update config:") {
		t.Errorf("authLoginClaudeOAuth() output = %q, want no config-write warning", out)
	}

	if _, err := os.Stat(getConfigPath()); err != nil {
		t.Errorf("stat(%q) = %v, want the config file to have been written", getConfigPath(), err)
	}
	persisted, err := config.LoadConfig(getConfigPath())
	if err != nil {
		t.Fatalf("LoadConfig(%q) = %v, want the persisted config", getConfigPath(), err)
	}
	if persisted.Providers == nil {
		t.Fatal("persisted.Providers = nil, want the providers section with auth_method")
	}
	if got := persisted.Providers.Anthropic.AuthMethod; got != "oauth" {
		t.Errorf("persisted Providers.Anthropic.AuthMethod = %q, want %q", got, "oauth")
	}
}

func TestAuthLoginClaudeOAuthPasteCode(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())

	cfg, ok := auth.OAuthProviderConfigByName("claude")
	if !ok {
		t.Fatal("auth.OAuthProviderConfigByName(\"claude\") not found")
	}

	originalPasteCode, originalBrowser, originalSetCredential := loginClaudePasteCode, loginClaudeBrowser, setCredential
	t.Cleanup(func() {
		loginClaudePasteCode, loginClaudeBrowser, setCredential = originalPasteCode, originalBrowser, originalSetCredential
	})

	pasteCodeCalled := false
	loginClaudePasteCode = func(auth.OAuthProviderConfig, io.Reader) (*auth.AuthCredential, error) {
		pasteCodeCalled = true
		return &auth.AuthCredential{AccessToken: "tok"}, nil
	}
	loginClaudeBrowser = func(auth.OAuthProviderConfig, io.Reader) (*auth.AuthCredential, error) {
		return nil, errors.New("browser flow must not run with --paste-code")
	}
	setCredential = func(string, *auth.AuthCredential) error { return nil }

	out := captureOutput(func() { authLoginClaudeOAuth(cfg, true) })

	if !pasteCodeCalled {
		t.Error("authLoginClaudeOAuth(cfg, true) did not route to loginClaudePasteCode")
	}
	if !strings.Contains(out, "Login successful!") {
		t.Errorf("authLoginClaudeOAuth() output = %q, want it to contain %q", out, "Login successful!")
	}
}

func TestAuthLoginClaudeOAuthConfigWriteFailureIsActionable(t *testing.T) {
	base := t.TempDir()
	configDir := filepath.Join(base, "cfg")
	if err := os.Symlink(filepath.Join(base, "missing-dir"), configDir); err != nil {
		t.Fatalf("os.Symlink(%q) error = %v", configDir, err)
	}
	t.Setenv("LELE_CONFIG_DIR", configDir)

	cfg, ok := auth.OAuthProviderConfigByName("claude")
	if !ok {
		t.Fatal("auth.OAuthProviderConfigByName(\"claude\") not found")
	}

	originalBrowser, originalSetCredential := loginClaudeBrowser, setCredential
	t.Cleanup(func() {
		loginClaudeBrowser, setCredential = originalBrowser, originalSetCredential
	})

	loginClaudeBrowser = func(auth.OAuthProviderConfig, io.Reader) (*auth.AuthCredential, error) {
		return &auth.AuthCredential{AccessToken: "tok", Provider: cfg.Name}, nil
	}
	setCredential = func(string, *auth.AuthCredential) error { return nil }

	out := captureOutput(func() { authLoginClaudeOAuth(cfg, false) })

	if !strings.HasPrefix(out, "Warning: could not update config: mkdir ") {
		t.Errorf("authLoginClaudeOAuth() output = %q, want it to start with the config-write warning", out)
	}
	wantAction := "Set providers.anthropic.auth_method = \"oauth\" in " + getConfigPath() +
		" for the Claude OAuth login to be used.\n"
	if !strings.Contains(out, "\n"+wantAction) {
		t.Errorf("authLoginClaudeOAuth() output = %q, want it to contain %q on its own line after the warning", out, wantAction)
	}
	if !strings.HasSuffix(out, "Login successful!\n") {
		t.Errorf("authLoginClaudeOAuth() output = %q, want it to end with %q", out, "Login successful!\n")
	}
}

func TestAuthLoginCmdPasteCodeRejectedForNonClaude(t *testing.T) {
	codes := stubLoginExit(t)
	withLoginArgs(t, "--provider", "antigravity", "--paste-code")

	originalLoginBrowser := loginBrowser
	loginBrowser = func(auth.OAuthProviderConfig) (*auth.AuthCredential, error) {
		return nil, errors.New("unexpected browser flow")
	}
	t.Cleanup(func() { loginBrowser = originalLoginBrowser })

	out := captureOutput(authLoginCmd)

	want := "--paste-code is only supported for the Claude and Gemini OAuth flows; use: lele auth login --provider claude --paste-code\n"
	if out != want {
		t.Errorf("authLoginCmd() output = %q, want %q", out, want)
	}
	assertExitCodes(t, []int{1}, *codes)
}

func TestAuthLoginGeminiOAuthStoresCanonicalKeyAndWritesAuthMethod(t *testing.T) {
	tests := []struct {
		name     string
		provider string
	}{
		{name: "canonical spelling", provider: "gemini"},
		{name: "gemini-cli alias", provider: "gemini-cli"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LELE_CONFIG_DIR", t.TempDir())

			codes := stubLoginExit(t)
			withLoginArgs(t, "--provider", tc.provider)

			cfg, ok := auth.OAuthProviderConfigByName(tc.provider)
			if !ok {
				t.Fatalf("auth.OAuthProviderConfigByName(%q) not found", tc.provider)
			}
			if cfg.Name != "gemini" {
				t.Fatalf("registry cfg.Name = %q, want %q", cfg.Name, "gemini")
			}

			originalGeminiBrowser, originalLoginBrowser, originalSetCredential := loginGeminiBrowser, loginBrowser, setCredential
			t.Cleanup(func() {
				loginGeminiBrowser, loginBrowser, setCredential = originalGeminiBrowser, originalLoginBrowser, originalSetCredential
			})

			var gotCfg auth.OAuthProviderConfig
			cred := &auth.AuthCredential{AccessToken: "tok", Provider: cfg.Name, AccountID: "acct-gemini"}
			loginGeminiBrowser = func(c auth.OAuthProviderConfig, _ io.Reader) (*auth.AuthCredential, error) {
				gotCfg = c
				return cred, nil
			}
			loginBrowser = func(auth.OAuthProviderConfig) (*auth.AuthCredential, error) {
				return nil, errors.New("unexpected generic browser-preset flow")
			}

			var gotKey string
			var gotCred *auth.AuthCredential
			setCredential = func(name string, c *auth.AuthCredential) error {
				gotKey, gotCred = name, c
				return nil
			}

			out := captureOutput(authLoginCmd)

			if gotCfg.Name != cfg.Name {
				t.Errorf("loginGeminiBrowser received cfg.Name = %q, want %q", gotCfg.Name, cfg.Name)
			}
			if gotKey != "gemini" {
				t.Errorf("setCredential key = %q, want %q", gotKey, "gemini")
			}
			if gotCred != cred {
				t.Errorf("setCredential credential = %p, want %p", gotCred, cred)
			}
			for _, want := range []string{"Login successful!", "Account: acct-gemini"} {
				if !strings.Contains(out, want) {
					t.Errorf("authLoginCmd() output = %q, want it to contain %q", out, want)
				}
			}
			if strings.Contains(out, "Warning: could not update config:") {
				t.Errorf("authLoginCmd() output = %q, want no config-write warning", out)
			}
			if len(*codes) != 0 {
				t.Errorf("loginExit called with %v, want no exit on the success path", *codes)
			}

			if _, err := os.Stat(getConfigPath()); err != nil {
				t.Errorf("stat(%q) = %v, want the config file to have been written", getConfigPath(), err)
			}
			persisted, err := config.LoadConfig(getConfigPath())
			if err != nil {
				t.Fatalf("LoadConfig(%q) = %v, want the persisted config", getConfigPath(), err)
			}
			if persisted.Providers == nil {
				t.Fatal("persisted.Providers = nil, want the providers section with auth_method")
			}
			if got := persisted.Providers.Gemini.AuthMethod; got != "oauth" {
				t.Errorf("persisted Providers.Gemini.AuthMethod = %q, want %q", got, "oauth")
			}
		})
	}
}

func TestAuthLoginGeminiOAuthPasteCode(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())

	codes := stubLoginExit(t)
	withLoginArgs(t, "--provider", "gemini", "--paste-code")

	cfg, ok := auth.OAuthProviderConfigByName("gemini")
	if !ok {
		t.Fatal("auth.OAuthProviderConfigByName(\"gemini\") not found")
	}

	originalPasteCode, originalGeminiBrowser, originalLoginBrowser, originalSetCredential := loginGeminiPasteCode, loginGeminiBrowser, loginBrowser, setCredential
	t.Cleanup(func() {
		loginGeminiPasteCode, loginGeminiBrowser, loginBrowser, setCredential = originalPasteCode, originalGeminiBrowser, originalLoginBrowser, originalSetCredential
	})

	var gotCfgName string
	pasteCodeCalled := false
	loginGeminiPasteCode = func(c auth.OAuthProviderConfig, _ io.Reader) (*auth.AuthCredential, error) {
		pasteCodeCalled = true
		gotCfgName = c.Name
		return &auth.AuthCredential{AccessToken: "tok", Provider: c.Name}, nil
	}
	loginGeminiBrowser = func(auth.OAuthProviderConfig, io.Reader) (*auth.AuthCredential, error) {
		return nil, errors.New("browser flow must not run with --paste-code")
	}
	loginBrowser = func(auth.OAuthProviderConfig) (*auth.AuthCredential, error) {
		return nil, errors.New("generic browser-preset flow must not run with --paste-code")
	}
	setCredential = func(string, *auth.AuthCredential) error { return nil }

	out := captureOutput(authLoginCmd)

	if !pasteCodeCalled {
		t.Error("authLoginCmd() with --paste-code did not route to loginGeminiPasteCode")
	}
	if gotCfgName != cfg.Name {
		t.Errorf("loginGeminiPasteCode received cfg.Name = %q, want %q", gotCfgName, cfg.Name)
	}
	if !strings.Contains(out, "Login successful!") {
		t.Errorf("authLoginCmd() output = %q, want it to contain %q", out, "Login successful!")
	}
	if len(*codes) != 0 {
		t.Errorf("loginExit called with %v, want no exit on the success path", *codes)
	}
}

func TestAuthLogoutGeminiClearsAuthMethod(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())

	if got := canonicalProviderName("gemini-cli"); got != "gemini" {
		t.Fatalf("canonicalProviderName(%q) = %q, want %q", "gemini-cli", got, "gemini")
	}

	if err := auth.SetCredential("gemini", &auth.AuthCredential{AccessToken: "tok", Provider: "gemini"}); err != nil {
		t.Fatalf("auth.SetCredential(%q, …) error = %v", "gemini", err)
	}
	before, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if before.Providers == nil {
		t.Fatal("loadConfig().Providers = nil, want the providers section")
	}
	before.Providers.Gemini.APIKey = "keep-me"
	before.Providers.Gemini.AuthMethod = "oauth"
	if err := config.SaveConfig(getConfigPath(), before); err != nil {
		t.Fatalf("SaveConfig(%q) error = %v", getConfigPath(), err)
	}
	seeded, err := config.LoadConfig(getConfigPath())
	if err != nil {
		t.Fatalf("LoadConfig(%q) = %v, want the seeded config", getConfigPath(), err)
	}
	if got := seeded.Providers.Gemini.AuthMethod; got != "oauth" {
		t.Fatalf("seeded Providers.Gemini.AuthMethod = %q, want %q", got, "oauth")
	}

	originalArgs := os.Args
	os.Args = []string{"lele", "auth", "logout", "--provider", "gemini-cli"}
	t.Cleanup(func() { os.Args = originalArgs })

	out := captureOutput(authLogoutCmd)

	if want := "Logged out from gemini\n"; out != want {
		t.Errorf("authLogoutCmd() output = %q, want %q", out, want)
	}
	if cred, err := auth.GetCredential("gemini"); err != nil || cred != nil {
		t.Errorf("auth.GetCredential(%q) = (%v, %v), want (nil, nil) after logout", "gemini", cred, err)
	}

	persisted, err := config.LoadConfig(getConfigPath())
	if err != nil {
		t.Fatalf("LoadConfig(%q) = %v, want the persisted config", getConfigPath(), err)
	}
	if persisted.Providers == nil {
		t.Fatal("persisted.Providers = nil, want the providers section")
	}
	if got := persisted.Providers.Gemini.AuthMethod; got != "" {
		t.Errorf("persisted Providers.Gemini.AuthMethod = %q, want it blanked by logout", got)
	}
	if got := persisted.Providers.Gemini.APIKey; got != "keep-me" {
		t.Errorf("persisted Providers.Gemini.APIKey = %q, want %q (logout must not drop the provider config)", got, "keep-me")
	}
}

func seedConfigOnDisk(t *testing.T, mutate func(*config.Config)) {
	t.Helper()
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg.Providers == nil {
		t.Fatal("loadConfig().Providers = nil, want the providers section")
	}
	mutate(cfg)
	if err := config.SaveConfig(getConfigPath(), cfg); err != nil {
		t.Fatalf("SaveConfig(%q) error = %v", getConfigPath(), err)
	}
}

func reloadConfigFromDisk(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadConfig(getConfigPath())
	if err != nil {
		t.Fatalf("LoadConfig(%q) = %v, want the persisted config", getConfigPath(), err)
	}
	if cfg.Providers == nil {
		t.Fatal("persisted.Providers = nil, want the providers section")
	}
	return cfg
}

func TestAuthLoginGeminiOAuthPersistsAuthMethodOverExistingNamedEntry(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())

	codes := stubLoginExit(t)
	withLoginArgs(t, "--provider", "gemini")

	cfg, ok := auth.OAuthProviderConfigByName("gemini")
	if !ok {
		t.Fatal("auth.OAuthProviderConfigByName(\"gemini\") not found")
	}
	if cfg.Name != "gemini" {
		t.Fatalf("registry cfg.Name = %q, want %q", cfg.Name, "gemini")
	}

	originalGeminiBrowser, originalLoginBrowser, originalSetCredential := loginGeminiBrowser, loginBrowser, setCredential
	t.Cleanup(func() {
		loginGeminiBrowser, loginBrowser, setCredential = originalGeminiBrowser, originalLoginBrowser, originalSetCredential
	})

	loginGeminiBrowser = func(c auth.OAuthProviderConfig, _ io.Reader) (*auth.AuthCredential, error) {
		return &auth.AuthCredential{AccessToken: "tok", Provider: c.Name}, nil
	}
	loginBrowser = func(auth.OAuthProviderConfig) (*auth.AuthCredential, error) {
		return nil, errors.New("unexpected generic browser-preset flow")
	}

	var gotKey string
	setCredential = func(name string, _ *auth.AuthCredential) error {
		gotKey = name
		return nil
	}

	seedConfigOnDisk(t, func(c *config.Config) { c.Providers.Gemini.APIKey = "keep-me" })

	if got := reloadConfigFromDisk(t).Providers.Gemini.APIKey; got != "keep-me" {
		t.Fatalf("seeded Providers.Gemini.APIKey = %q, want %q", got, "keep-me")
	}

	out := captureOutput(authLoginCmd)

	if gotKey != "gemini" {
		t.Errorf("setCredential key = %q, want %q", gotKey, "gemini")
	}
	if !strings.Contains(out, "Login successful!") {
		t.Errorf("authLoginCmd() output = %q, want it to contain %q", out, "Login successful!")
	}
	if strings.Contains(out, "Warning: could not update config:") {
		t.Errorf("authLoginCmd() output = %q, want no config-write warning", out)
	}
	if len(*codes) != 0 {
		t.Errorf("loginExit called with %v, want no exit on the success path", *codes)
	}

	persisted := reloadConfigFromDisk(t)
	if got := persisted.Providers.Gemini.AuthMethod; got != "oauth" {
		t.Errorf("persisted Providers.Gemini.AuthMethod = %q, want %q (the write must reach the file even when a gemini entry already exists)", got, "oauth")
	}
	if got := persisted.Providers.Gemini.APIKey; got != "keep-me" {
		t.Errorf("persisted Providers.Gemini.APIKey = %q, want %q (login must not drop the provider config)", got, "keep-me")
	}
}

func TestAuthLoginClaudeOAuthPersistsAuthMethodOverExistingNamedEntry(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())

	cfg, ok := auth.OAuthProviderConfigByName("claude")
	if !ok {
		t.Fatal("auth.OAuthProviderConfigByName(\"claude\") not found")
	}
	if cfg.Name != "anthropic" {
		t.Fatalf("registry cfg.Name = %q, want %q", cfg.Name, "anthropic")
	}

	originalLoginClaudeBrowser, originalSetCredential := loginClaudeBrowser, setCredential
	t.Cleanup(func() {
		loginClaudeBrowser, setCredential = originalLoginClaudeBrowser, originalSetCredential
	})

	loginClaudeBrowser = func(c auth.OAuthProviderConfig, _ io.Reader) (*auth.AuthCredential, error) {
		return &auth.AuthCredential{AccessToken: "tok", Provider: c.Name}, nil
	}

	var gotKey string
	setCredential = func(name string, _ *auth.AuthCredential) error {
		gotKey = name
		return nil
	}

	seedConfigOnDisk(t, func(c *config.Config) { c.Providers.Anthropic.APIKey = "keep-me" })

	if got := reloadConfigFromDisk(t).Providers.Anthropic.APIKey; got != "keep-me" {
		t.Fatalf("seeded Providers.Anthropic.APIKey = %q, want %q", got, "keep-me")
	}

	out := captureOutput(func() { authLoginClaudeOAuth(cfg, false) })

	if gotKey != "anthropic" {
		t.Errorf("setCredential key = %q, want %q", gotKey, "anthropic")
	}
	if !strings.Contains(out, "Login successful!") {
		t.Errorf("authLoginClaudeOAuth() output = %q, want it to contain %q", out, "Login successful!")
	}

	persisted := reloadConfigFromDisk(t)
	if got := persisted.Providers.Anthropic.AuthMethod; got != "oauth" {
		t.Errorf("persisted Providers.Anthropic.AuthMethod = %q, want %q (the write must reach the file even when an anthropic entry already exists)", got, "oauth")
	}
	if got := persisted.Providers.Anthropic.APIKey; got != "keep-me" {
		t.Errorf("persisted Providers.Anthropic.APIKey = %q, want %q (login must not drop the provider config)", got, "keep-me")
	}
}

func TestAuthLogoutAnthropicClearsPersistedAuthMethod(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())

	if got := canonicalProviderName("claude"); got != "anthropic" {
		t.Fatalf("canonicalProviderName(%q) = %q, want %q", "claude", got, "anthropic")
	}

	if err := auth.SetCredential("anthropic", &auth.AuthCredential{AccessToken: "tok", Provider: "anthropic"}); err != nil {
		t.Fatalf("auth.SetCredential(%q, …) error = %v", "anthropic", err)
	}
	seedConfigOnDisk(t, func(c *config.Config) {
		c.Providers.Anthropic.APIKey = "keep-me"
		c.Providers.Anthropic.AuthMethod = "oauth"
	})
	if got := reloadConfigFromDisk(t).Providers.Anthropic.AuthMethod; got != "oauth" {
		t.Fatalf("seeded Providers.Anthropic.AuthMethod = %q, want %q", got, "oauth")
	}

	originalArgs := os.Args
	os.Args = []string{"lele", "auth", "logout", "--provider", "claude"}
	t.Cleanup(func() { os.Args = originalArgs })

	out := captureOutput(authLogoutCmd)

	if want := "Logged out from anthropic\n"; out != want {
		t.Errorf("authLogoutCmd() output = %q, want %q", out, want)
	}
	if cred, err := auth.GetCredential("anthropic"); err != nil || cred != nil {
		t.Errorf("auth.GetCredential(%q) = (%v, %v), want (nil, nil) after logout", "anthropic", cred, err)
	}

	persisted := reloadConfigFromDisk(t)
	if got := persisted.Providers.Anthropic.AuthMethod; got != "" {
		t.Errorf("persisted Providers.Anthropic.AuthMethod = %q, want it blanked by logout", got)
	}
	if got := persisted.Providers.Anthropic.APIKey; got != "keep-me" {
		t.Errorf("persisted Providers.Anthropic.APIKey = %q, want %q (logout must not drop the provider config)", got, "keep-me")
	}
}

func TestAuthLogoutAllClearsPersistedGeminiAndAnthropicAuthMethod(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())

	for _, provider := range []string{"gemini", "anthropic"} {
		if err := auth.SetCredential(provider, &auth.AuthCredential{AccessToken: "tok", Provider: provider}); err != nil {
			t.Fatalf("auth.SetCredential(%q, …) error = %v", provider, err)
		}
	}
	seedConfigOnDisk(t, func(c *config.Config) {
		c.Providers.Gemini.APIKey = "keep-gemini"
		c.Providers.Gemini.AuthMethod = "oauth"
		c.Providers.Anthropic.APIKey = "keep-anthropic"
		c.Providers.Anthropic.AuthMethod = "oauth"
	})
	seeded := reloadConfigFromDisk(t)
	if got := seeded.Providers.Gemini.AuthMethod; got != "oauth" {
		t.Fatalf("seeded Providers.Gemini.AuthMethod = %q, want %q", got, "oauth")
	}
	if got := seeded.Providers.Anthropic.AuthMethod; got != "oauth" {
		t.Fatalf("seeded Providers.Anthropic.AuthMethod = %q, want %q", got, "oauth")
	}

	originalArgs := os.Args
	os.Args = []string{"lele", "auth", "logout"}
	t.Cleanup(func() { os.Args = originalArgs })

	out := captureOutput(authLogoutCmd)

	if want := "Logged out from all providers\n"; out != want {
		t.Errorf("authLogoutCmd() output = %q, want %q", out, want)
	}
	for _, provider := range []string{"gemini", "anthropic"} {
		if cred, err := auth.GetCredential(provider); err != nil || cred != nil {
			t.Errorf("auth.GetCredential(%q) = (%v, %v), want (nil, nil) after logout", provider, cred, err)
		}
	}

	persisted := reloadConfigFromDisk(t)
	if got := persisted.Providers.Gemini.AuthMethod; got != "" {
		t.Errorf("persisted Providers.Gemini.AuthMethod = %q, want it blanked by logout (a deleted credential must not keep selecting the OAuth wire)", got)
	}
	if got := persisted.Providers.Anthropic.AuthMethod; got != "" {
		t.Errorf("persisted Providers.Anthropic.AuthMethod = %q, want it blanked by logout", got)
	}
	if got := persisted.Providers.Gemini.APIKey; got != "keep-gemini" {
		t.Errorf("persisted Providers.Gemini.APIKey = %q, want %q (logout must not drop the provider config)", got, "keep-gemini")
	}
	if got := persisted.Providers.Anthropic.APIKey; got != "keep-anthropic" {
		t.Errorf("persisted Providers.Anthropic.APIKey = %q, want %q (logout must not drop the provider config)", got, "keep-anthropic")
	}
}

func TestSetNamedAuthMethod(t *testing.T) {
	t.Run("nil providers is a no-op", func(t *testing.T) {
		setNamedAuthMethod(nil, "gemini", "oauth")
	})

	t.Run("blank name is a no-op", func(t *testing.T) {
		providers := &config.ProvidersConfig{}
		setNamedAuthMethod(providers, "   ", "oauth")
		if len(providers.Named) != 0 {
			t.Errorf("Named = %v, want it untouched for a blank name", providers.Named)
		}
	})

	t.Run("existing entry keeps every other field", func(t *testing.T) {
		ws := true
		providers := &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"gemini": {
					Type: "gemini",
					ProviderConfig: config.ProviderConfig{
						APIKey:      "keep-me",
						APIBase:     "https://base",
						Proxy:       "http://proxy",
						AuthMethod:  "token",
						ConnectMode: "stdio",
					},
					WebSearch: &ws,
					Models:    map[string]config.ProviderModelConfig{"m": {Model: "m"}},
				},
			},
		}
		setNamedAuthMethod(providers, " GEMINI ", "oauth")
		got := providers.Named["gemini"]
		if got.AuthMethod != "oauth" {
			t.Errorf("AuthMethod = %q, want %q", got.AuthMethod, "oauth")
		}
		if got.APIKey != "keep-me" || got.APIBase != "https://base" || got.Proxy != "http://proxy" || got.ConnectMode != "stdio" {
			t.Errorf("provider fields = %+v, want api_key/api_base/proxy/connect_mode preserved", got.ProviderConfig)
		}
		if got.Type != "gemini" {
			t.Errorf("Type = %q, want %q", got.Type, "gemini")
		}
		if got.WebSearch == nil || !*got.WebSearch {
			t.Errorf("WebSearch = %v, want it preserved", got.WebSearch)
		}
		if len(got.Models) != 1 {
			t.Errorf("Models = %v, want the model map preserved", got.Models)
		}
	})

	t.Run("missing entry is seeded from the typed field", func(t *testing.T) {
		providers := &config.ProvidersConfig{Gemini: config.ProviderConfig{APIKey: "typed-key"}}
		setNamedAuthMethod(providers, "gemini", "oauth")
		got := providers.Named["gemini"]
		if got.AuthMethod != "oauth" {
			t.Errorf("AuthMethod = %q, want %q", got.AuthMethod, "oauth")
		}
		if got.APIKey != "typed-key" {
			t.Errorf("APIKey = %q, want %q (the typed field must not be shadowed away)", got.APIKey, "typed-key")
		}
		if got.Type != "gemini" {
			t.Errorf("Type = %q, want %q", got.Type, "gemini")
		}
	})

	t.Run("unknown provider still gets the method written", func(t *testing.T) {
		providers := &config.ProvidersConfig{}
		setNamedAuthMethod(providers, "acme", "oauth")
		got := providers.Named["acme"]
		if got.AuthMethod != "oauth" || got.Type != "acme" {
			t.Errorf("Named[\"acme\"] = %+v, want type and auth_method set", got)
		}
	})
}

var nilProvidersShapes = []struct {
	name string
	body string
}{
	{name: "providers key absent", body: "{}"},
	{name: "providers explicit null", body: `{"providers":null}`},
}

func writeRawConfig(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(getConfigPath(), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", getConfigPath(), err)
	}
}

func assertNilProvidersPrecondition(t *testing.T, body string) {
	t.Helper()
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg.Providers != nil {
		t.Fatalf("precondition: loadConfig().Providers = %+v, want nil for config %s", cfg.Providers, body)
	}
}

func providersSectionOf(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(getConfigPath())
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", getConfigPath(), err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("persisted config is not valid JSON: %v\n%s", err, raw)
	}
	return parsed["providers"]
}

func assertProvidersStillAbsentOrNull(t *testing.T) {
	t.Helper()
	section := providersSectionOf(t)
	if len(section) == 0 || string(section) == "null" {
		return
	}
	t.Errorf("persisted providers = %s, want it absent or null: a write to a nil section must not fabricate an empty providers object", section)
}

func assertSoleProviderEntry(t *testing.T, provider, method string) {
	t.Helper()
	section := providersSectionOf(t)
	if len(section) == 0 || string(section) == "null" {
		t.Fatalf("persisted providers = %s, want the %s entry written by the login", section, provider)
	}
	var entries map[string]struct {
		AuthMethod string `json:"auth_method"`
	}
	if err := json.Unmarshal(section, &entries); err != nil {
		t.Fatalf("persisted providers %s is not a JSON object: %v", section, err)
	}
	if len(entries) != 1 {
		t.Errorf("persisted providers = %s, want exactly one entry (%s); a nil-safe write must not fabricate others", section, provider)
	}
	if got := entries[provider].AuthMethod; got != method {
		t.Errorf("persisted providers.%s.auth_method = %q, want %q", provider, got, method)
	}
}

func stubSetCredential(t *testing.T) *string {
	t.Helper()
	original := setCredential
	key := ""
	setCredential = func(name string, _ *auth.AuthCredential) error {
		key = name
		return nil
	}
	t.Cleanup(func() { setCredential = original })
	return &key
}

func withLogoutArgs(t *testing.T, args ...string) {
	t.Helper()
	original := os.Args
	os.Args = append([]string{"lele", "auth", "logout"}, args...)
	t.Cleanup(func() { os.Args = original })
}

func TestAuthLogoutNilProvidersSectionDoesNotPanic(t *testing.T) {
	providers := []struct{ flag, canonical string }{
		{flag: "openai", canonical: "openai"},
		{flag: "anthropic", canonical: "anthropic"},
		{flag: "gemini", canonical: "gemini"},
		{flag: "claude", canonical: "anthropic"},
	}

	for _, shape := range nilProvidersShapes {
		for _, provider := range providers {
			t.Run(shape.name+"/"+provider.flag, func(t *testing.T) {
				t.Setenv("LELE_CONFIG_DIR", t.TempDir())
				writeRawConfig(t, shape.body)
				assertNilProvidersPrecondition(t, shape.body)

				withLogoutArgs(t, "--provider", provider.flag)

				out := captureOutput(authLogoutCmd)

				if want := "Logged out from " + provider.canonical + "\n"; out != want {
					t.Errorf("authLogoutCmd() output = %q, want %q", out, want)
				}
				assertProvidersStillAbsentOrNull(t)
			})
		}
	}
}

func TestAuthLogoutAllNilProvidersSectionDoesNotPanic(t *testing.T) {
	for _, shape := range nilProvidersShapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Setenv("LELE_CONFIG_DIR", t.TempDir())
			writeRawConfig(t, shape.body)
			assertNilProvidersPrecondition(t, shape.body)

			withLogoutArgs(t)

			out := captureOutput(authLogoutCmd)

			if want := "Logged out from all providers\n"; out != want {
				t.Errorf("authLogoutCmd() output = %q, want %q", out, want)
			}
			assertProvidersStillAbsentOrNull(t)
		})
	}
}

func TestAuthLoginNilProvidersSectionDoesNotPanic(t *testing.T) {
	t.Run("openai oauth", func(t *testing.T) {
		t.Setenv("LELE_CONFIG_DIR", t.TempDir())
		writeRawConfig(t, "{}")
		assertNilProvidersPrecondition(t, "{}")

		codes := stubLoginExit(t)
		withLoginArgs(t, "--provider", "openai")
		key := stubSetCredential(t)

		originalBrowser := loginBrowser
		loginBrowser = func(auth.OAuthProviderConfig) (*auth.AuthCredential, error) {
			return &auth.AuthCredential{AccessToken: "tok", Provider: "openai"}, nil
		}
		t.Cleanup(func() { loginBrowser = originalBrowser })

		out := captureOutput(authLoginCmd)

		if *key != "openai" {
			t.Errorf("setCredential key = %q, want %q", *key, "openai")
		}
		if !strings.Contains(out, "Login successful!") {
			t.Errorf("authLoginCmd() output = %q, want it to contain %q", out, "Login successful!")
		}
		if len(*codes) != 0 {
			t.Errorf("loginExit called with %v, want no exit on the success path", *codes)
		}
		assertSoleProviderEntry(t, "openai", "oauth")
	})

	t.Run("claude oauth", func(t *testing.T) {
		t.Setenv("LELE_CONFIG_DIR", t.TempDir())
		writeRawConfig(t, "{}")
		assertNilProvidersPrecondition(t, "{}")

		codes := stubLoginExit(t)
		withLoginArgs(t, "--provider", "claude")
		key := stubSetCredential(t)

		originalClaudeBrowser := loginClaudeBrowser
		loginClaudeBrowser = func(c auth.OAuthProviderConfig, _ io.Reader) (*auth.AuthCredential, error) {
			return &auth.AuthCredential{AccessToken: "tok", Provider: c.Name}, nil
		}
		t.Cleanup(func() { loginClaudeBrowser = originalClaudeBrowser })

		out := captureOutput(authLoginCmd)

		if *key != "anthropic" {
			t.Errorf("setCredential key = %q, want %q", *key, "anthropic")
		}
		if !strings.Contains(out, "Login successful!") {
			t.Errorf("authLoginCmd() output = %q, want it to contain %q", out, "Login successful!")
		}
		if strings.Contains(out, "Warning: could not update config:") {
			t.Errorf("authLoginCmd() output = %q, want no config-write warning", out)
		}
		if len(*codes) != 0 {
			t.Errorf("loginExit called with %v, want no exit on the success path", *codes)
		}
		assertSoleProviderEntry(t, "anthropic", "oauth")
	})

	t.Run("gemini oauth", func(t *testing.T) {
		t.Setenv("LELE_CONFIG_DIR", t.TempDir())
		writeRawConfig(t, `{"providers":null}`)
		assertNilProvidersPrecondition(t, `{"providers":null}`)

		codes := stubLoginExit(t)
		withLoginArgs(t, "--provider", "gemini")
		key := stubSetCredential(t)

		originalGeminiBrowser := loginGeminiBrowser
		loginGeminiBrowser = func(c auth.OAuthProviderConfig, _ io.Reader) (*auth.AuthCredential, error) {
			return &auth.AuthCredential{AccessToken: "tok", Provider: c.Name}, nil
		}
		t.Cleanup(func() { loginGeminiBrowser = originalGeminiBrowser })

		out := captureOutput(authLoginCmd)

		if *key != "gemini" {
			t.Errorf("setCredential key = %q, want %q", *key, "gemini")
		}
		if !strings.Contains(out, "Login successful!") {
			t.Errorf("authLoginCmd() output = %q, want it to contain %q", out, "Login successful!")
		}
		if strings.Contains(out, "Warning: could not update config:") {
			t.Errorf("authLoginCmd() output = %q, want no config-write warning", out)
		}
		if len(*codes) != 0 {
			t.Errorf("loginExit called with %v, want no exit on the success path", *codes)
		}
		assertSoleProviderEntry(t, "gemini", "oauth")
	})

	t.Run("anthropic paste token", func(t *testing.T) {
		t.Setenv("LELE_CONFIG_DIR", t.TempDir())
		writeRawConfig(t, `{"providers":null}`)
		assertNilProvidersPrecondition(t, `{"providers":null}`)

		codes := stubLoginExit(t)
		withLoginArgs(t, "--provider", "anthropic")
		key := stubSetCredential(t)

		originalPasteToken := pasteToken
		pasteToken = func(provider string, _ io.Reader) (*auth.AuthCredential, error) {
			return &auth.AuthCredential{AccessToken: "tok", Provider: provider}, nil
		}
		t.Cleanup(func() { pasteToken = originalPasteToken })

		out := captureOutput(authLoginCmd)

		if *key != "anthropic" {
			t.Errorf("setCredential key = %q, want %q", *key, "anthropic")
		}
		if want := "Token saved for anthropic!\n"; out != want {
			t.Errorf("authLoginCmd() output = %q, want %q", out, want)
		}
		if len(*codes) != 0 {
			t.Errorf("loginExit called with %v, want no exit on the success path", *codes)
		}
		assertSoleProviderEntry(t, "anthropic", "token")
	})
}

func TestProvidersForWriteAllocatesOnDemand(t *testing.T) {
	t.Run("nil section is allocated", func(t *testing.T) {
		cfg := &config.Config{}
		providers := providersForWrite(cfg)
		if providers == nil {
			t.Fatal("providersForWrite() = nil, want a non-nil section")
		}
		if cfg.Providers != providers {
			t.Errorf("cfg.Providers = %p, want the returned section %p", cfg.Providers, providers)
		}
		data, err := json.Marshal(providers)
		if err != nil {
			t.Fatalf("json.Marshal(providers) error = %v", err)
		}
		if string(data) != "null" {
			t.Errorf("json.Marshal(empty providers) = %s, want %s (allocating must not invent entries)", data, "null")
		}
	})

	t.Run("existing section is returned as is", func(t *testing.T) {
		existing := &config.ProvidersConfig{Gemini: config.ProviderConfig{APIKey: "keep-me"}}
		cfg := &config.Config{Providers: existing}
		if got := providersForWrite(cfg); got != existing {
			t.Errorf("providersForWrite() = %p, want the existing section %p", got, existing)
		}
	})
}
