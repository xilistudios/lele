package channels

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/update"
)

// TestHandleLogout covers the handleLogout branches.
func TestHandleLogout(t *testing.T) {
	ts := newNativeTestServer(t)

	// Valid client via HTTP → 200.
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("logout valid status = %d, want 200", resp.StatusCode)
	}
}

// TestHandleLogout_MissingClient directly exercises the missing X-Client-Id
// branch, which the auth middleware normally shields from HTTP clients.
func TestHandleLogout_MissingClient(t *testing.T) {
	ts := newNativeTestServer(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	ts.channel.handleLogout(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("logout missing client status = %d, want 400", rr.Code)
	}
}

// TestHandleLogout_AlreadyRemoved exercises the branch where RemoveClient
// already returned an error (client already gone).
func TestHandleLogout_AlreadyRemoved(t *testing.T) {
	ts := newNativeTestServer(t)

	// Remove the client first, then call the handler directly.
	if err := ts.channel.auth.RemoveClient(ts.clientID); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("X-Client-Id", ts.clientID)
	ts.channel.handleLogout(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("logout already-removed status = %d, want 200", rr.Code)
	}
}

// TestHandleChatApprove exercises the approval via HTTP (with and without a
// matching request id) and the persistApprovalMessage helper.
func TestHandleChatApprove(t *testing.T) {
	ts := newNativeTestServer(t)
	am := NewApprovalManager()
	ts.channel.approvalManager = am

	// Create a real approval.
	approval := am.CreateApproval("native:"+ts.clientID, "echo hi", "reason", 0)

	body := mustMarshal(ApproveRequest{RequestID: approval.ID, Approved: true})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/sessions/native:"+ts.clientID+"/approve", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve status = %d, want 200", resp.StatusCode)
	}
	var out ApproveResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Approved || out.RequestID != approval.ID {
		t.Errorf("approve response = %+v", out)
	}

	// Missing request_id → 400.
	req2, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/sessions/native:"+ts.clientID+"/approve", bytes.NewReader([]byte(`{}`)))
	req2.Header.Set("Authorization", "Bearer "+ts.token)
	resp2, _ := http.DefaultClient.Do(req2)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("missing request_id status = %d, want 400", resp2.StatusCode)
	}

	// Invalid body → 400.
	req3, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/sessions/native:"+ts.clientID+"/approve", bytes.NewReader([]byte(`bad`)))
	req3.Header.Set("Authorization", "Bearer "+ts.token)
	resp3, _ := http.DefaultClient.Do(req3)
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid body status = %d, want 400", resp3.StatusCode)
	}
}

// TestHandleChatApprove_NotFound covers request_id that doesn't exist.
func TestHandleChatApprove_NotFound(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.approvalManager = NewApprovalManager()

	body := mustMarshal(ApproveRequest{RequestID: "does-not-exist", Approved: true})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/sessions/native:"+ts.clientID+"/approve", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("approve not-found status = %d, want 404", resp.StatusCode)
	}
}

// TestPersistApprovalMessage directly exercises persistApprovalMessage in both
// approved and rejected directions.
func TestPersistApprovalMessage(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID
	ts.channel.persistApprovalMessage(sessionKey, "req-1", true, "echo hi", "")
	ts.channel.persistApprovalMessage(sessionKey, "req-2", false, "", "")
	ts.channel.persistApprovalMessage(sessionKey, "req-3", false, "rm -rf", "")

	hist := ts.loop.GetSessionHistory(sessionKey)
	if len(hist) != 3 {
		t.Fatalf("expected 3 persisted messages, got %d", len(hist))
	}
	if hist[0].Role != "tool" || hist[0].ToolCallID != "approval:req-1" {
		t.Errorf("first message = %+v", hist[0])
	}
	if hist[1].Content != "❌ Command rejected" {
		t.Errorf("rejected content = %q", hist[1].Content)
	}
}

// TestHandleSystemRestart covers the no-update-service 503 branch and the
// success path with a restarter that errors.
func TestHandleSystemRestart(t *testing.T) {
	ts := newNativeTestServer(t)

	// No update service → 503.
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/system/restart", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("no-service restart status = %d, want 503", resp.StatusCode)
	}
}

// TestHandleSystemRestart_WithService covers the accepted/restarting path.
func TestHandleSystemRestart_WithService(t *testing.T) {
	ts := newNativeTestServer(t)
	// NewUpdater with an empty binary path; Restart's Restarter.Restart will
	// not be able to detect a supervisor and returns an error, which only
	// happens in the goroutine (we assert the 202 ack here).
	ts.channel.SetUpdateService(update.NewUpdater("", t.TempDir(), "0.1.0"))

	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/system/restart", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("restart with service status = %d, want 202", resp.StatusCode)
	}
}

// TestHandleFileView covers path missing, deny outside leleDir, not-found, and
// success paths.
func TestHandleFileView(t *testing.T) {
	ts := newNativeTestServer(t)

	// Missing path → 400.
	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/files/view", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing path status = %d, want 400", resp.StatusCode)
	}

	// Path outside leleDir → 403. Use a path definitely outside ~/.lele.
	req2, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/files/view?path="+t.TempDir()+"/outside.txt", nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("outside path status = %d, want 403", resp2.StatusCode)
	}
}

// TestHandleFileView_InsideLeleDir verifies a real file inside leleDir serves.
func TestHandleFileView_InsideLeleDir(t *testing.T) {
	ts := newNativeTestServer(t)

	leleDir := filepath.Join(t.TempDir(), "lele")
	if err := os.MkdirAll(leleDir, 0755); err != nil {
		t.Fatal(err)
	}
	ts.channel.cfg.LeleDir = leleDir

	f := filepath.Join(leleDir, "hello.txt")
	if err := os.WriteFile(f, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/files/view?path="+f, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("file view status = %d, want 200", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	if string(data) != "hello" {
		t.Errorf("body = %q", string(data))
	}
}

// TestHandleFileView_NotFound covers a missing file inside the allowed dir.
func TestHandleFileView_NotFound(t *testing.T) {
	ts := newNativeTestServer(t)
	leleDir := t.TempDir()
	ts.channel.cfg.LeleDir = leleDir

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/files/view?path="+filepath.Join(leleDir, "missing.txt"), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing file status = %d, want 404", resp.StatusCode)
	}

	// Directory inside allowed dir → 400.
	req2, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/files/view?path="+leleDir, nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("directory status = %d, want 400", resp2.StatusCode)
	}
}
