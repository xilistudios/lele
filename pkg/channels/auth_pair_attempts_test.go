package channels

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
)

const invalidPINMessage = "invalid PIN"

func newJSONAuthManager(t *testing.T, cfg *config.NativeConfig, dir string) *AuthManager {
	t.Helper()

	am, err := NewAuthManager(cfg, dir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	return am
}

func requireInvalidPIN(t *testing.T, err error, label string) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s: expected an error, got nil", label)
	}
	if err.Error() != invalidPINMessage {
		t.Fatalf("%s: error = %q, want %q", label, err.Error(), invalidPINMessage)
	}
}

func TestPairWithPINBurnsAfterFiveFailures_JSON(t *testing.T) {
	dir := t.TempDir()
	am := newJSONAuthManager(t, defaultTestCfg(), dir)

	pending, err := am.GeneratePIN("phone")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}

	for i := 1; i < maxPairAttempts; i++ {
		_, _, _, err := am.PairWithPIN(pending.PIN, "attacker")
		requireInvalidPIN(t, err, "wrong device name")
	}

	reloaded := newJSONAuthManager(t, defaultTestCfg(), dir)
	pins := reloaded.GetPendingPINs()
	if len(pins) != 1 {
		t.Fatalf("expected the PIN to survive %d failures, got %d pending PINs", maxPairAttempts-1, len(pins))
	}
	if pins[0].Attempts != maxPairAttempts-1 {
		t.Errorf("persisted attempts = %d, want %d", pins[0].Attempts, maxPairAttempts-1)
	}

	_, _, _, err = am.PairWithPIN(pending.PIN, "attacker")
	requireInvalidPIN(t, err, "fifth wrong device name")

	_, _, _, err = am.PairWithPIN(pending.PIN, "phone")
	requireInvalidPIN(t, err, "correct device name after five failures")

	if pins := am.GetPendingPINs(); len(pins) != 0 {
		t.Errorf("burned PIN is still pending: %+v", pins)
	}
}

func TestPairWithPINBurnsAfterFiveFailures_SQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("phone")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	cliStore.Close()

	serverAuth, serverStore := openServerAuthManager(t, tmpDir, dbPath)
	repo := serverStore.NativeClients()

	for i := 0; i < maxPairAttempts; i++ {
		_, _, _, err := serverAuth.PairWithPIN(pending.PIN, "attacker")
		requireInvalidPIN(t, err, "wrong device name")
	}

	_, _, _, err = serverAuth.PairWithPIN(pending.PIN, "phone")
	requireInvalidPIN(t, err, "correct device name after five failures")

	if _, _, found, err := repo.GetPendingPIN(pending.PIN); err != nil {
		t.Fatalf("GetPendingPIN: %v", err)
	} else if found {
		t.Error("burned PIN must be removed from the shared store")
	}
}

func TestPairWithPINUniformErrors(t *testing.T) {
	maxedCfg := defaultTestCfg()
	maxedCfg.MaxClients = 1

	cases := []struct {
		name       string
		deviceName string
		setup      func(t *testing.T, am *AuthManager) string
	}{
		{
			name:       "device name mismatch",
			deviceName: "attacker",
			setup: func(t *testing.T, am *AuthManager) string {
				pending, err := am.GeneratePIN("phone")
				if err != nil {
					t.Fatalf("GeneratePIN: %v", err)
				}
				return pending.PIN
			},
		},
		{
			name:       "missing device_name",
			deviceName: "",
			setup: func(t *testing.T, am *AuthManager) string {
				pending, err := am.GeneratePIN("")
				if err != nil {
					t.Fatalf("GeneratePIN: %v", err)
				}
				return pending.PIN
			},
		},
		{
			name:       "maximum clients reached",
			deviceName: "second",
			setup: func(t *testing.T, am *AuthManager) string {
				first, err := am.GeneratePIN("first")
				if err != nil {
					t.Fatalf("GeneratePIN: %v", err)
				}
				if _, _, _, err := am.PairWithPIN(first.PIN, "first"); err != nil {
					t.Fatalf("seed pairing: %v", err)
				}
				pending, err := am.GeneratePIN("second")
				if err != nil {
					t.Fatalf("GeneratePIN: %v", err)
				}
				return pending.PIN
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			am := newJSONAuthManager(t, maxedCfg, t.TempDir())
			pin := tc.setup(t, am)

			_, _, _, err := am.PairWithPIN(pin, tc.deviceName)
			requireInvalidPIN(t, err, tc.name)
		})
	}
}

func TestPairWithPINExpiredReturnsInvalidPIN_SQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("phone")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	if _, err := cliStore.DB().Exec(
		`UPDATE native_pending_pins SET expires_at = ? WHERE pin = ?`,
		time.Now().Add(-time.Minute).UnixNano(),
		pending.PIN,
	); err != nil {
		t.Fatalf("cannot backdoor-expire PIN: %v", err)
	}
	cliStore.Close()

	serverAuth, _ := openServerAuthManager(t, tmpDir, dbPath)

	_, _, _, err = serverAuth.PairWithPIN(pending.PIN, "phone")
	requireInvalidPIN(t, err, "expired PIN")
}

func TestPairWithPINEntropyFailureKeepsPendingPIN_SQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("phone")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	cliStore.Close()

	serverAuth, serverStore := openServerAuthManager(t, tmpDir, dbPath)
	repo := serverStore.NativeClients()

	failRandRead(t)

	if _, _, _, err := serverAuth.PairWithPIN(pending.PIN, "phone"); !errors.Is(err, errEntropyUnavailable) {
		t.Fatalf("PairWithPIN error = %v, want errEntropyUnavailable", err)
	}

	pendingJSON, _, found, err := repo.GetPendingPIN(pending.PIN)
	if err != nil {
		t.Fatalf("GetPendingPIN: %v", err)
	}
	if !found {
		t.Fatal("an entropy failure must not consume the PIN in the shared store")
	}
	var stored PendingPIN
	if err := json.Unmarshal([]byte(pendingJSON), &stored); err != nil {
		t.Fatalf("unmarshal stored pending PIN: %v", err)
	}
	if stored.Attempts != 0 {
		t.Errorf("stored attempts = %d, want 0: an entropy failure is not a failed guess", stored.Attempts)
	}
}

func TestPairWithPINAttemptCounterFailureBurnsPIN_SQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	pending, err := cliAuth.GeneratePIN("phone")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	cliStore.Close()

	serverAuth, serverStore := openServerAuthManager(t, tmpDir, dbPath)
	repo := serverStore.NativeClients()

	if _, err := serverStore.DB().Exec(`CREATE TRIGGER fail_pending_pin_update
		BEFORE UPDATE ON native_pending_pins
		BEGIN SELECT RAISE(ABORT, 'injected: attempt counter write refused'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, _, _, err = serverAuth.PairWithPIN(pending.PIN, "attacker")
	requireInvalidPIN(t, err, "wrong device name with an uncountable attempt")

	if _, _, found, err := repo.GetPendingPIN(pending.PIN); err != nil {
		t.Fatalf("GetPendingPIN: %v", err)
	} else if found {
		t.Error("a PIN whose attempt counter cannot be persisted must be burned, not left usable")
	}
}

func TestPairWithPINAttemptCounterFailureBurnsPIN_JSON(t *testing.T) {
	dir := t.TempDir()
	am := newJSONAuthManager(t, defaultTestCfg(), dir)

	pending, err := am.GeneratePIN("phone")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}

	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}
	am.storePath = filepath.Join(blocker, "native_clients.json")

	_, _, _, err = am.PairWithPIN(pending.PIN, "attacker")
	requireInvalidPIN(t, err, "wrong device name with an unwritable store")

	if pins := am.GetPendingPINs(); len(pins) != 0 {
		t.Errorf("a PIN whose attempt counter cannot be persisted must be burned, got %d pending PINs", len(pins))
	}
}
