package channels

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
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

	for i := 0; i < 10; i++ {
		_, err := auth.GeneratePIN("Device")
		if i >= 10 {
			if err == nil {
				t.Error("expected error when exceeding max pending PINs")
			}
		}
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
