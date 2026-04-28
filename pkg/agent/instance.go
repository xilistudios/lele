package agent

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
)

// extractProviderFromModel extracts the provider name from a model string.
// If model is "provider/model-name", returns "provider".
// If model has no provider prefix, returns defaultProvider.
func extractProviderFromModel(model, defaultProvider string) string {
	model = strings.TrimSpace(model)
	if idx := strings.Index(model, "/"); idx > 0 {
		return strings.ToLower(strings.TrimSpace(model[:idx]))
	}
	return strings.ToLower(strings.TrimSpace(defaultProvider))
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
}

// NewAgentInstance creates an agent instance from config.

func getProviderModelConfig(cfg *config.Config, model string, defaultProvider string) (config.ProviderModelConfig, bool) {
	// The model parameter is the raw model specification which may be:
	// 1. An alias like "myprovider/vision-model" that maps to a different resolved model
	// 2. A direct model reference like "myprovider/gpt-4o-vision"
	// We handle both by attempting multiple lookup strategies:
	// - First: exact alias match
	// - Second: normalized alias match (lowercase, dots replaced with dashes)
	// - Third: search by resolved model name in the Model field
	model = strings.TrimSpace(model)

	// Extract provider and model name from the model string
	var providerName, modelName string
	if idx := strings.Index(model, "/"); idx > 0 {
		providerName = strings.ToLower(strings.TrimSpace(model[:idx]))
		modelName = strings.TrimSpace(model[idx+1:])
	} else {
		providerName = strings.ToLower(strings.TrimSpace(defaultProvider))
		modelName = model
	}

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
		log.Printf("[ERROR] Failed to initialize workspace %q: %v", workspace, err)
	}

	model := resolveAgentModel(agentCfg, defaults, cfg)
	fallbacks := resolveAgentFallbacks(agentCfg, defaults, cfg)

	// Extract provider name from the agent's model specification
	// This allows each agent to use its own provider based on its model config
	providerName := extractProviderFromModel(model, defaults.Provider)

	// Create a provider specifically for this agent
	provider, err := providers.CreateProviderForCandidate(cfg, providerName)
	if err != nil {
		log.Printf("[WARN] Failed to create provider '%s' for agent, falling back to default: %v", providerName, err)
		// Fallback: try to create default provider
		provider, err = providers.CreateProvider(cfg)
		if err != nil {
			log.Printf("[ERROR] Failed to create any provider: %v", err)
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
	if getSupportsImages(cfg, model, providerName) {
		toolsRegistry.Register(tools.NewReadImageTool(workspace, restrict))
	}

	sessionsDir := filepath.Join(workspace, "sessions")
	sessionsManager := session.NewSessionManager(sessionsDir)

	contextBuilder := NewContextBuilder(workspace)
	contextBuilder.SetToolsRegistry(toolsRegistry)

	agentID := routing.DefaultAgentID
	agentName := ""
	var subagents *config.SubagentsConfig
	var skillsFilter []string

	if agentCfg != nil {
		agentID = routing.NormalizeAgentID(agentCfg.ID)
		agentName = agentCfg.Name
		subagents = agentCfg.Subagents
		skillsFilter = agentCfg.Skills
	}

	maxIter := defaults.MaxToolIterations
	if maxIter == 0 {
		maxIter = 20
	}

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
	home, _ := os.UserHomeDir()
	id := routing.NormalizeAgentID(agentCfg.ID)
	return filepath.Join(home, ".lele", "workspace-"+id)
}

// resolveAgentModel resolves the primary model for an agent.
func resolveAgentModel(agentCfg *config.AgentConfig, defaults *config.AgentDefaults, cfg *config.Config) string {
	log.Printf("[DEBUG] resolveAgentModel: defaults.Model=%s, defaults.Provider=%s", defaults.Model, defaults.Provider)
	if agentCfg != nil && agentCfg.Model != nil && strings.TrimSpace(agentCfg.Model.Primary) != "" {
		resolved := cfg.Providers.ResolveModelAlias(strings.TrimSpace(agentCfg.Model.Primary), defaults.Provider)
		log.Printf("[DEBUG] resolveAgentModel: resolved=%s", resolved)
		return resolved
	}
	resolved := cfg.Providers.ResolveModelAlias(defaults.Model, defaults.Provider)
	log.Printf("[DEBUG] resolveAgentModel: resolved=%s", resolved)
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
