package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/xilistudios/lele/pkg/mcp"
)

func mcpAuthDo(t *testing.T, ts *nativeTestServer, method, url string, body any, authed bool) (int, []byte) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("Marshal(%v): %v", body, err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("NewRequest(%s %s): %v", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authed {
		req.Header.Set("Authorization", "Bearer "+ts.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(%s %s): %v", method, url, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(%s %s): %v", method, url, err)
	}
	return resp.StatusCode, out
}

func mcpAuthURL(fx *mcpAuthEngineFixture) string { return fx.f.ts.server.URL }

func mcpAuthStartFlow(t *testing.T, fx *mcpAuthEngineFixture, agentID, name string) (int, MCPAuthStart, []byte) {
	t.Helper()
	status, body := mcpAuthDo(t, fx.f.ts, http.MethodPost,
		fmt.Sprintf("%s/api/v1/mcp/servers/%s/auth?agent_id=%s", mcpAuthURL(fx), name, agentID), nil, true)
	var out MCPAuthStart
	if status == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode MCPAuthStart: %v (%s)", err, body)
		}
	}
	return status, out, body
}

func mcpAuthPoll(t *testing.T, fx *mcpAuthEngineFixture, agentID, flowID string) (int, MCPAuthStatus, []byte) {
	t.Helper()
	status, body := mcpAuthDo(t, fx.f.ts, http.MethodGet,
		fmt.Sprintf("%s/api/v1/mcp/auth/%s?agent_id=%s", mcpAuthURL(fx), flowID, agentID), nil, true)
	var out MCPAuthStatus
	if status == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode MCPAuthStatus: %v (%s)", err, body)
		}
	}
	return status, out, body
}

func mcpAuthComplete(t *testing.T, fx *mcpAuthEngineFixture, agentID, flowID, code, state string) (int, MCPAuthStatus, []byte) {
	t.Helper()
	status, body := mcpAuthDo(t, fx.f.ts, http.MethodPost,
		fmt.Sprintf("%s/api/v1/mcp/auth/%s/complete?agent_id=%s", mcpAuthURL(fx), flowID, agentID),
		MCPAuthCompleteRequest{Code: code, State: state}, true)
	var out MCPAuthStatus
	if status == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode MCPAuthStatus: %v (%s)", err, body)
		}
	}
	return status, out, body
}

func mcpAuthCancel(t *testing.T, fx *mcpAuthEngineFixture, agentID, flowID string) (int, []byte) {
	t.Helper()
	return mcpAuthDo(t, fx.f.ts, http.MethodDelete,
		fmt.Sprintf("%s/api/v1/mcp/auth/%s?agent_id=%s", mcpAuthURL(fx), flowID, agentID), nil, true)
}

func mcpAuthLogout(t *testing.T, fx *mcpAuthEngineFixture, agentID, name string) (int, []byte) {
	t.Helper()
	return mcpAuthDo(t, fx.f.ts, http.MethodDelete,
		fmt.Sprintf("%s/api/v1/mcp/servers/%s/auth?agent_id=%s", mcpAuthURL(fx), name, agentID), nil, true)
}

func mcpAuthReloads(fx *mcpAuthEngineFixture) *int64 {
	var calls int64
	fx.f.ts.channel.SetReloadMCP(func() { atomic.AddInt64(&calls, 1) })
	return &calls
}

func mcpAuthBodyCode(t *testing.T, body []byte) string {
	t.Helper()
	var e APIError
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("error body is not an APIError: %v (%s)", err, body)
	}
	return e.Code
}

func mcpAuthStoredSecrets(t *testing.T, f *mcpFixture, name string) []string {
	t.Helper()
	rec, ok := mcp.NewAuthStore(f.paths.LeleDir).Record(name)
	if !ok {
		return nil
	}
	var out []string
	if rec.ClientSecret != "" {
		out = append(out, rec.ClientSecret)
	}
	if rec.Token != nil {
		for _, v := range []string{rec.Token.AccessToken, rec.Token.RefreshToken} {
			if v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

func mcpAuthAssertNoSecrets(t *testing.T, what string, body []byte, secrets []string) {
	t.Helper()
	for _, key := range []string{"access_token", "refresh_token", "id_token", "client_secret", "code_verifier", "verifier"} {
		if bytes.Contains(body, []byte(key)) {
			t.Fatalf("%s leaked the key %q: %s", what, key, body)
		}
	}
	for _, s := range secrets {
		if bytes.Contains(body, []byte(s)) {
			t.Fatalf("%s leaked the stored value %q: %s", what, s, body)
		}
	}
}

func TestMCPAuthRoutes_RequireBearerToken(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	base := mcpAuthURL(fx)
	flowID := strings.Repeat("a", mcpAuthFlowIDLen)

	requests := []struct {
		method string
		url    string
	}{
		{http.MethodPost, base + "/api/v1/mcp/servers/" + mcpAuthRemoteName + "/auth?agent_id=agent1"},
		{http.MethodGet, base + "/api/v1/mcp/auth/" + flowID + "?agent_id=agent1"},
		{http.MethodPost, base + "/api/v1/mcp/auth/" + flowID + "/complete?agent_id=agent1"},
		{http.MethodDelete, base + "/api/v1/mcp/auth/" + flowID + "?agent_id=agent1"},
		{http.MethodDelete, base + "/api/v1/mcp/servers/" + mcpAuthRemoteName + "/auth?agent_id=agent1"},
	}
	for _, r := range requests {
		status, body := mcpAuthDo(t, fx.f.ts, r.method, r.url, nil, false)
		if status != http.StatusUnauthorized {
			t.Fatalf("%s %s without a token: status = %d, want 401 (404 would mean the route is not registered); body=%s",
				r.method, r.url, status, body)
		}
	}
}

func TestMCPAuthStart_ReturnsAWorkingURLAndNoSecret(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	before := time.Now()

	status, start, body := mcpAuthStartFlow(t, fx, "agent1", mcpAuthRemoteName)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if !isValidMCPFlowID(start.FlowID) {
		t.Fatalf("flow_id = %q, want %d lowercase hex chars", start.FlowID, mcpAuthFlowIDLen)
	}

	authURL, err := url.Parse(start.AuthURL)
	if err != nil {
		t.Fatalf("auth_url %q is not a URL: %v", start.AuthURL, err)
	}
	if authURL.Host != strings.TrimPrefix(fx.asrv.URL, "http://") {
		t.Fatalf("auth_url host = %q, want the authorization server %q", authURL.Host, fx.asrv.URL)
	}
	q := authURL.Query()
	if q.Get("client_id") == "" || q.Get("state") == "" || q.Get("code_challenge") == "" {
		t.Fatalf("auth_url is not a PKCE authorization request: %s", start.AuthURL)
	}

	redirect, err := url.Parse(start.RedirectURI)
	if err != nil || redirect.Hostname() != "127.0.0.1" {
		t.Fatalf("redirect_uri = %q (err %v), want a 127.0.0.1 loopback URI", start.RedirectURI, err)
	}
	assertPortOpen(t, portOf(t, start.RedirectURI))

	if d := start.ExpiresAt.Sub(before); d < 4*time.Minute || d > 6*time.Minute {
		t.Fatalf("expires_at is %v away, want ~5m", d)
	}

	mcpAuthAssertNoSecrets(t, "start", body, mcpAuthStoredSecrets(t, fx.f, mcpAuthRemoteName))
}

func TestMCPAuthStart_SecondStartReplacesThePairsFlow(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")

	status, first, body := mcpAuthStartFlow(t, fx, "agent1", mcpAuthRemoteName)
	if status != http.StatusOK {
		t.Fatalf("first start status = %d, want 200; body=%s", status, body)
	}
	firstPort := portOf(t, first.RedirectURI)

	status, second, body := mcpAuthStartFlow(t, fx, "agent1", mcpAuthRemoteName)
	if status != http.StatusOK {
		t.Fatalf("second start status = %d, want 200; body=%s", status, body)
	}
	if second.FlowID == first.FlowID {
		t.Fatalf("second start reused flow id %q, want a fresh one", first.FlowID)
	}
	if second.RedirectURI != first.RedirectURI {
		t.Fatalf("second start bound %q, want the pinned %q (the release would otherwise not be exercised)",
			second.RedirectURI, first.RedirectURI)
	}
	assertPortOpen(t, firstPort)

	if status, _, body := mcpAuthPoll(t, fx, "agent1", first.FlowID); status != http.StatusNotFound {
		t.Fatalf("poll of the replaced flow: status = %d, want 404; body=%s", status, body)
	}
	status, poll, body := mcpAuthPoll(t, fx, "agent1", second.FlowID)
	if status != http.StatusOK || poll.State != "pending" {
		t.Fatalf("poll of the replacing flow = (%d, %+v), want (200, pending); body=%s", status, poll, body)
	}

	if status, body := mcpAuthCancel(t, fx, "agent1", second.FlowID); status != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204; body=%s", status, body)
	}
	assertPortClosed(t, firstPort)
}

func TestMCPAuthStart_ErrorMatrix(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")

	tests := []struct {
		name     string
		agentID  string
		server   string
		want     int
		wantCode string
	}{
		{"unknown server", "agent1", "no-such-server", http.StatusNotFound, "mcp_server_not_found"},
		{"known but unresolved server", "agent1", mcpAuthBrokenName, http.StatusNotFound, "mcp_server_not_found"},
		{"stdio server", "agent1", mcpAuthStdioName, http.StatusNotImplemented, "mcp_auth_not_supported"},
		{"missing agent_id", "", mcpAuthRemoteName, http.StatusBadRequest, "agent_id_missing"},
		{"unknown agent", "ghost", mcpAuthRemoteName, http.StatusNotFound, "agent_not_found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, body := mcpAuthDo(t, fx.f.ts, http.MethodPost,
				fmt.Sprintf("%s/api/v1/mcp/servers/%s/auth?agent_id=%s", mcpAuthURL(fx), tc.server, tc.agentID), nil, true)
			if status != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", status, tc.want, body)
			}
			if code := mcpAuthBodyCode(t, body); code != tc.wantCode {
				t.Fatalf("code = %q, want %q; body=%s", code, tc.wantCode, body)
			}
		})
	}
}

func TestMCPAuthPoll_PendingThenDoneFiresTheReloadOnce(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	reloads := mcpAuthReloads(fx)

	status, start, body := mcpAuthStartFlow(t, fx, "agent1", mcpAuthRemoteName)
	if status != http.StatusOK {
		t.Fatalf("start status = %d, want 200; body=%s", status, body)
	}
	secrets := mcpAuthStoredSecrets(t, fx.f, mcpAuthRemoteName)

	status, poll, body := mcpAuthPoll(t, fx, "agent1", start.FlowID)
	if status != http.StatusOK {
		t.Fatalf("poll status = %d, want 200; body=%s", status, body)
	}
	if poll.FlowID != start.FlowID || poll.Server != mcpAuthRemoteName {
		t.Fatalf("poll = %+v, want flow_id %q server %q", poll, start.FlowID, mcpAuthRemoteName)
	}
	if poll.State != "pending" || poll.Authenticated || poll.Error != "" {
		t.Fatalf("poll = %+v, want pending/unauthenticated/no error", poll)
	}
	if got := atomic.LoadInt64(reloads); got != 0 {
		t.Fatalf("reload fired %d times before completion, want 0", got)
	}

	code, state := mcpAuthAuthorizeCode(t, start.AuthURL)

	status, done, body := mcpAuthComplete(t, fx, "agent1", start.FlowID, code, state)
	if status != http.StatusOK {
		t.Fatalf("complete status = %d, want 200; body=%s", status, body)
	}
	if done.State != "done" || !done.Authenticated || done.Error != "" {
		t.Fatalf("complete = %+v, want done/authenticated/no error", done)
	}
	if got := atomic.LoadInt64(reloads); got != 1 {
		t.Fatalf("reload fired %d times after completion, want exactly 1", got)
	}
	mcpAuthAssertNoSecrets(t, "complete", body, secrets)

	for i := 0; i < 3; i++ {
		status, poll, body := mcpAuthPoll(t, fx, "agent1", start.FlowID)
		if status != http.StatusOK || poll.State != "done" || !poll.Authenticated {
			t.Fatalf("poll %d = (%d, %+v), want (200, done/authenticated); body=%s", i, status, poll, body)
		}
		mcpAuthAssertNoSecrets(t, "poll", body, secrets)
	}
	if got := atomic.LoadInt64(reloads); got != 1 {
		t.Fatalf("reload fired %d times over repeated polls, want 1", got)
	}

	invStatus, inv, invBody := mcpInventory(t, fx.f, "agent1")
	if invStatus != http.StatusOK {
		t.Fatalf("inventory status = %d, want 200; body=%s", invStatus, invBody)
	}
	auth := mcpRow(t, inv, mcpAuthRemoteName).Server.Auth
	if auth == nil || !auth.Authenticated || !auth.Required || auth.Method != "oauth" {
		t.Fatalf("inventory auth after the login = %+v, want required/authenticated/oauth", auth)
	}
}

func TestMCPAuthComplete_FailedExchangeIsRetryableAndDoesNotReload(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	reloads := mcpAuthReloads(fx)

	status, start, body := mcpAuthStartFlow(t, fx, "agent1", mcpAuthRemoteName)
	if status != http.StatusOK {
		t.Fatalf("start status = %d, want 200; body=%s", status, body)
	}

	status, body = mcpAuthCompleteRaw(t, fx, "agent1", start.FlowID, MCPAuthCompleteRequest{Code: "not-a-real-code"})
	if status != http.StatusBadGateway {
		t.Fatalf("complete with a bogus code: status = %d, want 502; body=%s", status, body)
	}
	if code := mcpAuthBodyCode(t, body); code != "mcp_auth_failed" {
		t.Fatalf("code = %q, want mcp_auth_failed; body=%s", code, body)
	}

	status, poll, body := mcpAuthPoll(t, fx, "agent1", start.FlowID)
	if status != http.StatusOK {
		t.Fatalf("poll after the failure: status = %d, want 200 (a failed exchange is not terminal); body=%s", status, body)
	}
	if poll.State != "error" || poll.Error == "" || poll.Authenticated {
		t.Fatalf("poll after the failure = %+v, want error with a message and authenticated=false", poll)
	}
	if got := atomic.LoadInt64(reloads); got != 0 {
		t.Fatalf("reload fired %d times for a failed exchange, want 0", got)
	}

	code, state := mcpAuthAuthorizeCode(t, start.AuthURL)
	status, done, body := mcpAuthComplete(t, fx, "agent1", start.FlowID, code, state)
	if status != http.StatusOK || done.State != "done" {
		t.Fatalf("retry = (%d, %+v), want (200, done); body=%s", status, done, body)
	}
	if got := atomic.LoadInt64(reloads); got != 1 {
		t.Fatalf("reload fired %d times after the retry, want 1", got)
	}
}

func mcpAuthCompleteRaw(t *testing.T, fx *mcpAuthEngineFixture, agentID, flowID string, req MCPAuthCompleteRequest) (int, []byte) {
	t.Helper()
	return mcpAuthDo(t, fx.f.ts, http.MethodPost,
		fmt.Sprintf("%s/api/v1/mcp/auth/%s/complete?agent_id=%s", mcpAuthURL(fx), flowID, agentID), req, true)
}

func TestMCPAuthComplete_RejectsBlankCodeAndBadBody(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")

	status, start, body := mcpAuthStartFlow(t, fx, "agent1", mcpAuthRemoteName)
	if status != http.StatusOK {
		t.Fatalf("start status = %d, want 200; body=%s", status, body)
	}

	status, body = mcpAuthCompleteRaw(t, fx, "agent1", start.FlowID, MCPAuthCompleteRequest{})
	if status != http.StatusBadRequest {
		t.Fatalf("blank code: status = %d, want 400; body=%s", status, body)
	}
	if code := mcpAuthBodyCode(t, body); code != "invalid_request" {
		t.Fatalf("blank code: code = %q, want invalid_request; body=%s", code, body)
	}

	status, body = mcpAuthDo(t, fx.f.ts, http.MethodPost,
		fmt.Sprintf("%s/api/v1/mcp/auth/%s/complete?agent_id=agent1", mcpAuthURL(fx), start.FlowID),
		"not-json", true)
	if status != http.StatusBadRequest {
		t.Fatalf("junk body: status = %d, want 400; body=%s", status, body)
	}
	if code := mcpAuthBodyCode(t, body); code != "body_invalid" {
		t.Fatalf("junk body: code = %q, want body_invalid; body=%s", code, body)
	}

	if status, poll, body := mcpAuthPoll(t, fx, "agent1", start.FlowID); status != http.StatusOK || poll.State != "pending" {
		t.Fatalf("poll after the refused completes = (%d, %+v), want (200, pending); body=%s", status, poll, body)
	}
}

func TestMCPAuthStatus_RejectsUnknownMalformedAndForeignIDs(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1", "agent2")

	status, start, body := mcpAuthStartFlow(t, fx, "agent1", mcpAuthRemoteName)
	if status != http.StatusOK {
		t.Fatalf("start status = %d, want 200; body=%s", status, body)
	}

	if status, poll, body := mcpAuthPoll(t, fx, "agent1", start.FlowID); status != http.StatusOK || poll.State != "pending" {
		t.Fatalf("owner poll = (%d, %+v), want (200, pending); body=%s", status, poll, body)
	}
	for _, route := range []struct {
		name string
		call func(agentID, flowID string) (int, []byte)
	}{
		{"poll", func(a, id string) (int, []byte) { st, _, b := mcpAuthPoll(t, fx, a, id); return st, b }},
		{"cancel", func(a, id string) (int, []byte) { return mcpAuthCancel(t, fx, a, id) }},
	} {
		status, body := route.call("agent2", start.FlowID)
		if status != http.StatusNotFound {
			t.Fatalf("%s as another agent: status = %d, want 404; body=%s", route.name, status, body)
		}
		if code := mcpAuthBodyCode(t, body); code != "mcp_auth_flow_not_found" {
			t.Fatalf("%s as another agent: code = %q, want mcp_auth_flow_not_found; body=%s", route.name, code, body)
		}
	}
	if status, poll, body := mcpAuthPoll(t, fx, "agent1", start.FlowID); status != http.StatusOK || poll.State != "pending" {
		t.Fatalf("owner poll after the foreign attempts = (%d, %+v), want (200, pending); body=%s", status, poll, body)
	}

	tests := []struct {
		name     string
		flowID   string
		want     int
		wantCode string
	}{
		{"unknown id", strings.Repeat("f", mcpAuthFlowIDLen), http.StatusNotFound, "mcp_auth_flow_not_found"},
		{"too short", "abc", http.StatusBadRequest, "invalid_request"},
		{"uppercase hex", strings.ToUpper(strings.Repeat("a", mcpAuthFlowIDLen)), http.StatusBadRequest, "invalid_request"},
		{"non-hex", strings.Repeat("z", mcpAuthFlowIDLen), http.StatusBadRequest, "invalid_request"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, body := mcpAuthDo(t, fx.f.ts, http.MethodGet,
				fmt.Sprintf("%s/api/v1/mcp/auth/%s?agent_id=agent1", mcpAuthURL(fx), tc.flowID), nil, true)
			if status != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", status, tc.want, body)
			}
			if code := mcpAuthBodyCode(t, body); code != tc.wantCode {
				t.Fatalf("code = %q, want %q; body=%s", code, tc.wantCode, body)
			}
		})
	}
}

func TestMCPAuthCancel_IsIdempotentAndFreesTheListener(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	reloads := mcpAuthReloads(fx)

	status, start, body := mcpAuthStartFlow(t, fx, "agent1", mcpAuthRemoteName)
	if status != http.StatusOK {
		t.Fatalf("start status = %d, want 200; body=%s", status, body)
	}
	port := portOf(t, start.RedirectURI)

	status, body = mcpAuthCancel(t, fx, "agent1", start.FlowID)
	if status != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204; body=%s", status, body)
	}
	if len(body) != 0 {
		t.Fatalf("cancel body = %q, want empty (204 carries no body)", body)
	}
	assertPortClosed(t, port)

	status, poll, body := mcpAuthPoll(t, fx, "agent1", start.FlowID)
	if status != http.StatusOK {
		t.Fatalf("poll after cancel: status = %d, want 200 (the flow stays resolvable); body=%s", status, body)
	}
	if poll.State != "error" || poll.Error != "canceled" || poll.Authenticated {
		t.Fatalf("poll after cancel = %+v, want error/canceled/unauthenticated", poll)
	}
	if got := atomic.LoadInt64(reloads); got != 0 {
		t.Fatalf("reload fired %d times for a cancelled flow, want 0", got)
	}

	if status, body := mcpAuthCancel(t, fx, "agent1", start.FlowID); status != http.StatusNoContent {
		t.Fatalf("second cancel status = %d, want 204; body=%s", status, body)
	}
	unknown := strings.Repeat("f", mcpAuthFlowIDLen)
	status, body = mcpAuthCancel(t, fx, "agent1", unknown)
	if status != http.StatusNotFound {
		t.Fatalf("cancel of an unknown id: status = %d, want 404; body=%s", status, body)
	}
	if code := mcpAuthBodyCode(t, body); code != "mcp_auth_flow_not_found" {
		t.Fatalf("cancel of an unknown id: code = %q, want mcp_auth_flow_not_found; body=%s", code, body)
	}
}

func TestMCPAuthLogout_ForgetsTheRecordAndCancelsTheFlow(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	reloads := mcpAuthReloads(fx)

	writer := mcp.NewAuthStore(fx.f.paths.LeleDir)
	if err := writer.Save(mcpAuthRemoteName, mcp.AuthRecord{
		URL:          fx.resource,
		ClientID:     "stored-client-id",
		ClientSecret: "stored-client-secret",
		AuthRequired: true,
		Token: &transport.Token{
			AccessToken:  "stored-access-token",
			RefreshToken: "stored-refresh-token",
			TokenType:    "Bearer",
			ExpiresAt:    time.Now().Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("Save(): %v", err)
	}
	if status, inv, body := mcpInventory(t, fx.f, "agent1"); status != http.StatusOK {
		t.Fatalf("inventory status = %d, want 200; body=%s", status, body)
	} else if auth := mcpRow(t, inv, mcpAuthRemoteName).Server.Auth; auth == nil || !auth.Authenticated {
		t.Fatalf("inventory before the logout = %+v, want authenticated (the fixture did not seed a usable token)", auth)
	}

	status, start, body := mcpAuthStartFlow(t, fx, "agent1", mcpAuthRemoteName)
	if status != http.StatusOK {
		t.Fatalf("start status = %d, want 200; body=%s", status, body)
	}

	status, body = mcpAuthLogout(t, fx, "agent1", mcpAuthRemoteName)
	if status != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204; body=%s", status, body)
	}
	if len(body) != 0 {
		t.Fatalf("logout body = %q, want empty", body)
	}
	if _, ok := writer.Record(mcpAuthRemoteName); ok {
		t.Fatal("the record survived the logout, want the token AND the registration forgotten")
	}
	if got := atomic.LoadInt64(reloads); got != 1 {
		t.Fatalf("reload fired %d times for one successful logout, want 1", got)
	}

	if status, poll, body := mcpAuthPoll(t, fx, "agent1", start.FlowID); status != http.StatusOK {
		t.Fatalf("poll after logout: status = %d, want 200; body=%s", status, body)
	} else if poll.State != "error" || poll.Authenticated {
		t.Fatalf("poll after logout = %+v, want the flow cancelled", poll)
	}

	if status, body := mcpAuthLogout(t, fx, "agent1", mcpAuthRemoteName); status != http.StatusNoContent {
		t.Fatalf("second logout status = %d, want 204; body=%s", status, body)
	}
	if got := atomic.LoadInt64(reloads); got != 2 {
		t.Fatalf("reload fired %d times after two successful logouts, want 2", got)
	}

	status, body = mcpAuthLogout(t, fx, "agent1", "no-such-server")
	if status != http.StatusNotFound {
		t.Fatalf("logout of an unknown server: status = %d, want 404; body=%s", status, body)
	}
	if code := mcpAuthBodyCode(t, body); code != "mcp_server_not_found" {
		t.Fatalf("logout of an unknown server: code = %q, want mcp_server_not_found; body=%s", code, body)
	}
	if got := atomic.LoadInt64(reloads); got != 2 {
		t.Fatalf("reload fired %d times after a refused logout, want 2", got)
	}
}

func TestMCPAuthLogout_ClearsARecordForANameThatIsNotInAnyLayer(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")

	writer := mcp.NewAuthStore(fx.f.paths.LeleDir)
	if err := writer.Save("gone-from-config", mcp.AuthRecord{URL: "https://old.example/mcp"}); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	status, body := mcpAuthLogout(t, fx, "agent1", "gone-from-config")
	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (the name is gone from every layer but a record exists); body=%s", status, body)
	}
	if _, ok := writer.Record("gone-from-config"); ok {
		t.Fatal("the orphaned record survived the logout")
	}
}

func TestMCPAuthErrorCode_PinsTheContractErrorTable(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		want      int
		code      string
		forbidden string
	}{
		{name: "nil error", err: nil, want: http.StatusBadGateway, code: "mcp_auth_failed"},
		{name: "unclassified engine failure", err: errors.New("dial tcp 127.0.0.1:9: connect: connection refused"), want: http.StatusBadGateway, code: "mcp_auth_failed"},

		{name: "ErrAuthNotSupported", err: mcp.ErrAuthNotSupported, want: http.StatusNotImplemented, code: "mcp_auth_not_supported"},
		{name: "wrapped ErrAuthNotSupported", err: fmt.Errorf("mcp auth %q: register: %w", "srv", mcp.ErrAuthNotSupported), want: http.StatusNotImplemented, code: "mcp_auth_not_supported"},

		{name: "ErrAuthStoreDisabled", err: mcp.ErrAuthStoreDisabled, want: http.StatusServiceUnavailable, code: "mcp_auth_disabled"},
		{name: "wrapped ErrAuthStoreDisabled", err: fmt.Errorf("failed to start authorization: %w", mcp.ErrAuthStoreDisabled), want: http.StatusServiceUnavailable, code: "mcp_auth_disabled"},

		{name: "ErrAuthNotRequired", err: mcp.ErrAuthNotRequired, want: http.StatusConflict, code: "mcp_auth_not_required"},
		{name: "wrapped ErrAuthNotRequired", err: fmt.Errorf("mcp auth probe: %w", mcp.ErrAuthNotRequired), want: http.StatusConflict, code: "mcp_auth_not_required"},

		{name: "errMCPAuthFlowNotFound", err: errMCPAuthFlowNotFound, want: http.StatusNotFound, code: "mcp_auth_flow_not_found"},
		{name: "wrapped errMCPAuthFlowNotFound", err: fmt.Errorf("lookup %q: %w", "abc", errMCPAuthFlowNotFound), want: http.StatusNotFound, code: "mcp_auth_flow_not_found"},

		{name: "ErrAuthProbeFailed", err: mcp.ErrAuthProbeFailed, want: http.StatusBadGateway, code: "mcp_auth_failed", forbidden: "mcp_auth_not_required"},
		{name: "doubly wrapped ErrAuthProbeFailed", err: fmt.Errorf("outer: %w", fmt.Errorf("mcp auth %q: probe: %w", "srv", mcp.ErrAuthProbeFailed)), want: http.StatusBadGateway, code: "mcp_auth_failed", forbidden: "mcp_auth_not_required"},

		{name: "ErrAuthCanceled", err: mcp.ErrAuthCanceled, want: http.StatusBadGateway, code: "mcp_auth_failed"},
		{name: "ErrAuthTimedOut", err: mcp.ErrAuthTimedOut, want: http.StatusBadGateway, code: "mcp_auth_failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, code := mcpAuthErrorCode(tc.err)
			if status != tc.want || code != tc.code {
				t.Fatalf("mcpAuthErrorCode(%v) = (%d, %q), want (%d, %q)", tc.err, status, code, tc.want, tc.code)
			}
			if tc.forbidden != "" && code == tc.forbidden {
				t.Fatalf("mcpAuthErrorCode(%v) answered %q: a failed probe is not a server that needs no credentials", tc.err, code)
			}
		})
	}
}

func TestMCPAuthStart_EngineFailuresReachTheContractCodes(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")

	restore := startMCPAuthFlow
	t.Cleanup(func() { startMCPAuthFlow = restore })

	tests := []struct {
		name     string
		fail     error
		want     int
		wantCode string
	}{
		{"store disabled", mcp.ErrAuthStoreDisabled, http.StatusServiceUnavailable, "mcp_auth_disabled"},
		{"not required", mcp.ErrAuthNotRequired, http.StatusConflict, "mcp_auth_not_required"},
		{"no registration endpoint", mcp.ErrAuthNotSupported, http.StatusNotImplemented, "mcp_auth_not_supported"},
		{"probe failed", mcp.ErrAuthProbeFailed, http.StatusBadGateway, "mcp_auth_failed"},
		{"unclassified transport failure", errors.New("dial tcp: connect: connection refused"), http.StatusBadGateway, "mcp_auth_failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			startMCPAuthFlow = func(context.Context, string, mcp.ServerConfig, *mcp.AuthStore, mcp.AuthOptions) (*mcp.AuthFlow, error) {
				return nil, fmt.Errorf("engine: %w", tc.fail)
			}

			status, body := mcpAuthDo(t, fx.f.ts, http.MethodPost,
				fmt.Sprintf("%s/api/v1/mcp/servers/%s/auth?agent_id=agent1", mcpAuthURL(fx), mcpAuthRemoteName), nil, true)
			if status != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", status, tc.want, body)
			}
			if code := mcpAuthBodyCode(t, body); code != tc.wantCode {
				t.Fatalf("code = %q, want %q; body=%s", code, tc.wantCode, body)
			}
			if tc.fail == mcp.ErrAuthProbeFailed && strings.Contains(string(body), "mcp_auth_not_required") {
				t.Fatalf("a failed probe was reported as not-required: %s", body)
			}
			if got := fx.f.ts.channel.mcpAuthFlowRegistry().entryCount(); got != 0 {
				t.Fatalf("registry holds %d live flows after a refused start, want 0", got)
			}
		})
	}
}

func mcpAuthAddRootlessAgent(t *testing.T, fx *mcpAuthEngineFixture, agentID string) {
	t.Helper()
	agentDir := t.TempDir()
	writeMCPJSON(t, filepath.Join(agentDir, "mcp.json"), fx.f.globalRaw)
	fx.f.loop.paths[agentID] = mcp.Paths{
		LeleDir:        "",
		AgentWorkspace: agentDir,
		Cwd:            t.TempDir(),
	}
}

func TestMCPAuthStart_StoreDisabledIs503(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1", "rootless")
	mcpAuthAddRootlessAgent(t, fx, "rootless")

	if status, start, body := mcpAuthStartFlow(t, fx, "agent1", mcpAuthRemoteName); status != http.StatusOK || start.FlowID == "" {
		t.Fatalf("control start for the rooted agent = (%d, %+v); body=%s", status, start, body)
	}

	status, body := mcpAuthDo(t, fx.f.ts, http.MethodPost,
		fmt.Sprintf("%s/api/v1/mcp/servers/%s/auth?agent_id=rootless", mcpAuthURL(fx), mcpAuthRemoteName), nil, true)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", status, body)
	}
	if code := mcpAuthBodyCode(t, body); code != "mcp_auth_disabled" {
		t.Fatalf("code = %q, want mcp_auth_disabled; body=%s", code, body)
	}
}

func TestMCPAuthRoutes_RefuseEscapedServerNames(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")

	for _, escaped := range []string{"a%2Fb", "a%2fb", "..%2F..%2Fetc%2Fpasswd", "%2Fetc%2Fpasswd"} {
		for _, route := range []struct {
			name   string
			method string
		}{
			{"start", http.MethodPost},
			{"logout", http.MethodDelete},
		} {
			t.Run(route.name+" "+escaped, func(t *testing.T) {
				status, body := mcpAuthDo(t, fx.f.ts, route.method,
					fmt.Sprintf("%s/api/v1/mcp/servers/%s/auth?agent_id=agent1", mcpAuthURL(fx), escaped), nil, true)
				if status != http.StatusBadRequest {
					t.Fatalf("%s %s: status = %d, want 400; body=%s", route.method, escaped, status, body)
				}
				if code := mcpAuthBodyCode(t, body); code != "invalid_request" {
					t.Fatalf("%s %s: code = %q, want invalid_request; body=%s", route.method, escaped, code, body)
				}
				if strings.Contains(string(body), "a/b") || strings.Contains(string(body), "passwd") {
					t.Fatalf("%s %s echoed the refused name: %s", route.method, escaped, body)
				}
				if got := fx.f.ts.channel.mcpAuthFlowRegistry().entryCount(); got != 0 {
					t.Fatalf("%s %s left %d live flows in the registry, want 0", route.method, escaped, got)
				}
			})
		}
	}

	status, body := mcpAuthDo(t, fx.f.ts, http.MethodDelete,
		fmt.Sprintf("%s/api/v1/mcp/servers/%s/auth?agent_id=agent1", mcpAuthURL(fx), mcpAuthRemoteName), nil, true)
	if status != http.StatusNoContent {
		t.Fatalf("logout of %q: status = %d, want 204; body=%s", mcpAuthRemoteName, status, body)
	}
}

func TestMCPAuthCompleteAndCancel_RejectMalformedFlowIDsEarly(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")

	badIDs := []string{
		"abc",
		strings.Repeat("a", mcpAuthFlowIDLen+1),
		strings.ToUpper(strings.Repeat("a", mcpAuthFlowIDLen)),
		strings.Repeat("z", mcpAuthFlowIDLen),
	}
	for _, id := range badIDs {
		t.Run("complete "+id, func(t *testing.T) {
			status, body := mcpAuthDo(t, fx.f.ts, http.MethodPost,
				fmt.Sprintf("%s/api/v1/mcp/auth/%s/complete?agent_id=agent1", mcpAuthURL(fx), id), "not-json", true)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (a malformed id is not a missing flow); body=%s", status, body)
			}
			if code := mcpAuthBodyCode(t, body); code != "invalid_request" {
				t.Fatalf("code = %q, want invalid_request (body_invalid would mean the body is decoded before the id); body=%s", code, body)
			}
		})
		t.Run("cancel "+id, func(t *testing.T) {
			status, body := mcpAuthCancel(t, fx, "agent1", id)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (a malformed id is not a missing flow); body=%s", status, body)
			}
			if code := mcpAuthBodyCode(t, body); code != "invalid_request" {
				t.Fatalf("code = %q, want invalid_request; body=%s", code, body)
			}
		})
	}

	unknown := strings.Repeat("f", mcpAuthFlowIDLen)
	status, _, body := mcpAuthComplete(t, fx, "agent1", unknown, "a-code", "a-state")
	if status != http.StatusNotFound {
		t.Fatalf("complete of a well-formed unknown id: status = %d, want 404; body=%s", status, body)
	}
	if code := mcpAuthBodyCode(t, body); code != "mcp_auth_flow_not_found" {
		t.Fatalf("complete of a well-formed unknown id: code = %q, want mcp_auth_flow_not_found; body=%s", code, body)
	}
	if status, body := mcpAuthCancel(t, fx, "agent1", unknown); status != http.StatusNotFound {
		t.Fatalf("cancel of a well-formed unknown id: status = %d, want 404; body=%s", status, body)
	}

	for _, route := range []struct {
		name string
		call func(agentID, flowID string) (int, []byte)
	}{
		{"complete", func(a, id string) (int, []byte) {
			return mcpAuthCompleteRaw(t, fx, a, id, MCPAuthCompleteRequest{Code: "a-code", State: "a-state"})
		}},
		{"cancel", func(a, id string) (int, []byte) { return mcpAuthCancel(t, fx, a, id) }},
	} {
		status, body := route.call("ghost", "abc")
		if status != http.StatusNotFound {
			t.Fatalf("%s as an unknown agent: status = %d, want 404 agent_not_found; body=%s", route.name, status, body)
		}
		if code := mcpAuthBodyCode(t, body); code != "agent_not_found" {
			t.Fatalf("%s as an unknown agent: code = %q, want agent_not_found; body=%s", route.name, code, body)
		}
	}
}
