package main

import (
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
	// Last provider wins, like the real parser
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
			// `claude` is the alias of the anthropic preset that runs the OAuth
			// flow instead of the paste-token flow.
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
	tests := []struct {
		provider string
		want     string
	}{
		{"google-antigravity", "device code flow is not available for google-antigravity"},
		{"antigravity", "device code flow is not available for google-antigravity"},
		{" AntiGravity ", "device code flow is not available for google-antigravity"},
		{"claude", "device code flow is not available for anthropic"},
		// The verbatim spelling is intercepted before the registry lookup, so it
		// has to reject the flag itself: before this case existed,
		// `--provider anthropic --device-code` silently dropped the flag and ran
		// the paste-token flow.
		{"anthropic", "device code flow is not available for anthropic"},
		{"  ANTHROPIC ", "device code flow is not available for anthropic"},
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

// TestCanonicalProviderName pins the logout normalization: aliases and odd
// spellings must resolve to the canonical credential key, unknown names are
// returned normalized but unresolved.
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

// TestSupportedProvidersListsAnthropicOnce guards against the historical
// duplication: "anthropic" is a registry name now, so appending it by hand
// printed it twice ("anthropic, google-antigravity, openai, anthropic").
func TestSupportedProvidersListsAnthropicOnce(t *testing.T) {
	got := supportedProviders()
	if n := strings.Count(got, "anthropic"); n != 1 {
		t.Errorf("supportedProviders() = %q mentions anthropic %d times, want exactly 1", got, n)
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
// registry (and documents the aliases: antigravity already had a line, claude
// and codex are pinned here because `claude` and `codex` are the spellings that
// change flow — supportedProviders() cannot list them, it feeds the shared
// error string).
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
		"  --provider claude    Claude OAuth (alias of the anthropic preset; use --provider anthropic to paste a token)",
		"  --provider codex     OpenAI OAuth (alias of the openai preset; same flow as --provider openai)",
	} {
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
// `--provider <browser-preset> --device-code` combination, for both the
// registry-backed preset (antigravity) and the `anthropic` spelling that is
// intercepted before the registry lookup.
//
// loginBrowser and pasteToken are both stubbed (with failing flows) on purpose:
// if the guard under test regressed, the command would start one of those flows
// instead of failing, and the stubs turn that hang (or stdin read) into a plain
// assertion failure.
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

// TestAuthLoginClaudeOAuthStoresCanonicalKeyAndWritesAuthMethod pins the
// end-to-end contract of `lele auth login --provider claude`: the credential is
// stored under the canonical registry key ("anthropic", never the typed alias
// "claude") and Providers.Anthropic.AuthMethod is flipped to "oauth" (that is
// what makes the provider pick the OAuth wire contract on the next run).
//
// LELE_CONFIG_DIR is redirected to a temp dir so the best-effort config write
// lands there and never touches the developer's real ~/.lele/config.json.
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

	// The best-effort Providers.Anthropic.AuthMethod = "oauth" write must have
	// run and must have persisted: in the isolated LELE_CONFIG_DIR (fresh, so
	// loadConfig returns the default) SaveConfig creates config.json and,
	// because an explicit auth method counts as provider configuration,
	// MarshalJSON keeps the anthropic entry despite there being no API key or
	// models. Reloading the file is therefore a real end-to-end assertion.
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

// TestAuthLoginClaudeOAuthPasteCode verifies that pasteCode=true routes to the
// headless paste-code seam (and never to the browser flow).
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

// TestAuthLoginClaudeOAuthConfigWriteFailureIsActionable covers the branch the
// success-path test cannot reach: the credential is stored but the best-effort
// `providers.anthropic.auth_method = "oauth"` write fails.
//
// The user-visible contract is that the warning stays and is followed by an
// actionable line naming the key and the file, because without that key
// pkg/providers/factory.go does NOT select the Claude OAuth provider for the
// stored credential and the next model call dies with a confusing
// "no API key configured for model".
//
// The failure is induced with the config seam this file already uses
// (LELE_CONFIG_DIR), no new production seam: pointing it at a dangling symlink
// keeps loadConfig happy (a missing path reads as the default config) while
// making config.SaveConfig's MkdirAll step fail deterministically.
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
	// The actionable line: exact key, exact file, and why it matters.
	wantAction := "Set providers.anthropic.auth_method = \"oauth\" in " + getConfigPath() +
		" for the Claude OAuth login to be used.\n"
	if !strings.Contains(out, "\n"+wantAction) {
		t.Errorf("authLoginClaudeOAuth() output = %q, want it to contain %q on its own line after the warning", out, wantAction)
	}
	// The success path is unchanged: the login is still reported as successful
	// (the credential was stored) and no Account line is invented.
	if !strings.HasSuffix(out, "Login successful!\n") {
		t.Errorf("authLoginClaudeOAuth() output = %q, want it to end with %q", out, "Login successful!\n")
	}
}

// TestAuthLoginCmdPasteCodeRejectedForNonClaude runs the real command and checks
// the exact user-facing line for `--paste-code` on a non-Claude provider.
//
// loginBrowser is stubbed (with a failing flow) on purpose: if the guard
// regressed, the command would start the browser flow instead of failing, and
// the stub turns that hang into a plain assertion failure.
func TestAuthLoginCmdPasteCodeRejectedForNonClaude(t *testing.T) {
	codes := stubLoginExit(t)
	withLoginArgs(t, "--provider", "antigravity", "--paste-code")

	originalLoginBrowser := loginBrowser
	loginBrowser = func(auth.OAuthProviderConfig) (*auth.AuthCredential, error) {
		return nil, errors.New("unexpected browser flow")
	}
	t.Cleanup(func() { loginBrowser = originalLoginBrowser })

	out := captureOutput(authLoginCmd)

	want := "--paste-code is only supported for the Claude OAuth flow; use: lele auth login --provider claude --paste-code\n"
	if out != want {
		t.Errorf("authLoginCmd() output = %q, want %q", out, want)
	}
	assertExitCodes(t, []int{1}, *codes)
}
