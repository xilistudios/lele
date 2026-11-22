package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/config"
)

type loginKind int

const (
	loginKindUnsupported loginKind = iota
	loginKindOpenAI
	loginKindPasteToken
	loginKindBrowserPreset
	loginKindClaudeOAuth
	loginKindGeminiOAuth
)

var (
	loginExit            = os.Exit
	loginBrowser         = auth.LoginBrowser
	pasteToken           = auth.LoginPasteToken
	loginClaudePasteCode = auth.LoginPasteCode
	loginClaudeBrowser   = auth.LoginBrowserWithPasteFallback
	loginGeminiPasteCode = auth.LoginPasteCode
	loginGeminiBrowser   = auth.LoginBrowserWithPasteFallback
	setCredential        = auth.SetCredential
)

func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func parseAuthSubcommand(args []string) string {
	if len(args) < 1 {
		return ""
	}
	return args[0]
}

type loginArgs struct {
	Provider   string
	DeviceCode bool
	PasteCode  bool
}

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

	s, cleanup, _ := openSharedStore(defaultDBPath(), "auth-cli")
	defer cleanup()
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

func supportedProviders() string {
	return strings.Join(auth.OAuthProviderNames(), ", ")
}

func unsupportedProviderError(provider string) error {
	return fmt.Errorf("Unsupported provider: %s\nSupported providers: %s", provider, supportedProviders())
}

func deviceCodeUnavailableError(provider string) error {
	return fmt.Errorf("device code flow is not available for %s", provider)
}

func resolveLoginProvider(name string, useDeviceCode bool) (loginKind, auth.OAuthProviderConfig, error) {
	if normalizeProviderName(name) == "anthropic" {
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
		return loginKindOpenAI, cfg, nil
	}

	if useDeviceCode {
		return loginKindUnsupported, auth.OAuthProviderConfig{}, deviceCodeUnavailableError(cfg.Name)
	}

	if cfg.Name == "anthropic" {
		return loginKindClaudeOAuth, cfg, nil
	}

	if cfg.Name == "gemini" {
		return loginKindGeminiOAuth, cfg, nil
	}

	return loginKindBrowserPreset, cfg, nil
}

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
		fmt.Println("--paste-code is only supported for the Claude and Gemini OAuth flows; use: lele auth login --provider claude --paste-code")
		loginExit(1)
		return
	}

	switch kind {
	case loginKindOpenAI:
		authLoginOpenAI(args.DeviceCode)
	case loginKindPasteToken:
		authLoginPasteToken(normalizeProviderName(provider))
	case loginKindClaudeOAuth:
		authLoginClaudeOAuth(cfg, args.PasteCode)
	case loginKindGeminiOAuth:
		authLoginGeminiOAuth(cfg, args.PasteCode)
	case loginKindBrowserPreset:
		authLoginBrowserPreset(cfg)
	default:
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

	if appCfg, err := loadConfig(); err == nil {
		providers := providersForWrite(appCfg)
		providers.Anthropic.AuthMethod = "oauth"
		setNamedAuthMethod(providers, "anthropic", "oauth")
		if err := config.SaveConfig(getConfigPath(), appCfg); err != nil {
			fmt.Printf("Warning: could not update config: %v\n", err)
			fmt.Printf("Set providers.anthropic.auth_method = \"oauth\" in %s for the Claude OAuth login to be used.\n", getConfigPath())
		}
	}

	fmt.Println("Login successful!")
	if cred.AccountID != "" {
		fmt.Printf("Account: %s\n", cred.AccountID)
	}
}

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

	if appCfg, err := loadConfig(); err == nil {
		providers := providersForWrite(appCfg)
		providers.Gemini.AuthMethod = "oauth"
		setNamedAuthMethod(providers, "gemini", "oauth")
		if err := config.SaveConfig(getConfigPath(), appCfg); err != nil {
			fmt.Printf("Warning: could not update config: %v\n", err)
			fmt.Printf("Set providers.gemini.auth_method = \"oauth\" in %s for the Gemini OAuth login to be used.\n", getConfigPath())
		}
	}

	fmt.Println("Login successful!")
	if cred.AccountID != "" {
		fmt.Printf("Account: %s\n", cred.AccountID)
	}
}

func authLoginBrowserPreset(cfg auth.OAuthProviderConfig) {
	cred, err := loginBrowser(cfg)
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

	fmt.Println("Login successful!")
	if cred.AccountID != "" {
		fmt.Printf("Account: %s\n", cred.AccountID)
	}
}

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

func providersForWrite(cfg *config.Config) *config.ProvidersConfig {
	if cfg.Providers == nil {
		cfg.Providers = &config.ProvidersConfig{}
	}
	return cfg.Providers
}

func setNamedAuthMethod(providers *config.ProvidersConfig, name, method string) {
	if providers == nil {
		return
	}
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return
	}
	named, ok := providers.GetNamed(key)
	if !ok {
		named = config.NamedProviderConfig{Type: key}
	}
	named.AuthMethod = method
	providers.Named[key] = named
}

func authLogoutCmd() {
	provider := parseLogoutArgs(os.Args[3:])

	if provider != "" {
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
				setNamedAuthMethod(providers, "anthropic", "")
			case "gemini":
				providers := providersForWrite(appCfg)
				providers.Gemini.AuthMethod = ""
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
