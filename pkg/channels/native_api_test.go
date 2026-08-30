package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/skills"
)

type nativeTestAgentLoop struct {
	config             *config.Config
	histories          map[string][]providers.Message
	evicted            map[string][]providers.Message // messages evicted from in-memory history, loaded on demand
	sessionAgents      map[string]string
	sessionModels      map[string]string
	sessionAliases     map[string]string // base -> resolved
	sessionAliasesMu   sync.RWMutex
	subagentParents    map[string]string // subagent_key -> parent_key
	workspace          string            // Override workspace path for GetAgentInfo (default: "/tmp/workspace")
	sessionNames       map[string]string
	sessionThinkLevels map[string]string
	sessionSubagents   map[string][]SubagentTaskInfo // sessionKey -> subagent tasks
	processing         map[string]bool               // sessionKey -> is session processing

	// groupSnapshotsBySession lets a test control what the store-backed session
	// lookup returns; groupSnapshotCalls records that the channel delegated to
	// the agent loop instead of filtering memory itself (#239).
	groupSnapshotsBySession map[string][]group.GroupSnapshot
	groupSnapshotCalls      []string
	groupSnapshotMu         sync.Mutex
	// allGroupSnapshotsCalls counts uses of the memory-only API, which the
	// session read path must no longer touch (#239).
	allGroupSnapshotsCalls int
}

func newNativeTestAgentLoop(cfg *config.Config) *nativeTestAgentLoop {
	return &nativeTestAgentLoop{
		config:             cfg,
		histories:          make(map[string][]providers.Message),
		evicted:            make(map[string][]providers.Message),
		sessionAgents:      make(map[string]string),
		sessionModels:      make(map[string]string),
		sessionAliases:     make(map[string]string),
		subagentParents:    make(map[string]string),
		sessionNames:       make(map[string]string),
		sessionThinkLevels: make(map[string]string),
		sessionSubagents:   make(map[string][]SubagentTaskInfo),
		processing:         make(map[string]bool),
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
	workspace := m.workspace
	if workspace == "" {
		workspace = "/tmp/workspace"
	}
	return AgentBasicInfo{
		ID:        "main",
		Name:      "Main Agent",
		Workspace: workspace,
		Model:     "gpt-4",
	}, true
}

func (m *nativeTestAgentLoop) GetSessionHistory(sessionKey string) []providers.Message {
	history := m.histories[sessionKey]
	result := make([]providers.Message, len(history))
	copy(result, history)
	return result
}

func (m *nativeTestAgentLoop) GetHistoryView(sessionKey string) []providers.Message {
	return m.histories[sessionKey]
}

func (m *nativeTestAgentLoop) LoadEvictedMessages(sessionKey string) int {
	evicted := m.evicted[sessionKey]
	if len(evicted) == 0 {
		return 0
	}
	// Prepend evicted messages to the FRONT of the in-memory history.
	m.histories[sessionKey] = append(append([]providers.Message{}, evicted...), m.histories[sessionKey]...)
	delete(m.evicted, sessionKey)
	return len(evicted)
}

func (m *nativeTestAgentLoop) GetEvictedMessageCount(sessionKey string) int {
	return len(m.evicted[sessionKey])
}

func (m *nativeTestAgentLoop) GetTotalMessageCount(sessionKey string) int {
	return len(m.histories[sessionKey]) + len(m.evicted[sessionKey])
}

func (m *nativeTestAgentLoop) HasMessages(sessionKey string) bool {
	return len(m.histories[sessionKey]) > 0 || len(m.evicted[sessionKey]) > 0
}

func (m *nativeTestAgentLoop) AddSessionMessage(sessionKey string, msg providers.Message) error {
	m.histories[sessionKey] = append(m.histories[sessionKey], msg)
	return nil
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

func (m *nativeTestAgentLoop) GetSessionMode(sessionKey string) string {
	return ""
}

func (m *nativeTestAgentLoop) SetSessionMode(sessionKey, mode string) error {
	return nil
}

func (m *nativeTestAgentLoop) GetSessionModelSupportsImages(sessionKey string) bool {
	model := m.GetSessionModel(sessionKey)
	if model == "" {
		return false
	}
	if m.config == nil {
		return false
	}
	providerName := "openai"
	if idx := strings.Index(model, "/"); idx > 0 {
		providerName = strings.ToLower(model[:idx])
	}
	if prov, ok := m.config.Providers.GetNamed(providerName); ok {
		if modelCfg, exists := prov.Models[model]; exists {
			return modelCfg.Vision
		}
	}
	return false
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
	if level, ok := m.sessionThinkLevels[sessionKey]; ok {
		return level
	}
	return "default"
}

func (m *nativeTestAgentLoop) SetThinkLevel(sessionKey string, level string) bool {
	m.sessionThinkLevels[sessionKey] = level
	return true
}

func (m *nativeTestAgentLoop) GetSubagents() string {
	return ""
}

func (m *nativeTestAgentLoop) GetSessionSubagents(sessionKey string) []SubagentTaskInfo {
	if tasks, ok := m.sessionSubagents[sessionKey]; ok {
		result := make([]SubagentTaskInfo, len(tasks))
		copy(result, tasks)
		return result
	}
	return nil
}

func (m *nativeTestAgentLoop) ClearSession(sessionKey string) string {
	return "cleared"
}

func (m *nativeTestAgentLoop) GetName(sessionKey string) string {
	if name, ok := m.sessionNames[sessionKey]; ok {
		return name
	}
	return ""
}

func (m *nativeTestAgentLoop) GetUpdated(sessionKey string) time.Time {
	return time.Time{}
}

func (m *nativeTestAgentLoop) GetCreated(sessionKey string) time.Time {
	return time.Time{}
}

func (m *nativeTestAgentLoop) SetName(sessionKey string, name string) error {
	m.sessionNames[sessionKey] = name
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

func (m *nativeTestAgentLoop) GetSubagentParentSessionKey(sessionKey string) string {
	if parent, ok := m.subagentParents[sessionKey]; ok {
		return parent
	}
	return ""
}

func (m *nativeTestAgentLoop) IsSessionProcessing(sessionKey string) bool {
	return m.processing[sessionKey]
}

func (m *nativeTestAgentLoop) GetTokenCounts(sessionKey string) (int, int, int) {
	return 0, 0, 128000
}

func (m *nativeTestAgentLoop) GetCurrentContextUsage(sessionKey string) (int, int) {
	return 0, 128000
}

func (m *nativeTestAgentLoop) GetCompactionCount(sessionKey string) int {
	return 0
}

func (m *nativeTestAgentLoop) GetSessionSummary(sessionKey string) string {
	return ""
}

func (m *nativeTestAgentLoop) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	return "", nil
}

func (m *nativeTestAgentLoop) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	return "", nil
}

func (m *nativeTestAgentLoop) ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error) {
	return "HEARTBEAT_OK", nil
}

func (m *nativeTestAgentLoop) ListAllSessions() []SessionKindInfo {
	var result []SessionKindInfo
	seen := make(map[string]bool)
	for key := range m.histories {
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, SessionKindInfo{
			Key:  key,
			Name: m.sessionNames[key],
			Mode: m.GetSessionMode(key),
			Kind: classifySessionKeyKind(key),
		})
	}
	return result
}

func (m *nativeTestAgentLoop) AppendAssistantChunk(sessionKey, chunk string) {}

func (m *nativeTestAgentLoop) AppendReasoningChunk(sessionKey, chunk string) {}

func (m *nativeTestAgentLoop) FinalizeAssistantMessage(sessionKey string) {}

func (m *nativeTestAgentLoop) HasStreamedContent(sessionKey string) bool {
	return false
}

func (m *nativeTestAgentLoop) GetInProgressAssistant(sessionKey string) *providers.Message {
	return nil
}

func (m *nativeTestAgentLoop) GetBackgroundExecs(includeCompleted bool) []BackgroundExecInfo {
	return nil
}

func (m *nativeTestAgentLoop) GetBackgroundExecOutput(id string, tail int) (string, string, int64, error) {
	return "", "", 0, fmt.Errorf("not implemented")
}

func (m *nativeTestAgentLoop) StopBackgroundExec(id string) error {
	return fmt.Errorf("not implemented")
}

func (m *nativeTestAgentLoop) AllGroupSnapshots() []group.GroupSnapshot {
	m.groupSnapshotMu.Lock()
	defer m.groupSnapshotMu.Unlock()
	m.allGroupSnapshotsCalls++
	return nil
}

// GroupSnapshotsForSession is the store-backed session lookup the channel now
// delegates to (#239).
func (m *nativeTestAgentLoop) GroupSnapshotsForSession(sessionKey string) []group.GroupSnapshot {
	m.groupSnapshotMu.Lock()
	defer m.groupSnapshotMu.Unlock()
	m.groupSnapshotCalls = append(m.groupSnapshotCalls, sessionKey)
	return m.groupSnapshotsBySession[sessionKey]
}

// setGroupSnapshotsForSession publishes the snapshots a session should get.
func (m *nativeTestAgentLoop) setGroupSnapshotsForSession(sessionKey string, snaps ...group.GroupSnapshot) {
	m.groupSnapshotMu.Lock()
	defer m.groupSnapshotMu.Unlock()
	if m.groupSnapshotsBySession == nil {
		m.groupSnapshotsBySession = make(map[string][]group.GroupSnapshot)
	}
	m.groupSnapshotsBySession[sessionKey] = snaps
}

// groupSnapshotCallCount reports how many times the channel delegated the
// session group lookup to the agent loop.
func (m *nativeTestAgentLoop) groupSnapshotCallCount() int {
	m.groupSnapshotMu.Lock()
	defer m.groupSnapshotMu.Unlock()
	return len(m.groupSnapshotCalls)
}

// allGroupSnapshotsCallCount reports how many times the memory-only group API
// was used; the session read path must leave it at zero (#239).
func (m *nativeTestAgentLoop) allGroupSnapshotsCallCount() int {
	m.groupSnapshotMu.Lock()
	defer m.groupSnapshotMu.Unlock()
	return m.allGroupSnapshotsCalls
}

func (m *nativeTestAgentLoop) ResolveRoute(channel, peerKind, peerID string) string {
	// Simple test implementation: use per-peer scope if configured, else main.
	if m.config != nil && m.config.Session.DMScope == "per-peer" && peerKind == "direct" && peerID != "" {
		return fmt.Sprintf("agent:main:direct:%s", peerID)
	}
	if peerKind == "group" && peerID != "" {
		return fmt.Sprintf("agent:main:%s:group:%s", channel, peerID)
	}
	return "agent:main:main"
}

type nativeTestServer struct {
	channel  *NativeChannel
	loop     *nativeTestAgentLoop
	bus      *bus.MessageBus
	server   *httptest.Server
	token    string
	clientID string
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
		cfg:              &cfg.Channels.Native,
		auth:             auth,
		bus:              msgBus,
		agentLoop:        loop,
		wsClients:        make(map[string]*WSClient),
		pinLimiter:       newRateLimiter(10, time.Minute),
		pairLimiter:      newRateLimiter(5, time.Minute),
		apiLimiter:       newRateLimiter(120, time.Minute),
		wsMessageLimiter: newRateLimiter(30, time.Minute),
		skillsLoader:     &skills.SkillsLoader{},
	}

	mux := http.NewServeMux()
	channel.RegisterRoutes(mux)
	// Routes already have auth middleware applied via withAuth wrapper
	server := httptest.NewServer(channel.corsMiddleware(channel.securityHeadersMiddleware(mux)))

	t.Cleanup(func() {
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
	}
}

// newNativeTestServerWithConfigPath creates a test server with a custom config path.
// This is useful for tests that need to control the config file location.
func newNativeTestServerWithConfigPath(t *testing.T, configPath string) *nativeTestServer {
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
		cfg:              &cfg.Channels.Native,
		auth:             auth,
		bus:              msgBus,
		agentLoop:        loop,
		wsClients:        make(map[string]*WSClient),
		pinLimiter:       newRateLimiter(10, time.Minute),
		pairLimiter:      newRateLimiter(5, time.Minute),
		apiLimiter:       newRateLimiter(120, time.Minute),
		wsMessageLimiter: newRateLimiter(30, time.Minute),
		configPath:       configPath,
		skillsLoader:     &skills.SkillsLoader{},
	}

	mux := http.NewServeMux()
	channel.RegisterRoutes(mux)
	// Routes already have auth middleware applied via withAuth wrapper
	server := httptest.NewServer(channel.corsMiddleware(channel.securityHeadersMiddleware(mux)))

	t.Cleanup(func() {
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

func TestNativeChannelConfigPreflightAllowsPutFromSameHost(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.cfg.Host = "192.168.0.171"

	req, err := http.NewRequest(http.MethodOptions, ts.server.URL+"/api/v1/config", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Origin", "http://192.168.0.171:3005")
	req.Header.Set("Access-Control-Request-Method", http.MethodPut)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://192.168.0.171:3005" {
		t.Fatalf("allow origin = %q, want %q", got, "http://192.168.0.171:3005")
	}

	allowMethods := resp.Header.Get("Access-Control-Allow-Methods")
	if !strings.Contains(allowMethods, http.MethodPut) {
		t.Fatalf("allow methods = %q, expected to contain %q", allowMethods, http.MethodPut)
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

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.QueryEscape(sessionKey)+"/history", nil)
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
	if payload.HasMore != false {
		t.Fatalf("has_more = %v, want false", payload.HasMore)
	}
}

func TestNativeChannelSubagentHistoryReturnsMessages(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID + ":uuid-test-session"
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)
	ts.loop.histories[sessionKey] = []providers.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}

	// Register subagent history - new format: native:{parent_uuid}:{task_id}
	subagentSessionKey := sessionKey + ":subagent-1"
	ts.loop.histories[subagentSessionKey] = []providers.Message{
		{Role: "user", Content: "Subagent task"},
		{Role: "assistant", Content: "Subagent result"},
	}

	// Registrar relación parent-subagent (using new format without subagent: prefix)
	ts.loop.subagentParents[sessionKey+":subagent-1"] = sessionKey

	// Test subagent history endpoint
	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.QueryEscape(sessionKey)+"/history/subagent-1", nil)
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

	if payload.SessionKey != subagentSessionKey {
		t.Fatalf("session_key = %q, want %q", payload.SessionKey, subagentSessionKey)
	}
	if len(payload.Messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(payload.Messages))
	}
	if payload.Messages[0].Role != "user" || payload.Messages[0].Content != "Subagent task" {
		t.Fatalf("first message = %#v, want user Subagent task", payload.Messages[0])
	}
	if payload.Messages[1].Role != "assistant" || payload.Messages[1].Content != "Subagent result" {
		t.Fatalf("second message = %#v, want assistant Subagent result", payload.Messages[1])
	}
	if payload.HasMore != false {
		t.Fatalf("has_more = %v, want false", payload.HasMore)
	}
}

func TestNativeChannelChatSessionsReturnsTrackedSessionKeys(t *testing.T) {
	ts := newNativeTestServer(t)
	trackedSession := "native:" + ts.clientID + ":secondary"
	ts.channel.auth.TrackSessionKey(ts.clientID, trackedSession)

	// Both sessions need history so neither is skipped as empty
	ts.loop.histories[ts.clientID] = []providers.Message{
		{Role: "user", Content: "First message"},
	}
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
		}
	}

	if !found {
		t.Fatalf("expected session %q in payload %#v", trackedSession, payload.Sessions)
	}
}

func TestChatHistory_LoadsEvictedMessages(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID + ":evicted-history"
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)

	// Older messages were evicted (out-of-context) after compaction; they remain
	// in SQLite and should be materialized on demand.
	ts.loop.evicted[sessionKey] = []providers.Message{
		{Role: "user", Content: "Evicted-1"},
		{Role: "assistant", Content: "Evicted-2"},
	}
	// Recent messages remain in the in-memory slice.
	ts.loop.histories[sessionKey] = []providers.Message{
		{Role: "user", Content: "Recent-1"},
		{Role: "assistant", Content: "Recent-2"},
	}

	// 1) Full history (default limit) returns ALL messages in order (evicted first).
	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.QueryEscape(sessionKey)+"/history", nil)
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

	if len(payload.Messages) != 4 {
		t.Fatalf("len(messages) = %d, want 4 (evicted + recent)", len(payload.Messages))
	}
	wantOrder := []string{"Evicted-1", "Evicted-2", "Recent-1", "Recent-2"}
	for i, want := range wantOrder {
		if payload.Messages[i].Content != want {
			t.Fatalf("messages[%d].Content = %q, want %q", i, payload.Messages[i].Content, want)
		}
	}
	if payload.HasMore {
		t.Fatalf("has_more = true, want false for full history")
	}

	// Evicted messages were loaded into memory and cleared from the evicted pool.
	if len(ts.loop.evicted[sessionKey]) != 0 {
		t.Fatalf("evicted pool not cleared after load: %d remaining", len(ts.loop.evicted[sessionKey]))
	}
	if len(ts.loop.histories[sessionKey]) != 4 {
		t.Fatalf("in-memory history = %d messages, want 4 after load", len(ts.loop.histories[sessionKey]))
	}

	// 2) Pagination with a small limit: oldest evicted messages are reachable
	//    via before_id. The first page returns only the most recent messages.
	req2, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.QueryEscape(sessionKey)+"/history?limit=2", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req2.Header.Set("Authorization", "Bearer "+ts.token)

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp2.Body.Close()

	var payload2 ChatHistoryResponse
	if err := json.NewDecoder(resp2.Body).Decode(&payload2); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(payload2.Messages) != 2 {
		t.Fatalf("page1 len(messages) = %d, want 2", len(payload2.Messages))
	}
	if payload2.Messages[0].Content != "Recent-1" || payload2.Messages[1].Content != "Recent-2" {
		t.Fatalf("page1 messages = [%q, %q], want [Recent-1, Recent-2]", payload2.Messages[0].Content, payload2.Messages[1].Content)
	}
	if !payload2.HasMore {
		t.Fatalf("page1 has_more = false, want true (older evicted messages exist)")
	}
	beforeID := payload2.Messages[0].ID

	// Follow the before_id cursor to reach the oldest evicted messages.
	req3, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.QueryEscape(sessionKey)+"/history?limit=2&before_id="+url.QueryEscape(beforeID), nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req3.Header.Set("Authorization", "Bearer "+ts.token)

	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp3.Body.Close()

	var payload3 ChatHistoryResponse
	if err := json.NewDecoder(resp3.Body).Decode(&payload3); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(payload3.Messages) != 2 {
		t.Fatalf("page2 len(messages) = %d, want 2", len(payload3.Messages))
	}
	if payload3.Messages[0].Content != "Evicted-1" || payload3.Messages[1].Content != "Evicted-2" {
		t.Fatalf("page2 messages = [%q, %q], want [Evicted-1, Evicted-2]", payload3.Messages[0].Content, payload3.Messages[1].Content)
	}
	if payload3.HasMore {
		t.Fatalf("page2 has_more = true, want false (reached oldest)")
	}
}

func TestChatSessions_CountIncludesEvicted(t *testing.T) {
	ts := newNativeTestServer(t)
	trackedSession := "native:" + ts.clientID + ":evicted-count"
	ts.channel.auth.TrackSessionKey(ts.clientID, trackedSession)

	// In-memory: 2 counted user/assistant messages.
	ts.loop.histories[trackedSession] = []providers.Message{
		{Role: "user", Content: "Recent user"},
		{Role: "assistant", Content: "Recent assistant"},
	}
	// Evicted: 2 more user/assistant messages not in the in-memory slice.
	ts.loop.evicted[trackedSession] = []providers.Message{
		{Role: "user", Content: "Evicted user"},
		{Role: "assistant", Content: "Evicted assistant"},
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

	var found bool
	for _, session := range payload.Sessions {
		if session.Key == trackedSession {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected session %q in payload %#v", trackedSession, payload.Sessions)
	}

	// The evicted messages must NOT have been materialized by handleChatSessions.
	if len(ts.loop.evicted[trackedSession]) != 2 {
		t.Fatalf("evicted pool mutated by handleChatSessions: %d remaining, want 2", len(ts.loop.evicted[trackedSession]))
	}
}

func TestNativeChannelChatSessionsKindFilter(t *testing.T) {
	ts := newNativeTestServer(t)

	// Tracked user session
	userSession := "native:" + ts.clientID + ":user-chat"
	ts.channel.auth.TrackSessionKey(ts.clientID, userSession)

	// System sessions that exist in the shared session store but are NOT
	// tracked by any native client (heartbeat + cron).
	ts.loop.histories[userSession] = []providers.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi"},
	}
	ts.loop.histories["heartbeat"] = []providers.Message{
		{Role: "user", Content: "# Heartbeat Check"},
		{Role: "assistant", Content: "HEARTBEAT_OK"},
	}
	ts.loop.histories["cron-abc123"] = []providers.Message{
		{Role: "user", Content: "Run daily report"},
		{Role: "assistant", Content: "Done"},
	}

	t.Run("kind=heartbeat includes untracked heartbeat session", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions?kind=heartbeat", nil)
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

		if len(payload.Sessions) != 1 {
			t.Fatalf("len(sessions) = %d, want 1 (only heartbeat); got %#v", len(payload.Sessions), payload.Sessions)
		}
		if payload.Sessions[0].Key != "heartbeat" {
			t.Fatalf("session key = %q, want %q", payload.Sessions[0].Key, "heartbeat")
		}
		if payload.Sessions[0].Kind != "heartbeat" {
			t.Fatalf("session kind = %q, want %q", payload.Sessions[0].Kind, "heartbeat")
		}
	})

	t.Run("kind=cron includes untracked cron session", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions?kind=cron", nil)
		if err != nil {
			t.Fatalf("NewRequest() error = %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+ts.token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer resp.Body.Close()

		var payload ChatSessionsResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}

		if len(payload.Sessions) != 1 {
			t.Fatalf("len(sessions) = %d, want 1 (only cron); got %#v", len(payload.Sessions), payload.Sessions)
		}
		if payload.Sessions[0].Key != "cron-abc123" {
			t.Fatalf("session key = %q, want %q", payload.Sessions[0].Key, "cron-abc123")
		}
		if payload.Sessions[0].Kind != "cron" {
			t.Fatalf("session kind = %q, want %q", payload.Sessions[0].Kind, "cron")
		}
	})

	t.Run("kind=chat only returns tracked chat session", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions?kind=chat", nil)
		if err != nil {
			t.Fatalf("NewRequest() error = %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+ts.token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer resp.Body.Close()

		var payload ChatSessionsResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}

		if len(payload.Sessions) != 1 {
			t.Fatalf("len(sessions) = %d, want 1 (only chat); got %#v", len(payload.Sessions), payload.Sessions)
		}
		if payload.Sessions[0].Key != userSession {
			t.Fatalf("session key = %q, want %q", payload.Sessions[0].Key, userSession)
		}
	})

	t.Run("include_system=true merges heartbeat and cron in all view", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions?include_system=true", nil)
		if err != nil {
			t.Fatalf("NewRequest() error = %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+ts.token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer resp.Body.Close()

		var payload ChatSessionsResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}

		keys := make(map[string]string)
		for _, s := range payload.Sessions {
			keys[s.Key] = s.Kind
		}
		if keys[userSession] != "chat" {
			t.Fatalf("user session kind = %q, want chat; got %#v", keys[userSession], payload.Sessions)
		}
		if keys["heartbeat"] != "heartbeat" {
			t.Fatalf("heartbeat session kind = %q, want heartbeat; got %#v", keys["heartbeat"], payload.Sessions)
		}
		if keys["cron-abc123"] != "cron" {
			t.Fatalf("cron session kind = %q, want cron; got %#v", keys["cron-abc123"], payload.Sessions)
		}
	})

	t.Run("kind filter keeps empty system sessions", func(t *testing.T) {
		// An empty cron session (no messages) must still show up in the
		// cron view so users can see that a cron run happened.
		ts.loop.histories["cron-empty-job"] = []providers.Message{}

		req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions?kind=cron", nil)
		if err != nil {
			t.Fatalf("NewRequest() error = %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+ts.token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer resp.Body.Close()

		var payload ChatSessionsResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}

		var found bool
		for _, s := range payload.Sessions {
			if s.Key == "cron-empty-job" {
				found = true
				if s.Kind != "cron" {
					t.Fatalf("empty cron kind = %q, want cron", s.Kind)
				}
				break
			}
		}
		if !found {
			t.Fatalf("empty cron session not found in kind=cron; got %#v", payload.Sessions)
		}
	})
}

func TestNativeChannelCreateSession(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID + ":" + "uuid-test-session"

	body, _ := json.Marshal(CreateSessionRequest{SessionKey: sessionKey})
	req, err := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/sessions", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	var payload CreateSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if payload.SessionKey != sessionKey {
		t.Fatalf("session_key = %q, want %q", payload.SessionKey, sessionKey)
	}

	client, ok := ts.channel.auth.GetClient(ts.clientID)
	if !ok {
		t.Fatal("client not found")
	}

	var found bool
	for _, sk := range client.SessionKeys {
		if sk == sessionKey {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("session key %q not tracked in client SessionKeys", sessionKey)
	}
}

func TestNativeChannelCreateSessionAllowsAnyValidKey(t *testing.T) {
	ts := newNativeTestServer(t)

	body, _ := json.Marshal(CreateSessionRequest{SessionKey: "native:otherclient:123"})
	req, err := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/sessions", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	// Session creation accepts any valid key; ownership is validated on access
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	client, ok := ts.channel.auth.GetClient(ts.clientID)
	if !ok {
		t.Fatal("client not found")
	}

	var found bool
	for _, sk := range client.SessionKeys {
		if sk == "native:otherclient:123" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("session key not tracked for client")
	}
}

func TestNativeChannelSessionModelEndpoints(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID

	getReq, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/model", nil)
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
	patchReq, err := http.NewRequest(http.MethodPatch, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/model", body)
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

func TestNativeChannelConfigUsesEditableDocument(t *testing.T) {
	// Create a temporary config file with default values
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Write a minimal config file with default native settings
	defaultConfig := config.DefaultConfig()
	configData, err := json.MarshalIndent(defaultConfig, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	ts := newNativeTestServerWithConfigPath(t, configPath)

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

	var response ConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	// Verificar que la respuesta contiene los datos esperados
	configMap, ok := response.Config.(map[string]interface{})
	if !ok {
		t.Fatalf("config is not a map, got %T", response.Config)
	}

	channels, ok := configMap["channels"].(map[string]interface{})
	if !ok {
		t.Fatal("channels config not found or invalid")
	}

	native, ok := channels["native"].(map[string]interface{})
	if !ok {
		t.Fatal("native channel config not found or invalid")
	}

	// Verificar que el native config tiene valores por defecto (documento editable)
	if native["host"] != "127.0.0.1" {
		t.Errorf("host = %v, want 127.0.0.1 (default)", native["host"])
	}

	// JSON numbers are decoded as float64
	if native["port"] != float64(18790) {
		t.Errorf("port = %v, want 18790 (default)", native["port"])
	}

	// Verificar metadata
	if response.Metadata.Source != "file" {
		t.Errorf("metadata.source = %q, want 'file'", response.Metadata.Source)
	}
	if !response.Metadata.CanSave {
		t.Error("expected metadata.can_save to be true")
	}
	if response.Metadata.ConfigPath == "" {
		t.Error("expected config_path in metadata")
	}
}

func TestNativeChannelWebSocketSupportsQueryTokenAndStructuredEvents(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := ts.clientID

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

	ts.channel.Send(context.Background(), bus.OutboundMessage{
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

	// After message.complete, a history.updated event is emitted to signal persistence
	historyUpdated := readWSMessage(t, conn)
	if historyUpdated.Event != "history.updated" {
		t.Fatalf("event after complete = %q, want history.updated", historyUpdated.Event)
	}

	ts.channel.Send(context.Background(), bus.OutboundMessage{
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

	ts.channel.Send(context.Background(), bus.OutboundMessage{
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

	ts.channel.Send(context.Background(), bus.OutboundMessage{
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

func TestNativeRESTChatSendStream(t *testing.T) {
	ts := newNativeTestServer(t)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		inbound, ok := ts.bus.ConsumeInbound(ctx)
		if !ok {
			return
		}

		messageID := inbound.Metadata["message_id"]
		ts.channel.Send(context.Background(), bus.OutboundMessage{
			Channel:   ChannelName,
			ChatID:    inbound.ChatID,
			Event:     "message.stream",
			Content:   "Hello ",
			MessageID: messageID,
			Metadata:  map[string]string{"done": "false"},
		})
		ts.channel.Send(context.Background(), bus.OutboundMessage{
			Channel:   ChannelName,
			ChatID:    inbound.ChatID,
			Event:     "message.stream",
			Content:   "back",
			MessageID: messageID,
			Metadata:  map[string]string{"done": "false"},
		})
		ts.channel.Send(context.Background(), bus.OutboundMessage{
			Channel:   ChannelName,
			ChatID:    inbound.ChatID,
			Content:   "Hello back",
			MessageID: messageID,
		})
	}()

	body, err := json.Marshal(ChatSendRequest{
		Content:    "Hello from REST stream",
		SessionKey: ts.clientID,
		AgentID:    "main",
	})
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

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, payload)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", contentType)
	}

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	stream := string(payload)
	for _, expected := range []string{
		"event: message.ack",
		"event: message.stream",
		`"chunk":"Hello "`,
		`"chunk":"back"`,
		"event: message.complete",
		`"content":"Hello back"`,
		"event: history.updated",
	} {
		if !strings.Contains(stream, expected) {
			t.Fatalf("SSE stream missing %q:\n%s", expected, stream)
		}
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
	ts.channel.Send(context.Background(), bus.OutboundMessage{
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

// ==================== Config API Tests ====================

func TestHandleConfig_Get(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	configContent := `{
		"agents": {
			"defaults": {
				"workspace": "/test/workspace",
				"provider": "openai",
				"model": "gpt-4",
				"max_tokens": 8192,
				"max_tool_iterations": 20
			}
		},
		"channels": {
			"native": {
				"enabled": true,
				"port": 8080
			}
		}
	}`

	leleDir := filepath.Join(tmpDir, ".lele")
	os.MkdirAll(leleDir, 0755)
	configPath := filepath.Join(leleDir, "config.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	nc := &NativeChannel{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	w := httptest.NewRecorder()

	nc.handleGetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
		return
	}

	var response ConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	configMap, ok := response.Config.(map[string]interface{})
	if !ok {
		t.Error("expected config to be a map")
		return
	}

	if response.Metadata.ConfigPath == "" {
		t.Error("expected config_path in metadata")
	}
	if response.Metadata.Source != "file" {
		t.Errorf("expected source 'file', got %q", response.Metadata.Source)
	}

	agents, ok := configMap["agents"].(map[string]interface{})
	if !ok {
		t.Error("expected agents config")
		return
	}
	defaults, ok := agents["defaults"].(map[string]interface{})
	if !ok {
		t.Error("expected agents.defaults")
		return
	}
	if defaults["workspace"] != "/test/workspace" {
		t.Errorf("workspace = %v, want /test/workspace", defaults["workspace"])
	}
}

func TestHandleConfig_GetWithEnvPlaceholder(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	configContent := `{
		"channels": {
			"telegram": {
				"enabled": true,
				"token": "{{ENV_TELEGRAM_BOT_TOKEN}}"
			}
		}
	}`

	leleDir := filepath.Join(tmpDir, ".lele")
	os.MkdirAll(leleDir, 0755)
	configPath := filepath.Join(leleDir, "config.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	nc := &NativeChannel{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	w := httptest.NewRecorder()

	nc.handleGetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var response ConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.Metadata.SecretsByPath["channels.telegram.token"] != "env" {
		t.Errorf("expected token to be marked as env, got %q", response.Metadata.SecretsByPath["channels.telegram.token"])
	}
}

func TestHandleConfig_Put(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	leleDir := filepath.Join(tmpDir, ".lele")
	os.MkdirAll(leleDir, 0755)

	nc := &NativeChannel{}

	doc := config.EditableDocument{
		Agents: config.EditableAgentsConfig{
			Defaults: config.EditableAgentDefaults{
				Workspace:         "/test",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
		Channels: config.EditableChannelsConfig{
			Native: config.EditableNativeConfig{
				Enabled: true,
				Port:    8080,
			},
		},
	}

	body, _ := json.Marshal(ConfigUpdateRequest{Config: doc})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	nc.handlePutConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
		return
	}

	var response ConfigUpdateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(response.Errors) > 0 {
		t.Errorf("unexpected validation errors: %v", response.Errors)
	}

	configPath := filepath.Join(leleDir, "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("config file should exist: %v", err)
	}
}

func TestHandleConfig_Put_InvalidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	leleDir := filepath.Join(tmpDir, ".lele")
	os.MkdirAll(leleDir, 0755)

	nc := &NativeChannel{}

	doc := config.EditableDocument{
		Agents: config.EditableAgentsConfig{
			Defaults: config.EditableAgentDefaults{
				Workspace:         "",
				Provider:          "",
				Model:             "",
				MaxTokens:         0,
				MaxToolIterations: 0,
			},
		},
	}

	body, _ := json.Marshal(ConfigUpdateRequest{Config: doc})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	nc.handlePutConfig(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected status 422, got %d", w.Code)
		return
	}

	var respConfigUpdate ConfigUpdateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &respConfigUpdate); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(respConfigUpdate.Errors) == 0 {
		t.Error("expected validation errors")
	}
}

func TestHandleConfig_Post_Validate(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	nc := &NativeChannel{}

	doc := config.EditableDocument{
		Agents: config.EditableAgentsConfig{
			Defaults: config.EditableAgentDefaults{
				Workspace:         "/test",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
	}

	body, _ := json.Marshal(ConfigValidateRequest{Config: doc})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	nc.handleValidateConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response ConfigValidateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !response.Valid {
		t.Error("expected config to be valid")
	}
	if len(response.Errors) > 0 {
		t.Errorf("expected no errors, got %v", response.Errors)
	}
}

func TestHandleConfig_Post_Validate_Invalid(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	nc := &NativeChannel{}

	doc := config.EditableDocument{
		Agents: config.EditableAgentsConfig{
			Defaults: config.EditableAgentDefaults{
				Workspace: "",
				Provider:  "",
				Model:     "",
			},
		},
	}

	body, _ := json.Marshal(ConfigValidateRequest{Config: doc})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	nc.handleValidateConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var response ConfigValidateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response.Valid {
		t.Error("expected config to be invalid")
	}
	if len(response.Errors) == 0 {
		t.Error("expected validation errors")
	}
}

func TestHandleConfig_Put_WithEnvProviders(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	leleDir := filepath.Join(tmpDir, ".lele")
	os.MkdirAll(leleDir, 0755)

	nc := &NativeChannel{}

	doc := config.EditableDocument{
		Agents: config.EditableAgentsConfig{
			Defaults: config.EditableAgentDefaults{
				Workspace:         "/test",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
		Providers: config.EditableProvidersConfig{
			"my-openai": {
				Type:    "openai",
				APIBase: "https://api.example.com",
				APIKey:  config.SecretValue{Mode: config.SecretModeEnv, EnvName: "MY_API_KEY"},
			},
		},
	}

	body, _ := json.Marshal(ConfigUpdateRequest{Config: doc})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	nc.handlePutConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
		return
	}

	configPath := filepath.Join(leleDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	if !bytes.Contains(data, []byte("{{ENV_MY_API_KEY}}")) {
		t.Error("expected ENV placeholder to be preserved in saved config")
	}
}
func TestNativeChannelAgentFiles_ListFiles(t *testing.T) {
	workspace := t.TempDir()
	ts := newNativeTestServer(t)
	ts.loop.workspace = workspace

	// Create test workspace with context files
	for _, name := range []string{"AGENT.md", "SOUL.md", "MEMORY.md"} {
		os.WriteFile(filepath.Join(workspace, name), []byte("test content"), 0644)
	}

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/agents/main/files", nil)
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
		t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusOK, readBody(resp))
	}

	var payload AgentFilesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if len(payload.Files) == 0 {
		t.Fatal("expected at least one file in response")
	}

	// Check that AGENT.md is listed
	found := false
	for _, f := range payload.Files {
		if f.Name == "AGENT.md" {
			found = true
			if f.Size != 12 { // "test content" length
				t.Errorf("AGENT.md size = %d, want 12", f.Size)
			}
			break
		}
	}
	if !found {
		t.Error("expected AGENT.md to be listed")
	}
}

func TestNativeChannelAgentFiles_ReadFile(t *testing.T) {
	workspace := t.TempDir()
	ts := newNativeTestServer(t)
	ts.loop.workspace = workspace

	testContent := "# Agent Context\n\nThis is the agent context file."
	os.WriteFile(filepath.Join(workspace, "AGENT.md"), []byte(testContent), 0644)

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/agents/main/files/AGENT.md", nil)
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
		t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusOK, readBody(resp))
	}

	var payload AgentFilesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if payload.Content != testContent {
		t.Errorf("content = %q, want %q", payload.Content, testContent)
	}
}

func TestNativeChannelAgentFiles_AgentNotFound(t *testing.T) {
	ts := newNativeTestServer(t)

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/agents/nonexistent/files", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func readBody(resp *http.Response) string {
	data, _ := io.ReadAll(resp.Body)
	return string(data)
}

func TestCheckOrigin(t *testing.T) {
	// Build a minimal NativeChannel for unit-level checkOrigin tests.
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"

	msgBus := bus.NewMessageBus()
	loop := newNativeTestAgentLoop(cfg)
	auth, err := NewAuthManager(&cfg.Channels.Native, t.TempDir())
	if err != nil {
		t.Fatalf("NewAuthManager() error = %v", err)
	}

	channel := &NativeChannel{
		cfg:              &cfg.Channels.Native,
		auth:             auth,
		bus:              msgBus,
		agentLoop:        loop,
		wsClients:        make(map[string]*WSClient),
		pinLimiter:       newRateLimiter(10, time.Minute),
		pairLimiter:      newRateLimiter(5, time.Minute),
		apiLimiter:       newRateLimiter(120, time.Minute),
		wsMessageLimiter: newRateLimiter(30, time.Minute),
		skillsLoader:     &skills.SkillsLoader{},
	}

	tests := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{
			name:   "empty origin is allowed",
			origin: "",
			host:   "192.168.0.171:18790",
			want:   true,
		},
		{
			name:   "same-origin via LAN IP",
			origin: "http://192.168.0.171:18790",
			host:   "192.168.0.171:18790",
			want:   true,
		},
		{
			name:   "same-origin via hostname",
			origin: "http://mipc:18790",
			host:   "mipc:18790",
			want:   true,
		},
		{
			name:   "cross-origin not allowed",
			origin: "http://evil.example.com",
			host:   "192.168.0.171:18790",
			want:   false,
		},
		{
			name:   "cross-origin allowed via CORSOrigins config",
			origin: "http://custom.example.com",
			host:   "192.168.0.171:18790",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// For the CORSOrigins test, set the config value.
			if tt.name == "cross-origin allowed via CORSOrigins config" {
				channel.cfg.CORSOrigins = []string{"http://custom.example.com"}
			} else {
				channel.cfg.CORSOrigins = nil
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			req.Host = tt.host

			got := channel.checkOrigin(req)
			if got != tt.want {
				t.Errorf("checkOrigin() = %v, want %v (origin=%q, host=%q)", got, tt.want, tt.origin, tt.host)
			}
		})
	}
}

// TestNativeChannelChatSessionsMeta verifies the lightweight metadata endpoint
// (/api/v1/chat/sessions/meta). It must NOT load full history to check for
// messages — it only uses HasMessages/getters, and any evicted messages must
// stay in the evicted pool (not materialized).
func TestNativeChannelChatSessionsMeta(t *testing.T) {
	ts := newNativeTestServer(t)
	trackedSession := "native:" + ts.clientID + ":meta"
	emptySession := "native:" + ts.clientID + ":empty"
	ts.channel.auth.TrackSessionKey(ts.clientID, trackedSession)
	ts.channel.auth.TrackSessionKey(ts.clientID, emptySession)

	// trackedSession: has messages (and evicted ones).
	ts.loop.histories[trackedSession] = []providers.Message{
		{Role: "user", Content: "Recent user"},
		{Role: "assistant", Content: "Recent assistant"},
	}
	ts.loop.evicted[trackedSession] = []providers.Message{
		{Role: "user", Content: "Evicted user"},
	}
	// emptySession: tracked but never used → must be skipped.
	ts.loop.histories[emptySession] = nil

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/meta", nil)
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

	var foundTracked, foundEmpty bool
	for _, session := range payload.Sessions {
		if session.Key == trackedSession {
			foundTracked = true
		}
		if session.Key == emptySession {
			foundEmpty = true
		}
	}
	if !foundTracked {
		t.Fatalf("expected session %q in payload %#v", trackedSession, payload.Sessions)
	}
	if foundEmpty {
		t.Fatalf("empty session %q should not appear in payload %#v", emptySession, payload.Sessions)
	}

	// The meta endpoint must NOT materialize evicted history.
	if len(ts.loop.evicted[trackedSession]) != 1 {
		t.Fatalf("evicted pool mutated by handleChatSessionsMeta: %d remaining, want 1", len(ts.loop.evicted[trackedSession]))
	}
}

// TestNativeChannelChatSessionsMetaKindFilter verifies mode/kind filtering on
// the lightweight endpoint and its pagination behavior.
func TestNativeChannelChatSessionsMetaKindFilter(t *testing.T) {
	ts := newNativeTestServer(t)
	trackedSession := "native:" + ts.clientID + ":meta-kind"
	ts.channel.auth.TrackSessionKey(ts.clientID, trackedSession)
	ts.loop.histories[trackedSession] = []providers.Message{
		{Role: "user", Content: "Hello"},
	}

	// kind=chat filter should include it.
	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/meta?kind=chat", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	var payload ChatSessionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	var found bool
	for _, session := range payload.Sessions {
		if session.Key == trackedSession {
			found = true
		}
	}
	if !found {
		t.Fatalf("kind=chat filter should include %q, got %#v", trackedSession, payload.Sessions)
	}

	// kind=heartbeat filter should exclude it.
	req2, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/meta?kind=heartbeat", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req2.Header.Set("Authorization", "Bearer "+ts.token)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp2.Body.Close()

	var payload2 ChatSessionsResponse
	if err := json.NewDecoder(resp2.Body).Decode(&payload2); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	found = false
	for _, session := range payload2.Sessions {
		if session.Key == trackedSession {
			found = true
		}
	}
	if found {
		t.Fatalf("kind=heartbeat filter should NOT include %q, got %#v", trackedSession, payload2.Sessions)
	}
}
