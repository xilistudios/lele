package main

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
)

func buildAgentLoop(t *testing.T) *agent.AgentLoop {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	if cfg.Agents.Defaults.Workspace == "" {
		cfg.Agents.Defaults.Workspace = dir
	}
	t.Setenv("LELE_CONFIG_DIR", dir)
	return agent.NewAgentLoop(cfg, bus.NewMessageBus())
}

// TestSimpleInteractiveMode_ExitInput feeds an "exit" line so the loop
// terminates immediately without invoking the provider.
func TestSimpleInteractiveMode_ExitInput(t *testing.T) {
	al := buildAgentLoop(t)
	p := newStdinPipe(t)
	p.feed("exit\n")
	p.close()
	out := runCmd(func() { simpleInteractiveMode(al, "cli:test") })
	if !strings.Contains(out, "You:") && out == "" {
		t.Errorf("expected prompt output, got: %s", out)
	}
}

// TestAgentParseCLIArgs is already covered by existing tests; add one for the
// combined flags form.
func TestAgentCmd_ParseCLIArgs_MessageFlags(t *testing.T) {
	m, s, d, v := parseCLIArgs([]string{"--message", "hello", "--session", "cli:x", "-d"})
	if m != "hello" {
		t.Errorf("message = %q, want hello", m)
	}
	if s != "cli:x" {
		t.Errorf("session = %q, want cli:x", s)
	}
	if !d {
		t.Error("debug should be true")
	}
	if v {
		t.Error("verbose should be false")
	}
}
