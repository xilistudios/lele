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
	// FIX-2: /auth/pin is behind withAuth — must send Authorization header.
	req.Header.Set("Authorization", "Bearer "+ts.token)
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

	// 1. Get a PIN (requires authentication after FIX-2)
	pinReq, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/auth/pin?device_name=test-device", nil)
	pinReq.Header.Set("Authorization", "Bearer "+ts.token)
	pinResp, err := http.DefaultClient.Do(pinReq)
	if err != nil {
		t.Fatalf("PIN request error = %v", err)
	}
	defer pinResp.Body.Close()

	if pinResp.StatusCode != http.StatusOK {
		t.Fatalf("PIN status = %d, want %d", pinResp.StatusCode, http.StatusOK)
	}

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

func TestHandlePairWithoutDeviceName_WhenPINIssuedWithoutOne(t *testing.T) {
	ts := newNativeTestServer(t)

	// Generate PIN via auth manager directly (without device_name).
	pending, err := ts.channel.auth.GeneratePIN("")
	if err != nil {
		t.Fatalf("GeneratePIN() error = %v", err)
	}

	// Pair without device_name — must be rejected.
	body := mustMarshal(AuthPairRequest{PIN: pending.PIN, DeviceName: ""})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/auth/pair", strings.NewReader(string(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	var apiErr APIError
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if apiErr.Code != "pair_error" {
		t.Fatalf("error code = %q, want pair_error", apiErr.Code)
	}
}

func TestHandlePairWithDeviceName_WhenPINIssuedWithoutOne(t *testing.T) {
	ts := newNativeTestServer(t)

	pending, err := ts.channel.auth.GeneratePIN("")
	if err != nil {
		t.Fatalf("GeneratePIN() error = %v", err)
	}

	// Pair WITH device_name — should succeed.
	body := mustMarshal(AuthPairRequest{PIN: pending.PIN, DeviceName: "CLI-Device"})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/auth/pair", strings.NewReader(string(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
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

func TestHandleGetPIN_UnauthenticatedReturns401(t *testing.T) {
	ts := newNativeTestServer(t)

	// FIX-2: /auth/pin must be behind withAuth.
	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/auth/pin", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	// No Authorization header.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
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

func TestHandleListAndRemoveClients(t *testing.T) {
	ts := newNativeTestServer(t)

	// 1. Get a PIN (requires authentication after FIX-2)
	pinReq, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/auth/pin?device_name=test-device", nil)
	pinReq.Header.Set("Authorization", "Bearer "+ts.token)
	pinResp, err := http.DefaultClient.Do(pinReq)
	if err != nil {
		t.Fatalf("PIN request error = %v", err)
	}
	defer pinResp.Body.Close()

	if pinResp.StatusCode != http.StatusOK {
		t.Fatalf("PIN status = %d, want %d", pinResp.StatusCode, http.StatusOK)
	}

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

	var pairPayload AuthPairResponse
	if err := json.NewDecoder(pairResp.Body).Decode(&pairPayload); err != nil {
		t.Fatalf("Decode pair error = %v", err)
	}

	// 3. List clients (requires authorization)
	listReq, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/auth/clients", nil)
	listReq.Header.Set("Authorization", "Bearer "+pairPayload.Token)
	listResp, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatalf("List request error = %v", err)
	}
	defer listResp.Body.Close()

	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listResp.StatusCode, http.StatusOK)
	}

	var clients []SafeClientInfo
	if err := json.NewDecoder(listResp.Body).Decode(&clients); err != nil {
		t.Fatalf("Decode list error = %v", err)
	}

	if len(clients) != 2 {
		t.Fatalf("len(clients) = %d, want 2", len(clients))
	}

	var foundNew, foundDefault bool
	for _, c := range clients {
		if c.ClientID == pairPayload.ClientID {
			foundNew = true
			if c.DeviceName != "test-device" {
				t.Fatalf("deviceName = %s, want %s", c.DeviceName, "test-device")
			}
		} else if c.DeviceName == "Test Desktop" {
			foundDefault = true
		}
	}

	if !foundNew {
		t.Fatal("expected to find newly paired client in the list")
	}
	if !foundDefault {
		t.Fatal("expected to find default Test Desktop client in the list")
	}

	// 4. Remove the new client
	removeReq, _ := http.NewRequest(http.MethodDelete, ts.server.URL+"/api/v1/auth/clients/"+pairPayload.ClientID, nil)
	removeReq.Header.Set("Authorization", "Bearer "+pairPayload.Token)
	removeResp, err := http.DefaultClient.Do(removeReq)
	if err != nil {
		t.Fatalf("Remove request error = %v", err)
	}
	defer removeResp.Body.Close()

	if removeResp.StatusCode != http.StatusOK {
		t.Fatalf("remove status = %d, want %d", removeResp.StatusCode, http.StatusOK)
	}

	// 5. Try to list again using default token (since the new one is revoked/unauthorized)
	listReq2, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/auth/clients", nil)
	listReq2.Header.Set("Authorization", "Bearer "+ts.token)
	listResp2, err := http.DefaultClient.Do(listReq2)
	if err != nil {
		t.Fatalf("List 2 request error = %v", err)
	}
	defer listResp2.Body.Close()

	if listResp2.StatusCode != http.StatusOK {
		t.Fatalf("list 2 status = %d, want %d", listResp2.StatusCode, http.StatusOK)
	}

	var clientsAfter []SafeClientInfo
	if err := json.NewDecoder(listResp2.Body).Decode(&clientsAfter); err != nil {
		t.Fatalf("Decode list after error = %v", err)
	}

	if len(clientsAfter) != 1 {
		t.Fatalf("len(clientsAfter) = %d, want 1", len(clientsAfter))
	}
	if clientsAfter[0].DeviceName != "Test Desktop" {
		t.Fatalf("remaining client device name = %s, want %s", clientsAfter[0].DeviceName, "Test Desktop")
	}

	// 6. Try to list using the revoked token (should fail with 401)
	listReq3, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/auth/clients", nil)
	listReq3.Header.Set("Authorization", "Bearer "+pairPayload.Token)
	listResp3, err := http.DefaultClient.Do(listReq3)
	if err != nil {
		t.Fatalf("List 3 request error = %v", err)
	}
	defer listResp3.Body.Close()

	if listResp3.StatusCode != http.StatusUnauthorized {
		t.Fatalf("list 3 status = %d, want %d", listResp3.StatusCode, http.StatusUnauthorized)
	}
}
