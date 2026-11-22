package agent

import (
	"context"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
)

func newInProgressToolTestLoop(t *testing.T) *AgentLoop {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{
			Anthropic: config.ProviderConfig{APIKey: "test-key"},
		},
	}

	return NewAgentLoop(cfg, bus.NewMessageBus())
}

func toolExecutingMsg(key, tool, action, args, callID string) bus.OutboundMessage {
	return bus.OutboundMessage{
		Channel: channels.ChannelName,
		ChatID:  key,
		Event:   "tool.executing",
		Metadata: map[string]string{
			"tool":         tool,
			"action":       action,
			"arguments":    args,
			"tool_call_id": callID,
		},
	}
}

func toolResultMsg(key, tool, callID string) bus.OutboundMessage {
	return bus.OutboundMessage{
		Channel: channels.ChannelName,
		ChatID:  key,
		Event:   "tool.result",
		Metadata: map[string]string{
			"tool":         tool,
			"tool_call_id": callID,
		},
	}
}

func TestInProgressTool_Lifecycle(t *testing.T) {
	al := newInProgressToolTestLoop(t)
	key := "native:in-progress"

	if tool := al.GetInProgressTool(key); tool != nil {
		t.Fatalf("expected no tool before any event, got %#v", tool)
	}

	al.publishToolLifecycle(toolExecutingMsg(key, "sleep", "sleep: 600s", `{"seconds":600}`, "call_1"))

	tool := al.GetInProgressTool(key)
	if tool == nil {
		t.Fatal("tool.executing did not record the in-flight tool")
	}
	if tool.Tool != "sleep" || tool.Action != "sleep: 600s" {
		t.Fatalf("recorded %#v, want sleep/sleep: 600s", tool)
	}
	if tool.Arguments != `{"seconds":600}` || tool.ToolCallID != "call_1" {
		t.Fatalf("recorded args/call id %#v", tool)
	}
	if tool.StartedAt.IsZero() {
		t.Fatal("StartedAt was not set")
	}

	al.publishToolLifecycle(toolResultMsg(key, "sleep", "call_1"))

	if tool := al.GetInProgressTool(key); tool != nil {
		t.Fatalf("matching tool.result did not clear the record: %#v", tool)
	}
}

func TestInProgressTool_StaleResultDoesNotClear(t *testing.T) {
	al := newInProgressToolTestLoop(t)
	key := "native:stale-result"

	al.publishToolLifecycle(toolExecutingMsg(key, "wait_for_subagent", "wait_for_subagent: subagent-1", "", "call_wait"))

	al.publishToolLifecycle(toolResultMsg(key, "exec", "call_other"))
	if al.GetInProgressTool(key) == nil {
		t.Fatal("a result for another call cleared the in-flight tool")
	}

	al.publishToolLifecycle(toolResultMsg(key, "exec", "call_wait"))
	if al.GetInProgressTool(key) == nil {
		t.Fatal("a result with a mismatching tool name cleared the in-flight tool")
	}

	al.publishToolLifecycle(toolResultMsg(key, "wait_for_subagent", "call_wait"))
	if al.GetInProgressTool(key) != nil {
		t.Fatal("the matching result did not clear the in-flight tool")
	}
}

func TestInProgressTool_IdLessResultFallsBackToName(t *testing.T) {
	al := newInProgressToolTestLoop(t)
	key := "native:id-less"

	al.publishToolLifecycle(toolExecutingMsg(key, "compact", "Compacting context...", "", ""))

	al.publishToolLifecycle(bus.OutboundMessage{
		Channel:  channels.ChannelName,
		ChatID:   key,
		Event:    "tool.result",
		Metadata: map[string]string{"tool": "compact"},
	})

	if al.GetInProgressTool(key) != nil {
		t.Fatal("id-less result for the same tool did not clear the record")
	}
}

func TestInProgressTool_OnlyNativeChannelTracked(t *testing.T) {
	al := newInProgressToolTestLoop(t)

	msg := toolExecutingMsg("telegram:42", "exec", "exec: ls", `{"command":"ls"}`, "call_tg")
	msg.Channel = "telegram"
	al.publishToolLifecycle(msg)

	if tool := al.GetInProgressTool("telegram:42"); tool != nil {
		t.Fatalf("non-native channel recorded a tool: %#v", tool)
	}
}

func TestInProgressTool_ResolvesSessionAlias(t *testing.T) {
	al := newInProgressToolTestLoop(t)
	base := "native:alias-base"
	active := base + ":chat:2"
	al.setSessionAlias(base, active)

	al.publishToolLifecycle(toolExecutingMsg(active, "sleep", "sleep: 10s", "", "call_alias"))

	if tool := al.GetInProgressTool(active); tool == nil {
		t.Fatal("active key did not resolve the in-flight tool")
	}
	if tool := al.GetInProgressTool(base); tool == nil {
		t.Fatal("base key did not resolve the aliased in-flight tool")
	}
}

func TestInProgressTool_ClearedWhenTurnEnds(t *testing.T) {
	al := newInProgressToolTestLoop(t)
	key := "native:turn-end"

	_, cancel := context.WithCancel(context.Background())
	unregister := al.sessionManager.RegisterSessionCancel(key, cancel)

	al.publishToolLifecycle(toolExecutingMsg(key, "sleep", "sleep: 600s", "", "call_slow"))
	if al.GetInProgressTool(key) == nil {
		t.Fatal("tool.executing did not record the in-flight tool")
	}

	unregister()

	if tool := al.GetInProgressTool(key); tool != nil {
		t.Fatalf("ending the turn left a stale in-flight tool: %#v", tool)
	}
}

func TestInProgressTool_CancelSessionClears(t *testing.T) {
	al := newInProgressToolTestLoop(t)
	key := "native:cancelled"

	_, cancel := context.WithCancel(context.Background())
	al.sessionManager.RegisterSessionCancel(key, cancel)

	al.publishToolLifecycle(toolExecutingMsg(key, "wait_for_subagent", "waiting", "", "call_cancel"))
	if al.GetInProgressTool(key) == nil {
		t.Fatal("tool.executing did not record the in-flight tool")
	}

	if stopped := al.cancelSession(key); stopped == 0 {
		t.Fatal("cancelSession stopped nothing")
	}

	if tool := al.GetInProgressTool(key); tool != nil {
		t.Fatalf("cancelSession left a stale in-flight tool: %#v", tool)
	}
}

func TestInProgressTool_GetReturnsCopy(t *testing.T) {
	al := newInProgressToolTestLoop(t)
	key := "native:copy"

	al.publishToolLifecycle(toolExecutingMsg(key, "exec", "exec: ls", "", "call_copy"))

	first := al.GetInProgressTool(key)
	second := al.GetInProgressTool(key)
	if first == nil || second == nil {
		t.Fatal("expected a recorded tool")
	}
	if first == second {
		t.Fatal("GetInProgressTool returned the same pointer twice")
	}
	first.Tool = "mutated"
	if third := al.GetInProgressTool(key); third.Tool != "exec" {
		t.Fatalf("mutating the returned record leaked into the store: %q", third.Tool)
	}
}
