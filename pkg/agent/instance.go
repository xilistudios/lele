package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
)

// extractProviderFromModel extracts the provider name from a model string.
// Delegates to providers.ParseModelRef for consistent parsing.
// If model is "provider:model-name", returns "provider".
// If model has no provider prefix, returns defaultProvider.
func extractProviderFromModel(model, defaultProvider string) string {
	ref := providers.ParseModelRef(model, defaultProvider)
	if ref == nil {
		return strings.ToLower(strings.TrimSpace(defaultProvider))
	}
	return ref.Provider
}

// AgentInstance represents a fully configured agent with its own workspace,
// session manager, context builder, and tool registry.
type AgentInstance struct {
	ID             string
	Name           string
	Model          string
	Fallbacks      []string
	Workspace      string
	MaxIterations  int
	MaxTokens      int
	Temperature    float64
	ContextWindow  int
	SupportsImages bool
	Reasoning      *config.ReasoningConfig // Reasoning configuration for the model
	Provider       providers.LLMProvider
	Sessions       *session.SessionManager
	ContextBuilder *ContextBuilder
	Tools          *tools.ToolRegistry
	Subagents      *config.SubagentsConfig
	SkillsFilter   []string
	Candidates     []providers.FallbackCandidate
	IsDefault      bool
}

// NewAgentInstance creates an agent instance from config.

func getProviderModelConfig(cfg *config.Config, model string, defaultProvider string) (config.ProviderModelConfig, bool) {
	// The model parameter is the raw model specification which may be:
	// 1. An alias like "myprovider:vision-model" that maps to a different resolved model
	// 2. A direct model reference like "myprovider:gpt-4o-vision"
	// We handle both by attempting multiple lookup strategies:
	// - First: exact alias match
	// - Second: normalized alias match (lowercase, dots replaced with dashes)
	// - Third: search by resolved model name in the Model field
	model = strings.TrimSpace(model)

	// Use ParseModelRef for consistent provider:model parsing
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
		// Case 1: Try lookup by alias (exact match)
		if modelCfg, exists := prov.Models[modelName]; exists {
			return modelCfg, true
		}

		// Case 2: Try lookup by normalized alias (lowercase, replace . with -)
		normalizedAlias := strings.ToLower(strings.ReplaceAll(modelName, ".", "-"))
		if modelCfg, exists := prov.Models[normalizedAlias]; exists {
			return modelCfg, true
		}

		// Case 3: The modelName might be a resolved model name (e.g., "gpt-4o-vision")
		// Search for an entry where the Model field matches
		normalizedModelName := strings.ToLower(strings.ReplaceAll(modelName, ".", "-"))
		for alias, modelCfg := range prov.Models {
			resolvedModel := strings.TrimSpace(modelCfg.Model)
			if resolvedModel == "" {
				// If Model field is empty, treat the alias as the resolved name
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

// getContextWindow returns the context window for a model from provider config.
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

// getReasoningConfig returns the reasoning configuration for a model from provider config.
func getReasoningConfig(cfg *config.Config, model string, provider string) *config.ReasoningConfig {
	if modelCfg, ok := getProviderModelConfig(cfg, model, provider); ok && modelCfg.Reasoning != nil {
		return modelCfg.Reasoning
	}
	return nil
}

func NewAgentInstance(
	agentCfg *config.AgentConfig,
	defaults *config.AgentDefaults,
	cfg *config.Config,
) *AgentInstance {
	workspace := resolveAgentWorkspace(agentCfg, defaults)
	// Initialize workspace with template context files
	// This creates the directory and copies AGENT.md, SOUL.md, etc.
	if err := InitializeWorkspace(workspace); err != nil {
		logger.ErrorCF("agent", "Failed to initialize workspace",
			map[string]interface{}{
				"workspace": workspace,
				"error":     err.Error(),
			})
	}

	model := resolveAgentModel(agentCfg, defaults, cfg)
	fallbacks := resolveAgentFallbacks(agentCfg, defaults, cfg)

	// Extract provider name from the agent's model specification
	// This allows each agent to use its own provider based on its model config
	providerName := extractProviderFromModel(model, defaults.Provider)

	// Create a provider specifically for this agent
	provider, err := providers.CreateProviderForCandidate(cfg, providerName)
	if err != nil {
		logger.WarnCF("agent", "Failed to create provider for agent, falling back to default",
			map[string]interface{}{
				"provider": providerName,
				"error":    err.Error(),
			})
		// Fallback: try to create default provider
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
	toolsRegistry := tools.NewToolRegistry()
	toolsRegistry.Register(tools.NewReadFileTool(workspace, restrict, maxReadLines))
	toolsRegistry.Register(tools.NewWriteFileTool(workspace, restrict))
	toolsRegistry.Register(tools.NewListDirTool(workspace, restrict))
	toolsRegistry.Register(tools.NewExecToolWithConfig(workspace, restrict, cfg))
	toolsRegistry.Register(tools.NewEditFileTool(workspace, restrict))
	toolsRegistry.Register(tools.NewAppendFileTool(workspace, restrict))

	// Advanced editing tools. The legacy FMOD preview/apply workflow is deprecated.
	toolsRegistry.Register(tools.NewSmartEditTool(workspace, restrict))
	// toolsRegistry.Register(tools.NewPreviewTool(workspace, restrict))     // DEPRECATED
	// toolsRegistry.Register(tools.NewApplyTool(workspace, restrict))        // DEPRECATED
	toolsRegistry.Register(tools.NewPatchTool(workspace, restrict))
	toolsRegistry.Register(tools.NewSequentialReplaceTool(workspace, restrict))
	// Always register read_image tool so it's available when the user switches to a vision model.
	// It will be filtered out from tool definitions if the current session model doesn't support vision.
	toolsRegistry.Register(tools.NewReadImageTool(workspace, restrict))

	// SessionManager uses SQLite for persistence. Each agent's Sessions field
	// is replaced with a shared SessionManager instance when created through
	// AgentLoop. This per-agent one serves as a fallback for direct
	// instantiation (e.g., tests).
	sessionsManager := session.NewSessionManager()

	contextBuilder := NewContextBuilder(workspace)
	contextBuilder.SetToolsRegistry(toolsRegistry)

	agentID := routing.DefaultAgentID
	agentName := ""
	var subagents *config.SubagentsConfig
	var skillsFilter []string
	isDefault := false

	if agentCfg != nil {
		agentID = routing.NormalizeAgentID(agentCfg.ID)
		agentName = agentCfg.Name
		subagents = agentCfg.Subagents
		skillsFilter = agentCfg.Skills
		isDefault = agentCfg.Default
	}

	// Resolve available subagents from config and inject into system prompt.
	if subagents != nil && len(subagents.AllowAgents) > 0 {
		available := resolveAvailableSubagents(agentCfg, cfg)
		contextBuilder.SetAvailableSubagents(available)
	}

	maxIter := defaults.MaxToolIterations
	// 0 means unlimited — loop runs until LLM stops calling tools

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

	// Resolve fallback candidates using the agent's provider
	modelCfg := providers.ModelConfig{
		Primary:   model,
		Fallbacks: fallbacks,
	}
	candidates := providers.ResolveCandidates(modelCfg, providerName)

	return &AgentInstance{
		ID:             agentID,
		Name:           agentName,
		Model:          model,
		Fallbacks:      fallbacks,
		Workspace:      workspace,
		MaxIterations:  maxIter,
		MaxTokens:      maxTokens,
		Temperature:    temperature,
		ContextWindow:  getContextWindow(cfg, model, providerName),
		SupportsImages: getSupportsImages(cfg, model, providerName),
		Reasoning:      getReasoningConfig(cfg, model, providerName),
		Provider:       provider,
		Sessions:       sessionsManager,
		ContextBuilder: contextBuilder,
		Tools:          toolsRegistry,
		Subagents:      subagents,
		SkillsFilter:   skillsFilter,
		Candidates:     candidates,
		IsDefault:      isDefault,
	}
}

// resolveAgentWorkspace determines the workspace directory for an agent.
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

// resolveAgentModel resolves the primary model for an agent.
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

// resolveAgentFallbacks resolves the fallback models for an agent.
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

// resolveAvailableSubagents builds the list of subagents that an agent can
// delegate to, based on its allow_agents config and the full agent list.
// If allow_agents contains "*", all agents (except self) are included.
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

	// Build a lookup from agent config list for descriptions.
	descMap := make(map[string]string, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		descMap[routing.NormalizeAgentID(ac.ID)] = ac.Description
	}

	var result []subagentInfo
	for _, ac := range cfg.Agents.List {
		id := routing.NormalizeAgentID(ac.ID)
		if id == selfID {
			continue // don't list self
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
