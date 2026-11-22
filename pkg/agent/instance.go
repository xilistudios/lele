package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
	"github.com/xilistudios/lele/pkg/voice"
)

func extractProviderFromModel(model, defaultProvider string) string {
	ref := providers.ParseModelRef(model, defaultProvider)
	if ref == nil {
		return strings.ToLower(strings.TrimSpace(defaultProvider))
	}
	return ref.Provider
}

type AgentInstance struct {
	ID                    string
	Name                  string
	Description           string
	Model                 string
	Fallbacks             []string
	Workspace             string
	MaxIterations         int
	MaxTokens             int
	Temperature           float64
	ContextWindow         int
	SupportsImages        bool
	SupportsVideo         bool
	Reasoning             *config.ReasoningConfig
	ThinkingLevel         string
	PromptCache           config.PromptCacheConfig
	Provider              providers.LLMProvider
	Sessions              *session.SessionManager
	ContextBuilder        *ContextBuilder
	Tools                 *tools.ToolRegistry
	Subagents             *config.SubagentsConfig
	SkillsFilter          []string
	Candidates            []providers.FallbackCandidate
	IsDefault             bool
	LLMLoopTimeoutMinutes int
}

func getProviderModelConfig(cfg *config.Config, model string, defaultProvider string) (config.ProviderModelConfig, bool) {
	model = strings.TrimSpace(model)

	ref := providers.ParseModelRef(model, defaultProvider)
	if ref == nil {
		return config.ProviderModelConfig{}, false
	}
	providerName := ref.Provider
	modelName := ref.Model

	if providerName == "" || modelName == "" {
		return config.ProviderModelConfig{}, false
	}

	if prov, ok := cfg.Providers.GetNamed(providerName); ok {
		if modelCfg, exists := prov.Models[modelName]; exists {
			return modelCfg, true
		}

		normalizedAlias := strings.ToLower(strings.ReplaceAll(modelName, ".", "-"))
		if modelCfg, exists := prov.Models[normalizedAlias]; exists {
			return modelCfg, true
		}

		normalizedModelName := strings.ToLower(strings.ReplaceAll(modelName, ".", "-"))
		for alias, modelCfg := range prov.Models {
			resolvedModel := strings.TrimSpace(modelCfg.Model)
			if resolvedModel == "" {
				resolvedModel = alias
			}
			normalizedResolved := strings.ToLower(strings.ReplaceAll(resolvedModel, ".", "-"))
			if normalizedResolved == normalizedModelName {
				return modelCfg, true
			}
		}
	}

	return config.ProviderModelConfig{}, false
}

func getContextWindow(cfg *config.Config, model string, provider string) int {
	if modelCfg, ok := getProviderModelConfig(cfg, model, provider); ok && modelCfg.ContextWindow > 0 {
		return modelCfg.ContextWindow
	}
	return 128000
}

func getSupportsImages(cfg *config.Config, model string, provider string) bool {
	if modelCfg, ok := getProviderModelConfig(cfg, model, provider); ok {
		return modelCfg.Vision
	}
	return false
}

func getSupportsVideo(cfg *config.Config, model string, provider string) bool {
	if modelCfg, ok := getProviderModelConfig(cfg, model, provider); ok {
		return modelCfg.Video
	}
	return false
}

func getReasoningConfig(cfg *config.Config, model string, provider string) *config.ReasoningConfig {
	if modelCfg, ok := getProviderModelConfig(cfg, model, provider); ok && modelCfg.Reasoning != nil {
		return modelCfg.Reasoning
	}
	return nil
}

func getThinkingType(cfg *config.Config, model string, defaultProvider string) string {
	if modelCfg, ok := getProviderModelConfig(cfg, model, defaultProvider); ok {
		if tt, ok := config.NormalizeThinkingType(modelCfg.ThinkingType); ok && tt != config.ThinkingTypeAuto {
			return tt
		}
	}
	return ""
}

func NewAgentInstance(
	agentCfg *config.AgentConfig,
	defaults *config.AgentDefaults,
	cfg *config.Config,
) *AgentInstance {
	workspace := resolveAgentWorkspace(agentCfg, defaults)
	if err := InitializeWorkspace(workspace); err != nil {
		logger.ErrorCF("agent", "Failed to initialize workspace",
			map[string]interface{}{
				"workspace": workspace,
				"error":     err.Error(),
			})
	}

	model := resolveAgentModel(agentCfg, defaults, cfg)
	fallbacks := resolveAgentFallbacks(agentCfg, defaults, cfg)

	providerName := extractProviderFromModel(model, defaults.Provider)

	provider, err := providers.CreateProviderForCandidate(cfg, providerName)
	if err != nil {
		logger.WarnCF("agent", "Failed to create provider for agent, falling back to default",
			map[string]interface{}{
				"provider": providerName,
				"error":    err.Error(),
			})
		provider, err = providers.CreateProvider(cfg)
		if err != nil {
			logger.ErrorCF("agent", "Failed to create any provider",
				map[string]interface{}{
					"error": err.Error(),
				})
			provider = nil
		}
	}

	restrict := defaults.RestrictToWorkspace
	maxReadLines := defaults.MaxReadLines
	if maxReadLines <= 0 {
		maxReadLines = 500
	}
	memoryMaxLines := defaults.MemoryMaxLines
	if memoryMaxLines <= 0 {
		memoryMaxLines = 500
	}
	toolsRegistry := tools.NewToolRegistry()
	sendFileTool := tools.NewSendFileTool()
	sendFileTool.SetWorkspaceRestrictions(workspace, restrict)
	toolsRegistry.Register(sendFileTool)
	toolsRegistry.Register(tools.NewReadFileTool(workspace, restrict, maxReadLines))
	toolsRegistry.Register(tools.NewWriteFileTool(workspace, restrict))
	toolsRegistry.Register(tools.NewListDirTool(workspace, restrict))
	toolsRegistry.Register(tools.NewExecToolWithConfig(workspace, restrict, cfg))
	toolsRegistry.Register(tools.NewEditFileTool(workspace, restrict))
	toolsRegistry.Register(tools.NewAppendFileTool(workspace, restrict))
	toolsRegistry.Register(tools.NewUpdateMemoryTool(workspace, memoryMaxLines, restrict))

	toolsRegistry.Register(tools.NewSmartEditTool(workspace, restrict))
	toolsRegistry.Register(tools.NewPatchTool(workspace, restrict))
	toolsRegistry.Register(tools.NewSequentialReplaceTool(workspace, restrict))
	toolsRegistry.Register(tools.NewReadImageTool(workspace, restrict))
	videoCaps := tools.VideoCapabilities{
		Video:  getSupportsVideo(cfg, model, providerName),
		Vision: getSupportsImages(cfg, model, providerName),
	}
	var transcribe tools.TranscribeFunc
	if cfg != nil && cfg.Providers != nil && cfg.Providers.Groq.APIKey != "" {
		groqTranscriber := voice.NewGroqTranscriber(cfg.Providers.Groq.APIKey)
		transcribe = func(ctx context.Context, audioPath string) (string, error) {
			resp, err := groqTranscriber.Transcribe(ctx, audioPath)
			if err != nil {
				return "", err
			}
			return resp.Text, nil
		}
	}
	toolsRegistry.Register(tools.NewReadVideoTool(workspace, restrict, videoCaps, transcribe))

	if agentCfg != nil {
		applyToolsAllowlist(toolsRegistry, agentCfg.Tools, routing.NormalizeAgentID(agentCfg.ID))
	}

	sessionsManager := session.NewSessionManager()

	contextBuilder := NewContextBuilder(workspace)
	contextBuilder.SetToolsRegistry(toolsRegistry)
	contextBuilder.SetVisionSupported(videoCaps.Vision)
	contextBuilder.SetVideoSupported(videoCaps.Video)

	agentID := routing.DefaultAgentID
	agentName := ""
	var agentDescription string
	var subagents *config.SubagentsConfig
	var skillsFilter []string
	isDefault := false

	if agentCfg != nil {
		agentID = routing.NormalizeAgentID(agentCfg.ID)
		agentName = agentCfg.Name
		agentDescription = agentCfg.Description
		subagents = agentCfg.Subagents
		skillsFilter = agentCfg.Skills
		isDefault = agentCfg.Default
	}

	contextBuilder.SetSkillsFilter(skillsFilter)

	if subagents != nil && len(subagents.AllowAgents) > 0 {
		available := resolveAvailableSubagents(agentCfg, cfg)
		contextBuilder.SetAvailableSubagents(available)
	}

	maxIter := defaults.MaxToolIterations

	maxTokens := defaults.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	temperature := 0.7
	if defaults.Temperature != nil {
		temperature = *defaults.Temperature
	}
	if agentCfg != nil && agentCfg.Temperature != nil {
		temperature = *agentCfg.Temperature
	}

	modelCfg := providers.ModelConfig{
		Primary:   model,
		Fallbacks: fallbacks,
	}
	candidates := providers.ResolveCandidates(modelCfg, providerName)

	return &AgentInstance{
		ID:                    agentID,
		Name:                  agentName,
		Description:           agentDescription,
		Model:                 model,
		Fallbacks:             fallbacks,
		Workspace:             workspace,
		MaxIterations:         maxIter,
		MaxTokens:             maxTokens,
		Temperature:           temperature,
		ContextWindow:         getContextWindow(cfg, model, providerName),
		SupportsImages:        getSupportsImages(cfg, model, providerName),
		SupportsVideo:         getSupportsVideo(cfg, model, providerName),
		Reasoning:             getReasoningConfig(cfg, model, providerName),
		ThinkingLevel:         resolveAgentThinkingLevel(agentCfg, defaults),
		PromptCache:           defaults.PromptCache,
		Provider:              provider,
		Sessions:              sessionsManager,
		ContextBuilder:        contextBuilder,
		Tools:                 toolsRegistry,
		Subagents:             subagents,
		SkillsFilter:          skillsFilter,
		Candidates:            candidates,
		IsDefault:             isDefault,
		LLMLoopTimeoutMinutes: defaults.LLMLoopTimeoutMinutes,
	}
}

func resolveAgentWorkspace(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	if agentCfg != nil && strings.TrimSpace(agentCfg.Workspace) != "" {
		return expandHome(strings.TrimSpace(agentCfg.Workspace))
	}
	if agentCfg == nil || agentCfg.Default || agentCfg.ID == "" || routing.NormalizeAgentID(agentCfg.ID) == "main" {
		return expandHome(defaults.Workspace)
	}
	id := routing.NormalizeAgentID(agentCfg.ID)
	return filepath.Join(config.GetLeleDir(), "workspace-"+id)
}

func resolveAgentModel(agentCfg *config.AgentConfig, defaults *config.AgentDefaults, cfg *config.Config) string {
	logger.DebugCF("agent", "Resolving agent model",
		map[string]interface{}{
			"defaults_model":    defaults.Model,
			"defaults_provider": defaults.Provider,
		})
	if agentCfg != nil && agentCfg.Model != nil && strings.TrimSpace(agentCfg.Model.Primary) != "" {
		resolved := cfg.Providers.ResolveModelAlias(strings.TrimSpace(agentCfg.Model.Primary), defaults.Provider)
		logger.DebugCF("agent", "Agent model resolved",
			map[string]interface{}{
				"resolved": resolved,
			})
		return resolved
	}
	resolved := cfg.Providers.ResolveModelAlias(defaults.Model, defaults.Provider)
	logger.DebugCF("agent", "Agent model resolved (defaults)",
		map[string]interface{}{
			"resolved": resolved,
		})
	return resolved
}

func resolveAgentFallbacks(agentCfg *config.AgentConfig, defaults *config.AgentDefaults, cfg *config.Config) []string {
	resolve := func(in []string) []string {
		if in == nil {
			return nil
		}
		out := make([]string, 0, len(in))
		for _, model := range in {
			out = append(out, cfg.Providers.ResolveModelAlias(model, defaults.Provider))
		}
		return out
	}
	if agentCfg != nil && agentCfg.Model != nil && agentCfg.Model.Fallbacks != nil {
		return resolve(agentCfg.Model.Fallbacks)
	}
	return resolve(defaults.ModelFallbacks)
}

func resolveAgentThinkingLevel(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	raw := ""
	if defaults != nil && defaults.ThinkingLevel != nil {
		raw = *defaults.ThinkingLevel
	}
	if agentCfg != nil && agentCfg.ThinkingLevel != nil {
		raw = *agentCfg.ThinkingLevel
	}
	normalized, ok := config.NormalizeThinkingLevel(raw)
	if !ok {
		logger.WarnCF("agent", "Invalid thinking_level in config, treating as unset",
			map[string]interface{}{
				"raw_value": raw,
			})
		return ""
	}
	return normalized
}

func resolveAvailableSubagents(agentCfg *config.AgentConfig, cfg *config.Config) []subagentInfo {
	if agentCfg == nil || agentCfg.Subagents == nil {
		return nil
	}
	selfID := routing.NormalizeAgentID(agentCfg.ID)
	allowList := agentCfg.Subagents.AllowAgents
	if len(allowList) == 0 {
		return nil
	}

	wildcard := false
	allowedSet := make(map[string]bool, len(allowList))
	for _, a := range allowList {
		if a == "*" {
			wildcard = true
			break
		}
		allowedSet[routing.NormalizeAgentID(a)] = true
	}

	descMap := make(map[string]string, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		descMap[routing.NormalizeAgentID(ac.ID)] = ac.Description
	}

	var result []subagentInfo
	for _, ac := range cfg.Agents.List {
		id := routing.NormalizeAgentID(ac.ID)
		if id == selfID {
			continue
		}
		if wildcard || allowedSet[id] {
			result = append(result, subagentInfo{
				ID:          id,
				Description: ac.Description,
			})
		}
	}
	return result
}

func applyToolsAllowlist(registry *tools.ToolRegistry, allowed []string, agentID string) {
	if registry == nil {
		return
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		allowedSet[trimmed] = struct{}{}
	}
	if len(allowedSet) == 0 {
		return
	}

	registered := registry.List()
	sort.Strings(registered)

	var toRemove []string
	var unknown []string
	known := make(map[string]struct{}, len(registered))
	for _, name := range registered {
		known[name] = struct{}{}
		if _, keep := allowedSet[name]; !keep {
			toRemove = append(toRemove, name)
		}
	}
	for _, name := range allowed {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if _, ok := known[trimmed]; !ok {
			unknown = append(unknown, trimmed)
		}
	}
	if len(unknown) > 0 {
		slog.Warn("agent tools allowlist contains unknown tool names, ignoring them",
			"agent", agentID, "unknown_tools", unknown)
	}

	if len(registered) > 0 && len(toRemove) == len(registered) {
		slog.Error("agent tools allowlist matches no registered tool, keeping all tools",
			"agent", agentID, "allowlist", allowed, "available_tools", registered)
		return
	}

	for _, name := range toRemove {
		registry.Unregister(name)
	}
}

func expandHome(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		home, _ := os.UserHomeDir()
		if len(path) > 1 && path[1] == '/' {
			return home + path[1:]
		}
		return home
	}
	return path
}
