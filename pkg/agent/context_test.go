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

func TestGetGlobalConfigDir_NormalCase(t *testing.T) {
	dir := getGlobalConfigDir()

	if dir == "" {
		t.Error("Expected non-empty config dir, got empty string")
	}

	if !strings.HasSuffix(dir, ".lele") {
		t.Errorf("Expected path to end with .lele, got: %s", dir)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("Cannot get home dir: %v", err)
	}

	if !strings.HasPrefix(dir, home) {
		t.Errorf("Expected path to start with home dir %s, got: %s", home, dir)
	}
}

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

func TestSetToolsRegistry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	if cb.tools != nil {
		t.Error("Expected tools to be nil initially")
	}

	registry := tools.NewToolRegistry()
	cb.SetToolsRegistry(registry)

	if cb.tools != registry {
		t.Error("Expected tools to be set to registry")
	}

	cb.SetToolsRegistry(nil)
	if cb.tools != nil {
		t.Error("Expected tools to be nil after setting nil registry")
	}
}

func TestGetIdentity_ContentVerification(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	identity := cb.getIdentity()

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

	expectedRuntime := runtime.GOOS + " " + runtime.GOARCH
	if !strings.Contains(identity, expectedRuntime) {
		t.Errorf("Expected identity to contain runtime info '%s'", expectedRuntime)
	}

	absPath, _ := filepath.Abs(tmpDir)
	if !strings.Contains(identity, absPath) {
		t.Errorf("Expected identity to contain workspace path '%s'", absPath)
	}
}

func TestGetIdentity_WithTools(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	identity := cb.getIdentity()
	if strings.Contains(identity, "## Available Tools") {
		t.Error("Expected no Available Tools section when tools is nil")
	}

	registry := tools.NewToolRegistry()
	cb.SetToolsRegistry(registry)

	identity = cb.getIdentity()
	if strings.Contains(identity, "## Available Tools") {
		t.Error("Expected no Available Tools section when registry is empty")
	}
}

func TestGetIdentity_NoMemoryPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	identity := cb.getIdentity()

	if strings.Contains(identity, "MEMORY.md") {
		t.Error("Expected identity not to contain 'MEMORY.md'")
	}
	if strings.Contains(identity, "/MEMORY") {
		t.Error("Expected identity not to contain '/MEMORY'")
	}

	if !strings.Contains(identity, "update_memory") {
		t.Error("Expected identity to contain 'update_memory'")
	}
	if !strings.Contains(identity, "`update_memory`") {
		t.Error("Expected identity to contain '`update_memory`' (inline-code markup rendered)")
	}
	if strings.Contains(identity, "%!") {
		t.Error("Expected identity not to contain fmt error markers ('%!')")
	}

	absPath, _ := filepath.Abs(tmpDir)
	if !strings.Contains(identity, "Your workspace is at: "+absPath) {
		t.Errorf("Expected identity to contain workspace line with '%s'", absPath)
	}
	if !strings.Contains(identity, "- Skills: "+absPath+"/skills/{skill-name}/SKILL.md") {
		t.Errorf("Expected identity to contain skills line with '%s'", absPath)
	}
	if !strings.Contains(identity, "## Workspace") {
		t.Error("Expected identity to contain '## Workspace' section")
	}
}

func TestBuildToolsSection_WithTools(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	section := cb.buildToolsSection()
	if section != "" {
		t.Errorf("Expected empty section with nil tools, got: %s", section)
	}

	registry := tools.NewToolRegistry()

	mockTool := &mockTool{
		name:        "test_tool",
		description: "A test tool for testing",
	}
	registry.Register(mockTool)

	cb.SetToolsRegistry(registry)

	section = cb.buildToolsSection()

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

func TestBuildToolsSection_EmptyRegistry(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	registry := tools.NewToolRegistry()
	cb.SetToolsRegistry(registry)

	section := cb.buildToolsSection()
	if section != "" {
		t.Errorf("Expected empty section with empty registry, got: %s", section)
	}
}

func TestBuildToolsSection_ReadImageHiddenWithoutVision(t *testing.T) {
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
	registry.Register(&mockTool{
		name:        "read_file",
		description: "Read a text file",
	})
	cb.SetToolsRegistry(registry)

	section := cb.buildToolsSection()
	if strings.Contains(section, "`read_image`") {
		t.Error("Expected read_image to be hidden when vision is not supported")
	}
	if !strings.Contains(section, "`read_file`") {
		t.Error("Expected read_file to be present when vision is not supported")
	}

	cb.SetVisionSupported(true)
	section = cb.buildToolsSection()
	if !strings.Contains(section, "`read_image`") {
		t.Error("Expected read_image to be present when vision is supported")
	}
	if !strings.Contains(section, "`read_file`") {
		t.Error("Expected read_file to be present when vision is supported")
	}
}

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

	cb.SetVisionSupported(false)
	initial := cb.GetInitialContext()
	if strings.Contains(initial, "`read_image`") {
		t.Error("Expected read_image hidden in initial context")
	}

	cb.SetVisionSupported(true)
	updated := cb.GetInitialContext()
	if !strings.Contains(updated, "`read_image`") {
		t.Error("Expected read_image present after enabling vision")
	}

	cb.SetVisionSupported(true)
	again := cb.GetInitialContext()
	if !strings.Contains(again, "`read_image`") {
		t.Error("Expected read_image still present after redundant SetVisionSupported(true)")
	}
}

func TestBuildToolsSection_ReadVideoGating(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	registry := tools.NewToolRegistry()
	registry.Register(&mockTool{
		name:        "read_video",
		description: "Read a video file",
	})
	registry.Register(&mockTool{
		name:        "read_image",
		description: "Read an image file",
	})
	registry.Register(&mockTool{
		name:        "read_file",
		description: "Read a text file",
	})
	cb.SetToolsRegistry(registry)

	section := cb.buildToolsSection()
	if strings.Contains(section, "`read_video`") {
		t.Error("Expected read_video to be hidden when video and vision are not supported")
	}
	if !strings.Contains(section, "`read_file`") {
		t.Error("Expected read_file to be present when video is not supported")
	}

	cb.SetVisionSupported(true)
	section = cb.buildToolsSection()
	if !strings.Contains(section, "`read_image`") {
		t.Error("Expected read_image to be present when vision is supported")
	}
	if !strings.Contains(section, "`read_video`") {
		t.Error("Expected read_video present when only vision is supported (frames fallback)")
	}

	cb.SetVisionSupported(false)
	section = cb.buildToolsSection()
	if strings.Contains(section, "`read_video`") {
		t.Error("Expected read_video hidden again when both flags are off")
	}

	cb.SetVideoSupported(true)
	section = cb.buildToolsSection()
	if !strings.Contains(section, "`read_video`") {
		t.Error("Expected read_video to be present when video is supported")
	}
	if strings.Contains(section, "`read_image`") {
		t.Error("Expected read_image to stay gated by vision, not video")
	}
	if !strings.Contains(section, "`read_file`") {
		t.Error("Expected read_file to be present when video is supported")
	}
}

func TestSetVideoSupported_InvalidatesCaches(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	registry := tools.NewToolRegistry()
	registry.Register(&mockTool{
		name:        "read_video",
		description: "Read a video file",
	})
	cb.SetToolsRegistry(registry)

	cb.SetVideoSupported(false)
	initial := cb.GetInitialContext()
	if strings.Contains(initial, "`read_video`") {
		t.Error("Expected read_video hidden in initial context")
	}

	cb.SetVideoSupported(true)
	updated := cb.GetInitialContext()
	if !strings.Contains(updated, "`read_video`") {
		t.Error("Expected read_video present after enabling video")
	}

	cb.SetVideoSupported(true)
	again := cb.GetInitialContext()
	if !strings.Contains(again, "`read_video`") {
		t.Error("Expected read_video still present after redundant SetVideoSupported(true)")
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	prompt := cb.BuildSystemPrompt()

	if !strings.Contains(prompt, "# lele") {
		t.Error("Expected prompt to contain lele header")
	}

	if prompt == "" {
		t.Error("Expected non-empty system prompt")
	}
}

func TestBuildSystemPromptForSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

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
	defer func() {
		_ = os.Chdir(originalCwd)
	}()

	agentsContent := "harness-specific-agent-instructions"
	if err := os.WriteFile(filepath.Join(runDir, "AGENTS.md"), []byte(agentsContent), 0644); err != nil {
		t.Fatalf("Failed to write AGENTS.md: %v", err)
	}

	promptNonNative := cb.BuildSystemPromptForSession("some-session", "web")
	if strings.Contains(promptNonNative, "Harness Module") || strings.Contains(promptNonNative, agentsContent) {
		t.Error("Expected system prompt for non-native channel to exclude harness context")
	}

	promptNativeChannel := cb.BuildSystemPromptForSession("some-session", "native")
	if !strings.Contains(promptNativeChannel, "Harness Module") || !strings.Contains(promptNativeChannel, agentsContent) {
		t.Error("Expected system prompt for native channel to include harness context")
	}

	promptNativeSessionKey := cb.BuildSystemPromptForSession("tui:chat:123", "")
	if !strings.Contains(promptNativeSessionKey, "Harness Module") || !strings.Contains(promptNativeSessionKey, agentsContent) {
		t.Error("Expected system prompt for tui session key to include harness context")
	}

	promptSubstringNative := cb.BuildSystemPromptForSession("web:my-native-thing", "web")
	if strings.Contains(promptSubstringNative, "Harness Module") || strings.Contains(promptSubstringNative, agentsContent) {
		t.Error("Expected system prompt for sessionKey with 'native' as substring (not prefix) to exclude harness context")
	}
}

func TestResetMemoryContext(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	cb.ResetMemoryContext()
}

func TestLoadBootstrapFiles_NoFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	result := cb.LoadBootstrapFiles()

	if result != "" {
		t.Errorf("Expected empty result when no bootstrap files exist, got: %s", result)
	}
}

func TestLoadBootstrapFiles_WithFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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

	for filename, content := range bootstrapFiles {
		if !strings.Contains(result, "## "+filename) {
			t.Errorf("Expected result to contain header for %s", filename)
		}
		if !strings.Contains(result, content) {
			t.Errorf("Expected result to contain content from %s", filename)
		}
	}
}

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

func TestLoadBootstrapFiles_PartialFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	content := "Only SOUL content"
	path := filepath.Join(tmpDir, "SOUL.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create SOUL.md: %v", err)
	}

	cb := NewContextBuilder(tmpDir)
	result := cb.LoadBootstrapFiles()

	if !strings.Contains(result, "## SOUL.md") {
		t.Error("Expected result to contain SOUL.md header")
	}
	if !strings.Contains(result, content) {
		t.Error("Expected result to contain SOUL.md content")
	}

	if strings.Contains(result, "AGENT.md") {
		t.Error("Expected result to not contain AGENT.md")
	}
}

func TestGetInitialContext_NoFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	context := cb.GetInitialContext()

	if !strings.Contains(context, "# lele") {
		t.Error("Expected context to contain lele header")
	}

}

func TestGetInitialContext_WithBootstrapFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	path := filepath.Join(tmpDir, "SOUL.md")
	if err := os.WriteFile(path, []byte("SOUL content here"), 0644); err != nil {
		t.Fatalf("Failed to create SOUL.md: %v", err)
	}

	cb := NewContextBuilder(tmpDir)
	context := cb.GetInitialContext()

	if !strings.Contains(context, "# lele") {
		t.Error("Expected context to contain lele header")
	}

	if !strings.Contains(context, "SOUL content here") {
		t.Error("Expected context to contain SOUL.md content")
	}
}

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

	if len(messages) != 4 {
		t.Errorf("Expected 4 messages, got %d", len(messages))
	}

	if messages[0].Role != "system" {
		t.Errorf("Expected first message to be system, got %s", messages[0].Role)
	}

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

	first := cb.BuildSystemPromptForSession("tui:chat:1", "")
	if !strings.Contains(first, "harness-v1") {
		t.Fatalf("expected first build to contain harness-v1, got: %s", first)
	}

	if err := os.WriteFile(filepath.Join(runDir, "AGENTS.md"), []byte("harness-v2"), 0644); err != nil {
		t.Fatalf("Failed to rewrite AGENTS.md: %v", err)
	}

	second := cb.BuildSystemPromptForSession("tui:chat:2", "")
	if !strings.Contains(second, "harness-v1") {
		t.Fatalf("expected second build to reuse cached harness-v1, got: %s", second)
	}
	if strings.Contains(second, "harness-v2") {
		t.Fatalf("expected cached harness to NOT contain harness-v2, got: %s", second)
	}

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

func TestBuildMessages_WithOrphanedToolMessages(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	history := []providers.Message{
		{Role: "tool", Content: "Orphaned tool result", ToolCallID: "call-1"},
		{Role: "user", Content: "User message"},
		{Role: "assistant", Content: "Assistant response"},
	}

	messages := cb.BuildMessages(history, "", "Current", nil, "", "", "", "")

	if len(messages) != 5 {
		t.Errorf("Expected 5 messages (history passed through), got %d", len(messages))
	}
	if messages[1].Role != "tool" || messages[1].ToolCallID != "call-1" {
		t.Errorf("Expected the orphaned tool message to be passed through, got %+v", messages[1])
	}
}

func TestBuildMessages_EmptyHistory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	messages := cb.BuildMessages([]providers.Message{}, "", "Hello", nil, "", "", "", "")

	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}
}

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

	if len(result) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(result))
	}

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

	if len(result) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(result))
	}

	assistantMsg := result[len(result)-1]
	if assistantMsg.Role != "assistant" {
		t.Errorf("Expected assistant message, got %s", assistantMsg.Role)
	}
	if assistantMsg.Content != "Assistant response" {
		t.Errorf("Expected content 'Assistant response', got %s", assistantMsg.Content)
	}
}

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

func TestLoadSkills_NoSkills(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	result := cb.loadSkills()

	if result != "" {
		if strings.Contains(result, filepath.Join(tmpDir, "skills")) {
			t.Errorf("Expected no workspace-local skills, but found workspace path in result: %s", result)
		}
	}
}

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

	for _, name := range names {
		skillPath := filepath.Join(tmpDir, "skills", name, "SKILL.md")
		if _, err := os.Stat(skillPath); err == nil {
			t.Errorf("Found unexpected workspace-local skill: %s", name)
		}
	}

	if info["total"] != info["available"] {
		t.Errorf("Expected total == available, got total=%v available=%v", info["total"], info["available"])
	}
}

func TestGetSkillsInfo_WithSkills(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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

func TestContextBuilder_NilSafety(t *testing.T) {

	var cb *ContextBuilder

	defer func() {
		if r := recover(); r != nil {
			t.Logf("Expected panic when calling method on nil ContextBuilder: %v", r)
		}
	}()

	_ = cb.GetInitialContext()
	t.Error("Expected panic when calling GetInitialContext on nil ContextBuilder")
}

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

func TestBuildMessages_MultipleOrphanedTools(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	history := []providers.Message{
		{Role: "tool", Content: "Orphaned 1", ToolCallID: "call-1"},
		{Role: "tool", Content: "Orphaned 2", ToolCallID: "call-2"},
		{Role: "user", Content: "User message"},
	}

	messages := cb.BuildMessages(history, "", "Current", nil, "", "", "", "")

	if len(messages) != 5 {
		t.Errorf("Expected 5 messages (history passed through), got %d", len(messages))
	}
	if messages[1].Role != "tool" || messages[2].Role != "tool" {
		t.Errorf("Expected both orphaned tool messages to be passed through, got %s/%s",
			messages[1].Role, messages[2].Role)
	}
}

func TestBuildMessages_ToolNotAtStart(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	history := []providers.Message{
		{Role: "user", Content: "User message"},
		{Role: "assistant", Content: "Assistant response", ToolCalls: []providers.ToolCall{{ID: "call-1", Name: "exec"}}},
		{Role: "tool", Content: "Tool result", ToolCallID: "call-1"},
	}

	messages := cb.BuildMessages(history, "", "Current", nil, "", "", "", "")

	if len(messages) != 5 {
		t.Errorf("Expected 5 messages, got %d", len(messages))
	}

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

func TestGetInitialContext_WithMemory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	memoryFile := filepath.Join(tmpDir, "MEMORY.md")
	if err := os.WriteFile(memoryFile, []byte("Long-term memory content"), 0644); err != nil {
		t.Fatalf("Failed to create MEMORY.md: %v", err)
	}

	cb := NewContextBuilder(tmpDir)
	context := cb.GetInitialContext()

	if !strings.Contains(context, "Long-term memory content") {
		t.Error("Expected context to contain memory content")
	}
}

func TestGetInitialContext_WithSkills(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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

	if !strings.Contains(context, "# Skills") {
		t.Error("Expected context to contain Skills section")
	}

	if !strings.Contains(context, "my-skill") {
		t.Error("Expected context to contain skill name")
	}
}

func TestLoadBootstrapFiles_InvalidPath(t *testing.T) {
	cb := NewContextBuilder("/nonexistent/path/that/does/not/exist")
	result := cb.LoadBootstrapFiles()

	if result != "" {
		t.Errorf("Expected empty result for invalid path, got: %s", result)
	}
}

func TestBuildMessages_NilMedia(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	messages := cb.BuildMessages([]providers.Message{}, "", "Hello", nil, "", "", "", "")

	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}
}

func TestBuildMessages_EmptyMedia(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

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

func TestContextBuilder_MultipleCalls(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	ctx1 := cb.GetInitialContext()
	ctx2 := cb.GetInitialContext()

	if ctx1 != ctx2 {
		t.Error("Expected GetInitialContext to return consistent results")
	}

	prompt1 := cb.BuildSystemPrompt()
	prompt2 := cb.BuildSystemPrompt()

	if prompt1 != prompt2 {
		t.Error("Expected BuildSystemPrompt to return consistent results")
	}
}

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

func TestGetSkillsInfo_MultipleSkills(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "context-builder-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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

func TestBuildMinimalSystemPrompt(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "minimal-prompt-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	prompt := cb.BuildMinimalSystemPrompt()

	required := []string{"web_search", "web_fetch", "helpful AI assistant"}
	for _, r := range required {
		if !strings.Contains(prompt, r) {
			t.Errorf("BuildMinimalSystemPrompt() missing %q", r)
		}
	}

	excluded := []string{"AGENT.md", "SOUL.md", "## Skills", "IDENTITY.md", "MEMORY.md"}
	for _, e := range excluded {
		if strings.Contains(prompt, e) {
			t.Errorf("BuildMinimalSystemPrompt() should not contain %q, but does", e)
		}
	}
}

func TestBuildMessagesChatMode(t *testing.T) {
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

	if !strings.Contains(systemMsg.Content, "web_search") {
		t.Error("Chat mode system prompt should contain 'web_search'")
	}
	if !strings.Contains(systemMsg.Content, "web_fetch") {
		t.Error("Chat mode system prompt should contain 'web_fetch'")
	}

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

	agentMessages := cb.BuildMessages(
		[]providers.Message{}, "", "Hello", nil,
		"", "", "agent-session-test", "agent",
	)

	if agentMessages[0].Content == systemMsg.Content {
		t.Error("Chat mode and agent mode should produce different system prompts")
	}

	if !strings.Contains(agentMessages[0].Content, agentContent) {
		t.Error("Agent mode system prompt should contain AGENT.md content")
	}
}

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

	if agentSystemPrompt != defaultSystemPrompt {
		t.Error("Agent mode and empty mode should produce identical system prompts")
	}

	if strings.Contains(agentSystemPrompt, "You can search the web and fetch web pages") {
		t.Error("Agent mode should NOT use the minimal chat prompt")
	}
}

func TestBuildMessagesChatModeCacheIsolation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cache-isolation-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	agentContent := "# AGENT.md\nCache isolation test content."
	os.WriteFile(filepath.Join(tmpDir, "AGENT.md"), []byte(agentContent), 0644)

	cb := NewContextBuilder(tmpDir)

	chatMsgs := cb.BuildMessages(
		[]providers.Message{}, "", "Hello", nil,
		"", "", "chat-isolation-session", "chat",
	)

	agentMsgs := cb.BuildMessages(
		[]providers.Message{}, "", "Hello", nil,
		"", "", "agent-isolation-session", "agent",
	)

	chatPrompt := chatMsgs[0].Content
	agentPrompt := agentMsgs[0].Content

	if chatPrompt == agentPrompt {
		t.Fatal("Chat and agent session prompts should be different")
	}

	chatMsgs2 := cb.BuildMessages(
		[]providers.Message{{Role: "user", Content: "Hello"}}, "", "Follow up", nil,
		"", "", "chat-isolation-session", "chat",
	)
	agentMsgs2 := cb.BuildMessages(
		[]providers.Message{{Role: "user", Content: "Hello"}}, "", "Follow up", nil,
		"", "", "agent-isolation-session", "agent",
	)

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

func TestSubagentsInSystemPrompt_Wildcard(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagents-wildcard-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)
	cb.SetAvailableSubagents([]subagentInfo{
		{ID: "agent-a", Description: "Agent A"},
		{ID: "agent-b", Description: "Agent B"},
		{ID: "agent-c"},
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
	if strings.Contains(prompt, "**agent-c** —") {
		t.Error("agent-c should not have a description dash")
	}
}

func TestSubagentsInSystemPrompt_NoSubagents(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "no-subagents-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	prompt := cb.GetInitialContext()

	if strings.Contains(prompt, "## Subagents Available") {
		t.Error("Expected prompt to NOT contain '## Subagents Available' when no subagents configured")
	}
}

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

func TestSubagentsInSystemPrompt_Invalidation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subagents-invalidation-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	prompt1 := cb.GetInitialContext()
	if strings.Contains(prompt1, "## Subagents Available") {
		t.Error("First build should not have subagents section")
	}

	cb.SetAvailableSubagents([]subagentInfo{
		{ID: "new-agent", Description: "A new agent"},
	})

	prompt2 := cb.GetInitialContext()
	if !strings.Contains(prompt2, "## Subagents Available") {
		t.Error("Second build should have subagents section after SetAvailableSubagents")
	}
	if !strings.Contains(prompt2, "new-agent") {
		t.Error("Second build should contain new-agent")
	}
}

func writeTestSkill(t *testing.T, tmpDir, name, description string) {
	t.Helper()
	skillDir := filepath.Join(tmpDir, "skills", name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir %s: %v", name, err)
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n# %s\n", name, description, name)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md for %s: %v", name, err)
	}
}

func skillNamesInPrompt(t *testing.T, prompt string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "<name>") && strings.HasSuffix(line, "</name>") {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(line, "<name>"), "</name>"))
		}
	}
	return out
}

func TestSetSkillsFilter_RestrictsPromptSkills(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	tmpDir, err := os.MkdirTemp("", "skills-filter-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	writeTestSkill(t, tmpDir, "alpha-skill", "Alpha description")
	writeTestSkill(t, tmpDir, "beta-skill", "Beta description")

	cb := NewContextBuilder(tmpDir)
	cb.SetSkillsFilter([]string{"alpha-skill"})

	prompt := cb.GetInitialContext()
	if !strings.Contains(prompt, "# Skills") {
		t.Fatal("Expected a Skills section in the prompt")
	}
	if !strings.Contains(prompt, "<name>alpha-skill</name>") {
		t.Errorf("Expected alpha-skill in prompt, got names: %v", skillNamesInPrompt(t, prompt))
	}
	if strings.Contains(prompt, "<name>beta-skill</name>") {
		t.Errorf("beta-skill must be filtered out, got names: %v", skillNamesInPrompt(t, prompt))
	}
	if strings.Contains(prompt, "Beta description") {
		t.Error("Filtered skill description leaked into the prompt")
	}

	cbAll := NewContextBuilder(tmpDir)
	cbAll.SetSkillsFilter([]string{})

	promptAll := cbAll.GetInitialContext()
	for _, want := range []string{"<name>alpha-skill</name>", "<name>beta-skill</name>"} {
		if !strings.Contains(promptAll, want) {
			t.Errorf("Expected %s with an empty filter, got names: %v", want, skillNamesInPrompt(t, promptAll))
		}
	}

	cbNil := NewContextBuilder(tmpDir)
	cbNil.SetSkillsFilter(nil)
	if got := skillNamesInPrompt(t, cbNil.GetInitialContext()); len(got) < 2 {
		t.Errorf("Expected both skills with a nil filter, got %v", got)
	}
}

func TestSetSkillsFilter_InvalidatesCachedContext(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	tmpDir, err := os.MkdirTemp("", "skills-filter-invalidation-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	writeTestSkill(t, tmpDir, "alpha-skill", "Alpha description")
	writeTestSkill(t, tmpDir, "beta-skill", "Beta description")

	cb := NewContextBuilder(tmpDir)

	first := cb.GetInitialContext()
	if !strings.Contains(first, "<name>beta-skill</name>") {
		t.Fatal("First build should list every skill")
	}

	cb.SetSkillsFilter([]string{"alpha-skill"})
	second := cb.GetInitialContext()
	if strings.Contains(second, "<name>beta-skill</name>") {
		t.Error("Second build must drop beta-skill: cached context was not invalidated")
	}
	if !strings.Contains(second, "<name>alpha-skill</name>") {
		t.Error("Second build must keep alpha-skill")
	}

	cb.SetSkillsFilter(nil)
	third := cb.GetInitialContext()
	if !strings.Contains(third, "<name>beta-skill</name>") {
		t.Error("Third build must list every skill again after clearing the filter")
	}
}

func TestSetSkillsFilter_UnknownNameDropsSection(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skills-filter-unknown-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	writeTestSkill(t, tmpDir, "alpha-skill", "Alpha description")

	cb := NewContextBuilder(tmpDir)
	cb.SetSkillsFilter([]string{"not-installed"})

	prompt := cb.GetInitialContext()
	if strings.Contains(prompt, "<name>alpha-skill</name>") {
		t.Error("alpha-skill must not appear when the filter excludes it")
	}
	if strings.Contains(prompt, "# Skills\n\nThe following skills extend") {
		t.Errorf("Expected no Skills section when the filter matches nothing:\n%s", prompt)
	}
}

func TestLoadSkills_AppliesSkillsFilter(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "load-skills-filter-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	writeTestSkill(t, tmpDir, "alpha-skill", "Alpha description")
	writeTestSkill(t, tmpDir, "beta-skill", "Beta description")

	cb := NewContextBuilder(tmpDir)
	cb.SetSkillsFilter([]string{"alpha-skill"})

	result := cb.loadSkills()
	if !strings.Contains(result, "### Skill: alpha-skill") {
		t.Errorf("Expected alpha-skill body, got:\n%s", result)
	}
	if strings.Contains(result, "### Skill: beta-skill") {
		t.Errorf("beta-skill body must be filtered out, got:\n%s", result)
	}
}
