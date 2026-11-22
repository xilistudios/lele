package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestGetHistoryMessageCount_FrameSnapshot(t *testing.T) {
	m := newTestModel(t)

	key := "tui:chat:count-cache-test"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.AddMessage(key, "user", "hello")
	m.sessionMgr.AddMessage(key, "assistant", "hi there")
	m.sessionMgr.AddMessage(key, "tool", "tool output")
	m.currentKey = key

	if got := m.getHistoryMessageCount(); got != 2 {
		t.Fatalf("getHistoryMessageCount() = %d, want 2", got)
	}
	if m.frameKey != key || m.frameCount != 2 {
		t.Fatalf("frame snapshot not populated: key=%q count=%d", m.frameKey, m.frameCount)
	}

	m.beginRenderFrame()
	reads := 0
	m.onHistoryRead = func(string) { reads++ }
	if got := m.getHistoryMessageCount(); got != 2 {
		t.Fatalf("in-frame getHistoryMessageCount() = %d, want 2", got)
	}
	if got := m.getHistoryMessageCount(); got != 2 {
		t.Fatalf("second in-frame call = %d, want 2", got)
	}
	if reads != 1 {
		t.Fatalf("in-frame history reads = %d, want 1", reads)
	}
	m.endRenderFrame()
	m.onHistoryRead = nil

	m.sessionMgr.AddMessage(key, "user", "second question")
	m.beginRenderFrame()
	if got := m.getHistoryMessageCount(); got != 3 {
		t.Fatalf("after adding message: got %d, want 3", got)
	}
	m.endRenderFrame()
	if m.frameCount != 3 {
		t.Fatalf("frame count not refreshed: %d", m.frameCount)
	}

	otherKey := "tui:chat:count-cache-other"
	m.sessionMgr.GetOrCreate(otherKey)
	m.sessionMgr.AddMessage(otherKey, "user", "only message")
	m.currentKey = otherKey
	if got := m.getHistoryMessageCount(); got != 1 {
		t.Fatalf("after session switch: got %d, want 1", got)
	}
}

func TestBouncingDots_NoPerFrameStyleAllocation(t *testing.T) {
	m := newTestModel(t)

	first := m.getBouncingDots()
	if first == "" {
		t.Fatal("getBouncingDots() returned empty string")
	}

	for i := 0; i < 20; i++ {
		m.animationTick++
		got := m.getBouncingDots()
		if len([]rune(got)) != len([]rune(first)) {
			t.Fatalf("animation width changed at tick %d: %q vs %q", m.animationTick, got, first)
		}
	}

	if !strings.Contains(first, bouncingDotChar) {
		t.Fatal("getBouncingDots output does not use the pre-rendered bouncingDotChar")
	}
}

func TestMessageFingerprint_Stable(t *testing.T) {
	msg1 := providers.Message{Role: "user", Content: "hello world"}
	msg2 := providers.Message{Role: "user", Content: "hello world"}
	msg3 := providers.Message{Role: "user", Content: "different content"}

	fp1 := messageFingerprint(msg1, 80)
	fp2 := messageFingerprint(msg2, 80)
	fp3 := messageFingerprint(msg3, 80)

	if fp1 != fp2 {
		t.Fatalf("same message produced different fingerprints: %q vs %q", fp1, fp2)
	}
	if fp1 == fp3 {
		t.Fatalf("different messages produced same fingerprint: %q", fp1)
	}
}

func TestMessageFingerprint_WidthSensitive(t *testing.T) {
	msg := providers.Message{Role: "assistant", Content: "some response"}

	fp80 := messageFingerprint(msg, 80)
	fp120 := messageFingerprint(msg, 120)

	if fp80 == fp120 {
		t.Fatalf("different widths produced same fingerprint: %q", fp80)
	}
}

func TestMessageFingerprint_RoleSensitive(t *testing.T) {
	user := providers.Message{Role: "user", Content: "same content"}
	assistant := providers.Message{Role: "assistant", Content: "same content"}

	fpUser := messageFingerprint(user, 80)
	fpAssistant := messageFingerprint(assistant, 80)

	if fpUser == fpAssistant {
		t.Fatalf("different roles produced same fingerprint: %q", fpUser)
	}
}

func TestMessageFingerprint_ToolCallsSensitive(t *testing.T) {
	msg1 := providers.Message{
		Role:    "assistant",
		Content: "I'll run that command",
		ToolCalls: []providers.ToolCall{
			{Name: "exec", Function: &providers.FunctionCall{Name: "exec", Arguments: `{"command":"ls"}`}},
		},
	}
	msg2 := providers.Message{
		Role:    "assistant",
		Content: "I'll run that command",
		ToolCalls: []providers.ToolCall{
			{Name: "exec", Function: &providers.FunctionCall{Name: "exec", Arguments: `{"command":"pwd"}`}},
		},
	}

	fp1 := messageFingerprint(msg1, 80)
	fp2 := messageFingerprint(msg2, 80)

	if fp1 == fp2 {
		t.Fatalf("different tool calls produced same fingerprint: %q", fp1)
	}
}

func TestCountHistoryMessages(t *testing.T) {
	history := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "tool", Content: "tool output"},
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "answer"},
	}

	if got := countHistoryMessages(history); got != 4 {
		t.Fatalf("countHistoryMessages() = %d, want 4", got)
	}

	if got := countHistoryMessages(nil); got != 0 {
		t.Fatalf("countHistoryMessages(nil) = %d, want 0", got)
	}
}

func TestLastHistoryRoleFromHistory(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "system", Content: "compaction summary"},
	}

	if got := lastHistoryRoleFromHistory(history); got != "assistant" {
		t.Fatalf("lastHistoryRoleFromHistory() = %q, want %q", got, "assistant")
	}

	sysOnly := []providers.Message{{Role: "system", Content: "prompt"}}
	if got := lastHistoryRoleFromHistory(sysOnly); got != "" {
		t.Fatalf("system-only: got %q, want empty", got)
	}
}
