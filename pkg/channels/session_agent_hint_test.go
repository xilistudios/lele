package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWSMessageRoutesAgentIDThroughHint(t *testing.T) {
	ts := newNativeTestServer(t)

	base := "native:" + ts.clientID + "-hint"
	client := &WSClient{
		ID:         "fake-client",
		SessionKey: base,
		ClientInfo: &ClientInfo{ClientID: ts.clientID},
		SendChan:   make(chan []byte, 16),
		done:       make(chan struct{}),
	}

	payload, err := json.Marshal(WSMessagePayload{
		Content:    "hola",
		SessionKey: base,
		AgentID:    "agent1",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ts.channel.handleWSClientMessage(client, payload, "evt-hint")
	}()

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer drainCancel()
	if _, ok := ts.bus.ConsumeInbound(drainCtx); !ok {
		t.Fatal("expected inbound message from handleWSClientMessage")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleWSClientMessage did not return")
	}

	hinted := ts.loop.HintedAgents()
	if len(hinted) != 1 {
		t.Fatalf("HintSessionAgent calls = %d, want 1 (agent_id must go through the hint)", len(hinted))
	}
	if hinted[0].agentID != "agent1" || hinted[0].sessionKey != base {
		t.Errorf("hint = (%q,%q), want (%q,%q)", hinted[0].sessionKey, hinted[0].agentID, base, "agent1")
	}

	if len(ts.loop.sessionModels) != 0 {
		t.Errorf("sessionModels = %v, want untouched by a routing hint", ts.loop.sessionModels)
	}
}

func TestRESTChatRoutesAgentIDThroughHint(t *testing.T) {
	ts := newNativeTestServer(t)

	base := "native:" + ts.clientID + "-rest-hint"
	body := `{"content":"hola","session_key":"` + base + `","agent_id":"agent1"}`
	req, err := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/send", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer drainCancel()
	if _, ok := ts.bus.ConsumeInbound(drainCtx); !ok {
		t.Fatal("expected inbound message from the REST chat handler")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("REST chat handler did not return")
	}

	hinted := ts.loop.HintedAgents()
	if len(hinted) != 1 {
		t.Fatalf("HintSessionAgent calls = %d, want 1 (agent_id must go through the hint)", len(hinted))
	}
	if hinted[0].agentID != "agent1" {
		t.Errorf("hint agent = %q, want %q", hinted[0].agentID, "agent1")
	}
	if len(ts.loop.sessionModels) != 0 {
		t.Errorf("sessionModels = %v, want untouched by a routing hint", ts.loop.sessionModels)
	}
}

func TestRESTAgentEndpointStillBindsExplicitly(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-explicit"
	body := `{"agent_id":"agent1"}`
	req, err := http.NewRequest(http.MethodPatch, ts.server.URL+"/api/v1/chat/sessions/"+sessionKey+"/agent", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("agent update handler did not return")
	}

	if got := ts.loop.GetSessionAgent(sessionKey); got != "agent1" {
		t.Errorf("agent after explicit bind = %q, want %q", got, "agent1")
	}
	if hinted := ts.loop.HintedAgents(); len(hinted) != 0 {
		t.Errorf("explicit agent bind used the hint path: %v", hinted)
	}
}
