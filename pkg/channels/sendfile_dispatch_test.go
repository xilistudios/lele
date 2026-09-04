package channels

// End-to-end tests for send_file attachment handling in
// dispatchOutboundMessage: staging outside paths under the lele dir,
// persisting the staged attachments on the assistant message, and emitting
// message.complete with a servable path.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
)

func TestDispatchStagesAttachmentAndServesIt(t *testing.T) {
	ts := newStagingTestServer(t)

	src := filepath.Join(t.TempDir(), "result.csv") // outside leleDir
	if err := os.WriteFile(src, []byte("a,b\n1,2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	sessionKey := "native:" + ts.clientID + "-stage"
	client := registerFakeWSClient(t, ts, sessionKey)

	out := []bus.FileAttachment{{
		Name:     "result.csv",
		Path:     src,
		MIMEType: "text/csv",
		Kind:     "file",
		Caption:  "here you go",
	}}
	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:     ChannelName,
		ChatID:      sessionKey,
		Content:     "done",
		MessageID:   "msg-stage-1",
		Attachments: out,
	})

	// 1) The caller's slice must not be mutated (the same OutboundMessage can
	//    be delivered to other channel subscribers sharing the backing array).
	if out[0].Path != src {
		t.Errorf("dispatch mutated caller attachment path: %q, want %q", out[0].Path, src)
	}

	// 2) WS message.complete must carry the STAGED path (servable) + original name.
	events := drainWSEvents(t, client)
	complete := findWSEvent(events, "message.complete")
	if complete == nil {
		t.Fatalf("no message.complete; events=%v", eventNames(events))
	}
	var payload WSMessageCompletePayload
	if err := json.Unmarshal(complete.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Attachments) != 1 {
		t.Fatalf("attachments = %v, want 1", payload.Attachments)
	}
	stagedPath, _ := payload.Attachments[0]["path"].(string)
	if stagedPath == "" || stagedPath == src {
		t.Fatalf("attachment path not staged: %q", stagedPath)
	}
	if !strings.HasPrefix(stagedPath, filepath.Join(ts.channel.cfg.LeleDir, "tmp", "attachments")) {
		t.Errorf("staged path %q not under lele staging dir", stagedPath)
	}
	if got := payload.Attachments[0]["name"]; got != "result.csv" {
		t.Errorf("attachment name = %v, want result.csv", got)
	}
	if got := payload.Attachments[0]["mime_type"]; got != "text/csv" {
		t.Errorf("attachment mime_type = %v, want text/csv", got)
	}
	if got := payload.Attachments[0]["caption"]; got != "here you go" {
		t.Errorf("attachment caption = %v, want 'here you go'", got)
	}

	// 3) Persistence hook was called with the staged attachment (same key the
	//    history refetch uses).
	calls := ts.loop.attachCalls()
	if len(calls) != 1 {
		t.Fatalf("AttachFilesToLastAssistant calls = %d, want 1", len(calls))
	}
	if calls[0].sessionKey != sessionKey {
		t.Errorf("persist sessionKey = %q, want %q", calls[0].sessionKey, sessionKey)
	}
	if len(calls[0].attachments) != 1 || calls[0].attachments[0].Path != stagedPath {
		t.Errorf("persisted attachments = %+v, want staged path %q", calls[0].attachments, stagedPath)
	}
	if calls[0].attachments[0].Name != "result.csv" {
		t.Errorf("persisted name = %q, want result.csv", calls[0].attachments[0].Name)
	}

	// 4) The staged copy is downloadable through the view endpoint. The WebUI
	//    sends name=<original attachment name> (MessageBubble buildDownloadUrl)
	//    so the browser saves "result.csv", not the uuid-prefixed staged name.
	resp, body := getView(t, ts, url.Values{
		"path": {stagedPath}, "download": {"1"}, "name": {"result.csv"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("view status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	if body != "a,b\n1,2\n" {
		t.Errorf("view body = %q, want csv payload", body)
	}
	want := `attachment; filename="result.csv"`
	if cd := resp.Header.Get("Content-Disposition"); cd != want {
		t.Errorf("Content-Disposition = %q, want %q", cd, want)
	}
}

func TestDispatchInsideLeleDirNotCopied(t *testing.T) {
	ts := newStagingTestServer(t)

	uploadDir := filepath.Join(ts.channel.cfg.LeleDir, "tmp", "uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(uploadDir, "zz_photo.png")
	if err := os.WriteFile(src, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}

	sessionKey := "native:" + ts.clientID + "-inside"
	client := registerFakeWSClient(t, ts, sessionKey)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Content:   "look",
		MessageID: "msg-inside-1",
		Attachments: []bus.FileAttachment{{
			Name: "photo.png", Path: src, MIMEType: "image/png", Kind: "image",
		}},
	})

	events := drainWSEvents(t, client)
	complete := findWSEvent(events, "message.complete")
	if complete == nil {
		t.Fatalf("no message.complete; events=%v", eventNames(events))
	}
	var payload WSMessageCompletePayload
	if err := json.Unmarshal(complete.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Attachments) != 1 {
		t.Fatalf("attachments = %v, want 1", payload.Attachments)
	}
	// Already under leleDir → the original path must be served as-is (no copy).
	if got := payload.Attachments[0]["path"]; got != src {
		t.Errorf("path = %v, want unchanged %q", got, src)
	}
	entries, err := os.ReadDir(filepath.Join(ts.channel.cfg.LeleDir, "tmp", "attachments"))
	if err == nil && len(entries) > 0 {
		t.Errorf("no staging copies expected, found %d", len(entries))
	}
}

func TestDispatchNoAttachmentsDoesNotPersist(t *testing.T) {
	ts := newStagingTestServer(t)

	sessionKey := "native:" + ts.clientID + "-plain"
	client := registerFakeWSClient(t, ts, sessionKey)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Content:   "hi",
		MessageID: "msg-plain-1",
	})

	drainWSEvents(t, client)
	if calls := ts.loop.attachCalls(); len(calls) != 0 {
		t.Errorf("AttachFilesToLastAssistant called %d times for attachment-less message", len(calls))
	}
}

func TestDispatchMissingFileKeepsOriginalPath(t *testing.T) {
	ts := newStagingTestServer(t)

	ghost := filepath.Join(t.TempDir(), "gone.txt")
	sessionKey := "native:" + ts.clientID + "-ghost"
	client := registerFakeWSClient(t, ts, sessionKey)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:     ChannelName,
		ChatID:      sessionKey,
		Content:     "oops",
		MessageID:   "msg-ghost-1",
		Attachments: []bus.FileAttachment{{Name: "gone.txt", Path: ghost}},
	})

	events := drainWSEvents(t, client)
	complete := findWSEvent(events, "message.complete")
	if complete == nil {
		t.Fatalf("message must still be delivered even if staging fails; events=%v", eventNames(events))
	}
	var payload WSMessageCompletePayload
	if err := json.Unmarshal(complete.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Attachments) != 1 {
		t.Fatalf("attachments = %v, want 1", payload.Attachments)
	}
	// Staging failed → original path preserved (view endpoint will 403/404 it,
	// but the message itself must not be dropped).
	if got := payload.Attachments[0]["path"]; got != ghost {
		t.Errorf("path = %v, want original %q", got, ghost)
	}
}

// --- REST history exposes persisted attachments (contract: JSON field
// "attachments" on ChatHistoryMessage, omitempty) ---

func TestChatHistoryReturnsAttachments(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID
	ts.loop.histories[sessionKey] = []providers.Message{
		{Role: "user", Content: "send me the file"},
		{
			Role:    "assistant",
			Content: "here you go",
			Attachments: []providers.MessageAttachment{{
				Name:     "result.csv",
				Path:     "/home/u/.lele/tmp/attachments/ab12cd34_result.csv",
				MIMEType: "text/csv",
				Kind:     "file",
				Caption:  "report",
			}},
		},
	}

	req, err := http.NewRequest(http.MethodGet,
		ts.server.URL+"/api/v1/chat/sessions/"+url.QueryEscape(sessionKey)+"/history", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Decode generically to pin the WIRE field names (contract), not the Go struct.
	var raw struct {
		Messages []map[string]interface{} `json:"messages"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, body)
	}
	if len(raw.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (%s)", len(raw.Messages), body)
	}
	// User message must omit the field entirely (omitempty).
	if _, ok := raw.Messages[0]["attachments"]; ok {
		t.Errorf("attachment-less message carries \"attachments\" key: %s", body)
	}
	attRaw, ok := raw.Messages[1]["attachments"]
	if !ok {
		t.Fatalf("assistant message missing \"attachments\": %s", body)
	}
	list, ok := attRaw.([]interface{})
	if !ok || len(list) != 1 {
		t.Fatalf("attachments = %v, want list of 1", attRaw)
	}
	entry := list[0].(map[string]interface{})
	for key, want := range map[string]string{
		"name":      "result.csv",
		"path":      "/home/u/.lele/tmp/attachments/ab12cd34_result.csv",
		"mime_type": "text/csv",
		"kind":      "file",
		"caption":   "report",
	} {
		if got := entry[key]; got != want {
			t.Errorf("attachments.%s = %v, want %q", key, got, want)
		}
	}
}
