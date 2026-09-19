package channels

// auth_pin_crossproc_test.go — Characterization tests for PIN flow between
// CLI and server over a shared SQLite store.
//
// These tests model the REAL cross-process wiring from cmd/lele/client.go:39-52:
//
//	authMgr, _ := channels.NewAuthManager(cfg, dir)
//	s, _ := store.Open(filepath.Join(dir, "lele.db"))
//	authMgr.SetStore(s.NativeClients())
//
// Both CLI and server carry repo != nil with independent store.Open handles
// on the same DB path, exactly like two real processes.
//
// PROHIBITED: simulating the CLI with repo == nil (that falls through to the
// JSON branch and writes native_clients.json — the file the real CLI no longer
// writes since PR #326. That is precisely the bug these tests catch).

import (
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

// defaultTestCfg returns a NativeConfig suitable for PIN tests.
func defaultTestCfg() *config.NativeConfig {
	return &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}
}

// openCLIAuthManager constructs the CLI side exactly like cmd/lele/client.go:39-52.
func openCLIAuthManager(t *testing.T, tmpDir, dbPath string) (*AuthManager, *store.Store) {
	t.Helper()
	cliAuth, err := NewAuthManager(defaultTestCfg(), tmpDir)
	if err != nil {
		t.Fatalf("CLI: NewAuthManager: %v", err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("CLI: store.Open: %v", err)
	}
	cliAuth.SetStore(s.NativeClients())
	return cliAuth, s
}

// openServerAuthManager constructs the server side: new AuthManager + new
// store.Open on the same DB path (independent connection, like a separate
// process).
func openServerAuthManager(t *testing.T, tmpDir, dbPath string) (*AuthManager, *store.Store) {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("server: store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srvAuth, err := NewAuthManager(defaultTestCfg(), tmpDir)
	if err != nil {
		t.Fatalf("server: NewAuthManager: %v", err)
	}
	srvAuth.SetStore(s.NativeClients())
	return srvAuth, s
}

// ---------------------------------------------------------------------------
// Test 1: CLI generates → server redeems (shared SQLite)
// ---------------------------------------------------------------------------

func TestPinFlow_CLIGeneratesPIN_ServerRedeems_SharedSQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	// CLI side: generate a PIN (repo != nil, same as client.go:39-52).
	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("cli-phone")
	if err != nil {
		t.Fatalf("CLI: GeneratePIN: %v", err)
	}
	cliStore.Close() // simulate process exit

	// Server side: new connection, must find and redeem the PIN.
	serverAuth, _ := openServerAuthManager(t, tmpDir, dbPath)

	client, token, refreshToken, err := serverAuth.PairWithPIN(pending.PIN, "cli-phone")
	if err != nil {
		t.Fatalf("server: PairWithPIN failed: %v (PIN %s was not persisted to shared store)", err, pending.PIN)
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
}

// ---------------------------------------------------------------------------
// Test 2: pending PIN survives CLI process exit
// ---------------------------------------------------------------------------

func TestPinFlow_PendingPINSurvivesCLIProcessExit(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("cli-phone")
	if err != nil {
		t.Fatalf("CLI: GeneratePIN: %v", err)
	}
	cliStore.Close() // CLI exits

	// New server process — must see the pending PIN.
	serverAuth, _ := openServerAuthManager(t, tmpDir, dbPath)
	pins := serverAuth.GetPendingPINs()

	found := false
	for _, p := range pins {
		if p.PIN == pending.PIN {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected pending PIN %s to survive CLI exit; got %d pins in store", pending.PIN, len(pins))
	}
}

// ---------------------------------------------------------------------------
// Test 3: redeem is single-use (F-REPLAY catcher)
// ---------------------------------------------------------------------------
// NOTE ON INTERPRETATION: Before the fix (a0e256f), the CLI does NOT persist
// the PIN to SQLite (saveStoreUnlocked only writes Clients and returns before
// the PendingPINs JSON write). So the first redeem on the server side fails
// with "invalid PIN" because the PIN simply doesn't exist in the shared store.
// After the fix, the first redeem succeeds and ONLY the second redeem fails
// with "invalid PIN". Without this comment, a reader seeing the test fail on
// the first redeem might misinterpret it as a single-use violation.

func TestPinFlow_RedeemIsSingleUse_SharedSQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("cli-phone")
	if err != nil {
		t.Fatalf("CLI: GeneratePIN: %v", err)
	}
	cliStore.Close()

	serverAuth, _ := openServerAuthManager(t, tmpDir, dbPath)

	// First redeem — must succeed.
	_, _, _, err = serverAuth.PairWithPIN(pending.PIN, "cli-phone")
	if err != nil {
		t.Fatalf("first redeem: expected success, got: %v", err)
	}

	// Second redeem with the same PIN — must fail with "invalid PIN".
	_, _, _, err = serverAuth.PairWithPIN(pending.PIN, "cli-phone")
	if err == nil {
		t.Fatal("second redeem: expected 'invalid PIN' error, got nil")
	}
	if err.Error() != "invalid PIN" {
		t.Errorf("second redeem: expected 'invalid PIN', got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Test 4: concurrent redeem — exactly one winner
// ---------------------------------------------------------------------------

func TestPinFlow_ConcurrentRedeemExactlyOneWinner(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("cli-phone")
	if err != nil {
		t.Fatalf("CLI: GeneratePIN: %v", err)
	}
	cliStore.Close()

	// Two independent store handles (like two server processes).
	s1, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open 1: %v", err)
	}
	defer s1.Close()
	s2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open 2: %v", err)
	}
	defer s2.Close()

	auth1, err := NewAuthManager(defaultTestCfg(), tmpDir)
	if err != nil {
		t.Fatalf("NewAuthManager 1: %v", err)
	}
	auth1.SetStore(s1.NativeClients())

	auth2, err := NewAuthManager(defaultTestCfg(), tmpDir)
	if err != nil {
		t.Fatalf("NewAuthManager 2: %v", err)
	}
	auth2.SetStore(s2.NativeClients())

	const goroutines = 12
	var wg sync.WaitGroup
	var successes atomic.Int32
	var failures atomic.Int32

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Alternate between the two store handles.
			var am *AuthManager
			if idx%2 == 0 {
				am = auth1
			} else {
				am = auth2
			}
			_, _, _, err := am.PairWithPIN(pending.PIN, "cli-phone")
			if err == nil {
				successes.Add(1)
			} else {
				failures.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if successes.Load() != 1 {
		t.Errorf("expected exactly 1 successful redeem out of %d, got %d successes and %d failures",
			goroutines, successes.Load(), failures.Load())
	}
}

// ---------------------------------------------------------------------------
// Test 5: expired PIN not redeemable
// ---------------------------------------------------------------------------

func TestPinFlow_ExpiredPINNotRedeemable_SharedSQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("cli-phone")
	if err != nil {
		t.Fatalf("CLI: GeneratePIN: %v", err)
	}

	// Backdoor: force expires_at into the past directly in the DB.
	// On current code (a0e256f) the table doesn't exist yet, so this
	// will fail — which is correct (the PIN wasn't persisted at all).
	_, err = cliStore.DB().Exec(
		`UPDATE native_pending_pins SET expires_at = ? WHERE pin = ?`,
		time.Now().Add(-1*time.Minute).UnixNano(),
		pending.PIN,
	)
	if err != nil {
		// Expected before the fix: table doesn't exist because PIN was
		// never persisted. The test still fails, demonstrating the bug.
		t.Fatalf("cannot backdoor-expire PIN (table missing = PIN not persisted): %v", err)
	}
	cliStore.Close()

	serverAuth, _ := openServerAuthManager(t, tmpDir, dbPath)

	_, _, _, err = serverAuth.PairWithPIN(pending.PIN, "cli-phone")
	if err == nil {
		t.Fatal("expected error for expired PIN, got nil")
	}
	if err.Error() != "PIN expired" {
		t.Errorf("expected 'PIN expired', got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Test 6: device name mismatch does NOT consume the PIN
// ---------------------------------------------------------------------------

func TestPinFlow_DeviceNameMismatchDoesNotConsumePIN_SharedSQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("correct-name")
	if err != nil {
		t.Fatalf("CLI: GeneratePIN: %v", err)
	}
	cliStore.Close()

	serverAuth, _ := openServerAuthManager(t, tmpDir, dbPath)

	// Redeem with wrong name — must get "device name mismatch".
	_, _, _, err = serverAuth.PairWithPIN(pending.PIN, "wrong-name")
	if err == nil {
		t.Fatal("expected error for mismatched name, got nil")
	}
	if err.Error() != "device name mismatch" {
		t.Errorf("expected 'device name mismatch', got %q", err.Error())
	}

	// The PIN must still be redeemable with the correct name.
	client, token, _, err := serverAuth.PairWithPIN(pending.PIN, "correct-name")
	if err != nil {
		t.Fatalf("redeem with correct name: expected success, got: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if token == "" {
		t.Error("expected non-empty token")
	}
}

// ---------------------------------------------------------------------------
// Test 7: JSON backend still works (no repo) — guard for desktop/mips64
// ---------------------------------------------------------------------------

func TestPinFlow_JSONBackendStillWorks_NoRepo(t *testing.T) {
	// Both sides use repo == nil (JSON file backend).
	// This MUST PASS on current code — it guards the desktop/mips64 path
	// (driver_stub.go, sqliteSupported=false).
	tmpDir := t.TempDir()
	cfg := defaultTestCfg()

	// CLI side (no SetStore → JSON mode).
	cliAuth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("CLI: NewAuthManager: %v", err)
	}
	pending, err := cliAuth.GeneratePIN("desktop-device")
	if err != nil {
		t.Fatalf("CLI: GeneratePIN: %v", err)
	}

	// Server side (no SetStore → JSON mode).
	serverAuth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("server: NewAuthManager: %v", err)
	}

	client, token, refreshToken, err := serverAuth.PairWithPIN(pending.PIN, "desktop-device")
	if err != nil {
		t.Fatalf("server: PairWithPIN: %v", err)
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
		t.Error("expected token to be valid after JSON-mode pairing")
	}
}

// ---------------------------------------------------------------------------
// Test 8: cap purge-then-evict in SQLite mode (mirror of GW-M8)
// ---------------------------------------------------------------------------

func TestPinFlow_CapPurgeThenEvict_SharedSQLite(t *testing.T) {
	// With 10 live (non-expired) pending PINs, generating the 11th must
	// succeed by evicting the oldest PIN (FIFO). After the eviction, the
	// evicted PIN must NOT be redeemable, and the new one MUST be.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)

	var firstPIN *PendingPIN
	for i := 0; i < 10; i++ {
		p, err := cliAuth.GeneratePIN(fmt.Sprintf("device-%d", i))
		if err != nil {
			t.Fatalf("GeneratePIN %d: %v", i, err)
		}
		if i == 0 {
			firstPIN = p // oldest
		}
		// Small sleep so Created timestamps differ deterministically.
		if i < 9 {
			time.Sleep(time.Millisecond)
		}
	}

	// 11th must succeed (eviction of oldest makes room).
	p11, err := cliAuth.GeneratePIN("device-10")
	if err != nil {
		t.Fatalf("11th GeneratePIN should succeed via eviction, got: %v", err)
	}
	cliStore.Close()

	// Server side: verify the oldest is gone and the newest is redeemable.
	serverAuth, _ := openServerAuthManager(t, tmpDir, dbPath)

	// Oldest PIN must have been evicted.
	_, _, _, err = serverAuth.PairWithPIN(firstPIN.PIN, "device-0")
	if err == nil {
		t.Fatal("oldest PIN should have been evicted, but redeem succeeded")
	}
	// Error must be "invalid PIN" (evicted = not found).
	if err.Error() != "invalid PIN" {
		t.Errorf("evicted PIN: expected 'invalid PIN', got %q", err.Error())
	}

	// Newest PIN must be redeemable.
	client, token, _, err := serverAuth.PairWithPIN(p11.PIN, "device-10")
	if err != nil {
		t.Fatalf("11th PIN should be redeemable, got: %v", err)
	}
	if client == nil || token == "" {
		t.Fatal("expected valid client and token for 11th PIN")
	}
}
