package channels

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func failRandRead(t *testing.T) {
	t.Helper()

	original := randRead
	randRead = func([]byte) (int, error) { return 0, errors.New("entropy source unavailable") }
	t.Cleanup(func() { randRead = original })
}

func TestGeneratePINFailsClosed(t *testing.T) {
	auth, err := NewAuthManager(defaultTestCfg(), t.TempDir())
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}

	pending, err := auth.GeneratePIN("phone")
	if err != nil {
		t.Fatalf("GeneratePIN with a working entropy source: %v", err)
	}
	pinsBefore := len(auth.GetPendingPINs())

	failRandRead(t)

	pin, err := auth.GeneratePIN("phone")
	if !errors.Is(err, errEntropyUnavailable) {
		t.Fatalf("GeneratePIN error = %v, want errEntropyUnavailable", err)
	}
	if pin != nil {
		t.Errorf("GeneratePIN returned a PIN (%+v) despite the entropy failure", pin)
	}
	if got := len(auth.GetPendingPINs()); got != pinsBefore {
		t.Errorf("pending PIN count = %d, want %d: no PIN may be minted without entropy", got, pinsBefore)
	}

	if _, _, _, err := auth.PairWithPIN(pending.PIN, "phone"); !errors.Is(err, errEntropyUnavailable) {
		t.Errorf("PairWithPIN error = %v, want errEntropyUnavailable", err)
	}

	if _, err := NewAuthManager(defaultTestCfg(), t.TempDir()); !errors.Is(err, errEntropyUnavailable) {
		t.Errorf("NewAuthManager error = %v, want errEntropyUnavailable", err)
	}

	randRead = func(b []byte) (int, error) {
		for i := range b {
			b[i] = 7
		}
		return len(b), nil
	}
	if _, err := auth.GeneratePIN("phone"); err != nil {
		t.Errorf("GeneratePIN after the entropy source recovered: %v", err)
	}
}

func TestGeneratePINFailsClosedHandlers(t *testing.T) {
	ts := newNativeTestServer(t)

	pinReq, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/auth/pin?device_name=webui", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	pinReq.Header.Set("Authorization", "Bearer "+ts.token)

	pending, err := ts.channel.auth.GeneratePIN("webui")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}

	failRandRead(t)

	resp, err := http.DefaultClient.Do(pinReq)
	if err != nil {
		t.Fatalf("GET /auth/pin: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("GET /auth/pin status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	var pinErr APIError
	if err := json.NewDecoder(resp.Body).Decode(&pinErr); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if pinErr.Code != errorCodePairingUnavailable {
		t.Errorf("GET /auth/pin code = %q, want %q", pinErr.Code, errorCodePairingUnavailable)
	}

	body := mustMarshal(AuthPairRequest{PIN: pending.PIN, DeviceName: "webui"})
	pairResp, err := http.Post(ts.server.URL+"/api/v1/auth/pair", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST /auth/pair: %v", err)
	}
	defer pairResp.Body.Close()

	if pairResp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("POST /auth/pair status = %d, want %d", pairResp.StatusCode, http.StatusInternalServerError)
	}
	var pairErr APIError
	if err := json.NewDecoder(pairResp.Body).Decode(&pairErr); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if pairErr.Code != errorCodePairingUnavailable {
		t.Errorf("POST /auth/pair code = %q, want %q", pairErr.Code, errorCodePairingUnavailable)
	}
	if strings.Contains(pairErr.Message, invalidPINMessage) {
		t.Errorf("entropy failure must not blame the PIN, got %q", pairErr.Message)
	}
}

func TestHandleRefresh_EntropyFailureIsServerError(t *testing.T) {
	ts := newNativeTestServer(t)

	pending, err := ts.channel.auth.GeneratePIN("webui")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	_, _, refreshToken, err := ts.channel.auth.PairWithPIN(pending.PIN, "webui")
	if err != nil {
		t.Fatalf("PairWithPIN: %v", err)
	}

	failRandRead(t)

	body := mustMarshal(AuthRefreshRequest{RefreshToken: refreshToken})
	resp, err := http.Post(ts.server.URL+"/api/v1/auth/refresh", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST /auth/refresh: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	var apiErr APIError
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if apiErr.Code != errorCodePairingUnavailable {
		t.Errorf("code = %q, want %q", apiErr.Code, errorCodePairingUnavailable)
	}
}

func TestAuthManagerEntropyFailureKeepsExistingStore(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.NativeConfig{PinExpiryMinutes: 5, MaxClients: 5, TokenExpiryDays: 30}

	am := newJSONAuthManager(t, cfg, dir)
	pending, err := am.GeneratePIN("phone")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}

	failRandRead(t)

	if _, err := NewAuthManager(cfg, dir); !errors.Is(err, errEntropyUnavailable) {
		t.Fatalf("NewAuthManager error = %v, want errEntropyUnavailable", err)
	}

	if _, _, _, err := am.PairWithPIN(pending.PIN, "phone"); !errors.Is(err, errEntropyUnavailable) {
		t.Fatalf("PairWithPIN error = %v, want errEntropyUnavailable", err)
	}
	if pins := am.GetPendingPINs(); len(pins) != 1 {
		t.Errorf("a failed pairing must not consume the PIN, got %d pending PINs", len(pins))
	}
}
