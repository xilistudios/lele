package main

import (
	"fmt"
	"os"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/config"
)

func statusCmd() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		return
	}

	configPath := getConfigPath()

	fmt.Printf("%s lele Status\n", logo)
	fmt.Printf("Version: %s\n", formatVersion())
	build, _ := formatBuildInfo()
	if build != "" {
		fmt.Printf("Build: %s\n", build)
	}
	fmt.Println()

	if _, err := os.Stat(configPath); err == nil {
		fmt.Println("Config:", configPath, "✓")
	} else {
		fmt.Println("Config:", configPath, "✗")
	}

	workspace := cfg.WorkspacePath()
	if _, err := os.Stat(workspace); err == nil {
		fmt.Println("Workspace:", workspace, "✓")
	} else {
		fmt.Println("Workspace:", workspace, "✗")
	}

	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("Model: %s\n", cfg.Agents.Defaults.Model)

		// Every provider read below must go through this one non-nil value:
		// the section is absent (nil) on a fresh machine, see
		// providersForDisplay.
		providers := providersForDisplay(cfg)

		hasOpenRouter := providers.OpenRouter.APIKey != ""
		hasAnthropic := providers.Anthropic.APIKey != ""
		hasOpenAI := providers.OpenAI.APIKey != ""
		hasGemini := providers.Gemini.APIKey != ""
		hasZhipu := providers.Zhipu.APIKey != ""
		hasGroq := providers.Groq.APIKey != ""
		hasVLLM := providers.VLLM.APIBase != ""

		status := func(enabled bool) string {
			if enabled {
				return "✓"
			}
			return "not set"
		}
		fmt.Println("OpenRouter API:", status(hasOpenRouter))
		fmt.Println("Anthropic API:", status(hasAnthropic))
		fmt.Println("OpenAI API:", status(hasOpenAI))
		fmt.Println("Gemini API:", status(hasGemini))
		fmt.Println("Zhipu API:", status(hasZhipu))
		fmt.Println("Groq API:", status(hasGroq))
		if hasVLLM {
			fmt.Printf("vLLM/Local: ✓ %s\n", providers.VLLM.APIBase)
		} else {
			fmt.Println("vLLM/Local: not set")
		}

		store, _ := auth.LoadStore()
		if store != nil && len(store.Credentials) > 0 {
			fmt.Println("\nOAuth/Token Auth:")
			for provider, cred := range store.Credentials {
				status := "authenticated"
				if cred.IsExpired() {
					status = "expired"
				} else if cred.NeedsRefresh() {
					status = "needs refresh"
				}
				fmt.Printf("  %s (%s): %s\n", provider, cred.AuthMethod, status)
			}
		}
	}
}

// providersForDisplay returns the providers section to read for the status
// report, substituting an empty section when it is nil.
//
// A nil section is a normal CLI-produced state, not corruption:
// config.LoadConfig sets cfg.Providers = nil when config.json carries no usable
// "providers" section, and ProvidersConfig.MarshalJSON writes a data-less
// section as "null" — which loads back as nil. `lele auth logout` on a fresh
// machine therefore writes "providers": null, and the `lele status` run right
// after it used to SIGSEGV on the first provider dereference.
//
// Unlike providersForWrite in auth.go, this is deliberately read-only: it never
// allocates into cfg.Providers and never mutates the config, because `status`
// only prints and a config it holds is not a config it is about to save. The
// fallback value is a fresh local, never a shared package-level pointer.
func providersForDisplay(cfg *config.Config) *config.ProvidersConfig {
	if cfg.Providers == nil {
		return &config.ProvidersConfig{}
	}
	return cfg.Providers
}
