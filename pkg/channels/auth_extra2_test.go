package channels

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

// TestAuthManager_SaveStore_JSONMissingDir exercises the JSON saveStore body
// where the parent directory does not exist yet (Rename path).
func TestAuthManager_SaveStore_JSONMissingDir(t *testing.T) {
	base := t.TempDir()
	leleDir := filepath.Join(base, "nested", "lele")
	cfg := &config.NativeConfig{PinExpiryMinutes: 5, MaxClients: 5, TokenExpiryDays: 30}
	am, err := NewAuthManager(cfg, leleDir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	pending, err := am.GeneratePIN("Device")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}

	client, _, _, err := am.PairWithPIN(pending.PIN, "Device")
	if err != nil {
		t.Fatalf("PairWithPIN: %v", err)
	}
	if client == nil {
		t.Fatal("expected client")
	}

	data, err := os.ReadFile(filepath.Join(leleDir, "native_clients.json"))
	if err != nil {
		t.Fatalf("read store file: %v", err)
	}
	var st ClientStore
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("parse store: %v", err)
	}
	if len(st.Clients) != 1 {
		t.Errorf("expected 1 client in JSON store, got %d", len(st.Clients))
	}
}

// TestAuthManager_RegisterDesktopClient_Empty covers the empty-token guard.
func TestAuthManager_RegisterDesktopClient_Empty(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.NativeConfig{}
	am, err := NewAuthManager(cfg, dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	if err := am.RegisterDesktopClient("", "rt"); err == nil {
		t.Error("expected error for empty token")
	}
	if err := am.RegisterDesktopClient("tok", ""); err == nil {
		t.Error("expected error for empty refresh token")
	}
}

// TestAuthManager_LoadJSONStoreFromFile covers the legacy JSON file loader
// including the empty-map case and nil maps initialization.
func TestAuthManager_LoadJSONStoreFromFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.NativeConfig{}
	am, err := NewAuthManager(cfg, dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	// Missing file → (nil, nil).
	st, err := am.loadJSONStoreFromFile()
	if err != nil || st != nil {
		t.Errorf("missing file: st=%v err=%v", st, err)
	}

	// Present but empty JSON object initializes maps.
	if err := os.WriteFile(filepath.Join(dir, "native_clients.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	st, err = am.loadJSONStoreFromFile()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if st.PendingPINs == nil || st.Clients == nil {
		t.Error("maps should be initialized to non-nil")
	}
}

// TestAuthManager_SaveStore_SQLite exercises the SQLite saveStore path through
// SetStore migration + PairWithPIN persistence.
func TestAuthManager_SaveStore_SQLite(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	repo := s.NativeClients()
	cfg := &config.NativeConfig{PinExpiryMinutes: 5, MaxClients: 5, TokenExpiryDays: 30}
	am, err := NewAuthManager(cfg, dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	am.SetStore(repo)

	pending, err := am.GeneratePIN("Device")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	client, _, _, err := am.PairWithPIN(pending.PIN, "Device")
	if err != nil {
		t.Fatalf("PairWithPIN: %v", err)
	}
	raw, found, err := repo.GetClient(client.ClientID)
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}
	if !found || raw == "" {
		t.Fatal("client not persisted to sqlite")
	}
}

// TestAuthManager_saveStore_Wrapper explicitly invokes the exported-style
// saveStore wrapper (currently 0% coverage) to ensure it flushes to disk.
func TestAuthManager_saveStore_Wrapper(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.NativeConfig{PinExpiryMinutes: 5, MaxClients: 5, TokenExpiryDays: 30}
	am, err := NewAuthManager(cfg, dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	if err := am.saveStore(); err != nil {
		t.Fatalf("saveStore: %v", err)
	}

	// Verify the file was written.
	if _, err := os.Stat(filepath.Join(dir, "native_clients.json")); err != nil {
		t.Fatalf("store file not written: %v", err)
	}
}

var _ = time.Now