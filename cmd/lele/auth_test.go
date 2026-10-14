package main

import (
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/auth"
)

// stubLoginExit swaps the loginExit seam for a fake that records the requested
// status codes instead of terminating the test binary, and restores it on
// cleanup. Every test that drives a login error path must use it: calling the
// real os.Exit would kill the whole test process.
func stubLoginExit(t *testing.T) *[]int {
	t.Helper()
	codes := []int{}
	original := loginExit
	loginExit = func(code int) { codes = append(codes, code) }
	t.Cleanup(func() { loginExit = original })
	return &codes
}

// withLoginArgs points os.Args at an `auth login` invocation for the duration of
// the test, so authLoginCmd() reads the flags under test.
func withLoginArgs(t *testing.T, args ...string) {
	t.Helper()
	original := os.Args
	os.Args = append([]string{"lele", "auth", "login"}, args...)
	t.Cleanup(func() { os.Args = original })
}

// assertExitCodes checks the whole sequence of exit codes a fake loginExit saw.
// An empty sequence is a failure: it means the command returned without ever
// signalling the failure to the shell.
func assertExitCodes(t *testing.T, want []int, got []int) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loginExit called with %v, want %v", got, want)
	}
}

// TestParseAuthSubcommand tests the subcommand parsing from authCmd.
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

// TestParseLoginArgs tests the login argument parsing logic.
func TestParseLoginArgs_ProviderLong(t *testing.T) {
	provider, dev := parseLoginArgs([]string{"--provider", "openai"})
	if provider != "openai" {
		t.Errorf("provider = %q, want %q", provider, "openai")
	}
	if dev {
		t.Error("useDeviceCode = true, want false")
	}
}

func TestParseLoginArgs_ProviderShort(t *testing.T) {
	provider, _ := parseLoginArgs([]string{"-p", "anthropic"})
	if provider != "anthropic" {
		t.Errorf("provider = %q, want %q", provider, "anthropic")
	}
}

func TestParseLoginArgs_DeviceCode(t *testing.T) {
	_, dev := parseLoginArgs([]string{"--device-code"})
	if !dev {
		t.Error("useDeviceCode = false, want true")
	}
}

func TestParseLoginArgs_DeviceCodeWithProvider(t *testing.T) {
	provider, dev := parseLoginArgs([]string{
		"--provider", "openai", "--device-code",
	})
	if provider != "openai" {
		t.Errorf("provider = %q, want %q", provider, "openai")
	}
	if !dev {
		t.Error("useDeviceCode = false, want true")
	}
}

func TestParseLoginArgs_AllFlags(t *testing.T) {
	provider, dev := parseLoginArgs([]string{
		"--provider", "openai", "--device-code",
	})
	if provider != "openai" {
		t.Errorf("provider = %q, want %q", provider, "openai")
	}
	if !dev {
		t.Error("useDeviceCode = false, want true")
	}
}

func TestParseLoginArgs_Empty(t *testing.T) {
	provider, dev := parseLoginArgs([]string{})
	if provider != "" {
		t.Errorf("provider = %q, want empty", provider)
	}
	if dev {
		t.Error("useDeviceCode = true, want false")
	}
}

func TestParseLoginArgs_ProviderMissingValue(t *testing.T) {
	provider, _ := parseLoginArgs([]string{"--provider"})
	if provider != "" {
		t.Errorf("provider = %q, want empty (no value to consume)", provider)
	}
}

func TestParseLoginArgs_ProviderShortMissingValue(t *testing.T) {
	provider, _ := parseLoginArgs([]string{"-p"})
	if provider != "" {
		t.Errorf("provider = %q, want empty (no value to consume)", provider)
	}
}

func TestParseLoginArgs_MultipleProviders(t *testing.T) {
	// Last provider wins, like the real parser
	provider, _ := parseLoginArgs([]string{
		"--provider", "openai", "--provider", "anthropic",
	})
	if provider != "anthropic" {
		t.Errorf("provider = %q, want %q", provider, "anthropic")
	}
}

func TestParseLoginArgs_DeviceCodeBeforeProvider(t *testing.T) {
	provider, dev := parseLoginArgs([]string{
		"--device-code", "--provider", "openai",
	})
	if provider != "openai" {
		t.Errorf("provider = %q, want %q", provider, "openai")
	}
	if !dev {
		t.Error("useDeviceCode = false, want true")
	}
}

func TestParseLoginArgs_UnknownFlagsIgnored(t *testing.T) {
	provider, dev := parseLoginArgs([]string{"--unknown", "value"})
	if provider != "" {
		t.Errorf("provider = %q, want empty", provider)
	}
	if dev {
		t.Error("useDeviceCode = true, want false")
	}
}

// TestParseLogoutArgs tests the logout argument parsing logic.
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

// resolveLoginProvider maps a --provider value to the login implementation and
// is the only place the CLI decides which flow runs. These tests exercise it
// without any network, config write or os.Exit.
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
			// Regression guard: anthropic must accept the same normalization as
			// every registry-backed provider.
			name:        "uppercase anthropic pastes token",
			provider:    "  ANTHROPIC ",
			wantKind:    loginKindPasteToken,
			wantCfgName: "",
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

// TestResolveLoginProviderUnsupportedProviderKeepsName covers the legacy
// behavior kept by the refactor: the offending name is echoed verbatim.
func TestResolveLoginProviderUnsupportedProviderKeepsName(t *testing.T) {
	_, _, err := resolveLoginProvider("GROK", false)
	if err == nil {
		t.Fatal("resolveLoginProvider(\"GROK\", false) error = nil, want an unsupported provider error")
	}
	if !strings.HasPrefix(err.Error(), "Unsupported provider: GROK\n") {
		t.Errorf("error = %q, want it to start with %q", err.Error(), "Unsupported provider: GROK\n")
	}
}

// TestResolveLoginProviderDeviceCodeUnavailable documents that --device-code is
// rejected explicitly for presets that only implement the browser flow instead
// of being silently ignored.
func TestResolveLoginProviderDeviceCodeUnavailable(t *testing.T) {
	for _, provider := range []string{"google-antigravity", "antigravity", " AntiGravity "} {
		t.Run(provider, func(t *testing.T) {
			kind, cfg, err := resolveLoginProvider(provider, true)

			if kind != loginKindUnsupported {
				t.Errorf("resolveLoginProvider(%q, true) kind = %v, want loginKindUnsupported", provider, kind)
			}
			if cfg.Name != "" {
				t.Errorf("resolveLoginProvider(%q, true) cfg.Name = %q, want empty on failure", provider, cfg.Name)
			}
			want := "device code flow is not available for google-antigravity"
			if err == nil {
				t.Fatalf("resolveLoginProvider(%q, true) error = nil, want %q", provider, want)
			}
			if err.Error() != want {
				t.Errorf("resolveLoginProvider(%q, true) error = %q, want %q", provider, err.Error(), want)
			}
		})
	}
}

// TestSupportedProvidersIsDerivedFromRegistry pins the single-source-of-truth
// rule: the "supported providers" text is built from auth.OAuthProviderNames,
// so every registered preset (and only those) must appear in it. A hardcoded
// list would drop a provider as soon as the registry grows.
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

// TestUnsupportedProviderErrorListsRegistryNames checks the user-facing error
// line by line.
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

// TestAuthHelpDocumentsEveryRegistryProvider keeps the help in sync with the
// registry (and documents the antigravity alias).
func TestAuthHelpDocumentsEveryRegistryProvider(t *testing.T) {
	out := captureOutput(authHelp)

	for _, name := range auth.OAuthProviderNames() {
		if !strings.Contains(out, name) {
			t.Errorf("authHelp() output is missing registry provider %q:\n%s", name, out)
		}
	}
	for _, want := range []string{"antigravity   (alias of google-antigravity)", "anthropic", "--device-code"} {
		if !strings.Contains(out, want) {
			t.Errorf("authHelp() output is missing %q:\n%s", want, out)
		}
	}
}

// TestAuthLoginCmdUnsupportedProviderPrintsRegistryList runs the real command
// (no network, no store) and checks what the user actually sees.
//
// It injects the loginExit seam because the error paths now exit non-zero: the
// real os.Exit would terminate the test binary.
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

// TestAuthLoginCmdMissingProvider checks the required-provider path still lists
// the registry-derived providers.
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

// TestAuthLoginCmdMissingProviderExitsNonZero pins the shell contract of the
// required-provider path: the message must still be printed and the process must
// end with status 1, so scripts can detect the failure.
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

// TestAuthLoginCmdUnsupportedProviderExitsNonZero covers the unknown-provider
// path end to end: same two lines as before plus a non-zero status.
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

// TestAuthLoginCmdDeviceCodeRejectionExitsNonZero covers the rejected
// `--provider <browser-preset> --device-code` combination.
//
// loginBrowser is also stubbed (with a failing flow) on purpose: if the guard
// under test regressed, the command would start the browser flow instead of
// failing, and the stub turns that hang into a plain assertion failure.
func TestAuthLoginCmdDeviceCodeRejectionExitsNonZero(t *testing.T) {
	codes := stubLoginExit(t)
	withLoginArgs(t, "--provider", "antigravity", "--device-code")

	originalLoginBrowser := loginBrowser
	loginBrowser = func(auth.OAuthProviderConfig) (*auth.AuthCredential, error) {
		return nil, errors.New("unexpected browser flow")
	}
	t.Cleanup(func() { loginBrowser = originalLoginBrowser })

	out := captureOutput(authLoginCmd)

	want := "device code flow is not available for google-antigravity\n"
	if out != want {
		t.Errorf("authLoginCmd() output = %q, want %q", out, want)
	}
	assertExitCodes(t, []int{1}, *codes)
}

// TestAuthLoginCmdPassesNormalizedNameToPasteToken anchors the second half of the
// normalization rule: the name the paste-token flow receives (and therefore the
// credential key, since authLoginPasteToken stores under it) is always the
// canonical `anthropic`, whatever spelling the user typed. The fake pasteToken
// returns an error so the test stops before auth.SetCredential and before any
// config write — and that error path exercises loginExit too.
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
			// Parity: both spellings produce byte-identical output.
			want := "Login failed: stop before storing\n"
			if out != want {
				t.Errorf("authLoginCmd() output = %q, want %q", out, want)
			}
			assertExitCodes(t, []int{1}, *codes)
		})
	}
}

// TestAuthLoginBrowserPresetStoresCanonicalKey is the regression guard for
// pkg/providers/antigravity_provider.go, which reads the credential under the
// canonical name "google-antigravity": if authLoginBrowserPreset ever stored it
// under another key (or under the user-typed alias) the account would break
// silently, with no test turning red.
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

// TestAuthLoginBrowserPresetPropagatesErrors checks both failure paths: a failing
// OAuth flow must not write any credential, and a failing store write must exit
// non-zero. loginExit is fake in both cases (the real os.Exit would kill the test
// binary) — and because authLoginBrowserPreset returns right after loginExit,
// execution never continues past the failure.
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

// Note on coverage: the --device-code guard for non-OpenAI presets is tested both
// at the pure-helper level (TestResolveLoginProviderDeviceCodeUnavailable, which
// pins the exact message) and end to end
// (TestAuthLoginCmdDeviceCodeRejectionExitsNonZero, which stubs loginBrowser so a
// regressed guard fails instead of starting a real browser flow).
