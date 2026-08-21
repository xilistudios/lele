package main

import (
	"strings"
	"testing"
)

func TestAuthLogoutCmd_Provider(t *testing.T) {
	setupTestLeleDir(t)
	authUseStore(nil)
	replaceArgs(t, []string{"lele", "auth", "logout", "--provider", "openai"})
	out := runCmd(authLogoutCmd)
	if !strings.Contains(out, "Logged out from openai") {
		t.Errorf("expected logout message, got: %s", out)
	}
}

func TestAuthLogoutCmd_All(t *testing.T) {
	setupTestLeleDir(t)
	authUseStore(nil)
	replaceArgs(t, []string{"lele", "auth", "logout"})
	out := runCmd(authLogoutCmd)
	if !strings.Contains(out, "Logged out from all providers") {
		t.Errorf("expected logout-all message, got: %s", out)
	}
}