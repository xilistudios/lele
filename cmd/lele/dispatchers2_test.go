package main

import (
	"strings"
	"testing"
)

// TestClientCmd_RemoveNoID exercises the "<4 args" usage branch.
func TestClientCmd_RemoveNoID(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)
	replaceArgs(t, []string{"lele", "client", "remove"})
	out := runCmd(clientCmd)
	if !strings.Contains(out, "Usage: lele client remove") {
		t.Errorf("expected usage, got: %s", out)
	}
}

// TestClientCmd_Pending dispatches to clientPendingCmd.
func TestClientCmd_Pending(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)
	replaceArgs(t, []string{"lele", "client", "pending"})
	out := runCmd(clientCmd)
	if !strings.Contains(out, "No pending pairing requests") {
		t.Errorf("expected pending message, got: %s", out)
	}
}

// TestCronCmd_RemoveNoID exercises dispatcher arity branch.
func TestCronCmd_RemoveNoID(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "cron", "remove"})
	out := runCmd(cronCmd)
	if !strings.Contains(out, "Usage: lele cron remove") {
		t.Errorf("expected usage, got: %s", out)
	}
}

// TestCronCmd_AddThroughDispatch drives cronCmd with add flags.
func TestCronCmd_AddThroughDispatch(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "cron", "add", "--name", "job", "--message", "hi", "--every", "60"})
	out := runCmd(cronCmd)
	if !strings.Contains(out, "Added job") {
		t.Errorf("expected Added job, got: %s", out)
	}
}

// TestCronCmd_RemoveDispatch drives cronCmd remove for a non-existent job.
func TestCronCmd_RemoveDispatch(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "cron", "remove", "nonexistent"})
	out := runCmd(cronCmd)
	if !strings.Contains(out, "not found") {
		t.Errorf("expected not found, got: %s", out)
	}
}