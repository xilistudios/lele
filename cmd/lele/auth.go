package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/config"
)

// loginKind identifies which login implementation must handle a --provider
// value. The zero value is loginKindUnsupported on purpose: an unresolved
// provider can never accidentally start a flow (fail closed).
type loginKind int

const (
	// loginKindUnsupported is returned (together with an error) when the
	// provider is unknown or when the requested flow is not available for it.
	loginKindUnsupported loginKind = iota
	// loginKindOpenAI runs the OpenAI OAuth flow (browser or --device-code).
	loginKindOpenAI
	// loginKindPasteToken runs the manual paste-token flow (anthropic).
	loginKindPasteToken
	// loginKindBrowserPreset runs the browser OAuth flow with a preset
	// resolved from the auth registry (today: google-antigravity).
	loginKindBrowserPreset
)

// Login-flow seams.
//
// They exist so the unit tests can drive the login commands without a network,
// without touching the real credential store and — crucially — without killing
// the test binary: loginExit replaces os.Exit (a fake records the status code)
// while loginBrowser/pasteToken/setCredential replace the network and stdio
// side effects of the flows.
var (
	// loginExit is the seam used by the login commands' error paths: tests replace
	// it to assert the exit status without terminating the test binary.
	loginExit = os.Exit
	// loginBrowser is the seam for the interactive browser OAuth flow.
	loginBrowser = auth.LoginBrowser
	// pasteToken is the seam for the manual paste-token flow (reads from stdin).
	pasteToken = auth.LoginPasteToken
	// setCredential is the seam for persisting a credential under its provider key.
	setCredential = auth.SetCredential
)

// normalizeProviderName lowercases and trims a --provider value.
//
// It is used for the values the OAuth registry does not own: the registry
// normalizes internally (auth.OAuthProviderConfigByName), but the paste-token
// provider (`anthropic`) is resolved here and its name is also used verbatim as
// the credential key, so the CLI has to normalize it itself. Otherwise
// `--provider " ANTHROPIC "` would be rejected (or store a credential under a
// key no reader ever looks up) while `--provider " OPENAI "` works.
func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// parseAuthSubcommand extracts the subcommand from auth arguments for testability.
func parseAuthSubcommand(args []string) string {
	if len(args) < 1 {
		return ""
	}
	return args[0]
}

// parseLoginArgs extracts the login argument parsing logic for testability.
// Returns: provider, useDeviceCode.
func parseLoginArgs(args []string) (provider string, useDeviceCode bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--provider", "-p":
			if i+1 < len(args) {
				provider = args[i+1]
				i++
			}
		case "--device-code":
			useDeviceCode = true
		}
	}
	return provider, useDeviceCode
}

// parseLogoutArgs extracts the logout argument parsing logic for testability.
func parseLogoutArgs(args []string) (provider string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--provider", "-p":
			if i+1 < len(args) {
				provider = args[i+1]
				i++
			}
		}
	}
	return provider
}

func authCmd() {
	if len(os.Args) < 3 {
		authHelp()
		return
	}

	// Wire SQLite store when available so auth commands read/write the
	// same database as the running server. When the database cannot be
	// opened (unsupported platform, corruption, …) the CLI intentionally
	// falls back to auth.json, mirroring the server's fallback semantics.
	s, cleanup, _ := openSharedStore(defaultDBPath(), "auth-cli")
	defer cleanup() // no-op when the store could not be opened
	if s != nil {
		auth.UseStore(s.Auth())
	}

	switch os.Args[2] {
	case "login":
		authLoginCmd()
	case "logout":
		authLogoutCmd()
	case "status":
		authStatusCmd()
	default:
		fmt.Printf("Unknown auth command: %s\n", os.Args[2])
		authHelp()
	}
}

func authHelp() {
	fmt.Println("\nAuth commands:")
	fmt.Println("  login       Login via OAuth or paste token")
	fmt.Println("  logout      Remove stored credentials")
	fmt.Println("  status      Show current auth status")
	fmt.Println()
	fmt.Println("Login options:")
	fmt.Printf("  --provider <name>    Provider to login with (%s)\n", supportedProviders())
	fmt.Println("  --device-code        Use device code flow (openai only, for headless environments)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  lele auth login --provider openai")
	fmt.Println("  lele auth login --provider openai --device-code")
	fmt.Println("  lele auth login --provider anthropic")
	fmt.Println("  lele auth login --provider google-antigravity")
	fmt.Println("  lele auth login --provider antigravity   (alias of google-antigravity)")
	fmt.Println("  lele auth logout --provider openai")
	fmt.Println("  lele auth status")
}

// supportedProviders returns the provider names `auth login` accepts, as a
// single string ready to be embedded in help/error text.
//
// The OAuth names come from the registry (auth.OAuthProviderNames) so they can
// never drift from the presets that actually exist; "anthropic" is appended by
// hand because it is not an OAuth preset: it uses the paste-token flow.
func supportedProviders() string {
	return strings.Join(auth.OAuthProviderNames(), ", ") + ", anthropic"
}

// unsupportedProviderError builds the user-facing error for an unknown
// --provider value, listing the providers derived from the registry.
func unsupportedProviderError(provider string) error {
	return fmt.Errorf("Unsupported provider: %s\nSupported providers: %s", provider, supportedProviders())
}

// resolveLoginProvider maps a --provider value to the login implementation that
// must handle it. It is pure (no I/O, no global state besides the read-only auth
// registry), which is what makes the CLI's provider dispatch unit-testable.
//
// Any returned error already carries every user-facing line the CLI has to
// print (including the supported-provider list derived from
// auth.OAuthProviderNames), so the caller only needs to print it.
//
// `anthropic` does not go through the registry (it is not an OAuth preset) but
// its name is normalized with normalizeProviderName before being matched, so it
// accepts the same spellings as the registry-backed providers: `anthropic`,
// `ANTHROPIC` and `  Anthropic  ` all resolve to the same paste-token flow.
// `--provider anthropic` keeps being exactly what it was before (same flow, same
// credential key, same message). Every other name is normalized by the registry,
// so `openai` keeps its exact flow while `OPENAI`/`codex` now resolve to it as
// well.
func resolveLoginProvider(name string, useDeviceCode bool) (loginKind, auth.OAuthProviderConfig, error) {
	if normalizeProviderName(name) == "anthropic" {
		return loginKindPasteToken, auth.OAuthProviderConfig{}, nil
	}

	cfg, ok := auth.OAuthProviderConfigByName(name)
	if !ok {
		return loginKindUnsupported, auth.OAuthProviderConfig{}, unsupportedProviderError(name)
	}

	if cfg.Name == "openai" {
		// The OpenAI flow owns its preset (authLoginOpenAI builds it itself, so
		// the browser/device-code output stays byte-identical to the pre-refactor
		// behavior); the resolved config is returned for symmetry only.
		return loginKindOpenAI, cfg, nil
	}

	if useDeviceCode {
		// Silently ignoring the flag would make the CLI look like it honored
		// --device-code while actually starting an interactive browser flow.
		return loginKindUnsupported, auth.OAuthProviderConfig{}, fmt.Errorf(
			"device code flow is not available for %s", cfg.Name)
	}

	return loginKindBrowserPreset, cfg, nil
}

func authLoginCmd() {
	provider, useDeviceCode := parseLoginArgs(os.Args[3:])

	if provider == "" {
		fmt.Println("Error: --provider is required")
		fmt.Printf("Supported providers: %s\n", supportedProviders())
		loginExit(1)
		return
	}

	kind, cfg, err := resolveLoginProvider(provider, useDeviceCode)
	if err != nil {
		fmt.Println(err)
		loginExit(1)
		return
	}

	switch kind {
	case loginKindOpenAI:
		authLoginOpenAI(useDeviceCode)
	case loginKindPasteToken:
		// The normalized name is what the flow receives, and therefore what
		// becomes the credential key: `--provider " ANTHROPIC "` stores
		// `anthropic`.
		authLoginPasteToken(normalizeProviderName(provider))
	case loginKindBrowserPreset:
		authLoginBrowserPreset(cfg)
	default:
		// loginKindUnsupported: resolveLoginProvider always returns an error with
		// it, so this is only a fail-closed guard against a future kind being
		// added without a matching branch.
		fmt.Println(unsupportedProviderError(provider))
		loginExit(1)
	}
}

func authLoginOpenAI(useDeviceCode bool) {
	cfg := auth.OpenAIOAuthConfig()

	var cred *auth.AuthCredential
	var err error

	if useDeviceCode {
		cred, err = auth.LoginDeviceCode(cfg)
	} else {
		cred, err = loginBrowser(cfg)
	}

	if err != nil {
		fmt.Printf("Login failed: %v\n", err)
		loginExit(1)
		return
	}

	if err := setCredential("openai", cred); err != nil {
		fmt.Printf("Failed to save credentials: %v\n", err)
		loginExit(1)
		return
	}

	appCfg, err := loadConfig()
	if err == nil {
		appCfg.Providers.OpenAI.AuthMethod = "oauth"
		if err := config.SaveConfig(getConfigPath(), appCfg); err != nil {
			fmt.Printf("Warning: could not update config: %v\n", err)
		}
	}

	fmt.Println("Login successful!")
	if cred.AccountID != "" {
		fmt.Printf("Account: %s\n", cred.AccountID)
	}
}

// authLoginBrowserPreset runs the browser OAuth flow for a preset resolved from
// the registry (i.e. any registered provider that is not `openai`) and stores the
// credential under the provider's canonical name.
//
// Unlike authLoginOpenAI it deliberately does NOT write AuthMethod into the
// config: ProvidersConfig has no field for these providers (today
// google-antigravity is selected by provider/model name in pkg/providers and
// reads its credential by name), so there is nothing to persist.
func authLoginBrowserPreset(cfg auth.OAuthProviderConfig) {
	cred, err := loginBrowser(cfg)
	if err != nil {
		fmt.Printf("Login failed: %v\n", err)
		loginExit(1)
		return
	}

	// cfg.Name is the canonical registry name (e.g. "google-antigravity"): it is
	// the key pkg/providers looks the credential up by, so it must be stored
	// under that exact value and never under the user-typed alias.
	if err := setCredential(cfg.Name, cred); err != nil {
		fmt.Printf("Failed to save credentials: %v\n", err)
		loginExit(1)
		return
	}

	fmt.Println("Login successful!")
	if cred.AccountID != "" {
		fmt.Printf("Account: %s\n", cred.AccountID)
	}
}

// authLoginPasteToken runs the manual paste-token flow for provider (already
// normalized by authLoginCmd: the name is used as both the prompt selector and
// the credential key, so it must be the canonical spelling).
func authLoginPasteToken(provider string) {
	cred, err := pasteToken(provider, os.Stdin)
	if err != nil {
		fmt.Printf("Login failed: %v\n", err)
		loginExit(1)
		return
	}

	if err := setCredential(provider, cred); err != nil {
		fmt.Printf("Failed to save credentials: %v\n", err)
		loginExit(1)
		return
	}

	appCfg, err := loadConfig()
	if err == nil {
		switch provider {
		case "anthropic":
			appCfg.Providers.Anthropic.AuthMethod = "token"
		case "openai":
			appCfg.Providers.OpenAI.AuthMethod = "token"
		}
		if err := config.SaveConfig(getConfigPath(), appCfg); err != nil {
			fmt.Printf("Warning: could not update config: %v\n", err)
		}
	}

	fmt.Printf("Token saved for %s!\n", provider)
}

func authLogoutCmd() {
	provider := parseLogoutArgs(os.Args[3:])

	if provider != "" {
		if err := auth.DeleteCredential(provider); err != nil {
			fmt.Printf("Failed to remove credentials: %v\n", err)
			os.Exit(1)
		}

		appCfg, err := loadConfig()
		if err == nil {
			switch provider {
			case "openai":
				appCfg.Providers.OpenAI.AuthMethod = ""
			case "anthropic":
				appCfg.Providers.Anthropic.AuthMethod = ""
			}
			config.SaveConfig(getConfigPath(), appCfg)
		}

		fmt.Printf("Logged out from %s\n", provider)
	} else {
		if err := auth.DeleteAllCredentials(); err != nil {
			fmt.Printf("Failed to remove credentials: %v\n", err)
			os.Exit(1)
		}

		appCfg, err := loadConfig()
		if err == nil {
			appCfg.Providers.OpenAI.AuthMethod = ""
			appCfg.Providers.Anthropic.AuthMethod = ""
			config.SaveConfig(getConfigPath(), appCfg)
		}

		fmt.Println("Logged out from all providers")
	}
}

func authStatusCmd() {
	store, err := auth.LoadStore()
	if err != nil {
		fmt.Printf("Error loading auth store: %v\n", err)
		return
	}

	if len(store.Credentials) == 0 {
		fmt.Println("No authenticated providers.")
		fmt.Println("Run: lele auth login --provider <name>")
		return
	}

	fmt.Println("\nAuthenticated Providers:")
	fmt.Println("------------------------")
	for provider, cred := range store.Credentials {
		status := "active"
		if cred.IsExpired() {
			status = "expired"
		} else if cred.NeedsRefresh() {
			status = "needs refresh"
		}

		fmt.Printf("  %s:\n", provider)
		fmt.Printf("    Method: %s\n", cred.AuthMethod)
		fmt.Printf("    Status: %s\n", status)
		if cred.AccountID != "" {
			fmt.Printf("    Account: %s\n", cred.AccountID)
		}
		if !cred.ExpiresAt.IsZero() {
			fmt.Printf("    Expires: %s\n", cred.ExpiresAt.Format("2006-01-02 15:04"))
		}
	}
}
