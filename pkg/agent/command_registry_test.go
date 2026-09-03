// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"testing"

	"github.com/xilistudios/lele/pkg/agent/commands"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// testCommandHandler builds a minimal AgentLoop + command handler good enough to
// dispatch slash commands, mirroring the setup used by command_handler_test.go.
func testCommandHandler(t *testing.T) *commandHandlerImpl {
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
	}
	return newCommandHandler(NewAgentLoop(cfg, bus.NewMessageBus()))
}

// TestWebUICommands_ReturnsClearAndCompactSorted asserts the registry exposes
// exactly the two commands the WebUI palette should show, in stable order.
func TestWebUICommands_ReturnsClearAndCompactSorted(t *testing.T) {
	got := WebUICommands()

	if len(got) != 2 {
		t.Fatalf("WebUICommands() returned %d entries, want 2: %+v", len(got), got)
	}

	want := []CommandInfo{
		{Name: "/clear", Description: "Clear the conversation history for this session.", Usage: "/clear"},
		{Name: "/compact", Description: "Summarize and compact the conversation history (needs 5+ messages).", Usage: "/compact"},
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("WebUICommands()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Defensive: sorted by name must hold regardless of declaration order.
	for i := 1; i < len(got); i++ {
		if got[i-1].Name >= got[i].Name {
			t.Errorf("commands not sorted by name: %q before %q", got[i-1].Name, got[i].Name)
		}
	}
}

// TestWebUICommands_ReturnsCopy asserts the internal registry is not exposed for
// mutation: overwriting the returned slice (or its elements) must not leak into
// subsequent calls.
func TestWebUICommands_ReturnsCopy(t *testing.T) {
	first := WebUICommands()

	// Clobber the elements and shrink the slice via append-into-backing-array.
	for i := range first {
		first[i] = CommandInfo{Name: "/tampered", Description: "tampered", Usage: "tampered"}
	}
	_ = append(first, CommandInfo{Name: "/injected", Description: "injected", Usage: "/injected"})

	second := WebUICommands()
	if len(second) != 2 {
		t.Fatalf("registry length changed after mutating a copy: %d", len(second))
	}
	if second[0].Name != "/clear" || second[1].Name != "/compact" {
		t.Errorf("registry mutated through a returned copy: %+v", second)
	}
	for _, c := range second {
		if c.Description == "" || c.Usage == "" {
			t.Errorf("command %q mutated through a returned copy: %+v", c.Name, c)
		}
	}
}

// TestWebUICommands_MatchDispatchedCommands guards the one real risk of keeping
// the registry separate from the handleCommand switch: drift. Every command the
// registry advertises must actually be dispatched by the backend, and no
// session-scoped, argument-free command may be missing from the registry.
func TestWebUICommands_MatchDispatchedCommands(t *testing.T) {
	ch := testCommandHandler(t)

	// Every advertised command must be handled by the dispatcher.
	for _, c := range WebUICommands() {
		if _, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
			Channel:  "test",
			SenderID: "user1",
			ChatID:   "chat1",
			Content:  c.Name,
		}); !handled {
			t.Errorf("registry advertises %q but handleCommand does not dispatch it", c.Name)
		}
	}

	// The two commands the WebUI must expose today; if handleCommand grows a new
	// session-scoped argument-free command, decide explicitly whether to register
	// it here (and in the registry).
	for _, name := range []string{"/clear", "/compact"} {
		found := false
		for _, c := range WebUICommands() {
			if c.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("handleCommand dispatches %q but the registry does not advertise it", name)
		}
	}
}

// TestWebUICommands_FieldsNonEmpty keeps the palette honest: a command with an
// empty description or usage renders as a broken row in the UI.
func TestWebUICommands_FieldsNonEmpty(t *testing.T) {
	for _, c := range WebUICommands() {
		if c.Name == "" || c.Description == "" || c.Usage == "" {
			t.Errorf("command has empty field: %+v", c)
		}
	}
}

// TestCommandRegistry_ReexportMatchesSource guards the alias in
// command_registry.go: agent.WebUICommands must keep returning exactly what the
// registry package (the single source of truth pkg/channels reads) returns, so
// the two entry points can never disagree.
func TestCommandRegistry_ReexportMatchesSource(t *testing.T) {
	viaAgent := WebUICommands()
	viaSource := commands.WebUICommands()

	if len(viaAgent) != len(viaSource) {
		t.Fatalf("agent.WebUICommands() has %d entries, commands.WebUICommands() has %d",
			len(viaAgent), len(viaSource))
	}
	for i := range viaSource {
		if viaAgent[i] != viaSource[i] {
			t.Errorf("entry %d differs: agent=%+v source=%+v", i, viaAgent[i], viaSource[i])
		}
	}
}
