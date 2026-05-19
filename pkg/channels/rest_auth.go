package channels

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (n *NativeChannel) handleGetPIN(w http.ResponseWriter, r *http.Request) {
	deviceName := getQueryParam(r, "device_name")

	pending, err := n.auth.GeneratePIN(deviceName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "pin_error")
		return
	}

	writeJSON(w, http.StatusOK, AuthPINResponse{
		PIN:     pending.PIN,
		Expires: pending.Expires.Format(time.RFC3339),
	})
}

func (n *NativeChannel) handlePair(w http.ResponseWriter, r *http.Request) {
	var req AuthPairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	client, token, refreshToken, err := n.auth.PairWithPIN(req.PIN, req.DeviceName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "pair_error")
		return
	}

	writeJSON(w, http.StatusCreated, AuthPairResponse{
		Token:        token,
		RefreshToken: refreshToken,
		Expires:      client.Expires.Format(time.RFC3339),
		ClientID:     client.ClientID,
	})
}

func (n *NativeChannel) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req AuthRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	client, token, refreshToken, err := n.auth.RefreshToken(req.RefreshToken)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "refresh_error")
		return
	}

	writeJSON(w, http.StatusOK, AuthRefreshResponse{
		Token:        token,
		RefreshToken: refreshToken,
		Expires:      client.Expires.Format(time.RFC3339),
	})
}

func (n *NativeChannel) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		writeJSON(w, http.StatusOK, AuthStatusResponse{Valid: false})
		return
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == authHeader {
		writeJSON(w, http.StatusOK, AuthStatusResponse{Valid: false})
		return
	}

	client, valid := n.auth.ValidateToken(token)
	resp := AuthStatusResponse{Valid: valid}
	if valid && client != nil {
		resp.ClientID = client.ClientID
		resp.DeviceName = client.DeviceName
		resp.Expires = client.Expires.Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, resp)
}
