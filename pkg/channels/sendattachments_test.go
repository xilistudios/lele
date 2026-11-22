package channels

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/utils"
)

type sendVerdict struct {
	accepted    bool
	code        string
	content     string
	attachments []bus.FileAttachment
}

type sendFixture struct {
	leleDir     string
	validPath   string
	outsidePath string
}

func newSendFixture(t *testing.T) *sendFixture {
	t.Helper()

	leleDir := t.TempDir()
	uploads := filepath.Join(leleDir, "tmp", "uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatal(err)
	}

	validPath := filepath.Join(uploads, "abcd1234_note.txt")
	if err := os.WriteFile(validPath, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	outsidePath := filepath.Join(t.TempDir(), "elsewhere.txt")
	if err := os.WriteFile(outsidePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	return &sendFixture{leleDir: leleDir, validPath: validPath, outsidePath: outsidePath}
}

func newFixtureServer(t *testing.T, f *sendFixture) *nativeTestServer {
	t.Helper()
	ts := newNativeTestServer(t)
	ts.channel.cfg.LeleDir = f.leleDir
	return ts
}

func driveWSSend(t *testing.T, ts *nativeTestServer, content string, attachments []string) sendVerdict {
	t.Helper()

	sessionKey := "native:" + ts.clientID
	client := &WSClient{
		ID:         "fake-client",
		SessionKey: sessionKey,
		ClientInfo: &ClientInfo{ClientID: ts.clientID},
		SendChan:   make(chan []byte, 16),
		done:       make(chan struct{}),
	}

	payload, err := json.Marshal(WSMessagePayload{
		Content:     content,
		Attachments: attachments,
		SessionKey:  sessionKey,
	})
	if err != nil {
		t.Fatalf("Marshal(payload) error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ts.channel.handleWSClientMessage(client, payload, "evt-1")
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handleWSClientMessage did not return")
	}

	for {
		select {
		case raw := <-client.SendChan:
			var msg WSMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Fatalf("Unmarshal(envelope) error = %v", err)
			}
			if msg.Event != "error" {
				continue
			}
			var errPayload WSErrorPayload
			decodeWSData(t, msg.Data, &errPayload)
			return sendVerdict{code: errPayload.Code}
		default:
		}
		break
	}

	inbound, ok := waitInbound(t, ts.bus, 2*time.Second)
	if !ok {
		t.Fatal("handler neither published an inbound message nor reported an error")
	}
	return sendVerdict{accepted: true, content: inbound.Content, attachments: inbound.Attachments}
}

func driveRESTChatSend(t *testing.T, ts *nativeTestServer, content string, attachments []string) sendVerdict {
	t.Helper()

	body := mustMarshal(ChatSendRequest{Content: content, Attachments: attachments})
	req, err := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/send", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusBadRequest:
		var apiErr APIError
		if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
			t.Fatalf("Decode(error) error = %v", err)
		}
		return sendVerdict{code: apiErr.Code}
	case http.StatusCreated:
	default:
		t.Fatalf("status = %d, want 201 or 400", resp.StatusCode)
	}

	inbound, ok := waitInbound(t, ts.bus, 2*time.Second)
	if !ok {
		t.Fatal("accepted send did not publish an inbound message")
	}
	return sendVerdict{accepted: true, content: inbound.Content, attachments: inbound.Attachments}
}

func driveRESTChatSendStream(t *testing.T, ts *nativeTestServer, content string, attachments []string) sendVerdict {
	t.Helper()

	drained := make(chan bus.InboundMessage, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		inbound, ok := ts.bus.ConsumeInbound(ctx)
		if !ok {
			return
		}
		drained <- inbound
		ts.channel.Send(context.Background(), bus.OutboundMessage{
			Channel:   ChannelName,
			ChatID:    inbound.ChatID,
			Content:   "ok",
			MessageID: inbound.Metadata["message_id"],
		})
	}()

	body := mustMarshal(ChatSendRequest{Content: content, Attachments: attachments})
	req, err := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/send/stream", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusBadRequest:
		var apiErr APIError
		if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
			t.Fatalf("Decode(error) error = %v", err)
		}
		return sendVerdict{code: apiErr.Code}
	case http.StatusOK:
	default:
		t.Fatalf("status = %d, want 200 or 400", resp.StatusCode)
	}

	select {
	case inbound := <-drained:
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatalf("drain stream error = %v", err)
		}
		return sendVerdict{accepted: true, content: inbound.Content, attachments: inbound.Attachments}
	case <-time.After(5 * time.Second):
		t.Fatal("accepted stream send did not publish an inbound message")
	}
	return sendVerdict{}
}

func TestSendEndpoints_EmptyContentWithAttachments(t *testing.T) {
	endpoints := []struct {
		name  string
		drive func(t *testing.T, ts *nativeTestServer, content string, attachments []string) sendVerdict
	}{
		{"websocket", driveWSSend},
		{"rest_chat", driveRESTChatSend},
		{"rest_stream", driveRESTChatSendStream},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			cases := []struct {
				name        string
				content     string
				attachments func(f *sendFixture) []string
				wantAccept  bool
				wantCode    string
				wantContent string
			}{
				{
					name:       "a_empty_without_attachments",
					content:    "",
					wantAccept: false,
					wantCode:   "content_missing",
				},
				{
					name:    "b_empty_with_valid_attachment",
					content: "",
					attachments: func(f *sendFixture) []string {
						return []string{f.validPath}
					},
					wantAccept: true,
				},
				{
					name:    "c_whitespace_with_valid_attachment",
					content: "  ",
					attachments: func(f *sendFixture) []string {
						return []string{f.validPath}
					},
					wantAccept:  true,
					wantContent: "  ",
				},
				{
					name:        "d_text_without_attachments",
					content:     "hola",
					wantAccept:  true,
					wantContent: "hola",
				},
				{
					name:        "f_whitespace_without_attachments",
					content:     "   ",
					wantAccept:  true,
					wantContent: "   ",
				},
				{
					name:    "e_empty_with_rejected_attachment",
					content: "",
					attachments: func(f *sendFixture) []string {
						return []string{f.outsidePath}
					},
					wantAccept: false,
					wantCode:   "attachments_rejected",
				},
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					f := newSendFixture(t)
					ts := newFixtureServer(t, f)

					var attachments []string
					if tc.attachments != nil {
						attachments = tc.attachments(f)
					}

					got := ep.drive(t, ts, tc.content, attachments)

					if got.accepted != tc.wantAccept {
						t.Fatalf("accepted = %v, want %v (code %q)", got.accepted, tc.wantAccept, got.code)
					}
					if !tc.wantAccept {
						if got.code != tc.wantCode {
							t.Fatalf("error code = %q, want %q", got.code, tc.wantCode)
						}
						return
					}

					if tc.wantContent != "" && got.content != tc.wantContent {
						t.Errorf("content = %q, want %q", got.content, tc.wantContent)
					}
					if tc.wantContent == "" && got.content != "" {
						t.Errorf("content = %q, want empty (no server-side trim/injection)", got.content)
					}
					if len(attachments) > 0 && len(got.attachments) != 1 {
						t.Fatalf("attachments = %d, want 1 (the valid upload)", len(got.attachments))
					}
					if len(got.attachments) == 1 {
						rendered := utils.BuildAttachmentContext(got.attachments)
						if !strings.HasPrefix(rendered, "## Attachments") {
							t.Errorf("rendered attachment context = %q, want it to start with the marker", rendered)
						}
					}
				})
			}
		})
	}
}
