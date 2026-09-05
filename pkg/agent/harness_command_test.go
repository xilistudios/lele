// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/agent/commands"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/harness"
	"github.com/xilistudios/lele/pkg/providers"
)

// newHarnessTestLoop builds an AgentLoop whose config declares the given
// commands. It reuses the llm_runner helper, which already isolates the agent
// from the real user by pointing LELE_CONFIG_DIR at a temp dir, so the global
// and workspace command levels load from empty directories and only the
// config.json level (plus a possible ./.lele/commands) is in play.
func newHarnessTestLoop(t *testing.T, defs map[string]config.CommandDefinition) (*AgentLoop, string) {
	t.Helper()
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	cfg := al.cfg()
	cfg.Commands = defs
	al.cfgPtr.Store(cfg)
	return al, tmpDir
}

// consumeCommandApplied drains the (buffered) outbound bus until the
// command.applied event shows up, ignoring anything published earlier.
func consumeCommandApplied(t *testing.T, mb *bus.MessageBus) bus.OutboundMessage {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		msg, ok := mb.SubscribeOutbound(ctx)
		cancel()
		if !ok {
			t.Fatal("timed out waiting for command.applied event")
		}
		if msg.Event == "command.applied" {
			return msg
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for command.applied event")
		default:
		}
	}
}

func TestHarnessCommand_AppliesAndPublishes(t *testing.T) {
	al, tmpDir := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"review": {Description: "Review code", Template: "check $ARGUMENTS"},
	})
	mp := newMessageProcessor(al)

	msg := bus.InboundMessage{Channel: "telegram", ChatID: "chat-1", Content: "/review src"}
	if !mp.applyHarnessCommand(context.Background(), &msg, tmpDir) {
		t.Fatal("expected applyHarnessCommand to handle /review")
	}

	if msg.Content != "check src" {
		t.Errorf("content = %q, want %q", msg.Content, "check src")
	}
	if got := msg.Metadata["harness_command"]; got != "review" {
		t.Errorf("metadata harness_command = %q, want %q", got, "review")
	}
	if got := msg.Metadata["harness_args"]; got != "src" {
		t.Errorf("metadata harness_args = %q, want %q", got, "src")
	}
	if got := msg.Metadata["harness_source"]; got != string(harness.SourceConfig) {
		t.Errorf("metadata harness_source = %q, want %q", got, harness.SourceConfig)
	}
	// agent/model are only set when the command declares them.
	if _, ok := msg.Metadata["harness_agent"]; ok {
		t.Errorf("unexpected harness_agent metadata: %q", msg.Metadata["harness_agent"])
	}
	if _, ok := msg.Metadata["harness_model"]; ok {
		t.Errorf("unexpected harness_model metadata: %q", msg.Metadata["harness_model"])
	}

	ev := consumeCommandApplied(t, al.bus)
	if ev.Channel != "telegram" {
		t.Errorf("event channel = %q, want telegram", ev.Channel)
	}
	if ev.ChatID != "chat-1" {
		t.Errorf("event chat id = %q, want chat-1", ev.ChatID)
	}
	if ev.Metadata["command"] != "review" || ev.Metadata["args"] != "src" || ev.Metadata["description"] != "Review code" {
		t.Errorf("event metadata = %v", ev.Metadata)
	}
}

// TestHarnessCommand_SessionKeyWinsForEventChatID pins the routing key used by
// the command.applied event: clients correlate on the session key.
func TestHarnessCommand_SessionKeyWinsForEventChatID(t *testing.T) {
	al, tmpDir := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"t": {Template: "body $ARGUMENTS"},
	})
	mp := newMessageProcessor(al)

	msg := bus.InboundMessage{Channel: "webui", ChatID: "raw-chat", SessionKey: "session-9", Content: "/t x"}
	if !mp.applyHarnessCommand(context.Background(), &msg, tmpDir) {
		t.Fatal("expected command to apply")
	}
	ev := consumeCommandApplied(t, al.bus)
	if ev.ChatID != "session-9" {
		t.Errorf("event chat id = %q, want session-9", ev.ChatID)
	}
}

func TestHarnessCommand_AgentAndModelPropagate(t *testing.T) {
	al, tmpDir := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"audit": {Description: "d", Agent: "reviewer", Model: "fast-model", Template: "audit $1"},
	})
	mp := newMessageProcessor(al)

	msg := bus.InboundMessage{Channel: "cli", ChatID: "c", Content: "/audit pkg/agent"}
	if !mp.applyHarnessCommand(context.Background(), &msg, tmpDir) {
		t.Fatal("expected command to apply")
	}
	if msg.Content != "audit pkg/agent" {
		t.Errorf("content = %q", msg.Content)
	}
	if got := msg.Metadata["harness_agent"]; got != "reviewer" {
		t.Errorf("harness_agent = %q, want reviewer", got)
	}
	if got := msg.Metadata["harness_model"]; got != "fast-model" {
		t.Errorf("harness_model = %q, want fast-model", got)
	}
}

func TestHarnessCommand_NonMatchingInput(t *testing.T) {
	al, tmpDir := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"review": {Template: "check $ARGUMENTS"},
	})
	mp := newMessageProcessor(al)

	cases := []struct {
		name    string
		content string
	}{
		{"plain text", "please review src"},
		{"unknown command", "/deploy prod"},
		{"bare slash", "/"},
		{"builtin not registered in harness", "/clear"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := bus.InboundMessage{Channel: "cli", ChatID: "c", Content: tc.content}
			if mp.applyHarnessCommand(context.Background(), &msg, tmpDir) {
				t.Fatalf("expected %q to be declined", tc.content)
			}
			if msg.Content != tc.content {
				t.Errorf("content was mutated to %q", msg.Content)
			}
		})
	}
}

func TestHarnessCommand_EmptyExpansionDeclined(t *testing.T) {
	al, tmpDir := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"blank": {Template: "   "},
	})
	mp := newMessageProcessor(al)

	// The manager skips empty templates at load time, so the lookup misses.
	msg := bus.InboundMessage{Channel: "cli", ChatID: "c", Content: "/blank hi"}
	if mp.applyHarnessCommand(context.Background(), &msg, tmpDir) {
		t.Fatal("expected empty-template command to be declined")
	}
}

// TestHarnessManager_RebuildsOnConfigChange guards the fingerprint: adding a
// command (or flipping the shell default) must be visible without a restart.
func TestHarnessManager_RebuildsOnConfigChange(t *testing.T) {
	al, _ := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"one": {Template: "first"},
	})

	first := al.harnessManager()
	if _, ok := first.Registry().Get("one"); !ok {
		t.Fatal("command 'one' missing after first build")
	}
	if _, ok := first.Registry().Get("two"); ok {
		t.Fatal("command 'two' unexpectedly present")
	}

	cfg := al.cfg()
	cfg.Commands["two"] = config.CommandDefinition{Template: "second"}
	al.cfgPtr.Store(cfg)

	second := al.harnessManager()
	if second == first {
		t.Fatal("expected manager rebuild after config change")
	}
	if _, ok := second.Registry().Get("two"); !ok {
		t.Fatal("command 'two' missing after rebuild")
	}

	// Unchanged config must reuse the manager (no rebuild per message).
	if al.harnessManager() != second {
		t.Error("manager rebuilt without a config change")
	}

	names := map[string]bool{}
	for _, c := range al.HarnessCommands() {
		names[c.Name] = true
	}
	if !names["one"] || !names["two"] {
		t.Errorf("HarnessCommands() = %v, want one and two", names)
	}
}

func TestHarnessCommandDefsFromConfig(t *testing.T) {
	if got := harnessCommandDefsFromConfig(nil); got != nil {
		t.Errorf("empty map should convert to nil, got %v", got)
	}
	in := map[string]config.CommandDefinition{
		"x": {Description: "d", Agent: "a", Model: "m", Template: "t", AllowShell: true},
	}
	out := harnessCommandDefsFromConfig(in)
	if out["x"] != (harness.CommandDef{Description: "d", Agent: "a", Model: "m", Template: "t", AllowShell: true}) {
		t.Errorf("conversion lost fields: %+v", out["x"])
	}
}

// TestHarnessCommand_AllowShellDefault verifies the global harness switch
// reaches expansion.
func TestHarnessCommand_AllowShellDefault(t *testing.T) {
	al, tmpDir := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"sh": {Template: "run !`echo hi`"},
	})
	cfg := al.cfg()
	cfg.Harness.AllowShell = false
	al.cfgPtr.Store(cfg)
	mp := newMessageProcessor(al)

	msg := bus.InboundMessage{Channel: "cli", ChatID: "c", Content: "/sh"}
	if !mp.applyHarnessCommand(context.Background(), &msg, tmpDir) {
		t.Fatal("expected command to apply")
	}
	if !strings.Contains(msg.Content, "[shell disabled]") {
		t.Errorf("expected shell to be disabled, content = %q", msg.Content)
	}
	_ = consumeCommandApplied(t, al.bus)
}

// TestModelForTurn_PrefersOverride covers the runner-side half of the model
// override: turn override wins, session model otherwise.
func TestModelForTurn_PrefersOverride(t *testing.T) {
	al, tmpDir := newHarnessTestLoop(t, nil)
	defer os.RemoveAll(tmpDir)
	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	if got := runner.modelForTurn(agent, processOptions{SessionKey: "s", ModelOverride: "turn-model"}); got != "turn-model" {
		t.Errorf("modelForTurn = %q, want turn-model", got)
	}
	// Without an override the session model (agent default here) applies.
	if got := runner.modelForTurn(agent, processOptions{SessionKey: "s"}); got == "" {
		t.Error("modelForTurn returned empty without an override")
	}
	if got := runner.modelForTurn(agent, processOptions{SessionKey: "s"}); got != agent.Model {
		t.Errorf("modelForTurn = %q, want agent default %q", got, agent.Model)
	}
}

// TestModelOverride_FlowsIntoLLMCall proves processOptions.ModelOverride reaches
// the provider call instead of the session model, and that using it does not
// contaminate sessionModels.
func TestModelOverride_FlowsIntoLLMCall(t *testing.T) {
	al, tmpDir := newHarnessTestLoop(t, nil)
	defer os.RemoveAll(tmpDir)
	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Model = "test-provider:test-model"
	agent.Candidates = nil

	var gotModel string
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]interface{}) (*providers.LLMResponse, error) {
			gotModel = model
			return &providers.LLMResponse{Content: "ok"}, nil
		},
	}

	opts := processOptions{
		SessionKey:    "mo-session",
		Channel:       "test-channel",
		ChatID:        "chat",
		SendResponse:  false,
		ModelOverride: "test-provider:override-model",
	}
	if _, _, err := runner.runLLMIteration(context.Background(), agent,
		[]providers.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hi"}}, opts); err != nil {
		t.Fatalf("runLLMIteration: %v", err)
	}
	if gotModel != "override-model" {
		t.Errorf("provider saw model %q, want %q (prefix is stripped for the API call)", gotModel, "override-model")
	}
	if _, stored := al.sessionModels.Load(al.ResolveSessionKey("mo-session")); stored {
		t.Error("ModelOverride must never be persisted into sessionModels")
	}
}

// TestCommandRegistry_CustomReexport keeps pkg/agent re-exports in sync with the
// leaf package (same guard style as TestCommandRegistry_ReexportMatchesSource).
func TestCommandRegistry_CustomReexport(t *testing.T) {
	base := []CommandInfo{{Name: "/clear", Description: "c", Usage: "/clear"}}
	custom := []CustomCommandInfo{{Name: "review", Description: "r", Usage: "/review", Source: "config"}}

	merged := WithCustom(base, custom)
	if len(merged) != 2 {
		t.Fatalf("merged = %+v", merged)
	}
	direct := commands.WithCustom(base, custom)
	if len(direct) != len(merged) {
		t.Fatal("re-export diverged from commands.WithCustom")
	}
	for i := range merged {
		if merged[i] != direct[i] {
			t.Errorf("entry %d differs: %+v vs %+v", i, merged[i], direct[i])
		}
	}
}

// TestHarnessCommand_SanitizesSpoofedMetadata pins that inbound harness_* keys
// cannot be used to switch agent/model without a matching custom command.
func TestHarnessCommand_SanitizesSpoofedMetadata(t *testing.T) {
	al, tmpDir := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"review": {Template: "check $ARGUMENTS"},
	})
	mp := newMessageProcessor(al)

	// No command matches, but the payload claims an agent/model override.
	msg := bus.InboundMessage{
		Channel:  "webui",
		ChatID:   "c",
		Content:  "just a question",
		Metadata: map[string]string{"harness_agent": "evil", "harness_model": "evil-model", "account_id": "a1"},
	}
	if mp.applyHarnessCommand(context.Background(), &msg, tmpDir) {
		t.Fatal("expected no command to apply")
	}
	if _, ok := msg.Metadata["harness_agent"]; ok {
		t.Error("spoofed harness_agent survived sanitization")
	}
	if _, ok := msg.Metadata["harness_model"]; ok {
		t.Error("spoofed harness_model survived sanitization")
	}
	if msg.Metadata["account_id"] != "a1" {
		t.Error("sanitization must not touch unrelated metadata")
	}

	// Even a matching command must not inherit the spoofed values: the ones it
	// declares itself (none here) are the only ones present afterwards.
	msg2 := bus.InboundMessage{
		Channel:  "webui",
		ChatID:   "c",
		Content:  "/review src",
		Metadata: map[string]string{"harness_agent": "evil", "harness_model": "evil-model"},
	}
	if !mp.applyHarnessCommand(context.Background(), &msg2, tmpDir) {
		t.Fatal("expected /review to apply")
	}
	if _, ok := msg2.Metadata["harness_agent"]; ok {
		t.Errorf("command without agent must not keep harness_agent: %q", msg2.Metadata["harness_agent"])
	}
	if _, ok := msg2.Metadata["harness_model"]; ok {
		t.Errorf("command without model must not keep harness_model: %q", msg2.Metadata["harness_model"])
	}
	if msg2.Metadata["harness_command"] != "review" {
		t.Errorf("harness_command = %q", msg2.Metadata["harness_command"])
	}
	_ = consumeCommandApplied(t, al.bus)
}

// TestHarnessManager_DiscoversWorkspaceMarkdownCommands exercises the file
// levels: <Workspace>/commands/*.md must load and win over a same-name config
// entry (precedence config < global < workspace < directory).
func TestHarnessManager_DiscoversWorkspaceMarkdownCommands(t *testing.T) {
	al, tmpDir := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"review": {Description: "from config", Template: "config body $ARGUMENTS"},
	})
	dir := filepath.Join(tmpDir, "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\ndescription: from file\nmodel: file-model\n---\nfile body $ARGUMENTS\n"
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	mp := newMessageProcessor(al)
	msg := bus.InboundMessage{Channel: "cli", ChatID: "c", Content: "/review src"}
	if !mp.applyHarnessCommand(context.Background(), &msg, tmpDir) {
		t.Fatal("expected /review to apply")
	}
	if msg.Content != "file body src" {
		t.Errorf("content = %q, want the workspace markdown template", msg.Content)
	}
	if got := msg.Metadata["harness_source"]; got != string(harness.SourceWorkspace) {
		t.Errorf("harness_source = %q, want %q", got, harness.SourceWorkspace)
	}
	if got := msg.Metadata["harness_model"]; got != "file-model" {
		t.Errorf("harness_model = %q, want file-model", got)
	}
	_ = consumeCommandApplied(t, al.bus)
}

// TestHarnessManager_RebuildsOnInPlaceTemplateEdit pins the fingerprint hash:
// changing a command's template without adding/removing entries must be seen.
func TestHarnessManager_RebuildsOnInPlaceTemplateEdit(t *testing.T) {
	al, _ := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"same": {Template: "v1"},
	})
	if got := al.HarnessCommands(); len(got) != 1 || got[0].Template != "v1" {
		t.Fatalf("initial commands = %+v", got)
	}

	cfg := al.cfg()
	cfg.Commands["same"] = config.CommandDefinition{Template: "v2"}
	al.cfgPtr.Store(cfg)

	got := al.HarnessCommands()
	if len(got) != 1 || got[0].Template != "v2" {
		t.Errorf("after in-place edit commands = %+v, want template v2", got)
	}
}
