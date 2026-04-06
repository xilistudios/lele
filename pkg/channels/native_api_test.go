package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

type nativeTestAgentLoop struct {
	config           *config.Config
	histories        map[string][]providers.Message
	sessionAgents    map[string]string
	sessionModels    map[string]string
	sessionAliases   map[string]string // base -> resolved
	sessionAliasesMu sync.RWMutex
}

func newNativeTestAgentLoop(cfg *config.Config) *nativeTestAgentLoop {
	return &nativeTestAgentLoop{
		config:         cfg,
		histories:      make(map[string][]providers.Message),
		sessionAgents:  make(map[string]string),
		sessionModels:  make(map[string]string),
		sessionAliases: make(map[string]string),
	}
}

func (m *nativeTestAgentLoop) GetSessionAgent(sessionKey string) string {
	if agentID, ok := m.sessionAgents[sessionKey]; ok {
		return agentID
	}
	return "main"
}

func (m *nativeTestAgentLoop) SetSessionAgent(sessionKey, agentID string) {
	m.sessionAgents[sessionKey] = agentID
}

func (m *nativeTestAgentLoop) ListAvailableAgentIDs() []string {
	return []string{"main"}
}

func (m *nativeTestAgentLoop) GetDefaultAgentID() string {
	return "main"
}

func (m *nativeTestAgentLoop) GetAgentInfo(agentID string) (AgentBasicInfo, bool) {
	if agentID != "main" {
		return AgentBasicInfo{}, false
	}
	return AgentBasicInfo{
		ID:        "main",
		Name:      "Main Agent",
		Workspace: "/tmp/workspace",
		Model:     "gpt-4",
	}, true
}

func (m *nativeTestAgentLoop) GetSessionHistory(sessionKey string) []providers.Message {
	history := m.histories[sessionKey]
	result := make([]providers.Message, len(history))
	copy(result, history)
	return result
}

func (m *nativeTestAgentLoop) GetSessionModel(sessionKey string) string {
	if model, ok := m.sessionModels[sessionKey]; ok {
		return model
	}
	return "gpt-4"
}

func (m *nativeTestAgentLoop) SetSessionModel(sessionKey, model string) string {
	m.sessionModels[sessionKey] = model
	return model
}

func (m *nativeTestAgentLoop) ListAvailableModels(agentID string) []string {
	if agentID == "research" {
		return []string{"gpt-4.1", "gpt-4.1-mini"}
	}
	return []string{"gpt-4", "gpt-4o-mini"}
}

func (m *nativeTestAgentLoop) GetConfigSnapshot() *config.Config {
	return m.config
}

func (m *nativeTestAgentLoop) GetStatus(sessionKey string) string {
	return "idle"
}

func (m *nativeTestAgentLoop) StopAgent(sessionKey string) string {
	return "stopped"
}

func (m *nativeTestAgentLoop) CompactSession(sessionKey string) string {
	return "compacted"
}

func (m *nativeTestAgentLoop) ToggleVerbose(sessionKey string) string {
	return "verbose"
}

func (m *nativeTestAgentLoop) GetVerboseLevel(sessionKey string) string {
	return "off"
}

func (m *nativeTestAgentLoop) SetVerboseLevel(sessionKey string, level string) bool {
	return true
}

func (m *nativeTestAgentLoop) GetThinkLevel(sessionKey string) string {
	return "default"
}

func (m *nativeTestAgentLoop) SetThinkLevel(sessionKey string, level string) bool {
	return true
}

func (m *nativeTestAgentLoop) GetSubagents() string {
	return ""
}

func (m *nativeTestAgentLoop) ClearSession(sessionKey string) string {
	return "cleared"
}

func (m *nativeTestAgentLoop) GetName(sessionKey string) string {
	return ""
}

func (m *nativeTestAgentLoop) SetName(sessionKey string, name string) error {
	return nil
}

func (m *nativeTestAgentLoop) ResolveSessionKey(sessionKey string) string {
	m.sessionAliasesMu.RLock()
	defer m.sessionAliasesMu.RUnlock()
	if resolved, ok := m.sessionAliases[sessionKey]; ok {
		return resolved
	}
	return sessionKey
}

type nativeTestServer struct {
	channel  *NativeChannel
	loop     *nativeTestAgentLoop
	bus      *bus.MessageBus
	server   *httptest.Server
	token    string
	clientID string
	cancel   context.CancelFunc
}

func newNativeTestServer(t *testing.T) *nativeTestServer {
	t.Helper()

	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 18793
	cfg.Channels.Native.TokenExpiryDays = 30
	cfg.Channels.Native.PinExpiryMinutes = 5
	cfg.Channels.Native.MaxClients = 5
	cfg.Channels.Native.SessionExpiryDays = 30

	// Configure test providers for model endpoint tests
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type: "openai",
			Models: map[string]config.ProviderModelConfig{
				"gpt-4":       {Model: "gpt-4"},
				"gpt-4o":      {Model: "gpt-4o"},
				"gpt-4o-mini": {Model: "gpt-4o-mini"},
			},
		},
		"anthropic": {
			Type: "anthropic",
			Models: map[string]config.ProviderModelConfig{
				"claude-sonnet": {Model: "claude-3-5-sonnet"},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	loop := newNativeTestAgentLoop(cfg)
	auth, err := NewAuthManager(&cfg.Channels.Native, t.TempDir())
	if err != nil {
		t.Fatalf("NewAuthManager() error = %v", err)
	}

	channel := &NativeChannel{
		cfg:       &cfg.Channels.Native,
		auth:      auth,
		bus:       msgBus,
		agentLoop: loop,
		wsClients: make(map[string]*WSClient),
	}

	mux := http.NewServeMux()
	channel.registerRoutes(mux)
	server := httptest.NewServer(channel.corsMiddleware(channel.authMiddleware(mux)))

	ctx, cancel := context.WithCancel(context.Background())
	go channel.listenForOutbound(ctx)

	t.Cleanup(func() {
		cancel()
		server.Close()
	})

	pending, err := auth.GeneratePIN("Test Desktop")
	if err != nil {
		t.Fatalf("GeneratePIN() error = %v", err)
	}

	client, token, _, err := auth.PairWithPIN(pending.PIN, "Test Desktop")
	if err != nil {
		t.Fatalf("PairWithPIN() error = %v", err)
	}

	return &nativeTestServer{
		channel:  channel,
		loop:     loop,
		bus:      msgBus,
		server:   server,
		token:    token,
		clientID: client.ClientID,
		cancel:   cancel,
	}
}

func TestNativeChannelAuthStatusInvalidTokenReturnsValidFalse(t *testing.T) {
	ts := newNativeTestServer(t)

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/auth/status", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer invalid-token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload AuthStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if payload.Valid {
		t.Fatal("expected valid=false for invalid token")
	}
	if payload.ClientID != "" {
		t.Fatalf("client_id = %q, want empty", payload.ClientID)
	}
}

func TestNativeChannelChatHistoryReturnsPersistedMessages(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID
	ts.loop.histories[sessionKey] = []providers.Message{
		{Role: "system", Content: "hidden"},
		{Role: "user", Content: "Hello"},
		{
			Role:    "assistant",
			Content: "Hi there!",
			ToolCalls: []providers.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Name: "search_docs",
				Arguments: map[string]interface{}{
					"query": "lele",
				},
			}},
		},
		{Role: "tool", Content: "result", ToolCallID: "call-1"},
	}

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/history?session_key="+url.QueryEscape(sessionKey), nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ChatHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if payload.SessionKey != sessionKey {
		t.Fatalf("session_key = %q, want %q", payload.SessionKey, sessionKey)
	}
	if len(payload.Messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(payload.Messages))
	}
	if payload.Messages[0].Role != "user" || payload.Messages[0].Content != "Hello" {
		t.Fatalf("first message = %#v, want user Hello", payload.Messages[0])
	}
	if payload.Messages[1].Role != "assistant" || payload.Messages[1].Content != "Hi there!" {
		t.Fatalf("second message = %#v, want assistant Hi there!", payload.Messages[1])
	}
	if len(payload.Messages[1].ToolCalls) != 1 {
		t.Fatalf("assistant tool_calls = %#v, want 1 call", payload.Messages[1].ToolCalls)
	}
	if payload.Messages[1].ToolCalls[0].ID != "call-1" || payload.Messages[1].ToolCalls[0].Name != "search_docs" {
		t.Fatalf("assistant tool call = %#v, want call-1/search_docs", payload.Messages[1].ToolCalls[0])
	}
	if payload.Messages[2].Role != "tool" || payload.Messages[2].Content != "result" {
		t.Fatalf("third message = %#v, want tool result", payload.Messages[2])
	}
	if payload.Messages[2].ToolCallID != "call-1" {
		t.Fatalf("tool_call_id = %#v, want call-1", payload.Messages[2].ToolCallID)
	}
}

func TestNativeChannelChatSessionsReturnsTrackedSessionKeys(t *testing.T) {
	ts := newNativeTestServer(t)
	trackedSession := "native:" + ts.clientID + ":secondary"
	ts.channel.auth.TrackSessionKey(ts.clientID, trackedSession)
	ts.loop.histories[trackedSession] = []providers.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi"},
	}

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ChatSessionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if len(payload.Sessions) < 2 {
		t.Fatalf("len(sessions) = %d, want at least 2", len(payload.Sessions))
	}

	var found bool
	for _, session := range payload.Sessions {
		if session.Key == trackedSession {
			found = true
			if session.MessageCount != 2 {
				t.Fatalf("message_count = %d, want 2", session.MessageCount)
			}
		}
	}

	if !found {
		t.Fatalf("expected session %q in payload %#v", trackedSession, payload.Sessions)
	}
}

func TestNativeChannelSessionModelEndpoints(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID

	getReq, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/session/"+url.PathEscape(sessionKey)+"?action=model", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	getReq.Header.Set("Authorization", "Bearer "+ts.token)

	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", getResp.StatusCode, http.StatusOK)
	}

	var getPayload SessionModelResponse
	if err := json.NewDecoder(getResp.Body).Decode(&getPayload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if getPayload.Model != "gpt-4" {
		t.Fatalf("model = %q, want gpt-4", getPayload.Model)
	}
	if len(getPayload.Models) == 0 {
		t.Fatal("expected available models")
	}
	if len(getPayload.ModelGroups) == 0 {
		t.Fatal("expected grouped models")
	}
	if getPayload.ModelGroups[0].Provider == "" {
		t.Fatalf("unexpected provider group = %q", getPayload.ModelGroups[0].Provider)
	}
	if len(getPayload.ModelGroups[0].Models) == 0 {
		t.Fatal("expected models in first group")
	}

	body := strings.NewReader(`{"model":"gpt-4o-mini"}`)
	patchReq, err := http.NewRequest(http.MethodPatch, ts.server.URL+"/api/v1/chat/session/"+url.PathEscape(sessionKey)+"?action=model", body)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	patchReq.Header.Set("Authorization", "Bearer "+ts.token)
	patchReq.Header.Set("Content-Type", "application/json")

	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer patchResp.Body.Close()

	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", patchResp.StatusCode, http.StatusOK)
	}

	var patchPayload SessionModelResponse
	if err := json.NewDecoder(patchResp.Body).Decode(&patchPayload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if patchPayload.Model != "gpt-4o-mini" {
		t.Fatalf("model = %q, want gpt-4o-mini", patchPayload.Model)
	}
}

func TestNativeChannelConfigUsesAgentSnapshot(t *testing.T) {
	ts := newNativeTestServer(t)

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/config", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload struct {
		Config map[string]map[string]map[string]interface{} `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	nativeCfg := payload.Config["channels"]["native"]
	if nativeCfg["host"] != ts.loop.config.Channels.Native.Host {
		t.Fatalf("host = %v, want %q", nativeCfg["host"], ts.loop.config.Channels.Native.Host)
	}
	if nativeCfg["session_expiry_days"] != float64(ts.loop.config.Channels.Native.SessionExpiryDays) {
		t.Fatalf("session_expiry_days = %v, want %d", nativeCfg["session_expiry_days"], ts.loop.config.Channels.Native.SessionExpiryDays)
	}
}

func TestNativeChannelWebSocketSupportsQueryTokenAndStructuredEvents(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID

	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws?token=" + url.QueryEscape(ts.token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	welcome := readWSMessage(t, conn)
	if welcome.Event != "welcome" {
		t.Fatalf("first event = %q, want welcome", welcome.Event)
	}

	if err := conn.WriteJSON(map[string]interface{}{
		"event": "message",
		"data": map[string]interface{}{
			"content": "Hello from socket",
		},
	}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	ack := readWSMessage(t, conn)
	if ack.Event != "message.ack" {
		t.Fatalf("ack event = %q, want message.ack", ack.Event)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	inbound, ok := ts.bus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("expected inbound message from websocket send")
	}
	if inbound.ChatID != sessionKey {
		t.Fatalf("inbound chat_id = %q, want %q", inbound.ChatID, sessionKey)
	}

	ts.bus.PublishOutbound(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Content:   "Hello back",
		MessageID: "msg-1",
	})

	stream := readWSMessage(t, conn)
	if stream.Event != "message.stream" {
		t.Fatalf("stream event = %q, want message.stream", stream.Event)
	}
	var streamPayload WSStreamPayload
	decodeWSData(t, stream.Data, &streamPayload)
	if streamPayload.MessageID != "msg-1" || streamPayload.SessionKey != sessionKey || streamPayload.Chunk != "Hello back" || !streamPayload.Done {
		t.Fatalf("stream payload = %#v, want msg-1/Hello back/done", streamPayload)
	}

	complete := readWSMessage(t, conn)
	if complete.Event != "message.complete" {
		t.Fatalf("complete event = %q, want message.complete", complete.Event)
	}
	var completePayload WSMessageCompletePayload
	decodeWSData(t, complete.Data, &completePayload)
	if completePayload.MessageID != "msg-1" || completePayload.SessionKey != sessionKey || completePayload.Content != "Hello back" {
		t.Fatalf("complete payload = %#v, want msg-1/Hello back", completePayload)
	}

	ts.bus.PublishOutbound(bus.OutboundMessage{
		Channel: ChannelName,
		ChatID:  sessionKey,
		Event:   "tool.executing",
		Metadata: map[string]string{
			"tool":   "read_file",
			"action": "Executing read_file",
		},
	})

	toolExecuting := readWSMessage(t, conn)
	if toolExecuting.Event != "tool.executing" {
		t.Fatalf("tool executing event = %q, want tool.executing", toolExecuting.Event)
	}
	var toolExecutingPayload WSToolExecutingPayload
	decodeWSData(t, toolExecuting.Data, &toolExecutingPayload)
	if toolExecutingPayload.Tool != "read_file" {
		t.Fatalf("tool = %q, want read_file", toolExecutingPayload.Tool)
	}

	ts.bus.PublishOutbound(bus.OutboundMessage{
		Channel: ChannelName,
		ChatID:  sessionKey,
		Event:   "tool.result",
		Metadata: map[string]string{
			"tool":   "read_file",
			"result": "file contents",
		},
	})

	toolResult := readWSMessage(t, conn)
	if toolResult.Event != "tool.result" {
		t.Fatalf("tool result event = %q, want tool.result", toolResult.Event)
	}
	var toolResultPayload WSToolResultPayload
	decodeWSData(t, toolResult.Data, &toolResultPayload)
	if toolResultPayload.Tool != "read_file" || toolResultPayload.Result != "file contents" {
		t.Fatalf("tool result payload = %#v, want read_file/file contents", toolResultPayload)
	}

	ts.bus.PublishOutbound(bus.OutboundMessage{
		Channel: ChannelName,
		ChatID:  sessionKey,
		Event:   "approval.request",
		Metadata: map[string]string{
			"id":      "approval-1",
			"command": "rm -rf /tmp/test",
			"reason":  "Dangerous command requires approval",
		},
	})

	approval := readWSMessage(t, conn)
	if approval.Event != "approval.request" {
		t.Fatalf("approval event = %q, want approval.request", approval.Event)
	}
	var approvalPayload WSApprovalRequestPayload
	decodeWSData(t, approval.Data, &approvalPayload)
	if approvalPayload.ID != "approval-1" || approvalPayload.Command != "rm -rf /tmp/test" {
		t.Fatalf("approval payload = %#v, want approval-1/rm -rf /tmp/test", approvalPayload)
	}
}

type wsEnvelope struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

func readWSMessage(t *testing.T, conn *websocket.Conn) wsEnvelope {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}

	var msg wsEnvelope
	if err := json.Unmarshal(payload, &msg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return msg
}

func decodeWSData(t *testing.T, data json.RawMessage, target interface{}) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("Unmarshal(data) error = %v", err)
	}
}

func TestNativeChannelWebSocketBroadcastsToBaseSessionKeyWhenAliased(t *testing.T) {
	ts := newNativeTestServer(t)
	baseSessionKey := "native:" + ts.clientID
	aliasedSessionKey := baseSessionKey + ":chat:1"

	// Set up alias: base -> aliased
	ts.loop.sessionAliasesMu.Lock()
	ts.loop.sessionAliases[baseSessionKey] = aliasedSessionKey
	ts.loop.sessionAliasesMu.Unlock()

	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws?token=" + url.QueryEscape(ts.token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	// Read welcome
	welcome := readWSMessage(t, conn)
	if welcome.Event != "welcome" {
		t.Fatalf("first event = %q, want welcome", welcome.Event)
	}

	// Subscribe to base session key
	if err := conn.WriteJSON(map[string]interface{}{
		"event": "subscribe",
		"data": map[string]interface{}{
			"session_key": baseSessionKey,
		},
	}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	// Read subscribe.ack
	ack := readWSMessage(t, conn)
	if ack.Event != "subscribe.ack" {
		t.Fatalf("ack event = %q, want subscribe.ack", ack.Event)
	}

	// Publish outbound message to the aliased session key (simulating what happens when a new chat starts)
	ts.bus.PublishOutbound(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    aliasedSessionKey, // Message is published with the resolved key
		Content:   "Message from aliased session",
		MessageID: "msg-aliased-1",
	})

	// Should receive message.stream even though we subscribed to base key
	stream := readWSMessage(t, conn)
	if stream.Event != "message.stream" {
		t.Fatalf("stream event = %q, want message.stream", stream.Event)
	}
	var streamPayload WSStreamPayload
	decodeWSData(t, stream.Data, &streamPayload)
	if streamPayload.MessageID != "msg-aliased-1" || streamPayload.SessionKey != aliasedSessionKey {
		t.Fatalf("stream payload = %#v, want msg-aliased-1/%s", streamPayload, aliasedSessionKey)
	}

	// Should also receive message.complete
	complete := readWSMessage(t, conn)
	if complete.Event != "message.complete" {
		t.Fatalf("complete event = %q, want message.complete", complete.Event)
	}
	var completePayload WSMessageCompletePayload
	decodeWSData(t, complete.Data, &completePayload)
	if completePayload.MessageID != "msg-aliased-1" || completePayload.SessionKey != aliasedSessionKey {
		t.Fatalf("complete payload = %#v, want msg-aliased-1/%s", completePayload, aliasedSessionKey)
	}
}
