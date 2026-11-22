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

func TestChatModeToolGuard_BlockedTools(t *testing.T) {
	blockedToolNames := []string{"exec", "read_file", "write_file", "spawn", "list_dir", "edit_file"}

	for _, toolName := range blockedToolNames {
		t.Run(toolName, func(t *testing.T) {
			sm := session.NewSessionManager()
			sessionKey := "test:chat-guard:" + toolName

			sm.GetOrCreate(sessionKey)
			if err := sm.SetMode(sessionKey, "chat"); err != nil {
				t.Fatalf("SetMode failed: %v", err)
			}

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
			if !containsSubstring(result.ForLLM, "chat mode") {
				t.Errorf("expected error message to mention 'chat mode', got: %s", result.ForLLM)
			}
		})
	}
}

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
			if result.IsError {
				t.Errorf("web tool %q should not be blocked by chat mode guard, got error: %s", toolName, result.ForLLM)
			}
		})
	}
}

func TestChatModeGuard_AgentModeNoBlock(t *testing.T) {
	sm := session.NewSessionManager()
	sessionKey := "test:agent-mode"

	sm.GetOrCreate(sessionKey)
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
	if result.IsError && containsSubstring(result.ForLLM, "chat mode") {
		t.Error("exec should not be blocked in agent mode")
	}
}

func TestChatModeGuard_NilAgent(t *testing.T) {
	registry := tools.NewToolRegistry()
	mockExec := &mockToolForExecutor{name: "exec", description: "Mock exec"}
	registry.Register(mockExec)

	al := &AgentLoop{
		bus:            bus.NewMessageBus(),
		verboseManager: session.NewVerboseManager(),
	}
	te := newToolExecutor(al)

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

	result, err := te.Execute(opts)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ToolResult")
	}
}

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

func TestVideoToolGuard_BlocksWhenModelLacksVideoAndVision(t *testing.T) {
	al, agent, tmpDir := newVideoGuardHarness(t)
	defer os.RemoveAll(tmpDir)

	agent.Model = "test-provider:text-model"

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

func TestVideoToolGuard_AllowsWhenModelSupportsVideo(t *testing.T) {
	al, agent, tmpDir := newVideoGuardHarness(t)
	defer os.RemoveAll(tmpDir)

	agent.Model = "test-provider:video-model"

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

func TestVideoToolGuard_AllowsVisionOnlyModel(t *testing.T) {
	al, agent, tmpDir := newVideoGuardHarness(t)
	defer os.RemoveAll(tmpDir)

	agent.Model = "test-provider:vision-model"

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

func TestVideoToolGuard_SkipsWhenNoSessionModel(t *testing.T) {
	al, agent, tmpDir := newVideoGuardHarness(t)
	defer os.RemoveAll(tmpDir)

	agent.Model = ""

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

func TestVideoToolGuard_SessionModelStampsVideoCaps(t *testing.T) {
	al, agent, tmpDir := newVideoGuardHarness(t)
	defer os.RemoveAll(tmpDir)

	agent.Model = "test-provider:video-model"
	al.sessionModels.Store("test:video-guard", "test-provider:vision-model")

	videoPath := makeAgentTestVideo(t, tmpDir)

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
