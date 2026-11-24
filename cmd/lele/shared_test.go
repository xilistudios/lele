package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/mcp"
)

func TestGetConfigPath(t *testing.T) {
	path := getConfigPath()
	if path == "" {
		t.Error("getConfigPath() returned empty string")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("getConfigPath() = %q, want absolute path", path)
	}
}

func TestGetLeleDir(t *testing.T) {
	dir := getLeleDir()
	if dir == "" {
		t.Error("getLeleDir() returned empty string")
	}
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".lele")
	if dir != expected {
		t.Errorf("getLeleDir() = %q, want %q", dir, expected)
	}
}

func TestCopyDirectory_Basic(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	srcFile := filepath.Join(srcDir, "test.txt")
	if err := os.WriteFile(srcFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := copyDirectory(srcDir, filepath.Join(dstDir, "subdir")); err != nil {
		t.Fatalf("copyDirectory failed: %v", err)
	}

	dstFile := filepath.Join(dstDir, "subdir", "test.txt")
	data, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("copied content = %q, want %q", string(data), "hello")
	}
}

func TestCopyDirectory_NestedDirectories(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	nestedDir := filepath.Join(srcDir, "nested")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	nestedFile := filepath.Join(nestedDir, "file.txt")
	if err := os.WriteFile(nestedFile, []byte("nested content"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := copyDirectory(srcDir, dstDir); err != nil {
		t.Fatalf("copyDirectory failed: %v", err)
	}

	dstNestedFile := filepath.Join(dstDir, "nested", "file.txt")
	data, err := os.ReadFile(dstNestedFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "nested content" {
		t.Errorf("copied content = %q, want %q", string(data), "nested content")
	}
}

func TestCopyDirectory_SourceNotExist(t *testing.T) {
	dstDir := t.TempDir()

	err := copyDirectory("/nonexistent/path", dstDir)
	if err == nil {
		t.Error("copyDirectory should return error when source does not exist")
	}
}

const sharedTestMCPURL = "https://mcp.example.com/mcp"

func newFileKeyringService(t *testing.T, dir string) *keyring.Service {
	t.Helper()
	return keyring.NewService(keyring.ServiceConfig{
		Enabled:      true,
		VaultPath:    filepath.Join(dir, "keyring.enc"),
		Backend:      keyring.BackendFile,
		AuditLogSize: 100,
		LeleDir:      dir,
	})
}

func useMCPSecretVault(t *testing.T, svc *keyring.Service) {
	t.Helper()
	registerMCPSecretVault(svc)
	t.Cleanup(func() { mcp.RegisterSecretVaultFactory(nil) })
}

func mcpAuthFileBytes(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "mcp-auth.json"))
	if err != nil {
		t.Fatalf("read mcp-auth.json: %v", err)
	}
	return string(data)
}

func seedLegacyMCPAuthFile(t *testing.T, dir, name, clientSecret, accessToken string) {
	t.Helper()
	body := `{"servers":{"` + name + `":{"url":"` + sharedTestMCPURL + `","client_id":"cid-123",` +
		`"client_secret":"` + clientSecret + `","token":{"access_token":"` + accessToken + `","token_type":"Bearer"}}}}`
	if err := os.WriteFile(filepath.Join(dir, "mcp-auth.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write legacy mcp-auth.json: %v", err)
	}
}

func TestRegisterMCPSecretVault_StoresCredentialsInTheKeyring(t *testing.T) {
	dir := t.TempDir()
	svc := newFileKeyringService(t, dir)
	useMCPSecretVault(t, svc)

	store := mcp.NewAuthStore(dir)
	if !store.Enabled() {
		t.Fatal("the store is disabled: the factory did not resolve a vault for its own directory")
	}
	err := store.Save("api", mcp.AuthRecord{
		URL:                 sharedTestMCPURL,
		ClientID:            "cid-123",
		ClientSecret:        "client-secret-1",
		ResourceMetadataURL: "https://mcp.example.com/.well-known/oauth-protected-resource",
		Token: &transport.Token{
			AccessToken:  "access-1",
			RefreshToken: "refresh-1",
			TokenType:    "Bearer",
		},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	raw := mcpAuthFileBytes(t, dir)
	for _, secret := range []string{"access-1", "refresh-1", "client-secret-1"} {
		if strings.Contains(raw, secret) {
			t.Errorf("mcp-auth.json still holds %q: %s", secret, raw)
		}
	}
	for _, want := range []string{`"url":"` + sharedTestMCPURL + `"`, `"client_id":"cid-123"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("mcp-auth.json lost %s: %s", want, raw)
		}
	}

	blob, err := svc.GetRaw("mcp.api.auth")
	if err != nil {
		t.Fatalf("GetRaw(mcp.api.auth): %v", err)
	}
	for _, secret := range []string{"access-1", "refresh-1", "client-secret-1"} {
		if !strings.Contains(blob, secret) {
			t.Errorf("the vault blob is missing %q: %s", secret, blob)
		}
	}

	got, ok := store.Record("api")
	if !ok {
		t.Fatal("Record(api) = absent after a successful save")
	}
	if got.ClientSecret != "client-secret-1" || got.Token == nil || got.Token.AccessToken != "access-1" {
		t.Errorf("merged record lost its credentials: %+v", got)
	}
	if !store.Authenticated("api", sharedTestMCPURL) {
		t.Error("a vault-backed record is not reported as authenticated")
	}
}

func TestRegisterMCPSecretVault_MigratesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	seedLegacyMCPAuthFile(t, dir, "api", "legacy-secret", "legacy-token")
	svc := newFileKeyringService(t, dir)
	useMCPSecretVault(t, svc)

	store := mcp.NewAuthStore(dir)

	raw := mcpAuthFileBytes(t, dir)
	for _, secret := range []string{"legacy-secret", "legacy-token"} {
		if strings.Contains(raw, secret) {
			t.Errorf("mcp-auth.json still holds %q after migration: %s", secret, raw)
		}
	}
	blob, err := svc.GetRaw("mcp.api.auth")
	if err != nil {
		t.Fatalf("GetRaw(mcp.api.auth): %v", err)
	}
	for _, secret := range []string{"legacy-secret", "legacy-token"} {
		if !strings.Contains(blob, secret) {
			t.Errorf("the migrated blob is missing %q: %s", secret, blob)
		}
	}
	got, ok := store.Record("api")
	if !ok || got.ClientSecret != "legacy-secret" || got.Token == nil || got.Token.AccessToken != "legacy-token" {
		t.Errorf("record after migration = %+v (present=%v), want the full credential", got, ok)
	}
}

func TestRegisterMCPSecretVault_DeclinesOtherDirectories(t *testing.T) {
	svcDir := t.TempDir()
	svc := newFileKeyringService(t, svcDir)
	useMCPSecretVault(t, svc)

	otherDir := t.TempDir()
	store := mcp.NewAuthStore(otherDir)
	if err := store.TokenStore("api", sharedTestMCPURL).SaveToken(t.Context(),
		&transport.Token{AccessToken: "other-token", TokenType: "Bearer"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	if raw := mcpAuthFileBytes(t, otherDir); !strings.Contains(raw, "other-token") {
		t.Errorf("a store for a foreign directory must keep its token in its own file: %s", raw)
	}
	if _, err := svc.GetRaw("mcp.api.auth"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("the foreign store wrote into this installation's keyring: err = %v", err)
	}
}

func TestRegisterMCPSecretVault_DisabledModuleKeepsPlaintext(t *testing.T) {
	tests := []struct {
		name string
		svc  func(t *testing.T, dir string) *keyring.Service
	}{
		{name: "nil service", svc: func(*testing.T, string) *keyring.Service { return nil }},
		{
			name: "service built disabled",
			svc: func(t *testing.T, dir string) *keyring.Service {
				return keyring.NewService(keyring.ServiceConfig{
					Enabled:   false,
					VaultPath: filepath.Join(dir, "keyring.enc"),
					Backend:   keyring.BackendFile,
					LeleDir:   dir,
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			useMCPSecretVault(t, tt.svc(t, dir))

			store := mcp.NewAuthStore(dir)
			if err := store.TokenStore("api", sharedTestMCPURL).SaveToken(t.Context(),
				&transport.Token{AccessToken: "plain-token", TokenType: "Bearer"}); err != nil {
				t.Fatalf("save with the keyring module off must succeed: %v", err)
			}
			if raw := mcpAuthFileBytes(t, dir); !strings.Contains(raw, "plain-token") {
				t.Errorf("the credential did not land in mcp-auth.json: %s", raw)
			}
			if _, err := os.Stat(filepath.Join(dir, "keyring.enc")); err == nil {
				t.Error("a vault file was created with the keyring module disabled")
			}
		})
	}
}

func TestSameLeleDir(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)

	tests := []struct {
		name       string
		requested  string
		configured string
		want       bool
	}{
		{name: "identical absolute", requested: base, configured: base, want: true},
		{name: "trailing separator", requested: base + string(filepath.Separator), configured: base, want: true},
		{name: "dot segments", requested: filepath.Join(base, "sub", ".."), configured: base, want: true},
		{name: "relative vs absolute", requested: ".", configured: base, want: true},
		{name: "different directories", requested: base, configured: t.TempDir(), want: false},
		{name: "empty request", requested: "", configured: base, want: false},
		{name: "empty configured", requested: base, configured: "", want: false},
		{name: "both empty", requested: "", configured: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameLeleDir(tt.requested, tt.configured); got != tt.want {
				t.Errorf("sameLeleDir(%q, %q) = %v, want %v", tt.requested, tt.configured, got, tt.want)
			}
		})
	}
}
func TestLoadConfigWiresMCPSecretVault(t *testing.T) {
	t.Cleanup(func() { mcp.RegisterSecretVaultFactory(nil) })

	writeConfig := func(t *testing.T, keyringBlock string) {
		t.Helper()
		body := `{"keyring":{"backend":"file"` + keyringBlock + `}}`
		if err := os.WriteFile(filepath.Join(config.GetLeleDir(), "config.json"), []byte(body), 0o600); err != nil {
			t.Fatalf("write config.json: %v", err)
		}
	}

	t.Run("enabled keyring migrates into the vault", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("LELE_CONFIG_DIR", dir)
		writeConfig(t, "")
		seedLegacyMCPAuthFile(t, dir, "api", "legacy-secret", "legacy-token")

		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
		if !cfg.Keyring.Enabled {
			t.Fatal("fixture premise broken: the keyring module is disabled")
		}
		if _, ok := mcp.NewAuthStore(dir).Record("api"); !ok {
			t.Fatal("Record(api) = absent after the migration")
		}
		if raw := mcpAuthFileBytes(t, dir); strings.Contains(raw, "legacy-token") {
			t.Errorf("loadConfig did not wire the vault: mcp-auth.json still holds the token: %s", raw)
		}
		if _, err := os.Stat(filepath.Join(dir, "keyring.enc")); err != nil {
			t.Errorf("no keyring vault was written next to the config: %v", err)
		}
	})

	t.Run("disabled keyring keeps the file plaintext", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("LELE_CONFIG_DIR", dir)
		writeConfig(t, `,"enabled":false`)
		seedLegacyMCPAuthFile(t, dir, "api", "legacy-secret", "legacy-token")

		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
		if cfg.Keyring.Enabled {
			t.Fatal("fixture premise broken: the keyring module is enabled")
		}
		if _, ok := mcp.NewAuthStore(dir).Record("api"); !ok {
			t.Fatal("Record(api) = absent")
		}
		if raw := mcpAuthFileBytes(t, dir); !strings.Contains(raw, "legacy-token") {
			t.Errorf("a disabled keyring must leave mcp-auth.json untouched: %s", raw)
		}
		if _, err := os.Stat(filepath.Join(dir, "keyring.enc")); err == nil {
			t.Error("a vault file was created with the keyring module disabled")
		}
	})
}
