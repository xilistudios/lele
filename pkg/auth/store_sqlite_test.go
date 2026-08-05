package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

func openSQLiteStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open() failed: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store.Close() failed: %v", err)
		}
	})
	return s
}

func useTestAuthRepo(t *testing.T, repo *store.AuthRepo) {
	t.Helper()
	UseStore(repo)
	t.Cleanup(func() { UseStore(nil) })
}

func TestUseStore_CRUD(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	s := openSQLiteStore(t)
	useTestAuthRepo(t, s.Auth())

	cred := &AuthCredential{
		AccessToken:  "sql-access-token",
		RefreshToken: "sql-refresh-token",
		AccountID:    "acct-456",
		ProjectID:    "proj-1",
		Email:        "user@example.com",
		ExpiresAt:    time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		Provider:     "openai",
		AuthMethod:   "oauth",
	}

	// Set credential
	if err := SetCredential("openai", cred); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	// Get credential
	loaded, err := GetCredential("openai")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if loaded == nil {
		t.Fatal("GetCredential() returned nil, want non-nil")
	}
	if loaded.AccessToken != cred.AccessToken {
		t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, cred.AccessToken)
	}
	if loaded.RefreshToken != cred.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", loaded.RefreshToken, cred.RefreshToken)
	}
	if loaded.AccountID != cred.AccountID {
		t.Errorf("AccountID = %q, want %q", loaded.AccountID, cred.AccountID)
	}
	if loaded.ProjectID != cred.ProjectID {
		t.Errorf("ProjectID = %q, want %q", loaded.ProjectID, cred.ProjectID)
	}
	if loaded.Email != cred.Email {
		t.Errorf("Email = %q, want %q", loaded.Email, cred.Email)
	}
	if loaded.Provider != cred.Provider {
		t.Errorf("Provider = %q, want %q", loaded.Provider, cred.Provider)
	}
	if loaded.AuthMethod != cred.AuthMethod {
		t.Errorf("AuthMethod = %q, want %q", loaded.AuthMethod, cred.AuthMethod)
	}
	if !loaded.ExpiresAt.Equal(cred.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", loaded.ExpiresAt, cred.ExpiresAt)
	}

	// Get missing credential
	missing, err := GetCredential("missing")
	if err != nil {
		t.Fatalf("GetCredential(\"missing\") error: %v", err)
	}
	if missing != nil {
		t.Errorf("GetCredential(\"missing\") = %v, want nil", missing)
	}

	// Delete credential
	if err := DeleteCredential("openai"); err != nil {
		t.Fatalf("DeleteCredential() error: %v", err)
	}
	deleted, err := GetCredential("openai")
	if err != nil {
		t.Fatalf("GetCredential() after delete error: %v", err)
	}
	if deleted != nil {
		t.Errorf("GetCredential() after delete = %v, want nil", deleted)
	}

	// Set multiple and delete all
	if err := SetCredential("openai", cred); err != nil {
		t.Fatalf("SetCredential(\"openai\") error: %v", err)
	}
	if err := SetCredential("anthropic", cred); err != nil {
		t.Fatalf("SetCredential(\"anthropic\") error: %v", err)
	}
	if err := DeleteAllCredentials(); err != nil {
		t.Fatalf("DeleteAllCredentials() error: %v", err)
	}
	store, err := LoadStore()
	if err != nil {
		t.Fatalf("LoadStore() error: %v", err)
	}
	if len(store.Credentials) != 0 {
		t.Errorf("LoadStore() after DeleteAll: len(Credentials) = %d, want 0", len(store.Credentials))
	}
}

func TestUseStore_LazyMigration(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	// Use legacy JSON backend (authRepo is nil by default)
	UseStore(nil)

	legacyStore := &AuthStore{
		Credentials: map[string]*AuthCredential{
			"openai": {
				AccessToken: "legacy-token-openai",
				Provider:    "openai",
				AuthMethod:  "oauth",
			},
			"anthropic": {
				AccessToken: "legacy-token-anthropic",
				Provider:    "anthropic",
				AuthMethod:  "api_key",
			},
		},
	}
	if err := SaveStore(legacyStore); err != nil {
		t.Fatalf("SaveStore() legacy error: %v", err)
	}

	// Verify auth.json exists
	if _, err := os.Stat(filepath.Join(tmpDir, "auth.json")); err != nil {
		t.Fatalf("auth.json should exist: %v", err)
	}

	// Switch to SQLite backend
	s := openSQLiteStore(t)
	useTestAuthRepo(t, s.Auth())

	// GetCredential should trigger migration and return migrated data
	loaded, err := GetCredential("openai")
	if err != nil {
		t.Fatalf("GetCredential(\"openai\") error: %v", err)
	}
	if loaded == nil {
		t.Fatal("GetCredential(\"openai\") returned nil, want non-nil")
	}
	if loaded.AccessToken != "legacy-token-openai" {
		t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, "legacy-token-openai")
	}

	// Verify both credentials exist in SQLite
	rows, err := s.Auth().ListCredentials()
	if err != nil {
		t.Fatalf("ListCredentials() error: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("ListCredentials() len = %d, want 2", len(rows))
	}

	// Verify legacy auth.json still exists on disk (migration never deletes it)
	if _, err := os.Stat(filepath.Join(tmpDir, "auth.json")); err != nil {
		t.Errorf("auth.json should still exist after migration: %v", err)
	}
}

func TestUseStore_NilFallback(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	useTestAuthRepo(t, nil)

	cred := &AuthCredential{
		AccessToken: "json-fallback-token",
		Provider:    "openai",
		AuthMethod:  "oauth",
	}
	if err := SetCredential("openai", cred); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	// Verify auth.json exists on disk
	if _, err := os.Stat(filepath.Join(tmpDir, "auth.json")); err != nil {
		t.Fatalf("auth.json should exist: %v", err)
	}

	// Round-trip through GetCredential
	loaded, err := GetCredential("openai")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if loaded == nil {
		t.Fatal("GetCredential() returned nil, want non-nil")
	}
	if loaded.AccessToken != "json-fallback-token" {
		t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, "json-fallback-token")
	}
}
