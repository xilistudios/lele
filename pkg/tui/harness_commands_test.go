package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/harness"
)

// harnessTestModel builds a TUI model whose backend loop exposes two custom
// (harness) commands: "hola" (plain) and "tests" (with agent+model overrides).
// The commands are declared through config.Commands, the lowest-precedence
// discovery level, so no fixture files are needed.
func harnessTestModel(t *testing.T) *Model {
	t.Helper()
	cfg := testModelConfig(t)
	cfg.Commands = map[string]config.CommandDefinition{
		"hola":  {Description: "Saluda", Template: "hola $ARGUMENTS"},
		"tests": {Description: "Corre tests", Agent: "coder", Model: "openai/gpt-5", Template: "run tests"},
	}
	return newTestModelWithConfig(t, cfg, true)
}

// --- customCommands() -------------------------------------------------------

func TestCustomCommands_ListsHarnessCommands(t *testing.T) {
	m := harnessTestModel(t)

	customs := m.customCommands()
	if len(customs) != 2 {
		t.Fatalf("customCommands() = %d entries, want 2: %+v", len(customs), customs)
	}
	// Registry.All() is sorted by name, so "hola" comes first.
	if customs[0].name != "/hola" || customs[0].description != "Saluda" {
		t.Errorf("first entry = %+v, want /hola Saluda", customs[0])
	}
	if customs[1].name != "/tests" {
		t.Fatalf("second entry name = %q, want /tests", customs[1].name)
	}
	// Agent/model overrides are appended to the description.
	for _, want := range []string{"Corre tests", "agent: coder", "model: openai/gpt-5"} {
		if !strings.Contains(customs[1].description, want) {
			t.Errorf("description %q missing %q", customs[1].description, want)
		}
	}
}

func TestCustomCommands_NilLoopReturnsNil(t *testing.T) {
	m := &Model{}
	if got := m.customCommands(); got != nil {
		t.Errorf("customCommands() with nil loop = %+v, want nil", got)
	}
	if m.isCustomCommand("hola") {
		t.Error("isCustomCommand must be false without an agent loop")
	}
}

func TestCustomCommands_CachesResult(t *testing.T) {
	m := harnessTestModel(t)

	first := m.customCommands()
	if len(first) != 2 {
		t.Fatalf("want 2 customs, got %d", len(first))
	}
	// Mutate the cache; a cached read must return the slice as-is instead of
	// re-querying the backend (which would rebuild it from config).
	m.customCmds = []commandInfo{{name: "/cached"}}
	if got := m.customCommands(); len(got) != 1 || got[0].name != "/cached" {
		t.Fatalf("cache not honoured, got %+v", got)
	}
	// Forcing the snapshot older than the TTL triggers a refresh.
	m.customCmdsAt = m.customCmdsAt.Add(-2 * customCommandsRefreshTTL)
	if got := m.customCommands(); len(got) != 2 {
		t.Fatalf("stale cache not refreshed, got %+v", got)
	}
}

func TestIsCustomCommand_CaseInsensitiveAndSlashOptional(t *testing.T) {
	m := harnessTestModel(t)

	for _, name := range []string{"hola", "/hola", "/HOLA", "Hola"} {
		if !m.isCustomCommand(name) {
			t.Errorf("isCustomCommand(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "/", "/clear", "/nope"} {
		if m.isCustomCommand(name) {
			t.Errorf("isCustomCommand(%q) = true, want false", name)
		}
	}
}

// --- filterAutocomplete -----------------------------------------------------

func TestFilterAutocomplete_MergesCustomsAfterBuiltins(t *testing.T) {
	m := harnessTestModel(t)

	// A prefix that matches no built-in still finds the custom command.
	m.filterAutocomplete("/ho")
	names := autocompleteNames(m.autocompleteItems)
	if len(names) != 1 || names[0] != "/hola" {
		t.Fatalf("autocomplete for /ho = %v, want [/hola]", names)
	}

	m.filterAutocomplete("/h")
	names = autocompleteNames(m.autocompleteItems)
	if len(names) != 1 || names[0] != "/hola" {
		t.Fatalf("autocomplete for /h = %v, want [/hola]", names)
	}

	// Full listing: builtins keep priority order, customs come last.
	m.filterAutocomplete("/")
	names = autocompleteNames(m.autocompleteItems)
	if names[len(names)-2] != "/hola" || names[len(names)-1] != "/tests" {
		t.Fatalf("customs must be appended after builtins, got %v", names)
	}
	for _, b := range allCommands {
		if !containsName(names, b.name) {
			t.Fatalf("builtin %s missing from autocomplete", b.name)
		}
	}
}

func TestFilterAutocomplete_BuiltinWinsOverCustom(t *testing.T) {
	cfg := testModelConfig(t)
	// A custom command shadowing a built-in name must not be offered twice.
	cfg.Commands = map[string]config.CommandDefinition{
		"clear": {Description: "custom clear", Template: "please clear"},
	}
	m := newTestModelWithConfig(t, cfg, true)

	m.filterAutocomplete("/cle")
	names := autocompleteNames(m.autocompleteItems)
	count := 0
	for _, n := range names {
		if n == "/clear" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("/clear appears %d times, want 1 (built-in wins): %v", count, names)
	}
	// The surviving entry is the built-in description.
	for _, it := range m.autocompleteItems {
		if it.name == "/clear" && strings.Contains(it.description, "custom") {
			t.Errorf("custom entry leaked into autocomplete: %+v", it)
		}
	}
}

// --- executeCommand pass-through -------------------------------------------

func TestExecuteCommand_UnknownCustomPublishes(t *testing.T) {
	m := harnessTestModel(t)

	cmd := m.executeCommand("/hola mundo")
	if cmd == nil {
		t.Fatal("executeCommand on a custom command must return a command (tick), not nil")
	}
	// Mirrors publishUserMessage: the raw text becomes the pending user
	// message (so it shows in the transcript) and the turn starts.
	if m.pendingUserMessage != "/hola mundo" {
		t.Errorf("pendingUserMessage = %q, want %q", m.pendingUserMessage, "/hola mundo")
	}
	if !m.processing {
		t.Error("expected processing state after publishing a custom command")
	}
	if m.currentKey == "" {
		t.Error("expected a session to be created for the custom command turn")
	}
	if m.chatInput.Value() != "" {
		t.Errorf("composer should be cleared by the caller, got %q", m.chatInput.Value())
	}
}

func TestExecuteCommand_CustomWhileBusyQueues(t *testing.T) {
	m := harnessTestModel(t)
	m.executeCommand("/new")
	m.processing = true // a turn is in flight

	if cmd := m.executeCommand("/tests -v"); cmd == nil {
		t.Fatal("expected a tick command for the queued message")
	}
	q := m.messageQueue[m.currentKey]
	if len(q) != 1 || q[0].Content != "/tests -v" {
		t.Fatalf("queue = %+v, want one entry %q", q, "/tests -v")
	}
	if m.pendingUserMessage != "" {
		t.Errorf("busy path must not publish directly, pendingUserMessage = %q", m.pendingUserMessage)
	}
}

func TestExecuteCommand_UnknownNonCustomFallsThrough(t *testing.T) {
	m := harnessTestModel(t)

	if cmd := m.executeCommand("/notacommand arg"); cmd != nil {
		t.Errorf("unknown command should return nil (fall through to LLM), got %v", cmd)
	}
	if m.pendingUserMessage != "" {
		t.Errorf("unknown command must not publish, pendingUserMessage = %q", m.pendingUserMessage)
	}
	// Built-ins keep their local behavior (no publish).
	if cmd := m.executeCommand("/clear"); cmd != nil {
		t.Errorf("/clear should stay local, got %v", cmd)
	}
	if m.pendingUserMessage != "" {
		t.Errorf("/clear must not be published as a message, pendingUserMessage = %q", m.pendingUserMessage)
	}
}

// --- command.applied event --------------------------------------------------

func TestFormatCommandApplied(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]string
		want string
	}{
		{
			name: "bare command",
			meta: map[string]string{"command": "hola"},
			want: "⚡ /hola applied",
		},
		{
			name: "already slashed",
			meta: map[string]string{"command": "/hola"},
			want: "⚡ /hola applied",
		},
		{
			name: "with args",
			meta: map[string]string{"command": "hola", "args": "mundo"},
			want: "⚡ /hola applied mundo",
		},
		{
			name: "agent and model",
			meta: map[string]string{"command": "tests", "agent": "coder", "model": "openai/gpt-5"},
			want: "⚡ /tests applied · agent: coder · model: openai/gpt-5",
		},
		{
			name: "args truncated to 60 runes",
			meta: map[string]string{"command": "hola", "args": strings.Repeat("é", 70)},
			want: "⚡ /hola applied " + strings.Repeat("é", 60) + "…",
		},
		{
			name: "empty command yields empty line",
			meta: map[string]string{"args": "orphan"},
			want: "",
		},
		{
			name: "nil metadata",
			meta: nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCommandApplied(tt.meta); got != tt.want {
				t.Errorf("formatCommandApplied() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommandAppliedEvent_RendersActivityLine(t *testing.T) {
	m := harnessTestModel(t)
	m.executeCommand("/new")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	// The card is an activity line for the turn in flight (identical
	// lifecycle to tool.executing), so the backend has marked it processing.
	m.processing = true

	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		Event:  "command.applied",
		ChatID: m.currentKey,
		Metadata: map[string]string{
			"command": "tests", "description": "Corre tests", "args": "-v",
			"agent": "coder", "model": "openai/gpt-5", "source": string(harness.SourceWorkspace),
		},
	}})

	want := "⚡ /tests applied -v · agent: coder · model: openai/gpt-5"
	if m.currentToolAction != want {
		t.Errorf("currentToolAction = %q, want %q", m.currentToolAction, want)
	}
	// The line must actually reach the rendered overlay (same path as
	// tool.executing), not just the state field.
	joined := strings.Join(m.viewport.overlayLines, "\n")
	if !strings.Contains(joined, "/tests applied") {
		t.Fatalf("overlay does not contain the command card:\n%s", joined)
	}
}

func TestCommandAppliedEvent_OtherSessionIgnored(t *testing.T) {
	m := harnessTestModel(t)
	m.executeCommand("/new")

	_, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		Event:    "command.applied",
		ChatID:   "tui:chat:some-other-session",
		Metadata: map[string]string{"command": "hola"},
	}})

	if m.currentToolAction != "" {
		t.Errorf("event for another session leaked into current chat: %q", m.currentToolAction)
	}
}

// --- helpers ----------------------------------------------------------------

func autocompleteNames(items []commandInfo) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.name)
	}
	return out
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestQueueFlush_CustomCommandReachesBackendExpanded is the integration pin
// for the queued-custom-command path: enqueue while busy, flush when idle, and
// verify (a) the TUI publishes the RAW "/hola mundo" text (no prefix or
// rewrite that could block expansion) and (b) the real backend loop expands it
// and emits command.applied with the harness metadata. Steps (a) and (b) are
// each covered in isolation elsewhere; this test pins the seam between them.
//
// It deliberately does NOT reuse newTestModelWithConfig: that helper cancels
// the loop context when it returns, so no test could ever observe the backend
// processing a message.
func TestQueueFlush_CustomCommandReachesBackendExpanded(t *testing.T) {
	cfg := testModelConfig(t)
	cfg.Commands = map[string]config.CommandDefinition{
		"hola": {Description: "Saluda", Template: "hola $ARGUMENTS"},
	}
	msgBus := bus.NewMessageBus()
	al := agent.NewAgentLoop(cfg, msgBus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go al.Run(ctx)

	m := NewModel(cfg, al, al.SessionManager())
	m.onboardingActive = false

	if cmd := m.executeCommand("/new"); cmd != nil {
		if r := cmd(); r != nil {
			updated, _ := m.Update(r)
			m = updated.(*Model)
		}
	}
	if m.currentKey == "" {
		t.Fatal("/new did not create a session")
	}

	// Busy: the custom command must land in the queue, not on the bus.
	m.processing = true
	if cmd := m.executeCommand("/hola mundo"); cmd == nil {
		t.Fatal("expected tick command for queued message")
	}
	q := m.messageQueue[m.currentKey]
	if len(q) != 1 || q[0].Content != "/hola mundo" {
		t.Fatalf("queue = %+v, want raw '/hola mundo'", q)
	}

	// Idle again: flush pops the raw text and publishes it. The returned cmd
	// may legitimately be nil (the tick chain armed at enqueue time still
	// counts as pending), so assert on the publish side effects instead.
	m.processing = false
	m.startTime = time.Time{}
	if cmd := m.maybeFlushQueue(); cmd != nil {
		if r := cmd(); r != nil {
			updated, _ := m.Update(r)
			m = updated.(*Model)
		}
	}

	if m.pendingUserMessage != "/hola mundo" {
		t.Errorf("pendingUserMessage = %q, want raw '/hola mundo'", m.pendingUserMessage)
	}
	if len(m.messageQueue[m.currentKey]) != 0 {
		t.Errorf("queue not drained: %+v", m.messageQueue[m.currentKey])
	}

	// The real agent loop consumes the inbound message, applyHarnessCommand
	// expands it, and command.applied shows up on the outbound bus.
	mb := m.agentLoop.MessageBus()
	deadline := time.After(8 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		msg, ok := mb.SubscribeOutbound(ctx)
		cancel()
		if ok && msg.Event == "command.applied" {
			if msg.Metadata["command"] != "hola" || msg.Metadata["args"] != "mundo" || msg.Metadata["source"] != "config" {
				t.Errorf("command.applied metadata = %+v, want command=hola args=mundo source=config", msg.Metadata)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for command.applied from the backend")
		default:
		}
	}
}
