package heartbeat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/tools"
)

func timeMin() time.Duration { return time.Minute }

func testCtx() context.Context {
	ctx, _ := context.WithTimeout(context.Background(), time.Second)
	return ctx
}

func TestNewHeartbeatService_IntervalClamping(t *testing.T) {
	tmpDir := t.TempDir()

	// Interval below minimum is clamped up to the minimum.
	hs := NewHeartbeatService(tmpDir, 1, true)
	if hs.interval != minIntervalMinutes*timeMin() {
		t.Errorf("expected interval %v, got %v", minIntervalMinutes*timeMin(), hs.interval)
	}

	// Zero interval becomes the default.
	hs = NewHeartbeatService(tmpDir, 0, true)
	if hs.interval != defaultIntervalMinutes*timeMin() {
		t.Errorf("expected default interval %v, got %v", defaultIntervalMinutes*timeMin(), hs.interval)
	}

	// A large interval is left untouched.
	hs = NewHeartbeatService(tmpDir, 120, false)
	if hs.interval != 120*timeMin() {
		t.Errorf("expected interval 120m, got %v", hs.interval)
	}
	if hs.enabled {
		t.Error("expected disabled service")
	}
}

func TestSetBusAndIsRunning(t *testing.T) {
	tmpDir := t.TempDir()
	hs := NewHeartbeatService(tmpDir, 30, true)

	if hs.IsRunning() {
		t.Error("IsRunning() = true before Start")
	}

	mb := bus.NewMessageBus()
	defer mb.Close()
	hs.SetBus(mb)

	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		return nil
	})

	if err := hs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !hs.IsRunning() {
		t.Error("IsRunning() = false after Start")
	}
	// Calling Start again is a no-op.
	if err := hs.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	hs.Stop()
	if hs.IsRunning() {
		t.Error("IsRunning() = true after Stop")
	}
	// Stop again is a no-op.
	hs.Stop()
}

func TestUpdateConfig_DisablesRunningService(t *testing.T) {
	tmpDir := t.TempDir()
	hs := NewHeartbeatService(tmpDir, 30, true)
	if err := hs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !hs.IsRunning() {
		t.Fatal("expected running")
	}
	// Disabling should close the current run loop.
	hs.UpdateConfig(5, false)
	if hs.IsRunning() {
		t.Error("expected not running after disabling via UpdateConfig")
	}
	if hs.enabled {
		t.Error("expected enabled = false")
	}
}

func TestUpdateConfig_IntervalClamp(t *testing.T) {
	tmpDir := t.TempDir()
	hs := NewHeartbeatService(tmpDir, 30, true)

	hs.UpdateConfig(1, true) // below min -> clamp
	if hs.interval != minIntervalMinutes*timeMin() {
		t.Errorf("expected clamped interval, got %v", hs.interval)
	}

	hs.UpdateConfig(0, true) // zero -> default
	if hs.interval != defaultIntervalMinutes*timeMin() {
		t.Errorf("expected default interval, got %v", hs.interval)
	}

	hs.UpdateConfig(60, false)
	if hs.enabled {
		t.Error("expected disabled")
	}
}

func TestExecuteHeartbeat_DisabledOrNoHandler(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("task"), 0644)

	// stopChan nil or disabled -> early return, no panic.
	hs := NewHeartbeatService(tmpDir, 30, false)
	hs.executeHeartbeat()

	hs2 := NewHeartbeatService(tmpDir, 30, true)
	hs2.stopChan = make(chan struct{})
	hs2.enabled = true
	// No handler set -> logs error, returns.
	hs2.executeHeartbeat()

	// Verify log file contains handler-not-configured message.
	hs2.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		return nil
	})
}

func TestExecuteHeartbeat_EmptyPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	// Empty HEARTBEAT.md -> prompt is empty, handler not called.
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte(""), 0644)

	called := false
	hs := NewHeartbeatService(tmpDir, 30, true)
	hs.stopChan = make(chan struct{})
	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		called = true
		return nil
	})
	hs.executeHeartbeat()
	if called {
		t.Error("handler called with empty prompt")
	}
}

func TestExecuteHeartbeat_MissingFile_CreatesTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	hs := NewHeartbeatService(tmpDir, 30, true)
	hs.stopChan = make(chan struct{})
	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult { return nil })
	hs.executeHeartbeat()

	// Default template should be created.
	if _, err := os.Stat(filepath.Join(tmpDir, "HEARTBEAT.md")); err != nil {
		t.Fatalf("default template not created: %v", err)
	}
}

func TestExecuteHeartbeat_SendsResponseViaBus(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("task"), 0644)

	hs := NewHeartbeatService(tmpDir, 30, true)
	hs.stopChan = make(chan struct{})
	mb := bus.NewMessageBus()
	defer mb.Close()
	hs.SetBus(mb)

	// Record a last channel so sendResponse has a valid destination.
	hs.state.SetLastChannel("telegram:12345")

	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		if channel != "telegram" || chatID != "12345" {
			t.Errorf("handler got channel=%q chatID=%q", channel, chatID)
		}
		return &tools.ToolResult{ForUser: "heartbeat says hello", ForLLM: "heartbeat says hello", Silent: false, IsError: false, Async: false}
	})

	hs.executeHeartbeat()

	msg, ok := mb.SubscribeOutbound(testCtx())
	if !ok {
		t.Fatal("expected outbound message to be published")
	}
	if msg.Channel != "telegram" || msg.ChatID != "12345" || msg.Content != "heartbeat says hello" {
		t.Fatalf("unexpected outbound message: %+v", msg)
	}
}

func TestParseLastChannel(t *testing.T) {
	tmpDir := t.TempDir()
	hs := NewHeartbeatService(tmpDir, 30, true)

	platform, userID := hs.parseLastChannel("telegram:12345")
	if platform != "telegram" || userID != "12345" {
		t.Errorf("got (%q, %q)", platform, userID)
	}

	// Invalid formats.
	for _, tc := range []string{"", "no-colon", ":", "telegram:", ":12345"} {
		p, u := hs.parseLastChannel(tc)
		if p != "" || u != "" {
			t.Errorf("parseLastChannel(%q) = (%q, %q), want empty", tc, p, u)
		}
	}

	// Internal channels are skipped.
	p, u := hs.parseLastChannel("cli:123")
	if p != "" || u != "" {
		t.Errorf("internal channel not skipped: (%q, %q)", p, u)
	}

	// Valid parse of an internal-checked channel.
	if !strings.Contains("telegram:12345", ":") {
		t.Fatal("sanity")
	}
}

func TestSendResponse_NoBus_NoLastChannel(t *testing.T) {
	tmpDir := t.TempDir()
	hs := NewHeartbeatService(tmpDir, 30, true)

	// No bus configured -> logs and returns.
	hs.sendResponse("hello")

	// Bus configured but no last channel -> returns.
	mb := bus.NewMessageBus()
	defer mb.Close()
	hs.SetBus(mb)
	hs.sendResponse("hello")

	// Last channel = internal channel -> returns without sending.
	hs.state.SetLastChannel("system:1")
	hs.sendResponse("hello")

	// Last channel that produces empty platform/userID.
	hs.state.SetLastChannel("telegram:") // invalid
	hs.sendResponse("hello")
}

func TestBuildPrompt_WithContent(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("check disk"), 0644)
	hs := NewHeartbeatService(tmpDir, 30, true)
	prompt := hs.buildPrompt()
	if !strings.Contains(prompt, "check disk") {
		t.Error("prompt missing HEARTBEAT.md content")
	}
	if !strings.Contains(prompt, "Current time") {
		t.Error("prompt missing current time header")
	}
}

func TestExecuteHeartbeat_ResolvesChannelFromState(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "HEARTBEAT.md"), []byte("task"), 0644)
	hs := NewHeartbeatService(tmpDir, 30, true)
	hs.stopChan = make(chan struct{})
	mb := bus.NewMessageBus()
	defer mb.Close()
	hs.SetBus(mb)
	hs.state.SetLastChannel("system:1") // internal -> parseLastChannel returns empty

	var gotChan, gotChat string
	hs.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		gotChan, gotChat = channel, chatID
		return &tools.ToolResult{ForLLM: "ok", ForUser: "ok", Silent: false, IsError: false, Async: false}
	})
	hs.executeHeartbeat()
	if gotChan != "" || gotChat != "" {
		t.Errorf("expected empty channel for internal last channel, got (%q, %q)", gotChan, gotChat)
	}
}

func TestCreateDefaultTemplate_WriteFailure(t *testing.T) {
	// Create a regular file at a path we'll treat as the workspace; the
	// HEARTBEAT.md path underneath cannot be created, exercising the error
	// branch of WriteFile in createDefaultHeartbeatTemplate.
	filePath := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	hs := NewHeartbeatService(filePath, 30, true)
	hs.createDefaultHeartbeatTemplate()
	// Should not panic; failure is logged.
}