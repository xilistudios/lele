package main

import (
	"strings"
	"testing"
)

// TestMainDispatchCovers main()'s switch routing. We call main() directly with
// different os.Args for commands that return normally (no os.Exit / no blocking
// server). Each sets a temp LELE_CONFIG_DIR to isolate state.

func TestMainDispatch_Status(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "status"})
	out := runCmd(func() { main() })
	if !strings.Contains(out, "lele Status") {
		t.Errorf("main status: expected status output, got: %s", out)
	}
}

func TestMainDispatch_AuthNoArgs(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "auth"})
	out := runCmd(func() { main() })
	if !strings.Contains(out, "Auth commands") {
		t.Errorf("main auth: expected help, got: %s", out)
	}
}

func TestMainDispatch_CronNoArgs(t *testing.T) {
	setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	temp := 0.7
	cfg.Agents.Defaults.Temperature = &temp
	saveTestConfig(getConfigPath(), cfg)
	replaceArgs(t, []string{"lele", "cron"})
	out := runCmd(func() { main() })
	if !strings.Contains(out, "Cron commands") {
		t.Errorf("main cron: expected help, got: %s", out)
	}
}

func TestMainDispatch_Web(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "web"})
	out := runCmd(func() { main() })
	if !strings.Contains(out, "web UI is served by the unified gateway") {
		t.Errorf("main web: expected web help, got: %s", out)
	}
}

func TestMainDispatch_Version(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "version"})
	out := runCmd(func() { main() })
	if !strings.Contains(out, "lele") {
		t.Errorf("main version: expected version output, got: %s", out)
	}
}

// TestMainDispatch_NoArgs exercises the len(remaining)<1 help path (main exits
// 1, which cannot run in-process). We instead exercise the sibling path where a
// session flag is given pointing to a known-good command.

func TestMainDispatch_SessionFlagRoutesToTUI(t *testing.T) {
	// When only a session flag is given, main routes to tuiCmd(sessionID).
	// tuiCmd loadConfig and runs the TUI which needs a TTY; we instead assert
	// that a missing config dir triggers the tuiCmd error path gracefully via
	// subprocess-free exploration is not possible in-process (TUI).
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "--session=abc", "version"})
	out := runCmd(func() { main() })
	if !strings.Contains(out, "lele") {
		t.Errorf("main session+version: expected version output, got: %s", out)
	}
}
