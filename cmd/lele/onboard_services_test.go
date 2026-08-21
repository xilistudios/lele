package main

import (
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// TestCronCmd_List covers the dispatcher's "list" branch end to end.
func TestCronCmd_List(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "cron", "list"})
	out := runCmd(cronCmd)
	if !strings.Contains(out, "No scheduled jobs") {
		t.Errorf("expected no jobs message, got: %s", out)
	}
}

// TestClientCmd_Pin covers the dispatcher's "pin" branch end to end.
func TestClientCmd_Pin(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)
	replaceArgs(t, []string{"lele", "client", "pin"})
	out := runCmd(clientCmd)
	if !strings.Contains(out, "Pairing PIN Generated") {
		t.Errorf("expected PIN generated, got: %s", out)
	}
}

// TestClientCmd_ListEmpty covers the dispatcher's "list" branch.
func TestClientCmd_ListEmpty(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)
	replaceArgs(t, []string{"lele", "client", "list"})
	out := runCmd(clientCmd)
	if !strings.Contains(out, "No paired clients") {
		t.Errorf("expected no clients, got: %s", out)
	}
}

// TestMaybeStartServices_Disabled returns early when web ui is disabled.
func TestMaybeStartServices_Disabled(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Channels.Web.Enabled = false
	out := runCmd(func() { maybeStartServices(cfg) })
	if strings.Contains(out, "Starting gateway") {
		t.Errorf("should not start services when web disabled")
	}
}

// TestMaybeStartServices_Declined: web enabled but user declines starting now.
func TestMaybeStartServices_Declined(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Channels.Web.Enabled = true

	p := newStdinPipe(t)
	p.feed("n\n") // "Start services now?" -> no
	p.close()
	_ = captureStdout(t)
	out := runCmd(func() { maybeStartServices(cfg) })
	if !strings.Contains(out, "To start services manually") {
		t.Errorf("expected manual-start hint, got: %s", out)
	}
}

// TestMaybeGeneratePIN_Disabled returns early when web ui is disabled.
func TestMaybeGeneratePIN_Disabled(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Channels.Web.Enabled = false
	out := runCmd(func() { maybeGeneratePIN(cfg, t.TempDir()) })
	if out != "" {
		t.Errorf("expected no output when web disabled, got: %q", out)
	}
}

var _ = config.DefaultConfig
var _ = os.Getenv