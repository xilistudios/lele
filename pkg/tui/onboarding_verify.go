package tui

import (
	"net/http"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
)

// obVerifyResultMsg is returned by obVerifyKeyCmd once the async API key
// validation completes. success=false is a WARNING, not a blocker — the user
// can still proceed to the done screen.
type obVerifyResultMsg struct {
	success      bool
	providerName string
	err          error
}

// obVerifyKeyCmd runs async API key validation against the provider that was
// just configured during onboarding. It runs on a goroutine (via tea.Cmd) so
// the TUI never blocks. Local providers (ollama / localhost) are skipped and
// considered valid.
func (m *Model) obVerifyKeyCmd() tea.Cmd {
	return func() tea.Msg {
		providers := m.cfg.Providers.ListNamed()
		var p config.NamedProviderConfig
		var name string
		for n, prov := range providers {
			p = prov
			name = n
			break // get the first/only one
		}

		// Skip validation for local providers.
		if p.Type == "ollama" {
			return obVerifyResultMsg{success: true, providerName: name}
		}
		if strings.Contains(p.APIBase, "localhost") || strings.Contains(p.APIBase, "127.0.0.1") {
			return obVerifyResultMsg{success: true, providerName: name}
		}

		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequest("GET", p.APIBase+"/models", nil)
		if err != nil {
			return obVerifyResultMsg{success: false, providerName: name, err: err}
		}

		// Set auth header based on provider type.
		if p.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.APIKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			return obVerifyResultMsg{success: false, providerName: name, err: err}
		}
		defer resp.Body.Close()

		// 200 or 403 means the key is likely valid (some providers return 403
		// on /models).
		success := resp.StatusCode == 200 || resp.StatusCode == 403
		return obVerifyResultMsg{success: success, providerName: name}
	}
}

// obFinalizeSetup sets the agent defaults (provider + model) from the provider
// that was just configured during onboarding, persists the config to disk, and
// ensures the workspace directory exists. It must be called BEFORE the verify
// cmd so the config is persisted even if validation fails.
func (m *Model) obFinalizeSetup() {
	if m.cfg == nil || m.cfg.Providers == nil {
		return
	}

	// Find the provider that was just configured — the one with a model
	// configured. ListNamed() calls ensureNamedDefaults(), which populates
	// empty placeholders for every known provider name, so we must select the
	// provider that actually has a model (the connect flow always saves one).
	// Prefer the provider the user just connected (providerSelectedName) so the
	// result is deterministic even if a usable default already exists (e.g.
	// ollama in tests); fall back to scanning for any provider with a model.
	providers := m.cfg.Providers.ListNamed()
	var chosenName string
	var chosen config.NamedProviderConfig
	if sel := strings.ToLower(strings.TrimSpace(m.providerSelectedName)); sel != "" {
		if p, ok := providers[sel]; ok && len(p.Models) > 0 {
			chosenName = sel
			chosen = p
		}
	}
	if chosenName == "" {
		for name, p := range providers {
			if len(p.Models) > 0 {
				chosenName = name
				chosen = p
				break
			}
		}
	}
	if chosenName == "" {
		for name, p := range providers {
			chosenName = name
			chosen = p
			break
		}
	}
	if chosenName == "" {
		return
	}

	// Sync the chosen provider back to the corresponding typed field so that
	// ensureNamedDefaults() (which runs on every config load) does not
	// overwrite the user's just-saved provider with an empty placeholder.
	syncProviderToTypedField(m.cfg.Providers, chosenName, chosen)

	// Set agent defaults to the freshly-configured provider. A true first-run
	// config (DefaultConfig) seeds placeholder defaults (openrouter /
	// deepseek-v4-pro) that are NOT usable here — no API key was ever set for
	// them — so they must be overridden with the chosen provider unless the
	// user already selected a usable default during setup.
	pickable := m.cfg.Agents.Defaults.Provider == "" ||
		!providerIsUsable(m.cfg, m.cfg.Agents.Defaults.Provider)
	if pickable {
		m.cfg.Agents.Defaults.Provider = chosenName
		// Find first model alias.
		for alias := range chosen.Models {
			m.cfg.Agents.Defaults.Model = alias
			m.obModelName = alias
			break
		}
	}
	m.obProviderName = chosenName
	if chosen.APIKey != "" {
		m.obMaskedKey = maskAPIKey(chosen.APIKey)
	} else {
		m.obMaskedKey = "(none)"
	}

	// Mark onboarding as completed so it doesn't reappear on next launch.
	m.cfg.TUI.OnboardingCompleted = true

	// Persist config.
	m.saveConfigToDisk()

	// Ensure the workspace directory exists (full workspace template creation
	// is a follow-up; the important thing is the config is persisted).
	if ws := m.cfg.WorkspacePath(); ws != "" {
		_ = os.MkdirAll(ws, 0o755)
	}
}

// syncProviderToTypedField copies a named provider's ProviderConfig (API key,
// base URL, etc.) into the corresponding typed field on ProvidersConfig. This
// is necessary because ensureNamedDefaults() recreates named entries from typed
// fields on every config load — if the typed field is empty, the user's saved
// provider gets silently wiped.
func syncProviderToTypedField(p *config.ProvidersConfig, name string, np config.NamedProviderConfig) {
	pc := np.ProviderConfig
	switch strings.ToLower(name) {
	case "anthropic":
		p.Anthropic = pc
	case "openai":
		p.OpenAI.ProviderConfig = pc
	case "openrouter":
		p.OpenRouter = pc
	case "groq":
		p.Groq = pc
	case "zhipu":
		p.Zhipu = pc
	case "vllm":
		p.VLLM = pc
	case "gemini":
		p.Gemini = pc
	case "nvidia":
		p.Nvidia = pc
	case "ollama":
		p.Ollama = pc
	case "moonshot":
		p.Moonshot = pc
	case "shengsuanyun":
		p.ShengSuanYun = pc
	case "deepseek":
		p.DeepSeek = pc
	case "github_copilot":
		p.GitHubCopilot = pc
	case "nanogpt":
		p.NanogPT = pc
	case "alibaba_coding_plan":
		p.AlibabaCodingPlan = pc
	case "zai_coding_plan":
		p.ZAICodingPlan = pc
	case "modelark_coding_plan":
		p.ModelArkCodingPlan = pc
	}
}

// maskAPIKey masks an API key for display, keeping the first and last 4
// characters. Short keys are fully masked.
func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// providerIsUsable reports whether the named provider already has a key (or is
// a local type) AND at least one model — i.e. it is genuinely ready to chat.
func providerIsUsable(cfg *config.Config, name string) bool {
	if cfg == nil || cfg.Providers == nil || name == "" {
		return false
	}
	p, ok := cfg.Providers.Named[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		p, ok = cfg.Providers.Named[name]
		if !ok {
			return false
		}
	}
	hasKey := p.APIKey != ""
	isLocal := p.Type == "ollama"
	return (hasKey || isLocal) && len(p.Models) > 0
}
