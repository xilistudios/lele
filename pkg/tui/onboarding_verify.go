package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
)

type obVerifyResultMsg struct {
	success      bool
	providerName string
	err          error
}

var obVerifyHTTPClient *http.Client

func (m *Model) obVerifyKeyCmd() tea.Cmd {
	preferred := m.obProviderName
	if preferred == "" {
		preferred = m.providerSelectedName
	}
	type namedProv struct {
		name string
		p    config.NamedProviderConfig
	}
	var chosen *namedProv
	providers := m.cfg.Providers.ListNamed()
	if preferred != "" {
		if prov, ok := providers[preferred]; ok && (prov.APIKey != "" || prov.APIBase != "") {
			chosen = &namedProv{name: preferred, p: prov}
		}
	}
	if chosen == nil {
		names := make([]string, 0, len(providers))
		for n, prov := range providers {
			if prov.APIKey != "" || prov.APIBase != "" {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		if len(names) > 0 {
			chosen = &namedProv{name: names[0], p: providers[names[0]]}
		}
	}
	if chosen == nil {
		return func() tea.Msg { return obVerifyResultMsg{success: true} }
	}
	name := chosen.name
	p := chosen.p

	return func() tea.Msg {
		if p.Type == "ollama" {
			return obVerifyResultMsg{success: true, providerName: name}
		}
		if strings.Contains(p.APIBase, "localhost") || strings.Contains(p.APIBase, "127.0.0.1") {
			return obVerifyResultMsg{success: true, providerName: name}
		}

		client := obVerifyHTTPClient
		if client == nil {
			client = &http.Client{Timeout: 10 * time.Second}
		}
		req, err := http.NewRequest("GET", p.APIBase+"/models", nil)
		if err != nil {
			return obVerifyResultMsg{success: false, providerName: name, err: err}
		}

		if p.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.APIKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			return obVerifyResultMsg{success: false, providerName: name, err: err}
		}
		defer resp.Body.Close()

		if resp.StatusCode == 403 {
			return obVerifyResultMsg{success: true, providerName: name}
		}
		if resp.StatusCode != 200 {
			return obVerifyResultMsg{success: false, providerName: name,
				err: fmt.Errorf("unexpected status %d", resp.StatusCode)}
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if err != nil {
			return obVerifyResultMsg{success: false, providerName: name,
				err: fmt.Errorf("reading response body: %w", err)}
		}
		if err := checkVerifyBody(body); err != nil {
			return obVerifyResultMsg{success: false, providerName: name, err: err}
		}
		return obVerifyResultMsg{success: true, providerName: name}
	}
}

func checkVerifyBody(body []byte) error {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil
	}
	var payload struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Error) == 0 {
		return nil //nolint:nilerr // not JSON or no error field — tolerant success
	}
	var errStr string
	if json.Unmarshal(payload.Error, &errStr) == nil && errStr != "" {
		return fmt.Errorf("provider reported error: %s", errStr)
	}
	var errObj struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(payload.Error, &errObj) == nil {
		switch {
		case errObj.Message != "":
			return fmt.Errorf("provider reported error: %s", errObj.Message)
		case errObj.Error != "":
			return fmt.Errorf("provider reported error: %s", errObj.Error)
		}
	}
	return nil
}

func (m *Model) obFinalizeSetup() {
	if m.cfg == nil || m.cfg.Providers == nil {
		return
	}

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

	syncProviderToTypedField(m.cfg.Providers, chosenName, chosen)

	pickable := m.cfg.Agents.Defaults.Provider == "" ||
		!providerIsUsable(m.cfg, m.cfg.Agents.Defaults.Provider)
	if pickable {
		m.cfg.Agents.Defaults.Provider = chosenName
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

	m.cfg.TUI.OnboardingCompleted = true

	m.saveConfigToDisk()

	if ws := m.cfg.WorkspacePath(); ws != "" {
		_ = os.MkdirAll(ws, 0o755)
	}
}

func syncProviderToTypedField(p *config.ProvidersConfig, name string, np config.NamedProviderConfig) {
	if slot := typedProviderSlot(p, name); slot != nil {
		*slot = np.ProviderConfig
	}
}

type providerEditChanges struct {
	APIKey      bool
	APIBase     bool
	Proxy       bool
	AuthMethod  bool
	ConnectMode bool
	WebSearch   bool
	Headers     bool
}

func syncEditedProviderCredentials(p *config.ProvidersConfig, name string, np config.NamedProviderConfig, changed providerEditChanges) {
	slot := typedProviderSlot(p, name)
	if slot == nil {
		return
	}
	if changed.APIKey || slot.APIKey == "" {
		slot.APIKey = np.APIKey
	}
	if changed.APIBase || slot.APIBase == "" {
		slot.APIBase = np.APIBase
	}
	if changed.Proxy || slot.Proxy == "" {
		slot.Proxy = np.Proxy
	}
	if changed.AuthMethod || slot.AuthMethod == "" {
		slot.AuthMethod = np.AuthMethod
	}
	if changed.ConnectMode || slot.ConnectMode == "" {
		slot.ConnectMode = np.ConnectMode
	}
	if changed.Headers || len(slot.Headers) == 0 {
		slot.Headers = cloneHeaders(np.Headers)
	}
}

func typedProviderSlot(p *config.ProvidersConfig, name string) *config.ProviderConfig {
	if p == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic":
		return &p.Anthropic
	case "openai":
		return &p.OpenAI.ProviderConfig
	case "openrouter":
		return &p.OpenRouter
	case "groq":
		return &p.Groq
	case "zhipu":
		return &p.Zhipu
	case "vllm":
		return &p.VLLM
	case "gemini":
		return &p.Gemini
	case "nvidia":
		return &p.Nvidia
	case "ollama":
		return &p.Ollama
	case "moonshot":
		return &p.Moonshot
	case "shengsuanyun":
		return &p.ShengSuanYun
	case "deepseek":
		return &p.DeepSeek
	case "github_copilot":
		return &p.GitHubCopilot
	case "nanogpt":
		return &p.NanogPT
	case "alibaba_coding_plan":
		return &p.AlibabaCodingPlan
	case "zai_coding_plan":
		return &p.ZAICodingPlan
	case "modelark_coding_plan":
		return &p.ModelArkCodingPlan
	}
	return nil
}

func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

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
