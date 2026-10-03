// Regression tests for #330: a gateway whose shared SQLite store failed to open
// on a SQLite-capable binary must NOT answer pairing requests with the same
// 400 "invalid PIN" it uses for a genuinely wrong PIN.
//
// The scenario these tests reproduce is the "F2" case measured during the #329
// review: a healthy CLI writes the pending PIN to the shared database, the
// gateway runs degraded (repo == nil, JSON file empty), so the PIN is invisible
// to it and every redeem attempt fails forever with a message that blames the
// user's PIN. The fix marks the auth manager degraded and turns both pairing
// endpoints into 503 + "store_unavailable" carrying the store error.

package channels

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

// realStoreOpenError produces a store.Open failure that is NOT
// store.ErrUnsupportedPlatform - i.e. exactly the class of real/transient
// failure (permissions, corrupted file, locked DB, missing parent) that leaves
// a SQLite-capable gateway degraded. The distinction is the axis of the fix:
// ErrUnsupportedPlatform means JSON is the legitimate shared backend and
// pairing must keep working.
func realStoreOpenError(t *testing.T) error {
	t.Helper()

	// A regular file where the DB directory should be: store.Open cannot
	// create lele.db inside it. Same trick cmd/lele/onboard_pin_test.go uses.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("plain file"), 0o644); err != nil {
		t.Fatalf("writing blocker file: %v", err)
	}

	_, err := store.Open(filepath.Join(blocker, "lele.db"))
	if err == nil {
		t.Fatal("store.Open inside a regular file must fail")
	}
	if errors.Is(err, store.ErrUnsupportedPlatform) {
		t.Skipf("this platform has no SQLite; the degraded path cannot be exercised: %v", err)
	}
	return err
}

// newHealthyCLIAuth builds the CLI side of the story: an AuthManager wired to a
// real SQLite store, which is where GeneratePIN persists the pending PIN.
func newHealthyCLIAuth(t *testing.T) (*AuthManager, *store.Store) {
	t.Helper()

	s, err := store.Open(filepath.Join(t.TempDir(), "lele.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	cfg := config.DefaultConfig()
	am, err := NewAuthManager(&cfg.Channels.Native, t.TempDir())
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	am.SetStore(s.NativeClients())
	return am, s
}

// TestPairWithPIN_DegradedGateway_IsDiagnosable is the central test: the PIN is
// valid and present in the shared database, the gateway cannot open that
// database, and the answer must say so instead of "invalid PIN".
func TestPairWithPIN_DegradedGateway_IsDiagnosable(t *testing.T) {
	cliAuth, s := newHealthyCLIAuth(t)

	pending, err := cliAuth.GeneratePIN("cli-phone")
	if err != nil {
		t.Fatalf("CLI GeneratePIN: %v", err)
	}

	// Gateway side: store.Open failed at startup, so the channel keeps its
	// JSON backend and the gateway marks it degraded (cmd/lele/gateway.go).
	storeErr := realStoreOpenError(t)
	gw := newNativeTestServer(t)
	gw.channel.auth.SetStoreUnavailable(storeErr)

	body := mustMarshal(AuthPairRequest{PIN: pending.PIN, DeviceName: "cli-phone"})
	resp, err := http.Post(gw.server.URL+"/api/v1/auth/pair", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST /auth/pair: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (a degraded gateway is a server-side fault, not a bad credential)",
			resp.StatusCode, http.StatusServiceUnavailable)
	}

	var apiErr APIError
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decoding error body: %v", err)
	}
	if apiErr.Code != errorCodeStoreUnavailable {
		t.Errorf("code = %q, want %q", apiErr.Code, errorCodeStoreUnavailable)
	}
	// The whole point: the operator must be able to tell this apart from a
	// typo'd PIN without reading the gateway's log.
	if strings.Contains(apiErr.Message, "invalid PIN") {
		t.Errorf("message must not blame the PIN, got %q", apiErr.Message)
	}
	if !strings.Contains(apiErr.Message, storeErr.Error()) {
		t.Errorf("message must carry the store error %q, got %q", storeErr.Error(), apiErr.Message)
	}

	// The degraded gateway must not have consumed the PIN: once the store is
	// reachable again the very same PIN is still redeemable.
	if _, _, found, ferr := s.NativeClients().GetPendingPIN(pending.PIN); ferr != nil {
		t.Fatalf("GetPendingPIN: %v", ferr)
	} else if !found {
		t.Error("degraded gateway consumed a PIN it could not even read")
	}
	if _, _, _, err := cliAuth.PairWithPIN(pending.PIN, "cli-phone"); err != nil {
		t.Errorf("healthy side must still redeem the PIN: %v", err)
	}
}

// TestHandleGetPIN_DegradedGateway_Refuses covers the mint side: a PIN minted
// into the JSON file while the rest of the system uses SQLite is a dead PIN (it
// vanishes the moment the store opens again), so the endpoint refuses too.
func TestHandleGetPIN_DegradedGateway_Refuses(t *testing.T) {
	storeErr := realStoreOpenError(t)
	gw := newNativeTestServer(t)
	gw.channel.auth.SetStoreUnavailable(storeErr)

	req, err := http.NewRequest(http.MethodGet, gw.server.URL+"/api/v1/auth/pin?device_name=webui", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gw.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /auth/pin: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}

	var apiErr APIError
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decoding error body: %v", err)
	}
	if apiErr.Code != errorCodeStoreUnavailable {
		t.Errorf("code = %q, want %q", apiErr.Code, errorCodeStoreUnavailable)
	}
}

// TestPairWithPIN_NilRepoWithoutDegradation_StillRedeems is the guard on the
// other side of the axis: a nil repo alone (no-SQLite platform, where JSON is
// the real shared backend) must keep pairing working exactly as before. Only
// the explicit degraded flag changes the answer.
func TestPairWithPIN_NilRepoWithoutDegradation_StillRedeems(t *testing.T) {
	cfg := config.DefaultConfig()
	am, err := NewAuthManager(&cfg.Channels.Native, t.TempDir())
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	if backend := am.StorageBackend(); backend != "json" {
		t.Fatalf("StorageBackend() = %q, want json", backend)
	}
	if reason, degraded := am.StoreUnavailable(); degraded {
		t.Fatalf("a fresh manager must not be degraded, got reason %q", reason)
	}

	pending, err := am.GeneratePIN("mips-device")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	client, token, _, err := am.PairWithPIN(pending.PIN, "mips-device")
	if err != nil {
		t.Fatalf("PairWithPIN on the legitimate JSON backend: %v", err)
	}
	if client == nil || token == "" {
		t.Fatal("expected a paired client and a token")
	}
}

// TestSetStore_ClearsDegradation pins the recovery path: wiring a working store
// into a degraded manager supersedes the flag, so a gateway that gets its store
// back (config reload re-injecting the repo) starts serving pairing again.
func TestSetStore_ClearsDegradation(t *testing.T) {
	cliAuth, s := newHealthyCLIAuth(t)
	pending, err := cliAuth.GeneratePIN("cli-phone")
	if err != nil {
		t.Fatalf("CLI GeneratePIN: %v", err)
	}

	cfg := config.DefaultConfig()
	am, err := NewAuthManager(&cfg.Channels.Native, t.TempDir())
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	am.SetStoreUnavailable(realStoreOpenError(t))

	if _, _, _, err := am.PairWithPIN(pending.PIN, "cli-phone"); !errors.Is(err, ErrPairingUnavailable) {
		t.Fatalf("degraded: err = %v, want ErrPairingUnavailable", err)
	}

	// Same store the CLI used: the degraded gateway recovers and the PIN that
	// was unreachable a moment ago is now redeemable.
	am.SetStore(s.NativeClients())

	if _, degraded := am.StoreUnavailable(); degraded {
		t.Fatal("SetStore with a working repo must clear the degradation")
	}
	if backend := am.StorageBackend(); backend != "sqlite" {
		t.Fatalf("StorageBackend() = %q, want sqlite", backend)
	}
	if _, token, _, err := am.PairWithPIN(pending.PIN, "cli-phone"); err != nil || token == "" {
		t.Fatalf("recovered PairWithPIN: err = %v, token empty = %v", err, token == "")
	}

	// Passing nil clears the flag without wiring anything, which is how a
	// caller says "the store is back" before SetStore runs.
	am.SetStoreUnavailable(realStoreOpenError(t))
	am.SetStoreUnavailable(nil)
	if _, degraded := am.StoreUnavailable(); degraded {
		t.Fatal("SetStoreUnavailable(nil) must clear the degradation")
	}
}

// TestStorageStatus_ReportsDegradedBackend covers the option-3 signal: the
// status payload must say the gateway is on JSON and why, so the 400/503 is
// diagnosable from outside the process.
func TestStorageStatus_ReportsDegradedBackend(t *testing.T) {
	storeErr := realStoreOpenError(t)
	gw := newNativeTestServer(t)

	if st := gw.channel.storageStatus(); st == nil || st.Backend != "json" || st.Degraded {
		t.Fatalf("healthy-but-JSON status = %+v, want backend json / degraded false", st)
	}

	gw.channel.auth.SetStoreUnavailable(storeErr)

	req, err := http.NewRequest(http.MethodGet, gw.server.URL+"/api/v1/status", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gw.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/status: %v", err)
	}
	defer resp.Body.Close()

	var payload SystemStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decoding status: %v", err)
	}
	if payload.Storage == nil {
		t.Fatal("status payload has no storage block")
	}
	if payload.Storage.Backend != "json" || !payload.Storage.Degraded {
		t.Fatalf("storage = %+v, want backend json / degraded true", payload.Storage)
	}
	if !strings.Contains(payload.Storage.Error, storeErr.Error()) {
		t.Errorf("storage error = %q, want it to contain %q", payload.Storage.Error, storeErr.Error())
	}
}
