package channels

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// setthinkLoop overrides SetThinkLevel to return false (invalid level).
type setthinkLoop struct {
	*overrideLoop
	invalider   func(sessionKey, level string) bool
	getLevel    func(sessionKey string) string
	inprog      func(sessionKey string) *providers.Message
}

// stLoop builds a wrapper that overrides the level-related methods.
func stLoop(base *overrideLoop) *setthinkLoop {
	return &setthinkLoop{overrideLoop: base}
}

func (s *setthinkLoop) SetThinkLevel(sessionKey, level string) bool {
	if s.invalider != nil {
		return s.invalider(sessionKey, level)
	}
	return s.overrideLoop.SetThinkLevel(sessionKey, level)
}

func (s *setthinkLoop) GetThinkLevel(sessionKey string) string {
	if s.getLevel != nil {
		return s.getLevel(sessionKey)
	}
	return s.overrideLoop.GetThinkLevel(sessionKey)
}

func TestHandleSessionAgent_InvalidBodyV7(t *testing.T) {
	ts := newNativeTestServer(t)
	req := authedRecorderReq(t, ts, http.MethodPatch, "/api/v1/chat/sessions/native:x/agent", `{bad`)
	req.SetPathValue("sessionKey", "native:x")
	rec := httptest.NewRecorder()
	ts.channel.handleSessionAgent(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleSessionAgent_MissingAgentIDV7(t *testing.T) {
	ts := newNativeTestServer(t)
	req := authedRecorderReq(t, ts, http.MethodPatch, "/api/v1/chat/sessions/native:x/agent", `{"agent_id":""}`)
	req.SetPathValue("sessionKey", "native:x")
	rec := httptest.NewRecorder()
	ts.channel.handleSessionAgent(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleSessionThinking_InvalidBodyV7(t *testing.T) {
	ts := newNativeTestServer(t)
	req := authedRecorderReq(t, ts, http.MethodPatch, "/api/v1/chat/sessions/native:x/thinking", `{bad`)
	req.SetPathValue("sessionKey", "native:x")
	rec := httptest.NewRecorder()
	ts.channel.handleSessionThinking(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestHandleSessionThinking_InvalidLevelV7 exercises SetThinkLevel returning false.
func TestHandleSessionThinking_InvalidLevelV7(t *testing.T) {
	ts := newNativeTestServer(t)
	loop := stLoop(&overrideLoop{nativeTestAgentLoop: ts.loop})
	loop.invalider = func(string, string) bool { return false }
	ts.channel.agentLoop = loop

	req := authedRecorderReq(t, ts, http.MethodPatch, "/api/v1/chat/sessions/native:x/thinking", `{"level":"high"}`)
	req.SetPathValue("sessionKey", "native:x")
	rec := httptest.NewRecorder()
	ts.channel.handleSessionThinking(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleSessionName_InvalidBodyV7(t *testing.T) {
	ts := newNativeTestServer(t)
	req := authedRecorderReq(t, ts, http.MethodPatch, "/api/v1/chat/sessions/native:x/name", `{bad`)
	req.SetPathValue("sessionKey", "native:x")
	rec := httptest.NewRecorder()
	ts.channel.handleSessionName(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleSessionName_MissingNameV7(t *testing.T) {
	ts := newNativeTestServer(t)
	req := authedRecorderReq(t, ts, http.MethodPatch, "/api/v1/chat/sessions/native:x/name", `{"name":""}`)
	req.SetPathValue("sessionKey", "native:x")
	rec := httptest.NewRecorder()
	ts.channel.handleSessionName(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// setNameErrLoop overrides SetName to return an error.
type setNameErrLoop struct {
	*overrideLoop
}

func (s *setNameErrLoop) SetName(sessionKey, name string) error {
	return errTestSetName
}

var errTestSetName = newTestError("set name failed")

// TestHandleSessionName_SetNameFailV7 exercises the SetName error branch.
func TestHandleSessionName_SetNameFailV7(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.agentLoop = &setNameErrLoop{overrideLoop: &overrideLoop{nativeTestAgentLoop: ts.loop}}

	req := authedRecorderReq(t, ts, http.MethodPatch, "/api/v1/chat/sessions/native:x/name", `{"name":"Chat"}`)
	req.SetPathValue("sessionKey", "native:x")
	rec := httptest.NewRecorder()
	ts.channel.handleSessionName(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// TestHandleSessionContext_OverflowV7 exercises the usagePercent>100 clamp.
func TestHandleSessionContext_OverflowV7(t *testing.T) {
	ts := newNativeTestServer(t)
	loop := &overrideLoop{nativeTestAgentLoop: ts.loop, contextUse: func(string) (int, int) {
		return 200, 100 // 200% usage → clamped to 100
	}}
	ts.channel.agentLoop = loop

	req := authedRecorderReq(t, ts, http.MethodGet, "/api/v1/chat/sessions/native:x/context", "")
	req.SetPathValue("sessionKey", "native:x")
	rec := httptest.NewRecorder()
	ts.channel.handleSessionContext(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload SessionContextResponse
	decodeJSONResponse(t, rec, &payload)
	if payload.UsagePercent != 100 {
		t.Fatalf("usage_percent = %v, want 100", payload.UsagePercent)
	}
}

func TestHandleSessionSummary_WithSummaryV7(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.loop.histories["native:"+ts.clientID] = []providers.Message{{Role: "user", Content: "hi"}}
	loop := &summaryLoop{nativeTestAgentLoop: ts.loop, summary: "a summary"}
	ts.channel.agentLoop = loop

	req := authedRecorderReq(t, ts, http.MethodGet, "/api/v1/chat/sessions/native:x/summary", "")
	req.SetPathValue("sessionKey", "native:"+ts.clientID)
	rec := httptest.NewRecorder()
	ts.channel.handleSessionSummary(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload SessionSummaryResponse
	decodeJSONResponse(t, rec, &payload)
	if payload.Summary != "a summary" {
		t.Fatalf("summary = %q, want %q", payload.Summary, "a summary")
	}
}

type summaryLoop struct {
	*nativeTestAgentLoop
	summary string
}

func (s *summaryLoop) GetSessionSummary(sessionKey string) string {
	return s.summary
}

// TestHandleSessionSubagents_NoSessionKeyV7 exercises the missing-session-key branch.
func TestHandleSessionSubagents_NoSessionKeyV7(t *testing.T) {
	ts := newNativeTestServer(t)
	req := authedRecorderReq(t, ts, http.MethodGet, "/api/v1/chat/sessions//subagents", "")
	req.SetPathValue("sessionKey", "")
	rec := httptest.NewRecorder()
	ts.channel.handleSessionSubagents(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestHandleSessionSubagents_NormalizesAndStripsPrefixV7 exercises the key
// normalization (non "native:" input) and native: prefix-stripping on entries.
func TestHandleSessionSubagents_NormalizesAndStripsPrefixV7(t *testing.T) {
	ts := newNativeTestServer(t)
	bare := ts.clientID // no "native:" prefix — handler should normalize it
	full := "native:" + bare

	ts.loop.sessionSubagents[full] = []SubagentTaskInfo{
		{TaskID: "t1", SessionKey: full + ":subagent-1", Status: "running", Created: 1},
		{TaskID: "t2", SessionKey: "bare-key", Status: "completed", Created: 2},
	}

	req := authedRecorderReq(t, ts, http.MethodGet, "/api/v1/chat/sessions/"+bare+"/subagents", "")
	req.SetPathValue("sessionKey", bare)
	rec := httptest.NewRecorder()
	ts.channel.handleSessionSubagents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var payload SessionSubagentsResponse
	decodeJSONResponse(t, rec, &payload)
	// Response returns the original (non-normalized) session key.
	if payload.SessionKey != bare {
		t.Fatalf("session_key = %q, want %q", payload.SessionKey, bare)
	}
	if len(payload.Subagents) != 2 {
		t.Fatalf("got %d subagents, want 2", len(payload.Subagents))
	}
	// t2 created=2 newest first.
	if payload.Subagents[0].TaskID != "t2" {
		t.Fatalf("first = %q, want t2", payload.Subagents[0].TaskID)
	}
	// The t1 entry's session key had the native: prefix stripped.
	var t1Key string
	for _, s := range payload.Subagents {
		if s.TaskID == "t1" {
			t1Key = s.SessionKey
		}
	}
	if t1Key != full+":subagent-1" {
		t.Fatalf("t1 session_key = %q, want %q (native: stripped)", t1Key, full+":subagent-1")
	}
}

// TestHandleSessionForbiddenV7 exercises ownership denial for a subagent-style
// key with no parent (returns 403).
func TestHandleSessionForbiddenV7(t *testing.T) {
	ts := newNativeTestServer(t)
	req := authedRecorderReq(t, ts, http.MethodGet, "/api/v1/chat/sessions/subagent:nope/model", "")
	req.SetPathValue("sessionKey", "subagent:nope")
	rec := httptest.NewRecorder()
	ts.channel.handleSessionModel(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// TestHandleSessionCompactV7 covers the compact success path.
func TestHandleSessionCompactV7(t *testing.T) {
	ts := newNativeTestServer(t)
	req := authedRecorderReq(t, ts, http.MethodPost, "/api/v1/chat/sessions/native:x/compact", "")
	req.SetPathValue("sessionKey", "native:"+ts.clientID)
	rec := httptest.NewRecorder()
	ts.channel.handleSessionCompact(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "compacted") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestHandleSessionModel_InvalidBodyV7(t *testing.T) {
	ts := newNativeTestServer(t)
	req := authedRecorderReq(t, ts, http.MethodPatch, "/api/v1/chat/sessions/native:x/model", `{bad`)
	req.SetPathValue("sessionKey", "native:x")
	rec := httptest.NewRecorder()
	ts.channel.handleSessionModel(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleSessionGet_WithDataV7(t *testing.T) {
	ts := newNativeTestServer(t)
	sk := "native:" + ts.clientID
	loop := &overrideLoop{nativeTestAgentLoop: ts.loop, inProgress: func(string) *providers.Message {
		return nil
	}, contextUse: func(string) (int, int) { return 0, 0 }}
	ts.channel.agentLoop = loop

	req := authedRecorderReq(t, ts, http.MethodGet, "/api/v1/chat/sessions/native:x/", "")
	req.SetPathValue("sessionKey", sk)
	rec := httptest.NewRecorder()
	ts.channel.handleChatSessionGet(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload map[string]interface{}
	decodeJSONResponse(t, rec, &payload)
	if payload["session_key"] != sk {
		t.Fatalf("session_key = %v, want %q", payload["session_key"], sk)
	}
}

func newTestError(msg string) error {
	return &testError{msg: msg}
}

type testError struct {
	msg string
}

func (e *testError) Error() string { return e.msg }