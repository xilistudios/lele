package channels

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
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

	// GW-M8: the 11th no longer fails — the oldest pending PIN is evicted
	// (FIFO) so a new pairing always succeeds.
	auth.mu.Lock()
	countBefore := len(auth.store.PendingPINs)
	auth.mu.Unlock()
	if countBefore != 10 {
		t.Fatalf("expected 10 pending PINs before the extra request, got %d", countBefore)
	}

	if _, err := auth.GeneratePIN("Device"); err != nil {
		t.Fatalf("expected PIN mint to succeed via oldest-entry eviction, got error: %v", err)
	}

	auth.mu.Lock()
	countAfter := len(auth.store.PendingPINs)
	auth.mu.Unlock()
	if countAfter != 10 {
		t.Errorf("pending PINs should stay bounded at %d after eviction, got %d", 10, countAfter)
	}
}

// TestAuthManager_GeneratePIN_PurgesExpiredFirst verifies that on GeneratePIN
// expired PINs are removed before the cap is applied, so abandoned requests
// cannot starve new pairings (GW-M8).
func TestAuthManager_GeneratePIN_PurgesExpiredFirst(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	// Fill the store to the cap, then backdate every entry so all are
	// expired (as if the expiry window had elapsed with nobody redeeming).
	for i := 0; i < 10; i++ {
		if _, err := auth.GeneratePIN("Device"); err != nil {
			t.Fatalf("failed to generate PIN %d: %v", i, err)
		}
	}

	auth.mu.Lock()
	now := time.Now()
	for _, pending := range auth.store.PendingPINs {
		pending.Expires = now.Add(-time.Minute)
	}
	auth.mu.Unlock()

	// The next mint must succeed via lazy expiry purge — and the resulting
	// pending count must be 1 (only the fresh PIN survives).
	pending, err := auth.GeneratePIN("FreshDevice")
	if err != nil {
		t.Fatalf("expected mint to succeed after purging expired PINs, got error: %v", err)
	}

	auth.mu.Lock()
	count := len(auth.store.PendingPINs)
	auth.mu.Unlock()
	if count != 1 {
		t.Errorf("expected expired PINs to be purged, leaving 1 pending PIN, got %d", count)
	}
	if pending.DeviceName != "FreshDevice" {
		t.Errorf("expected fresh PIN for 'FreshDevice', got '%s'", pending.DeviceName)
	}
}

// TestAuthManager_GeneratePIN_EvictsOldestNotNewest verifies that when the
// cap is hit with all entries still unexpired, the entry with the oldest
// Created timestamp is the one evicted (FIFO), keeping newer legitimate
// pairings intact (GW-M8).
func TestAuthManager_GeneratePIN_EvictsOldestNotNewest(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
	}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	// First PIN is explicitly the oldest (backdated, still unexpired).
	first, err := auth.GeneratePIN("OldestDevice")
	if err != nil {
		t.Fatalf("failed to generate first PIN: %v", err)
	}
	auth.mu.Lock()
	auth.store.PendingPINs[first.PIN].Created = time.Now().Add(-4 * time.Minute)
	auth.mu.Unlock()

	// Fill the remaining slots with newer entries.
	for i := 0; i < 9; i++ {
		if _, err := auth.GeneratePIN("NewerDevice"); err != nil {
			t.Fatalf("failed to generate PIN %d: %v", i, err)
		}
	}

	auth.mu.Lock()
	_, oldestStillThere := auth.store.PendingPINs[first.PIN]
	auth.mu.Unlock()
	if !oldestStillThere {
		t.Fatal("setup error: oldest PIN vanished before eviction could be observed")
	}

	// One more mint: the oldest unexpired entry must be evicted to make room.
	extra, err := auth.GeneratePIN("LatestDevice")
	if err != nil {
		t.Fatalf("expected mint to succeed via eviction, got error: %v", err)
	}

	auth.mu.Lock()
	_, evicted := auth.store.PendingPINs[first.PIN]
	count := len(auth.store.PendingPINs)
	auth.mu.Unlock()

	if evicted {
		t.Error("oldest pending PIN should have been evicted")
	}
	if count != 10 {
		t.Errorf("pending count should stay at cap 10, got %d", count)
	}
	if extra.DeviceName != "LatestDevice" {
		t.Errorf("new PIN should be present, got device '%s'", extra.DeviceName)
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

// TestAuthManager_UpdateLastSeen asserts the observable effect of the
// last-seen update through the public readers (GetClient / ListClients). The
// timestamp is no longer written into the shared LastSeen field (that write
// needed the manager's exclusive lock on every authenticated request); it goes
// into the client's atomic field and the readers fold it back into LastSeen.
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

	before, ok := auth.GetClient(client.ClientID)
	if !ok {
		t.Fatal("expected client to exist")
	}
	originalLastSeen := before.LastSeen

	time.Sleep(10 * time.Millisecond)
	auth.UpdateLastSeen(client.ClientID)

	after, ok := auth.GetClient(client.ClientID)
	if !ok {
		t.Fatal("expected client to exist after UpdateLastSeen")
	}
	if !after.LastSeen.After(originalLastSeen) {
		t.Errorf("expected GetClient LastSeen after %v to be later than %v", after.LastSeen, originalLastSeen)
	}

	listed := auth.ListClients()
	if len(listed) != 1 {
		t.Fatalf("expected 1 client, got %d", len(listed))
	}
	if !listed[0].LastSeen.After(originalLastSeen) {
		t.Errorf("expected ListClients LastSeen after %v to be later than %v", listed[0].LastSeen, originalLastSeen)
	}
}

// TestAuthManager_UpdateLastSeen_DoesNotTakeWriteLock is the regression guard
// for the latency finding: UpdateLastSeen runs on every authenticated HTTP
// request and every WebSocket connect, so it must never need am.mu
// exclusively. Holding a read lock for the whole call proves it — with the
// previous implementation (am.mu.Lock()) this test times out.
func TestAuthManager_UpdateLastSeen_DoesNotTakeWriteLock(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{PinExpiryMinutes: 5, MaxClients: 5, TokenExpiryDays: 30}

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

	auth.mu.RLock()
	done := make(chan struct{})
	go func() {
		auth.UpdateLastSeen(client.ClientID)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		auth.mu.RUnlock()
		t.Fatal("UpdateLastSeen blocked while a reader held am.mu: it must not take the exclusive lock")
	}
	auth.mu.RUnlock()

	if got, _ := auth.GetClient(client.ClientID); !got.LastSeen.After(client.Created) {
		t.Error("expected LastSeen to be updated after the lock-free write")
	}
}

// TestAuthManager_UpdateLastSeen_ConcurrentReaders races the hot-path writer
// against every reader that shares the client struct (GetClient, ListClients,
// ValidateToken). Run with -race it proves the atomic field replaced the
// lock-protected field write; without -race it still catches panics and
// lost/degraded values.
func TestAuthManager_UpdateLastSeen_ConcurrentReaders(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{PinExpiryMinutes: 5, MaxClients: 5, TokenExpiryDays: 30}

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}

	pending, err := auth.GeneratePIN("TestDevice")
	if err != nil {
		t.Fatalf("failed to generate PIN: %v", err)
	}
	client, token, _, err := auth.PairWithPIN(pending.PIN, "TestDevice")
	if err != nil {
		t.Fatalf("failed to pair: %v", err)
	}

	const iterations = 200
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				auth.UpdateLastSeen(client.ClientID)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if got, ok := auth.GetClient(client.ClientID); ok && got.LastSeen.IsZero() {
					t.Error("GetClient returned a zero LastSeen")
					return
				}
				for _, c := range auth.ListClients() {
					if c.LastSeen.IsZero() {
						t.Error("ListClients returned a zero LastSeen")
						return
					}
				}
				if _, valid := auth.ValidateToken(token); !valid {
					t.Error("expected token to stay valid while last-seen updates run")
					return
				}
			}
		}()
	}

	wg.Wait()

	got, ok := auth.GetClient(client.ClientID)
	if !ok {
		t.Fatal("expected client to exist")
	}
	if got.LastSeen.Before(client.Created) {
		t.Errorf("LastSeen %v is older than the client creation time %v", got.LastSeen, client.Created)
	}
}

// TestClientInfo_MarshalJSON_UsesFreshestLastSeen pins the JSON contract the
// hot-path field must not break: an atomic.Int64 does not marshal like an
// int64, so (*ClientInfo).MarshalJSON folds it into "last_seen" instead of
// leaking a nested object or dropping the value.
func TestClientInfo_MarshalJSON_UsesFreshestLastSeen(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	touched := time.Date(2026, 6, 7, 8, 9, 10, 123456789, time.UTC)

	client := &ClientInfo{
		ClientID:    "cli-1",
		DeviceName:  "Phone",
		Created:     created,
		LastSeen:    created,
		SessionKeys: []string{"native:one"},
	}
	client.touchLastSeen(touched)

	raw, err := json.Marshal(client)
	if err != nil {
		t.Fatalf("json.Marshal(ClientInfo) error = %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(marshal output) error = %v", err)
	}
	if _, leaked := decoded["lastSeenNanos"]; leaked {
		t.Errorf("atomic field leaked into JSON: %s", raw)
	}

	var lastSeen string
	if err := json.Unmarshal(decoded["last_seen"], &lastSeen); err != nil {
		t.Fatalf("last_seen missing or not a string: %s", raw)
	}
	// Compare instants, not strings: time.Unix (used by touchLastSeen) yields a
	// local-zone time, exactly like the time.Now() values this field held
	// before, so the rendered offset is the machine's, not the test's.
	parsedSeen, err := time.Parse(time.RFC3339Nano, lastSeen)
	if err != nil {
		t.Fatalf("last_seen %q is not RFC3339Nano: %v", lastSeen, err)
	}
	if !parsedSeen.Equal(touched) {
		t.Errorf("last_seen = %q (%v), want the touched instant %v", lastSeen, parsedSeen, touched)
	}

	// Round-trip: the value must survive a reload into the plain field.
	var roundTripped ClientInfo
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatalf("json.Unmarshal(raw) error = %v", err)
	}
	if !roundTripped.LastSeen.Equal(touched) {
		t.Errorf("round-tripped LastSeen = %v, want %v", roundTripped.LastSeen, touched)
	}
	if !roundTripped.effectiveLastSeen().Equal(touched) {
		t.Errorf("round-tripped effectiveLastSeen = %v, want %v", roundTripped.effectiveLastSeen(), touched)
	}

	// The plain field is still the fallback when nothing was touched (clients
	// persisted by an older version only have "last_seen").
	plain := &ClientInfo{ClientID: "cli-2", LastSeen: created}
	if !plain.effectiveLastSeen().Equal(created) {
		t.Errorf("effectiveLastSeen = %v, want the plain field %v", plain.effectiveLastSeen(), created)
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

	// Simulate CLI: creates an AuthManager with SQLite store (same wiring as
	// cmd/lele/client.go:39-52). The CLI uses the same shared DB as the server.
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("CLI: failed to create auth manager: %v", err)
	}
	cliStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("CLI: failed to open store: %v", err)
	}
	cliAuth.SetStore(cliStore.NativeClients())

	pending, err := cliAuth.GeneratePIN("CLI-Device")
	if err != nil {
		t.Fatalf("CLI: failed to generate PIN: %v", err)
	}
	cliStore.Close() // simulate CLI process exit

	// Server creates its own AuthManager and opens the same DB independently
	// (separate connection, like a real separate process).
	serverStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("server: failed to open store: %v", err)
	}
	defer serverStore.Close()

	serverAuth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("server: failed to create auth manager: %v", err)
	}
	serverAuth.SetStore(serverStore.NativeClients())

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
// PIN (via the shared SQLite store), THEN the webui tries to pair with
// that PIN. This is the exact flow that caused "invalid PIN" before the
// fix: the CLI stopped writing to native_clients.json (PR #326) but the
// server still only read PINs from there.
func TestAuthManager_SQLitePairAfterCLIGeneratesPIN(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}

	// Both CLI and server share the same DB path (independent connections).
	dbPath := filepath.Join(tmpDir, "lele.db")

	// Step 1: Server starts. SetStore is called during initialization.
	serverStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("server: failed to open store: %v", err)
	}
	defer serverStore.Close()

	serverAuth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("server: failed to create auth manager: %v", err)
	}
	serverAuth.SetStore(serverStore.NativeClients())

	// Step 2: CLI generates a PIN AFTER the server started.
	// The CLI is a separate process with its own store.Open on the same DB
	// (same wiring as cmd/lele/client.go:39-52).
	cliAuth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("CLI: failed to create auth manager: %v", err)
	}
	cliStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("CLI: failed to open store: %v", err)
	}
	cliAuth.SetStore(cliStore.NativeClients())
	pending, err := cliAuth.GeneratePIN("CLI-Device")
	if err != nil {
		t.Fatalf("CLI: failed to generate PIN: %v", err)
	}
	cliStore.Close()

	// Step 3: Webui tries to pair with the CLI's PIN.
	// PairWithPIN must read the PIN from the shared SQLite store.
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

// FIX-2: PairWithPIN must reject pairing when both the PIN's DeviceName and
// the caller's deviceName are empty. This closes the bypass where an attacker
// could pair without any device identification.
func TestAuthManager_PairWithPIN_RequiresDeviceNameWhenPINIssuedWithoutOne(t *testing.T) {
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

	// Generate PIN without a device name (e.g. CLI flow).
	pending, err := auth.GeneratePIN("")
	if err != nil {
		t.Fatalf("GeneratePIN failed: %v", err)
	}

	// Pairing without providing a device name must fail.
	_, _, _, err = auth.PairWithPIN(pending.PIN, "")
	if err == nil {
		t.Fatal("expected error when pairing without device_name and PIN issued without one")
	}
	if err.Error() != "device_name is required" {
		t.Fatalf("error = %q, want 'device_name is required'", err.Error())
	}

	// Pairing WITH a device name should succeed.
	client, token, _, err := auth.PairWithPIN(pending.PIN, "My Device")
	if err != nil {
		t.Fatalf("pairing with device_name should succeed: %v", err)
	}
	if client == nil || token == "" {
		t.Fatal("expected valid client and token")
	}
	if client.DeviceName != "My Device" {
		t.Errorf("client.DeviceName = %q, want 'My Device'", client.DeviceName)
	}
}

// When the PIN was issued WITH a device name, pairing with the same name
// should still work (existing behaviour preserved).
func TestAuthManager_PairWithPIN_DeviceNameMatch(t *testing.T) {
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

	pending, err := auth.GeneratePIN("Server-Device")
	if err != nil {
		t.Fatalf("GeneratePIN failed: %v", err)
	}

	// Pair with matching device name.
	client, token, _, err := auth.PairWithPIN(pending.PIN, "Server-Device")
	if err != nil {
		t.Fatalf("pairing with matching device_name: %v", err)
	}
	if client == nil || token == "" {
		t.Fatal("expected valid client and token")
	}
}

// When the PIN was issued WITH a device name, pairing with mismatched name
// should fail (existing behaviour preserved).
func TestAuthManager_PairWithPIN_DeviceNameMismatch(t *testing.T) {
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

	pending, err := auth.GeneratePIN("Server-Device")
	if err != nil {
		t.Fatalf("GeneratePIN failed: %v", err)
	}

	// Pair with mismatched device name.
	_, _, _, err = auth.PairWithPIN(pending.PIN, "Evil-Device")
	if err == nil {
		t.Fatal("expected error for device name mismatch")
	}
	if err.Error() != "device name mismatch" {
		t.Fatalf("error = %q, want 'device name mismatch'", err.Error())
	}
}

// When the PIN was issued WITH a device name, pairing with empty device name
// should use the PIN's device name (existing behaviour: the finalDeviceName
// falls back to pending.DeviceName).
func TestAuthManager_PairWithPIN_EmptyCallerWithPINDeviceName(t *testing.T) {
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

	pending, err := auth.GeneratePIN("Server-Device")
	if err != nil {
		t.Fatalf("GeneratePIN failed: %v", err)
	}

	// Pair with empty device name — should use the PIN's device name.
	client, token, _, err := auth.PairWithPIN(pending.PIN, "")
	if err != nil {
		t.Fatalf("pairing with empty device_name should succeed when PIN has one: %v", err)
	}
	if client.DeviceName != "Server-Device" {
		t.Errorf("client.DeviceName = %q, want 'Server-Device'", client.DeviceName)
	}
	_ = token
}

// PairWithPIN must reject expired and nonexistent PINs (existing tests
// consolidated here for completeness).
func TestAuthManager_PairWithPIN_InvalidAndExpiredPIN(t *testing.T) {
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

	// Nonexistent PIN.
	_, _, _, err = auth.PairWithPIN("999999", "Test")
	if err == nil {
		t.Fatal("expected error for nonexistent PIN")
	}

	// Expired PIN: generate one and force its expiry to the past, then save
	// to disk so that PairWithPIN's internal loadStore picks up the change.
	pending, err := auth.GeneratePIN("Test")
	if err != nil {
		t.Fatalf("GeneratePIN failed: %v", err)
	}
	// Force expiry to the past and persist.
	auth.mu.Lock()
	if p, ok := auth.store.PendingPINs[pending.PIN]; ok {
		p.Expires = time.Now().Add(-1 * time.Minute)
	}
	auth.saveStoreUnlocked()
	auth.mu.Unlock()

	_, _, _, err = auth.PairWithPIN(pending.PIN, "Test")
	if err == nil {
		t.Fatal("expected error for expired PIN")
	}
}

// TestPlanClientWrites_SkipsUnchangedRows unit-tests the store-write
// de-duplication: only rows whose payload actually differs from the persisted
// blob are returned (new rows always are).
func TestPlanClientWrites_SkipsUnchangedRows(t *testing.T) {
	unchanged := &ClientInfo{ClientID: "cli-unchanged", DeviceName: "Same", SessionKeys: []string{"native:a"}}
	changed := &ClientInfo{ClientID: "cli-changed", DeviceName: "Renamed", SessionKeys: []string{"native:b"}}
	fresh := &ClientInfo{ClientID: "cli-new", DeviceName: "New", SessionKeys: []string{"native:c"}}

	unchangedJSON, err := json.Marshal(unchanged)
	if err != nil {
		t.Fatalf("marshal unchanged: %v", err)
	}
	changedJSON, err := json.Marshal(changed)
	if err != nil {
		t.Fatalf("marshal changed: %v", err)
	}
	// Persisted copy of the changed client differs by one session key.
	staleChanged, err := json.Marshal(&ClientInfo{
		ClientID:    changed.ClientID,
		DeviceName:  changed.DeviceName,
		SessionKeys: []string{"native:b", "native:b2"},
	})
	if err != nil {
		t.Fatalf("marshal stale changed: %v", err)
	}

	clients := map[string]*ClientInfo{
		unchanged.ClientID: unchanged,
		changed.ClientID:   changed,
		fresh.ClientID:     fresh,
	}
	// "cli-gone" exists only in the DB; it is not part of clients, so it is
	// never a write candidate (the caller deletes it instead).
	stored := map[string]string{
		unchanged.ClientID: string(unchangedJSON),
		changed.ClientID:   string(staleChanged),
		"cli-gone":         `{"client_id":"cli-gone"}`,
	}

	writes, err := planClientWrites(clients, stored)
	if err != nil {
		t.Fatalf("planClientWrites() error = %v", err)
	}

	got := make(map[string]bool, len(writes))
	for _, w := range writes {
		got[w.id] = true
	}
	if len(writes) != 2 || !got[changed.ClientID] || !got[fresh.ClientID] {
		t.Fatalf("planClientWrites() wrote %v, want exactly [%s %s]", writes, changed.ClientID, fresh.ClientID)
	}
	for _, w := range writes {
		if w.id == changed.ClientID && w.payload != string(changedJSON) {
			t.Errorf("planned payload for %s = %q, want %q", w.id, w.payload, changedJSON)
		}
		if w.id == "cli-gone" {
			t.Errorf("planClientWrites() planned a write for a client absent from memory")
		}
	}
}

// TestAuthManager_TrackSessionKey_RewritesOnlyChangedClientRows is the
// end-to-end guard for the write amplification: appending a session key used
// to rewrite EVERY paired client row (one SetClient transaction each, against
// a single-connection SQLite pool) while holding AuthManager.mu.
//
// The number of row writes is observed with an AFTER UPDATE trigger on
// native_clients rather than a mock, so the real repo and real SQL are
// exercised.
func TestAuthManager_TrackSessionKey_RewritesOnlyChangedClientRows(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "writes.db")
	cfg := &config.NativeConfig{PinExpiryMinutes: 5, MaxClients: 5, TokenExpiryDays: 30}

	dbStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q) error = %v", dbPath, err)
	}
	defer dbStore.Close()
	repo := dbStore.NativeClients()

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}
	auth.SetStore(repo)

	var paired []*ClientInfo
	for i := 0; i < 2; i++ {
		pending, err := auth.GeneratePIN("Device")
		if err != nil {
			t.Fatalf("GeneratePIN(%d) failed: %v", i, err)
		}
		client, _, _, err := auth.PairWithPIN(pending.PIN, "Device")
		if err != nil {
			t.Fatalf("PairWithPIN(%d) failed: %v", i, err)
		}
		paired = append(paired, client)
	}

	secondBlobBefore, found, err := repo.GetClient(paired[1].ClientID)
	if err != nil || !found {
		t.Fatalf("GetClient(second) found=%v err=%v", found, err)
	}

	// Log every UPDATE on native_clients from here on.
	if _, err := dbStore.DB().Exec(`CREATE TABLE client_write_log(id TEXT NOT NULL)`); err != nil {
		t.Fatalf("create write log table: %v", err)
	}
	if _, err := dbStore.DB().Exec(
		`CREATE TRIGGER log_client_writes AFTER UPDATE ON native_clients
		 BEGIN INSERT INTO client_write_log(id) VALUES (NEW.id); END`); err != nil {
		t.Fatalf("create write log trigger: %v", err)
	}

	const newKey = "native:first:chat-1"
	auth.TrackSessionKey(paired[0].ClientID, newKey)

	rows, err := dbStore.DB().Query(`SELECT id FROM client_write_log`)
	if err != nil {
		t.Fatalf("query write log: %v", err)
	}
	var written []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan write log: %v", err)
		}
		written = append(written, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("read write log: %v", err)
	}
	// Close before any further query: the pool has a single connection.
	rows.Close()

	if len(written) != 1 || written[0] != paired[0].ClientID {
		t.Errorf("TrackSessionKey rewrote %v; want exactly [%s] (the client whose payload changed)",
			written, paired[0].ClientID)
	}

	// The changed client must really be persisted with the new key.
	firstBlob, found, err := repo.GetClient(paired[0].ClientID)
	if err != nil || !found {
		t.Fatalf("GetClient(first) found=%v err=%v", found, err)
	}
	var firstStored ClientInfo
	if err := json.Unmarshal([]byte(firstBlob), &firstStored); err != nil {
		t.Fatalf("unmarshal first client: %v", err)
	}
	if !slices.Contains(firstStored.SessionKeys, newKey) {
		t.Errorf("persisted session keys = %v, want %q tracked", firstStored.SessionKeys, newKey)
	}

	// The untouched client must be byte-identical (skipped, not rewritten).
	secondBlobAfter, found, err := repo.GetClient(paired[1].ClientID)
	if err != nil || !found {
		t.Fatalf("GetClient(second, after) found=%v err=%v", found, err)
	}
	if secondBlobBefore != secondBlobAfter {
		t.Errorf("untouched client row changed:\n before=%s\n after =%s", secondBlobBefore, secondBlobAfter)
	}
}

// TestAuthManager_SQLitePersistsLockFreeLastSeen proves the hot-path last-seen
// value still reaches the persisted blob (via (*ClientInfo).MarshalJSON) and
// that UpdateLastSeen itself costs no disk write.
func TestAuthManager_SQLitePersistsLockFreeLastSeen(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lastseen.db")
	cfg := &config.NativeConfig{PinExpiryMinutes: 5, MaxClients: 5, TokenExpiryDays: 30}

	dbStore, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q) error = %v", dbPath, err)
	}
	defer dbStore.Close()
	repo := dbStore.NativeClients()

	auth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth manager: %v", err)
	}
	auth.SetStore(repo)

	pending, err := auth.GeneratePIN("Device")
	if err != nil {
		t.Fatalf("GeneratePIN failed: %v", err)
	}
	client, _, _, err := auth.PairWithPIN(pending.PIN, "Device")
	if err != nil {
		t.Fatalf("PairWithPIN failed: %v", err)
	}

	persisted := func() *ClientInfo {
		t.Helper()
		blob, found, err := repo.GetClient(client.ClientID)
		if err != nil || !found {
			t.Fatalf("GetClient found=%v err=%v", found, err)
		}
		var stored ClientInfo
		if err := json.Unmarshal([]byte(blob), &stored); err != nil {
			t.Fatalf("unmarshal persisted client: %v", err)
		}
		return &stored
	}

	beforePair := persisted()

	// Hot path: updates memory only.
	time.Sleep(5 * time.Millisecond)
	touchedFrom := time.Now()
	auth.UpdateLastSeen(client.ClientID)

	if afterTouch := persisted(); !afterTouch.LastSeen.Equal(beforePair.LastSeen) {
		t.Errorf("UpdateLastSeen wrote to disk: last_seen %v -> %v", beforePair.LastSeen, afterTouch.LastSeen)
	}

	// A real (locked) save must fold the lock-free value into the blob.
	if err := auth.saveStore(); err != nil {
		t.Fatalf("saveStore failed: %v", err)
	}
	stored := persisted()
	if stored.LastSeen.Before(touchedFrom) {
		t.Errorf("persisted last_seen = %v, want >= %v (the lock-free update)", stored.LastSeen, touchedFrom)
	}

	// A restarted gateway must observe the same value.
	auth2, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("failed to create second auth manager: %v", err)
	}
	auth2.SetStore(repo)
	reloaded, ok := auth2.GetClient(client.ClientID)
	if !ok {
		t.Fatal("expected client after reload")
	}
	if !reloaded.LastSeen.Equal(stored.LastSeen) {
		t.Errorf("reloaded LastSeen = %v, want %v", reloaded.LastSeen, stored.LastSeen)
	}
}
