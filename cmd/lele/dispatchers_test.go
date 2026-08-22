package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

func storeOpenAt(t *testing.T, dir string) (*store.Store, error) {
	t.Helper()
	s, err := store.Open(filepath.Join(dir, "lele.db"))
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { s.Close() })
	return s, nil
}

func authUseStore(repo *store.AuthRepo) {
	auth.UseStore(repo)
}

// --- authCmd dispatcher ---

func TestAuthCmd_Status(t *testing.T) {
	dir := setupTestLeleDir(t)
	// Provide a valid config so store wiring succeeds.
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)

	replaceArgs(t, []string{"lele", "auth", "status"})
	out := runCmd(authCmd)
	if !strings.Contains(out, "No authenticated providers") {
		t.Errorf("expected no providers message, got: %s", out)
	}
}

func TestAuthCmd_NoArgs(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "auth"})
	out := runCmd(authCmd)
	if !strings.Contains(out, "Auth commands") {
		t.Errorf("expected auth help, got: %s", out)
	}
}

func TestAuthCmd_LoginMissingProvider(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "auth", "login"})
	out := runCmd(authCmd)
	if !strings.Contains(out, "--provider is required") {
		t.Errorf("expected provider required message, got: %s", out)
	}
}

func TestAuthCmd_LoginUnsupportedProvider(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "auth", "login", "--provider", "bogus"})
	out := runCmd(authCmd)
	if !strings.Contains(out, "Unsupported provider") {
		t.Errorf("expected unsupported provider message, got: %s", out)
	}
}

func TestAuthCmd_Unknown(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "auth", "frobnicate"})
	out := runCmd(authCmd)
	if !strings.Contains(out, "Unknown auth command") {
		t.Errorf("expected unknown command message, got: %s", out)
	}
}

func TestAuthStatusCmd_NoCredentials(t *testing.T) {
	setupTestLeleDir(t)
	authUseStore(nil) // reset any global repo state from other tests
	out := runCmd(authStatusCmd)
	if !strings.Contains(out, "No authenticated providers") {
		t.Errorf("expected no providers message, got: %s", out)
	}
}

func TestAuthStatusCmd_WithCredentials(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)

	// Seed the SQLite auth store via the store repo so authStatusCmd reads it.
	s, err := storeOpenAt(t, dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer s.Close()
	if err := s.Auth().SetCredential("openai", `{"method":"oauth"}`); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	authUseStore(s.Auth())

	out := runCmd(authStatusCmd)
	if !strings.Contains(out, "Authenticated Providers") {
		t.Errorf("expected authenticated providers message, got: %s", out)
	}
}

// --- cronCmd dispatcher ---

func TestCronCmd_Unknown(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "cron", "bogus"})
	out := runCmd(cronCmd)
	if !strings.Contains(out, "Unknown cron command") {
		t.Errorf("expected unknown cron message, got: %s", out)
	}
}

func TestCronCmd_NoArgs(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "cron"})
	out := runCmd(cronCmd)
	if !strings.Contains(out, "Cron commands") {
		t.Errorf("expected cron help, got: %s", out)
	}
}

// --- clientCmd dispatcher ---

func TestClientCmd_Unknown(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)
	replaceArgs(t, []string{"lele", "client", "bogus"})
	out := runCmd(clientCmd)
	if !strings.Contains(out, "Unknown client command") {
		t.Errorf("expected unknown client message, got: %s", out)
	}
}

func TestClientCmd_NoArgs(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "client"})
	out := runCmd(clientCmd)
	if !strings.Contains(out, "Client commands") {
		t.Errorf("expected client help, got: %s", out)
	}
}

func TestClientCmd_Status(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)
	replaceArgs(t, []string{"lele", "client", "status"})
	out := runCmd(clientCmd)
	if !strings.Contains(out, "Client Channel Status") {
		t.Errorf("expected status output, got: %s", out)
	}
}

// --- webCmd + migrateCmd dispatchers ---

func TestWebCmd_Messages(t *testing.T) {
	setupTestLeleDir(t)
	out := runCmd(webCmd)
	if !strings.Contains(out, "lele gateway") {
		t.Errorf("webCmd should mention gateway, got: %s", out)
	}
	if !strings.Contains(out, "http://") {
		t.Errorf("webCmd should print a url, got: %s", out)
	}
}

func TestMigrateCmd_Help(t *testing.T) {
	setupTestLeleDir(t)
	replaceArgs(t, []string{"lele", "migrate", "--help"})
	out := runCmd(migrateCmd)
	if !strings.Contains(out, "Migrate from OpenClaw") {
		t.Errorf("expected migrate help, got: %s", out)
	}
}

var _ = os.Getenv
var _ = config.DefaultConfig
