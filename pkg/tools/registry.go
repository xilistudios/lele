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

// ToolRegistry is the tool set an agent exposes to the model.
//
// A registry may be SCOPED: NewScopedRegistry builds an empty child layered on
// top of a base registry. Registrations always land in the child (the layer
// owns only what it was given) while lookups fall back to the base, so a
// scoped registry behaves like "base + this session's extras" without copying
// or freezing the base. That is how MCP tools stay session-local: the base
// registry (agent-wide, shared by every chat) never learns about them.
type ToolRegistry struct {
	tools map[string]Tool
	mu    sync.RWMutex
	// parent is the read-only fallback layer of a scoped registry; nil for a
	// plain (unscoped) registry, which is the common case.
	parent *ToolRegistry
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// NewScopedRegistry returns an empty registry layered over base: registrations
// land in the returned layer (shadowing base names), and every read
// (Get/List/ToProviderDefs/GetDefinitions/Count/GetSummaries) resolves
// against the layer first and base second.
//
// The base is read live on every call — never snapshotted — so tools added to
// or removed from the base after the layer was created (a config reload) are
// seen immediately, and Unregister on the layer never touches the base.
func NewScopedRegistry(base *ToolRegistry) *ToolRegistry {
	return &ToolRegistry{
		tools:  make(map[string]Tool),
		parent: base,
	}
}

// IsScoped reports whether the registry has a parent layer. Exposed for tests
// and diagnostics; callers never need it to read through the layer.
func (r *ToolRegistry) IsScoped() bool {
	return r != nil && r.parent != nil
}

func (r *ToolRegistry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
}

// Unregister removes the tool with the given name from the registry.
// It is a no-op if no such tool is registered. Used to keep per-feature
// tools (e.g. group_chat) in sync with configuration on reload.
//
// On a scoped registry it only removes the layer's own entry: a name that
// lives in the parent layer stays reachable through the fallback (a session
// layer must never be able to delete an agent-wide tool).
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

// collectNames appends the names of this layer and every ancestor, skipping
// duplicates so a shadowed parent name is reported once. It takes at most one
// lock at a time (own lock released before recursing) so two registries in a
// parent chain can never deadlock against each other.
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

// mergedNames returns every visible name (own layer + ancestors), sorted for
// deterministic output.
func (r *ToolRegistry) mergedNames() []string {
	seen := make(map[string]struct{})
	// Size hint only; the map itself is read under collectNames' lock (reading
	// r.tools here would race with a concurrent Register).
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

// ExecuteWithContext executes a tool with channel/chatID context and optional async callback.
// If the tool implements AsyncTool and a non-nil callback is provided,
// the callback will be set on the tool before execution.
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

	// If tool implements AsyncTool and callback is provided, set callback
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

	// Log based on result type
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
			continue // unregistered concurrently
		}
		definitions = append(definitions, ToolToSchema(tool))
	}
	return definitions
}

// ToProviderDefs converts tool definitions to provider-compatible format.
// This is the format expected by LLM provider APIs.
func (r *ToolRegistry) ToProviderDefs() []providers.ToolDefinition {
	names := r.mergedNames()

	definitions := make([]providers.ToolDefinition, 0, len(names))
	for _, name := range names {
		tool, ok := r.Get(name)
		if !ok {
			continue // unregistered concurrently
		}
		schema := ToolToSchema(tool)

		// Safely extract nested values with type checks
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

// List returns a list of all registered tool names.
func (r *ToolRegistry) List() []string {
	return r.mergedNames()
}

// Count returns the number of registered tools.
func (r *ToolRegistry) Count() int {
	return len(r.mergedNames())
}

// CloneWithout returns a shallow clone of the registry excluding the named tools.
// Tool instances are shared with the original registry.
//
// A scoped registry is FLATTENED: the clone is a plain registry holding the
// merged view (layer + parent), so the copy keeps working even if the original
// layer is dropped afterwards.
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
			continue // unregistered concurrently
		}
		clone.tools[name] = tool
	}

	return clone
}

// GetSummaries returns human-readable summaries of all registered tools.
// Returns a slice of "name - description" strings.
func (r *ToolRegistry) GetSummaries() []string {
	names := r.mergedNames()

	summaries := make([]string, 0, len(names))
	for _, name := range names {
		tool, ok := r.Get(name)
		if !ok {
			continue // unregistered concurrently
		}
		summaries = append(summaries, fmt.Sprintf("- `%s` - %s", tool.Name(), tool.Description()))
	}
	return summaries
}
