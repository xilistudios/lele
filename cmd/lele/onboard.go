package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
)

type providerInfo struct {
	name        string
	displayName string
	typeKey     string
	apiBase     string
	authHeader  string
	local       bool
}

func providerRegistry() []providerInfo {
	return []providerInfo{
		{name: "anthropic", displayName: "Anthropic (Claude)", typeKey: "anthropic", apiBase: "https://api.anthropic.com/v1", authHeader: "x-api-key"},
		{name: "openai", displayName: "OpenAI (GPT)", typeKey: "openai", apiBase: "https://api.openai.com/v1", authHeader: "Bearer"},
		{name: "openrouter", displayName: "OpenRouter", typeKey: "openrouter", apiBase: "https://openrouter.ai/api/v1", authHeader: "Bearer"},
		{name: "groq", displayName: "Groq", typeKey: "groq", apiBase: "https://api.groq.com/openai/v1", authHeader: "Bearer"},
		{name: "deepseek", displayName: "DeepSeek", typeKey: "deepseek", apiBase: "https://api.deepseek.com/v1", authHeader: "Bearer"},
		{name: "gemini", displayName: "Gemini (Google)", typeKey: "gemini", apiBase: "https://generativelanguage.googleapis.com/v1beta", authHeader: "Bearer"},
		{name: "zhipu", displayName: "Zhipu (GLM)", typeKey: "zhipu", apiBase: "https://open.bigmodel.cn/api/paas/v4", authHeader: "Bearer"},
		{name: "ollama", displayName: "Ollama (local)", typeKey: "ollama", apiBase: "http://localhost:11434/v1", authHeader: "Bearer", local: true},
		{name: "nvidia", displayName: "NVIDIA", typeKey: "nvidia", apiBase: "https://integrate.api.nvidia.com/v1", authHeader: "Bearer"},
		{name: "moonshot", displayName: "Moonshot (Kimi)", typeKey: "moonshot", apiBase: "https://api.moonshot.cn/v1", authHeader: "Bearer"},
		{name: "vllm", displayName: "VLLM", typeKey: "vllm", apiBase: "", authHeader: "Bearer"},
		{name: "shengsuanyun", displayName: "ShengSuanYun", typeKey: "shengsuanyun", apiBase: "https://router.shengsuanyun.com/api/v1", authHeader: "Bearer"},
		{name: "github_copilot", displayName: "GitHub Copilot", typeKey: "github_copilot", apiBase: "localhost:4321", authHeader: "Bearer"},
		{name: "custom", displayName: "Custom (OpenAI-compatible)", typeKey: "", apiBase: "", authHeader: "Bearer"},
	}
}

func maskAPIKey(key string) string {
	if len(key) < 8 {
		return "***"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func printHelp() {
	fmt.Printf("%s lele - Personal AI Assistant v%s\n\n", logo, version)
	fmt.Println("Usage: lele <command>")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  onboard     Initialize lele configuration and workspace")
	fmt.Println("  agent       Interact with the agent directly")
	fmt.Println("  auth        Manage authentication (login, logout, status)")
	fmt.Println("  gateway     Start lele gateway")
	fmt.Println("  web         Start or stop the web app server")
	fmt.Println("  status      Show lele status")
	fmt.Println("  cron        Manage scheduled tasks")
	fmt.Println("  migrate     Migrate from OpenClaw to Lele")
	fmt.Println("  skills      Manage skills (install, list, remove)")
	fmt.Println("  client      Manage native channel clients (pair, list, remove)")
	fmt.Println("  version     Show version information")
}

func askYesNo(prompt string, defaultYes bool) bool {
	defaultHint := "y/N"
	if defaultYes {
		defaultHint = "Y/n"
	}
	fmt.Printf("%s (%s): ", prompt, defaultHint)

	var response string
	fmt.Scanln(&response)
	response = strings.ToLower(strings.TrimSpace(response))

	if response == "" {
		return defaultYes
	}
	return response == "y" || response == "yes"
}

func askString(prompt string, defaultVal string) string {
	fmt.Printf("%s [%s]: ", prompt, defaultVal)

	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(response)

	if response == "" {
		return defaultVal
	}
	return response
}

func askSelect(prompt string, options []string, defaultIdx int) int {
	fmt.Println(prompt)
	for i, opt := range options {
		fmt.Printf("  %d. %s\n", i+1, opt)
	}

	defaultChoice := defaultIdx + 1
	fmt.Printf("Choice [%d]: ", defaultChoice)

	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(response)

	if response == "" {
		return defaultIdx
	}

	var choice int
	fmt.Sscanf(response, "%d", &choice)
	if choice < 1 || choice > len(options) {
		return defaultIdx
	}
	return choice - 1
}

func askInt(prompt string, defaultVal int) int {
	fmt.Printf("%s [%d]: ", prompt, defaultVal)

	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(response)

	if response == "" {
		return defaultVal
	}

	var result int
	fmt.Sscanf(response, "%d", &result)
	if result == 0 {
		return defaultVal
	}
	return result
}

func askFloat(prompt string, defaultVal float64) float64 {
	fmt.Printf("%s [%g]: ", prompt, defaultVal)

	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(response)

	if response == "" {
		return defaultVal
	}

	var result float64
	fmt.Sscanf(response, "%f", &result)
	if result == 0 {
		return defaultVal
	}
	return result
}

func askSecret(prompt string) string {
	fmt.Printf("%s: ", prompt)

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(input)
}

func validateProvider(providerType, apiKey, apiBase, authHeader string) bool {
	if apiKey == "" || apiBase == "" {
		return false
	}

	if strings.HasPrefix(apiBase, "localhost") || strings.HasPrefix(apiBase, "http://localhost") {
		return true
	}

	client := &http.Client{Timeout: 10 * time.Second}
	url := apiBase + "/models"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false
	}

	if authHeader == "x-api-key" {
		req.Header.Set("x-api-key", apiKey)
	} else {
		req.Header.Set("Authorization", authHeader+" "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	return resp.StatusCode == 200 || resp.StatusCode == 403
}

func configureModels(providerName string) map[string]config.ProviderModelConfig {
	models := make(map[string]config.ProviderModelConfig)

	for {
		alias := askString("  Alias name", "")
		if alias == "" {
			break
		}

		modelReal := askString("  Model", "")
		if modelReal == "" {
			break
		}

		vision := askYesNo("  Vision support?", true)

		modelCfg := config.ProviderModelConfig{
			Model:  modelReal,
			Vision: vision,
		}

		if askYesNo("  Configure context window?", false) {
			modelCfg.ContextWindow = askInt("  Context window", 8192)
		}

		models[alias] = modelCfg

		if !askYesNo("  Add another model?", false) {
			break
		}
	}

	return models
}

func configureProvider(cfg *config.Config, info providerInfo) {
	fmt.Printf("\n--- Configuring %s ---\n", info.displayName)

	apiKey := ""
	if !info.local {
		apiKey = askSecret("API Key")
	}

	apiBase := askString("API Base", info.apiBase)

	proxy := ""
	if askYesNo("[Advanced] Configure proxy?", false) {
		proxy = askString("Proxy URL", "")
	}

	if apiKey != "" && apiBase != "" && !info.local {
		fmt.Print("Validating API key... ")
		if validateProvider(info.typeKey, apiKey, apiBase, info.authHeader) {
			fmt.Println("\u2713 Valid")
		} else {
			fmt.Println("\u2717 Could not validate (warning)")
		}
	}

	models := make(map[string]config.ProviderModelConfig)
	if askYesNo("Configure model aliases?", false) {
		models = configureModels(info.name)
	}

	named := config.NamedProviderConfig{
		Type: info.typeKey,
		ProviderConfig: config.ProviderConfig{
			APIKey:  apiKey,
			APIBase: apiBase,
			Proxy:   proxy,
		},
		Models: models,
	}

	if cfg.Providers.Named == nil {
		cfg.Providers.Named = make(map[string]config.NamedProviderConfig)
	}
	cfg.Providers.Named[info.name] = named

	switch info.typeKey {
	case "anthropic":
		cfg.Providers.Anthropic = named.ProviderConfig
	case "openai":
		cfg.Providers.OpenAI.ProviderConfig = named.ProviderConfig
	case "openrouter":
		cfg.Providers.OpenRouter = named.ProviderConfig
	case "groq":
		cfg.Providers.Groq = named.ProviderConfig
	case "deepseek":
		cfg.Providers.DeepSeek = named.ProviderConfig
	case "gemini":
		cfg.Providers.Gemini = named.ProviderConfig
	case "zhipu":
		cfg.Providers.Zhipu = named.ProviderConfig
	case "ollama":
		cfg.Providers.Ollama = named.ProviderConfig
	case "nvidia":
		cfg.Providers.Nvidia = named.ProviderConfig
	case "moonshot":
		cfg.Providers.Moonshot = named.ProviderConfig
	case "vllm":
		cfg.Providers.VLLM = named.ProviderConfig
	case "shengsuanyun":
		cfg.Providers.ShengSuanYun = named.ProviderConfig
	case "github_copilot":
		cfg.Providers.GitHubCopilot = named.ProviderConfig
	}

	fmt.Printf("\u2713 Provider %s configured\n", info.displayName)
}

func configureProviders(cfg *config.Config) {
	fmt.Println("\n=== Provider Configuration ===")

	registry := providerRegistry()
	commonCount := 8
	allShown := false

	for {
		options := make([]string, 0, len(registry))
		for i := 0; i < commonCount; i++ {
			options = append(options, registry[i].displayName)
		}
		if !allShown {
			options = append(options, "[Show all providers]")
		} else {
			for i := commonCount; i < len(registry); i++ {
				options = append(options, registry[i].displayName)
			}
		}

		choice := askSelect("Select a provider to configure:", options, 1)

		if !allShown && choice == len(options)-1 {
			allShown = true
			continue
		}

		idx := choice
		if allShown && choice >= commonCount {
			idx = choice
		} else if choice >= commonCount {
			idx = commonCount + choice - len(options) + 1
		}

		if idx >= len(registry) {
			idx = len(registry) - 1
		}

		info := registry[idx]

		if info.name == "custom" {
			customName := askString("Provider name", "")
			if customName == "" {
				continue
			}
			info.name = strings.ToLower(customName)
			info.typeKey = info.name
			info.displayName = customName
		}

		configureProvider(cfg, info)

		if !askYesNo("\nConfigure another provider?", false) {
			break
		}
	}
}

func getConfiguredModels(cfg *config.Config) []string {
	models := []string{}
	for provName, named := range cfg.Providers.Named {
		if named.APIKey == "" && named.APIBase == "" {
			continue
		}
		for alias := range named.Models {
			models = append(models, provName+"/"+alias)
		}
		if len(named.Models) == 0 && named.APIKey != "" {
			models = append(models, provName+"/default")
		}
	}
	return models
}

func selectModel(cfg *config.Config, prompt string, defaultVal string) string {
	models := getConfiguredModels(cfg)
	if len(models) == 0 {
		return askString(prompt, defaultVal)
	}

	options := models
	options = append(options, "[Enter manually]")

	choice := askSelect(prompt, options, 0)

	if choice == len(options)-1 {
		return askString("Model", defaultVal)
	}

	return models[choice]
}

func configureAgentDefaults(cfg *config.Config) {
	fmt.Println("\n=== Agent Configuration ===")

	defaultModel := "nanogpt/qwen3-5-397b-thinking"
	configuredModels := getConfiguredModels(cfg)
	if len(configuredModels) > 0 {
		defaultModel = configuredModels[0]
	}

	model := selectModel(cfg, "Default model", defaultModel)
	cfg.Agents.Defaults.Model = model

	if idx := strings.Index(model, "/"); idx > 0 {
		cfg.Agents.Defaults.Provider = model[:idx]
	}

	cfg.Agents.Defaults.MaxTokens = askInt("Max tokens", 8192)

	temp := askFloat("Temperature", 0.7)
	cfg.Agents.Defaults.Temperature = &temp

	cfg.Agents.Defaults.MaxToolIterations = askInt("Max tool iterations", 20)

	fmt.Println("\u2713 Agent defaults configured")
}

func configureAdditionalAgents(cfg *config.Config) {
	if !askYesNo("\nAdd additional agents?", false) {
		return
	}

	agentNum := 1
	for {
		fmt.Printf("\n--- Agent %d ---\n", agentNum)

		name := askString("Name", "")
		if name == "" {
			name = fmt.Sprintf("Agent %d", agentNum)
		}

		id := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
		id = askString("ID", id)

		model := selectModel(cfg, "Model", cfg.Agents.Defaults.Model)

		temp := askFloat("Temperature", *cfg.Agents.Defaults.Temperature)

		skills := []string{}
		if askYesNo("Add skills?", false) {
			for {
				skill := askString("Skill", "")
				if skill == "" {
					break
				}
				skills = append(skills, skill)
				if !askYesNo("Add another skill?", false) {
					break
				}
			}
		}

		agentCfg := config.AgentConfig{
			ID:          id,
			Name:        name,
			Model:       &config.AgentModelConfig{Primary: model},
			Temperature: &temp,
			Skills:      skills,
		}

		cfg.Agents.List = append(cfg.Agents.List, agentCfg)
		fmt.Printf("\u2713 Agent %s configured\n", name)

		if !askYesNo("\nAdd another agent?", false) {
			break
		}
		agentNum++
	}
}

func configureWebUI(cfg *config.Config, leleDir string) {
	fmt.Println("\n=== Web UI Configuration ===")

	cfg.Channels.Web.Enabled = true
	cfg.Channels.Web.Port = askInt("Web port", 3005)
	cfg.Channels.Web.Host = "0.0.0.0"

	fmt.Println("\n\u2713 Web UI enabled on port", cfg.Channels.Web.Port)

	fmt.Println("\u2713 Native channel auto-enabled (required for Web UI)")
	cfg.Channels.Native.Enabled = true

	if askYesNo("[Advanced] Configure native channel?", false) {
		configureNativeAdvanced(cfg)
	}
}

func configureNativeAdvanced(cfg *config.Config) {
	fmt.Println("\n=== Native Channel Configuration (Advanced) ===")

	cfg.Channels.Native.Host = askString("API host", "127.0.0.1")
	cfg.Channels.Native.Port = askInt("API port", 18793)
	cfg.Channels.Native.MaxClients = askInt("Max paired clients", 5)
	cfg.Channels.Native.TokenExpiryDays = askInt("Token expiry days", 30)

	fmt.Println("\n\u2713 Native channel configured")
}

func maybeGeneratePIN(cfg *config.Config, leleDir string) {
	if !cfg.Channels.Web.Enabled {
		return
	}

	if !askYesNo("Generate pairing PIN?", true) {
		return
	}

	deviceName := askString("Device name", "Desktop")

	authMgr, err := channels.NewAuthManager(&cfg.Channels.Native, leleDir)
	if err != nil {
		fmt.Printf("Error creating auth manager: %v\n", err)
		return
	}

	pending, err := authMgr.GeneratePIN(deviceName)
	if err != nil {
		fmt.Printf("Error generating PIN: %v\n", err)
		return
	}

	fmt.Println("\n\u2713 Pairing PIN generated")
	fmt.Printf("  PIN:     %s\n", pending.PIN)
	fmt.Printf("  Expires: %s (%d minutes)\n",
		pending.Expires.Format("15:04:05"),
		cfg.Channels.Native.PinExpiryMinutes)
}

func maybeStartServices(cfg *config.Config) {
	if !cfg.Channels.Web.Enabled {
		return
	}

	if !askYesNo("Start services now?", true) {
		fmt.Println("\nTo start services manually:")
		fmt.Println("  lele gateway")
		fmt.Println("  lele web start")
		return
	}

	fmt.Println("\n[+] Starting gateway...")
	gatewayCmd := exec.Command("lele", "gateway")
	gatewayCmd.Start()

	time.Sleep(1 * time.Second)

	fmt.Println("[+] Starting web server...")
	webCmd := exec.Command("lele", "web", "start",
		"--port", fmt.Sprintf("%d", cfg.Channels.Web.Port))
	webCmd.Start()

	time.Sleep(500 * time.Millisecond)

	fmt.Println("\n\u2713 Services started")
	fmt.Printf("\nOpen http://127.0.0.1:%d in your browser\n", cfg.Channels.Web.Port)
	fmt.Println("Enter the PIN to connect your device.")
}

func printSummary(cfg *config.Config) {
	fmt.Println("\n=== Configuration Summary ===")

	fmt.Println("\nProviders:")
	for provName, named := range cfg.Providers.Named {
		if named.APIKey == "" && named.APIBase == "" {
			continue
		}
		keyDisplay := maskAPIKey(named.APIKey)
		if named.APIKey == "" {
			keyDisplay = "(no key)"
		}
		modelInfo := ""
		for alias, mc := range named.Models {
			modelInfo += fmt.Sprintf("%s -> %s", alias, mc.Model)
			if mc.Vision {
				modelInfo += " [vision]"
			}
			modelInfo += ", "
		}
		modelInfo = strings.TrimSuffix(modelInfo, ", ")
		if modelInfo == "" {
			modelInfo = "default"
		}
		fmt.Printf("  %s: %s (%s)\n", provName, keyDisplay, modelInfo)
	}

	fmt.Println("\nAgents:")
	fmt.Printf("  default: %s, %d tokens, temp %g\n",
		cfg.Agents.Defaults.Model,
		cfg.Agents.Defaults.MaxTokens,
		*cfg.Agents.Defaults.Temperature)
	for _, agent := range cfg.Agents.List {
		fmt.Printf("  %s: %s, temp %g\n", agent.Name, agent.Model.Primary, *agent.Temperature)
	}

	fmt.Println("\nWeb UI:")
	if cfg.Channels.Web.Enabled {
		fmt.Printf("  enabled on port %d\n", cfg.Channels.Web.Port)
	} else {
		fmt.Println("  disabled")
	}
}

func onboard() {
	configPath := getConfigPath()

	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("Config already exists at %s\n", configPath)
		fmt.Print("Overwrite? (y/n): ")
		var response string
		fmt.Scanln(&response)
		if response != "y" {
			fmt.Println("Aborted.")
			return
		}
	}

	fmt.Println("\n=== Lele Onboarding ===")

	cfg := config.DefaultConfig()

	home, _ := os.UserHomeDir()
	leleDir := filepath.Join(home, ".lele")

	configureProviders(cfg)

	configureAgentDefaults(cfg)
	configureAdditionalAgents(cfg)

	if askYesNo("\nEnable Web UI?", true) {
		configureWebUI(cfg, leleDir)
		maybeGeneratePIN(cfg, leleDir)
	}

	printSummary(cfg)

	if !askYesNo("\nSave configuration?", true) {
		fmt.Println("Aborted.")
		return
	}

	if err := config.SaveConfig(configPath, cfg); err != nil {
		fmt.Printf("Error saving config: %v\n", err)
		os.Exit(1)
	}

	workspace := cfg.WorkspacePath()
	createWorkspaceTemplates(workspace)

	logsPath := cfg.LogsPath()
	if err := os.MkdirAll(logsPath, 0755); err != nil {
		fmt.Printf("Warning: could not create logs directory: %v\n", err)
	} else {
		currentDate := time.Now().Format("2006-01-02")
		infoLog := filepath.Join(logsPath, fmt.Sprintf("info-%s.log", currentDate))
		errorsLog := filepath.Join(logsPath, fmt.Sprintf("errors-%s.log", currentDate))

		if _, err := os.Create(infoLog); err != nil {
			fmt.Printf("Warning: could not create info log file: %v\n", err)
		}
		if _, err := os.Create(errorsLog); err != nil {
			fmt.Printf("Warning: could not create errors log file: %v\n", err)
		}
	}

	fmt.Printf("\n%s lele is ready!\n", logo)

	maybeStartServices(cfg)
}