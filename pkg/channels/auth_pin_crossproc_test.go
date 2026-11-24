package channels

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

func defaultTestCfg() *config.NativeConfig {
	return &config.NativeConfig{
		PinExpiryMinutes: 5,
		MaxClients:       5,
		TokenExpiryDays:  30,
	}
}

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

func TestPinFlow_CLIGeneratesPIN_ServerRedeems_SharedSQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("cli-phone")
	if err != nil {
		t.Fatalf("CLI: GeneratePIN: %v", err)
	}
	cliStore.Close()

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

func TestPinFlow_PendingPINSurvivesCLIProcessExit(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("cli-phone")
	if err != nil {
		t.Fatalf("CLI: GeneratePIN: %v", err)
	}
	cliStore.Close()

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

	_, _, _, err = serverAuth.PairWithPIN(pending.PIN, "cli-phone")
	if err != nil {
		t.Fatalf("first redeem: expected success, got: %v", err)
	}

	_, _, _, err = serverAuth.PairWithPIN(pending.PIN, "cli-phone")
	if err == nil {
		t.Fatal("second redeem: expected 'invalid PIN' error, got nil")
	}
	if err.Error() != "invalid PIN" {
		t.Errorf("second redeem: expected 'invalid PIN', got %q", err.Error())
	}
}

func TestPinFlow_ConcurrentRedeemExactlyOneWinner(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("cli-phone")
	if err != nil {
		t.Fatalf("CLI: GeneratePIN: %v", err)
	}
	cliStore.Close()

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

func TestPinFlow_ExpiredPINNotRedeemable_SharedSQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("cli-phone")
	if err != nil {
		t.Fatalf("CLI: GeneratePIN: %v", err)
	}

	_, err = cliStore.DB().Exec(
		`UPDATE native_pending_pins SET expires_at = ? WHERE pin = ?`,
		time.Now().Add(-1*time.Minute).UnixNano(),
		pending.PIN,
	)
	if err != nil {
		t.Fatalf("cannot backdoor-expire PIN (table missing = PIN not persisted): %v", err)
	}
	cliStore.Close()

	serverAuth, _ := openServerAuthManager(t, tmpDir, dbPath)

	_, _, _, err = serverAuth.PairWithPIN(pending.PIN, "cli-phone")
	if err == nil {
		t.Fatal("expected error for expired PIN, got nil")
	}
	if err.Error() != "invalid PIN" {
		t.Errorf("expired PIN must answer the uniform error, got %q", err.Error())
	}
}

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

	_, _, _, err = serverAuth.PairWithPIN(pending.PIN, "wrong-name")
	if err == nil {
		t.Fatal("expected error for mismatched name, got nil")
	}
	if err.Error() != "invalid PIN" {
		t.Errorf("device name mismatch must answer the uniform error, got %q", err.Error())
	}

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

func TestPinFlow_JSONBackendStillWorks_NoRepo(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := defaultTestCfg()

	cliAuth, err := NewAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("CLI: NewAuthManager: %v", err)
	}
	pending, err := cliAuth.GeneratePIN("desktop-device")
	if err != nil {
		t.Fatalf("CLI: GeneratePIN: %v", err)
	}

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

	_, valid := serverAuth.ValidateToken(token)
	if !valid {
		t.Error("expected token to be valid after JSON-mode pairing")
	}
}

func TestPinFlow_CapPurgeThenEvict_SharedSQLite(t *testing.T) {
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
			firstPIN = p
		}
		if i < 9 {
			time.Sleep(time.Millisecond)
		}
	}

	p11, err := cliAuth.GeneratePIN("device-10")
	if err != nil {
		t.Fatalf("11th GeneratePIN should succeed via eviction, got: %v", err)
	}
	cliStore.Close()

	serverAuth, _ := openServerAuthManager(t, tmpDir, dbPath)

	_, _, _, err = serverAuth.PairWithPIN(firstPIN.PIN, "device-0")
	if err == nil {
		t.Fatal("oldest PIN should have been evicted, but redeem succeeded")
	}
	if err.Error() != "invalid PIN" {
		t.Errorf("evicted PIN: expected 'invalid PIN', got %q", err.Error())
	}

	client, token, _, err := serverAuth.PairWithPIN(p11.PIN, "device-10")
	if err != nil {
		t.Fatalf("11th PIN should be redeemable, got: %v", err)
	}
	if client == nil || token == "" {
		t.Fatal("expected valid client and token for 11th PIN")
	}
}

func TestPinFlow_GeneratePIN_ReturnsImmediatelyOnNonDuplicateDBError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)

	if _, err := cliAuth.GeneratePIN("seed-device"); err != nil {
		t.Fatalf("seed GeneratePIN: %v", err)
	}

	cliStore.Close()

	_, err := cliAuth.GeneratePIN("after-close")
	if err == nil {
		t.Fatal("GeneratePIN on closed DB expected error, got nil")
	}
	if errors.Is(err, store.ErrDuplicate) {
		t.Errorf("closed-DB error must NOT be ErrDuplicate, got: %v", err)
	}
	if strings.Contains(err.Error(), "exhausted") {
		t.Errorf("GeneratePIN should fail immediately on non-duplicate error, "+
			"but got exhaustion message: %v", err)
	}
	if !strings.Contains(err.Error(), "database is closed") {
		t.Logf("NOTE: error text is %q (expected 'database is closed' substring)", err.Error())
	}
}

func TestPairWithPIN_ClientWriteFails_NoTokenNoPhantomClient(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")
	auth, s := openCLIAuthManager(t, tmpDir, dbPath)

	pending, err := auth.GeneratePIN("ghost-phone")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}

	if _, err := s.DB().Exec(`CREATE TRIGGER fail_client_insert
		BEFORE INSERT ON native_clients
		BEGIN SELECT RAISE(ABORT, 'injected: client write blocked'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	client, token, refresh, err := auth.PairWithPIN(pending.PIN, "ghost-phone")
	if err == nil {
		t.Fatal("PairWithPIN must fail when the client cannot be persisted")
	}
	if token != "" || refresh != "" {
		t.Errorf("must not hand back credentials on persistence failure; got token=%q refresh=%q", token, refresh)
	}
	if client != nil {
		t.Errorf("must not return a client on persistence failure; got %+v", client)
	}

	if n := len(auth.store.Clients); n != 0 {
		t.Errorf("in-memory client map must be rolled back, still holds %d client(s)", n)
	}

	if _, _, found, err := auth.repo.GetPendingPIN(pending.PIN); err != nil {
		t.Fatalf("GetPendingPIN: %v", err)
	} else if found {
		t.Error("PIN is expected to remain consumed after a failed pairing (fail-closed tradeoff)")
	}
}

func TestPairWithPIN_PersistenceFailureReturnsErrorNoToken(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	serverAuth, serverStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := serverAuth.GeneratePIN("server-phone")
	if err != nil {
		t.Fatalf("server: GeneratePIN: %v", err)
	}

	serverStore.Close()

	client, token, _, err := serverAuth.PairWithPIN(pending.PIN, "server-phone")

	if err == nil {
		t.Fatal("PairWithPIN should fail when store is unavailable, got nil error")
	}

	if token != "" {
		t.Errorf("PairWithPIN must not return token on failure; got %q", token)
	}

	if client != nil {
		t.Errorf("PairWithPIN must not return client on failure; got %v", client)
	}
}
