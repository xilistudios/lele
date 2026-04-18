package main

import (
	"fmt"
	"os"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/i18n"
)

func authCmd() {
	if len(os.Args) < 3 {
		authHelp()
		return
	}

	switch os.Args[2] {
	case "login":
		authLoginCmd()
	case "logout":
		authLogoutCmd()
	case "status":
		authStatusCmd()
	default:
		fmt.Println(i18n.TPrintf("cli.common.unknownSubcommand", "auth", os.Args[2]))
		authHelp()
	}
}

func authHelp() {
	fmt.Println(i18n.T("cli.auth.help.title"))
	fmt.Println(i18n.T("cli.auth.help.login"))
	fmt.Println(i18n.T("cli.auth.help.logout"))
	fmt.Println(i18n.T("cli.auth.help.status"))
	fmt.Println(i18n.T("cli.auth.help.loginOptions"))
	fmt.Println(i18n.T("cli.auth.help.provider"))
	fmt.Println(i18n.T("cli.auth.help.deviceCode"))
	fmt.Println(i18n.T("cli.auth.help.examples"))
	fmt.Println(i18n.T("cli.auth.help.exampleLoginOpenAI"))
	fmt.Println(i18n.T("cli.auth.help.exampleLoginDeviceCode"))
	fmt.Println(i18n.T("cli.auth.help.exampleLoginAnthropic"))
	fmt.Println(i18n.T("cli.auth.help.exampleLogoutOpenAI"))
	fmt.Println(i18n.T("cli.auth.help.exampleStatus"))
}

func authLoginCmd() {
	provider := ""
	useDeviceCode := false

	args := os.Args[3:]
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

	if provider == "" {
		fmt.Println(i18n.T("cli.auth.errorProviderRequired"))
		fmt.Println(i18n.T("cli.auth.supportedProviders"))
		return
	}

	switch provider {
	case "openai":
		authLoginOpenAI(useDeviceCode)
	case "anthropic":
		authLoginPasteToken(provider)
	default:
		fmt.Println(i18n.TPrintf("cli.auth.unsupportedProvider", provider))
		fmt.Println(i18n.T("cli.auth.supportedProviders"))
	}
}

func authLoginOpenAI(useDeviceCode bool) {
	cfg := auth.OpenAIOAuthConfig()

	var cred *auth.AuthCredential
	var err error

	if useDeviceCode {
		cred, err = auth.LoginDeviceCode(cfg)
	} else {
		cred, err = auth.LoginBrowser(cfg)
	}

	if err != nil {
		fmt.Println(i18n.TPrintf("cli.auth.loginFailed", err))
		os.Exit(1)
	}

	if err := auth.SetCredential("openai", cred); err != nil {
		fmt.Println(i18n.TPrintf("cli.auth.failedSaveCredentials", err))
		os.Exit(1)
	}

	appCfg, err := loadConfig()
	if err == nil {
		appCfg.Providers.OpenAI.AuthMethod = "oauth"
		if err := config.SaveConfig(getConfigPath(), appCfg); err != nil {
			fmt.Println(i18n.TPrintf("cli.auth.warningUpdateConfig", err))
		}
	}

	fmt.Println(i18n.T("cli.auth.loginSuccessful"))
	if cred.AccountID != "" {
		fmt.Println(i18n.TPrintf("cli.auth.account", cred.AccountID))
	}
}

func authLoginPasteToken(provider string) {
	cred, err := auth.LoginPasteToken(provider, os.Stdin)
	if err != nil {
		fmt.Println(i18n.TPrintf("cli.auth.loginFailed", err))
		os.Exit(1)
	}

	if err := auth.SetCredential(provider, cred); err != nil {
		fmt.Println(i18n.TPrintf("cli.auth.failedSaveCredentials", err))
		os.Exit(1)
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
			fmt.Println(i18n.TPrintf("cli.auth.warningUpdateConfig", err))
		}
	}

	fmt.Println(i18n.TPrintf("cli.auth.tokenSaved", provider))
}

func authLogoutCmd() {
	provider := ""

	args := os.Args[3:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--provider", "-p":
			if i+1 < len(args) {
				provider = args[i+1]
				i++
			}
		}
	}

	if provider != "" {
		if err := auth.DeleteCredential(provider); err != nil {
			fmt.Println(i18n.TPrintf("cli.auth.failedRemoveCredentials", err))
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

		fmt.Println(i18n.TPrintf("cli.auth.loggedOutFrom", provider))
	} else {
		if err := auth.DeleteAllCredentials(); err != nil {
			fmt.Println(i18n.TPrintf("cli.auth.failedRemoveCredentials", err))
			os.Exit(1)
		}

		appCfg, err := loadConfig()
		if err == nil {
			appCfg.Providers.OpenAI.AuthMethod = ""
			appCfg.Providers.Anthropic.AuthMethod = ""
			config.SaveConfig(getConfigPath(), appCfg)
		}

		fmt.Println(i18n.T("cli.auth.loggedOutFromAll"))
	}
}

func authStatusCmd() {
	store, err := auth.LoadStore()
	if err != nil {
		fmt.Println(i18n.TPrintf("cli.auth.errorLoadingAuthStore", err))
		return
	}

	if len(store.Credentials) == 0 {
		fmt.Println(i18n.T("cli.auth.noAuthenticatedProviders"))
		fmt.Println(i18n.T("cli.auth.runAuthLogin"))
		return
	}

	fmt.Println(i18n.T("cli.auth.authenticatedProviders"))
	fmt.Println(i18n.T("cli.auth.separator"))
	for provider, cred := range store.Credentials {
		status := i18n.T("cli.auth.statusActive")
		if cred.IsExpired() {
			status = i18n.T("cli.auth.statusExpired")
		} else if cred.NeedsRefresh() {
			status = i18n.T("cli.auth.statusNeedsRefresh")
		}

		fmt.Printf("  %s:\n", provider)
		fmt.Println(i18n.TPrintf("cli.auth.method", cred.AuthMethod))
		fmt.Println(i18n.TPrintf("cli.auth.status", status))
		if cred.AccountID != "" {
			fmt.Println(i18n.TPrintf("cli.auth.accountInfo", cred.AccountID))
		}
		if !cred.ExpiresAt.IsZero() {
			fmt.Println(i18n.TPrintf("cli.auth.expires", cred.ExpiresAt.Format("2006-01-02 15:04")))
		}
	}
}
