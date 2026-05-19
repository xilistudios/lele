package channels

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestHandleGetPIN(t *testing.T) {
	ts := newNativeTestServer(t)

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/auth/pin", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload AuthPINResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if payload.PIN == "" {
		t.Fatal("expected non-empty PIN")
	}
	if payload.Expires == "" {
		t.Fatal("expected non-empty Expires")
	}
}

func TestHandlePairAndRefresh(t *testing.T) {
	ts := newNativeTestServer(t)

	// 1. Get a PIN
	pinReq, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/auth/pin?device_name=test-device", nil)
	pinResp, err := http.DefaultClient.Do(pinReq)
	if err != nil {
		t.Fatalf("PIN request error = %v", err)
	}
	defer pinResp.Body.Close()

	var pinPayload AuthPINResponse
	if err := json.NewDecoder(pinResp.Body).Decode(&pinPayload); err != nil {
		t.Fatalf("Decode PIN error = %v", err)
	}

	// 2. Pair with the PIN
	pairBody := mustMarshal(AuthPairRequest{PIN: pinPayload.PIN, DeviceName: "test-device"})
	pairReq, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/auth/pair", strings.NewReader(string(pairBody)))
	pairResp, err := http.DefaultClient.Do(pairReq)
	if err != nil {
		t.Fatalf("Pair request error = %v", err)
	}
	defer pairResp.Body.Close()

	if pairResp.StatusCode != http.StatusCreated {
		t.Fatalf("pair status = %d, want %d", pairResp.StatusCode, http.StatusCreated)
	}

	var pairPayload AuthPairResponse
	if err := json.NewDecoder(pairResp.Body).Decode(&pairPayload); err != nil {
		t.Fatalf("Decode pair error = %v", err)
	}
	if pairPayload.Token == "" {
		t.Fatal("expected non-empty token")
	}
	if pairPayload.RefreshToken == "" {
		t.Fatal("expected non-empty refresh token")
	}
	if pairPayload.ClientID == "" {
		t.Fatal("expected non-empty client ID")
	}

	// 3. Refresh the token
	refreshBody := mustMarshal(AuthRefreshRequest{RefreshToken: pairPayload.RefreshToken})
	refreshReq, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/auth/refresh", strings.NewReader(string(refreshBody)))
	refreshResp, err := http.DefaultClient.Do(refreshReq)
	if err != nil {
		t.Fatalf("Refresh request error = %v", err)
	}
	defer refreshResp.Body.Close()

	if refreshResp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d, want %d", refreshResp.StatusCode, http.StatusOK)
	}

	var refreshPayload AuthRefreshResponse
	if err := json.NewDecoder(refreshResp.Body).Decode(&refreshPayload); err != nil {
		t.Fatalf("Decode refresh error = %v", err)
	}
	if refreshPayload.Token == "" {
		t.Fatal("expected non-empty refreshed token")
	}
}

func TestHandlePairInvalidPIN(t *testing.T) {
	ts := newNativeTestServer(t)

	body := mustMarshal(AuthPairRequest{PIN: "000000", DeviceName: "test"})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/auth/pair", strings.NewReader(string(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	var apiErr APIError
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if apiErr.Code != "pair_error" {
		t.Fatalf("error code = %q, want %q", apiErr.Code, "pair_error")
	}
}

func TestHandleRefreshInvalidToken(t *testing.T) {
	ts := newNativeTestServer(t)

	body := mustMarshal(AuthRefreshRequest{RefreshToken: "invalid-refresh-token"})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/auth/refresh", strings.NewReader(string(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}
