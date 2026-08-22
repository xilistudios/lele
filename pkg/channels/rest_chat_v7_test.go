package channels

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// TestClassifySessionKeyKindAllV7 exercises all classifySessionKeyKind branches.
func TestClassifySessionKeyKindAllV7(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "chat"},
		{"heartbeat", "heartbeat"},
		{"cron-spawn-123", "cron-spawn"},
		{"cron-abc", "cron"},
		{"subagent:xyz", "subagent"},
		{"agent:main:subagent-42", "subagent"},
		{"plain-session", "chat"},
	}
	for _, c := range cases {
		if got := classifySessionKeyKind(c.in); got != c.want {
			t.Errorf("classifySessionKeyKind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// historyRequest builds an authenticated history request with the client id
// header set (bypassing the auth middleware when calling handlers directly).
func historyRequest(t *testing.T, ts *nativeTestServer, path string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("X-Client-Id", ts.clientID)
	return req
}

// TestHandleChatSessions_NoClientV7 exercises the GetClient-miss branch.
func TestHandleChatSessions_NoClientV7(t *testing.T) {
	ts := newNativeTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/sessions", nil)
	req.Header.Set("X-Client-Id", "nope")
	rec := httptest.NewRecorder()
	ts.channel.handleChatSessions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload ChatSessionsResponse
	decodeJSONResponse(t, rec, &payload)
	if payload.Sessions == nil || len(payload.Sessions) != 0 {
		t.Fatalf("expected empty sessions, got %+v", payload)
	}
}

// TestHandleChatSessions_MergeAndFiltersV7 exercises kind/mode filtering and
// the persisted-session merge loop (kindFilter/includeSystem set).
func TestHandleChatSessions_MergeAndFiltersV7(t *testing.T) {
	ts := newNativeTestServer(t)

	// Track a session with a history that has messages (owned by client).
	sk := "native:" + ts.clientID
	ts.channel.auth.TrackSessionKey(ts.clientID, sk)
	ts.loop.histories[sk] = []providers.Message{{Role: "user", Content: "hi"}}

	// Seed persisted (system) sessions visible via ListAllSessions.
	ts.loop.histories["heartbeat"] = []providers.Message{{Role: "user", Content: "beat"}}
	ts.loop.histories["cron-7"] = []providers.Message{{Role: "user", Content: "cron"}}

	// kind filter = chat → the native session matches; heartbeat/cron do not.
	req := authenticatedRequest(t, ts, "/api/v1/chat/sessions?kind=chat")
	rec := httptest.NewRecorder()
	ts.channel.handleChatSessions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload ChatSessionsResponse
	decodeJSONResponse(t, rec, &payload)
	if len(payload.Sessions) != 1 {
		t.Fatalf("kind=chat got %d sessions, want 1", len(payload.Sessions))
	}

	// include_system=true merges persisted sessions (heartbeat + cron).
	req2 := authenticatedRequest(t, ts, "/api/v1/chat/sessions?include_system=true")
	rec2 := httptest.NewRecorder()
	ts.channel.handleChatSessions(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec2.Code, http.StatusOK)
	}
	var payload2 ChatSessionsResponse
	decodeJSONResponse(t, rec2, &payload2)
	if len(payload2.Sessions) < 3 {
		t.Fatalf("include_system got %d sessions, want >= 3", len(payload2.Sessions))
	}
}

// TestHandleChatSessions_ModeAndKindFilteringV7 exercises the effective-mode
// default and the filter mismatch branches in the tracked-session loop.
func TestHandleChatSessions_ModeAndKindFilteringV7(t *testing.T) {
	ts := newNativeTestServer(t)
	sk := "native:" + ts.clientID
	ts.channel.auth.TrackSessionKey(ts.clientID, sk)
	ts.loop.histories[sk] = []providers.Message{{Role: "user", Content: "hi"}}

	// mode=nonexistent — session has no mode → effective "agent" → mismatch.
	req := authenticatedRequest(t, ts, "/api/v1/chat/sessions?mode=nonexistent")
	rec := httptest.NewRecorder()
	ts.channel.handleChatSessions(rec, req)
	var payload ChatSessionsResponse
	decodeJSONResponse(t, rec, &payload)
	if len(payload.Sessions) != 0 {
		t.Fatalf("mode=nonexistent got %d sessions, want 0", len(payload.Sessions))
	}

	// mode filter that matches the effective "agent" default.
	req3 := authenticatedRequest(t, ts, "/api/v1/chat/sessions?mode=agent")
	rec3 := httptest.NewRecorder()
	ts.channel.handleChatSessions(rec3, req3)
	payload3 := ChatSessionsResponse{}
	decodeJSONResponse(t, rec3, &payload3)
	if len(payload3.Sessions) != 1 {
		t.Fatalf("mode=agent got %d sessions, want 1", len(payload3.Sessions))
	}

	// kind filter mismatch: kind=cron won't match the native chat session.
	req2 := authenticatedRequest(t, ts, "/api/v1/chat/sessions?kind=cron")
	rec2 := httptest.NewRecorder()
	ts.channel.handleChatSessions(rec2, req2)
	payload2 := ChatSessionsResponse{}
	decodeJSONResponse(t, rec2, &payload2)
	if len(payload2.Sessions) != 0 {
		t.Fatalf("kind=cron got %d sessions, want 0", len(payload2.Sessions))
	}
}

// TestHandleChatSessionsMeta_CoverageV7 exercises the meta fast-path with
// evicted-message handling and filters.
func TestHandleChatSessionsMeta_CoverageV7(t *testing.T) {
	ts := newNativeTestServer(t)
	sk := "native:" + ts.clientID
	ts.channel.auth.TrackSessionKey(ts.clientID, sk)

	// Session with no in-memory messages but evicted messages → HasMessages
	// false but GetEvictedMessageCount > 0 → included.
	ts.loop.evicted[sk] = []providers.Message{{Role: "user", Content: "evicted"}}
	ts.loop.histories["heartbeat"] = []providers.Message{{Role: "user", Content: "b"}}

	req := authenticatedRequest(t, ts, "/api/v1/chat/sessions/meta")
	rec := httptest.NewRecorder()
	ts.channel.handleChatSessionsMeta(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload ChatSessionsResponse
	decodeJSONResponse(t, rec, &payload)
	if len(payload.Sessions) == 0 {
		t.Fatal("expected at least the evicted-having session")
	}

	// include_system merge path.
	req2 := authenticatedRequest(t, ts, "/api/v1/chat/sessions/meta?include_system=true")
	rec2 := httptest.NewRecorder()
	ts.channel.handleChatSessionsMeta(rec2, req2)
	payload2 := ChatSessionsResponse{}
	decodeJSONResponse(t, rec2, &payload2)
	if len(payload2.Sessions) < 2 {
		t.Fatalf("include_system got %d sessions, want >= 2", len(payload2.Sessions))
	}

	// meta no-client branch.
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/chat/sessions/meta", nil)
	req3.Header.Set("X-Client-Id", "absent")
	rec3 := httptest.NewRecorder()
	ts.channel.handleChatSessionsMeta(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("no-client status = %d, want %d", rec3.Code, http.StatusOK)
	}
}

// TestHandleCreateSession_WithModeV7 covers the req.Mode non-empty branch.
func TestHandleCreateSession_WithModeV7(t *testing.T) {
	ts := newNativeTestServer(t)
	req := authedRecorderReq(t, ts, http.MethodPost, "/api/v1/chat/sessions",
		`{"session_key":"mysess","mode":"agent"}`)
	rec := httptest.NewRecorder()
	ts.channel.handleCreateSession(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	var payload CreateSessionResponse
	decodeJSONResponse(t, rec, &payload)
	if payload.SessionKey != "mysess" {
		t.Fatalf("session_key = %q, want %q", payload.SessionKey, "mysess")
	}
}

// TestHandleChatApprove_NoManagerV7 exercises the approval-unavailable branch.
func TestHandleChatApprove_NoManagerV7(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.approvalManager = nil
	req := authedRecorderReq(t, ts, http.MethodPost, "/api/v1/chat/sessions/native:x/approve",
		`{"request_id":"r1","approved":true}`)
	req.SetPathValue("sessionKey", "native:x")
	rec := httptest.NewRecorder()
	ts.channel.handleChatApprove(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// TestHandleChatApprove_InvalidBodyV7 exercises the JSON decode failure.
func TestHandleChatApprove_InvalidBodyV7(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.approvalManager = newApprovalManagerForTest()
	req := authedRecorderReq(t, ts, http.MethodPost, "/api/v1/chat/sessions/native:x/approve", `{bad`)
	req.SetPathValue("sessionKey", "native:x")
	rec := httptest.NewRecorder()
	ts.channel.handleChatApprove(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestHandleChatApprove_MissingRequestIDV7 exercises request_id missing.
func TestHandleChatApprove_MissingRequestIDV7(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.approvalManager = newApprovalManagerForTest()
	req := authedRecorderReq(t, ts, http.MethodPost, "/api/v1/chat/sessions/native:x/approve", `{"request_id":""}`)
	req.SetPathValue("sessionKey", "native:x")
	rec := httptest.NewRecorder()
	ts.channel.handleChatApprove(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestHandleChatApprove_NotFoundV7 exercises HandleApproval error branch.
func TestHandleChatApprove_NotFoundV7(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.approvalManager = newApprovalManagerForTest()
	req := authedRecorderReq(t, ts, http.MethodPost, "/api/v1/chat/sessions/native:x/approve",
		`{"request_id":"does-not-exist","approved":true}`)
	req.SetPathValue("sessionKey", "native:x")
	rec := httptest.NewRecorder()
	ts.channel.handleChatApprove(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestHandleChatApprove_SuccessV7 covers the approve and reject success paths.
func TestHandleChatApprove_SuccessV7(t *testing.T) {
	ts := newNativeTestServer(t)
	am := newApprovalManagerForTest()
	ts.channel.approvalManager = am

	pending := am.CreateApproval("native:"+ts.clientID, "rm -rf /tmp/x", "test", 0)
	requestID := pending.ID

	req := authedRecorderReq(t, ts, http.MethodPost, "/api/v1/chat/sessions/native:x/approve",
		`{"request_id":"`+requestID+`","approved":true}`)
	req.SetPathValue("sessionKey", "native:"+ts.clientID)
	rec := httptest.NewRecorder()
	ts.channel.handleChatApprove(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var payload ApproveResponse
	decodeJSONResponse(t, rec, &payload)
	if !payload.Approved || payload.RequestID != requestID {
		t.Fatalf("approve response = %+v", payload)
	}
	if payload.Message != "✅ Command approved" {
		t.Fatalf("approve message = %q", payload.Message)
	}

	pending2 := am.CreateApproval("native:"+ts.clientID, "command2", "reason2", 0)
	req2 := authedRecorderReq(t, ts, http.MethodPost, "/api/v1/chat/sessions/native:x/approve",
		`{"request_id":"`+pending2.ID+`","approved":false}`)
	req2.SetPathValue("sessionKey", "native:"+ts.clientID)
	rec2 := httptest.NewRecorder()
	ts.channel.handleChatApprove(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("reject status = %d, want %d", rec2.Code, http.StatusOK)
	}
	payload2 := ApproveResponse{}
	decodeJSONResponse(t, rec2, &payload2)
	if payload2.Approved {
		t.Fatal("expected approved=false for reject")
	}
	if payload2.Message != "❌ Command rejected" {
		t.Fatalf("reject message = %q", payload2.Message)
	}
}

// TestHandleChatHistory_SubagentInvalidV7 exercises handleChatHistory's
// subagent ID validation error branches (too long / bad format).
func TestHandleChatHistory_SubagentInvalidV7(t *testing.T) {
	ts := newNativeTestServer(t)

	longID := strings.Repeat("a", 65)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/sessions/native:x/history/"+longID, nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("X-Client-Id", ts.clientID)
	req.SetPathValue("sessionKey", "native:x")
	req.SetPathValue("subagentId", longID)
	rec := httptest.NewRecorder()
	ts.channel.handleChatHistory(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("too-long status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/chat/sessions/native:x/history/not-a-subagent-id", nil)
	req2.Header.Set("Authorization", "Bearer "+ts.token)
	req2.Header.Set("X-Client-Id", ts.clientID)
	req2.SetPathValue("sessionKey", "native:x")
	req2.SetPathValue("subagentId", "not-a-subagent-id")
	rec2 := httptest.NewRecorder()
	ts.channel.handleChatHistory(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("bad-format status = %d, want %d", rec2.Code, http.StatusBadRequest)
	}
}

// TestHandleChatHistory_ToolCallsV7 exercises the tool-call parsing branch
// including function-based args/name fallbacks.
func TestHandleChatHistory_ToolCallsV7(t *testing.T) {
	ts := newNativeTestServer(t)
	sk := "native:" + ts.clientID
	ts.loop.histories[sk] = []providers.Message{
		{
			Role: "assistant", Content: "planning",
			ToolCalls: []providers.ToolCall{
				{ID: "call-1", Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"/tmp/x"}`}},
				{ID: "call-2", Name: "search", Arguments: map[string]interface{}{"q": "golang"}},
				{ID: "call-empty", Function: &providers.FunctionCall{Name: "noop", Arguments: `{bad json`}},
			},
		},
		{Role: "tool", Content: "result", ToolCallID: "call-1"},
	}

	req := historyRequest(t, ts, "/api/v1/chat/sessions/"+sk+"/history")
	req.SetPathValue("sessionKey", sk)
	rec := httptest.NewRecorder()
	ts.channel.handleChatHistory(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var payload ChatHistoryResponse
	decodeJSONResponse(t, rec, &payload)
	if len(payload.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(payload.Messages))
	}
	var toolMsg *ChatHistoryMessage
	for i := range payload.Messages {
		if payload.Messages[i].Role == "tool" && payload.Messages[i].ToolCallID == "call-1" {
			toolMsg = &payload.Messages[i]
		}
	}
	if toolMsg == nil {
		t.Fatal("tool message not found")
	}
	if toolMsg.ToolName != "read_file" {
		t.Fatalf("tool name = %q, want read_file", toolMsg.ToolName)
	}
	// Verify the assistant's first parsed arguments (from function string).
	var asst *ChatHistoryMessage
	for i := range payload.Messages {
		if payload.Messages[i].Role == "assistant" {
			asst = &payload.Messages[i]
		}
	}
	if asst == nil || len(asst.ToolCalls) != 3 {
		t.Fatalf("expected 3 tool calls on assistant, got %d", toolCallsLen(rec))
	}
}

func toolCallsLen(rec *httptest.ResponseRecorder) int {
	var p ChatHistoryResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	for _, m := range p.Messages {
		if m.Role == "assistant" {
			return len(m.ToolCalls)
		}
	}
	return 0
}

// TestHandleChatHistory_InjectedContextSkipV7 exercises the injected-content
// skip branch (user message with empty Content but non-empty ContentParts).
func TestHandleChatHistory_InjectedContextSkipV7(t *testing.T) {
	ts := newNativeTestServer(t)
	sk := "native:" + ts.clientID
	ts.loop.histories[sk] = []providers.Message{
		{Role: "user", Content: "", ContentParts: []providers.ContentPart{{Type: "image"}}},
		{Role: "user", Content: "real"},
	}
	req := historyRequest(t, ts, "/api/v1/chat/sessions/"+sk+"/history")
	req.SetPathValue("sessionKey", sk)
	rec := httptest.NewRecorder()
	ts.channel.handleChatHistory(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload ChatHistoryResponse
	decodeJSONResponse(t, rec, &payload)
	if len(payload.Messages) != 1 {
		t.Fatalf("got %d messages, want 1 (injected context skipped)", len(payload.Messages))
	}
}

// TestHandleChatHistory_EvictedMaterializationV7 exercises the
// LoadEvictedMessages branch in handleChatHistory.
func TestHandleChatHistory_EvictedMaterializationV7(t *testing.T) {
	ts := newNativeTestServer(t)
	sk := "native:" + ts.clientID
	ts.loop.evicted[sk] = []providers.Message{{Role: "user", Content: "old"}}
	ts.loop.histories[sk] = []providers.Message{{Role: "assistant", Content: "new"}}
	req := historyRequest(t, ts, "/api/v1/chat/sessions/"+sk+"/history")
	req.SetPathValue("sessionKey", sk)
	rec := httptest.NewRecorder()
	ts.channel.handleChatHistory(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload ChatHistoryResponse
	decodeJSONResponse(t, rec, &payload)
	if len(payload.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(payload.Messages))
	}
}

// newApprovalManagerForTest builds an ApprovalManager with a real pending map.
func newApprovalManagerForTest() *ApprovalManager {
	return NewApprovalManager()
}

// authedRecorderReq builds a request with auth + client-id headers for direct
// handler invocation, bypassing the auth middleware.
func authedRecorderReq(t *testing.T, ts *nativeTestServer, method, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("X-Client-Id", ts.clientID)
	return req
}

func decodeJSONResponse(t *testing.T, rec *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	body := rec.Body.Bytes()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode response %q: %v", string(body), err)
	}
}
