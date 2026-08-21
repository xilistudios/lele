package channels

import (
	"context"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func TestTelegramCommands_Remaining(t *testing.T) {
	loop := newNativeTestAgentLoop(config.DefaultConfig())
	ch, m := newMockTelegramChannel(t, nil, loop, nil)
	defer m.Close()

	c, ok := ch.commands.(*cmd)
	if !ok {
		t.Fatalf("commands is not *cmd: %T", ch.commands)
	}

	ctx := context.Background()
	msg := sampleTelegoMessage("/new", 100, 100)

	if err := c.New(ctx, *msg); err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Stop(ctx, *msg); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := c.Status(ctx, *msg); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if err := c.Subagents(ctx, *msg); err != nil {
		t.Fatalf("Subagents: %v", err)
	}
	// Model without args
	if err := c.Model(ctx, *msg); err != nil {
		t.Fatalf("Model no args: %v", err)
	}
	// Model with args
	msgWithArg := sampleTelegoMessage("/model gpt-4", 100, 100)
	if err := c.Model(ctx, *msgWithArg); err != nil {
		t.Fatalf("Model with args: %v", err)
	}

	if !m.hadMethod("sendMessage") {
		t.Fatal("expected sendMessage calls")
	}
}

func TestTelegramCommands_ListModelsAndChannels(t *testing.T) {
	loop := newNativeTestAgentLoop(config.DefaultConfig())
	ch, m := newMockTelegramChannel(t, nil, loop, nil)
	defer m.Close()

	c := ch.commands.(*cmd)
	ctx := context.Background()

	// list no args
	if err := c.List(ctx, *sampleTelegoMessage("/list", 1, 1)); err != nil {
		t.Fatalf("list no args: %v", err)
	}
	// list models
	if err := c.List(ctx, *sampleTelegoMessage("/list models", 1, 1)); err != nil {
		t.Fatalf("list models: %v", err)
	}
	// list channels (all disabled by default)
	if err := c.List(ctx, *sampleTelegoMessage("/list channels", 1, 1)); err != nil {
		t.Fatalf("list channels: %v", err)
	}
	// list unknown
	if err := c.List(ctx, *sampleTelegoMessage("/list foo", 1, 1)); err != nil {
		t.Fatalf("list unknown: %v", err)
	}
}

func TestTelegramCommands_ShowAgents(t *testing.T) {
	loop := newNativeTestAgentLoop(config.DefaultConfig())
	cfg := config.DefaultConfig()
	loop.config = cfg
	ch, m := newMockTelegramChannel(t, nil, loop, nil)
	defer m.Close()
	// Ensure config defaults are set so Show "model" branch fills in.
	c := ch.commands.(*cmd)
	ctx := context.Background()
	if err := c.Show(ctx, *sampleTelegoMessage("/show model", 1, 1)); err != nil {
		t.Fatalf("show model: %v", err)
	}
	if err := c.Show(ctx, *sampleTelegoMessage("/show channel", 1, 1)); err != nil {
		t.Fatalf("show channel: %v", err)
	}
	if err := c.Show(ctx, *sampleTelegoMessage("/show bogus", 1, 1)); err != nil {
		t.Fatalf("show unknown: %v", err)
	}
	if err := c.Show(ctx, *sampleTelegoMessage("/show", 1, 1)); err != nil {
		t.Fatalf("show no args: %v", err)
	}
}

func TestTelegramCommands_AgentNoAgents(t *testing.T) {
	loop := newNativeTestAgentLoop(config.DefaultConfig())
	loop.workspace = "/tmp/workspace"
	ch, m := newMockTelegramChannel(t, nil, loop, nil)
	defer m.Close()
	c := ch.commands.(*cmd)
	ctx := context.Background()
	// ListAvailableAgentIDs returns ["main"] in nativeTestAgentLoop, so
	// this will hit the "has agents" branch. Just ensure no panic.
	if err := c.Agent(ctx, *sampleTelegoMessage("/agent", 1, 1)); err != nil {
		t.Fatalf("agent: %v", err)
	}
	// With args
	if err := c.Agent(ctx, *sampleTelegoMessage("/agent research", 1, 1)); err != nil {
		t.Fatalf("agent with args: %v", err)
	}
}

func TestTelegramCommandArgs(t *testing.T) {
	if got := commandArgs("/list models"); got != "models" {
		t.Errorf("got %q", got)
	}
	if got := commandArgs("/list"); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestTelegramCommands_VerboseAndThink(t *testing.T) {
	loop := newNativeTestAgentLoop(config.DefaultConfig())
	ch, m := newMockTelegramChannel(t, nil, loop, nil)
	defer m.Close()
	c := ch.commands.(*cmd)
	ctx := context.Background()
	for _, lvl := range []string{"off", "basic", "full", "weird"} {
		if err := c.Verbose(ctx, *sampleTelegoMessage("/verbose", 1, 1), lvl); err != nil {
			t.Fatalf("verbose %s: %v", lvl, err)
		}
	}
	for _, lvl := range []string{"low", "medium", "high", "off", "weird"} {
		if err := c.Think(ctx, *sampleTelegoMessage("/think", 1, 1), lvl); err != nil {
			t.Fatalf("think %s: %v", lvl, err)
		}
	}
}