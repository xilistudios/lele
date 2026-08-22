package main

import (
	"strings"
	"testing"
)

func TestUpdateCmd_Help(t *testing.T) {
	replaceArgs(t, []string{"lele", "update", "--help"})
	out := runCmd(updateCmd)
	if !strings.Contains(out, "lele update") {
		t.Errorf("updateCmd --help should print help, got: %s", out)
	}
}

func TestOnboard_AbortOnExistingConfig(t *testing.T) {
	dir := setupTestLeleDir(t)
	// Create an existing config file.
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)

	// When asked "Overwrite?" answer no -> abort, no further prompts.
	p := newStdinPipe(t)
	// Overwrite? default false; respond "n" -> abort.
	p.feed("n\n")
	p.close()
	_ = captureStdout(t)
	onboard() // must not call os.Exit; returns after abort
}
