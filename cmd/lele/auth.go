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
	// loginKindClaudeOAuth runs the Claude.ai OAuth flow (browser with
	// paste-code fallback, or --paste-code for headless environments). It is
	// reached through the `claude` alias of the `anthropic` preset.
	loginKindClaudeOAuth
	// loginKindGeminiOAuth runs the Gemini / Google Code Assist OAuth flow
	// (browser with paste-code fallback, or --paste-code for headless
	// environments). It is reached through the `gemini` preset and its
	// `gemini-cli` alias.
	loginKindGeminiOAuth
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
	// loginClaudePasteCode is the seam for the headless Claude paste-code flow
	// (--paste-code; reads from stdin).
	loginClaudePasteCode = auth.LoginPasteCode
	// loginClaudeBrowser is the seam for the Claude browser flow with graceful
	// paste-code degradation for headless environments.
	loginClaudeBrowser = auth.LoginBrowserWithPasteFallback
	// loginGeminiPasteCode is the seam for the headless Gemini paste-code flow
	// (--paste-code; reads from stdin).
	loginGeminiPasteCode = auth.LoginPasteCode
	// loginGeminiBrowser is the seam for the Gemini browser flow with graceful
	// paste-code degradation for headless environments.
	loginGeminiBrowser = auth.LoginBrowserWithPasteFallback
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

// loginArgs holds the parsed `auth login` flags.
type loginArgs struct {
	// Provider is the raw --provider/-p value (not normalized: 'claude' and
	// 'anthropic' are different flows and normalization happens later).
	Provider string
	// DeviceCode requests the RFC 8628 device code flow (openai only).
	DeviceCode bool
	// PasteCode requests the headless paste-code flow (claude and gemini only).
	PasteCode bool
}

// parseLoginArgs extracts the login argument parsing logic for testability.
func parseLoginArgs(args []string) loginArgs {
	var parsed loginArgs
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--provider", "-p":
			if i+1 < len(args) {
				parsed.Provider = args[i+1]
				i++
			}
		case "--device-code":
			parsed.DeviceCode = true
		case "--paste-code":
			parsed.PasteCode = true
		}
	}
	return parsed
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
	fmt.Println("  --provider claude    Claude OAuth (alias of the anthropic preset; use --provider anthropic to paste a token)")
	fmt.Println("  --provider codex     OpenAI OAuth (alias of the openai preset; same flow as --provider openai)")
	fmt.Println("  --device-code        Use device code flow (openai only, for headless environments)")
	fmt.Println("  --paste-code         Paste the callback code/URL manually (claude and gemini)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  lele auth login --provider openai")
	fmt.Println("  lele auth login --provider openai --device-code")
	fmt.Println("  lele auth login --provider anthropic")
	fmt.Println("  lele auth login --provider claude")
	fmt.Println("  lele auth login --provider claude --paste-code")
	fmt.Println("  lele auth login --provider gemini")
	fmt.Println("  lele auth login --provider gemini --paste-code")
	fmt.Println("  lele auth login --provider gemini-cli   (alias of gemini)")
	fmt.Println("  lele auth login --provider google-antigravity")
	fmt.Println("  lele auth login --provider antigravity   (alias of google-antigravity)")
	fmt.Println("  lele auth logout --provider openai")
	fmt.Println("  lele auth status")
}

// supportedProviders returns the provider names `auth login` accepts, as a
// single string ready to be embedded in help/error text.
//
// The names come from the registry (auth.OAuthProviderNames) so they can never
// drift from the presets that actually exist. `anthropic` is the registry key of
// the Claude OAuth preset, but the two spellings drive different flows:
// `--provider anthropic` pastes a token (the intercept in resolveLoginProvider)
// while `--provider claude` (an alias of the same preset) runs the Claude OAuth
// flow, so `anthropic` is advertised once and never appended by hand.
func supportedProviders() string {
	return strings.Join(auth.OAuthProviderNames(), ", ")
}

// unsupportedProviderError builds the user-facing error for an unknown
// --provider value, listing the providers derived from the registry.
func unsupportedProviderError(provider string) error {
	return fmt.Errorf("Unsupported provider: %s\nSupported providers: %s", provider, supportedProviders())
}

// deviceCodeUnavailableError builds the rejection for a provider that does not
// implement the RFC 8628 device code flow (only `openai` does, and it is
// dispatched before this error can be reached).
//
// It is the single source of that message so the `anthropic` intercept and the
// registry branch cannot drift apart: `--provider anthropic --device-code` and
// `--provider claude --device-code` reject the flag with the very same line,
// because both describe the same preset (provider is always the canonical
// name, never the spelling the user typed).
func deviceCodeUnavailableError(provider string) error {
	return fmt.Errorf("device code flow is not available for %s", provider)
}

// resolveLoginProvider maps a --provider value to the login implementation that
// must handle it. It is pure (no I/O, no global state besides the read-only auth
// registry), which is what makes the CLI's provider dispatch unit-testable.
//
// Any returned error already carries every user-facing line the CLI has to
// print (including the supported-provider list derived from
// auth.OAuthProviderNames), so the caller only needs to print it.
//
// The verbatim `anthropic` spelling is intercepted before the registry lookup
// so it keeps the legacy paste-token flow, even though the registry does own the
// `anthropic` preset (the Claude OAuth one): only the `claude` alias reaches the
// registry branch, and that branch maps the preset to the Claude OAuth flow. The
// intercept still normalizes the name with normalizeProviderName before matching
// it, so it accepts the same spellings as the registry-backed providers:
// `anthropic`, `ANTHROPIC` and `  Anthropic  ` all resolve to the same
// paste-token flow.
// `--provider anthropic` keeps being exactly what it was before (same flow, same
// credential key, same message). Every other name is normalized by the registry,
// so `openai` keeps its exact flow while `OPENAI`/`codex` now resolve to it as
// well.
//
// The registry branch maps the `anthropic` preset (reached through the `claude`
// alias, since the verbatim `anthropic` spelling is intercepted above) to the
// Claude OAuth flow rather than the generic browser-preset flow.
func resolveLoginProvider(name string, useDeviceCode bool) (loginKind, auth.OAuthProviderConfig, error) {
	if normalizeProviderName(name) == "anthropic" {
		// The intercept must not swallow --device-code: the flag is openai-only,
		// and silently ignoring it would look like the CLI had honored it while
		// it actually prompts for a token. Reject it with the same message the
		// registry branch below uses so `anthropic` and `claude` (the alias that
		// reaches it) behave identically — fail closed, never a silent ignore.
		if useDeviceCode {
			return loginKindUnsupported, auth.OAuthProviderConfig{}, deviceCodeUnavailableError("anthropic")
		}
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
		return loginKindUnsupported, auth.OAuthProviderConfig{}, deviceCodeUnavailableError(cfg.Name)
	}

	if cfg.Name == "anthropic" {
		// `claude` (and any other alias of the anthropic preset that is not the
		// verbatim `anthropic` spelling intercepted above) runs the Claude OAuth
		// flow. Credentials are still stored under cfg.Name ("anthropic").
		return loginKindClaudeOAuth, cfg, nil
	}

	if cfg.Name == "gemini" {
		// The Gemini / Google Code Assist flow is reached through the `gemini`
		// preset and its `gemini-cli` alias. Like Claude it owns its preset
		// instead of falling into the generic browser-preset branch: that branch
		// stores the credential but writes nothing to the config, so the flow
		// would work while pkg/providers could never select the Gemini OAuth
		// wire contract. Credentials are still stored under cfg.Name ("gemini").
		return loginKindGeminiOAuth, cfg, nil
	}

	return loginKindBrowserPreset, cfg, nil
}

// canonicalProviderName resolves a user-supplied provider name to the canonical
// name used as the credential-store key, the config selector and the name echoed
// back in the CLI's messages.
//
// It is normalizeProviderName plus the registry alias resolution, so
// `claude`/` CLAUDE ` → `anthropic`, `codex` → `openai` and `antigravity` →
// `google-antigravity`, while an unknown name is returned normalized but
// otherwise untouched (callers that need to reject it do so separately).
func canonicalProviderName(name string) string {
	if cfg, ok := auth.OAuthProviderConfigByName(name); ok {
		return cfg.Name
	}
	return normalizeProviderName(name)
}

func authLoginCmd() {
	args := parseLoginArgs(os.Args[3:])
	provider := args.Provider

	if provider == "" {
		fmt.Println("Error: --provider is required")
		fmt.Printf("Supported providers: %s\n", supportedProviders())
		loginExit(1)
		return
	}

	kind, cfg, err := resolveLoginProvider(provider, args.DeviceCode)
	if err != nil {
		fmt.Println(err)
		loginExit(1)
		return
	}

	if args.PasteCode && kind != loginKindClaudeOAuth && kind != loginKindGeminiOAuth {
		// Fail closed rather than silently ignoring the flag: only the Claude and
		// Gemini OAuth flows implement the headless paste-code login.
		fmt.Println("--paste-code is only supported for the Claude and Gemini OAuth flows; use: lele auth login --provider claude --paste-code")
		loginExit(1)
		return
	}

	switch kind {
	case loginKindOpenAI:
		authLoginOpenAI(args.DeviceCode)
	case loginKindPasteToken:
		// The normalized name is what the flow receives, and therefore what
		// becomes the credential key: `--provider " ANTHROPIC "` stores
		// `anthropic`.
		authLoginPasteToken(normalizeProviderName(provider))
	case loginKindClaudeOAuth:
		authLoginClaudeOAuth(cfg, args.PasteCode)
	case loginKindGeminiOAuth:
		authLoginGeminiOAuth(cfg, args.PasteCode)
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
		providersForWrite(appCfg).OpenAI.AuthMethod = "oauth"
		if err := config.SaveConfig(getConfigPath(), appCfg); err != nil {
			fmt.Printf("Warning: could not update config: %v\n", err)
		}
	}

	fmt.Println("Login successful!")
	if cred.AccountID != "" {
		fmt.Printf("Account: %s\n", cred.AccountID)
	}
}

// authLoginClaudeOAuth runs the Claude.ai OAuth flow for the `anthropic` preset
// (reached through the `claude` alias) and stores the credential under the
// canonical key, cfg.Name ("anthropic").
//
// pasteCode selects the headless flow (loginClaudePasteCode, which reads one
// line from stdin); otherwise the browser flow runs with a graceful paste-code
// fallback (loginClaudeBrowser). Both are seams so the tests can drive the
// command without a network or a credential store.
func authLoginClaudeOAuth(cfg auth.OAuthProviderConfig, pasteCode bool) {
	var cred *auth.AuthCredential
	var err error

	if pasteCode {
		cred, err = loginClaudePasteCode(cfg, os.Stdin)
	} else {
		cred, err = loginClaudeBrowser(cfg, os.Stdin)
	}

	if err != nil {
		fmt.Printf("Login failed: %v\n", err)
		loginExit(1)
		return
	}

	if err := setCredential(cfg.Name, cred); err != nil {
		fmt.Printf("Failed to save credentials: %v\n", err)
		loginExit(1)
		return
	}

	// Best effort: the credential is already stored, so a config-write failure
	// only warns (the same pattern authLoginOpenAI uses).
	if appCfg, err := loadConfig(); err == nil {
		providers := providersForWrite(appCfg)
		providers.Anthropic.AuthMethod = "oauth"
		// The persisted file is written from the *named* copy as soon as the
		// config already has an anthropic entry (an api key, models or an auth
		// method), because MarshalJSON then early-returns for the typed field set
		// above — so without this sync the "oauth" write would be silently lost
		// for exactly the users who already configured anthropic, and the next
		// gateway start would not pick the Claude OAuth transport
		// (pkg/providers/factory.go selects it from named.AuthMethod). A latent
		// defect of the F1 commit that introduced this flow (898bfdbc, unmerged
		// and unpushed when it was fixed): it is in scope here.
		setNamedAuthMethod(providers, "anthropic", "oauth")
		if err := config.SaveConfig(getConfigPath(), appCfg); err != nil {
			fmt.Printf("Warning: could not update config: %v\n", err)
			// Actionable follow-up for the warning above: without
			// auth_method = "oauth" in the config, the provider selector in
			// pkg/providers/factory.go will not pick the Claude OAuth provider
			// for the stored credential and the next model call fails with a
			// confusing "no API key configured for model". Name the exact key
			// and the exact file so the user can fix it by hand.
			fmt.Printf("Set providers.anthropic.auth_method = \"oauth\" in %s for the Claude OAuth login to be used.\n", getConfigPath())
		}
	}

	fmt.Println("Login successful!")
	if cred.AccountID != "" {
		fmt.Printf("Account: %s\n", cred.AccountID)
	}
}

// authLoginGeminiOAuth runs the Gemini / Google Code Assist OAuth flow for the
// `gemini` preset (reached directly or through the `gemini-cli` alias) and
// stores the credential under the canonical key, cfg.Name ("gemini").
//
// pasteCode selects the headless flow (loginGeminiPasteCode, which reads one
// line from stdin); otherwise the browser flow runs with a graceful paste-code
// fallback (loginGeminiBrowser). Both are seams so the tests can drive the
// command without a network or a credential store. The flow itself is the same
// generic one Claude uses — only the preset and the config key differ, which is
// why this is a sibling of authLoginClaudeOAuth rather than a shared helper:
// the two write different Providers fields, and keeping them separate keeps
// each provider's message byte-identical to what its tests pin.
func authLoginGeminiOAuth(cfg auth.OAuthProviderConfig, pasteCode bool) {
	var cred *auth.AuthCredential
	var err error

	if pasteCode {
		cred, err = loginGeminiPasteCode(cfg, os.Stdin)
	} else {
		cred, err = loginGeminiBrowser(cfg, os.Stdin)
	}

	if err != nil {
		fmt.Printf("Login failed: %v\n", err)
		loginExit(1)
		return
	}

	if err := setCredential(cfg.Name, cred); err != nil {
		fmt.Printf("Failed to save credentials: %v\n", err)
		loginExit(1)
		return
	}

	// Best effort: the credential is already stored, so a config-write failure
	// only warns (the same pattern authLoginOpenAI uses).
	if appCfg, err := loadConfig(); err == nil {
		providers := providersForWrite(appCfg)
		providers.Gemini.AuthMethod = "oauth"
		// The persisted file is written from the *named* copy as soon as the
		// config already has a gemini entry (an api key, models or an auth
		// method), because MarshalJSON then early-returns for the typed field set
		// above — so without this sync the "oauth" write would be silently lost
		// for exactly the users who already configured gemini (SaveConfig still
		// succeeds, so the warning below would never fire) and the provider
		// selector would not pick the Gemini Code Assist transport after a
		// restart. See setNamedAuthMethod.
		setNamedAuthMethod(providers, "gemini", "oauth")
		if err := config.SaveConfig(getConfigPath(), appCfg); err != nil {
			fmt.Printf("Warning: could not update config: %v\n", err)
			// Actionable follow-up for the warning above: without
			// auth_method = "oauth" in the config, the provider selector in
			// pkg/providers will not pick the Gemini Code Assist provider for
			// the stored credential and the next model call fails with a
			// confusing "no API key configured for model". Name the exact key
			// and the exact file so the user can fix it by hand.
			fmt.Printf("Set providers.gemini.auth_method = \"oauth\" in %s for the Gemini OAuth login to be used.\n", getConfigPath())
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
			providersForWrite(appCfg).Anthropic.AuthMethod = "token"
		case "openai":
			providersForWrite(appCfg).OpenAI.AuthMethod = "token"
		}
		if err := config.SaveConfig(getConfigPath(), appCfg); err != nil {
			fmt.Printf("Warning: could not update config: %v\n", err)
		}
	}

	fmt.Printf("Token saved for %s!\n", provider)
}

// providersForWrite returns the config's providers section, allocating an empty
// one on demand, so an auth write path can always mutate it safely.
//
// Config.Providers is a *ProvidersConfig and the loader deliberately nil-s out
// the section when the JSON file carries no `providers` key (see
// pkg/config/config.go, LoadConfig: "If providers were not in the JSON, set to
// nil so it gets omitted on save"). ProvidersConfig.MarshalJSON also writes
// "null" for a section that holds no data, so a nil section is a normal state
// produced by this very CLI and not a corrupt file. Every auth write
// (login/logout setting auth_method) must therefore go through this helper
// instead of dereferencing appCfg.Providers directly.
//
// Allocating an empty section invents nothing: an empty ProvidersConfig still
// serializes as "null" (MarshalJSON's hasData stays false because no entry has
// an api_key, models or an auth_method), so no provider entry with fabricated
// values can reach the file — the section is simply written back as null.
func providersForWrite(cfg *config.Config) *config.ProvidersConfig {
	if cfg.Providers == nil {
		cfg.Providers = &config.ProvidersConfig{}
	}
	return cfg.Providers
}

// setNamedAuthMethod sets the auth_method of the *named* copy of a provider in
// a ProvidersConfig, creating that copy when the config does not carry one yet.
//
// ProvidersConfig holds every built-in provider twice: in its typed field
// (Providers.Gemini) and in Providers.Named["gemini"], which UnmarshalJSON fills
// from the same JSON keys (`pkg/config/config.go`, UnmarshalJSON). MarshalJSON
// writes the Named copy as soon as it carries data (an API key, models or an
// explicit auth method) and then early-returns for the typed field of the same
// name, so writing the typed field alone never reaches the file for a provider
// that is already in the config — which is exactly the state the auth login and
// logout paths run in (the login wrote the entry a moment earlier, or the user's
// config already has an api key or models). Every auth_method write — setting or
// blanking it — must therefore be mirrored into Named to be persisted, and to be
// visible to the readers that select the wire contract: pkg/providers reads the
// typed field for gemini and named.AuthMethod for anthropic.
//
// Only auth_method is ever touched: an API key, API base, proxy, connect mode,
// models or web_search stored on the same entry belong to the user's
// configuration and survive both a login and a logout.
//
// name is matched case-insensitively and trimmed, exactly like the config
// helpers do; a blank name or a nil providers section is a no-op.
func setNamedAuthMethod(providers *config.ProvidersConfig, name, method string) {
	if providers == nil {
		return
	}
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return
	}
	// GetNamed seeds the named copy from the provider's typed field (through
	// ensureNamedDefaults) when the config does not carry one yet, so creating a
	// missing entry can never drop an api_key/api_base/proxy/connect_mode the
	// typed field holds. An entry that already exists is returned verbatim, which
	// preserves its models and web_search as well.
	named, ok := providers.GetNamed(key)
	if !ok {
		// Not a built-in provider: there is no typed field to seed from, so a
		// bare entry is created — auth_method alone is enough for MarshalJSON to
		// keep it.
		named = config.NamedProviderConfig{Type: key}
	}
	named.AuthMethod = method
	providers.Named[key] = named
}

func authLogoutCmd() {
	provider := parseLogoutArgs(os.Args[3:])

	if provider != "" {
		// Resolve aliases to the canonical credential key first: `auth logout
		// --provider claude` must delete the `anthropic` credential, clear
		// Providers.Anthropic, and echo the name actually logged out from.
		provider = canonicalProviderName(provider)

		if err := auth.DeleteCredential(provider); err != nil {
			fmt.Printf("Failed to remove credentials: %v\n", err)
			os.Exit(1)
		}

		appCfg, err := loadConfig()
		if err == nil {
			switch provider {
			case "openai":
				providersForWrite(appCfg).OpenAI.AuthMethod = ""
			case "anthropic":
				providers := providersForWrite(appCfg)
				providers.Anthropic.AuthMethod = ""
				// ProvidersConfig serializes the named copy of the provider
				// whenever it holds data, so blanking the typed field alone is
				// invisible in the file — see setNamedAuthMethod. Without this,
				// `auth logout --provider claude` would leave
				// providers.anthropic.auth_method = "oauth" behind and the next
				// gateway start would still pick the Claude OAuth wire with a
				// deleted credential (pkg/providers/factory.go reads
				// named.AuthMethod for anthropic).
				setNamedAuthMethod(providers, "anthropic", "")
			case "gemini":
				providers := providersForWrite(appCfg)
				providers.Gemini.AuthMethod = ""
				// Same mechanism as above: without this,
				// `auth logout --provider gemini` would leave
				// providers.gemini.auth_method = "oauth" behind and the next
				// gateway start would still pick the OAuth wire contract.
				setNamedAuthMethod(providers, "gemini", "")
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
			providers := providersForWrite(appCfg)
			providers.OpenAI.AuthMethod = ""
			providers.Anthropic.AuthMethod = ""
			providers.Gemini.AuthMethod = ""
			// Every credential was just deleted, so no provider may keep an
			// auth_method that selects an OAuth transport: the typed writes above
			// would not reach the file for an entry that already carries data, so
			// the named copies are synced too (see setNamedAuthMethod). openai
			// keeps its historical typed-field-only clear on purpose — its
			// shadowing is pre-existing (commit 8e54a9db) and out of this task's
			// scope.
			setNamedAuthMethod(providers, "anthropic", "")
			setNamedAuthMethod(providers, "gemini", "")
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
