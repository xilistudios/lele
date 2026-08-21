package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func newTestNativeChannel(t *testing.T) (*NativeChannel, *bus.MessageBus, *nativeTestAgentLoop) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.LeleDir = t.TempDir()
	mb := bus.NewMessageBus()
	loop := newNativeTestAgentLoop(cfg)
	auth, err := NewAuthManager(&cfg.Channels.Native, cfg.Channels.Native.LeleDir)
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	n := &NativeChannel{
		base:             NewBaseChannel("native", &cfg.Channels.Native, mb, nil),
		cfg:              &cfg.Channels.Native,
		auth:             auth,
		bus:              mb,
		agentLoop:        loop,
		wsClients:        make(map[string]*WSClient),
		restStreams:      make(map[string]*restStreamSubscriber),
		leleDir:          cfg.Channels.Native.LeleDir,
		configPath:       "",
		pinLimiter:       newRateLimiter(10, time.Minute),
		pairLimiter:      newRateLimiter(5, time.Minute),
		apiLimiter:       newRateLimiter(120, time.Minute),
		wsMessageLimiter: newRateLimiter(120, time.Minute),
	}
	return n, mb, loop
}

func TestNativeChannel_SimpleMethods(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	if n.Name() != "native" {
		t.Errorf("Name() = %q", n.Name())
	}
	if !n.IsAllowed("anything") {
		t.Error("IsAllowed should always return true for native")
	}
	if n.IsRunning() {
		t.Error("should not be running initially")
	}
	n.running = true
	if !n.IsRunning() {
		t.Error("IsRunning should reflect the running field")
	}
	n.running = false
}

func TestNativeChannel_SetReloadConfig(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	var called atomic.Bool
	n.SetReloadConfig(func() error {
		called.Store(true)
		return nil
	})
	if n.reloadConfig == nil {
		t.Fatal("reloadConfig should be set")
	}
	if err := n.reloadConfig(); err != nil {
		t.Fatalf("callback error: %v", err)
	}
	if !called.Load() {
		t.Error("callback not called")
	}
}

func TestNativeChannel_RegisterDesktopClient(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	if err := n.RegisterDesktopClient("tok", "ref"); err != nil {
		t.Fatalf("RegisterDesktopClient: %v", err)
	}
	if err := n.RegisterDesktopClient("", "ref"); err == nil {
		t.Error("expected error with empty token")
	}

	// nil receiver
	var n2 *NativeChannel
	if err := n2.RegisterDesktopClient("tok", "ref"); err == nil {
		t.Error("expected error for nil receiver")
	}
}

func TestNativeChannel_StartStop(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := n.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !n.IsRunning() {
		t.Error("should be running after Start")
	}
	// Start again is a no-op.
	if err := n.Start(ctx); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if err := n.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if n.IsRunning() {
		t.Error("should not be running after Stop")
	}
	if err := n.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestNativeChannel_removeWSClient(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	// Removing a non-existent client is a no-op.
	n.removeWSClient("none")

	client := &WSClient{
		ID:        "c1",
		SendChan:  make(chan []byte, 1),
		closed:    false,
		reconnectTimer: time.AfterFunc(time.Hour, func() {}),
	}
	n.addWSClient(client)
	n.removeWSClient("c1")
	if _, exists := n.wsClients["c1"]; exists {
		t.Error("client should be removed")
	}
	if !client.closed {
		t.Error("client should be marked closed")
	}
}

func TestNativeChannel_broadcastAll(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	client := &WSClient{
		ID:       "c1",
		SendChan: make(chan []byte, 4),
	}
	n.addWSClient(client)
	n.broadcastAll("test.event", map[string]string{"a": "b"})
	select {
	case <-client.SendChan:
		// got it
	case <-time.After(500 * time.Millisecond):
		t.Error("expected broadcast message in SendChan")
	}

	// A closed client gets cleaned up.
	closedClient := &WSClient{
		ID:         "c2",
		SendChan:   make(chan []byte),
		Conn:       nil,
	}
	closedClient.closed = true
	n.addWSClient(closedClient)
	n.broadcastAll("x", "y")
	if _, exists := n.wsClients["c2"]; exists {
		t.Error("closed client should be removed after broadcast failure")
	}
}

func TestNativeChannel_reconnectWSClient(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	client := &WSClient{
		ID:        "c1",
		SessionKey: "sk",
		SendChan:  make(chan []byte, 4),
		pendingMsgs: []json.RawMessage{json.RawMessage(`{"a":1}`)},
		reconnecting: true,
		reconnectTimer: time.AfterFunc(time.Millisecond, func() {}),
	}
	buffered := n.reconnectWSClient(client, nil)
	if len(buffered) != 1 {
		t.Errorf("expected 1 buffered message, got %d", len(buffered))
	}
	if client.reconnecting || client.closed {
		t.Error("client should be active after reconnect")
	}
}

func TestNativeChannel_processAttachments(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	uploadDir := filepath.Join(n.cfg.LeleDir, "tmp", "uploads")
	absUploadDir, _ := filepath.Abs(uploadDir)
	if err := os.MkdirAll(absUploadDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	good := filepath.Join(absUploadDir, "good.txt")
	if err := os.WriteFile(good, []byte("hello"), 0644); err != nil {
		t.Fatalf("write good: %v", err)
	}

	// A valid file inside the upload dir.
	atts := n.processAttachments([]string{good}, "sk")
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(atts))
	}
	if atts[0].Name != "good.txt" {
		t.Errorf("name = %q", atts[0].Name)
	}
	if !atts[0].Temporary {
		t.Error("attachment should be marked temporary (inside upload dir)")
	}

	// A path outside the upload dir gets rejected.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(outside, []byte("x"), 0644)
	atts = n.processAttachments([]string{outside}, "sk")
	if len(atts) != 0 {
		t.Errorf("expected outside path to be rejected, got %d", len(atts))
	}

	// Nonexistent file.
	atts = n.processAttachments([]string{filepath.Join(absUploadDir, "missing.txt")}, "sk")
	if len(atts) != 0 {
		t.Errorf("expected missing file to be skipped, got %d", len(atts))
	}
}

func TestNativeChannel_validateSessionOwnership(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	pending, _ := n.auth.GeneratePIN("Dev")
	client, _, _, _ := n.auth.PairWithPIN(pending.PIN, "Dev")

	if !n.validateSessionOwnership(client.ClientID, "mysession") {
		t.Error("normal session should be allowed")
	}
	if n.validateSessionOwnership("no-such-client", "s") {
		t.Error("unknown client should be rejected")
	}
	if n.validateSessionOwnership(client.ClientID, "subagent:task1") {
		t.Error("subagent session without parent should be rejected")
	}
}

func TestNativeChannel_validateSessionOwnership_SubagentWithParent(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	pending, _ := n.auth.GeneratePIN("Dev")
	client, _, _, _ := n.auth.PairWithPIN(pending.PIN, "Dev")
	// Register a subagent parent in the agent loop.
	n.agentLoop.(*nativeTestAgentLoop).subagentParents["subagent:task1"] = "parent"
	if !n.validateSessionOwnership(client.ClientID, "subagent:task1") {
		t.Error("subagent with parent should be allowed")
	}
}

func TestWSClient_QueueSend_Closed(t *testing.T) {
	c := &WSClient{SendChan: make(chan []byte, 1)}
	c.closed = true
	if err := c.QueueSend([]byte("x")); err == nil {
		t.Error("expected error when sending to closed client")
	}
	_ = c.Send([]byte("x")) // Send delegates to QueueSend
}

func TestWSClient_QueueSend_ReconnectingBuffers(t *testing.T) {
	c := &WSClient{SendChan: make(chan []byte, 1)}
	c.reconnecting = true
	c.maxPendingMsgs = 2
	if err := c.QueueSend([]byte("a")); err != nil {
		t.Fatalf("first send while reconnecting: %v", err)
	}
	if err := c.QueueSend([]byte("b")); err != nil {
		t.Fatalf("second send while reconnecting: %v", err)
	}
	// Buffer full now.
	if err := c.QueueSend([]byte("c")); err == nil {
		t.Error("expected buffer-full error")
	}
	if len(c.pendingMsgs) != 2 {
		t.Errorf("expected 2 pending messages, got %d", len(c.pendingMsgs))
	}
}

func TestWSClient_QueueSend_Timeout(t *testing.T) {
	// Unbuffered channel with no reader: QueueSend should time out and force-close.
	c := &WSClient{SendChan: make(chan []byte)}
	err := c.QueueSend([]byte("x"))
	if err == nil {
		t.Error("expected timeout error")
	}
	if !c.closed {
		t.Error("client should be force-closed after send timeout")
	}
}

func TestAttachmentsToMaps(t *testing.T) {
	maps := attachmentsToMaps([]bus.FileAttachment{
		{Name: "n", Path: "p", MIMEType: "m", Kind: "k", Caption: "c"},
	})
	if len(maps) != 1 {
		t.Fatalf("len = %d", len(maps))
	}
	if maps[0]["name"] != "n" || maps[0]["caption"] != "c" {
		t.Errorf("map = %v", maps[0])
	}
}

func TestParseStringField(t *testing.T) {
	// nil → not provided
	if s, provided, err := parseStringField(nil); provided || err != nil || s != "" {
		t.Errorf("nil: s=%q provided=%v err=%v", s, provided, err)
	}
	// "null" → provided but empty
	if s, provided, err := parseStringField([]byte("null")); !provided || err != nil || s != "" {
		t.Errorf("null: s=%q provided=%v err=%v", s, provided, err)
	}
	// valid string
	if s, provided, err := parseStringField([]byte(`"hi"`)); s != "hi" || !provided || err != nil {
		t.Errorf("valid: s=%q provided=%v err=%v", s, provided, err)
	}
	// invalid
	if _, _, err := parseStringField([]byte(`{"a":1}`)); err == nil {
		t.Error("expected error for object")
	}
}

func TestParseSpawnInput(t *testing.T) {
	if _, provided, err := parseSpawnInput(nil); provided || err != nil {
		t.Errorf("nil: provided=%v err=%v", provided, err)
	}
	if _, provided, err := parseSpawnInput([]byte("null")); !provided || err != nil {
		t.Errorf("null: provided=%v err=%v", provided, err)
	}
	in, provided, err := parseSpawnInput([]byte(`{"task":"t"}`))
	if err != nil || !provided || in == nil || in.Task != "t" {
		t.Errorf("valid: in=%v provided=%v err=%v", in, provided, err)
	}
	if _, _, err := parseSpawnInput([]byte(`not-json`)); err == nil {
		t.Error("expected error for invalid spawn json")
	}
}

func TestNativeChannel_validateSpawnInput(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	// nil is fine.
	if cfg, err := n.validateSpawnInput(nil); err != nil || cfg != nil {
		t.Errorf("nil: cfg=%v err=%v", cfg, err)
	}
	// Missing task.
	if _, err := n.validateSpawnInput(&cronSpawnInput{}); err == nil {
		t.Error("expected error for missing task")
	}
	// Unknown agent.
	if _, err := n.validateSpawnInput(&cronSpawnInput{Task: "t", AgentID: "ghost"}); err == nil {
		t.Error("expected error for unknown agent")
	}
	// Valid.
	cfg, err := n.validateSpawnInput(&cronSpawnInput{Task: "  t  ", AgentID: "main", Label: "l"})
	if err != nil {
		t.Fatalf("valid: %v", err)
	}
	if cfg.Task != "t" || cfg.AgentID != "main" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestNativeChannel_validateSpawnModel(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	if err := n.validateSpawnModel("gpt-4o"); err != nil {
		t.Errorf("bare model should be accepted: %v", err)
	}
	// Valid provider:model
	if err := n.validateSpawnModel("openai:gpt-4o"); err != nil {
		t.Errorf("openai:gpt-4o should be accepted: %v", err)
	}
	// Unknown provider
	if err := n.validateSpawnModel("ghost:gpt-4o"); err == nil {
		t.Error("expected error for unknown provider")
	}
	// Invalid model ref
	if err := n.validateSpawnModel(":bad"); err == nil {
		t.Error("expected error for invalid model reference")
	}
}

// corruptType is an invalid provider parse case.
func TestNativeChannel_sendWSEvent_EmptySession(tt *testing.T) {
	n, _, _ := newTestNativeChannel(tt)
	// sessionKey "" → broadcast to all (no clients, no panic).
	n.sendWSEvent("", "event", "data")
}

func TestNativeChannel_cronAvailable(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	rec := httptest.NewRecorder()
	if n.cronAvailable(rec) {
		t.Error("expected false when cronService is nil")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestNativeChannel_handleBackgroundExecs(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	// The mock agentLoop returns nil for GetBackgroundExecs.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/background-exec?include_completed=true", nil)
	n.handleBackgroundExecs(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestNativeChannel_handleBackgroundExecOutput(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	// Missing id.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/background-exec/%7Bid%7D/output", nil)
	req.SetPathValue("id", "")
	n.handleBackgroundExecOutput(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing id status = %d", rec.Code)
	}

	// Existing id, but mock returns error.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/background-exec/x/output?tail=10", nil)
	req.SetPathValue("id", "x")
	n.handleBackgroundExecOutput(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("not found status = %d", rec.Code)
	}
}

func TestNativeChannel_handleBackgroundExecStop(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/background-exec/x/stop", nil)
	req.SetPathValue("id", "x")
	n.handleBackgroundExecStop(rec, req)
	// mock always errors → 404
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestNativeChannel_getConfigPath(t *testing.T) {
	n, _, _ := newTestNativeChannel(t)
	if n.getConfigPath() != config.DefaultConfigPath() {
		t.Error("expected default config path")
	}
	n.configPath = "/tmp/custom.json"
	if n.getConfigPath() != "/tmp/custom.json" {
		t.Error("expected custom config path")
	}
}