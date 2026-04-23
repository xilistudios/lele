// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

// TestGetGlobalConfigDir_NormalCase tests getGlobalConfigDir with normal home directory
func TestGetGlobalConfigDir_NormalCase(t *testing.T) {
	dir := getGlobalConfigDir()

	// Should return a non-empty path
	if dir == "" {
		t.Error("Expected non-empty config dir, got empty string")
	}

	// Should end with .lele
	if !strings.HasSuffix(dir, ".lele") {
		t.Errorf("Expected path to end with .lele, got: %s", dir)
	}

	// Should contain the home directory
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("Cannot get home dir: %v", err)
	}

	if !strings.HasPrefix(dir, home) {
		t.Errorf("Expected path to start with home dir %s, got: %s", home, dir)
	}
}

// TestNewContextBuilder_NormalCase tests creating a ContextBuilder with valid workspace
func TestNewContextBuilder_NormalCase(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	if cb == nil {
		t.Fatal("Expected ContextBuilder, got nil")
	}

	if cb.workspace != tmpDir {
		t.Errorf("Expected workspace %s, got %s", tmpDir, cb.workspace)
	}

	if cb.skillsLoader == nil {
		t.Error("Expected skillsLoader to be initialized")
	}

	if cb.memory == nil {
		t.Error("Expected memory to be initialized")
	}

	if cb.tools != nil {
		t.Error("Expected tools to be nil initially")
	}
}

// TestNewContextBuilder_EmptyWorkspace tests creating a ContextBuilder with empty workspace
func TestNewContextBuilder_EmptyWorkspace(t *testing.T) {
	cb := NewContextBuilder("")

	if cb == nil {
		t.Fatal("Expected ContextBuilder, got nil")
	}

	if cb.workspace != "" {
		t.Errorf("Expected empty workspace, got %s", cb.workspace)
	}

	if cb.skillsLoader == nil {
		t.Error("Expected skillsLoader to be initialized even with empty workspace")
	}

	if cb.memory == nil {
		t.Error("Expected memory to be initialized even with empty workspace")
	}
}

// TestSetToolsRegistry tests setting the tools registry
func TestSetToolsRegistry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Initially tools should be nil
	if cb.tools != nil {
		t.Error("Expected tools to be nil initially")
	}

	// Create and set a tools registry
	registry := tools.NewToolRegistry()
	cb.SetToolsRegistry(registry)

	if cb.tools != registry {
		t.Error("Expected tools to be set to registry")
	}

	// Test setting nil registry
	cb.SetToolsRegistry(nil)
	if cb.tools != nil {
		t.Error("Expected tools to be nil after setting nil registry")
	}
}

// TestGetIdentity_ContentVerification tests that getIdentity returns expected content
func TestGetIdentity_ContentVerification(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	identity := cb.getIdentity()

	// Check for expected sections
	expectedSections := []string{
		"# lele",
		"You are lele",
		"## Runtime",
		"## Workspace",
		"## Important Rules",
		"ALWAYS use tools",
		"Be helpful and accurate",
		"Memory",
	}

	for _, section := range expectedSections {
		if !strings.Contains(identity, section) {
			t.Errorf("Expected identity to contain '%s'", section)
		}
	}

	// Check runtime info
	expectedRuntime := runtime.GOOS + " " + runtime.GOARCH
	if !strings.Contains(identity, expectedRuntime) {
		t.Errorf("Expected identity to contain runtime info '%s'", expectedRuntime)
	}

	// Check workspace path
	absPath, _ := filepath.Abs(tmpDir)
	if !strings.Contains(identity, absPath) {
		t.Errorf("Expected identity to contain workspace path '%s'", absPath)
	}
}

// TestGetIdentity_WithTools tests getIdentity when tools registry is set
func TestGetIdentity_WithTools(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Without tools, should not have Available Tools section
	identity := cb.getIdentity()
	if strings.Contains(identity, "## Available Tools") {
		t.Error("Expected no Available Tools section when tools is nil")
	}

	// Create and set a tools registry with a mock tool
	registry := tools.NewToolRegistry()
	cb.SetToolsRegistry(registry)

	// With empty registry, should still not have Available Tools section
	identity = cb.getIdentity()
	if strings.Contains(identity, "## Available Tools") {
		t.Error("Expected no Available Tools section when registry is empty")
	}
}

// TestBuildToolsSection_WithTools tests buildToolsSection with registered tools
func TestBuildToolsSection_WithTools(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Test with nil tools
	section := cb.buildToolsSection()
	if section != "" {
		t.Errorf("Expected empty section with nil tools, got: %s", section)
	}

	// Create registry with a mock tool
	registry := tools.NewToolRegistry()

	// Register a simple mock tool
	mockTool := &mockTool{
		name:        "test_tool",
		description: "A test tool for testing",
	}
	registry.Register(mockTool)

	cb.SetToolsRegistry(registry)

	section = cb.buildToolsSection()

	// Should contain tool information
	if !strings.Contains(section, "## Available Tools") {
		t.Error("Expected section to contain '## Available Tools'")
	}

	if !strings.Contains(section, "test_tool") {
		t.Error("Expected section to contain tool name")
	}

	if !strings.Contains(section, "A test tool for testing") {
		t.Error("Expected section to contain tool description")
	}
}

// TestBuildToolsSection_EmptyRegistry tests buildToolsSection with empty registry
func TestBuildToolsSection_EmptyRegistry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Create empty registry
	registry := tools.NewToolRegistry()
	cb.SetToolsRegistry(registry)

	section := cb.buildToolsSection()
	if section != "" {
		t.Errorf("Expected empty section with empty registry, got: %s", section)
	}
}

// TestBuildSystemPrompt tests BuildSystemPrompt method
func TestBuildSystemPrompt(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	prompt := cb.BuildSystemPrompt()

	// Should contain identity
	if !strings.Contains(prompt, "# lele") {
		t.Error("Expected prompt to contain lele header")
	}

	// Should be non-empty
	if prompt == "" {
		t.Error("Expected non-empty system prompt")
	}
}

// TestResetMemoryContext tests ResetMemoryContext method
func TestResetMemoryContext(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// This method currently does nothing (no-op for future caching)
	// Just verify it doesn't panic
	cb.ResetMemoryContext()
}

// TestLoadBootstrapFiles_NoFiles tests LoadBootstrapFiles when no bootstrap files exist
func TestLoadBootstrapFiles_NoFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	result := cb.LoadBootstrapFiles()

	// Should return empty string when no files exist
	if result != "" {
		t.Errorf("Expected empty result when no bootstrap files exist, got: %s", result)
	}
}

// TestLoadBootstrapFiles_WithFiles tests LoadBootstrapFiles with existing files
func TestLoadBootstrapFiles_WithFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create some bootstrap files
	bootstrapFiles := map[string]string{
		"AGENT.md":    "This is the AGENT content",
		"SOUL.md":     "This is the SOUL content",
		"USER.md":     "This is the USER content",
		"IDENTITY.md": "This is the IDENTITY content",
	}

	for filename, content := range bootstrapFiles {
		path := filepath.Join(tmpDir, filename)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create %s: %v", filename, err)
		}
	}

	cb := NewContextBuilder(tmpDir)
	result := cb.LoadBootstrapFiles()

	// Should contain content from all files
	for filename, content := range bootstrapFiles {
		if !strings.Contains(result, "## "+filename) {
			t.Errorf("Expected result to contain header for %s", filename)
		}
		if !strings.Contains(result, content) {
			t.Errorf("Expected result to contain content from %s", filename)
		}
	}
}

// TestLoadBootstrapFiles_IgnoresDeprecatedAgentsFile tests LoadBootstrapFiles ignores deprecated AGENTS.md.
func TestLoadBootstrapFiles_IgnoresDeprecatedAgentsFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	legacyContent := "This is the legacy AGENTS content"
	path := filepath.Join(tmpDir, "AGENTS.md")
	if err := os.WriteFile(path, []byte(legacyContent), 0644); err != nil {
		t.Fatalf("Failed to create AGENTS.md: %v", err)
	}

	cb := NewContextBuilder(tmpDir)
	result := cb.LoadBootstrapFiles()

	if strings.Contains(result, "## AGENTS.md") {
		t.Error("Expected deprecated AGENTS.md to be ignored")
	}
	if strings.Contains(result, legacyContent) {
		t.Error("Expected deprecated AGENTS.md content to be ignored")
	}
}

// TestLoadBootstrapFiles_PartialFiles tests LoadBootstrapFiles with only some files present
func TestLoadBootstrapFiles_PartialFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create only one bootstrap file
	content := "Only SOUL content"
	path := filepath.Join(tmpDir, "SOUL.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create SOUL.md: %v", err)
	}

	cb := NewContextBuilder(tmpDir)
	result := cb.LoadBootstrapFiles()

	// Should contain SOUL.md content
	if !strings.Contains(result, "## SOUL.md") {
		t.Error("Expected result to contain SOUL.md header")
	}
	if !strings.Contains(result, content) {
		t.Error("Expected result to contain SOUL.md content")
	}

	// Should not contain other files
	if strings.Contains(result, "AGENT.md") {
		t.Error("Expected result to not contain AGENT.md")
	}
}

// TestGetInitialContext_NoFiles tests GetInitialContext with no files
func TestGetInitialContext_NoFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	context := cb.GetInitialContext()

	// Should contain identity
	if !strings.Contains(context, "# lele") {
		t.Error("Expected context to contain lele header")
	}

	// When there's only identity (no bootstrap files, skills, or memory),
	// there should be no separator since there's only one part
	// The separator only appears when joining multiple parts
}

// TestGetInitialContext_WithBootstrapFiles tests GetInitialContext with bootstrap files
func TestGetInitialContext_WithBootstrapFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create bootstrap file
	path := filepath.Join(tmpDir, "SOUL.md")
	if err := os.WriteFile(path, []byte("SOUL content here"), 0644); err != nil {
		t.Fatalf("Failed to create SOUL.md: %v", err)
	}

	cb := NewContextBuilder(tmpDir)
	context := cb.GetInitialContext()

	// Should contain identity
	if !strings.Contains(context, "# lele") {
		t.Error("Expected context to contain lele header")
	}

	// Should contain bootstrap content
	if !strings.Contains(context, "SOUL content here") {
		t.Error("Expected context to contain SOUL.md content")
	}
}

// TestBuildMessages_Basic tests BuildMessages with basic parameters
func TestBuildMessages_Basic(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	history := []providers.Message{
		{Role: "user", Content: "Previous message"},
		{Role: "assistant", Content: "Previous response"},
	}

	messages := cb.BuildMessages(history, "", "Current message", nil, "", "", "")

	// Should have system + history + current message
	if len(messages) != 4 {
		t.Errorf("Expected 4 messages, got %d", len(messages))
	}

	// First message should be system
	if messages[0].Role != "system" {
		t.Errorf("Expected first message to be system, got %s", messages[0].Role)
	}

	// Last message should be user with current content
	lastMsg := messages[len(messages)-1]
	if lastMsg.Role != "user" {
		t.Errorf("Expected last message to be user, got %s", lastMsg.Role)
	}
	if lastMsg.Content != "Current message" {
		t.Errorf("Expected content 'Current message', got %s", lastMsg.Content)
	}
}

// TestBuildMessages_WithSummary tests BuildMessages with summary
func TestBuildMessages_WithSummary(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	summary := "This is a conversation summary"
	messages := cb.BuildMessages([]providers.Message{}, summary, "Hello", nil, "", "", "")

	if len(messages) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(messages))
	}

	// System message should remain static
	systemMsg := messages[0]
	if strings.Contains(systemMsg.Content, "Summary of Previous Conversation") {
		t.Error("Expected system message to remain free of summary content")
	}

	summaryMsg := messages[1]
	if summaryMsg.Role != "user" {
		t.Fatalf("Expected summary message role user, got %s", summaryMsg.Role)
	}
	if !strings.Contains(summaryMsg.Content, "Summary of Previous Conversation") {
		t.Error("Expected summary message to contain summary header")
	}
	if !strings.Contains(summaryMsg.Content, summary) {
		t.Error("Expected summary message to contain summary content")
	}
}

// TestBuildMessages_WithSessionInfo tests BuildMessages with channel and chatID
func TestBuildMessages_WithSessionInfo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	messages := cb.BuildMessages([]providers.Message{}, "", "Hello", nil, "test-channel", "chat-123", "")

	if len(messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(messages))
	}

	// System message should stay static
	systemMsg := messages[0]
	if strings.Contains(systemMsg.Content, "## Current Session") {
		t.Error("Expected system message to remain free of session info")
	}

	userMsg := messages[1]
	if !strings.Contains(userMsg.Content, "## Current Session") {
		t.Error("Expected user message to contain session header")
	}
	if !strings.Contains(userMsg.Content, "Channel: test-channel") {
		t.Error("Expected user message to contain channel info")
	}
	if !strings.Contains(userMsg.Content, "Chat ID: chat-123") {
		t.Error("Expected user message to contain chat ID info")
	}
}

// TestBuildMessages_WithOrphanedToolMessages tests BuildMessages removes orphaned tool messages
func TestBuildMessages_WithOrphanedToolMessages(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// History with orphaned tool message at the beginning
	history := []providers.Message{
		{Role: "tool", Content: "Orphaned tool result", ToolCallID: "call-1"},
		{Role: "user", Content: "User message"},
		{Role: "assistant", Content: "Assistant response"},
	}

	messages := cb.BuildMessages(history, "", "Current", nil, "", "", "")

	// Should have system + 2 history (orphaned tool removed) + current = 4
	if len(messages) != 4 {
		t.Errorf("Expected 4 messages after removing orphaned tool, got %d", len(messages))
	}

	// First non-system message should be user (tool was removed)
	if messages[1].Role != "user" {
		t.Errorf("Expected first history message to be user, got %s", messages[1].Role)
	}
}

// TestBuildMessages_EmptyHistory tests BuildMessages with empty history
func TestBuildMessages_EmptyHistory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	messages := cb.BuildMessages([]providers.Message{}, "", "Hello", nil, "", "", "")

	// Should have system + current message = 2
	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}
}

// TestAddToolResult tests AddToolResult method
func TestAddToolResult(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	messages := []providers.Message{
		{Role: "user", Content: "Hello"},
	}

	result := cb.AddToolResult(messages, "call-123", "test_tool", "Tool result content")

	// Should have original message + tool result = 2
	if len(result) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(result))
	}

	// Last message should be tool
	toolMsg := result[len(result)-1]
	if toolMsg.Role != "tool" {
		t.Errorf("Expected tool message, got %s", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call-123" {
		t.Errorf("Expected ToolCallID 'call-123', got %s", toolMsg.ToolCallID)
	}
	if toolMsg.Content != "Tool result content" {
		t.Errorf("Expected content 'Tool result content', got %s", toolMsg.Content)
	}
}

// TestAddToolResult_EmptyMessages tests AddToolResult with empty messages slice
func TestAddToolResult_EmptyMessages(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	result := cb.AddToolResult([]providers.Message{}, "call-1", "tool", "result")

	if len(result) != 1 {
		t.Errorf("Expected 1 message, got %d", len(result))
	}

	if result[0].Role != "tool" {
		t.Errorf("Expected tool role, got %s", result[0].Role)
	}
}

// TestAddAssistantMessage tests AddAssistantMessage method
func TestAddAssistantMessage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	messages := []providers.Message{
		{Role: "user", Content: "Hello"},
	}

	toolCalls := []map[string]interface{}{
		{"id": "call-1", "name": "test_tool"},
	}

	result := cb.AddAssistantMessage(messages, "Assistant response", toolCalls)

	// Should have original message + assistant message = 2
	if len(result) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(result))
	}

	// Last message should be assistant
	assistantMsg := result[len(result)-1]
	if assistantMsg.Role != "assistant" {
		t.Errorf("Expected assistant message, got %s", assistantMsg.Role)
	}
	if assistantMsg.Content != "Assistant response" {
		t.Errorf("Expected content 'Assistant response', got %s", assistantMsg.Content)
	}
}

// TestAddAssistantMessage_NoToolCalls tests AddAssistantMessage without tool calls
func TestAddAssistantMessage_NoToolCalls(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	messages := []providers.Message{
		{Role: "user", Content: "Hello"},
	}

	result := cb.AddAssistantMessage(messages, "Just a response", nil)

	if len(result) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(result))
	}

	if result[1].Role != "assistant" {
		t.Errorf("Expected assistant role, got %s", result[1].Role)
	}
}

// TestAddAssistantMessage_EmptyMessages tests AddAssistantMessage with empty messages slice
func TestAddAssistantMessage_EmptyMessages(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	result := cb.AddAssistantMessage([]providers.Message{}, "Response", nil)

	if len(result) != 1 {
		t.Errorf("Expected 1 message, got %d", len(result))
	}

	if result[0].Role != "assistant" {
		t.Errorf("Expected assistant role, got %s", result[0].Role)
	}
}

// TestLoadSkills_NoSkills tests loadSkills when no local workspace skills exist.
// Global skills from ~/.lele/skills/ may still be present, so we only verify
// that no workspace-local skills content appears.
func TestLoadSkills_NoSkills(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	result := cb.loadSkills()

	// If there are no skills at all (local or global), result should be empty
	// If global skills exist, that's expected — just verify no workspace-local skill markers appear
	if result != "" {
		// Global skills may be present; verify no references to workspace-local skill paths
		if strings.Contains(result, filepath.Join(tmpDir, "skills")) {
			t.Errorf("Expected no workspace-local skills, but found workspace path in result: %s", result)
		}
	}
}

// TestGetSkillsInfo_NoSkills tests GetSkillsInfo when no local workspace skills exist.
// Global skills from ~/.lele/skills/ may still be present, so we verify the workspace
// has no local skills by checking that no workspace-local skill names appear.
func TestGetSkillsInfo_NoSkills(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	info := cb.GetSkillsInfo()

	names, ok := info["names"].([]string)
	if !ok {
		t.Fatalf("Expected names to be []string, got %T", info["names"])
	}

	// No workspace-local skills should exist — any skills present are global only
	// and should NOT reference the workspace temp dir
	for _, name := range names {
		skillPath := filepath.Join(tmpDir, "skills", name, "SKILL.md")
		if _, err := os.Stat(skillPath); err == nil {
			t.Errorf("Found unexpected workspace-local skill: %s", name)
		}
	}

	// Total and available should be consistent
	if info["total"] != info["available"] {
		t.Errorf("Expected total == available, got total=%v available=%v", info["total"], info["available"])
	}
}

// TestGetSkillsInfo_WithSkills tests GetSkillsInfo with skills present
func TestGetSkillsInfo_WithSkills(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create skills directory and a skill
	skillsDir := filepath.Join(tmpDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("Failed to create skills dir: %v", err)
	}

	skillDir := filepath.Join(skillsDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	skillFile := filepath.Join(skillDir, "SKILL.md")
	content := `---
name: test-skill
description: A test skill for testing
---

# Test Skill

This is a test skill.
`
	if err := os.WriteFile(skillFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create SKILL.md: %v", err)
	}

	cb := NewContextBuilder(tmpDir)
	info := cb.GetSkillsInfo()

	// Should have at least 1 skill (test-skill from workspace, plus any global skills)
	total, ok := info["total"].(int)
	if !ok {
		t.Fatalf("Expected total to be int, got %T", info["total"])
	}
	if total < 1 {
		t.Errorf("Expected total >= 1, got %d", total)
	}
	available, ok := info["available"].(int)
	if !ok {
		t.Fatalf("Expected available to be int, got %T", info["available"])
	}
	if available < 1 {
		t.Errorf("Expected available >= 1, got %d", available)
	}

	// Should contain the workspace-local skill name
	names, ok := info["names"].([]string)
	if !ok {
		t.Fatalf("Expected names to be []string, got %T", info["names"])
	}
	found := false
	for _, n := range names {
		if n == "test-skill" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected names to contain 'test-skill', got %v", names)
	}
}

// TestContextBuilder_NilSafety tests that methods handle nil ContextBuilder gracefully
func TestContextBuilder_NilSafety(t *testing.T) {
	// Note: In Go, calling methods on nil struct pointers can work if the methods
	// don't dereference the pointer. However, most methods here will panic on nil.
	// This test documents the expected behavior.

	var cb *ContextBuilder

	// These should panic when called on nil
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Expected panic when calling method on nil ContextBuilder: %v", r)
		}
	}()

	_ = cb.GetInitialContext()
	t.Error("Expected panic when calling GetInitialContext on nil ContextBuilder")
}

// mockTool is a mock implementation of tools.Tool for testing
type mockTool struct {
	name        string
	description string
}

func (m *mockTool) Name() string {
	return m.name
}

func (m *mockTool) Description() string {
	return m.description
}

func (m *mockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (m *mockTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	return &tools.ToolResult{
		ForLLM:  "Mock result",
		ForUser: "Mock result for user",
	}
}

// TestBuildMessages_MultipleOrphanedTools tests removal of multiple consecutive orphaned tool messages
func TestBuildMessages_MultipleOrphanedTools(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// History with multiple orphaned tool messages at the beginning
	history := []providers.Message{
		{Role: "tool", Content: "Orphaned 1", ToolCallID: "call-1"},
		{Role: "tool", Content: "Orphaned 2", ToolCallID: "call-2"},
		{Role: "user", Content: "User message"},
	}

	messages := cb.BuildMessages(history, "", "Current", nil, "", "", "")

	// Should have system + 1 history (both tools removed) + current = 3
	if len(messages) != 3 {
		t.Errorf("Expected 3 messages after removing orphaned tools, got %d", len(messages))
	}

	// First non-system message should be user
	if messages[1].Role != "user" {
		t.Errorf("Expected first history message to be user, got %s", messages[1].Role)
	}
}

// TestBuildMessages_ToolNotAtStart tests that tool messages not at start are preserved
func TestBuildMessages_ToolNotAtStart(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// History with tool message after user message (should be preserved)
	history := []providers.Message{
		{Role: "user", Content: "User message"},
		{Role: "assistant", Content: "Assistant response", ToolCalls: []providers.ToolCall{{ID: "call-1"}}},
		{Role: "tool", Content: "Tool result", ToolCallID: "call-1"},
	}

	messages := cb.BuildMessages(history, "", "Current", nil, "", "", "")

	// Should have system + 3 history + current = 5
	if len(messages) != 5 {
		t.Errorf("Expected 5 messages, got %d", len(messages))
	}

	// Tool message should be preserved (not at start)
	toolFound := false
	for _, msg := range messages {
		if msg.Role == "tool" {
			toolFound = true
			break
		}
	}
	if !toolFound {
		t.Error("Expected tool message to be preserved when not at start of history")
	}
}

// TestGetInitialContext_WithMemory tests GetInitialContext includes memory context
func TestGetInitialContext_WithMemory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create MEMORY.md at workspace root (not in memory/ directory)
	memoryFile := filepath.Join(tmpDir, "MEMORY.md")
	if err := os.WriteFile(memoryFile, []byte("Long-term memory content"), 0644); err != nil {
		t.Fatalf("Failed to create MEMORY.md: %v", err)
	}

	cb := NewContextBuilder(tmpDir)
	context := cb.GetInitialContext()

	// Should contain memory content in bootstrap files section
	if !strings.Contains(context, "Long-term memory content") {
		t.Error("Expected context to contain memory content")
	}
}

// TestGetInitialContext_WithSkills tests GetInitialContext includes skills summary
func TestGetInitialContext_WithSkills(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create skills directory and a skill
	skillsDir := filepath.Join(tmpDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("Failed to create skills dir: %v", err)
	}

	skillDir := filepath.Join(skillsDir, "my-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	skillFile := filepath.Join(skillDir, "SKILL.md")
	content := `---
name: my-skill
description: My test skill description
---

# My Skill

This is my skill.
`
	if err := os.WriteFile(skillFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create SKILL.md: %v", err)
	}

	cb := NewContextBuilder(tmpDir)
	context := cb.GetInitialContext()

	// Should contain skills section
	if !strings.Contains(context, "# Skills") {
		t.Error("Expected context to contain Skills section")
	}

	if !strings.Contains(context, "my-skill") {
		t.Error("Expected context to contain skill name")
	}
}

// TestLoadBootstrapFiles_InvalidPath tests LoadBootstrapFiles with invalid workspace path
func TestLoadBootstrapFiles_InvalidPath(t *testing.T) {
	cb := NewContextBuilder("/nonexistent/path/that/does/not/exist")
	result := cb.LoadBootstrapFiles()

	// Should return empty string for non-existent path
	if result != "" {
		t.Errorf("Expected empty result for invalid path, got: %s", result)
	}
}

// TestBuildMessages_NilMedia tests BuildMessages with nil attachment parameter
func TestBuildMessages_NilMedia(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Should not panic with nil attachments
	messages := cb.BuildMessages([]providers.Message{}, "", "Hello", nil, "", "", "")

	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}
}

// TestBuildMessages_EmptyMedia tests BuildMessages with empty attachment slice
func TestBuildMessages_EmptyMedia(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Should work with empty attachment slice
	messages := cb.BuildMessages([]providers.Message{}, "", "Hello", []bus.FileAttachment{}, "", "", "")

	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}
}

func TestRenderUserMessage_AttachmentsShowOnlyStoredPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	attachmentPath := filepath.Join(tmpDir, "attachments", "20260312", "abc_report.txt")
	rendered := cb.RenderUserMessage("Procesa este archivo", []bus.FileAttachment{{Path: attachmentPath}})

	if !strings.Contains(rendered, attachmentPath) {
		t.Fatalf("expected rendered message to contain attachment path %q, got %q", attachmentPath, rendered)
	}
	if strings.Contains(rendered, "secret-content") {
		t.Fatalf("rendered message should not contain attachment contents")
	}
}

// TestContextBuilder_MultipleCalls tests that multiple calls to methods work correctly
func TestContextBuilder_MultipleCalls(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Call GetInitialContext multiple times
	ctx1 := cb.GetInitialContext()
	ctx2 := cb.GetInitialContext()

	// Results should be consistent
	if ctx1 != ctx2 {
		t.Error("Expected GetInitialContext to return consistent results")
	}

	// Call BuildSystemPrompt multiple times
	prompt1 := cb.BuildSystemPrompt()
	prompt2 := cb.BuildSystemPrompt()

	if prompt1 != prompt2 {
		t.Error("Expected BuildSystemPrompt to return consistent results")
	}
}

// TestAddToolResult_EmptyStrings tests AddToolResult with empty strings
func TestAddToolResult_EmptyStrings(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	result := cb.AddToolResult([]providers.Message{}, "", "", "")

	if len(result) != 1 {
		t.Errorf("Expected 1 message, got %d", len(result))
	}

	if result[0].ToolCallID != "" {
		t.Errorf("Expected empty ToolCallID, got %s", result[0].ToolCallID)
	}

	if result[0].Content != "" {
		t.Errorf("Expected empty content, got %s", result[0].Content)
	}
}

// TestAddAssistantMessage_EmptyContent tests AddAssistantMessage with empty content
func TestAddAssistantMessage_EmptyContent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	result := cb.AddAssistantMessage([]providers.Message{}, "", nil)

	if len(result) != 1 {
		t.Errorf("Expected 1 message, got %d", len(result))
	}

	if result[0].Content != "" {
		t.Errorf("Expected empty content, got %s", result[0].Content)
	}
}

// TestGetSkillsInfo_MultipleSkills tests GetSkillsInfo with multiple skills
func TestGetSkillsInfo_MultipleSkills(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create skills directory and multiple skills
	skillsDir := filepath.Join(tmpDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("Failed to create skills dir: %v", err)
	}

	skillNames := []string{"skill-a", "skill-b", "skill-c"}
	for _, name := range skillNames {
		skillDir := filepath.Join(skillsDir, name)
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			t.Fatalf("Failed to create skill dir %s: %v", name, err)
		}

		skillFile := filepath.Join(skillDir, "SKILL.md")
		content := fmt.Sprintf(`---
name: %s
description: Description for %s
---

# %s
`, name, name, name)
		if err := os.WriteFile(skillFile, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create SKILL.md for %s: %v", name, err)
		}
	}

	cb := NewContextBuilder(tmpDir)
	info := cb.GetSkillsInfo()

	// Should have at least 3 local skills (plus any global skills)
	total, ok := info["total"].(int)
	if !ok {
		t.Fatalf("Expected total to be int, got %T", info["total"])
	}
	if total < 3 {
		t.Errorf("Expected total >= 3, got %d", total)
	}

	names, ok := info["names"].([]string)
	if !ok {
		t.Fatalf("Expected names to be []string, got %T", info["names"])
	}

	// Verify all 3 local skills are present
	for _, expected := range skillNames {
		found := false
		for _, n := range names {
			if n == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected names to contain '%s', got %v", expected, names)
		}
	}
}
