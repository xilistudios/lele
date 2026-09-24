// Lele - Ultra-lightweight personal AI agent
// Tests for chat-mode tool guard in toolExecutor.Execute
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
)

// TestChatModeToolGuard_BlockedTools verifies that in chat mode, tools
// other than web_search and web_fetch are rejected with an error result.
func TestChatModeToolGuard_BlockedTools(t *testing.T) {
	blockedToolNames := []string{"exec", "read_file", "write_file", "spawn", "list_dir", "edit_file"}

	for _, toolName := range blockedToolNames {
		t.Run(toolName, func(t *testing.T) {
			sm := session.NewSessionManager()
			sessionKey := "test:chat-guard:" + toolName

			// Set session to chat mode
			sm.GetOrCreate(sessionKey)
			if err := sm.SetMode(sessionKey, "chat"); err != nil {
				t.Fatalf("SetMode failed: %v", err)
			}

			// Build a minimal AgentLoop — the guard returns early before
			// touching bus or verboseManager, so nil-safe fields are fine.
			al := &AgentLoop{
				bus:            bus.NewMessageBus(),
				verboseManager: session.NewVerboseManager(),
			}
			te := newToolExecutor(al)

			agent := &AgentInstance{
				Sessions: sm,
			}

			opts := toolExecOptions{
				ctx:        context.Background(),
				agent:      agent,
				sessionKey: sessionKey,
				tc: providers.ToolCall{
					ID:   "call-test",
					Name: toolName,
				},
				channel: "test",
			}

			result, err := te.Execute(opts)
			if err != nil {
				t.Fatalf("Execute returned unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil ToolResult for blocked tool")
			}
			if !result.IsError {
				t.Errorf("expected IsError=true for blocked tool %q", toolName)
			}
			if result.ForLLM == "" {
				t.Errorf("expected error message in ForLLM for blocked tool %q", toolName)
			}
			// Verify the tool was not actually executed (error message mentions "chat mode")
			if !containsSubstring(result.ForLLM, "chat mode") {
				t.Errorf("expected error message to mention 'chat mode', got: %s", result.ForLLM)
			}
		})
	}
}

// TestChatModeToolGuard_AllowedTools verifies that web_search and web_fetch
// are NOT blocked by the chat mode guard. We test this by confirming the
// guard does NOT return an error result for these tool names.
// Full tool execution requires a registered tool, so we just verify the
// guard doesn't reject them (the code falls through to publishExecuting).
func TestChatModeToolGuard_AllowedTools(t *testing.T) {
	allowedToolNames := []string{"web_search", "web_fetch"}

	for _, toolName := range allowedToolNames {
		t.Run(toolName, func(t *testing.T) {
			sm := session.NewSessionManager()
			sessionKey := "test:chat-allow:" + toolName

			sm.GetOrCreate(sessionKey)
			if err := sm.SetMode(sessionKey, "chat"); err != nil {
				t.Fatalf("SetMode failed: %v", err)
			}

			// Create a registry with a mock tool so ExecuteWithContext doesn't nil-deref
			registry := tools.NewToolRegistry()
			mockWebTool := &mockToolForExecutor{
				name:        toolName,
				description: "Mock " + toolName,
			}
			registry.Register(mockWebTool)

			al := &AgentLoop{
				bus:            bus.NewMessageBus(),
				verboseManager: session.NewVerboseManager(),
			}
			te := newToolExecutor(al)

			agent := &AgentInstance{
				Sessions: sm,
				Tools:    registry,
			}

			opts := toolExecOptions{
				ctx:        context.Background(),
				agent:      agent,
				sessionKey: sessionKey,
				tc: providers.ToolCall{
					ID:   "call-test",
					Name: toolName,
				},
				channel:   "test",
				chatID:    "test-chat-id",
				iteration: 1,
			}

			result, err := te.Execute(opts)
			if err != nil {
				t.Fatalf("Execute returned unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil ToolResult for allowed tool")
			}
			// The guard should NOT have rejected it — IsError should be false
			// (the mock tool returns a success result).
			if result.IsError {
				t.Errorf("web tool %q should not be blocked by chat mode guard, got error: %s", toolName, result.ForLLM)
			}
		})
	}
}

// TestChatModeGuard_AgentModeNoBlock verifies that the same tools are
// NOT blocked when the session is in agent mode (default).
func TestChatModeGuard_AgentModeNoBlock(t *testing.T) {
	sm := session.NewSessionManager()
	sessionKey := "test:agent-mode"

	sm.GetOrCreate(sessionKey)
	// Explicitly set agent mode
	if err := sm.SetMode(sessionKey, "agent"); err != nil {
		t.Fatalf("SetMode failed: %v", err)
	}

	registry := tools.NewToolRegistry()
	mockExecTool := &mockToolForExecutor{
		name:        "exec",
		description: "Mock exec",
	}
	registry.Register(mockExecTool)

	al := &AgentLoop{
		bus:            bus.NewMessageBus(),
		verboseManager: session.NewVerboseManager(),
	}
	te := newToolExecutor(al)

	agent := &AgentInstance{
		Sessions: sm,
		Tools:    registry,
	}

	opts := toolExecOptions{
		ctx:        context.Background(),
		agent:      agent,
		sessionKey: sessionKey,
		tc: providers.ToolCall{
			ID:   "call-test",
			Name: "exec",
		},
		channel:   "test",
		chatID:    "test-chat-id",
		iteration: 1,
	}

	result, err := te.Execute(opts)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ToolResult")
	}
	// Should NOT be blocked — exec should execute normally in agent mode
	if result.IsError && containsSubstring(result.ForLLM, "chat mode") {
		t.Error("exec should not be blocked in agent mode")
	}
}

// TestChatModeGuard_NilAgent verifies that nil agent is safely skipped
// (the guard doesn't panic and doesn't block the tool).
func TestChatModeGuard_NilAgent(t *testing.T) {
	// Create a registry with a mock exec tool so ExecuteWithContext
	// can complete without panic.
	registry := tools.NewToolRegistry()
	mockExec := &mockToolForExecutor{name: "exec", description: "Mock exec"}
	registry.Register(mockExec)

	al := &AgentLoop{
		bus:            bus.NewMessageBus(),
		verboseManager: session.NewVerboseManager(),
	}
	te := newToolExecutor(al)

	// Minimal agent — only Tools populated so ExecuteWithContext works.
	agent := &AgentInstance{
		Tools: registry,
	}

	opts := toolExecOptions{
		ctx:        context.Background(),
		agent:      agent,
		sessionKey: "test:nil-sessions",
		tc: providers.ToolCall{
			ID:   "call-test",
			Name: "exec",
		},
		channel:   "test",
		chatID:    "test-chat-id",
		iteration: 1,
	}

	// Should not panic — the guard skips when Sessions is nil
	result, err := te.Execute(opts)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ToolResult")
	}
}

// ============================================================================
// Video tool guard (read_video) — mirrors the read_image vision guard
// ============================================================================

// newVideoGuardHarness builds an AgentLoop + agent wired for the read_video
// executor guard: the loop carries a session manager and a config exposing a
// video-capable, a vision-only (frames fallback) and a model with neither
// capability under "test-provider", and the agent's tool registry has a mock
// read_video so the allowed paths can run to completion. The caller removes
// tmpDir when the test finishes.
func newVideoGuardHarness(t *testing.T) (*AgentLoop, *AgentInstance, string) {
	t.Helper()

	al, tmpDir := createLLMRunnerTestAgentLoop(t)

	al.cfg().Providers = &config.ProvidersConfig{
		Named: map[string]config.NamedProviderConfig{
			"test-provider": {
				Type: "openai",
				Models: map[string]config.ProviderModelConfig{
					"video-model":  {Video: true},
					"vision-model": {Vision: true},
					"text-model":   {Video: false, Vision: false},
				},
			},
		},
	}

	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Tools.Register(&mockToolForExecutor{name: "read_video", description: "Mock read_video"})

	return al, agent, tmpDir
}

// executeReadVideo runs the executor for a read_video tool call on the given
// agent/session and returns the tool result (or the executor error).
func executeReadVideo(t *testing.T, al *AgentLoop, agent *AgentInstance) (*tools.ToolResult, error) {
	t.Helper()
	te := newToolExecutor(al)
	return te.Execute(toolExecOptions{
		ctx:        context.Background(),
		agent:      agent,
		sessionKey: "test:video-guard",
		tc: providers.ToolCall{
			ID:   "call-video",
			Name: "read_video",
		},
		channel:   "test",
		chatID:    "test-chat-id",
		iteration: 1,
	})
}

// TestVideoToolGuard_BlocksWhenModelLacksVideoAndVision verifies that
// read_video is rejected when the session's resolved model has neither the
// video capability (native mode) nor vision (frames fallback) — and only
// then.
func TestVideoToolGuard_BlocksWhenModelLacksVideoAndVision(t *testing.T) {
	al, agent, tmpDir := newVideoGuardHarness(t)
	defer os.RemoveAll(tmpDir)

	agent.Model = "test-provider:text-model" // Video: false, Vision: false

	result, err := executeReadVideo(t, al, agent)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ToolResult for blocked read_video")
	}
	if !result.IsError {
		t.Error("expected IsError=true when the model lacks video and vision")
	}
	const want = "read_video is not available: the current model supports neither video nor vision"
	if result.ForLLM != want {
		t.Errorf("ForLLM = %q, want %q", result.ForLLM, want)
	}
}

// TestVideoToolGuard_AllowsWhenModelSupportsVideo verifies that read_video
// executes normally when the session's model carries the video flag.
func TestVideoToolGuard_AllowsWhenModelSupportsVideo(t *testing.T) {
	al, agent, tmpDir := newVideoGuardHarness(t)
	defer os.RemoveAll(tmpDir)

	agent.Model = "test-provider:video-model" // Video: true

	result, err := executeReadVideo(t, al, agent)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ToolResult for allowed read_video")
	}
	if result.IsError {
		t.Errorf("read_video should not be blocked for a video-capable model, got: %s", result.ForLLM)
	}
	if result.ForLLM != "mock result for read_video" {
		t.Errorf("expected the mock tool to run, got ForLLM = %q", result.ForLLM)
	}
}

// TestVideoToolGuard_AllowsVisionOnlyModel verifies that a vision-only model
// may run read_video: it delivers the keyframes+transcript frames fallback,
// which only needs image input.
func TestVideoToolGuard_AllowsVisionOnlyModel(t *testing.T) {
	al, agent, tmpDir := newVideoGuardHarness(t)
	defer os.RemoveAll(tmpDir)

	agent.Model = "test-provider:vision-model" // Vision: true, Video: false

	result, err := executeReadVideo(t, al, agent)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ToolResult for allowed read_video")
	}
	if result.IsError {
		t.Errorf("read_video should not be blocked for a vision-only model, got: %s", result.ForLLM)
	}
	if result.ForLLM != "mock result for read_video" {
		t.Errorf("expected the mock tool to run, got ForLLM = %q", result.ForLLM)
	}
}

// TestVideoToolGuard_SkipsWhenNoSessionModel pins the historical skip
// behaviour shared with the read_image guard: when no model can be resolved
// for the session (empty model), the guard does not fire and the tool runs.
func TestVideoToolGuard_SkipsWhenNoSessionModel(t *testing.T) {
	al, agent, tmpDir := newVideoGuardHarness(t)
	defer os.RemoveAll(tmpDir)

	agent.Model = "" // no model resolvable for the session

	result, err := executeReadVideo(t, al, agent)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ToolResult when the guard is skipped")
	}
	if result.IsError {
		t.Errorf("read_video should be allowed when no session model is resolvable, got: %s", result.ForLLM)
	}
	if result.ForLLM != "mock result for read_video" {
		t.Errorf("expected the mock tool to run, got ForLLM = %q", result.ForLLM)
	}
}

// containsSubstring checks if s contains substr.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// mockToolForExecutor is a minimal tools.Tool implementation for testing.
type mockToolForExecutor struct {
	name        string
	description string
}

func (m *mockToolForExecutor) Name() string        { return m.name }
func (m *mockToolForExecutor) Description() string { return m.description }
func (m *mockToolForExecutor) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (m *mockToolForExecutor) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: "mock result for " + m.name}
}

// makeAgentTestVideo generates a short decodable mp4 fixture for frames-mode
// tests (video-only is enough: the tool under test has no transcriber).
// Skips the test when ffmpeg/ffprobe are unavailable or fixture generation
// fails entirely.
func makeAgentTestVideo(t *testing.T, dir string) string {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	out := filepath.Join(dir, "fixture.mp4")
	argsFor := func(codec string) []string {
		return []string{"-v", "error", "-f", "lavfi", "-i",
			"testsrc=duration=2:size=320x240:rate=10",
			"-pix_fmt", "yuv420p", "-c:v", codec, "-y", out}
	}
	for _, codec := range []string{"libx264", "mpeg4"} {
		if err := exec.Command(ffmpeg, argsFor(codec)...).Run(); err == nil {
			return out
		}
	}
	t.Skip("could not generate test video fixture")
	return ""
}

// TestVideoToolGuard_SessionModelStampsVideoCaps pins per-call capability
// resolution end-to-end through the executor: the ReadVideoTool was built
// with the PRIMARY model's video-capable snapshot, but the session model
// (via /model → sessionModels) is vision-only. The executor must stamp the
// resolved session caps onto the tool context so mode=auto delivers frames
// (image_url parts), not native video_url — which the vision-only session
// model would reject.
func TestVideoToolGuard_SessionModelStampsVideoCaps(t *testing.T) {
	al, agent, tmpDir := newVideoGuardHarness(t)
	defer os.RemoveAll(tmpDir)

	// Primary model is video-capable (the tool snapshot below reflects it)…
	agent.Model = "test-provider:video-model"
	// …but the session overrides it with a vision-only model.
	al.sessionModels.Store("test:video-guard", "test-provider:vision-model")

	videoPath := makeAgentTestVideo(t, tmpDir)

	// Replace the harness mock with a REAL read_video carrying the
	// video-capable construction-time snapshot.
	agent.Tools.Register(tools.NewReadVideoTool(tmpDir, true,
		tools.VideoCapabilities{Video: true, Vision: true}, nil))

	te := newToolExecutor(al)
	result, err := te.Execute(toolExecOptions{
		ctx:        context.Background(),
		agent:      agent,
		sessionKey: "test:video-guard",
		tc: providers.ToolCall{
			ID:   "call-video",
			Name: "read_video",
			Arguments: map[string]interface{}{
				"path":   videoPath,
				"prompt": "What happens here?",
				"frames": 3,
			},
		},
		channel:   "test",
		chatID:    "test-chat-id",
		iteration: 1,
	})
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ToolResult")
	}
	if result.IsError {
		t.Fatalf("read_video failed: %s", result.ForLLM)
	}
	if len(result.ContextMessages) != 1 {
		t.Fatalf("ContextMessages len = %d, want 1", len(result.ContextMessages))
	}
	hasImage, hasVideo := false, false
	for i, part := range result.ContextMessages[0].ContentParts {
		switch part.Type {
		case "text":
			// prompt part
		case "image_url":
			hasImage = true
		case "video_url":
			hasVideo = true
		default:
			t.Fatalf("parts[%d].Type = %q, want text/image_url only", i, part.Type)
		}
	}
	if !hasImage {
		t.Fatalf("no image_url part: session model is vision-only, frames caps were not stamped (ForLLM: %s)", result.ForLLM)
	}
	if hasVideo {
		t.Fatalf("video_url part delivered to a vision-only session model (stamped caps ignored; ForLLM: %s)", result.ForLLM)
	}
}
