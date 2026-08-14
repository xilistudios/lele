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

func TestAuthManager_GeneratePIN(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	pending, err := auth.GeneratePIN("TestDevice")
	if err != nil {
		t.Fatalf("failed to generate PIN: %v", err)
	}

	if len(pending.PIN) != 6 {
		t.Errorf("expected 6-digit PIN, got %s", pending.PIN)
	}

	if pending.DeviceName != "TestDevice" {
		t.Errorf("expected device name 'TestDevice', got '%s'", pending.DeviceName)
	}

	if pending.Expires.Before(time.Now()) {
		t.Error("PIN should expire in the future")
	}
}

func TestAuthManager_GeneratePIN_MaxPending(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	// Generate PINs up to the internal limit (10 pending max).
	for i := 0; i < 10; i++ {
		if _, err := auth.GeneratePIN("Device"); err != nil {
			t.Fatalf("failed to generate PIN %d: %v", i, err)
		}
	}

	// The 11th should be rejected.
	_, err = auth.GeneratePIN("Device")
	if err == nil {
		t.Error("expected error when exceeding max pending PINs")
	}
}

func TestAuthManager_PairWithPIN(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	pending, err := auth.GeneratePIN("TestDevice")
	if err != nil {
		t.Fatalf("failed to generate PIN: %v", err)
	}

	client, token, refreshToken, err := auth.PairWithPIN(pending.PIN, "TestDevice")
	if err != nil {
		t.Fatalf("failed to pair: %v", err)
	}

	if client == nil {
		t.Fatal("expected non-nil client")
	}

	if token == "" {
		t.Error("expected non-empty token")
	}

	if refreshToken == "" {
		t.Error("expected non-empty refresh token")
	}

	if client.DeviceName != "TestDevice" {
		t.Errorf("expected device name 'TestDevice', got '%s'", client.DeviceName)
	}

	if client.Expires.Before(time.Now()) {
		t.Error("client should expire in the future")
	}
}

func TestAuthManager_PairWithPIN_InvalidPIN(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	_, _, _, err = auth.PairWithPIN("000000", "TestDevice")
	if err == nil {
		t.Error("expected error when pairing with invalid PIN")
	}
}

func TestAuthManager_ValidateToken(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	pending, err := auth.GeneratePIN("TestDevice")
	if err != nil {
		t.Fatalf("failed to generate PIN: %v", err)
	}

	_, token, _, err := auth.PairWithPIN(pending.PIN, "TestDevice")
	if err != nil {
		t.Fatalf("failed to pair: %v", err)
	}

	client, valid := auth.ValidateToken(token)
	if !valid {
		t.Error("expected token to be valid")
	}

	if client == nil {
		t.Fatal("expected non-nil client")
	}

	if client.DeviceName != "TestDevice" {
		t.Errorf("expected device name 'TestDevice', got '%s'", client.DeviceName)
	}
}

func TestAuthManager_ValidateToken_Invalid(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	_, valid := auth.ValidateToken("invalid-token")
	if valid {
		t.Error("expected invalid token to be rejected")
	}
}

func TestAuthManager_RefreshToken(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	pending, err := auth.GeneratePIN("TestDevice")
	if err != nil {
		t.Fatalf("failed to generate PIN: %v", err)
	}

	_, _, refreshToken, err := auth.PairWithPIN(pending.PIN, "TestDevice")
	if err != nil {
		t.Fatalf("failed to pair: %v", err)
	}

	client, newToken, newRefreshToken, err := auth.RefreshToken(refreshToken)
	if err != nil {
		t.Fatalf("failed to refresh token: %v", err)
	}

	if newToken == "" {
		t.Error("expected non-empty new token")
	}

	if newRefreshToken == "" {
		t.Error("expected non-empty new refresh token")
	}

	if client == nil {
		t.Fatal("expected non-nil client")
	}

	_, valid := auth.ValidateToken(newToken)
	if !valid {
		t.Error("expected new token to be valid")
	}
}

func TestAuthManager_RefreshToken_Invalid(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	_, _, _, err = auth.RefreshToken("invalid-refresh-token")
	if err == nil {
		t.Error("expected error when refreshing with invalid token")
	}
}

func TestAuthManager_RemoveClient(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	pending, err := auth.GeneratePIN("TestDevice")
	if err != nil {
		t.Fatalf("failed to generate PIN: %v", err)
	}

	client, _, _, err := auth.PairWithPIN(pending.PIN, "TestDevice")
	if err != nil {
		t.Fatalf("failed to pair: %v", err)
	}

	err = auth.RemoveClient(client.ClientID)
	if err != nil {
		t.Fatalf("failed to remove client: %v", err)
	}

	clients := auth.ListClients()
	if len(clients) != 0 {
		t.Errorf("expected 0 clients, got %d", len(clients))
	}
}

func TestAuthManager_RemoveClient_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	err = auth.RemoveClient("non-existent-client")
	if err == nil {
		t.Error("expected error when removing non-existent client")
	}
}

func TestAuthManager_ListClients(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	clients := auth.ListClients()
	if len(clients) != 0 {
		t.Errorf("expected 0 clients initially, got %d", len(clients))
	}

	pending, err := auth.GeneratePIN("TestDevice")
	if err != nil {
		t.Fatalf("failed to generate PIN: %v", err)
	}

	_, _, _, err = auth.PairWithPIN(pending.PIN, "TestDevice")
	if err != nil {
		t.Fatalf("failed to pair: %v", err)
	}

	clients = auth.ListClients()
	if len(clients) != 1 {
		t.Errorf("expected 1 client, got %d", len(clients))
	}
}

func TestAuthManager_GetPendingPINs(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	pins := auth.GetPendingPINs()
	if len(pins) != 0 {
		t.Errorf("expected 0 pending PINs initially, got %d", len(pins))
	}

	_, err = auth.GeneratePIN("TestDevice")
	if err != nil {
		t.Fatalf("failed to generate PIN: %v", err)
	}

	pins = auth.GetPendingPINs()
	if len(pins) != 1 {
		t.Errorf("expected 1 pending PIN, got %d", len(pins))
	}
}

func TestAuthManager_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth1, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create first auth manager: %v", err)
	}

	pending, err := auth1.GeneratePIN("TestDevice")
	if err != nil {
		t.Fatalf("failed to generate PIN: %v", err)
	}

	_, token, _, err := auth1.PairWithPIN(pending.PIN, "TestDevice")
	if err != nil {
		t.Fatalf("failed to pair: %v", err)
	}

	auth2, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create second auth manager: %v", err)
	}

	client, valid := auth2.ValidateToken(token)
	if !valid {
		t.Error("expected token to be valid after reload")
	}

	if client == nil {
		t.Fatal("expected non-nil client after reload")
	}

	if client.DeviceName != "TestDevice" {
		t.Errorf("expected device name 'TestDevice', got '%s'", client.DeviceName)
	}
}

func TestAuthManager_UpdateLastSeen(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	pending, err := auth.GeneratePIN("TestDevice")
	if err != nil {
		t.Fatalf("failed to generate PIN: %v", err)
	}

	client, _, _, err := auth.PairWithPIN(pending.PIN, "TestDevice")
	if err != nil {
		t.Fatalf("failed to pair: %v", err)
	}

	originalLastSeen := client.LastSeen
	time.Sleep(10 * time.Millisecond)

	auth.UpdateLastSeen(client.ClientID)

	if !client.LastSeen.After(originalLastSeen) {
		t.Error("expected LastSeen to be updated")
	}
}

func TestAuthManager_MaxClients(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       2,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	for i := 0; i < 2; i++ {
		pending, err := auth.GeneratePIN("TestDevice")
		if err != nil {
			t.Fatalf("failed to generate PIN: %v", err)
		}
		_, _, _, err = auth.PairWithPIN(pending.PIN, "TestDevice")
		if err != nil {
			t.Fatalf("failed to pair client %d: %v", i, err)
		}
	}

	pending, err := auth.GeneratePIN("TestDevice3")
	if err != nil {
		t.Fatalf("failed to generate PIN for third client: %v", err)
	}

	_, _, _, err = auth.PairWithPIN(pending.PIN, "TestDevice3")
	if err == nil {
		t.Error("expected error when exceeding max clients")
	}
}

func TestAuthManager_PINExpiry(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 0,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	storePath := filepath.Join(tmpDir, "native_clients.json")
	if _, err := os.Stat(storePath); os.IsNotExist(err) {
		if err := os.WriteFile(storePath, []byte("{}"), 0644); err != nil {
			t.Fatalf("failed to create store file: %v", err)
		}
	}

	pending, err := auth.GeneratePIN("TestDevice")
	if err != nil {
		t.Fatalf("failed to generate PIN: %v", err)
	}

	_, _, _, err = auth.PairWithPIN(pending.PIN, "TestDevice")
	if err != nil {
		t.Fatalf("failed to pair: %v", err)
	}
}

func TestAuthManager_SQLitePicksUpCLIPendingPIN(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	// Simulate CLI: creates an AuthManager without SQLite, generates a PIN.
	// This writes the PIN to native_clients.json.
	cliAuth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("CLI: failed to create auth manager: %v", err)
	}
	pending, err := cliAuth.GeneratePIN("CLI-Device")
	if err != nil {
		t.Fatalf("CLI: failed to generate PIN: %v", err)
	}

	// Verify the PIN was written to the JSON file.
	storePath := filepath.Join(tmpDir, "native_clients.json")
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("expected native_clients.json to exist: %v", err)
	}
	var jsonStore ClientStore
	if err := json.Unmarshal(data, &jsonStore); err != nil {
		t.Fatalf("failed to parse native_clients.json: %v", err)
	}
	if _, ok := jsonStore.PendingPINs[pending.PIN]; !ok {
		t.Fatal("PIN not found in native_clients.json")
	}

	// Simulate server: creates an AuthManager, then migrates to SQLite.
	// The server must pick up the CLI's pending PIN from the JSON file.
	dbPath := filepath.Join(tmpDir, "test.db")
	dbStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("server: failed to open store: %v", err)
	}
	defer dbStore.Close()

	serverAuth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("server: failed to create auth manager: %v", err)
	}
	serverAuth.SetStore(dbStore.NativeClients())

	// Pair with the PIN generated by the CLI — must succeed.
	// Use empty deviceName to skip the name-match check (mirrors webui behavior
	// where the browser doesn't know the CLI's device name).
	client, token, refreshToken, err := serverAuth.PairWithPIN(pending.PIN, "")
	if err != nil {
		t.Fatalf("server: PairWithPIN failed: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if token == "" {
		t.Error("expected non-empty token")
	}
	if refreshToken == "" {
		t.Error("expected non-empty refresh token")
	}

	// Token must be valid.
	_, valid := serverAuth.ValidateToken(token)
	if !valid {
		t.Error("expected token to be valid after cross-process pairing")
	}
}

func TestAuthManager_SQLiteServerPINWithPairInSameProcess(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	dbStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer dbStore.Close()

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}
	auth.SetStore(dbStore.NativeClients())

	// Generate PIN via server (same process, SQLite active).
	pending, err := auth.GeneratePIN("Server-Device")
	if err != nil {
		t.Fatalf("GeneratePIN failed: %v", err)
	}

	// Pair immediately — PINs generated in-process must still work.
	client, token, _, err := auth.PairWithPIN(pending.PIN, "Server-Device")
	if err != nil {
		t.Fatalf("PairWithPIN failed: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if token == "" {
		t.Error("expected non-empty token")
	}
}

// TestAuthManager_SQLitePairAfterCLIGeneratesPIN tests the real-world
// scenario: server starts (SetStore called), THEN the CLI generates a
// PIN (writes to JSON file), THEN the webui tries to pair with that PIN.
// This is the exact flow that caused "invalid PIN" before the fix.
func TestAuthManager_SQLitePairAfterCLIGeneratesPIN(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	// Step 1: Server starts. SetStore is called during initialization.
	dbPath := filepath.Join(tmpDir, "test.db")
	dbStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("server: failed to open store: %v", err)
	}
	defer dbStore.Close()

	serverAuth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("server: failed to create auth manager: %v", err)
	}
	serverAuth.SetStore(dbStore.NativeClients())

	// Step 2: CLI generates a PIN AFTER the server started.
	// This is a separate process — no SQLite, writes to JSON file.
	cliAuth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("CLI: failed to create auth manager: %v", err)
	}
	pending, err := cliAuth.GeneratePIN("CLI-Device")
	if err != nil {
		t.Fatalf("CLI: failed to generate PIN: %v", err)
	}

	// Step 3: Webui tries to pair with the CLI's PIN.
	// PairWithPIN must read the JSON file to find the PIN.
	client, token, _, err := serverAuth.PairWithPIN(pending.PIN, "")
	if err != nil {
		t.Fatalf("server: PairWithPIN failed: %v — PIN was %s", err, pending.PIN)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if token == "" {
		t.Error("expected non-empty token")
	}

	// Token must be valid.
	_, valid := serverAuth.ValidateToken(token)
	if !valid {
		t.Error("expected token to be valid after cross-process pairing")
	}
}

// TestAuthManager_SQLiteSetStoreReloadsClientsAfterRestart simulates the
// real-world restart scenario where:
//  1. Server starts, SQLite store is wired via SetStore
//  2. Client pairs → client exists only in SQLite (JSON is stale)
//  3. Server restarts → NewAuthManager loads from stale JSON (missing client)
//  4. SetStore is called again → must reload from SQLite to recover the client
//
// Before the fix, SetStore did NOT reload from SQLite, so the client was lost
// and ValidateToken returned false → 401 → session cleared in WebUI.
func TestAuthManager_SQLiteSetStoreReloadsClientsAfterRestart(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	dbPath := filepath.Join(tmpDir, "test.db")

	// --- First server run: pair a client via SQLite ---
	dbStore1, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("run 1: failed to open store: %v", err)
	}

	auth1, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("run 1: failed to create auth manager: %v", err)
	}
	auth1.SetStore(dbStore1.NativeClients())

	// Generate PIN and pair — client is saved to SQLite only.
	pending, err := auth1.GeneratePIN("Desktop")
	if err != nil {
		t.Fatalf("run 1: GeneratePIN failed: %v", err)
	}
	client, token, _, err := auth1.PairWithPIN(pending.PIN, "Desktop")
	if err != nil {
		t.Fatalf("run 1: PairWithPIN failed: %v", err)
	}
	if client == nil || token == "" {
		t.Fatal("run 1: expected valid client and token")
	}

	// Verify the token works before restart.
	if _, valid := auth1.ValidateToken(token); !valid {
		t.Fatal("run 1: token should be valid")
	}

	// Close the store (simulates process exit).
	dbStore1.Close()

	// --- Second server run (restart) ---
	// NewAuthManager loads from the JSON file, which is stale because
	// saveStoreUnlocked skips the JSON write when SQLite is active.
	auth2, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("run 2: failed to create auth manager: %v", err)
	}

	// Before SetStore, the token is NOT valid (client only in SQLite).
	if _, valid := auth2.ValidateToken(token); valid {
		t.Fatal("run 2: token should NOT be valid before SetStore (stale JSON)")
	}

	// Open the same SQLite store and wire it in.
	dbStore2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("run 2: failed to open store: %v", err)
	}
	defer dbStore2.Close()

	auth2.SetStore(dbStore2.NativeClients())

	// After SetStore, the token MUST be valid — SetStore must reload from SQLite.
	if _, valid := auth2.ValidateToken(token); !valid {
		t.Fatal("run 2: token MUST be valid after SetStore reloads from SQLite")
	}

	// Also verify the client metadata is preserved.
	clients := auth2.ListClients()
	if len(clients) != 1 {
		t.Fatalf("run 2: expected 1 client, got %d", len(clients))
	}
	if clients[0].DeviceName != "Desktop" {
		t.Errorf("run 2: expected device name 'Desktop', got '%s'", clients[0].DeviceName)
	}
}
func TestRegisterDesktopClient_ValidateToken(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	if err := auth.RegisterDesktopClient("tok123", "ref456"); err != nil {
		t.Fatalf("failed to register desktop client: %v", err)
	}

	client, valid := auth.ValidateToken("tok123")
	if !valid {
		t.Fatal("expected desktop token to be valid")
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.ClientID != DesktopClientID {
		t.Errorf("expected client ID %q, got %q", DesktopClientID, client.ClientID)
	}

	if _, valid := auth.ValidateToken("wrong"); valid {
		t.Error("expected wrong token to be rejected")
	}
}

func TestRegisterDesktopClient_Upsert(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	if err := auth.RegisterDesktopClient("old-token", "old-refresh"); err != nil {
		t.Fatalf("failed to register desktop client: %v", err)
	}
	if err := auth.RegisterDesktopClient("new-token", "new-refresh"); err != nil {
		t.Fatalf("failed to re-register desktop client: %v", err)
	}

	if _, valid := auth.ValidateToken("old-token"); valid {
		t.Error("expected old token to be invalid after re-registration")
	}
	if _, valid := auth.ValidateToken("new-token"); !valid {
		t.Error("expected new token to be valid after re-registration")
	}

	clients := auth.ListClients()
	if len(clients) != 1 {
		t.Fatalf("expected exactly 1 desktop client, got %d", len(clients))
	}
	if clients[0].ClientID != DesktopClientID {
		t.Errorf("expected client ID %q, got %q", DesktopClientID, clients[0].ClientID)
	}
}

func TestRegisterDesktopClient_EmptyToken(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	if err := auth.RegisterDesktopClient("", "ref456"); err == nil {
		t.Error("expected error when token is empty")
	}
	if err := auth.RegisterDesktopClient("tok123", ""); err == nil {
		t.Error("expected error when refresh token is empty")
	}
}

func TestRegisterDesktopClient_RefreshWorks(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	if err := auth.RegisterDesktopClient("tok123", "ref456"); err != nil {
		t.Fatalf("failed to register desktop client: %v", err)
	}

	client, newToken, newRefreshToken, err := auth.RefreshToken("ref456")
	if err != nil {
		t.Fatalf("failed to refresh desktop client token: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.ClientID != DesktopClientID {
		t.Errorf("expected client ID %q, got %q", DesktopClientID, client.ClientID)
	}
	if newToken == "" {
		t.Error("expected non-empty new token")
	}
	if newRefreshToken == "" {
		t.Error("expected non-empty new refresh token")
	}

	if _, valid := auth.ValidateToken(newToken); !valid {
		t.Error("expected rotated token to be valid")
	}
}
