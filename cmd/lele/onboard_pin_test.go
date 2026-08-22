package main

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func TestMaybeGeneratePIN_Success(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Channels.Web.Enabled = true

	leleDir := t.TempDir()
	p := newStdinPipe(t)
	p.feedLines(
		"y",       // Generate pairing PIN? yes
		"TestDev", // Device name
	)
	p.close()
	_ = captureStdout(t)
	out := runCmd(func() { maybeGeneratePIN(cfg, leleDir) })
	if !strings.Contains(out, "Pairing PIN generated") {
		t.Errorf("expected PIN generation, got: %s", out)
	}
	if !strings.Contains(out, "PIN:") {
		t.Errorf("expected PIN value, got: %s", out)
	}
}

// TestConfigureModels_Empty covers the empty-alias break path.
func TestConfigureModels_EmptyAlias(t *testing.T) {
	p := newStdinPipe(t)
	p.feed("\n") // Alias name empty -> break
	p.close()
	_ = captureStdout(t)
	models := configureModels("test")
	if models == nil {
		t.Fatal("expected non-nil models map")
	}
	if len(models) != 0 {
		t.Errorf("expected empty models, got %d", len(models))
	}
}

var _ = config.DefaultConfig
