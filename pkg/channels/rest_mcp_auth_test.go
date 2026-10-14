// HTTP-level tests for the five MCP OAuth routes (rest_mcp.go, registry in
// mcp_auth_flows.go): start, poll, complete, cancel and logout.
//
// These tests drive the REAL stack — ServeMux + withAuth + the real engine
// (pkg/mcp/auth.go against the in-process authorization server in
// pkg/mcp/authtest) — so they are the ones that pin what an HTTP client
// actually sees:
//
//   - the routes are REGISTERED and behind withAuth (a 404 would mean missing,
//     a 401 means guarded);
//   - the start response carries a WORKING auth_url (its loopback listener is
//     really bound) and no credential;
//   - poll/complete report pending → done, and the reload seam fires exactly
//     once, on that transition only;
//   - a failed exchange stays retryable ("error", flow still resolvable);
//   - cancel is best-effort-idempotent and releases the listener;
//   - logout forgets the record, cancels the pair's live flow and reloads;
//   - every error cell of the contract's matrix is answered with its code.
//
// mcp_auth_flows_test.go covers the registry in isolation; this file covers the
// contract an HTTP client holds.

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

// --- HTTP helpers -----------------------------------------------------------

// mcpAuthDo issues one request against the fixture server. body == nil sends
// none; authed=false omits the Authorization header.
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

// mcpAuthURL is the URL of the fixture's HTTP server, i.e. the prefix every
// route below is built on.
func mcpAuthURL(fx *mcpAuthEngineFixture) string { return fx.f.ts.server.URL }

// mcpAuthStartFlow performs contract route 2 and decodes its DTO.
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

// mcpAuthPoll performs contract route 3 and decodes its DTO.
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

// mcpAuthComplete performs contract route 4 and decodes its DTO.
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

// mcpAuthCancel performs contract route 5.
func mcpAuthCancel(t *testing.T, fx *mcpAuthEngineFixture, agentID, flowID string) (int, []byte) {
	t.Helper()
	return mcpAuthDo(t, fx.f.ts, http.MethodDelete,
		fmt.Sprintf("%s/api/v1/mcp/auth/%s?agent_id=%s", mcpAuthURL(fx), flowID, agentID), nil, true)
}

// mcpAuthLogout performs contract route 6.
func mcpAuthLogout(t *testing.T, fx *mcpAuthEngineFixture, agentID, name string) (int, []byte) {
	t.Helper()
	return mcpAuthDo(t, fx.f.ts, http.MethodDelete,
		fmt.Sprintf("%s/api/v1/mcp/servers/%s/auth?agent_id=%s", mcpAuthURL(fx), name, agentID), nil, true)
}

// mcpAuthReloads installs a counting reload seam and returns the counter. The
// reload fires on the pending → done transition and after a successful logout,
// never on start/cancel/error — the counter is how the tests see the
// difference (invariant 2).
func mcpAuthReloads(fx *mcpAuthEngineFixture) *int64 {
	var calls int64
	fx.f.ts.channel.SetReloadMCP(func() { atomic.AddInt64(&calls, 1) })
	return &calls
}

// mcpAuthBodyCode decodes the `code` of an APIError body.
func mcpAuthBodyCode(t *testing.T, body []byte) string {
	t.Helper()
	var e APIError
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("error body is not an APIError: %v (%s)", err, body)
	}
	return e.Code
}

// mcpAuthStoredSecrets returns the secret VALUES the auth store holds for name,
// so a test can prove they appear in no response body. The canary is the
// cheapest way to notice a regression that starts echoing what it read.
//
// The client ID is deliberately NOT included: it is a public identifier that
// belongs in the authorization URL the user is sent to (the REST DTOs still
// never carry it outside that URL, which is what mcp_auth_flows.go's
// no-secret rule is about). The client SECRET and every token value are, and
// they are what this returns.
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

// mcpAuthAssertNoSecrets fails when body carries a credential-shaped key or one
// of the values the store holds. `client_id` as a KEY NAME is allowed inside the
// authorization URL (a public client id belongs there, and the user must be
// sent to the authorization server); the client SECRET and every token value
// are not.
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

// --- routing, auth and the no-secret rule ----------------------------------

// TestMCPAuthRoutes_RequireBearerToken proves all five routes are registered
// and guarded: an unregistered path would answer 404, a registered one guarded
// by withAuth answers 401 without a token.
func TestMCPAuthRoutes_RequireBearerToken(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	base := mcpAuthURL(fx)
	// A shape-valid id, so the 401 cannot be confused with a 400 for a bad id.
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

// --- route 2: start ---------------------------------------------------------

// TestMCPAuthStart_ReturnsAWorkingURLAndNoSecret pins the start response: the
// DTO's four fields, an auth_url that really leads to the authorization server
// with this flow's state and PKCE challenge, a redirect_uri whose loopback
// listener is BOUND (invariant 1's "always a working auth_url"), and no
// credential anywhere in the body.
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
	// The listener is the "working" half of the URL: without it the user's
	// browser would land on a dead port.
	assertPortOpen(t, portOf(t, start.RedirectURI))

	// 5m budget, matching pkg/mcp's defaultAuthTimeout.
	if d := start.ExpiresAt.Sub(before); d < 4*time.Minute || d > 6*time.Minute {
		t.Fatalf("expires_at is %v away, want ~5m", d)
	}

	mcpAuthAssertNoSecrets(t, "start", body, mcpAuthStoredSecrets(t, fx.f, mcpAuthRemoteName))
}

// TestMCPAuthStart_SecondStartReplacesThePairsFlow pins invariant 1 over HTTP:
// the second start returns a NEW working URL bound to the SAME (pinned) port —
// which is only possible because the first listener was released — and the
// first flow id stops resolving.
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
	// The pinned port is bound by the flow that replaced the first one.
	assertPortOpen(t, firstPort)

	if status, _, body := mcpAuthPoll(t, fx, "agent1", first.FlowID); status != http.StatusNotFound {
		t.Fatalf("poll of the replaced flow: status = %d, want 404; body=%s", status, body)
	}
	status, poll, body := mcpAuthPoll(t, fx, "agent1", second.FlowID)
	if status != http.StatusOK || poll.State != "pending" {
		t.Fatalf("poll of the replacing flow = (%d, %+v), want (200, pending); body=%s", status, poll, body)
	}

	// Releasing the survivor frees the one listener the pair owns.
	if status, body := mcpAuthCancel(t, fx, "agent1", second.FlowID); status != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204; body=%s", status, body)
	}
	assertPortClosed(t, firstPort)
}

// TestMCPAuthStart_ErrorMatrix walks the contract's error table for route 2:
// an unknown name, a name that exists but has no resolved config, a stdio
// entry (nothing to authorize), and the two agent_id failures.
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

// --- route 3 + 4: poll and complete ----------------------------------------

// TestMCPAuthPoll_PendingThenDoneFiresTheReloadOnce is the happy path end to
// end: start, poll pending, drive the authorization server, complete with the
// code, and see done + authenticated. The reload seam must fire exactly once —
// on the pending → done transition — and the inventory must agree that the
// server is now usable (route 1 and route 3 read the same store).
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

	// The user's browser half: GET the authorization URL and take the code.
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

	// Further polls are stable AND do not reload again.
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

	// The credential is visible to the inventory route, from a store built by
	// the request rather than shared with the flow.
	invStatus, inv, invBody := mcpInventory(t, fx.f, "agent1")
	if invStatus != http.StatusOK {
		t.Fatalf("inventory status = %d, want 200; body=%s", invStatus, invBody)
	}
	auth := mcpRow(t, inv, mcpAuthRemoteName).Server.Auth
	if auth == nil || !auth.Authenticated || !auth.Required || auth.Method != "oauth" {
		t.Fatalf("inventory auth after the login = %+v, want required/authenticated/oauth", auth)
	}
}

// TestMCPAuthComplete_FailedExchangeIsRetryableAndDoesNotReload: a wrong code
// leaves the flow OPEN ("error" without Done, per the engine), the client keeps
// its paste box, no reload fires, and the SAME flow recovers with the real code.
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

	// Still resolvable, reported as a retryable error, and never reloaded.
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

	// Retry on the SAME flow.
	code, state := mcpAuthAuthorizeCode(t, start.AuthURL)
	status, done, body := mcpAuthComplete(t, fx, "agent1", start.FlowID, code, state)
	if status != http.StatusOK || done.State != "done" {
		t.Fatalf("retry = (%d, %+v), want (200, done); body=%s", status, done, body)
	}
	if got := atomic.LoadInt64(reloads); got != 1 {
		t.Fatalf("reload fired %d times after the retry, want 1", got)
	}
}

// mcpAuthCompleteRaw posts route 4 with a caller-supplied body, so a test can
// send a shape the typed helper cannot express (blank code, junk JSON).
func mcpAuthCompleteRaw(t *testing.T, fx *mcpAuthEngineFixture, agentID, flowID string, req MCPAuthCompleteRequest) (int, []byte) {
	t.Helper()
	return mcpAuthDo(t, fx.f.ts, http.MethodPost,
		fmt.Sprintf("%s/api/v1/mcp/auth/%s/complete?agent_id=%s", mcpAuthURL(fx), flowID, agentID), req, true)
}

// TestMCPAuthComplete_RejectsBlankCodeAndBadBody pins route 4's client errors:
// a missing code is named as such instead of surfacing as a generic flow
// failure, and a body that is not JSON is body_invalid.
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

	// Junk body: the decoder refuses it before the flow is touched.
	status, body = mcpAuthDo(t, fx.f.ts, http.MethodPost,
		fmt.Sprintf("%s/api/v1/mcp/auth/%s/complete?agent_id=agent1", mcpAuthURL(fx), start.FlowID),
		"not-json", true)
	if status != http.StatusBadRequest {
		t.Fatalf("junk body: status = %d, want 400; body=%s", status, body)
	}
	if code := mcpAuthBodyCode(t, body); code != "body_invalid" {
		t.Fatalf("junk body: code = %q, want body_invalid; body=%s", code, body)
	}

	// The flow survived both refusals.
	if status, poll, body := mcpAuthPoll(t, fx, "agent1", start.FlowID); status != http.StatusOK || poll.State != "pending" {
		t.Fatalf("poll after the refused completes = (%d, %+v), want (200, pending); body=%s", status, poll, body)
	}
}

// --- route 3: unknown, malformed and foreign ids ---------------------------

// TestMCPAuthStatus_RejectsUnknownMalformedAndForeignIDs pins the lookup's three
// answers: a valid-shaped id nobody holds is 404, a malformed one is 400 (a
// client error, not a missing resource), and another agent's id is 404 — the
// same answer as unknown, so the route leaks nothing about other agents.
func TestMCPAuthStatus_RejectsUnknownMalformedAndForeignIDs(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1", "agent2")

	status, start, body := mcpAuthStartFlow(t, fx, "agent1", mcpAuthRemoteName)
	if status != http.StatusOK {
		t.Fatalf("start status = %d, want 200; body=%s", status, body)
	}

	// The owner resolves it...
	if status, poll, body := mcpAuthPoll(t, fx, "agent1", start.FlowID); status != http.StatusOK || poll.State != "pending" {
		t.Fatalf("owner poll = (%d, %+v), want (200, pending); body=%s", status, poll, body)
	}
	// ...another known agent does not (invariant 4).
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
	// The owner still holds it: a foreign lookup must not disturb the flow.
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

// --- route 5: cancel --------------------------------------------------------

// TestMCPAuthCancel_IsIdempotentAndFreesTheListener pins invariant 7 and the
// listener release: 204 while the flow is in the registry (twice), the port
// closed, the DTO reporting the literal "canceled", and no reload — a cancelled
// login produced no credential.
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

	// Idempotent while the entry lives...
	if status, body := mcpAuthCancel(t, fx, "agent1", start.FlowID); status != http.StatusNoContent {
		t.Fatalf("second cancel status = %d, want 204; body=%s", status, body)
	}
	// ...and 404 for an id that never existed.
	unknown := strings.Repeat("f", mcpAuthFlowIDLen)
	status, body = mcpAuthCancel(t, fx, "agent1", unknown)
	if status != http.StatusNotFound {
		t.Fatalf("cancel of an unknown id: status = %d, want 404; body=%s", status, body)
	}
	if code := mcpAuthBodyCode(t, body); code != "mcp_auth_flow_not_found" {
		t.Fatalf("cancel of an unknown id: code = %q, want mcp_auth_flow_not_found; body=%s", code, body)
	}
}

// --- route 6: logout --------------------------------------------------------

// TestMCPAuthLogout_ForgetsTheRecordAndCancelsTheFlow pins route 6's whole job:
// the stored credential is gone, the pair's live flow is cancelled (a login
// completing after the logout must not re-store what the user removed), the
// reload fires once, and the route is idempotent for a known server with
// nothing stored.
func TestMCPAuthLogout_ForgetsTheRecordAndCancelsTheFlow(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	reloads := mcpAuthReloads(fx)

	// A completed login: the record carries the credential the logout must drop.
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

	// And a live flow for the same pair.
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

	// The live flow went with it.
	if status, poll, body := mcpAuthPoll(t, fx, "agent1", start.FlowID); status != http.StatusOK {
		t.Fatalf("poll after logout: status = %d, want 200; body=%s", status, body)
	} else if poll.State != "error" || poll.Authenticated {
		t.Fatalf("poll after logout = %+v, want the flow cancelled", poll)
	}

	// Idempotent: the name is still a known server, there is just nothing left
	// to forget. Each successful logout reloads once.
	if status, body := mcpAuthLogout(t, fx, "agent1", mcpAuthRemoteName); status != http.StatusNoContent {
		t.Fatalf("second logout status = %d, want 204; body=%s", status, body)
	}
	if got := atomic.LoadInt64(reloads); got != 2 {
		t.Fatalf("reload fired %d times after two successful logouts, want 2", got)
	}

	// A name that is neither a server nor a record is a typo, not a no-op.
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

// TestMCPAuthLogout_ClearsARecordForANameThatIsNotInAnyLayer: a record can
// outlive the entry that created it (the user edited mcp.json after logging
// in), and that leftover is exactly what a logout must clear. The name exists
// in no layer, so the route's "unknown name" guard must consult the STORE
// before answering 404 — otherwise a stale credential would be impossible to
// remove through the API.
func TestMCPAuthLogout_ClearsARecordForANameThatIsNotInAnyLayer(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")

	// A record can outlive the entry that created it (the user edited mcp.json
	// after logging in), and that is exactly the leftover a logout must clear.
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

// --- the contract's error table (finding 1 of the adversarial review) -------

// TestMCPAuthErrorCode_PinsTheContractErrorTable is the UNIT half of the
// contract's error table. mcpAuthErrorCode (rest_mcp.go) is the single
// implementation of it, and before this test ONLY its default branch ever ran:
// the 501 the HTTP suite asserts for a stdio entry comes from
// handleMCPAuthStart's own pre-check, which never reaches the mapping. Swapping
// the 503 and 409 pairs therefore left the whole MCP suite green.
//
// Each sentinel is passed BOTH bare and wrapped with %w, and ErrAuthProbeFailed
// once doubly wrapped: the contract specifies the mapping by errors.Is, so a
// rewrite to `err == sentinel` — which the wrapped rows would silently stop
// matching — fails here.
//
// Contract rows pinned: mcp_auth_not_supported/501, mcp_auth_disabled/503,
// mcp_auth_not_required/409, mcp_auth_flow_not_found/404, mcp_auth_failed/502.
func TestMCPAuthErrorCode_PinsTheContractErrorTable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
		// forbidden is a code this row must NOT produce: it names the
		// neighbouring cell the sentinel exists to be distinguished from.
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

		// ErrAuthProbeFailed must land on the GENERIC 502. pkg/mcp defines it
		// precisely so that a server answering garbage is never reported as a
		// server that needs no login (see its doc comment): reporting it as
		// "not required" would tell the operator to do nothing.
		{name: "ErrAuthProbeFailed", err: mcp.ErrAuthProbeFailed, want: http.StatusBadGateway, code: "mcp_auth_failed", forbidden: "mcp_auth_not_required"},
		{name: "doubly wrapped ErrAuthProbeFailed", err: fmt.Errorf("outer: %w", fmt.Errorf("mcp auth %q: probe: %w", "srv", mcp.ErrAuthProbeFailed)), want: http.StatusBadGateway, code: "mcp_auth_failed", forbidden: "mcp_auth_not_required"},

		// Cancellation and timeout are DTO material (AuthFlow.Status/Error), not
		// response codes: they reach the mapping only if something misroutes
		// them, and the generic 502 is then the honest answer.
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

// TestMCPAuthStart_EngineFailuresReachTheContractCodes is the ROUTE-level half
// of the same table: handleMCPAuthStart's engine-failure branch (step 5) is the
// only place the mapping is applied to a start, and the startMCPAuthFlow seam
// exists for exactly this. The seam returns the WRAPPED error a real engine call
// returns (`fmt.Errorf("…: %w", sentinel)`), so the route is proved to classify
// by errors.Is rather than by message text.
//
// The 503 mcp_auth_disabled row is the contract's "the store is off" cell; the
// same answer is driven through the REAL engine, with no seam, by
// TestMCPAuthStart_StoreDisabledIs503.
//
// Contract rows pinned: mcp_auth_disabled/503, mcp_auth_not_required/409,
// mcp_auth_not_supported/501, mcp_auth_failed/502.
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
			// The probe failure must never be answered as "this server needs no
			// login": that collapse is exactly what the engine's sentinel
			// prevents, and 409 mcp_auth_not_required would tell the caller to
			// stop trying.
			if tc.fail == mcp.ErrAuthProbeFailed && strings.Contains(string(body), "mcp_auth_not_required") {
				t.Fatalf("a failed probe was reported as not-required: %s", body)
			}
			// Nothing started, so no flow may be live for the pair.
			if got := fx.f.ts.channel.mcpAuthFlowRegistry().entryCount(); got != 0 {
				t.Fatalf("registry holds %d live flows after a refused start, want 0", got)
			}
		})
	}
}

// mcpAuthAddRootlessAgent wires one extra agent whose roots RESOLVE — so
// mcpPathsFor and Discover both succeed and the request reaches the flow start
// — but whose LeleDir is EMPTY, i.e. an agent with nowhere to persist a login.
//
// The entry lives in the AGENT layer file because the global layer's root IS the
// (empty) LeleDir: an empty root resolves to "" and is skipped by the merge's
// own layerFiles (pkg/mcp/discovery.go).
func mcpAuthAddRootlessAgent(t *testing.T, fx *mcpAuthEngineFixture, agentID string) {
	t.Helper()
	agentDir := t.TempDir()
	writeMCPJSON(t, filepath.Join(agentDir, "mcp.json"), fx.f.globalRaw)
	fx.f.loop.paths[agentID] = mcp.Paths{
		LeleDir:        "", // no lele dir: no place to persist a login
		AgentWorkspace: agentDir,
		Cwd:            t.TempDir(), // no project layer
	}
}

// TestMCPAuthStart_StoreDisabledIs503 pins the contract's mcp_auth_disabled row
// through the REAL engine, with no seam. An agent whose roots resolve but whose
// LeleDir is empty makes mcpAuthStoreFor hand mcp.StartAuth a store that is
// present yet DISABLED (mcp.NewAuthStore("") is a deliberate no-op store, never
// a process-relative file), and StartAuth reports ErrAuthStoreDisabled for it —
// the same verdict the CLI gives when there is no lele directory.
//
// On the premise that the 503 can only surface "via mcpAuthStoreFor returning
// nil": that nil is NOT reachable from this route, and the reason is structural
// rather than a missing fixture. mcpAuthStoreFor returns nil exactly when (a)
// the agent loop does not implement mcpPathsSource, or (b) MCPPathsFor reports
// the agent unknown — and BOTH are already refused one step earlier by
// mcpPathsFor in the same handler (500 mcp_unavailable and 404 agent_not_found
// respectively). A nil would therefore need MCPPathsFor to answer differently
// twice within one request, which no implementation does. The reachable
// store-less path is this one, and the nil returns themselves are pinned
// directly by TestMCPAuthStoreFor_NilWhenTheAgentIsUnresolvable.
//
// Contract row pinned: mcp_auth_disabled/503.
func TestMCPAuthStart_StoreDisabledIs503(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1", "rootless")
	mcpAuthAddRootlessAgent(t, fx, "rootless")

	// Non-vacuity: the same request shape resolves AND starts for the agent that
	// does have a lele dir, so a 503 below cannot mean "the fixture is broken".
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

// --- the failure half of the routes (finding 3 of the adversarial review) ---

// TestMCPAuthRoutes_RefuseEscapedServerNames pins the {name} guard on BOTH
// {name} routes (start = route 2, logout = route 6). Go's ServeMux hands the
// handler the DECODED segment, so `POST /api/v1/mcp/servers/a%2Fb/auth` reaches
// handleMCPAuthStart with PathValue("name") == "a/b", and
// `..%2F..%2Fetc%2Fpasswd` with "../../etc/passwd" (verified against a
// standalone ServeMux). The `strings.Contains(name, "/")` check is the only
// thing keeping such a name out of the registry and out of the
// "failed to start authorization for %q" message, which is echoed in the
// response body and lands in the gateway logs.
//
// Contract rows pinned: invalid_request/400 (the "bad name" cell of the start
// route, and the same guard on logout).
func TestMCPAuthRoutes_RefuseEscapedServerNames(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")

	// Escaped names Go's mux really produces: a separator, a traversal attempt,
	// a rooted path, and the lowercase escape spelling.
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
				// The refused name crosses no surface: not echoed, not logged
				// into a URL, not registered as a live flow.
				if strings.Contains(string(body), "a/b") || strings.Contains(string(body), "passwd") {
					t.Fatalf("%s %s echoed the refused name: %s", route.method, escaped, body)
				}
				if got := fx.f.ts.channel.mcpAuthFlowRegistry().entryCount(); got != 0 {
					t.Fatalf("%s %s left %d live flows in the registry, want 0", route.method, escaped, got)
				}
			})
		}
	}

	// Non-vacuity: the same guard lets a legitimate name through (and the
	// logout of a known server with nothing stored is the idempotent 204).
	status, body := mcpAuthDo(t, fx.f.ts, http.MethodDelete,
		fmt.Sprintf("%s/api/v1/mcp/servers/%s/auth?agent_id=agent1", mcpAuthURL(fx), mcpAuthRemoteName), nil, true)
	if status != http.StatusNoContent {
		t.Fatalf("logout of %q: status = %d, want 204; body=%s", mcpAuthRemoteName, status, body)
	}
}

// TestMCPAuthCompleteAndCancel_RejectMalformedFlowIDsEarly pins the early
// flow_id SHAPE guard on the two routes that take a flow id in the path but had
// no coverage for it: complete (route 4) and cancel (route 5). A malformed id
// is a client error (400 invalid_request), never the 404
// mcp_auth_flow_not_found reserved for an unknown/expired/foreign id.
//
// The complete case sends a JUNK body on purpose: the handler validates the id
// BEFORE decoding (see its documented order), so invalid_request here also pins
// that order — a decoder-first implementation would answer body_invalid.
//
// The agent_id half is pinned for both routes too: an unknown agent is
// 404 agent_not_found even for a MALFORMED id, which is only possible if
// agent_id is checked before the id shape.
//
// Contract rows pinned: invalid_request/400 (bad flow_id shape),
// agent_not_found/404.
func TestMCPAuthCompleteAndCancel_RejectMalformedFlowIDsEarly(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")

	badIDs := []string{
		"abc",                                   // too short
		strings.Repeat("a", mcpAuthFlowIDLen+1), // too long
		strings.ToUpper(strings.Repeat("a", mcpAuthFlowIDLen)), // uppercase hex
		strings.Repeat("z", mcpAuthFlowIDLen),                  // non-hex, right length
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

	// Non-vacuity, both halves. A WELL-FORMED id is not a shape error: it is the
	// 404 mcp_auth_flow_not_found reserved for unknown/expired/foreign ids — and
	// on complete that 404 is produced by mcpAuthErrorCode (via writeMCPAuthError
	// around registry.Complete's errMCPAuthFlowNotFound), which is the mapping
	// branch the unit table pins in isolation.
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

	// Order: agent_id first, so an unknown agent wins over a malformed id on
	// both routes.
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
