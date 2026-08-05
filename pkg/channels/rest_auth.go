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

func (n *NativeChannel) handleListClients(w http.ResponseWriter, r *http.Request) {
	clients := n.auth.ListClients()
	safeClients := make([]SafeClientInfo, len(clients))
	for i, c := range clients {
		safeClients[i] = SafeClientInfo{
			ClientID:    c.ClientID,
			DeviceName:  c.DeviceName,
			Created:     c.Created,
			Expires:     c.Expires,
			LastSeen:    c.LastSeen,
			SessionKeys: c.SessionKeys,
		}
	}
	writeJSON(w, http.StatusOK, safeClients)
}

func (n *NativeChannel) handleLogout(w http.ResponseWriter, r *http.Request) {
	clientID := r.Header.Get("X-Client-Id")
	if clientID == "" {
		writeError(w, http.StatusBadRequest, "missing client context", "logout_error")
		return
	}

	if err := n.auth.RemoveClient(clientID); err != nil {
		// Client already removed is not an error
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (n *NativeChannel) handleRemoveClient(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("clientID")
	if clientID == "" {
		writeError(w, http.StatusBadRequest, "missing clientID", "client_id_missing")
		return
	}

	err := n.auth.RemoveClient(clientID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "remove_client_error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
