package channels

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/tencent-connect/botgo/dto"
	"github.com/tencent-connect/botgo/openapi"
	"github.com/tencent-connect/botgo/openapi/options"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// stubOpenAPI is a minimal openapi.OpenAPI implementation used to exercise
// QQChannel.Send without hitting the network. Only PostC2CMessage is needed
// by the paths under test; the embedded openapi.OpenAPI covers the remaining
// methods with a nil pointer.
type stubOpenAPI struct {
	openapi.OpenAPI
	c2cErr error
	calls  int
}

// qqID builds a deterministic unique message id string.
func qqID(i int) string {
	return "msg-" + strconv.Itoa(i)
}

func (s *stubOpenAPI) PostC2CMessage(_ context.Context, userID string, _ dto.APIMessage, _ ...options.Option) (*dto.Message, error) {
	s.calls++
	return &dto.Message{ID: "fake"}, s.c2cErr
}

func TestQQ_Send_Success(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewQQChannel(config.QQConfig{AllowFrom: []string{"*"}}, mb)
	ch.setRunning(true)
	stub := &stubOpenAPI{}
	ch.api = stub

	if err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "u1", Content: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if stub.calls != 1 {
		t.Errorf("PostC2CMessage calls = %d, want 1", stub.calls)
	}
}

func TestQQ_Send_APIError(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewQQChannel(config.QQConfig{}, mb)
	ch.setRunning(true)
	ch.api = &stubOpenAPI{c2cErr: errors.New("boom")}

	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "u1", Content: "hi"})
	if err == nil {
		t.Fatal("expected error from PostC2CMessage")
	}
}

func TestQQ_Start_InvalidCredentials(t *testing.T) {
	// app_secret empty is the branch not exercised by the existing test.
	ch, _ := NewQQChannel(config.QQConfig{AppID: "appid", AppSecret: ""}, bus.NewMessageBus())
	if err := ch.Start(context.Background()); err == nil {
		t.Fatal("expected error when app_secret is empty")
	}

	// app_id empty.
	ch2, _ := NewQQChannel(config.QQConfig{AppID: "", AppSecret: "sec"}, bus.NewMessageBus())
	if err := ch2.Start(context.Background()); err == nil {
		t.Fatal("expected error when app_id is empty")
	}
}

func TestQQ_Stop_CancelsContext(t *testing.T) {
	ch, _ := NewQQChannel(config.QQConfig{}, bus.NewMessageBus())
	ctx, cancel := context.WithCancel(context.Background())
	ch.ctx = ctx
	ch.cancel = cancel
	ch.setRunning(true)

	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ch.IsRunning() {
		t.Error("should not be running after Stop")
	}
	if ch.cancel == nil {
		t.Error("cancel should be released")
	}
}

func TestQQ_IsDuplicate_Eviction(t *testing.T) {
	// Exercise the ring-buffer style eviction branch: fill the map past the
	// hardcoded 10000 cap so the cleanup deletes up to 5000 entries.
	ch, _ := NewQQChannel(config.QQConfig{}, bus.NewMessageBus())

	for i := 0; i <= 10000; i++ {
		ch.isDuplicate(qqID(i))
	}

	// Eviction deletes up to 5000 entries, so 10001 total leaves 5001.
	if len(ch.processedIDs) != 5001 {
		t.Errorf("processedIDs len = %d, want 5001", len(ch.processedIDs))
	}

	// Fresh ids beyond the fill range remain unique after eviction.
	for i := 0; i < 10; i++ {
		if ch.isDuplicate(qqID(20000 + i)) {
			t.Errorf("id %d should be unique after eviction pass", i)
		}
	}
}