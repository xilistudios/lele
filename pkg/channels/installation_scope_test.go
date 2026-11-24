package channels

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/xilistudios/lele/pkg/providers"
)

func sessionHistoryStatus(t *testing.T, ts *nativeTestServer, token, sessionKey string) int {
	t.Helper()
	resp := nativeAuthedRequest(t, http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/history", token, nil)
	defer resp.Body.Close()
	return resp.StatusCode
}

func listedSessionKeys(t *testing.T, ts *nativeTestServer, token, path, query string) map[string]bool {
	t.Helper()

	resp := nativeAuthedRequest(t, http.MethodGet, ts.server.URL+path+query, token, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s%s status = %d, want %d", path, query, resp.StatusCode, http.StatusOK)
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

func seedNativeHistory(ts *nativeTestServer, keys ...string) {
	for _, key := range keys {
		ts.loop.histories[key] = []providers.Message{{Role: "user", Content: "external conversation"}}
	}
}

func installationScopedSessionKeys() []string {
	return []string{
		"heartbeat",
		"cron-1",
		"cron-spawn-1",
		"telegram:777001",
		"agent:main:telegram:direct:777001",
		"agent:main:telegram:group:-100200",
		"agent:main:telegram:default:direct:777001",
		"agent:main:discord:direct:4242",
		"agent:main:slack:channel:engineering",
		"agent:main:whatsapp:direct:34600111222",
		"agent:main:feishu:direct:ou_123",
		"agent:main:dingtalk:direct:manager-1",
		"agent:main:onebot:group:998877",
		"agent:main:qq:direct:112233",
		"agent:main:line:direct:user-1",
		"agent:main:maixcam:direct:cam-1",
	}
}

func TestInstallationScopedKeyClassification(t *testing.T) {
	cases := map[string]bool{
		"heartbeat":                            true,
		"cron-1":                               true,
		"cron-spawn-1":                         true,
		"telegram:777001":                      true,
		"discord:4242":                         true,
		"agent:main:telegram:direct:777001":    true,
		"agent:main:telegram:group:-100200":    true,
		"agent:main:telegram:default:direct:1": true,
		"agent:main:main":                      false,
		"agent:main:direct:777001":             false,
		"7f2f8a54-9c31-4ff2-8a1b-6f7f0f5b1c11": false,
		"native:abc:chat":                      false,
		"native:abc:telegram:777001":           false,
		"abc":                                  false,
	}

	for sessionKey, want := range cases {
		t.Run(sessionKey, func(t *testing.T) {
			if got := isInstallationScopedKey(sessionKey); got != want {
				t.Fatalf("isInstallationScopedKey(%q) = %v, want %v", sessionKey, got, want)
			}
		})
	}
}

func TestNativeChannelInstallationScopedSessionsStayReachable(t *testing.T) {
	ts := newNativeTestServer(t)
	_, tokenB := pairNativeTestClient(t, ts, "Second Device")

	seedNativeHistory(ts, installationScopedSessionKeys()...)

	for _, key := range installationScopedSessionKeys() {
		t.Run(key, func(t *testing.T) {
			if status := sessionHistoryStatus(t, ts, tokenB, key); status != http.StatusOK {
				t.Fatalf("foreign client history status = %d, want %d for %q", status, http.StatusOK, key)
			}

			body, _ := json.Marshal(ChatSendRequest{SessionKey: key, Content: "console reply"})
			resp := nativeAuthedRequest(t, http.MethodPost, ts.server.URL+"/api/v1/chat/send", tokenB, body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("foreign client send status = %d, want %d for %q", resp.StatusCode, http.StatusCreated, key)
			}
		})
	}

	t.Run("automation_rebinding_stays_available_to_the_console", func(t *testing.T) {
		body, _ := json.Marshal(SessionAgentUpdateRequest{AgentID: "research"})
		resp := nativeAuthedRequest(t, http.MethodPatch, ts.server.URL+"/api/v1/chat/sessions/heartbeat/agent", tokenB, body)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("foreign client PATCH agent status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})
}

func TestNativeChannelNativeChatSessionsStayPrivate(t *testing.T) {
	ts := newNativeTestServer(t)
	_, tokenB := pairNativeTestClient(t, ts, "Second Device")

	ownedKey := "native:" + ts.clientID + ":private-chat"
	ts.channel.auth.TrackSessionKey(ts.clientID, ownedKey)
	ts.loop.histories[ownedKey] = []providers.Message{{Role: "user", Content: "private"}}

	escaped := url.PathEscape(ownedKey)
	agentBody, _ := json.Marshal(SessionAgentUpdateRequest{AgentID: "research"})
	modelBody, _ := json.Marshal(SessionModelUpdateRequest{Model: "gpt-4o"})
	sendBody, _ := json.Marshal(ChatSendRequest{SessionKey: ownedKey, Content: "hijack"})

	cases := []struct {
		name   string
		method string
		path   string
		body   []byte
	}{
		{"read_history", http.MethodGet, "/api/v1/chat/sessions/" + escaped + "/history", nil},
		{"send", http.MethodPost, "/api/v1/chat/send", sendBody},
		{"clear", http.MethodPost, "/api/v1/chat/sessions/" + escaped + "/clear", nil},
		{"delete", http.MethodDelete, "/api/v1/chat/sessions/" + escaped + "/", nil},
		{"patch_agent", http.MethodPatch, "/api/v1/chat/sessions/" + escaped + "/agent", agentBody},
		{"patch_model", http.MethodPatch, "/api/v1/chat/sessions/" + escaped + "/model", modelBody},
		{"tools", http.MethodGet, "/api/v1/tools?session_key=" + escaped, nil},
		{"models", http.MethodGet, "/api/v1/models?session_key=" + escaped, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := nativeAuthedRequest(t, tc.method, ts.server.URL+tc.path, tokenB, tc.body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("foreign client status = %d, want %d", resp.StatusCode, http.StatusForbidden)
			}
		})
	}

	if agent := ts.loop.GetSessionAgent(ownedKey); agent != "main" {
		t.Fatalf("foreign client rebound the agent to %q", agent)
	}
	if model, pinned := ts.loop.sessionModels[ownedKey]; pinned {
		t.Fatalf("foreign client rebound the model to %q", model)
	}

	if status := sessionHistoryStatus(t, ts, ts.token, ownedKey); status != http.StatusOK {
		t.Fatalf("owner history status = %d, want %d", status, http.StatusOK)
	}
}

func TestNativeChannelToolsAndModelsAllowInstallationScope(t *testing.T) {
	ts := newNativeTestServer(t)
	_, tokenB := pairNativeTestClient(t, ts, "Second Device")

	ts.loop.histories["heartbeat"] = []providers.Message{{Role: "user", Content: "automation"}}

	for _, path := range []string{"/api/v1/tools", "/api/v1/models"} {
		t.Run(path+"?session_key=heartbeat", func(t *testing.T) {
			resp := nativeAuthedRequest(t, http.MethodGet, ts.server.URL+path+"?session_key=heartbeat", tokenB, nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
			}
		})
	}
}

func TestNativeChannelListingsExposeInstallationScopedSessions(t *testing.T) {
	ts := newNativeTestServer(t)
	clientB, _ := pairNativeTestClient(t, ts, "Second Device")

	foreignKey := "native:" + clientB + ":b-only"
	ts.channel.auth.TrackSessionKey(clientB, foreignKey)

	shared := []string{
		"heartbeat",
		"cron-1",
		"telegram:777001",
		"agent:main:telegram:direct:777001",
		"agent:main:discord:direct:4242",
	}
	callerParent := "native:" + ts.clientID + ":parent"
	ts.channel.auth.TrackSessionKey(ts.clientID, callerParent)
	callerSubagent := callerParent + ":subagent-1"
	foreignSubagent := foreignKey + ":subagent-1"
	ts.loop.subagentParents[callerSubagent] = callerParent
	ts.loop.subagentParents[foreignSubagent] = foreignKey

	seedNativeHistory(ts, append(shared, foreignKey, callerSubagent, foreignSubagent)...)

	for _, path := range []string{"/api/v1/chat/sessions", "/api/v1/chat/sessions/meta"} {
		t.Run(path, func(t *testing.T) {
			keys := listedSessionKeys(t, ts, ts.token, path, "?include_system=true")

			for _, key := range shared {
				if !keys[key] {
					t.Fatalf("installation-scoped session %q missing from %s: %v", key, path, keys)
				}
			}
			if !keys[callerSubagent] {
				t.Fatalf("owned-parent subagent %q missing from %s: %v", callerSubagent, path, keys)
			}
			if keys[foreignKey] || keys[foreignSubagent] {
				t.Fatalf("%s leaked another client's sessions %q/%q: %v", path, foreignKey, foreignSubagent, keys)
			}
		})
	}

	t.Run("kind_tab_still_works", func(t *testing.T) {
		keys := listedSessionKeys(t, ts, ts.token, "/api/v1/chat/sessions", "?kind=heartbeat")
		if !keys["heartbeat"] {
			t.Fatalf("heartbeat kind tab lost the heartbeat session: %v", keys)
		}
		if keys[foreignKey] {
			t.Fatalf("heartbeat kind tab leaked another client's session: %v", keys)
		}
	})
}

func TestSessionCreateConflictIsAliasAware(t *testing.T) {
	ts := newNativeTestServer(t)
	clientB, _ := pairNativeTestClient(t, ts, "Second Device")

	rotatedTarget := uuid.New().String()
	base := "native:" + clientB + ":base"
	ts.channel.auth.TrackSessionKey(clientB, base)
	ts.loop.sessionAliases[base] = rotatedTarget

	if conflict := ts.channel.sessionCreateConflict(ts.clientID, rotatedTarget); conflict == "" {
		t.Fatalf("alias target %q of another client's session was not reported as a conflict", rotatedTarget)
	}
}
