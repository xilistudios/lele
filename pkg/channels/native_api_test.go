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

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/security"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/skills"
)

type nativeTestAgentLoop struct {
	config             *config.Config
	histories          map[string][]providers.Message
	evicted            map[string][]providers.Message
	sessionAgents      map[string]string
	sessionModels      map[string]string
	sessionFolders     map[string]string
	sessionAliases     map[string]string
	sessionAliasesMu   sync.RWMutex
	subagentParents    map[string]string
	workspace          string
	agentTools         []AgentToolInfo
	sessionNames       map[string]string
	sessionThinkLevels map[string]string
	sessionSubagents   map[string][]SubagentTaskInfo
	processing         map[string]bool
	inProgressTools    map[string]*session.InProgressTool

	groupSnapshotsBySession map[string][]group.GroupSnapshot
	groupSnapshotCalls      []string
	groupSnapshotMu         sync.Mutex
	allGroupSnapshotsCalls  int

	sessions *session.SessionManager

	attachmentMessageCalls []attachmentMessageCall
	attachmentMu           sync.Mutex

	hintedAgents []hintCall
	hintMu       sync.Mutex
}

type hintCall struct {
	sessionKey string
	agentID    string
}

func newNativeTestAgentLoop(cfg *config.Config) *nativeTestAgentLoop {
	return &nativeTestAgentLoop{
		config:             cfg,
		histories:          make(map[string][]providers.Message),
		evicted:            make(map[string][]providers.Message),
		sessionAgents:      make(map[string]string),
		sessionModels:      make(map[string]string),
		sessionFolders:     make(map[string]string),
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

func (m *nativeTestAgentLoop) HintSessionAgent(sessionKey, agentID string) {
	m.hintMu.Lock()
	m.hintedAgents = append(m.hintedAgents, hintCall{sessionKey: sessionKey, agentID: agentID})
	m.hintMu.Unlock()

	if m.GetSessionAgent(sessionKey) == agentID {
		return
	}
	m.sessionAgents[sessionKey] = agentID
}

func (m *nativeTestAgentLoop) HintedAgents() []hintCall {
	m.hintMu.Lock()
	defer m.hintMu.Unlock()
	out := make([]hintCall, len(m.hintedAgents))
	copy(out, m.hintedAgents)
	return out
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

func (m *nativeTestAgentLoop) ListAgentTools(agentID string) ([]AgentToolInfo, bool) {
	if agentID != "main" {
		return nil, false
	}
	if m.agentTools != nil {
		return m.agentTools, true
	}
	return []AgentToolInfo{
		{Name: "exec", Description: "Execute a shell command"},
		{Name: "read_file", Description: "Read the contents of a file"},
	}, true
}

func (m *nativeTestAgentLoop) GetSessionHistory(sessionKey string) []providers.Message {
	if m.sessions != nil {
		return m.sessions.GetHistory(sessionKey)
	}
	history := m.histories[sessionKey]
	result := make([]providers.Message, len(history))
	copy(result, history)
	return result
}

func (m *nativeTestAgentLoop) GetHistoryView(sessionKey string) []providers.Message {
	return m.histories[sessionKey]
}

func (m *nativeTestAgentLoop) GetEvictedMessageCount(sessionKey string) int {
	return len(m.evicted[sessionKey])
}

func (m *nativeTestAgentLoop) LoadEvictedMessagesPage(sessionKey string, before, after, limit int) *session.EvictedMessagesPage {
	evicted := m.evicted[sessionKey]
	if len(evicted) == 0 {
		return nil
	}
	end := len(evicted)
	if before >= 0 && before < end {
		end = before
	}
	if end <= 0 {
		return nil
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	page := &session.EvictedMessagesPage{
		Messages:     append([]providers.Message{}, evicted[start:end]...),
		FirstSeq:     start,
		LastSeq:      end - 1,
		HasOlder:     start > 0,
		EvictedCount: len(evicted),
		TotalCount:   len(m.histories[sessionKey]) + len(evicted),
	}
	page.Seqs = make([]int, end-start)
	for i := range page.Seqs {
		page.Seqs[i] = start + i
	}
	return page
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

func (m *nativeTestAgentLoop) GetSessionFolder(sessionKey string) string {
	if folder, ok := m.sessionFolders[sessionKey]; ok {
		return folder
	}
	return ""
}

func (m *nativeTestAgentLoop) SetSessionFolder(sessionKey, folder string) string {
	m.sessionFolders[sessionKey] = folder
	return folder
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

func (m *nativeTestAgentLoop) GetSessionModelSupportsVideo(sessionKey string) bool {
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
			return modelCfg.Video
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

func (m *nativeTestAgentLoop) InterruptAgent(sessionKey string) string {
	return "interrupted"
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

func (m *nativeTestAgentLoop) GetEffectiveThinkLevel(sessionKey string) string {
	return m.GetThinkLevel(sessionKey)
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
			Key:         key,
			Name:        m.sessionNames[key],
			Mode:        m.GetSessionMode(key),
			Folder:      m.GetSessionFolder(key),
			Kind:        classifySessionKeyKind(key),
			HasMessages: m.HasMessages(key),
		})
	}
	return result
}

func (m *nativeTestAgentLoop) AppendAssistantChunk(sessionKey, chunk string) {}

func (m *nativeTestAgentLoop) AppendReasoningChunk(sessionKey, chunk string) {}

func (m *nativeTestAgentLoop) FinalizeAssistantMessage(sessionKey string) {}

type attachmentMessageCall struct {
	sessionKey  string
	content     string
	attachments []providers.MessageAttachment
}

func (m *nativeTestAgentLoop) AddAttachmentMessage(sessionKey, content string, attachments []providers.MessageAttachment) {
	m.attachmentMu.Lock()
	m.attachmentMessageCalls = append(m.attachmentMessageCalls, attachmentMessageCall{sessionKey: sessionKey, content: content, attachments: attachments})
	m.attachmentMu.Unlock()

	if m.sessions != nil {
		m.sessions.AddAttachmentMessage(sessionKey, content, attachments)
	}
}

func (m *nativeTestAgentLoop) attachmentCalls() []attachmentMessageCall {
	m.attachmentMu.Lock()
	defer m.attachmentMu.Unlock()
	out := make([]attachmentMessageCall, len(m.attachmentMessageCalls))
	copy(out, m.attachmentMessageCalls)
	return out
}

func (m *nativeTestAgentLoop) HasStreamedContent(sessionKey string) bool {
	return false
}

func (m *nativeTestAgentLoop) GetInProgressAssistant(sessionKey string) *providers.Message {
	return nil
}

func (m *nativeTestAgentLoop) GetInProgressTool(sessionKey string) *session.InProgressTool {
	if m.inProgressTools == nil {
		return nil
	}
	tool := m.inProgressTools[sessionKey]
	if tool == nil {
		return nil
	}
	return tool.Clone()
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

func (m *nativeTestAgentLoop) GroupSnapshotsForSession(sessionKey string) []group.GroupSnapshot {
	m.groupSnapshotMu.Lock()
	defer m.groupSnapshotMu.Unlock()
	m.groupSnapshotCalls = append(m.groupSnapshotCalls, sessionKey)
	return m.groupSnapshotsBySession[sessionKey]
}

func (m *nativeTestAgentLoop) setGroupSnapshotsForSession(sessionKey string, snaps ...group.GroupSnapshot) {
	m.groupSnapshotMu.Lock()
	defer m.groupSnapshotMu.Unlock()
	if m.groupSnapshotsBySession == nil {
		m.groupSnapshotsBySession = make(map[string][]group.GroupSnapshot)
	}
	m.groupSnapshotsBySession[sessionKey] = snaps
}

func (m *nativeTestAgentLoop) groupSnapshotCallCount() int {
	m.groupSnapshotMu.Lock()
	defer m.groupSnapshotMu.Unlock()
	return len(m.groupSnapshotCalls)
}

func (m *nativeTestAgentLoop) allGroupSnapshotsCallCount() int {
	m.groupSnapshotMu.Lock()
	defer m.groupSnapshotMu.Unlock()
	return m.allGroupSnapshotsCalls
}

func (m *nativeTestAgentLoop) ResolveRoute(channel, peerKind, peerID string) string {
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

	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIKey: "test-openai-key"},
			Models: map[string]config.ProviderModelConfig{
				"gpt-4":       {Model: "gpt-4"},
				"gpt-4o":      {Model: "gpt-4o"},
				"gpt-4o-mini": {Model: "gpt-4o-mini"},
			},
		},
		"anthropic": {
			Type:           "anthropic",
			ProviderConfig: config.ProviderConfig{APIKey: "test-anthropic-key"},
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
		base:             NewBaseChannel(ChannelName, cfg.Channels.Native, msgBus, []string{}),
		cfg:              &cfg.Channels.Native,
		auth:             auth,
		bus:              msgBus,
		agentLoop:        loop,
		wsClients:        make(map[string]*WSClient),
		pinLimiter:       newRateLimiter(10, time.Minute),
		pairLimiter:      newRateLimiter(5, time.Minute),
		refreshLimiter:   newRateLimiter(20, time.Minute),
		authLogLimiter:   newRateLimiter(6, time.Minute),
		apiLimiter:       newRateLimiter(120, time.Minute),
		wsMessageLimiter: newRateLimiter(30, time.Minute),
		skillsLoader:     &skills.SkillsLoader{},
	}

	mux := http.NewServeMux()
	channel.RegisterRoutes(mux)
	server := httptest.NewServer(security.Middleware()(mux))

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

	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIKey: "test-openai-key"},
			Models: map[string]config.ProviderModelConfig{
				"gpt-4":       {Model: "gpt-4"},
				"gpt-4o":      {Model: "gpt-4o"},
				"gpt-4o-mini": {Model: "gpt-4o-mini"},
			},
		},
		"anthropic": {
			Type:           "anthropic",
			ProviderConfig: config.ProviderConfig{APIKey: "test-anthropic-key"},
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
		base:             NewBaseChannel(ChannelName, cfg.Channels.Native, msgBus, []string{}),
		cfg:              &cfg.Channels.Native,
		auth:             auth,
		bus:              msgBus,
		agentLoop:        loop,
		wsClients:        make(map[string]*WSClient),
		pinLimiter:       newRateLimiter(10, time.Minute),
		pairLimiter:      newRateLimiter(5, time.Minute),
		refreshLimiter:   newRateLimiter(20, time.Minute),
		authLogLimiter:   newRateLimiter(6, time.Minute),
		apiLimiter:       newRateLimiter(120, time.Minute),
		wsMessageLimiter: newRateLimiter(30, time.Minute),
		configPath:       configPath,
		skillsLoader:     &skills.SkillsLoader{},
	}

	mux := http.NewServeMux()
	channel.RegisterRoutes(mux)
	server := httptest.NewServer(security.Middleware()(mux))

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

func TestHandleTools_ReadVideoGating(t *testing.T) {
	ts := newNativeTestServer(t)

	fetchToolNames := func(t *testing.T, sessionKey string) map[string]bool {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/tools?session_key="+url.QueryEscape(sessionKey), nil)
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
		var payload ToolsResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		names := make(map[string]bool, len(payload.Tools))
		for _, tool := range payload.Tools {
			names[tool.Name] = true
		}
		return names
	}

	for _, key := range []string{"native:gating-no-video", "native:gating-video", "native:gating-vision"} {
		ts.channel.auth.TrackSessionKey(ts.clientID, key)
	}

	noVideo := fetchToolNames(t, "native:gating-no-video")
	if noVideo["read_video"] {
		t.Fatal("expected read_video to be absent for a model without video: true and vision: true")
	}

	prov := ts.loop.config.Providers.Named["openai"]
	prov.Models["video-model"] = config.ProviderModelConfig{Model: "video-actual", Video: true}
	prov.Models["vision-model"] = config.ProviderModelConfig{Model: "vision-actual", Vision: true}
	ts.loop.config.Providers.Named["openai"] = prov
	ts.loop.SetSessionModel("native:gating-video", "video-model")
	ts.loop.SetSessionModel("native:gating-vision", "vision-model")

	withVideo := fetchToolNames(t, "native:gating-video")
	if !withVideo["read_video"] {
		t.Fatal("expected read_video to be present for a model with video: true")
	}
	if withVideo["read_image"] {
		t.Fatal("expected read_image to stay gated by vision, not video")
	}

	withVision := fetchToolNames(t, "native:gating-vision")
	if !withVision["read_video"] {
		t.Fatal("expected read_video to be present for a model with vision: true")
	}
	if !withVision["read_image"] {
		t.Fatal("expected read_image to be present for a model with vision: true")
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

	subagentSessionKey := sessionKey + ":subagent-1"
	ts.loop.histories[subagentSessionKey] = []providers.Message{
		{Role: "user", Content: "Subagent task"},
		{Role: "assistant", Content: "Subagent result"},
	}

	ts.loop.subagentParents[sessionKey+":subagent-1"] = sessionKey

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

func TestChatHistory_ServesEvictedMessagesOnDemand(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID + ":evicted-history"
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)

	ts.loop.evicted[sessionKey] = []providers.Message{
		{Role: "user", Content: "Evicted-1"},
		{Role: "assistant", Content: "Evicted-2"},
	}
	ts.loop.histories[sessionKey] = []providers.Message{
		{Role: "user", Content: "Recent-1"},
		{Role: "assistant", Content: "Recent-2"},
	}

	get := func(query string) *ChatHistoryResponse {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.QueryEscape(sessionKey)+"/history"+query, nil)
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
		return &payload
	}

	payload := get("?limit=2")
	if len(payload.Messages) != 2 {
		t.Fatalf("page1 len(messages) = %d, want 2 (resident only)", len(payload.Messages))
	}
	if payload.Messages[0].Content != "Recent-1" || payload.Messages[1].Content != "Recent-2" {
		t.Fatalf("page1 messages = [%q, %q], want [Recent-1, Recent-2]", payload.Messages[0].Content, payload.Messages[1].Content)
	}
	if !payload.HasMore {
		t.Fatalf("page1 has_more = false, want true (older evicted messages exist)")
	}
	if len(ts.loop.evicted[sessionKey]) != 2 {
		t.Fatalf("evicted pool mutated: %d remaining, want 2 (on-demand must not materialize)", len(ts.loop.evicted[sessionKey]))
	}
	if len(ts.loop.histories[sessionKey]) != 2 {
		t.Fatalf("in-memory history = %d messages, want 2 (must stay untouched)", len(ts.loop.histories[sessionKey]))
	}

	beforeID := payload.Messages[0].ID
	payload2 := get("?limit=2&before_id=" + url.QueryEscape(beforeID))
	if len(payload2.Messages) != 2 {
		t.Fatalf("page2 len(messages) = %d, want 2", len(payload2.Messages))
	}
	if payload2.Messages[0].Content != "Evicted-1" || payload2.Messages[1].Content != "Evicted-2" {
		t.Fatalf("page2 messages = [%q, %q], want [Evicted-1, Evicted-2]", payload2.Messages[0].Content, payload2.Messages[1].Content)
	}
	if payload2.HasMore {
		t.Fatalf("page2 has_more = true, want false (reached oldest)")
	}
	if payload2.Messages[0].ID != "evicted:0" || payload2.Messages[1].ID != "evicted:1" {
		t.Fatalf("page2 ids = [%q, %q], want [evicted:0, evicted:1]", payload2.Messages[0].ID, payload2.Messages[1].ID)
	}
	if len(ts.loop.histories[sessionKey]) != 2 {
		t.Fatalf("in-memory history = %d messages after evicted page, want 2 (never materialized)", len(ts.loop.histories[sessionKey]))
	}

	payload3 := get("?limit=2&before_id=evicted:0")
	if len(payload3.Messages) != 0 {
		t.Fatalf("page3 len(messages) = %d, want 0 (no messages older than evicted:0)", len(payload3.Messages))
	}
	if payload3.HasMore {
		t.Fatalf("page3 has_more = true, want false")
	}
	if len(ts.loop.evicted[sessionKey]) != 2 {
		t.Fatalf("evicted pool mutated after full walk: %d remaining, want 2", len(ts.loop.evicted[sessionKey]))
	}
}

func TestChatSessions_CountIncludesEvicted(t *testing.T) {
	ts := newNativeTestServer(t)
	trackedSession := "native:" + ts.clientID + ":evicted-count"
	ts.channel.auth.TrackSessionKey(ts.clientID, trackedSession)

	ts.loop.histories[trackedSession] = []providers.Message{
		{Role: "user", Content: "Recent user"},
		{Role: "assistant", Content: "Recent assistant"},
	}
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

	if len(ts.loop.evicted[trackedSession]) != 2 {
		t.Fatalf("evicted pool mutated by handleChatSessions: %d remaining, want 2", len(ts.loop.evicted[trackedSession]))
	}
}

func TestNativeChannelChatSessionsKindFilter(t *testing.T) {
	ts := newNativeTestServer(t)

	userSession := "native:" + ts.clientID + ":user-chat"
	ts.channel.auth.TrackSessionKey(ts.clientID, userSession)

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

func TestNativeChannelCreateSessionRejectsForeignKey(t *testing.T) {
	ts := newNativeTestServer(t)
	otherClientID, _ := pairNativeTestClient(t, ts, "Other Device")

	createSession := func(t *testing.T, sessionKey string) int {
		t.Helper()
		body, _ := json.Marshal(CreateSessionRequest{SessionKey: sessionKey})
		resp := nativeAuthedRequest(t, http.MethodPost, ts.server.URL+"/api/v1/chat/sessions", ts.token, body)
		defer resp.Body.Close()
		return resp.StatusCode
	}

	t.Run("another_known_client_prefix", func(t *testing.T) {
		key := "native:" + otherClientID + ":123"
		if status := createSession(t, key); status != http.StatusForbidden {
			t.Fatalf("status = %d, want %d for %q", status, http.StatusForbidden, key)
		}
		if keys := clientSessionKeys(t, ts, ts.clientID); containsKey(keys, key) {
			t.Fatalf("rejected create still tracked the key: %v", keys)
		}
	})

	t.Run("key_with_existing_messages", func(t *testing.T) {
		key := uuid.New().String()
		ts.loop.histories[key] = []providers.Message{{Role: "user", Content: "secret"}}
		if status := createSession(t, key); status != http.StatusForbidden {
			t.Fatalf("status = %d, want %d for %q", status, http.StatusForbidden, key)
		}
	})

	t.Run("system_kind_shape", func(t *testing.T) {
		if status := createSession(t, "heartbeat"); status != http.StatusForbidden {
			t.Fatalf("status = %d, want %d for heartbeat", status, http.StatusForbidden)
		}
	})

	for _, key := range []string{
		"agent:main:telegram:direct:777001",
		"telegram:777001",
		"agent:main:telegram:group:-100200",
		"agent:main:discord:direct:4242",
		"agent:main:telegram:direct:888002:chat:1",
		"cron-not-a-real-job",
		"arbitrary-bare-token",
	} {
		t.Run("preclaim_"+key, func(t *testing.T) {
			if status := createSession(t, key); status != http.StatusForbidden {
				t.Fatalf("status = %d, want %d for %q", status, http.StatusForbidden, key)
			}
			if keys := clientSessionKeys(t, ts, ts.clientID); containsKey(keys, key) {
				t.Fatalf("rejected pre-claim still tracked the key: %v", keys)
			}
		})
	}

	t.Run("fresh_uuid", func(t *testing.T) {
		key := uuid.New().String()
		if status := createSession(t, key); status != http.StatusCreated {
			t.Fatalf("status = %d, want %d for %q", status, http.StatusCreated, key)
		}
		if keys := clientSessionKeys(t, ts, ts.clientID); !containsKey(keys, key) {
			t.Fatalf("created session %q not tracked: %v", key, keys)
		}
	})
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
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

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

	if native["host"] != "127.0.0.1" {
		t.Errorf("host = %v, want 127.0.0.1 (default)", native["host"])
	}

	if native["port"] != float64(18790) {
		t.Errorf("port = %v, want 18790 (default)", native["port"])
	}

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

	ts.loop.sessionAliasesMu.Lock()
	ts.loop.sessionAliases[baseSessionKey] = aliasedSessionKey
	ts.loop.sessionAliasesMu.Unlock()

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
		"event": "subscribe",
		"data": map[string]interface{}{
			"session_key": baseSessionKey,
		},
	}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	ack := readWSMessage(t, conn)
	if ack.Event != "subscribe.ack" {
		t.Fatalf("ack event = %q, want subscribe.ack", ack.Event)
	}

	ts.channel.Send(context.Background(), bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    aliasedSessionKey,
		Content:   "Message from aliased session",
		MessageID: "msg-aliased-1",
	})

	stream := readWSMessage(t, conn)
	if stream.Event != "message.stream" {
		t.Fatalf("stream event = %q, want message.stream", stream.Event)
	}
	var streamPayload WSStreamPayload
	decodeWSData(t, stream.Data, &streamPayload)
	if streamPayload.MessageID != "msg-aliased-1" || streamPayload.SessionKey != aliasedSessionKey {
		t.Fatalf("stream payload = %#v, want msg-aliased-1/%s", streamPayload, aliasedSessionKey)
	}

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

	found := false
	for _, f := range payload.Files {
		if f.Name == "AGENT.md" {
			found = true
			if f.Size != 12 {
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
		base:             NewBaseChannel(ChannelName, cfg.Channels.Native, msgBus, []string{}),
		cfg:              &cfg.Channels.Native,
		auth:             auth,
		bus:              msgBus,
		agentLoop:        loop,
		wsClients:        make(map[string]*WSClient),
		pinLimiter:       newRateLimiter(10, time.Minute),
		pairLimiter:      newRateLimiter(5, time.Minute),
		refreshLimiter:   newRateLimiter(20, time.Minute),
		authLogLimiter:   newRateLimiter(6, time.Minute),
		apiLimiter:       newRateLimiter(120, time.Minute),
		wsMessageLimiter: newRateLimiter(30, time.Minute),
		skillsLoader:     &skills.SkillsLoader{},
	}

	tests := []struct {
		name     string
		origin   string
		host     string
		bindHost string
		want     bool
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
		{
			name:     "wildcard bind host does not allow arbitrary origins",
			origin:   "http://evil.example.com",
			host:     "192.168.0.171:18790",
			bindHost: "0.0.0.0",
			want:     false,
		},
		{
			name:   "localhost prefix bypass (localhost.evil.com) rejected",
			origin: "http://localhost.evil.com",
			host:   "127.0.0.1:18790",
			want:   false,
		},
		{
			name:   "127.0.0.1 prefix bypass rejected",
			origin: "http://127.0.0.1.evil.com",
			host:   "127.0.0.1:18790",
			want:   false,
		},
		{
			name:   "tauri prefix bypass rejected",
			origin: "tauri://evil",
			host:   "127.0.0.1:18790",
			want:   false,
		},
		{
			name:   "exact tauri origin allowed",
			origin: "tauri://localhost",
			host:   "127.0.0.1:18790",
			want:   true,
		},
		{
			name:   "localhost with port allowed (exact hostname)",
			origin: "http://localhost:3005",
			host:   "127.0.0.1:18790",
			want:   true,
		},
		{
			name:     "bind host with different port allowed (hostname match)",
			origin:   "http://192.168.0.171:3005",
			host:     "192.168.0.171:18790",
			bindHost: "192.168.0.171",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "cross-origin allowed via CORSOrigins config" {
				channel.cfg.CORSOrigins = []string{"http://custom.example.com"}
			} else {
				channel.cfg.CORSOrigins = nil
			}
			if tt.bindHost != "" {
				channel.cfg.Host = tt.bindHost
			} else {
				channel.cfg.Host = "127.0.0.1"
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

func TestNativeChannelChatSessionsMeta(t *testing.T) {
	ts := newNativeTestServer(t)
	trackedSession := "native:" + ts.clientID + ":meta"
	emptySession := "native:" + ts.clientID + ":empty"
	ts.channel.auth.TrackSessionKey(ts.clientID, trackedSession)
	ts.channel.auth.TrackSessionKey(ts.clientID, emptySession)

	ts.loop.histories[trackedSession] = []providers.Message{
		{Role: "user", Content: "Recent user"},
		{Role: "assistant", Content: "Recent assistant"},
	}
	ts.loop.evicted[trackedSession] = []providers.Message{
		{Role: "user", Content: "Evicted user"},
	}
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

	if len(ts.loop.evicted[trackedSession]) != 1 {
		t.Fatalf("evicted pool mutated by handleChatSessionsMeta: %d remaining, want 1", len(ts.loop.evicted[trackedSession]))
	}
}

func TestNativeChannelChatSessionsMetaKindFilter(t *testing.T) {
	ts := newNativeTestServer(t)
	trackedSession := "native:" + ts.clientID + ":meta-kind"
	ts.channel.auth.TrackSessionKey(ts.clientID, trackedSession)
	ts.loop.histories[trackedSession] = []providers.Message{
		{Role: "user", Content: "Hello"},
	}

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

func TestChatHistory_DuplicateContentGetsDistinctIDs(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID + ":dup-content"
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)

	ts.loop.histories[sessionKey] = []providers.Message{
		{Role: "user", Content: "same question"},
		{Role: "assistant", Content: "same answer"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: "same answer"},
	}

	get := func(query string) *ChatHistoryResponse {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.QueryEscape(sessionKey)+"/history"+query, nil)
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
		return &payload
	}

	payload := get("?limit=50")

	if len(payload.Messages) != 4 {
		t.Fatalf("len(messages) = %d, want 4", len(payload.Messages))
	}
	wantRoles := []string{"user", "assistant", "user", "assistant"}
	for i, want := range wantRoles {
		if payload.Messages[i].Role != want {
			t.Fatalf("messages[%d].Role = %q, want %q", i, payload.Messages[i].Role, want)
		}
	}

	ids := make([]string, 4)
	for i, m := range payload.Messages {
		ids[i] = m.ID
	}

	seen := make(map[string]bool, 4)
	for i, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id at index %d: %q", i, id)
		}
		seen[id] = true
	}

	expectedKey := residentMessageKey(ts.loop.histories[sessionKey][1])
	expectedFirst := sessionKey + ":" + expectedKey
	if ids[1] != expectedFirst {
		t.Fatalf("messages[1].ID = %q, want %q (legacy format)", ids[1], expectedFirst)
	}

	expectedSecond := expectedFirst + "-1"
	if ids[3] != expectedSecond {
		t.Fatalf("messages[3].ID = %q, want %q", ids[3], expectedSecond)
	}

	payload2 := get("?limit=50&before_id=" + url.QueryEscape(ids[3]))
	if len(payload2.Messages) != 3 {
		t.Fatalf("before_id=ids[3] len(messages) = %d, want 3", len(payload2.Messages))
	}

	payload3 := get("?limit=50&before_id=" + url.QueryEscape(ids[1]))
	if len(payload3.Messages) != 1 {
		t.Fatalf("before_id=ids[1] len(messages) = %d, want 1", len(payload3.Messages))
	}
	if payload3.Messages[0].Content != "same question" {
		t.Fatalf("before_id=ids[1] messages[0].Content = %q, want %q", payload3.Messages[0].Content, "same question")
	}
}

func TestSetReloadMCP_NilSafe(t *testing.T) {
	n := &NativeChannel{}

	n.SetReloadMCP(nil)
	if n.reloadMCP != nil {
		n.reloadMCP()
	}

	calls := 0
	n.SetReloadMCP(func() { calls++ })
	if n.reloadMCP != nil {
		n.reloadMCP()
	}
	if calls != 1 {
		t.Fatalf("wired reloadMCP invoked %d times, want 1", calls)
	}

	n.SetReloadMCP(nil)
	if n.reloadMCP != nil {
		n.reloadMCP()
	}
	if calls != 1 {
		t.Errorf("reloadMCP still invoked %d times after SetReloadMCP(nil), want 1", calls)
	}
}
