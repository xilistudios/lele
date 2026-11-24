package channels

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
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/store"
)

func durableDir(ts *nativeTestServer) string {
	return durableAttachmentsDir(ts.channel.cfg.LeleDir)
}

func fetchHistory(t *testing.T, ts *nativeTestServer, sessionKey string) ChatHistoryResponse {
	t.Helper()
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
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("history status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var out ChatHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	return out
}

func newAttachmentSessionManager(t *testing.T) *session.SessionManager {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })

	sm := session.NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	return sm
}

func TestDispatchPersistsAttachmentAndServesIt(t *testing.T) {
	ts := newStagingTestServer(t)

	src := filepath.Join(t.TempDir(), "result.csv")
	if err := os.WriteFile(src, []byte("a,b\n1,2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	sessionKey := "native:" + ts.clientID + "-persist"
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
		MessageID:   "msg-persist-1",
		Attachments: out,
	})

	if out[0].Path != src {
		t.Errorf("dispatch mutated caller attachment path: %q, want %q", out[0].Path, src)
	}

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
	persistedPath, _ := payload.Attachments[0]["path"].(string)
	if persistedPath == "" || persistedPath == src {
		t.Fatalf("attachment path not persisted: %q", persistedPath)
	}
	if !strings.HasPrefix(persistedPath, durableDir(ts)) {
		t.Errorf("persisted path %q not under durable attachments dir %q", persistedPath, durableDir(ts))
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

	calls := ts.loop.attachmentCalls()
	if len(calls) != 1 {
		t.Fatalf("AddAttachmentMessage calls = %d, want 1", len(calls))
	}
	if calls[0].sessionKey != sessionKey {
		t.Errorf("persist sessionKey = %q, want %q", calls[0].sessionKey, sessionKey)
	}
	if calls[0].content != "done" {
		t.Errorf("persist content = %q, want done", calls[0].content)
	}
	if len(calls[0].attachments) != 1 || calls[0].attachments[0].Path != persistedPath {
		t.Errorf("persisted attachments = %+v, want persisted path %q", calls[0].attachments, persistedPath)
	}
	if calls[0].attachments[0].Name != "result.csv" {
		t.Errorf("persisted name = %q, want result.csv", calls[0].attachments[0].Name)
	}

	resp, body := getSecureView(t, ts, url.Values{
		"path": {persistedPath}, "download": {"1"}, "name": {"result.csv"},
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

func TestDispatchPersistsAttachmentRowInHistory(t *testing.T) {
	ts := newStagingTestServer(t)
	ts.loop.sessions = newAttachmentSessionManager(t)

	sessionKey := "native:" + ts.clientID + "-history"
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)
	ts.loop.sessions.AddMessage(sessionKey, "user", "send me the chart")
	ts.loop.sessions.AddFullMessage(sessionKey, providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{{
			ID:        "call_1",
			Type:      "function",
			Name:      "send_file",
			Arguments: map[string]interface{}{"path": "chart.png"},
		}},
	})

	client := registerFakeWSClient(t, ts, sessionKey)

	src := filepath.Join(t.TempDir(), "chart.png")
	if err := os.WriteFile(src, []byte("png-bytes"), 0644); err != nil {
		t.Fatal(err)
	}

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Content:   "here is the chart",
		MessageID: "msg-history-1",
		Attachments: []bus.FileAttachment{{
			Name: "chart.png", Path: src, MIMEType: "image/png", Kind: "image",
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
	livePath, _ := payload.Attachments[0]["path"].(string)

	history := fetchHistory(t, ts, sessionKey)

	var displayRow *ChatHistoryMessage
	var toolCallRow *ChatHistoryMessage
	for i := range history.Messages {
		msg := &history.Messages[i]
		if msg.Role == "tool" && strings.HasPrefix(msg.ToolCallID, "file:") {
			displayRow = msg
		}
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			toolCallRow = msg
		}
	}

	if displayRow == nil {
		t.Fatalf("history has no file: display row: %+v", history.Messages)
	}
	if len(displayRow.Attachments) != 1 {
		t.Fatalf("display row attachments = %+v, want 1", displayRow.Attachments)
	}
	if displayRow.Content != "here is the chart" {
		t.Errorf("display row content = %q, want 'here is the chart'", displayRow.Content)
	}
	if !displayRow.ExcludeFromContext {
		t.Error("display row leaks into the model context: exclude_from_context is false")
	}

	persistedPath := displayRow.Attachments[0].Path
	if persistedPath != livePath {
		t.Errorf("history path = %q, live path = %q: both must point at the same durable file", persistedPath, livePath)
	}
	if !strings.HasPrefix(persistedPath, durableDir(ts)+string(filepath.Separator)) {
		t.Errorf("history path %q not under durable attachments dir %q", persistedPath, durableDir(ts))
	}
	if info, err := os.Stat(persistedPath); err != nil || info.IsDir() {
		t.Fatalf("persisted attachment %q missing on disk (err=%v)", persistedPath, err)
	}
	if displayRow.Attachments[0].Kind != "image" || displayRow.Attachments[0].MIMEType != "image/png" {
		t.Errorf("display row attachment = %+v, want kind image and mime image/png", displayRow.Attachments[0])
	}

	if toolCallRow == nil {
		t.Fatalf("history lost the assistant tool_calls row: %+v", history.Messages)
	}
	if len(toolCallRow.Attachments) != 0 {
		t.Errorf("assistant tool_calls row carries %d attachments, want 0", len(toolCallRow.Attachments))
	}
}

func TestDispatchInsideLeleDirPersistedToDurableDir(t *testing.T) {
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
	got, _ := payload.Attachments[0]["path"].(string)
	if got == src {
		t.Errorf("path = %v, expected a durable copy (not the original)", got)
	}
	if !strings.HasPrefix(got, durableDir(ts)) {
		t.Errorf("path = %v, expected prefix %q", got, durableDir(ts))
	}
	entries, err := os.ReadDir(durableDir(ts))
	if err != nil || len(entries) == 0 {
		t.Errorf("expected durable copies, found %d entries (err=%v)", len(entries), err)
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
	if calls := ts.loop.attachmentCalls(); len(calls) != 0 {
		t.Errorf("AddAttachmentMessage called %d times for attachment-less message", len(calls))
	}
	if _, err := os.Stat(durableDir(ts)); !os.IsNotExist(err) {
		t.Errorf("durable attachments dir created without attachments (err=%v)", err)
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
		t.Fatalf("message must still be delivered even if persistence fails; events=%v", eventNames(events))
	}
	var payload WSMessageCompletePayload
	if err := json.Unmarshal(complete.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Attachments) != 1 {
		t.Fatalf("attachments = %v, want 1", payload.Attachments)
	}
	if got := payload.Attachments[0]["path"]; got != ghost {
		t.Errorf("path = %v, want original %q", got, ghost)
	}
}

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
