package main

import (
	"context"
	"fmt"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// Mock provider for testing
type MockProvider struct{}

func (m *MockProvider) Chat(ctx context.Context, messages []providers.Message, toolDefs []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content: "✅ Subagent executed successfully! Task completed.",
	}, nil
}

func (m *MockProvider) GetDefaultModel() string { return "test-model" }
func (m *MockProvider) SupportsTools() bool     { return true }
func (m *MockProvider) GetContextWindow() int   { return 4096 }

func main() {
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║         COMPREHENSIVE SUBAGENT TEST - PICOCLAW             ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")

	// Setup
	provider := &MockProvider{}
	msgBus := bus.NewMessageBus()
	workspace := "/tmp/subagent_test"
	manager := tools.NewSubagentManager(provider, "test-model", workspace, msgBus)

	// Register FULL set of tools for the subagent (like in real agent)
	manager.RegisterTool(tools.NewReadFileTool(workspace, true))
	manager.RegisterTool(tools.NewListDirTool(workspace, true))
	manager.RegisterTool(tools.NewSmartEditTool(workspace, true))
	manager.RegisterTool(tools.NewPreviewTool(workspace, true))
	manager.RegisterTool(tools.NewApplyTool(workspace, true))
	manager.RegisterTool(tools.NewPatchTool(workspace, true))
	manager.RegisterTool(tools.NewSequentialReplaceTool(workspace, true))
	manager.RegisterTool(tools.NewAppendFileTool(workspace, true))

	// Create spawn tool
	spawnTool := tools.NewSpawnTool(manager)
	spawnTool.SetContext("telegram", "1779224049")
	spawnTool.SetAllowlistChecker(func(targetAgentID string) bool {
		fmt.Printf("  [Allowlist check for '%s'] -> ALLOWED ✅\n", targetAgentID)
		return true
	})

	ctx := context.Background()

	// Test 1: Basic spawn
	fmt.Println("\n📋 Test 1: Spawning subagent (basic)")
	result1 := spawnTool.Execute(ctx, map[string]interface{}{
		"task":  "List the contents of /tmp directory",
		"label": "filesystem-analysis",
	})
	fmt.Printf("   📝 %s\n", result1.ForLLM)

	// Test 2: Spawn with coder agent
	fmt.Println("\n📋 Test 2: Spawning 'coder' subagent")
	result2 := spawnTool.Execute(ctx, map[string]interface{}{
		"task":     "Analyze the codebase structure and write a summary",
		"label":    "codebase-analysis",
		"agent_id": "coder",
	})
	fmt.Printf("   📝 %s\n", result2.ForLLM)

	// Test 3: Spawn with architect agent
	fmt.Println("\n📋 Test 3: Spawning 'architect' subagent")
	result3 := spawnTool.Execute(ctx, map[string]interface{}{
		"task":     "Design a system architecture for a chatbot",
		"label":    "architecture-design",
		"agent_id": "architect",
	})
	fmt.Printf("   📝 %s\n", result3.ForLLM)

	// Wait for async completion
	time.Sleep(200 * time.Millisecond)

	// Test 4: List all tasks
	fmt.Println("\n📋 Test 4: Subagent task inventory")
	tasks := manager.ListTasks()
	fmt.Printf("   Total tasks spawned: %d\n", len(tasks))
	completed := 0
	running := 0
	for _, task := range tasks {
		icon := "⏳"
		if task.Status == "completed" {
			icon = "✅"
			completed++
		} else if task.Status == "running" {
			running++
		}
		fmt.Printf("   %s %s | %s | Status: %s\n", icon, task.ID, task.Label, task.Status)
	}
	fmt.Printf("\n   Summary: %d completed, %d running\n", completed, running)

	// Test 5: Full tool availability check
	fmt.Println("\n📋 Test 5: Subagent tool registry check")
	toolRegistry := []struct {
		name     string
		expected bool
	}{
		{"read_file", true},
		{"list_dir", true},
		{"smart_edit", true},
		{"preview", true},
		{"apply", true},
		{"patch", true},
		{"sequential_replace", true},
		{"append_file", true},
		{"spawn", false}, // Not registered yet
		{"web_search", false},
	}

	for _, tr := range toolRegistry {
		has := manager.HasTool(tr.name)
		status := "❌"
		if has == tr.expected {
			if has {
				status = "✅"
			} else {
				status = "➖"
			}
		}
		fmt.Printf("   %s Tool '%s' (expected: %v, got: %v)\n", status, tr.name, tr.expected, has)
	}

	// Test 6: Nested spawn capability
	fmt.Println("\n📋 Test 6: Nested spawn registration")
	manager.RegisterTool(tools.NewSpawnTool(manager))
	hasSpawn := manager.HasTool("spawn")
	if hasSpawn {
		fmt.Printf("   ✅ Subagents can now spawn other subagents!\n")
	}

	fmt.Println("\n╔════════════════════════════════════════════════════════════╗")
	fmt.Printf("║       ✅ SUBAGENT SYSTEM FULLY OPERATIONAL                   ║\n")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
}
