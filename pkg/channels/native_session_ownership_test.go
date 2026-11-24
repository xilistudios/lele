package channels

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/xilistudios/lele/pkg/providers"
)

func pairNativeTestClient(t *testing.T, ts *nativeTestServer, deviceName string) (clientID, token string) {
	t.Helper()

	pending, err := ts.channel.auth.GeneratePIN(deviceName)
	if err != nil {
		t.Fatalf("GeneratePIN() error = %v", err)
	}

	client, token, _, err := ts.channel.auth.PairWithPIN(pending.PIN, deviceName)
	if err != nil {
		t.Fatalf("PairWithPIN() error = %v", err)
	}

	return client.ClientID, token
}

func nativeAuthedRequest(t *testing.T, method, requestURL, token string, body []byte) *http.Response {
	t.Helper()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, requestURL, reader)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	return resp
}

func clientSessionKeys(t *testing.T, ts *nativeTestServer, clientID string) []string {
	t.Helper()

	client, ok := ts.channel.auth.GetClient(clientID)
	if !ok {
		t.Fatalf("client %q not found", clientID)
	}
	return client.SessionKeys
}

func containsKey(keys []string, key string) bool {
	for _, candidate := range keys {
		if candidate == key {
			return true
		}
	}
	return false
}

func TestValidateSessionOwnership_RejectsUntrackedKey(t *testing.T) {
	ts := newNativeTestServer(t)
	clientB, _ := pairNativeTestClient(t, ts, "Second Device")

	ownedKey := "native:" + ts.clientID + ":owned"
	ts.channel.auth.TrackSessionKey(ts.clientID, ownedKey)

	cases := []struct {
		name       string
		clientID   string
		sessionKey string
		want       bool
	}{
		{"owner_reaches_tracked_key", ts.clientID, ownedKey, true},
		{"foreign_client_cannot_reach_tracked_key", clientB, ownedKey, false},
		{"owner_reaches_default_key", ts.clientID, ts.clientID, true},
		{"foreign_client_cannot_reach_default_key", clientB, ts.clientID, false},
		{"unknown_key_rejected_for_owner", ts.clientID, "untracked-session", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ts.channel.validateSessionOwnership(tc.clientID, tc.sessionKey); got != tc.want {
				t.Fatalf("validateSessionOwnership(%q, %q) = %v, want %v", tc.clientID, tc.sessionKey, got, tc.want)
			}
		})
	}
}

func TestNativeChannelChatSessionsScopedToCaller(t *testing.T) {
	ts := newNativeTestServer(t)
	clientB, _ := pairNativeTestClient(t, ts, "Second Device")

	ownedKey := "native:" + ts.clientID + ":a-only"
	foreignKey := "native:" + clientB + ":b-only"
	ts.channel.auth.TrackSessionKey(ts.clientID, ownedKey)
	ts.channel.auth.TrackSessionKey(clientB, foreignKey)

	for _, key := range []string{ownedKey, foreignKey, "heartbeat", "cron-1"} {
		ts.loop.histories[key] = []providers.Message{{Role: "user", Content: "hi"}}
	}

	listKeys := func(t *testing.T, query string) map[string]bool {
		t.Helper()
		resp := nativeAuthedRequest(t, http.MethodGet, ts.server.URL+"/api/v1/chat/sessions"+query, ts.token, nil)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var payload ChatSessionsResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}

		keys := make(map[string]bool, len(payload.Sessions))
		for _, session := range payload.Sessions {
			keys[session.Key] = true
		}
		return keys
	}

	t.Run("own_sessions_only", func(t *testing.T) {
		keys := listKeys(t, "")
		if !keys[ownedKey] {
			t.Fatalf("caller's own session %q missing from %v", ownedKey, keys)
		}
		if keys[foreignKey] {
			t.Fatalf("listing leaked another client's session %q: %v", foreignKey, keys)
		}
		if keys["heartbeat"] || keys["cron-1"] {
			t.Fatalf("system sessions leaked into the default listing: %v", keys)
		}
	})

	t.Run("include_system_adds_system_but_not_foreign", func(t *testing.T) {
		keys := listKeys(t, "?include_system=true")
		if !keys[ownedKey] || !keys["heartbeat"] || !keys["cron-1"] {
			t.Fatalf("system merge dropped expected sessions: %v", keys)
		}
		if keys[foreignKey] {
			t.Fatalf("system merge leaked another client's session %q: %v", foreignKey, keys)
		}
	})
}

func TestNativeChannelChatSendCannotAdoptForeignSession(t *testing.T) {
	ts := newNativeTestServer(t)
	clientB, tokenB := pairNativeTestClient(t, ts, "Second Device")

	foreignKey := "native:" + ts.clientID + ":a-only"
	ts.channel.auth.TrackSessionKey(ts.clientID, foreignKey)
	before := clientSessionKeys(t, ts, clientB)

	body, _ := json.Marshal(ChatSendRequest{SessionKey: foreignKey, Content: "hijack"})
	resp := nativeAuthedRequest(t, http.MethodPost, ts.server.URL+"/api/v1/chat/send", tokenB, body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	after := clientSessionKeys(t, ts, clientB)
	if containsKey(after, foreignKey) {
		t.Fatalf("rejected send still adopted %q: before=%v after=%v", foreignKey, before, after)
	}
	if len(after) != len(before) {
		t.Fatalf("rejected send changed the caller's session keys: before=%v after=%v", before, after)
	}
}

func TestNativeChannelWSSubscribeCannotAdoptForeignSession(t *testing.T) {
	ts := newNativeTestServer(t)
	clientB, tokenB := pairNativeTestClient(t, ts, "Second Device")

	foreignKey := "native:" + ts.clientID + ":a-only"
	ts.channel.auth.TrackSessionKey(ts.clientID, foreignKey)

	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws?token=" + url.QueryEscape(tokenB)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	if welcome := readWSMessage(t, conn); welcome.Event != "welcome" {
		t.Fatalf("first event = %q, want welcome", welcome.Event)
	}

	if err := conn.WriteJSON(map[string]interface{}{
		"event": "subscribe",
		"data":  map[string]interface{}{"session_key": foreignKey},
	}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	event := readWSMessage(t, conn)
	if event.Event != "error" {
		t.Fatalf("event = %q, want error", event.Event)
	}

	var payload WSErrorPayload
	decodeWSData(t, event.Data, &payload)
	if payload.Code != "forbidden" {
		t.Fatalf("error code = %q, want forbidden", payload.Code)
	}

	if keys := clientSessionKeys(t, ts, clientB); containsKey(keys, foreignKey) {
		t.Fatalf("rejected subscribe still adopted %q: %v", foreignKey, keys)
	}
}

func TestNativeChannelSubagentHistoryRequiresOwnedParent(t *testing.T) {
	ts := newNativeTestServer(t)
	_, tokenB := pairNativeTestClient(t, ts, "Second Device")

	parentKey := "native:" + ts.clientID + ":uuid-test-session"
	ts.channel.auth.TrackSessionKey(ts.clientID, parentKey)

	subagentKey := parentKey + ":subagent-1"
	ts.loop.histories[subagentKey] = []providers.Message{
		{Role: "user", Content: "Subagent task"},
	}
	ts.loop.subagentParents[subagentKey] = parentKey

	historyURL := ts.server.URL + "/api/v1/chat/sessions/" + url.PathEscape(parentKey) + "/history/subagent-1"

	foreignResp := nativeAuthedRequest(t, http.MethodGet, historyURL, tokenB, nil)
	defer foreignResp.Body.Close()
	if foreignResp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign client status = %d, want %d", foreignResp.StatusCode, http.StatusForbidden)
	}

	ownerResp := nativeAuthedRequest(t, http.MethodGet, historyURL, ts.token, nil)
	defer ownerResp.Body.Close()
	if ownerResp.StatusCode != http.StatusOK {
		t.Fatalf("owner status = %d, want %d", ownerResp.StatusCode, http.StatusOK)
	}
}

func TestNativeChannelDeleteUntracksOnlyCaller(t *testing.T) {
	ts := newNativeTestServer(t)
	clientB, _ := pairNativeTestClient(t, ts, "Second Device")

	sharedKey := "shared-session"
	ts.channel.auth.TrackSessionKey(ts.clientID, sharedKey)
	ts.channel.auth.TrackSessionKey(clientB, sharedKey)

	resp := nativeAuthedRequest(t, http.MethodDelete, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sharedKey)+"/", ts.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if keys := clientSessionKeys(t, ts, ts.clientID); containsKey(keys, sharedKey) {
		t.Fatalf("delete did not untrack %q from the caller: %v", sharedKey, keys)
	}
	if keys := clientSessionKeys(t, ts, clientB); !containsKey(keys, sharedKey) {
		t.Fatalf("delete untracked %q from another client: %v", sharedKey, keys)
	}
}

func TestNativeChannelSystemSessionsVisibleToAllClients(t *testing.T) {
	ts := newNativeTestServer(t)
	_, tokenB := pairNativeTestClient(t, ts, "Second Device")

	for _, key := range []string{"heartbeat", "cron-1", "cron-spawn-1"} {
		t.Run(key, func(t *testing.T) {
			ts.loop.histories[key] = []providers.Message{{Role: "user", Content: "automation"}}

			resp := nativeAuthedRequest(t, http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(key)+"/history", tokenB, nil)
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d for system session %q", resp.StatusCode, http.StatusOK, key)
			}
		})
	}
}

func TestNativeChannelSessionListingsDoNotLeakAcrossClients(t *testing.T) {
	ts := newNativeTestServer(t)
	clientB, _ := pairNativeTestClient(t, ts, "Second Device")

	ownedKey := "native:" + ts.clientID + ":a-only"
	foreignKey := "native:" + clientB + ":b-only"
	ts.channel.auth.TrackSessionKey(ts.clientID, ownedKey)
	ts.channel.auth.TrackSessionKey(clientB, foreignKey)

	ts.loop.histories[ownedKey] = []providers.Message{{Role: "user", Content: "hi"}}
	ts.loop.histories[foreignKey] = []providers.Message{{Role: "user", Content: "hi"}}
	ts.loop.histories["heartbeat"] = []providers.Message{{Role: "user", Content: "hi"}}

	for _, query := range []string{"", "?include_system=true", "?kind=chat"} {
		t.Run(query, func(t *testing.T) {
			resp := nativeAuthedRequest(t, http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/meta"+query, ts.token, nil)
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
			}

			var payload ChatSessionsResponse
			if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}

			keys := make(map[string]bool, len(payload.Sessions))
			for _, session := range payload.Sessions {
				keys[session.Key] = true
			}

			if !keys[ownedKey] {
				t.Fatalf("own session %q missing from meta listing %v", ownedKey, keys)
			}
			if keys[foreignKey] {
				t.Fatalf("meta listing leaked another client's session %q: %v", foreignKey, keys)
			}
		})
	}
}
