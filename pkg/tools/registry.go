package tools

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

type ToolRegistry struct {
	tools  map[string]Tool
	mu     sync.RWMutex
	parent *ToolRegistry
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

func NewScopedRegistry(base *ToolRegistry) *ToolRegistry {
	return &ToolRegistry{
		tools:  make(map[string]Tool),
		parent: base,
	}
}

func (r *ToolRegistry) IsScoped() bool {
	return r != nil && r.parent != nil
}

func (r *ToolRegistry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
}

func (r *ToolRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

func (r *ToolRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	r.mu.RUnlock()
	if ok || r.parent == nil {
		return tool, ok
	}
	return r.parent.Get(name)
}

func (r *ToolRegistry) collectNames(seen map[string]struct{}, out *[]string) {
	r.mu.RLock()
	for name := range r.tools {
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		*out = append(*out, name)
	}
	r.mu.RUnlock()
	if r.parent != nil {
		r.parent.collectNames(seen, out)
	}
}

func (r *ToolRegistry) mergedNames() []string {
	seen := make(map[string]struct{})
	r.mu.RLock()
	hint := len(r.tools)
	r.mu.RUnlock()
	names := make([]string, 0, hint)
	r.collectNames(seen, &names)
	sort.Strings(names)
	return names
}

func (r *ToolRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) *ToolResult {
	return r.ExecuteWithContext(ctx, name, args, "", "", nil)
}

func (r *ToolRegistry) ExecuteWithContext(ctx context.Context, name string, args map[string]interface{}, channel, chatID string, asyncCallback AsyncCallback) *ToolResult {
	logger.InfoCF("tool", "Tool execution started",
		map[string]interface{}{
			"tool": name,
			"args": args,
		})

	tool, ok := r.Get(name)
	if !ok {
		logger.ErrorCF("tool", "Tool not found",
			map[string]interface{}{
				"tool": name,
			})
		return ErrorResult(fmt.Sprintf("tool %q not found", name)).WithError(fmt.Errorf("tool not found"))
	}

	if channel != "" && chatID != "" {
		if contextualTool, ok := tool.(ContextualTool); ok {
			contextualTool.SetContext(channel, chatID)
		}
		ctx = WithToolContext(ctx, channel, chatID)
	}

	if asyncTool, ok := tool.(AsyncTool); ok && asyncCallback != nil {
		asyncTool.SetCallback(asyncCallback)
		logger.DebugCF("tool", "Async callback injected",
			map[string]interface{}{
				"tool": name,
			})
	}

	start := time.Now()
	result := tool.Execute(ctx, args)
	duration := time.Since(start)

	if result.IsError {
		logger.ErrorCF("tool", "Tool execution failed",
			map[string]interface{}{
				"tool":     name,
				"duration": duration.Milliseconds(),
				"error":    result.ForLLM,
			})
	} else if result.Async {
		logger.InfoCF("tool", "Tool started (async)",
			map[string]interface{}{
				"tool":     name,
				"duration": duration.Milliseconds(),
			})
	} else {
		logger.InfoCF("tool", "Tool execution completed",
			map[string]interface{}{
				"tool":          name,
				"duration_ms":   duration.Milliseconds(),
				"result_length": len(result.ForLLM),
			})
	}

	return result
}

func (r *ToolRegistry) GetDefinitions() []map[string]interface{} {
	names := r.mergedNames()

	definitions := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		tool, ok := r.Get(name)
		if !ok {
			continue
		}
		definitions = append(definitions, ToolToSchema(tool))
	}
	return definitions
}

func (r *ToolRegistry) ToProviderDefs() []providers.ToolDefinition {
	names := r.mergedNames()

	definitions := make([]providers.ToolDefinition, 0, len(names))
	for _, name := range names {
		tool, ok := r.Get(name)
		if !ok {
			continue
		}
		schema := ToolToSchema(tool)

		fn, ok := schema["function"].(map[string]interface{})
		if !ok {
			continue
		}

		name, _ := fn["name"].(string)
		desc, _ := fn["description"].(string)
		params, _ := fn["parameters"].(map[string]interface{})

		definitions = append(definitions, providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        name,
				Description: desc,
				Parameters:  params,
			},
		})
	}
	return definitions
}

func (r *ToolRegistry) List() []string {
	return r.mergedNames()
}

func (r *ToolRegistry) Count() int {
	return len(r.mergedNames())
}

func (r *ToolRegistry) CloneWithout(excluded ...string) *ToolRegistry {
	excludedSet := make(map[string]struct{}, len(excluded))
	for _, name := range excluded {
		excludedSet[name] = struct{}{}
	}

	clone := NewToolRegistry()

	for _, name := range r.mergedNames() {
		if _, skip := excludedSet[name]; skip {
			continue
		}
		tool, ok := r.Get(name)
		if !ok {
			continue
		}
		clone.tools[name] = tool
	}

	return clone
}

func (r *ToolRegistry) GetSummaries() []string {
	names := r.mergedNames()

	summaries := make([]string, 0, len(names))
	for _, name := range names {
		tool, ok := r.Get(name)
		if !ok {
			continue
		}
		summaries = append(summaries, fmt.Sprintf("- `%s` - %s", tool.Name(), tool.Description()))
	}
	return summaries
}
