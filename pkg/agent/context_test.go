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

// TestBuildToolsSection_ReadImageHiddenWithoutVision tests that read_image is
// hidden from the tools section when vision is not supported, and shown when it is.
func TestBuildToolsSection_ReadImageHiddenWithoutVision(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Register a read_image mock tool plus a normal tool.
	registry := tools.NewToolRegistry()
	registry.Register(&mockTool{
		name:        "read_image",
		description: "Read an image file",
	})
	registry.Register(&mockTool{
		name:        "read_file",
		description: "Read a text file",
	})
	cb.SetToolsRegistry(registry)

	// Default: vision not supported -> read_image hidden, read_file shown.
	section := cb.buildToolsSection()
	if strings.Contains(section, "`read_image`") {
		t.Error("Expected read_image to be hidden when vision is not supported")
	}
	if !strings.Contains(section, "`read_file`") {
		t.Error("Expected read_file to be present when vision is not supported")
	}

	// Enable vision -> read_image shown.
	cb.SetVisionSupported(true)
	section = cb.buildToolsSection()
	if !strings.Contains(section, "`read_image`") {
		t.Error("Expected read_image to be present when vision is supported")
	}
	if !strings.Contains(section, "`read_file`") {
		t.Error("Expected read_file to be present when vision is supported")
	}
}

// TestSetVisionSupported_InvalidatesCaches tests that SetVisionSupported
// invalidates cached prompts so the tools section is rebuilt.
func TestSetVisionSupported_InvalidatesCaches(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	registry := tools.NewToolRegistry()
	registry.Register(&mockTool{
		name:        "read_image",
		description: "Read an image file",
	})
	cb.SetToolsRegistry(registry)

	// Build the initial context (caches it) with vision disabled.
	cb.SetVisionSupported(false)
	initial := cb.GetInitialContext()
	if strings.Contains(initial, "`read_image`") {
		t.Error("Expected read_image hidden in initial context")
	}

	// Enable vision; the cached initial context must be invalidated so the
	// next build reflects the change.
	cb.SetVisionSupported(true)
	updated := cb.GetInitialContext()
	if !strings.Contains(updated, "`read_image`") {
		t.Error("Expected read_image present after enabling vision")
	}

	// Setting the same value again must be a no-op (no unnecessary rebuild).
	cb.SetVisionSupported(true)
	again := cb.GetInitialContext()
	if !strings.Contains(again, "`read_image`") {
		t.Error("Expected read_image still present after redundant SetVisionSupported(true)")
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

// TestBuildSystemPromptForSession tests BuildSystemPromptForSession method
func TestBuildSystemPromptForSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Save original CWD
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get original cwd: %v", err)
	}

	// Create temp directory for current directory and change to it
	runDir, err := os.MkdirTemp("", "run-dir-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp run dir: %v", err)
	}
	defer os.RemoveAll(runDir)

	if err := os.Chdir(runDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalCwd)
	}()

	// Write AGENTS.md in runDir
	agentsContent := "harness-specific-agent-instructions"
	if err := os.WriteFile(filepath.Join(runDir, "AGENTS.md"), []byte(agentsContent), 0644); err != nil {
		t.Fatalf("Failed to write AGENTS.md: %v", err)
	}

	// 1. Check for non-native channel (should not include harness)
	promptNonNative := cb.BuildSystemPromptForSession("some-session", "web")
	if strings.Contains(promptNonNative, "Harness Module") || strings.Contains(promptNonNative, agentsContent) {
		t.Error("Expected system prompt for non-native channel to exclude harness context")
	}

	// 2. Check for native channel (should include harness)
	promptNativeChannel := cb.BuildSystemPromptForSession("some-session", "native")
	if !strings.Contains(promptNativeChannel, "Harness Module") || !strings.Contains(promptNativeChannel, agentsContent) {
		t.Error("Expected system prompt for native channel to include harness context")
	}

	// 3. Check for native/tui session key (should include harness even with empty channel)
	promptNativeSessionKey := cb.BuildSystemPromptForSession("tui:chat:123", "")
	if !strings.Contains(promptNativeSessionKey, "Harness Module") || !strings.Contains(promptNativeSessionKey, agentsContent) {
		t.Error("Expected system prompt for tui session key to include harness context")
	}

	// 4. Negative: sessionKey contains "native" but NOT as prefix — should NOT include harness
	promptSubstringNative := cb.BuildSystemPromptForSession("web:my-native-thing", "web")
	if strings.Contains(promptSubstringNative, "Harness Module") || strings.Contains(promptSubstringNative, agentsContent) {
		t.Error("Expected system prompt for sessionKey with 'native' as substring (not prefix) to exclude harness context")
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

	messages := cb.BuildMessages(history, "", "Current message", nil, "", "", "", "")

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
	if !strings.Contains(lastMsg.Content, "Current message") {
		t.Errorf("Expected content to contain 'Current message', got %s", lastMsg.Content)
	}
	if strings.Contains(messages[0].Content, "Current Time:") {
		t.Error("Expected system prompt to NOT contain 'Current Time:' (removed so the prompt stays byte-stable across turns)")
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
	messages := cb.BuildMessages([]providers.Message{}, summary, "Hello", nil, "", "", "", "")

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

func TestBuildMessages_DoesNotDuplicatePersistedSummary(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	summary := "This is a conversation summary"
	history := []providers.Message{buildSummaryMessage(summary)}

	messages := cb.BuildMessages(history, summary, "Hello", nil, "", "", "", "")

	count := 0
	for _, msg := range messages {
		if msg.Role == "user" && msg.Content == summaryMessageHeader+summary {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one summary message, got %d", count)
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

	messages := cb.BuildMessages([]providers.Message{}, "", "Hello", nil, "test-channel", "chat-123", "", "")

	if len(messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(messages))
	}

	// System message should stay static
	systemMsg := messages[0]
	if !strings.Contains(systemMsg.Content, "## Current Session") {
		t.Error("Expected system message to contain session header")
	}
	if !strings.Contains(systemMsg.Content, "Channel: test-channel") {
		t.Error("Expected system message to contain channel info")
	}
	if !strings.Contains(systemMsg.Content, "Chat ID: chat-123") {
		t.Error("Expected system message to contain chat ID info")
	}

	userMsg := messages[1]
	if strings.Contains(userMsg.Content, "## Current Session") {
		t.Error("Expected user message to remain free of session info")
	}
}

func TestBuildMessages_WithNativeSessionInfoUsesStableChatID(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	messages := cb.BuildMessages([]providers.Message{}, "", "Hello", nil, "native", "native:client-123:uuid-abc-123", "", "")

	if len(messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(messages))
	}

	systemMsg := messages[0]
	if !strings.Contains(systemMsg.Content, "Chat ID: native:client-123:uuid-abc-123") {
		t.Error("Expected native session prompt to use the full UUID-based chat ID")
	}

	messages = cb.BuildMessages([]providers.Message{}, "", "Hello", nil, "native", "native:client-123:chat:7", "", "")
	systemMsg = messages[0]
	if !strings.Contains(systemMsg.Content, "Chat ID: native:client-123") {
		t.Error("Expected native chat alias to collapse to stable base chat ID")
	}
	if strings.Contains(systemMsg.Content, "Chat ID: native:client-123:chat:7") {
		t.Error("Expected native chat alias suffix to be omitted from chat ID")
	}
}

func TestHarnessContext_GloballyCachedAndInvalidated(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get original cwd: %v", err)
	}

	runDir, err := os.MkdirTemp("", "run-dir-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp run dir: %v", err)
	}
	defer os.RemoveAll(runDir)

	if err := os.Chdir(runDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() { _ = os.Chdir(originalCwd) }()

	if err := os.WriteFile(filepath.Join(runDir, "AGENTS.md"), []byte("harness-v1"), 0644); err != nil {
		t.Fatalf("Failed to write AGENTS.md: %v", err)
	}

	cb := NewContextBuilder(tmpDir)

	// First build caches the harness context (contains v1).
	first := cb.BuildSystemPromptForSession("tui:chat:1", "")
	if !strings.Contains(first, "harness-v1") {
		t.Fatalf("expected first build to contain harness-v1, got: %s", first)
	}

	// Rewrite AGENTS.md to v2 while the process is running.
	if err := os.WriteFile(filepath.Join(runDir, "AGENTS.md"), []byte("harness-v2"), 0644); err != nil {
		t.Fatalf("Failed to rewrite AGENTS.md: %v", err)
	}

	// A second build (different session) must reuse the cached harness (still
	// v1), proving the harness is cached globally, not re-read per session.
	second := cb.BuildSystemPromptForSession("tui:chat:2", "")
	if !strings.Contains(second, "harness-v1") {
		t.Fatalf("expected second build to reuse cached harness-v1, got: %s", second)
	}
	if strings.Contains(second, "harness-v2") {
		t.Fatalf("expected cached harness to NOT contain harness-v2, got: %s", second)
	}

	// ResetMemoryContext (called on /new) invalidates the harness cache.
	cb.ResetMemoryContext()

	third := cb.BuildSystemPromptForSession("tui:chat:3", "")
	if !strings.Contains(third, "harness-v2") {
		t.Fatalf("expected post-reset build to contain harness-v2, got: %s", third)
	}
}

func TestBuildMessages_ChangingSessionKeyRebuildsSystemPrompt(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	first := cb.BuildMessages([]providers.Message{}, "", "same message", nil, "native", "native:test-client:111", "native:test-client:111", "")
	second := cb.BuildMessages([]providers.Message{}, "", "same message", nil, "test-channel", "chat-222", "test-channel:chat-222", "")

	if first[0].Content == second[0].Content {
		t.Fatalf("expected different session keys to rebuild system prompt when request context changes")
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

	messages := cb.BuildMessages(history, "", "Current", nil, "", "", "", "")

	// BuildMessages no longer repairs pairing: it hands the history over as it
	// is, and the single repair point is the provider call (llmCaller.call).
	// Healing here too would mean two rules for the same defect, and the ones
	// that disagree would silently drop real tool output.
	// system + 3 history + current = 5
	if len(messages) != 5 {
		t.Errorf("Expected 5 messages (history passed through), got %d", len(messages))
	}
	if messages[1].Role != "tool" || messages[1].ToolCallID != "call-1" {
		t.Errorf("Expected the orphaned tool message to be passed through, got %+v", messages[1])
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

	messages := cb.BuildMessages([]providers.Message{}, "", "Hello", nil, "", "", "", "")

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

	messages := cb.BuildMessages(history, "", "Current", nil, "", "", "", "")

	// Both orphans pass through unhealed - see the note in
	// TestBuildMessages_WithOrphanedToolMessages: pairing is repaired once, at
	// the provider call.
	// system + 3 history + current = 5
	if len(messages) != 5 {
		t.Errorf("Expected 5 messages (history passed through), got %d", len(messages))
	}
	if messages[1].Role != "tool" || messages[2].Role != "tool" {
		t.Errorf("Expected both orphaned tool messages to be passed through, got %s/%s",
			messages[1].Role, messages[2].Role)
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
		{Role: "assistant", Content: "Assistant response", ToolCalls: []providers.ToolCall{{ID: "call-1", Name: "exec"}}},
		{Role: "tool", Content: "Tool result", ToolCallID: "call-1"},
	}

	messages := cb.BuildMessages(history, "", "Current", nil, "", "", "", "")

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
	messages := cb.BuildMessages([]providers.Message{}, "", "Hello", nil, "", "", "", "")

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
	messages := cb.BuildMessages([]providers.Message{}, "", "Hello", []bus.FileAttachment{}, "", "", "", "")

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

// --- Mode-aware BuildMessages tests (3-mode feature) ---

// TestBuildMinimalSystemPrompt verifies the minimal system prompt
// contains web_search/web_fetch and the "helpful AI assistant" phrase,
// but does NOT contain full-prompt markers like AGENT.md or SOUL.md.
func TestBuildMinimalSystemPrompt(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "minimal-prompt-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	prompt := cb.BuildMinimalSystemPrompt()

	// Must contain these
	required := []string{"web_search", "web_fetch", "helpful AI assistant"}
	for _, r := range required {
		if !strings.Contains(prompt, r) {
			t.Errorf("BuildMinimalSystemPrompt() missing %q", r)
		}
	}

	// Must NOT contain full-prompt markers
	excluded := []string{"AGENT.md", "SOUL.md", "## Skills", "IDENTITY.md", "MEMORY.md"}
	for _, e := range excluded {
		if strings.Contains(prompt, e) {
			t.Errorf("BuildMinimalSystemPrompt() should not contain %q, but does", e)
		}
	}
}

// TestBuildMessagesChatMode verifies that BuildMessages in chat mode uses
// the minimal system prompt (not the full prompt with AGENT.md, SOUL.md, etc.).
func TestBuildMessagesChatMode(t *testing.T) {
	// Create a workspace with AGENT.md and SOUL.md so the full prompt
	// would normally include them.
	tmpDir, err := os.MkdirTemp("", "chat-mode-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	agentContent := "# AGENT.md\nThis is the agent instructions for testing."
	soulContent := "# SOUL.md\nThis is the soul content for testing."
	os.WriteFile(filepath.Join(tmpDir, "AGENT.md"), []byte(agentContent), 0644)
	os.WriteFile(filepath.Join(tmpDir, "SOUL.md"), []byte(soulContent), 0644)

	cb := NewContextBuilder(tmpDir)

	// Build messages in chat mode with a unique session key
	messages := cb.BuildMessages(
		[]providers.Message{}, "", "Hello", nil,
		"", "", "chat-session-test", "chat",
	)

	if len(messages) < 2 {
		t.Fatalf("Expected at least 2 messages, got %d", len(messages))
	}

	systemMsg := messages[0]
	if systemMsg.Role != "system" {
		t.Fatalf("Expected first message to be system, got %q", systemMsg.Role)
	}

	// Chat mode should use the minimal prompt
	if !strings.Contains(systemMsg.Content, "web_search") {
		t.Error("Chat mode system prompt should contain 'web_search'")
	}
	if !strings.Contains(systemMsg.Content, "web_fetch") {
		t.Error("Chat mode system prompt should contain 'web_fetch'")
	}

	// Should NOT contain full-prompt markers
	if strings.Contains(systemMsg.Content, "AGENT.md") {
		t.Error("Chat mode system prompt should NOT contain 'AGENT.md'")
	}
	if strings.Contains(systemMsg.Content, "SOUL.md") {
		t.Error("Chat mode system prompt should NOT contain 'SOUL.md'")
	}
	if strings.Contains(systemMsg.Content, agentContent) {
		t.Error("Chat mode system prompt should NOT contain AGENT.md content")
	}
	if strings.Contains(systemMsg.Content, soulContent) {
		t.Error("Chat mode system prompt should NOT contain SOUL.md content")
	}

	// Compare with agent mode to confirm they're different
	agentMessages := cb.BuildMessages(
		[]providers.Message{}, "", "Hello", nil,
		"", "", "agent-session-test", "agent",
	)

	if agentMessages[0].Content == systemMsg.Content {
		t.Error("Chat mode and agent mode should produce different system prompts")
	}

	// Agent mode SHOULD contain the full prompt markers
	if !strings.Contains(agentMessages[0].Content, agentContent) {
		t.Error("Agent mode system prompt should contain AGENT.md content")
	}
}

// TestBuildMessagesAgentMode verifies that BuildMessages in agent mode (or
// empty mode) produces the full system prompt.
func TestBuildMessagesAgentMode(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-mode-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	agentContent := "# AGENT.md\nTest agent content for mode verification."
	soulContent := "# SOUL.md\nTest soul content for mode verification."
	os.WriteFile(filepath.Join(tmpDir, "AGENT.md"), []byte(agentContent), 0644)
	os.WriteFile(filepath.Join(tmpDir, "SOUL.md"), []byte(soulContent), 0644)

	cb := NewContextBuilder(tmpDir)

	// Test with explicit "agent" mode
	messagesAgent := cb.BuildMessages(
		[]providers.Message{}, "", "Hello", nil,
		"", "", "agent-mode-session", "agent",
	)

	if len(messagesAgent) < 2 {
		t.Fatalf("Expected at least 2 messages, got %d", len(messagesAgent))
	}

	agentSystemPrompt := messagesAgent[0].Content
	if !strings.Contains(agentSystemPrompt, agentContent) {
		t.Error("Agent mode system prompt should contain AGENT.md content")
	}
	if !strings.Contains(agentSystemPrompt, soulContent) {
		t.Error("Agent mode system prompt should contain SOUL.md content")
	}

	// Test with empty mode (should default to agent/full prompt)
	messagesDefault := cb.BuildMessages(
		[]providers.Message{}, "", "Hello", nil,
		"", "", "default-mode-session", "",
	)

	defaultSystemPrompt := messagesDefault[0].Content
	if !strings.Contains(defaultSystemPrompt, agentContent) {
		t.Error("Empty mode (default) system prompt should contain AGENT.md content")
	}
	if !strings.Contains(defaultSystemPrompt, soulContent) {
		t.Error("Empty mode (default) system prompt should contain SOUL.md content")
	}

	// Both agent and empty mode should produce identical prompts
	// (since they use the same buildSystemPromptForTurn path)
	if agentSystemPrompt != defaultSystemPrompt {
		t.Error("Agent mode and empty mode should produce identical system prompts")
	}

	// Should NOT be the minimal prompt
	if strings.Contains(agentSystemPrompt, "You can search the web and fetch web pages") {
		t.Error("Agent mode should NOT use the minimal chat prompt")
	}
}

// TestBuildMessagesChatModeCacheIsolation verifies that chat-mode and
// agent-mode sessions keep their prompts isolated across turns (chat stays
// minimal, agent stays full).
func TestBuildMessagesChatModeCacheIsolation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cache-isolation-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	agentContent := "# AGENT.md\nCache isolation test content."
	os.WriteFile(filepath.Join(tmpDir, "AGENT.md"), []byte(agentContent), 0644)

	cb := NewContextBuilder(tmpDir)

	// Build chat mode messages — caches for "chat-isolation-session"
	chatMsgs := cb.BuildMessages(
		[]providers.Message{}, "", "Hello", nil,
		"", "", "chat-isolation-session", "chat",
	)

	// Build agent mode messages — caches for "agent-isolation-session"
	agentMsgs := cb.BuildMessages(
		[]providers.Message{}, "", "Hello", nil,
		"", "", "agent-isolation-session", "agent",
	)

	chatPrompt := chatMsgs[0].Content
	agentPrompt := agentMsgs[0].Content

	// They should be different (minimal vs full)
	if chatPrompt == agentPrompt {
		t.Fatal("Chat and agent session prompts should be different")
	}

	// Re-build for same session keys — mode stays isolated across turns
	chatMsgs2 := cb.BuildMessages(
		[]providers.Message{{Role: "user", Content: "Hello"}}, "", "Follow up", nil,
		"", "", "chat-isolation-session", "chat",
	)
	agentMsgs2 := cb.BuildMessages(
		[]providers.Message{{Role: "user", Content: "Hello"}}, "", "Follow up", nil,
		"", "", "agent-isolation-session", "agent",
	)

	// Re-building for the same session keys must keep the mode isolated:
	// chat stays minimal, agent stays full.
	if !strings.Contains(chatMsgs2[0].Content, "You can search the web") {
		t.Error("Chat session prompt should remain minimal (web search) across turns")
	}
	if strings.Contains(chatMsgs2[0].Content, agentContent) {
		t.Error("Chat session prompt should not contain AGENT.md content")
	}
	if !strings.Contains(agentMsgs2[0].Content, agentContent) {
		t.Error("Agent session prompt should keep AGENT.md content across turns")
	}
}

// --- Subagents in system prompt tests ---

// TestSubagentsInSystemPrompt_ExplicitList verifies that an agent with
// allow_agents listing specific agents includes them in the system prompt.
func TestSubagentsInSystemPrompt_ExplicitList(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagents-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	cb.SetAvailableSubagents([]subagentInfo{
		{ID: "sales", Description: "Handles sales inquiries"},
		{ID: "support", Description: "Technical support"},
	})

	prompt := cb.GetInitialContext()

	if !strings.Contains(prompt, "## Subagents Available") {
		t.Error("Expected prompt to contain '## Subagents Available'")
	}
	if !strings.Contains(prompt, "spawn") {
		t.Error("Expected prompt to mention spawn tool")
	}
	if !strings.Contains(prompt, "**sales**") {
		t.Error("Expected prompt to contain sales agent")
	}
	if !strings.Contains(prompt, "**support**") {
		t.Error("Expected prompt to contain support agent")
	}
}

// TestSubagentsInSystemPrompt_Wildcard verifies that a wildcard allow_agents
// config results in all agents (except self) being listed.
func TestSubagentsInSystemPrompt_Wildcard(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagents-wildcard-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	// Simulate what resolveAvailableSubagents would return for wildcard
	cb.SetAvailableSubagents([]subagentInfo{
		{ID: "agent-a", Description: "Agent A"},
		{ID: "agent-b", Description: "Agent B"},
		{ID: "agent-c"}, // no description
	})

	prompt := cb.GetInitialContext()

	if !strings.Contains(prompt, "**agent-a**") {
		t.Error("Expected prompt to contain agent-a")
	}
	if !strings.Contains(prompt, "**agent-b**") {
		t.Error("Expected prompt to contain agent-b")
	}
	if !strings.Contains(prompt, "**agent-c**") {
		t.Error("Expected prompt to contain agent-c")
	}
	// agent-c has no description, so should NOT have " — "
	if strings.Contains(prompt, "**agent-c** —") {
		t.Error("agent-c should not have a description dash")
	}
}

// TestSubagentsInSystemPrompt_NoSubagents verifies that when no subagents
// are configured, the system prompt does NOT contain a Subagents section.
func TestSubagentsInSystemPrompt_NoSubagents(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "no-subagents-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	// Don't call SetAvailableSubagents — default is nil/empty

	prompt := cb.GetInitialContext()

	if strings.Contains(prompt, "## Subagents Available") {
		t.Error("Expected prompt to NOT contain '## Subagents Available' when no subagents configured")
	}
}

// TestSubagentsInSystemPrompt_WithDescription verifies that agent descriptions
// are rendered correctly in the system prompt.
func TestSubagentsInSystemPrompt_WithDescription(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagents-desc-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	cb.SetAvailableSubagents([]subagentInfo{
		{ID: "sales", Description: "Handles sales inquiries and product recommendations"},
	})

	prompt := cb.GetInitialContext()

	expected := "**sales** — Handles sales inquiries and product recommendations"
	if !strings.Contains(prompt, expected) {
		t.Errorf("Expected prompt to contain %q", expected)
	}
}

// TestSubagentsInSystemPrompt_EmptyDescription verifies that when an agent
// has no description, only the ID is shown (no dash separator).
func TestSubagentsInSystemPrompt_EmptyDescription(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagents-empty-desc-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	cb.SetAvailableSubagents([]subagentInfo{
		{ID: "minimal-agent"},
	})

	prompt := cb.GetInitialContext()

	if !strings.Contains(prompt, "**minimal-agent**") {
		t.Error("Expected prompt to contain minimal-agent ID")
	}
	// Should have a newline after the ID, not " — "
	lines := strings.Split(prompt, "\n")
	for _, line := range lines {
		if strings.Contains(line, "minimal-agent") {
			if strings.Contains(line, "—") {
				t.Errorf("Expected no dash separator for empty description, got line: %q", line)
			}
			break
		}
	}
}

// TestSubagentsInSystemPrompt_Invalidation verifies that SetAvailableSubagents
// invalidates the cached initial context.
func TestSubagentsInSystemPrompt_Invalidation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagents-invalidation-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// First build — no subagents
	prompt1 := cb.GetInitialContext()
	if strings.Contains(prompt1, "## Subagents Available") {
		t.Error("First build should not have subagents section")
	}

	// Set subagents
	cb.SetAvailableSubagents([]subagentInfo{
		{ID: "new-agent", Description: "A new agent"},
	})

	// Second build — should include subagents
	prompt2 := cb.GetInitialContext()
	if !strings.Contains(prompt2, "## Subagents Available") {
		t.Error("Second build should have subagents section after SetAvailableSubagents")
	}
	if !strings.Contains(prompt2, "new-agent") {
		t.Error("Second build should contain new-agent")
	}
}
