package tools

import (
	"context"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// captureProvider is a minimal LLMProvider used in tests. It records the tool
// definitions passed to each Chat call so tests can assert what was (or was
// not) advertised to the model.
type captureProvider struct {
	mu       sync.Mutex
	lastDefs []providers.ToolDefinition
}

func (p *captureProvider) Chat(_ context.Context, _ []providers.Message, tools []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.lastDefs = tools
	p.mu.Unlock()
	// Return a direct answer (no tool calls) so the loop exits after one iteration.
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *captureProvider) GetDefaultModel() string {
	return "test-model"
}

func (p *captureProvider) toolNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.lastDefs))
	for _, def := range p.lastDefs {
		names = append(names, def.Function.Name)
	}
	return names
}

func contains(name string, names []string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// mockTool is a trivial Tool used to verify that non-image tools survive the
// vision filtering in RunToolLoop.
type mockTool struct{}

func (mockTool) Name() string        { return "echo" }
func (mockTool) Description() string { return "echoes input" }
func (mockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (mockTool) Execute(_ context.Context, _ map[string]interface{}) *ToolResult {
	return &ToolResult{ForLLM: "ok"}
}

func TestRunToolLoop_FiltersReadImageWithoutVision(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(NewReadImageTool(t.TempDir(), false))
	registry.Register(mockTool{})

	provider := &captureProvider{}
	messages := []providers.Message{{Role: "user", Content: "hello"}}

	t.Run("filters read_image when vision unsupported", func(t *testing.T) {
		_, err := RunToolLoop(context.Background(), ToolLoopConfig{
			Provider:        provider,
			Model:           "test-model",
			Tools:           registry,
			MaxIterations:   1,
			VisionSupported: false,
		}, messages, "cli", "direct")
		if err != nil {
			t.Fatalf("RunToolLoop returned error: %v", err)
		}
		names := provider.toolNames()
		if contains("read_image", names) {
			t.Fatalf("read_image should be filtered out when VisionSupported=false, got tools: %v", names)
		}
		if !contains("echo", names) {
			t.Fatalf("non-image tools should be preserved, got tools: %v", names)
		}
	})

	t.Run("keeps read_image when vision supported", func(t *testing.T) {
		_, err := RunToolLoop(context.Background(), ToolLoopConfig{
			Provider:        provider,
			Model:           "test-model",
			Tools:           registry,
			MaxIterations:   1,
			VisionSupported: true,
		}, messages, "cli", "direct")
		if err != nil {
			t.Fatalf("RunToolLoop returned error: %v", err)
		}
		names := provider.toolNames()
		if !contains("read_image", names) {
			t.Fatalf("read_image should be present when VisionSupported=true, got tools: %v", names)
		}
	})

	t.Run("filters read_image by default (zero value)", func(t *testing.T) {
		_, err := RunToolLoop(context.Background(), ToolLoopConfig{
			Provider:      provider,
			Model:         "test-model",
			Tools:         registry,
			MaxIterations: 1,
		}, messages, "cli", "direct")
		if err != nil {
			t.Fatalf("RunToolLoop returned error: %v", err)
		}
		names := provider.toolNames()
		if contains("read_image", names) {
			t.Fatalf("read_image should be filtered out by default (zero value), got tools: %v", names)
		}
	})
}
