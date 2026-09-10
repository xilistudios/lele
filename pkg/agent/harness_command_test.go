// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestHarnessManager_ConcurrentRebuild hammers harnessManager() from several
// goroutines while the config fingerprint changes underneath (simulating a
// hot-reload racing message processing). Guards the lock ordering that makes
// the lazy rebuild safe: harnessMu serializes rebuilds, the returned Manager
// is always fully loaded, and Registry pointers stay stable per Manager. Run
// under -race in CI.
func TestHarnessManager_ConcurrentRebuild(t *testing.T) {
	al, _ := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"base": {Template: "b"},
	})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				mgr := al.harnessManager()
				if mgr == nil || mgr.Registry() == nil {
					t.Error("harnessManager returned nil manager/registry")
					return
				}
			}
		}()
	}
	// Mutate the config (fingerprint changes -> rebuilds) while readers run.
	// Each iteration publishes a NEW Config: a real hot-reload replaces the
	// whole struct, and mutating the shared map in place would be a test-only
	// race. The clone goes through JSON because Config embeds a RWMutex (vet
	// forbids copying it with *cfg).
	for n := 1; n <= 30; n++ {
		raw, err := json.Marshal(al.cfg())
		if err != nil {
			t.Fatalf("marshal config: %v", err)
		}
		mutated := &config.Config{}
		if err := json.Unmarshal(raw, mutated); err != nil {
			t.Fatalf("unmarshal config: %v", err)
		}
		next := make(map[string]config.CommandDefinition, len(mutated.Commands)+1)
		for k, v := range mutated.Commands {
			next[k] = v
		}
		next[fmt.Sprintf("cmd%d", n)] = config.CommandDefinition{Template: "t"}
		mutated.Commands = next
		al.cfgPtr.Store(mutated)
	}
	close(stop)
	wg.Wait()

	// Final state must contain every command published so far.
	names := map[string]bool{}
	for _, c := range al.HarnessCommands() {
		names[c.Name] = true
	}
	if !names["base"] {
		t.Error("base command lost after concurrent rebuilds")
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

// TestHarnessCommand_AbsoluteFileOptIn pins the security default: @/abs/path
// inlining is blocked unless the harness default or the command tri-state
// enables it, and an explicit tri-state false vetoes a global true.
func TestHarnessCommand_AbsoluteFileOptIn(t *testing.T) {
	yes, no := true, false
	secret := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE-KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := "@" + secret

	// Default off: blocked placeholder, content never appears.
	al, tmpDir := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"peek": {Template: "see " + ref},
	})
	mp := newMessageProcessor(al)
	msg := bus.InboundMessage{Channel: "cli", ChatID: "c", Content: "/peek"}
	if !mp.applyHarnessCommand(context.Background(), &msg, tmpDir) {
		t.Fatal("expected command to apply")
	}
	if !strings.Contains(msg.Content, "[blocked: absolute path]") || strings.Contains(msg.Content, "PRIVATE-KEY") {
		t.Errorf("default must block absolute refs, content = %q", msg.Content)
	}
	_ = consumeCommandApplied(t, al.bus)

	// Harness-wide default on: inlined.
	cfg := al.cfg()
	cfg.Harness.AllowAbsoluteFiles = true
	al.cfgPtr.Store(cfg)
	msg = bus.InboundMessage{Channel: "cli", ChatID: "c", Content: "/peek"}
	if !mp.applyHarnessCommand(context.Background(), &msg, tmpDir) {
		t.Fatal("expected command to apply (default on)")
	}
	if !strings.Contains(msg.Content, "PRIVATE-KEY") {
		t.Errorf("default on must inline, content = %q", msg.Content)
	}
	_ = consumeCommandApplied(t, al.bus)

	// Tri-state false vetoes the global true (the deliberate difference from
	// allow_shell's OR merge).
	cfg = al.cfg()
	cfg.Commands["peek"] = config.CommandDefinition{Template: "see " + ref, AllowAbsoluteFiles: &no}
	al.cfgPtr.Store(cfg)
	msg = bus.InboundMessage{Channel: "cli", ChatID: "c", Content: "/peek"}
	if !mp.applyHarnessCommand(context.Background(), &msg, tmpDir) {
		t.Fatal("expected command to apply (tri-state false)")
	}
	if !strings.Contains(msg.Content, "[blocked: absolute path]") {
		t.Errorf("explicit false must veto default true, content = %q", msg.Content)
	}
	_ = consumeCommandApplied(t, al.bus)

	// And with the global default off, tri-state true opts in.
	cfg = al.cfg()
	cfg.Harness.AllowAbsoluteFiles = false
	cfg.Commands["peek"] = config.CommandDefinition{Template: "see " + ref, AllowAbsoluteFiles: &yes}
	al.cfgPtr.Store(cfg)
	msg = bus.InboundMessage{Channel: "cli", ChatID: "c", Content: "/peek"}
	if !mp.applyHarnessCommand(context.Background(), &msg, tmpDir) {
		t.Fatal("expected command to apply (tri-state true)")
	}
	if !strings.Contains(msg.Content, "PRIVATE-KEY") {
		t.Errorf("explicit true must opt in despite default off, content = %q", msg.Content)
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

// TestAgentProvidable_ExposesHarnessCommands is the structural guard for the
// wiring gap this pins down: channels.NewManager receives
// AgentLoop.GetProvidable() — the agentProvidableImpl wrapper — NOT the loop,
// and it discovers custom commands through an optional interface assertion
// (channels.customCommandProvider). If the wrapper ever stops exposing
// HarnessCommands, the assertion fails silently and the WebUI palette shows
// built-ins only, while the TUI (which holds *AgentLoop) keeps working. That
// asymmetry is exactly what a test must catch, so it is asserted here against
// the same interface shape channels/rest_commands.go declares.
func TestAgentProvidable_ExposesHarnessCommands(t *testing.T) {
	// Same shape as channels.customCommandProvider (pkg/channels cannot import
	// pkg/agent, so the declaration is duplicated by design).
	type harnessProvider interface {
		HarnessCommands() []*harness.Command
	}

	al, _ := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"review": {Description: "Review code", Template: "check $ARGUMENTS"},
	})

	provider, ok := interface{}(al.GetProvidable()).(harnessProvider)
	if !ok {
		t.Fatal("AgentLoop.GetProvidable() does not expose HarnessCommands(): " +
			"channels.customCommandProvider would never match in production")
	}

	got := provider.HarnessCommands()
	want := al.HarnessCommands()
	if len(got) != len(want) {
		t.Fatalf("providable returned %d commands, loop returned %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("command %d differs: providable=%+v loop=%+v", i, got[i], want[i])
		}
	}
}

// TestHarnessFingerprintTriStateByValue guards D4: the fingerprint must
// depend on the VALUE of the per-command *bool, not on the pointer address.
// Re-parsing the same config allocates new pointers; if the hash used %v the
// fingerprint would change on every re-parse and trigger spurious rebuilds.
func TestHarnessFingerprintTriStateByValue(t *testing.T) {
	yes1, yes2 := true, true
	no1 := false
	defs := func(p *bool) map[string]harness.CommandDef {
		return map[string]harness.CommandDef{"a": {Template: "t", AllowAbsoluteFiles: p}}
	}
	fp := func(p *bool) string {
		return harnessFingerprint(false, false, defs(p), "/ws", "/lele", "")
	}

	if fp(nil) == fp(&no1) {
		t.Error("nil and explicit false must fingerprint differently (tri-state semantics)")
	}
	if fp(&yes1) == fp(nil) || fp(&yes1) == fp(&no1) {
		t.Error("true must fingerprint differently from nil and false")
	}
	if fp(&yes1) != fp(&yes2) {
		t.Error("same value through different pointers must fingerprint the same")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Per-workspace manager cache (harnessManagerFor / HarnessCommandsFor)
// ──────────────────────────────────────────────────────────────────────────────

// writeHarnessCommandFile creates <workspace>/commands/<name>.md with the given
// frontmatter block (may be empty) and template body.
func writeHarnessCommandFile(t *testing.T, workspace, name, frontmatter, template string) {
	t.Helper()
	dir := filepath.Join(workspace, "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir commands dir: %v", err)
	}
	content := template
	if frontmatter != "" {
		content = "---\n" + frontmatter + "\n---\n" + template + "\n"
	}
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// commandNamesOf maps a command slice to its names for readable assertions.
func commandNamesOf(cmds []*harness.Command) map[string]bool {
	out := make(map[string]bool, len(cmds))
	for _, c := range cmds {
		out[c.Name] = true
	}
	return out
}

// TestHarnessManagerFor_PerWorkspaceManagers pins the core of the per-agent
// commands fix: one cached manager per workspace, each seeing the commands of
// ITS workspace, while the defaults workspace keeps exactly the view
// HarnessCommands() has always had.
func TestHarnessManagerFor_PerWorkspaceManagers(t *testing.T) {
	al, defaultsWs := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"shared": {Description: "from config", Template: "config body"},
	})

	wsA := t.TempDir()
	wsB := t.TempDir()
	writeHarnessCommandFile(t, wsA, "a-only", "description: A", "body A")
	writeHarnessCommandFile(t, wsB, "b-only", "description: B", "body B")
	// Same name in two workspaces: each manager must report ITS OWN file, which
	// a shared manager could never do.
	writeHarnessCommandFile(t, wsA, "clash", "description: from A", "A body")
	writeHarnessCommandFile(t, wsB, "clash", "description: from B", "B body")

	// "" is the defaults workspace, and so is the defaults workspace spelled
	// out: one entry, one manager.
	def := al.harnessManagerFor("")
	if def != al.harnessManager() {
		t.Error(`harnessManagerFor("") and harnessManager() returned different managers`)
	}
	if got, want := al.harnessManagerFor(defaultsWs), def; got != want {
		t.Errorf("defaults workspace resolved to a second manager (%p vs %p)", got, want)
	}
	// Trailing separators must not fork an entry either.
	if got, want := al.harnessManagerFor(wsA+string(filepath.Separator)), al.harnessManagerFor(wsA); got != want {
		t.Errorf("trailing separator forked the entry for %s", wsA)
	}

	mgrA := al.harnessManagerFor(wsA)
	mgrB := al.harnessManagerFor(wsB)
	if mgrA == mgrB || mgrA == def || mgrB == def {
		t.Fatalf("workspaces share a manager: def=%p A=%p B=%p", def, mgrA, mgrB)
	}
	// Repeated access reuses the cached manager (no rebuild per message).
	if al.harnessManagerFor(wsA) != mgrA {
		t.Error("manager for workspace A rebuilt without a config change")
	}

	// Registry contents per workspace.
	namesA := commandNamesOf(mgrA.Registry().All())
	namesB := commandNamesOf(mgrB.Registry().All())
	for _, tc := range []struct {
		label  string
		names  map[string]bool
		want   []string
		unwant []string
	}{
		{"A", namesA, []string{"a-only", "clash", "shared"}, []string{"b-only"}},
		{"B", namesB, []string{"b-only", "clash", "shared"}, []string{"a-only"}},
		{"defaults", commandNamesOf(def.Registry().All()), []string{"shared"}, []string{"a-only", "b-only", "clash"}},
	} {
		for _, n := range tc.want {
			if !tc.names[n] {
				t.Errorf("workspace %s: command %q missing", tc.label, n)
			}
		}
		for _, n := range tc.unwant {
			if tc.names[n] {
				t.Errorf("workspace %s: command %q leaked from another workspace", tc.label, n)
			}
		}
	}

	// The clash must resolve to each workspace's own template.
	cmdA, _ := mgrA.Registry().Get("clash")
	cmdB, _ := mgrB.Registry().Get("clash")
	if cmdA.Template != "A body" {
		t.Errorf("workspace A clash template = %q, want %q", cmdA.Template, "A body")
	}
	if cmdB.Template != "B body" {
		t.Errorf("workspace B clash template = %q, want %q", cmdB.Template, "B body")
	}

	// HarnessCommands() must be exactly HarnessCommandsFor("") — the palette
	// channels and the TUI keep seeing the defaults view, unchanged.
	defaults := al.HarnessCommands()
	for _, ws := range []string{wsA, wsB} {
		if got := commandNamesOf(al.HarnessCommandsFor(ws)); !got["clash"] {
			t.Errorf("HarnessCommandsFor(%s) lost the workspace command", ws)
		}
	}
	if names := commandNamesOf(defaults); names["a-only"] || names["b-only"] {
		t.Errorf("HarnessCommands() now leaks agent workspaces: %v", names)
	}
	if len(defaults) != len(al.HarnessCommandsFor("")) {
		t.Error("HarnessCommands() and HarnessCommandsFor(\"\") disagree")
	}
}

// TestHarnessManagerFor_RebuildsEntryOnHarnessFlagChange pins the hot-reload
// half of the per-entry fingerprint: flipping a harness permission default must
// replace every cached manager that depends on it, so no agent keeps running
// with the OLD permissions after a config reload.
func TestHarnessManagerFor_RebuildsEntryOnHarnessFlagChange(t *testing.T) {
	al, _ := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"sh": {Description: "d", Template: "run !`echo hi`"},
	})
	ws := t.TempDir()
	writeHarnessCommandFile(t, ws, "local", "description: local", "local body")

	defBefore := al.harnessManager()
	mgrBefore := al.harnessManagerFor(ws)
	if mgrBefore.AllowShell(mustHarnessCommand(t, mgrBefore, "sh")) {
		t.Fatal("test setup: shell must start disabled")
	}

	cfg := al.cfg()
	cfg.Harness.AllowShell = true
	al.cfgPtr.Store(cfg)

	defAfter := al.harnessManager()
	mgrAfter := al.harnessManagerFor(ws)
	if defAfter == defBefore {
		t.Error("defaults manager not rebuilt after AllowShell change")
	}
	if mgrAfter == mgrBefore {
		t.Error("workspace manager not rebuilt after AllowShell change: it would keep the old permissions")
	}
	if !mgrAfter.AllowShell(mustHarnessCommand(t, mgrAfter, "sh")) {
		t.Error("rebuilt workspace manager still reports shell disabled")
	}
	if !defAfter.AllowShell(mustHarnessCommand(t, defAfter, "sh")) {
		t.Error("rebuilt defaults manager still reports shell disabled")
	}
	// The other workspace's commands survive the rebuild.
	if _, ok := mgrAfter.Registry().Get("local"); !ok {
		t.Error("workspace command lost after rebuild")
	}
	// And the entry is cached again: no rebuild without a further change.
	if al.harnessManagerFor(ws) != mgrAfter {
		t.Error("manager rebuilt twice for one config change")
	}

}

// mustHarnessCommand fetches a command from a manager's registry or fails.
func mustHarnessCommand(t *testing.T, mgr *harness.Manager, name string) *harness.Command {
	t.Helper()
	cmd, ok := mgr.Registry().Get(name)
	if !ok {
		t.Fatalf("command %q missing from manager", name)
	}
	return cmd
}

// TestHarnessManagerFor_GlobalConfigChangeRebuildsAllEntriesLazily documents the
// intended laziness of the map: after a global config change, EVERY entry is
// rebuilt the next time it is touched (each computes its own fingerprint), and
// an entry nobody touches is never rebuilt.
func TestHarnessManagerFor_GlobalConfigChangeRebuildsAllEntriesLazily(t *testing.T) {
	al, _ := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"one": {Template: "first"},
	})
	wsA, wsB := t.TempDir(), t.TempDir()

	mgrADef := al.harnessManagerFor(wsA)
	mgrBDef := al.harnessManagerFor(wsB)

	cfg := al.cfg()
	cfg.Commands["two"] = config.CommandDefinition{Template: "second"}
	al.cfgPtr.Store(cfg)

	mgrANew := al.harnessManagerFor(wsA)
	if mgrANew == mgrADef {
		t.Fatal("workspace A entry not rebuilt after the config command map changed")
	}
	if _, ok := mgrANew.Registry().Get("two"); !ok {
		t.Fatal("new config command missing from rebuilt workspace A manager")
	}
	// B was untouched while the config changed; accessing it now must still see
	// the new config, because its own fingerprint changed too.
	mgrBNew := al.harnessManagerFor(wsB)
	if mgrBNew == mgrBDef {
		t.Fatal("workspace B entry not rebuilt on first access after a global config change")
	}
	if _, ok := mgrBNew.Registry().Get("two"); !ok {
		t.Error("workspace B manager kept a stale config command map")
	}
}

// TestHarnessManagerFor_ConcurrentPerWorkspaceAccess hammers harnessManagerFor
// from several goroutines across several workspaces while the config changes
// underneath, guarding the map and its entries under harnessMu. Run under -race
// in CI.
func TestHarnessManagerFor_ConcurrentPerWorkspaceAccess(t *testing.T) {
	al, _ := newHarnessTestLoop(t, map[string]config.CommandDefinition{
		"base": {Template: "b"},
	})
	workspaces := []string{"", t.TempDir(), t.TempDir(), t.TempDir()}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				mgr := al.harnessManagerFor(workspaces[i%len(workspaces)])
				if mgr == nil || mgr.Registry() == nil {
					t.Errorf("harnessManagerFor returned nil manager/registry for %q", workspaces[i%len(workspaces)])
					return
				}
			}
		}(i)
	}
	for n := 1; n <= 20; n++ {
		cfg := al.cfg()
		next := make(map[string]config.CommandDefinition, len(cfg.Commands)+1)
		for k, v := range cfg.Commands {
			next[k] = v
		}
		next[fmt.Sprintf("cmd%d", n)] = config.CommandDefinition{Template: "t"}
		cfg.Commands = next
		al.cfgPtr.Store(cfg)
	}
	close(stop)
	wg.Wait()

	for _, ws := range workspaces {
		if !commandNamesOf(al.HarnessCommandsFor(ws))["base"] {
			t.Errorf("base command lost in workspace %q", ws)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// T-B4: regression for the per-agent commands bug.
// ──────────────────────────────────────────────────────────────────────────────

// perWorkspaceHarnessLoop builds a loop with two agents living in two distinct
// workspaces, each carrying its own commands/ folder and its own notes.txt.
// "main" owns the defaults workspace; "coder" is reached either by a guild
// binding (discord/g1) or by a session agent override. The mock provider
// records what the LLM actually saw, which is the only way to observe the
// content processMessage expanded.
func perWorkspaceHarnessLoop(t *testing.T) (*AgentLoop, *llmRunnerMockLLMProvider, string, string) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "harness-per-ws-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	defaultsWs := filepath.Join(tmpDir, "ws-main")
	coderWs := filepath.Join(tmpDir, "ws-coder")
	for _, ws := range []string{defaultsWs, coderWs} {
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", ws, err)
		}
	}
	// The SAME command name exists in both workspaces with a different template,
	// and the SAME relative @file reference resolves to different content in
	// each — so a wrong-workspace expansion cannot pass by accident.
	writeHarnessCommandFile(t, defaultsWs, "peek", "description: from main", "main sees @notes.txt")
	writeHarnessCommandFile(t, coderWs, "peek", "description: from coder", "coder sees @notes.txt")
	if err := os.WriteFile(filepath.Join(defaultsWs, "notes.txt"), []byte("MAIN-NOTE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(coderWs, "notes.txt"), []byte("CODER-NOTE"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         defaultsWs,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Workspace: defaultsWs},
				{ID: "coder", Workspace: coderWs},
			},
		},
		Bindings: []config.AgentBinding{
			{AgentID: "coder", Match: config.BindingMatch{Channel: "discord", GuildID: "g1"}},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())

	provider := &llmRunnerMockLLMProvider{
		response:    &providers.LLMResponse{Content: "ok", ToolCalls: []providers.ToolCall{}},
		callHistory: []providers.Message{},
	}
	// Both agents need the mock: the routed one answers, the other must never be
	// reached with the real (keyless) provider.
	for _, id := range []string{"main", "coder"} {
		agent, ok := al.registry.GetAgent(id)
		if !ok || agent == nil {
			t.Fatalf("agent %q missing from registry", id)
		}
		agent.Provider = provider
		agent.Candidates = nil
	}

	return al, provider, defaultsWs, coderWs
}

// lastUserMessage returns the content of the last user message the provider saw.
func lastUserMessage(t *testing.T, p *llmRunnerMockLLMProvider) string {
	t.Helper()
	if p.callCount == 0 {
		t.Fatal("provider never called")
	}
	var last string
	for _, m := range p.callHistory {
		if m.Role == "user" {
			last = m.Content
		}
	}
	if last == "" {
		t.Fatal("provider saw no user message")
	}
	return last
}

// TestProcessMessage_HarnessCommandUsesRoutedAgentWorkspace is the regression
// test for the bug this change fixes: a custom command defined in the ROUTED
// agent's workspace must be found there and expanded with that workspace as the
// working directory. Before the fix, applyHarnessCommand ran before routing with
// the agents.defaults workspace, so an agent with its own workspace could never
// see or run its own commands.
func TestProcessMessage_HarnessCommandUsesRoutedAgentWorkspace(t *testing.T) {
	al, provider, _, coderWs := perWorkspaceHarnessLoop(t)

	msg := bus.InboundMessage{
		Channel:    "discord",
		SenderID:   "u1",
		ChatID:     "c1",
		SessionKey: "tb4-routed",
		Content:    "/peek",
		Metadata:   map[string]string{"guild_id": "g1", "peer_kind": "channel", "peer_id": "chan-1"},
	}
	if _, err := al.messageProcessor.processMessage(context.Background(), msg); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	got := lastUserMessage(t, provider)
	if got != "coder sees CODER-NOTE" {
		t.Errorf("expanded prompt = %q, want %q (coder's own command and file)", got, "coder sees CODER-NOTE")
	}
	if strings.Contains(got, "MAIN-NOTE") || strings.Contains(got, "main sees") {
		t.Errorf("expansion leaked the defaults workspace: %q", got)
	}
	// The command really came from the workspace level of the routed agent.
	if _, ok := al.harnessManagerFor(coderWs).Registry().Get("peek"); !ok {
		t.Error("coder workspace has no peek command — test setup broken")
	}
}

// TestProcessMessage_HarnessCommandUsesSessionAgentWorkspace pins the second
// half of the effective-agent resolution: the session's agent override (set by
// /agent) wins over the route, so it is also the workspace the command is looked
// up in. Here the channel routes to "main" but the session is pinned to "coder".
func TestProcessMessage_HarnessCommandUsesSessionAgentWorkspace(t *testing.T) {
	al, provider, _, _ := perWorkspaceHarnessLoop(t)

	sessionKey := "tb4-session-agent"
	al.setSessionAgent(sessionKey, "coder")

	msg := bus.InboundMessage{
		Channel:    "cli",
		SenderID:   "u1",
		ChatID:     "c1",
		SessionKey: sessionKey,
		Content:    "/peek",
		Metadata:   map[string]string{},
	}
	if _, err := al.messageProcessor.processMessage(context.Background(), msg); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if got := lastUserMessage(t, provider); got != "coder sees CODER-NOTE" {
		t.Errorf("expanded prompt = %q, want the session agent's command", got)
	}
}

// TestProcessMessage_HarnessCommandFallsBackToDefaultsWorkspace pins the other
// side: with no binding and no session override, the routed agent IS the defaults
// agent, so its workspace keeps being used — the historical behaviour.
func TestProcessMessage_HarnessCommandFallsBackToDefaultsWorkspace(t *testing.T) {
	al, provider, _, _ := perWorkspaceHarnessLoop(t)

	msg := bus.InboundMessage{
		Channel:    "cli",
		SenderID:   "u1",
		ChatID:     "c1",
		SessionKey: "tb4-default",
		Content:    "/peek",
		Metadata:   map[string]string{},
	}
	if _, err := al.messageProcessor.processMessage(context.Background(), msg); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if got := lastUserMessage(t, provider); got != "main sees MAIN-NOTE" {
		t.Errorf("expanded prompt = %q, want the defaults workspace command", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// sessionAgentOverride: the raw "is this session pinned?" lookup
// ──────────────────────────────────────────────────────────────────────────────

// TestSessionAgentOverride_DistinguishesPinFromDefault pins the root cause of
// the per-agent commands bug: getSessionAgent falls back to the default agent,
// so it cannot tell "no pin" from "pinned to the default agent". The override
// getter must, otherwise any route resolved from a binding is silently replaced
// by the default agent downstream.
func TestSessionAgentOverride_DistinguishesPinFromDefault(t *testing.T) {
	al, _, _, _ := perWorkspaceHarnessLoop(t)

	if id, pinned := al.sessionAgentOverride("no-such-session"); pinned {
		t.Errorf("unpinned session reported a pin to %q", id)
	}

	// A pin to the default agent is still a pin — the two getters must differ
	// exactly here, and the routed path must be able to tell them apart.
	al.setSessionAgent("sess-a", "main")
	id, pinned := al.sessionAgentOverride("sess-a")
	if !pinned || id != "main" {
		t.Errorf("sessionAgentOverride() = (%q,%v), want (\"main\",true)", id, pinned)
	}

	// ResolveSessionKey must be honoured: the pin lives on the active key the
	// base alias points at.
	al.setSessionAgent("sess-active", "coder")
	al.setSessionAlias("sess-base", "sess-active")
	if id, pinned := al.sessionAgentOverride("sess-base"); !pinned || id != "coder" {
		t.Errorf("override through alias = (%q,%v), want (\"coder\",true)", id, pinned)
	}

	// Deleting the pin returns the session to "no opinion", which is what lets
	// routing win again.
	al.deleteDurableSessionAgent("sess-a")
	if _, pinned := al.sessionAgentOverride("sess-a"); pinned {
		t.Error("pin survived deletion")
	}
}

// TestProcessMessage_HarnessCommandSurvivesNewCommand closes the loop between
// the two halves of the fix. /new rotates the session key and re-pins the agent
// it resolved, so if the command dispatcher still let the default-agent fallback
// of getSessionAgent win, /new on a bound channel would pin the WRONG agent on
// the fresh key and every later turn — including this one — would expand the
// defaults workspace again. The pin path and the expansion path must agree on
// who owns the session.
func TestProcessMessage_HarnessCommandSurvivesNewCommand(t *testing.T) {
	al, provider, _, _ := perWorkspaceHarnessLoop(t)

	base := "tb4-new"
	newMsg := func(content string) bus.InboundMessage {
		return bus.InboundMessage{
			Channel:    "discord",
			SenderID:   "u1",
			ChatID:     "c1",
			SessionKey: base,
			Content:    content,
			Metadata:   map[string]string{"guild_id": "g1", "peer_kind": "channel", "peer_id": "chan-1"},
		}
	}

	if _, err := al.messageProcessor.processMessage(context.Background(), newMsg("/new")); err != nil {
		t.Fatalf("/new: %v", err)
	}
	// /new must have rotated the session and pinned the routed agent.
	if id, pinned := al.sessionAgentOverride(al.ResolveSessionKey(base)); !pinned || id != "coder" {
		t.Fatalf("after /new, session pin = (%q,%v), want (\"coder\",true)", id, pinned)
	}

	if _, err := al.messageProcessor.processMessage(context.Background(), newMsg("/peek")); err != nil {
		t.Fatalf("/peek: %v", err)
	}
	if got := lastUserMessage(t, provider); got != "coder sees CODER-NOTE" {
		t.Errorf("expanded prompt after /new = %q, want the routed agent's command", got)
	}
}

// TestInvalidateHarnessWorkspace_ForcesRebuildFromDisk covers the hook the REST
// command endpoints use after writing <workspace>/commands/*.md: a file created
// behind the manager's back must be visible on the next read, not after the
// 30 s rescan TTL.
func TestInvalidateHarnessWorkspace_ForcesRebuildFromDisk(t *testing.T) {
	al, _, defaultsWs, coderWs := perWorkspaceHarnessLoop(t)

	// Warm both caches, then add a command to each workspace without touching
	// the managers.
	before := len(al.HarnessCommandsFor(coderWs))
	writeHarnessCommandFile(t, coderWs, "fresh", "description: written after warm-up", "do the thing")
	writeHarnessCommandFile(t, defaultsWs, "fresh-defaults", "description: defaults too", "do the other thing")
	if n := len(al.HarnessCommandsFor(coderWs)); n != before {
		t.Fatalf("cache is not fresh-respecting: got %d commands, want %d", n, before)
	}

	al.InvalidateHarnessWorkspace(coderWs)
	al.InvalidateHarnessWorkspace(defaultsWs)

	cmds := al.HarnessCommandsFor(coderWs)
	if len(cmds) != before+1 {
		t.Fatalf("after invalidation got %d commands, want %d", len(cmds), before+1)
	}
	if _, ok := al.harnessManagerFor(coderWs).Registry().Get("fresh"); !ok {
		t.Error("newly written workspace command missing after invalidation")
	}
	if _, ok := al.harnessManagerFor("").Registry().Get("fresh-defaults"); !ok {
		t.Error("newly written defaults command missing after invalidation")
	}
	// The defaults entry must have been rebuilt too, and its commands are still
	// only its own.
	if _, ok := al.harnessManagerFor("").Registry().Get("fresh"); ok {
		t.Error("invalidation leaked coder's command into the defaults manager")
	}

	// Unknown / unpinned workspaces are no-ops, and "" resolves through the
	// same key rule as the cache itself.
	al.InvalidateHarnessWorkspace(filepath.Join(coderWs, "..", "nowhere"))
	al.InvalidateHarnessWorkspace("")
	if _, ok := al.harnessManagerFor("").Registry().Get("fresh-defaults"); !ok {
		t.Error("InvalidateHarnessWorkspace(\"\") dropped a still-valid entry")
	}
}

// TestSetSessionAgent_CanPinDefaultAgent covers the corollary of the pin/route
// split: now that routing is honoured, "/agent main" is the only way to pull a
// bound session back to the default agent, so it must actually store a pin.
// SetSessionAgent's "already there" early return compared against
// GetSessionAgent, which answers "main" for any unpinned session, so the pin was
// silently dropped and the binding kept winning.
func TestSetSessionAgent_CanPinDefaultAgent(t *testing.T) {
	al, _, _, _ := perWorkspaceHarnessLoop(t)

	sessionKey := "tb-pin-default"
	if _, pinned := al.sessionAgentOverride(sessionKey); pinned {
		t.Fatal("session starts pinned — test setup broken")
	}
	// The legacy getter already answers "main": that is exactly the confusion
	// this guards against.
	if got := al.getSessionAgent(sessionKey); got != "main" {
		t.Fatalf("getSessionAgent() = %q, want the default fallback", got)
	}

	al.providable.SetSessionAgent(sessionKey, "main")

	id, pinned := al.sessionAgentOverride(sessionKey)
	if !pinned || id != "main" {
		t.Errorf("after SetSessionAgent(\"main\"): override = (%q,%v), want (\"main\",true)", id, pinned)
	}

	// A redundant re-pin of an already-pinned session still short-circuits, so
	// the history migration and the model reset below it do not run twice.
	al.providable.SetSessionModel(sessionKey, "some-model")
	al.providable.SetSessionAgent(sessionKey, "main")
	if _, hasModel := al.sessionModels.Load(al.ResolveSessionKey(sessionKey)); !hasModel {
		t.Error("re-pinning the same agent cleared the session model (early return lost)")
	}
}
