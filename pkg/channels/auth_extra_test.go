package channels

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

func newTestAuth(t *testing.T) *AuthManager {
	t.Helper()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}
	auth, err := NewAuthManager(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("NewAuthManager error = %v", err)
	}
	return auth
}

func TestAuthManager_TrackSessionKey(t *testing.T) {
	auth := newTestAuth(t)

	pending, err := auth.GeneratePIN("Dev")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	client, _, _, err := auth.PairWithPIN(pending.PIN, "Dev")
	if err != nil {
		t.Fatalf("PairWithPIN: %v", err)
	}

	// First tracking appends.
	auth.TrackSessionKey(client.ClientID, "keyA")
	got, ok := auth.GetClient(client.ClientID)
	if !ok {
		t.Fatal("GetClient failed")
	}
	if len(got.SessionKeys) != 2 {
		t.Errorf("expected 2 session keys after first track, got %d", len(got.SessionKeys))
	}

	// Duplicate is skipped.
	auth.TrackSessionKey(client.ClientID, "keyA")
	got, _ = auth.GetClient(client.ClientID)
	if len(got.SessionKeys) != 2 {
		t.Errorf("expected still 2 session keys after duplicate track, got %d", len(got.SessionKeys))
	}

	// Second append.
	auth.TrackSessionKey(client.ClientID, "keyB")
	got, _ = auth.GetClient(client.ClientID)
	if len(got.SessionKeys) != 3 {
		t.Errorf("expected 3 session keys, got %d", len(got.SessionKeys))
	}
}

func TestAuthManager_TrackSessionKey_EmptyAndUnknown(t *testing.T) {
	auth := newTestAuth(t)
	// Empty session key: no-op.
	auth.TrackSessionKey("unknown", "")
	// Unknown client: no-op (no panic).
	auth.TrackSessionKey("does-not-exist", "keyX")
}

func TestAuthManager_GetClient_Missing(t *testing.T) {
	auth := newTestAuth(t)
	if _, ok := auth.GetClient("nope"); ok {
		t.Error("expected ok=false for missing client")
	}
}

func TestAuthManager_RemoveSessionKey(t *testing.T) {
	auth := newTestAuth(t)
	pending, _ := auth.GeneratePIN("Dev")
	client, _, _, _ := auth.PairWithPIN(pending.PIN, "Dev")

	auth.TrackSessionKey(client.ClientID, "keyA")
	auth.TrackSessionKey(client.ClientID, "keyB")

	if err := auth.RemoveSessionKey(client.ClientID, "keyA"); err != nil {
		t.Fatalf("RemoveSessionKey: %v", err)
	}
	got, _ := auth.GetClient(client.ClientID)
	if len(got.SessionKeys) != 2 {
		t.Errorf("expected 2 keys after removing keyA (initial + keyB), got %d", len(got.SessionKeys))
	}
	for _, k := range got.SessionKeys {
		if k == "keyA" {
			t.Error("keyA should have been removed")
		}
	}

	// Removing a missing session key returns an error.
	if err := auth.RemoveSessionKey(client.ClientID, "ghost"); err == nil {
		t.Error("expected error removing a session key that does not exist")
	}
	// Removing from a missing client returns an error.
	if err := auth.RemoveSessionKey("ghost-client", "keyA"); err == nil {
		t.Error("expected error removing session key from missing client")
	}
}

func TestAuthManager_cleanupExpired(t *testing.T) {
	auth := newTestAuth(t)
	auth.mu.Lock()
	auth.store.PendingPINs["expiredpin"] = &PendingPIN{
		PIN:     "expiredpin",
		Expires: time.Now().Add(-time.Hour),
	}
	auth.store.Clients["expiredclient"] = &ClientInfo{
		ClientID: "expiredclient",
		Expires:  time.Now().Add(-time.Hour),
	}
	auth.store.PendingPINs["activepin"] = &PendingPIN{
		PIN:     "activepin",
		Expires: time.Now().Add(time.Hour),
	}
	auth.mu.Unlock()

	auth.cleanupExpired()

	auth.mu.RLock()
	defer auth.mu.RUnlock()
	if _, ok := auth.store.PendingPINs["expiredpin"]; ok {
		t.Error("expired PIN should have been removed")
	}
	if _, ok := auth.store.Clients["expiredclient"]; ok {
		t.Error("expired client should have been removed")
	}
	if _, ok := auth.store.PendingPINs["activepin"]; !ok {
		t.Error("active PIN should remain")
	}
}

func TestAuthManager_PairWithPIN_Expired(t *testing.T) {
	cfg := &config.NativeConfig{PinExpiryMinutes: 5, MaxClients: 5, TokenExpiryDays: 30}
	dir := t.TempDir()
	dbStore, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer dbStore.Close()

	auth, err := NewAuthManager(cfg, dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	auth.SetStore(dbStore.NativeClients())

	// Insert an already-expired pending PIN directly into the store.
	// With SQLite the repo branch of PairWithPIN does not reload the store,
	// so the expired entry is preserved and the expiry branch executes.
	auth.mu.Lock()
	auth.store.PendingPINs["123456"] = &PendingPIN{
		PIN:     "123456",
		Expires: time.Now().Add(-time.Minute),
	}
	auth.mu.Unlock()

	if _, _, _, err := auth.PairWithPIN("123456", "Dev"); err == nil {
		t.Error("expected error pairing with expired PIN")
	}
}

func TestAuthManager_PairWithPIN_DeviceNameMismatch(t *testing.T) {
	auth := newTestAuth(t)
	pending, _ := auth.GeneratePIN("DevA")
	if _, _, _, err := auth.PairWithPIN(pending.PIN, "DevB"); err == nil {
		t.Error("expected device name mismatch error")
	}
}

func TestAuthManager_PairWithPIN_NoDeviceName(t *testing.T) {
	auth := newTestAuth(t)
	pending, _ := auth.GeneratePIN("DevA")
	client, token, refreshToken, err := auth.PairWithPIN(pending.PIN, "")
	if err != nil {
		t.Fatalf("PairWithPIN: %v", err)
	}
	if client.DeviceName != "DevA" {
		t.Errorf("expected device name to fall back to pending name, got %q", client.DeviceName)
	}
	if token == "" || refreshToken == "" {
		t.Error("expected tokens")
	}
}

func TestAuthManager_PairWithPIN_MaxClientsReached(t *testing.T) {
	// Build a store already at capacity.
	auth := newTestAuth(t)
	auth.mu.Lock()
	auth.store.Clients["a"] = &ClientInfo{ClientID: "a", Expires: time.Now().Add(time.Hour)}
	auth.store.Clients["b"] = &ClientInfo{ClientID: "b", Expires: time.Now().Add(time.Hour)}
	auth.store.Clients["c"] = &ClientInfo{ClientID: "c", Expires: time.Now().Add(time.Hour)}
	auth.store.Clients["d"] = &ClientInfo{ClientID: "d", Expires: time.Now().Add(time.Hour)}
	auth.store.Clients["e"] = &ClientInfo{ClientID: "e", Expires: time.Now().Add(time.Hour)}
	auth.mu.Unlock()

	pending, _ := auth.GeneratePIN("Dev")
	if _, _, _, err := auth.PairWithPIN(pending.PIN, "Dev"); err == nil {
		t.Error("expected max clients error")
	}
}

func TestAuthManager_ValidateToken_ExpiredClient(t *testing.T) {
	auth := newTestAuth(t)
	auth.mu.Lock()
	auth.store.Clients["exp"] = &ClientInfo{
		ClientID:  "exp",
		TokenHash: hashToken("tok"),
		Expires:   time.Now().Add(-time.Minute),
	}
	auth.mu.Unlock()

	if _, valid := auth.ValidateToken("tok"); valid {
		t.Error("expected expired token to be invalid")
	}
}

func TestAuthManager_RefreshToken_ExpiredClientDeleted(t *testing.T) {
	auth := newTestAuth(t)
	auth.mu.Lock()
	auth.store.Clients["exp"] = &ClientInfo{
		ClientID:    "exp",
		RefreshHash: hashToken("ref"),
		Expires:     time.Now().Add(-time.Minute),
	}
	auth.mu.Unlock()

	if _, _, _, err := auth.RefreshToken("ref"); err == nil {
		t.Error("expected expired refresh error")
	}
	auth.mu.RLock()
	_, exists := auth.store.Clients["exp"]
	auth.mu.RUnlock()
	if exists {
		t.Error("expired client should be deleted during refresh")
	}
}

func TestAuthManager_saveStore(t *testing.T) {
	auth := newTestAuth(t)
	pending, _ := auth.GeneratePIN("Dev")
	// saveStore persists to JSON; a subsequent reload should see the client.
	_, token, _, _ := auth.PairWithPIN(pending.PIN, "Dev")

	// Reload a new manager from the same dir.
	cfg := &config.NativeConfig{PinExpiryMinutes: 5, MaxClients: 5, TokenExpiryDays: 30}
	auth2, err := NewAuthManager(cfg, auth.storePathDir(t))
	if err != nil {
		t.Fatalf("reload NewAuthManager: %v", err)
	}
	if _, valid := auth2.ValidateToken(token); !valid {
		t.Error("token should be valid after persistence reload")
	}
}

func (am *AuthManager) storePathDir(t *testing.T) string {
	t.Helper()
	return filepath.Dir(am.storePath)
}

func TestAuthManager_saveStore_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	// Create a corrupt store file.
	storePath := filepath.Join(dir, "native_clients.json")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(storePath, []byte("{invalid"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := &config.NativeConfig{PinExpiryMinutes: 5, MaxClients: 5, TokenExpiryDays: 30}
	auth, err := NewAuthManager(cfg, dir)
	if err != nil {
		// loadStore error returns the err; NewAuthManager warns and creates empty store.
		t.Logf("NewAuthManager returned err (expected for corrupt file): %v", err)
	}
	// Regardless, we can still operate with the empty fallback store.
	if auth != nil {
		pending, err2 := auth.GeneratePIN("Dev")
		if err2 != nil {
			t.Fatalf("GeneratePIN after corrupt load: %v", err2)
		}
		if pending.PIN == "" {
			t.Error("expected a PIN")
		}
	}
}

func TestAuthManager_SetStore_Nil(t *testing.T) {
	auth := newTestAuth(t)
	auth.SetStore(nil) // must be a no-op, not panic
	if auth.repo != nil {
		t.Error("repo should be nil")
	}
}

func TestAuthManager_SQLite_saveStoreStaleCleanup(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.NativeConfig{PinExpiryMinutes: 5, MaxClients: 5, TokenExpiryDays: 30}
	dbStore, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer dbStore.Close()

	auth, err := NewAuthManager(cfg, dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	auth.SetStore(dbStore.NativeClients())

	// Pair a client to persist in SQLite.
	pending, _ := auth.GeneratePIN("Dev")
	client, _, _, err := auth.PairWithPIN(pending.PIN, "Dev")
	if err != nil {
		t.Fatalf("PairWithPIN: %v", err)
	}

	// Now delete the client from the in-memory store and save; the stale
	// row in SQLite must be cleaned up.
	if err := auth.RemoveClient(client.ClientID); err != nil {
		t.Fatalf("RemoveClient: %v", err)
	}
	clients, err := dbStore.NativeClients().ListClients()
	if err != nil {
		t.Fatalf("ListClients: %v", err)
	}
	if len(clients) != 0 {
		t.Errorf("expected 0 clients after removal, got %d", len(clients))
	}
}
