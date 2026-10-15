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
			name:        "gemini -> gemini oauth",
			provider:    "gemini",
			wantKind:    loginKindGeminiOAuth,
			wantCfgName: "gemini",
		},
		{
			// `gemini-cli` is the spelling the official @google/gemini-cli tool
			// uses; it must reach the very same Gemini OAuth flow.
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
		// Gemini has no RFC 8628 device flow either; the rejection names the
		// canonical preset, so the `gemini-cli` alias reports `gemini`.
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
// the exact user-facing line for `--paste-code` on a provider that has no
// paste-code flow (antigravity; the flag is accepted for claude and gemini).
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

	want := "--paste-code is only supported for the Claude and Gemini OAuth flows; use: lele auth login --provider claude --paste-code\n"
	if out != want {
		t.Errorf("authLoginCmd() output = %q, want %q", out, want)
	}
	assertExitCodes(t, []int{1}, *codes)
}

// TestAuthLoginGeminiOAuthStoresCanonicalKeyAndWritesAuthMethod pins the
// end-to-end contract of `lele auth login --provider gemini` (and of its
// `gemini-cli` alias): the credential is stored under the canonical registry key
// ("gemini", never the typed alias) and Providers.Gemini.AuthMethod is flipped
// to "oauth" (that is what makes the provider pick the Gemini Code Assist wire
// contract on the next run).
//
// Unlike the Claude sibling this drives the real authLoginCmd: the kind returned
// by resolveLoginProvider is therefore observable here, and a `gemini` that fell
// back to the generic browser-preset flow would call the failing loginBrowser
// stub below instead of silently starting a real browser flow — so the missing
// auth_method is reported as a plain assertion failure rather than a hang.
//
// LELE_CONFIG_DIR is redirected to a temp dir so the best-effort config write
// lands there and never touches the developer's real ~/.lele/config.json.
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
			// Must never run: the generic browser-preset flow stores the
			// credential but writes no auth_method (and would hit the network in
			// production).
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

			// The best-effort Providers.Gemini.AuthMethod = "oauth" write must
			// have run and must have persisted: in the isolated LELE_CONFIG_DIR
			// (fresh, so loadConfig returns the default) SaveConfig creates
			// config.json and, because an explicit auth method counts as provider
			// configuration, MarshalJSON keeps the gemini entry despite there
			// being no API key or models — no pkg/config change is needed.
			// Reloading the file is therefore a real end-to-end assertion.
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

// TestAuthLoginGeminiOAuthPasteCode verifies that `--paste-code` on the Gemini
// preset is accepted — the shared guard must allow it, exactly as it does for
// Claude — and routes to the headless paste-code seam, never to the browser
// flow. It runs the real command on purpose: the guard lives in authLoginCmd,
// so driving authLoginGeminiOAuth directly would not exercise it.
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

// TestAuthLogoutGeminiClearsAuthMethod pins the logout side of the Gemini
// wiring: the canonical name resolves through the registry alias and the
// best-effort switch blanks Providers.Gemini.AuthMethod — and, because
// ProvidersConfig serializes the named copy of the provider whenever it holds
// data, the named copy too, so the clearing actually reaches the file (see
// setNamedAuthMethod). The API key on the same entry must survive: logout
// clears the auth *method*, it does not delete the user's provider config.
//
// LELE_CONFIG_DIR is redirected to a temp dir, which also isolates the legacy
// auth.json credential store (authFilePath() lives under GetLeleDir(), and no
// cmd/lele test installs a SQLite store), so the seeded credential is deleted
// from the temp dir and the developer's real store is never touched — there is
// no cmd/lele seam for auth.DeleteCredential to stub.
func TestAuthLogoutGeminiClearsAuthMethod(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())

	if got := canonicalProviderName("gemini-cli"); got != "gemini" {
		t.Fatalf("canonicalProviderName(%q) = %q, want %q", "gemini-cli", got, "gemini")
	}

	// Seed the state logout must clear: a stored credential and the config key
	// authLoginGeminiOAuth writes (next to an API key that must not be touched).
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
	// Precondition: the seeded file really carries the auth method, otherwise
	// the assertion below would pass vacuously.
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

	// The alias is resolved before deletion and before the message, so the
	// canonical key is both what was deleted and what the user is told.
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

// seedConfigOnDisk loads the config exactly like production does (loadConfig),
// applies mutate and writes the result back with the real config.SaveConfig.
//
// It exists so a test can start from a config file that ALREADY carries data
// for a provider: that is the state in which ProvidersConfig.MarshalJSON
// serializes the *named* copy of the provider and early-returns before it ever
// looks at the typed field, which is precisely the state the auth login/logout
// writes must survive. A fresh t.TempDir() cannot catch a regression here: with
// no provider entry on disk the typed field is written to the file anyway.
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

// reloadConfigFromDisk re-reads the config file written by the command under
// test and fails when the file is unreadable or lost its providers section (the
// latter is what LoadConfig does when the file has no "providers" key at all,
// so it also guards against a command that dropped the section).
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

// TestAuthLoginGeminiOAuthPersistsAuthMethodOverExistingNamedEntry is the
// regression test for the shadowing defect the fresh-config tests cannot see:
// when providers.gemini already exists on disk with data (here an api_key),
// MarshalJSON serializes the named copy Providers.Named["gemini"] and then
// early-returns for the typed field Providers.Gemini, so a login that writes
// ONLY the typed field silently loses auth_method = "oauth" on disk — and after
// a restart the provider selector does not pick the Gemini Code Assist wire
// contract, without any warning being printed (SaveConfig succeeded).
//
// It drives the real authLoginCmd so the provider dispatch is exercised too.
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

	// The pre-existing gemini entry: an api_key alone is enough for MarshalJSON
	// to keep the *named* copy (and therefore to drop the typed write), and it
	// must survive the login untouched.
	seedConfigOnDisk(t, func(c *config.Config) { c.Providers.Gemini.APIKey = "keep-me" })

	// Precondition: the seeded file really carries the entry, otherwise the
	// assertion below would pass vacuously.
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

// TestAuthLoginClaudeOAuthPersistsAuthMethodOverExistingNamedEntry pins the very
// same contract for the Claude OAuth flow: with a providers.anthropic entry
// already on disk, writing only Providers.Anthropic.AuthMethod is lost, because
// MarshalJSON serializes Providers.Named["anthropic"] and early-returns for the
// typed field. pkg/providers/factory.go selects the Claude OAuth transport from
// named.AuthMethod (selectionFromNamedProvider), so the lost write means the
// next run keeps failing with "no API key configured for model".
//
// This is a latent defect of the F1 commit that introduced the flow
// (898bfdbc, unmerged and unpushed when this test was written), fixed here.
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

	// Pre-existing anthropic entry carrying data but no auth method: MarshalJSON
	// keeps the named copy (api_key) and drops the typed-only write.
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

// TestAuthLogoutAnthropicClearsPersistedAuthMethod pins the logout side for
// anthropic: with providers.anthropic.auth_method = "oauth" already on disk
// (next to an api_key that must survive), `auth logout --provider claude`
// (alias -> anthropic) must blank the value ON DISK. Blanking only the typed
// field would leave it behind, because MarshalJSON serializes the named copy and
// early-returns — and the next gateway start would still pick the Claude OAuth
// wire with a deleted credential.
func TestAuthLogoutAnthropicClearsPersistedAuthMethod(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())

	if got := canonicalProviderName("claude"); got != "anthropic" {
		t.Fatalf("canonicalProviderName(%q) = %q, want %q", "claude", got, "anthropic")
	}

	// Seed the state logout must clear: a stored credential plus the config key
	// the Claude login writes (next to an API key that must not be touched).
	if err := auth.SetCredential("anthropic", &auth.AuthCredential{AccessToken: "tok", Provider: "anthropic"}); err != nil {
		t.Fatalf("auth.SetCredential(%q, …) error = %v", "anthropic", err)
	}
	seedConfigOnDisk(t, func(c *config.Config) {
		c.Providers.Anthropic.APIKey = "keep-me"
		c.Providers.Anthropic.AuthMethod = "oauth"
	})
	// Precondition: the seeded file really carries the auth method, otherwise
	// the assertion below would pass vacuously.
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

// TestAuthLogoutAllClearsPersistedGeminiAndAnthropicAuthMethod pins the
// provider-less `auth logout`: it deletes every credential, so it must not leave
// providers.gemini.auth_method = "oauth" or providers.anthropic.auth_method =
// "oauth" behind on disk (both entries already carry data, so only the named
// copy reaches the file — see setNamedAuthMethod). Otherwise the next gateway
// start still selects an OAuth transport whose credential was just deleted.
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
	// Precondition: the seeded file really carries both auth methods, otherwise
	// the assertions below would pass vacuously.
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

// TestSetNamedAuthMethod covers the helper's edge cases directly: a nil
// ProvidersConfig and a blank name must be no-ops (never a panic), an existing
// entry must keep every field but auth_method, a missing entry for a known
// provider must be seeded from the typed field, and an unknown provider must
// still get its auth_method written.
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

// nilProvidersShapes are the two on-disk configs that make LoadConfig leave
// Config.Providers nil.
//
// Both are normal states, not corrupted files: the loader nils the section when
// the JSON has no `providers` key ("If providers were not in the JSON, set to
// nil so it gets omitted on save"), and ProvidersConfig.MarshalJSON writes
// `"providers": null` for a section without data — so lele itself produces the
// second shape. Every auth write path must survive both (see providersForWrite).
var nilProvidersShapes = []struct {
	name string
	body string
}{
	{name: "providers key absent", body: "{}"},
	{name: "providers explicit null", body: `{"providers":null}`},
}

// writeRawConfig writes a literal config.json into the isolated
// LELE_CONFIG_DIR under test.
//
// It is needed because the real config writer cannot produce a nil providers
// section from scratch: a fresh t.TempDir() gets default providers, and the
// shapes under test here only exist on disk (they are what the loader reads
// back as nil). Writing the file by hand mirrors how the SIGSEGV was
// reproduced from the CLI.
func writeRawConfig(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(getConfigPath(), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", getConfigPath(), err)
	}
}

// assertNilProvidersPrecondition fails unless the config just written really
// loads with a nil providers section. Without it the nil-safety assertions
// below could pass vacuously (e.g. if a future loader stopped nil-ing the
// section, the tests would silently stop covering the panic).
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

// providersSectionOf returns the raw `providers` value of the persisted config,
// or nil when the key is absent or literally null.
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

// assertProvidersStillAbsentOrNull pins the serialization contract of a write
// against a nil providers section: the persisted file must keep the section
// absent or null (the two forms the loader/saver produce today) and must never
// grow an empty providers object with fabricated entries.
func assertProvidersStillAbsentOrNull(t *testing.T) {
	t.Helper()
	section := providersSectionOf(t)
	if len(section) == 0 || string(section) == "null" {
		return
	}
	t.Errorf("persisted providers = %s, want it absent or null: a write to a nil section must not fabricate an empty providers object", section)
}

// assertSoleProviderEntry fails unless the persisted providers section holds
// exactly one entry — provider — carrying the expected auth_method.
//
// It is the positive counterpart of assertProvidersStillAbsentOrNull: for the
// login flows, which do write real data, it proves the nil-safe write wrote
// that entry and invented no other.
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

// stubSetCredential swaps the setCredential seam for a fake that records the
// provider key instead of touching the credential store.
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

// withLogoutArgs points os.Args at an `auth logout` invocation for the duration
// of the test, so authLogoutCmd() reads the flags under test.
func withLogoutArgs(t *testing.T, args ...string) {
	t.Helper()
	original := os.Args
	os.Args = append([]string{"lele", "auth", "logout"}, args...)
	t.Cleanup(func() { os.Args = original })
}

// TestAuthLogoutNilProvidersSectionDoesNotPanic is the regression test for the
// SIGSEGV `lele auth logout --provider <p>` used to raise on a config that has
// no providers section (`{}` or `{"providers":null}`):
//
//	panic: runtime error: invalid memory address or nil pointer dereference
//	[signal SIGSEGV: segmentation violation ...]
//	main.authLogoutCmd() cmd/lele/auth.go:655
//
// Config.Providers is a pointer the loader deliberately nils out, and every
// logout branch used to dereference it unconditionally. The four --provider
// spellings below are the ones the alias resolution reaches an
// auth_method-clearing branch through (`claude` being the alias of the
// `anthropic` preset), and each is exercised against both shapes of the
// section.
//
// The CLI output and exit path must stay exactly as they are for a config that
// does have a section: one line naming the canonical provider, exit 0.
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

				// The panic used to happen inside this call, before any output.
				out := captureOutput(authLogoutCmd)

				if want := "Logged out from " + provider.canonical + "\n"; out != want {
					t.Errorf("authLogoutCmd() output = %q, want %q", out, want)
				}
				assertProvidersStillAbsentOrNull(t)
			})
		}
	}
}

// TestAuthLogoutAllNilProvidersSectionDoesNotPanic covers the provider-less
// `auth logout`, which writes all three typed fields (auth.go,
// authLogoutCmd's else branch) and therefore panicked on a nil section too.
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

// TestAuthLoginNilProvidersSectionDoesNotPanic covers the login-side writes on a
// config with no providers section — the state a fresh install starts in, and
// the one the Gemini/Claude OAuth happy path introduced on this branch ends in
// (authLoginGeminiOAuth / authLoginClaudeOAuth write an auth_method right after
// storing the credential).
//
// No network is involved: every flow is driven through its seam, exactly like
// the pre-existing login tests, and the credential store is stubbed. Each
// subtest asserts the real write reached the file and that nothing else was
// invented.
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

		// The credential key is the canonical preset name, not the alias.
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

// TestProvidersForWriteAllocatesOnDemand pins the helper itself: it must never
// return nil, must return the existing section untouched when there is one, and
// must allocate a section that still serializes as "null" (so allocating cannot
// invent provider entries).
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
