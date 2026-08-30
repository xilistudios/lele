package channels

// Tests for message.ack session_key symmetry.
//
// Bug: the backend emitted message.ack with the client-provided (base)
// session_key, while every other event (message.stream, message.complete,
// history.updated) is emitted with ResolveSessionKey(chatID) — after /new or
// /agent an alias base -> base:chat:N exists. The frontend registered its
// processing state under the base key while completion arrived under the alias
// (and vice versa on cleanup), leaving the loading spinner stuck.
//
// These tests pin the contract: message.ack (WebSocket and SSE) must report
// the RESOLVED key that subsequent events will be emitted with.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/bus"
)

// setLoopAlias registers a base -> resolved session-key alias on the test
// agent loop stub (mirrors agent.AgentLoop.sessionAliases).
func setLoopAlias(loop *nativeTestAgentLoop, base, resolved string) {
	loop.sessionAliasesMu.Lock()
	defer loop.sessionAliasesMu.Unlock()
	loop.sessionAliases[base] = resolved
}

// TestWSMessageAckCarriesResolvedSessionKey drives handleWSClientMessage directly
// with a fake WSClient and asserts the ack payload contains the alias-resolved
// session key (base -> base:chat:3), not the base key the client sent.
func TestWSMessageAckCarriesResolvedSessionKey(t *testing.T) {
	ts := newNativeTestServer(t)

	base := "native:" + ts.clientID + "-ack"
	alias := base + ":chat:3"
	setLoopAlias(ts.loop, base, alias)

	// Fake client: no real connection needed — QueueSend only touches the
	// buffered SendChan as long as the client is not marked reconnecting.
	client := &WSClient{
		ID:         "fake-client",
		SessionKey: base,
		ClientInfo: &ClientInfo{ClientID: ts.clientID},
		SendChan:   make(chan []byte, 16),
	}

	payload, err := json.Marshal(WSMessagePayload{Content: "hola", SessionKey: base})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ts.channel.handleWSClientMessage(client, payload, "evt-1")
	}()

	// Drain the inbound bus message so the handler never blocks.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer drainCancel()
	inbound, ok := ts.bus.ConsumeInbound(drainCtx)
	if !ok {
		t.Fatal("expected inbound message from handleWSClientMessage")
	}
	// Inbound keeps the client-provided key (resolution happens at emit time).
	if inbound.ChatID != base {
		t.Errorf("inbound chat_id = %q, want base %q", inbound.ChatID, base)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleWSClientMessage did not return")
	}

	var ack WSMessage
	select {
	case raw := <-client.SendChan:
		if err := json.Unmarshal(raw, &ack); err != nil {
			t.Fatalf("Unmarshal(ack envelope) error = %v", err)
		}
	default:
		t.Fatal("expected message.ack to be queued on the client")
	}

	if ack.Event != "message.ack" {
		t.Fatalf("ack event = %q, want message.ack", ack.Event)
	}
	var ackData struct {
		MessageID  string `json:"message_id"`
		SessionKey string `json:"session_key"`
	}
	decodeWSData(t, ack.Data, &ackData)
	if ackData.SessionKey != alias {
		t.Errorf("ack session_key = %q, want resolved alias %q", ackData.SessionKey, alias)
	}
	if ackData.MessageID == "" {
		t.Error("ack message_id is empty")
	}
}

// TestWSMessageAckWithoutAliasKeepsSessionKey ensures the nil-safe / no-alias
// path: when no alias exists the ack carries the original session key.
func TestWSMessageAckWithoutAliasKeepsSessionKey(t *testing.T) {
	ts := newNativeTestServer(t)

	base := "native:" + ts.clientID + "-noalias"

	client := &WSClient{
		ID:         "fake-client",
		SessionKey: base,
		ClientInfo: &ClientInfo{ClientID: ts.clientID},
		SendChan:   make(chan []byte, 16),
	}

	payload, err := json.Marshal(WSMessagePayload{Content: "hola", SessionKey: base})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, ok := ts.bus.ConsumeInbound(ctx); !ok {
			return
		}
	}()

	ts.channel.handleWSClientMessage(client, payload, "evt-1")

	var ack WSMessage
	select {
	case raw := <-client.SendChan:
		if err := json.Unmarshal(raw, &ack); err != nil {
			t.Fatalf("Unmarshal(ack envelope) error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message.ack")
	}

	if ack.Event != "message.ack" {
		t.Fatalf("ack event = %q, want message.ack", ack.Event)
	}
	var ackData struct {
		SessionKey string `json:"session_key"`
	}
	decodeWSData(t, ack.Data, &ackData)
	if ackData.SessionKey != base {
		t.Errorf("ack session_key = %q, want unchanged %q", ackData.SessionKey, base)
	}
}

// TestNativeRESTChatSendStreamAckCarriesResolvedSessionKey is the SSE mirror of
// the WebSocket fix: with an alias base -> base:chat:3, the message.ack event
// must report the resolved key AND subsequent events emitted under the alias
// (message.stream / message.complete / history.updated) must be delivered to
// the stream subscriber registered before resolution.
func TestNativeRESTChatSendStreamAckCarriesResolvedSessionKey(t *testing.T) {
	ts := newNativeTestServer(t)

	base := "native:" + ts.clientID
	alias := base + ":chat:3"
	setLoopAlias(ts.loop, base, alias)

	// Reply to the inbound message using the resolved key, exactly like the
	// agent loop does (PublishInbound carries the base chatID; the loop
	// resolves it before emitting outbound events).
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		inbound, ok := ts.bus.ConsumeInbound(ctx)
		if !ok {
			return
		}
		messageID := inbound.Metadata["message_id"]
		resolved := ts.loop.ResolveSessionKey(inbound.ChatID)
		ts.channel.Send(context.Background(), bus.OutboundMessage{
			Channel:   ChannelName,
			ChatID:    resolved,
			Event:     "message.stream",
			Content:   "hola",
			MessageID: messageID,
			Metadata:  map[string]string{"done": "true"},
		})
		ts.channel.Send(context.Background(), bus.OutboundMessage{
			Channel:   ChannelName,
			ChatID:    resolved,
			Content:   "hola",
			MessageID: messageID,
		})
	}()

	body, err := json.Marshal(ChatSendRequest{Content: "hola", SessionKey: base})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/send/stream", bytes.NewReader(body))
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
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, payload)
	}

	// Parse SSE frames event-by-event so we can assert ordering: the ack must
	// arrive first and already carry the resolved key.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	eventName := ""
	ackSeen := false
	streamSeenUnderAlias := false

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventName = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data := strings.TrimPrefix(line, "data: ")
			switch eventName {
			case "message.ack":
				var ack ChatSendResponse
				if err := json.Unmarshal([]byte(data), &ack); err != nil {
					t.Fatalf("Unmarshal(ack) error = %v", err)
				}
				if ack.SessionKey != alias {
					t.Errorf("SSE ack session_key = %q, want resolved alias %q", ack.SessionKey, alias)
				}
				ackSeen = true
			case "message.complete":
				var complete WSMessageCompletePayload
				if err := json.Unmarshal([]byte(data), &complete); err != nil {
					t.Fatalf("Unmarshal(complete) error = %v", err)
				}
				if complete.SessionKey == alias {
					streamSeenUnderAlias = true
				}
			}
			eventName = ""
		}
		if ackSeen && streamSeenUnderAlias {
			break
		}
	}
	wg.Wait()

	if !ackSeen {
		t.Fatal("SSE stream did not contain message.ack")
	}
	if !streamSeenUnderAlias {
		t.Fatal("SSE stream did not deliver message.complete emitted under the alias key")
	}
}

// TestNativeRESTChatSendStreamAckWithoutAlias keeps symmetry for the plain
// case: no alias registered -> ack carries the original key.
func TestNativeRESTChatSendStreamAckWithoutAlias(t *testing.T) {
	ts := newNativeTestServer(t)

	base := "native:" + ts.clientID

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		inbound, ok := ts.bus.ConsumeInbound(ctx)
		if !ok {
			return
		}
		messageID := inbound.Metadata["message_id"]
		ts.channel.Send(context.Background(), bus.OutboundMessage{
			Channel:   ChannelName,
			ChatID:    inbound.ChatID,
			Content:   "ok",
			MessageID: messageID,
		})
	}()

	body, err := json.Marshal(ChatSendRequest{Content: "hola", SessionKey: base})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/send/stream", bytes.NewReader(body))
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

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	stream := string(payload)
	if !strings.Contains(stream, "event: message.ack") {
		t.Fatalf("SSE stream missing message.ack:\n%s", stream)
	}
	if !strings.Contains(stream, `"session_key":"`+base+`"`) {
		t.Fatalf("SSE ack missing unchanged session_key %q:\n%s", base, stream)
	}
}

// TestWSMessageAckEndToEndViaWebSocket exercises the real WS path: dial, send
// a message with the base session key, and check the ack reports the alias.
func TestWSMessageAckEndToEndViaWebSocket(t *testing.T) {
	ts := newNativeTestServer(t)

	base := "native:" + ts.clientID
	alias := base + ":chat:3"
	setLoopAlias(ts.loop, base, alias)

	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws?token=" + url.QueryEscape(ts.token) + "&session_key=" + url.QueryEscape(base)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	welcome := readWSMessage(t, conn)
	if welcome.Event != "welcome" {
		t.Fatalf("first event = %q, want welcome", welcome.Event)
	}

	// Drain the inbound bus in background (the fake agent does not reply).
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = ts.bus.ConsumeInbound(ctx)
	}()

	if err := conn.WriteJSON(map[string]interface{}{
		"event": "message",
		"data": map[string]interface{}{
			"content":     "hola",
			"session_key": base,
		},
	}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	ack := readWSMessage(t, conn)
	if ack.Event != "message.ack" {
		t.Fatalf("ack event = %q, want message.ack", ack.Event)
	}
	var ackData struct {
		SessionKey string `json:"session_key"`
	}
	decodeWSData(t, ack.Data, &ackData)
	if ackData.SessionKey != alias {
		t.Errorf("WS ack session_key = %q, want resolved alias %q", ackData.SessionKey, alias)
	}
}
