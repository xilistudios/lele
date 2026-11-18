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

func TestLogoConstant(t *testing.T) {
	if logo != "🦞" {
		t.Errorf("logo = %q, want %q", logo, "🦞")
	}
}

// ---------------------------------------------------------------------------
// MCP secret vault wiring
// ---------------------------------------------------------------------------

// These tests pin the PRODUCTION wiring of pkg/mcp's secret vault: the factory
// that loadConfig installs (through registerKeyringResolver → the same keyring
// Service as the {{SECRET:}} resolver). They drive the real registration helper
// and the real mcp.NewAuthStore, so a regression in either half — the factory
// not registered, registered with a different Service, or answering for a
// directory it does not own — fails here.

const sharedTestMCPURL = "https://mcp.example.com/mcp"

// newFileKeyringService builds a file-backed keyring Service rooted at dir, so
// no test ever reaches the OS keychain.
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

// useMCPSecretVault registers svc as the process-wide vault for one test and
// restores the package default (no factory ⇒ legacy plaintext mode) afterwards,
// so the tests of this package that never wire a keyring stay untouched.
func useMCPSecretVault(t *testing.T, svc *keyring.Service) {
	t.Helper()
	registerMCPSecretVault(svc)
	t.Cleanup(func() { mcp.RegisterSecretVaultFactory(nil) })
}

// mcpAuthFileBytes returns the raw bytes of <dir>/mcp-auth.json, failing the
// test when the file is missing: every case below saves a record first.
func mcpAuthFileBytes(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "mcp-auth.json"))
	if err != nil {
		t.Fatalf("read mcp-auth.json: %v", err)
	}
	return string(data)
}

// seedLegacyMCPAuthFile writes the PRE-vault shape of mcp-auth.json: a record
// that still carries its credential half in the file.
func seedLegacyMCPAuthFile(t *testing.T, dir, name, clientSecret, accessToken string) {
	t.Helper()
	body := `{"servers":{"` + name + `":{"url":"` + sharedTestMCPURL + `","client_id":"cid-123",` +
		`"client_secret":"` + clientSecret + `","token":{"access_token":"` + accessToken + `","token_type":"Bearer"}}}}`
	if err := os.WriteFile(filepath.Join(dir, "mcp-auth.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write legacy mcp-auth.json: %v", err)
	}
}

// TestRegisterMCPSecretVault_StoresCredentialsInTheKeyring is the end-to-end
// wiring assertion: with the factory registered from an enabled Service, a
// saved record keeps NO credential in mcp-auth.json and every one of them is
// readable from the keyring under "mcp.<server>.auth".
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
	// The non-secret half stays in the file: it is the record's identity.
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

	// The store still answers with the whole record (the vault is overlaid on
	// every read), so nothing downstream notices the split.
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

// TestRegisterMCPSecretVault_MigratesLegacyFile: an existing plaintext
// mcp-auth.json is migrated by the first store opened through the factory —
// secrets into the keyring, file rewritten once without them.
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
	// And the migration is lossless from the store's point of view.
	got, ok := store.Record("api")
	if !ok || got.ClientSecret != "legacy-secret" || got.Token == nil || got.Token.AccessToken != "legacy-token" {
		t.Errorf("record after migration = %+v (present=%v), want the full credential", got, ok)
	}
}

// TestRegisterMCPSecretVault_DeclinesOtherDirectories: the factory answers for
// ONE lele directory. A store opened for another directory must fall back to
// plaintext in its own file rather than writing this installation's keyring —
// which is exactly the shape of a second `-c` dir, and of a package test
// inheriting a factory registered by an earlier test.
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

// TestRegisterMCPSecretVault_DisabledModuleKeepsPlaintext pins the degradation
// path: a nil Service (keyring switched off in config) and a Service that was
// built disabled both register NO factory, so mcp-auth.json keeps holding the
// credential exactly as it did before the vault existed. A disabled module must
// never turn every login into an ErrDisabled failure.
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

// TestSameLeleDir pins the identity rule the factory relies on: two spellings
// of one directory match (absolute/cleaned), different directories and empty
// strings do not.
func TestSameLeleDir(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base) // makes the relative spellings below deterministic

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
} // TestLoadConfigWiresMCPSecretVault pins the REGISTRATION POINT: loadConfig —
// the shared setup every entry point (gateway, TUI, plain agent CLI, `lele mcp
// …`) runs first — is what installs the MCP vault factory, from the same keyring
// Service as the {{SECRET:}} resolver.
//
// The assertion is behavioural, not structural: a LEGACY plaintext mcp-auth.json
// in the config's own lele directory is migrated by the first store opened after
// loadConfig (secrets to the keyring, file rewritten without them). With the
// factory unregistered — the state these tests start in — the file keeps its
// token and no vault file appears, which is exactly what the disabled subtest
// pins as the other side of the contract.
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
		// loadConfig installed the factory; a store for its own directory must
		// therefore migrate the legacy credential on construction.
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
