package channels

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/mymmrac/telego"
)

type botAPIRecorder struct {
	mu    sync.Mutex
	calls []botAPICall
}

type botAPICall struct {
	method string
	body   string
}

func (r *botAPIRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/getUpdates") {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}

	var body string
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		body = string(raw)
	}

	r.mu.Lock()
	r.calls = append(r.calls, botAPICall{method: req.URL.Path, body: body})
	r.mu.Unlock()

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)),
		Request:    req,
	}, nil
}

func (r *botAPIRecorder) reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}

func (r *botAPIRecorder) snapshot() []botAPICall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]botAPICall(nil), r.calls...)
}

func telegramCallbackUpdate(senderID int64, username, data string) telego.Update {
	return telego.Update{
		UpdateID: 1,
		CallbackQuery: &telego.CallbackQuery{
			ID:      "callback-1",
			From:    telego.User{ID: senderID, Username: username},
			Data:    data,
			Message: &telego.Message{MessageID: 21, Chat: telego.Chat{ID: 555, Type: "group"}},
		},
	}
}

func TestSetupBotHandlerDispatchesGuardedCallbacks(t *testing.T) {
	recorder := &botAPIRecorder{}
	channel := newTelegramTestChannel(t, []string{"42"}, recorder)

	bh, cancel, err := channel.setupBotHandler(context.Background())
	if err != nil {
		t.Fatalf("setupBotHandler() error = %v", err)
	}
	defer cancel()

	recorder.reset()
	if err := bh.BaseGroup().HandleUpdate(context.Background(), channel.bot, telegramCallbackUpdate(99, "mallory", "think:set:high")); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	calls := recorder.snapshot()
	if len(calls) != 1 {
		t.Fatalf("non-allowlisted callback produced %d bot API calls, want 1 (the ack): %#v", len(calls), calls)
	}
	if !strings.HasSuffix(calls[0].method, "/answerCallbackQuery") {
		t.Fatalf("bot API call = %q, want answerCallbackQuery", calls[0].method)
	}
	if !strings.Contains(calls[0].body, "Not authorized") {
		t.Fatalf("callback ack body = %q, want the allowlist rejection", calls[0].body)
	}

	recorder.reset()
	if err := bh.BaseGroup().HandleUpdate(context.Background(), channel.bot, telegramCallbackUpdate(42, "alice", "think:set:high")); err != nil {
		t.Fatalf("HandleUpdate() error = %v", err)
	}

	allowedCalls := recorder.snapshot()
	if len(allowedCalls) != 1 {
		t.Fatalf("allowlisted callback produced %d bot API calls, want 1: %#v", len(allowedCalls), allowedCalls)
	}
	if strings.Contains(allowedCalls[0].body, "Not authorized") {
		t.Fatalf("allowlisted sender was rejected by the guard: %q", allowedCalls[0].body)
	}
	if !strings.Contains(allowedCalls[0].body, "Agent management not available") {
		t.Fatalf("allowlisted callback did not reach the think handler: %q", allowedCalls[0].body)
	}
}
