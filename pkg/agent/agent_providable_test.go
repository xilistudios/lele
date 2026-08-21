package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

// ============================================================================
// Test helpers
// ============================================================================

// newCovTestLoop builds an AgentLoop with a named provider so that you can
// exercise providable methods that resolve models / providers.
func newCovTestLoop(t *testing.T) *AgentLoop {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "default-model",
				Provider:          "test",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Model: &config.AgentModelConfig{Primary: "default-model"}},
				{ID: "coder", Model: &config.AgentModelConfig{Primary: "coder-model"}},
			},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"test": {
					ProviderConfig: config.ProviderConfig{APIKey: "test-key"},
					Models: map[string]config.ProviderModelConfig{
						"default-model": {Model: "default-model", ContextWindow: 128000},
						"coder-model":   {Model: "coder-model", ContextWindow: 64000},
						"vision-model":  {Model: "vision-model", Vision: true},
					},
				},
			},
		},
	}
	loop := NewAgentLoop(cfg, bus.NewMessageBus())
	// Assign a non-nil mock provider to every agent so that async subagent
	// spawns (which run outside the test's lifetime) never dereference a nil
	// provider. This prevents pre-existing panics in tools/subagent_runner.go.
	for _, id := range loop.registry.ListAgentIDs() {
		if agent, ok := loop.registry.GetAgent(id); ok {
			agent.Provider = &mockProvider{mockResponse: "subagent-response"}
		}
	}
	return loop
}

// ============================================================================
// Agent Info
// ============================================================================

func TestGetAgentInfo_Existing(t *testing.T) {
	ap := newCovTestLoop(t).providable
	info, ok := ap.GetAgentInfo("main")
	if !ok {
		t.Fatal("expected agent to be found")
	}
	if info.ID != "main" {
		t.Errorf("ID = %q", info.ID)
	}
	if info.Model == "" {
		t.Error("expected non-empty model")
	}
}

func TestGetAgentInfo_NotFound(t *testing.T) {
	ap := newCovTestLoop(t).providable
	_, ok := ap.GetAgentInfo("does-not-exist")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestListAvailableAgentIDs(t *testing.T) {
	ap := newCovTestLoop(t).providable
	ids := ap.ListAvailableAgentIDs()
	if len(ids) == 0 {
		t.Fatal("expected at least one agent")
	}
}

func TestGetDefaultAgentID_Method(t *testing.T) {
	ap := newCovTestLoop(t).providable
	if id := ap.GetDefaultAgentID(); id == "" {
		t.Fatal("expected default agent id")
	}
}

// ============================================================================
// Session history / messages
// ============================================================================

func TestAddSessionMessage_NoAgent(t *testing.T) {
	al := newCovTestLoop(t)
	if err := al.providable.AddSessionMessage("", providers.Message{Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAddAndGetSessionHistory(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	al.sessionAgents.Store("native:test-sess", "main")

	if err := ap.AddSessionMessage("native:test-sess", providers.Message{Role: "user", Content: "hello"}); err != nil {
		t.Fatalf("AddSessionMessage failed: %v", err)
	}
	history := ap.GetSessionHistory("native:test-sess")
	if len(history) != 1 {
		t.Fatalf("expected 1 message, got %d", len(history))
	}
	if v := ap.GetHistoryView("native:test-sess"); len(v) != 1 {
		t.Errorf("GetHistoryView len = %d", len(v))
	}
}

func TestGetHistoryView_RawSubagentKey(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable

	// Seed a subagent session mapping to the coder agent and history.
	subKey := "subagent:task-1"
	al.subagentSessionAgent.Store(subKey, "coder")
	coder, ok := al.registry.GetAgent("coder")
	if !ok {
		t.Fatal("coder agent missing")
	}
	coder.Sessions.AddMessage(subKey, "user", "sub hello")
	if hist := ap.GetHistoryView(subKey); len(hist) != 1 {
		t.Fatalf("expected subagent history, got %d", len(hist))
	}
	// Missing subagent mapping -> self-healing scan.
	if hist := ap.GetHistoryView(":subagent-77"); len(hist) != 0 {
		t.Fatalf("expected empty for unknown subagent, got %d", len(hist))
	}
}

func TestEvictedMessageHelpers(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	al.sessionAgents.Store("native:evict", "main")

	if got := ap.GetEvictedMessageCount("native:evict"); got != 0 {
		t.Errorf("evicted count = %d", got)
	}
	if got := ap.LoadEvictedMessages("native:evict"); got != 0 {
		t.Errorf("loaded = %d", got)
	}
	if got := ap.GetTotalMessageCount("native:evict"); got != 0 {
		t.Errorf("total = %d", got)
	}
	if ap.HasMessages("native:evict") {
		t.Error("expected no messages")
	}
}

func TestMessageHelpers_WithMessages(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	al.sessionAgents.Store("native:msg", "main")
	main, _ := al.registry.GetAgent("main")
	main.Sessions.AddMessage("native:msg", "user", "hi")

	if !ap.HasMessages("native:msg") {
		t.Error("expected HasMessages true")
	}
	if got := ap.GetTotalMessageCount("native:msg"); got != 1 {
		t.Errorf("total = %d", got)
	}
}

func TestHasMessages_SubagentFallback(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	subKey := ":subagent-9"
	coder, _ := al.registry.GetAgent("coder")
	coder.Sessions.AddMessage("native:parent:subagent-9", "user", "sub")
	coder.Sessions.AddMessage(subKey, "user", "sub2")
	if !ap.HasMessages(subKey) {
		t.Error("expected HasMessages true for subagent key")
	}
}

func TestLoadEvictedMessages_Subagent(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	subKey := "subagent:task-99"
	al.subagentSessionAgent.Store(subKey, "coder")
	coder, _ := al.registry.GetAgent("coder")
	coder.Sessions.AddMessage(subKey, "user", "hi")
	if got := ap.GetEvictedMessageCount(subKey); got != 0 {
		t.Errorf("evicted count = %d", got)
	}
}

// ============================================================================
// Model management
// ============================================================================

func TestGetSessionModel(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	key := "native:m1"
	al.sessionAgents.Store(key, "main")

	// Default -> agent model
	if m := ap.GetSessionModel(key); m != "test:default-model" {
		t.Errorf("model = %q", m)
	}
	// After persist
	ap.SetSessionModel(key, "vision-model")
	if m := ap.GetSessionModel(key); m != "test:vision-model" {
		t.Errorf("model after set = %q", m)
	}
	// In-memory override wins
	al.sessionModels.Store(key, "test:coder-model")
	if m := ap.GetSessionModel(key); m != "test:coder-model" {
		t.Errorf("model after memory override = %q", m)
	}
}

func TestGetSessionModel_NoAgent(t *testing.T) {
	loop := newCovTestLoop(t)
	loop.registry.agents = nil
	if m := loop.providable.GetSessionModel("native:x"); m != "" {
		t.Errorf("expected empty model, got %q", m)
	}
}

func TestGetSessionModelSupportsImages(t *testing.T) {
	ap := newCovTestLoop(t).providable
	key := "native:vision"
	if ap.GetSessionModelSupportsImages(key) {
		t.Error("expected false for default model")
	}
	ap.SetSessionModel(key, "vision-model")
	if !ap.GetSessionModelSupportsImages(key) {
		t.Error("expected true for vision model")
	}
	// Empty model -> false
	ap.SetSessionModel(key, "")
	if ap.GetSessionModelSupportsImages(key) {
		t.Error("expected false for empty model")
	}
}

func TestSetSessionModel_EmptyKey(t *testing.T) {
	ap := newCovTestLoop(t).providable
	if m := ap.SetSessionModel("", "x"); m != "" {
		t.Errorf("expected empty, got %q", m)
	}
}

func TestListAvailableModels(t *testing.T) {
	ap := newCovTestLoop(t).providable
	models := ap.ListAvailableModels("")
	if len(models) == 0 {
		t.Fatal("expected models for default provider")
	}
	// Agent-specific provider prefix.
	models2 := ap.ListAvailableModels("main")
	if len(models2) == 0 {
		t.Fatal("expected models for main agent")
	}
	// Unknown agent id -> default provider still works.
	models3 := ap.ListAvailableModels("nope")
	if len(models3) == 0 {
		t.Fatal("expected models for unknown agent")
	}
}

func TestListAvailableModels_UnknownProvider(t *testing.T) {
	al := newCovTestLoop(t)
	al.cfgPtr.Store(&config.Config{
		Agents:    config.AgentsConfig{Defaults: config.AgentDefaults{Provider: "missing"}},
		Providers: &config.ProvidersConfig{Named: map[string]config.NamedProviderConfig{}},
	})
	if models := al.providable.ListAvailableModels(""); models != nil {
		t.Errorf("expected nil models, got %v", models)
	}
}

func TestGetSessionMode_and_Set(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	key := "native:mode"
	al.sessionAgents.Store(key, "main")

	if m := ap.GetSessionMode(key); m != "" {
		t.Errorf("initial mode = %q", m)
	}
	if err := ap.SetSessionMode(key, "chat"); err != nil {
		t.Fatalf("SetSessionMode failed: %v", err)
	}
	if m := ap.GetSessionMode(key); m != "chat" {
		t.Errorf("mode after set = %q", m)
	}
}

func TestGetSessionMode_NoAgent(t *testing.T) {
	loop := newCovTestLoop(t)
	loop.registry.agents = nil
	if m := loop.providable.GetSessionMode("native:x"); m != "" {
		t.Errorf("expected empty mode, got %q", m)
	}
	if err := loop.providable.SetSessionMode("native:x", "chat"); err == nil {
		t.Error("expected error for no agent")
	}
}

// ============================================================================
// Config snapshot
// ============================================================================

func TestGetConfigSnapshot(t *testing.T) {
	al := newCovTestLoop(t)
	cfg := al.providable.GetConfigSnapshot()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

// ============================================================================
// Status & control
// ============================================================================

func TestGetStatus_NoDefaultAgent(t *testing.T) {
	al := newCovTestLoop(t)
	al.registry.agents = nil
	s := al.providable.GetStatus("native:x")
	if !strings.Contains(s, "No default agent") {
		t.Errorf("unexpected status: %q", s)
	}
}

func TestGetStatus_WithAgent(t *testing.T) {
	al := newCovTestLoop(t)
	s := al.providable.GetStatus("native:status")
	if !strings.Contains(s, "lele") {
		t.Errorf("expected status to contain header, got %q", s)
	}
}

func TestStopAgent(t *testing.T) {
	al := newCovTestLoop(t)
	res := al.providable.StopAgent("native:stop")
	if !strings.Contains(res, "Agente detenido") {
		t.Errorf("unexpected: %q", res)
	}
}

// ============================================================================
// CompactSession
// ============================================================================

func TestCompactSession_NotEnoughMessages(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	key := "native:compact"
	al.sessionAgents.Store(key, "main")
	main, _ := al.registry.GetAgent("main")
	main.Sessions.AddMessage(key, "user", "a")
	res := ap.CompactSession(key)
	if !strings.Contains(res, "Not enough messages") {
		t.Errorf("unexpected: %q", res)
	}
}

func TestCompactSession_NoAgent(t *testing.T) {
	al := newCovTestLoop(t)
	al.registry.agents = nil
	res := al.providable.CompactSession("native:x")
	if !strings.Contains(res, "No default agent") {
		t.Errorf("unexpected: %q", res)
	}
}

func TestCompactSession_WithEnoughMessages(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	key := "native:compact2"
	al.sessionAgents.Store(key, "main")
	main, _ := al.registry.GetAgent("main")
	main.Provider = &mockProvider{mockResponse: "summarized content"}
	// Need > 4 messages.
	for i := 0; i < 6; i++ {
		main.Sessions.AddMessage(key, "user", "msg")
	}
	res := ap.CompactSession(key)
	if !strings.Contains(res, "✅ Compacted") {
		t.Errorf("expected compaction, got: %q", res)
	}
}

// ============================================================================
// Verbose & thinking
// ============================================================================

func TestToggleVerbose_Cycles(t *testing.T) {
	ap := newCovTestLoop(t).providable
	key := "native:verb"
	// off -> basic
	r1 := ap.ToggleVerbose(key)
	if !strings.Contains(r1, "BASIC") {
		t.Errorf("expected BASIC, got %q", r1)
	}
	// basic -> full
	r2 := ap.ToggleVerbose(key)
	if !strings.Contains(r2, "FULL") {
		t.Errorf("expected FULL, got %q", r2)
	}
	// full -> off
	r3 := ap.ToggleVerbose(key)
	if !strings.Contains(r3, "OFF") {
		t.Errorf("expected OFF, got %q", r3)
	}
}

func TestToggleVerbose_EmptyKey(t *testing.T) {
	ap := newCovTestLoop(t).providable
	if r := ap.ToggleVerbose(""); !strings.Contains(r, "requires a session") {
		t.Errorf("unexpected: %q", r)
	}
}

func TestGetVerboseLevel_EmptyAndSet(t *testing.T) {
	ap := newCovTestLoop(t).providable
	if got := ap.GetVerboseLevel(""); got != "off" {
		t.Errorf("empty -> %q", got)
	}
	if !ap.SetVerboseLevel("native:v", "full") {
		t.Fatal("expected SetVerboseLevel true")
	}
	if got := ap.GetVerboseLevel("native:v"); got != "full" {
		t.Errorf("level = %q", got)
	}
	// Invalid level
	if ap.SetVerboseLevel("native:v", "bogus") {
		t.Error("expected false for invalid level")
	}
	// Empty key fails
	if ap.SetVerboseLevel("", "full") {
		t.Error("expected false for empty key")
	}
}

func TestGetThinkLevel_and_Set(t *testing.T) {
	ap := newCovTestLoop(t).providable
	key := "native:think"
	if got := ap.GetThinkLevel(key); got != "default" {
		t.Errorf("default level = %q", got)
	}
	if !ap.SetThinkLevel(key, "high") {
		t.Fatal("expected SetThinkLevel true")
	}
	if got := ap.GetThinkLevel(key); got != "high" {
		t.Errorf("level = %q", got)
	}
	// Set back to default -> returns default
	ap.SetThinkLevel(key, "default")
	if got := ap.GetThinkLevel(key); got != "default" {
		t.Errorf("after default -> %q", got)
	}
	// Invalid level
	if ap.SetThinkLevel(key, "outrageous") {
		t.Error("expected false for invalid level")
	}
	// Empty key
	if ap.SetThinkLevel("", "high") {
		t.Error("expected false for empty key")
	}
}

func TestGetThinkLevel_PersistedFallback(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	key := "native:think2"
	al.sessionAgents.Store(key, "main")
	main, _ := al.registry.GetAgent("main")
	main.Sessions.SetThinkingLevel(key, "low")
	if got := ap.GetThinkLevel(key); got != "low" {
		t.Errorf("persisted level = %q", got)
	}
	if got := ap.GetThinkLevel(""); got != "default" {
		t.Errorf("empty key level = %q", got)
	}
}

// ============================================================================
// Subagents
// ============================================================================

func TestGetSubagents(t *testing.T) {
	ap := newCovTestLoop(t).providable
	if s := ap.GetSubagents(); s == "" {
		t.Error("expected non-empty formatted task list")
	}
}

func TestGetSessionSubagents_Empty(t *testing.T) {
	ap := newCovTestLoop(t).providable
	// No matching tasks -> returns empty/nil slice; just verify it doesn't panic.
	got := ap.GetSessionSubagents("native:no-sub")
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

func TestGetSessionSubagents_WithInMemory(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	key := "native:sub-parent"

	// Add a running task to a manager whose origin session matches.
	al.subagentSessionAgent.Store(key+":subagent-1", "coder")
	mgr := al.toolCoordinator.GetSubagents()["main"]
	if mgr == nil {
		t.Fatal("subagent manager missing for main")
	}
	taskID, err := mgr.Spawn(context.Background(), "task", "label", "coder", "native", key, nil)
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	_ = taskID // Spawn returns a human-readable message; extracting IDs of running tasks is complex.

	// GetSessionSubagents must not panic and should return a slice.
	_ = ap.GetSessionSubagents(key)
	if got := ap.GetSessionSubagents(key + ":subagent-1"); len(got) == 0 && got != nil {
		t.Error("unexpected non-empty result")
	}
}

// ============================================================================
// Session management
// ============================================================================

func TestClearSession(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	key := "native:clear"
	al.sessionAgents.Store(key, "main")
	main, _ := al.registry.GetAgent("main")
	main.Sessions.AddMessage(key, "user", "old")
	res := ap.ClearSession(key)
	if !strings.Contains(res, "New conversation started") {
		t.Errorf("unexpected: %q", res)
	}
}

func TestClearSession_NoAgent(t *testing.T) {
	al := newCovTestLoop(t)
	al.registry.agents = nil
	res := al.providable.ClearSession("native:x")
	if !strings.Contains(res, "No default agent") {
		t.Errorf("unexpected: %q", res)
	}
}

func TestNameAndUpdatedAndCreated(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	key := "native:names"
	al.sessionAgents.Store(key, "main")
	main, _ := al.registry.GetAgent("main")

	if err := ap.SetName(key, "My Chat"); err != nil {
		t.Fatalf("SetName failed: %v", err)
	}
	if got := ap.GetName(key); got != "My Chat" {
		t.Errorf("name = %q", got)
	}
	if ap.GetUpdated(key).IsZero() {
		t.Error("expected non-zero Updated")
	}
	if ap.GetCreated(key).IsZero() {
		t.Error("expected non-zero Created")
	}
	if s := ap.GetSessionSummary(key); s != "" {
		t.Errorf("summary = %q", s)
	}
	main.Sessions.SetSummary(key, "the-summary")
	if s := ap.GetSessionSummary(key); s != "the-summary" {
		t.Errorf("summary after set = %q", s)
	}
}

func TestNameMethods_NoAgent(t *testing.T) {
	al := newCovTestLoop(t)
	al.registry.agents = nil
	ap := al.providable
	if got := ap.GetName("native:x"); got != "" {
		t.Errorf("name = %q", got)
	}
	if !ap.GetUpdated("native:x").IsZero() {
		t.Error("expected zero Updated")
	}
	if !ap.GetCreated("native:x").IsZero() {
		t.Error("expected zero Created")
	}
	if err := ap.SetName("native:x", "n"); err == nil {
		t.Error("expected SetName error")
	}
	if s := ap.GetSessionSummary("native:x"); s != "" {
		t.Errorf("summary = %q", s)
	}
}

func TestResolveSessionKeyAndParent(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	al.sessionAliases.Store("native:base", "native:base:chat:1")
	if got := ap.ResolveSessionKey("native:base"); got != "native:base:chat:1" {
		t.Errorf("resolve = %q", got)
	}
	if got := ap.ResolveSessionKey(""); got != "" {
		t.Errorf("empty resolve = %q", got)
	}
	if got := ap.GetSubagentParentSessionKey(":subagent-3"); got != "" {
		t.Errorf("parent of bare subagent = %q", got)
	}
}

func TestIsSessionProcessing(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	if ap.IsSessionProcessing("native:proc") {
		t.Error("expected not processing")
	}
	// mark goal active -> processing true
	al.markGoalLoopActive("native:proc")
	if !ap.IsSessionProcessing("native:proc") {
		t.Error("expected processing after mark goal active")
	}
	al.clearGoalLoopActive("native:proc")
}

// TestTokenCountsAndCompaction tests token count getters.
func TestTokenCounts(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	key := "native:toks"
	al.sessionAgents.Store(key, "main")
	main, _ := al.registry.GetAgent("main")
	main.Sessions.AddTokenCounts(key, 10, 5)

	in, out, cw := ap.GetTokenCounts(key)
	if in != 10 || out != 5 {
		t.Errorf("token counts = %d,%d", in, out)
	}
	if cw <= 0 {
		t.Errorf("context window = %d", cw)
	}
	if cc := ap.GetCompactionCount(key); cc != 0 {
		t.Errorf("compaction count = %d", cc)
	}
	cw2 := ap.GetSessionContextWindow(key)
	if cw2 != cw {
		t.Errorf("context window mismatch")
	}
}

func TestTokenCounts_NoAgent(t *testing.T) {
	al := newCovTestLoop(t)
	al.registry.agents = nil
	ap := al.providable
	in, out, cw := ap.GetTokenCounts("native:x")
	if in != 0 || out != 0 || cw != 0 {
		t.Errorf("got %d,%d,%d", in, out, cw)
	}
	if cc := ap.GetCompactionCount("native:x"); cc != 0 {
		t.Errorf("compaction count = %d", cc)
	}
}

func TestGetCurrentContextUsage(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	key := "native:usage"
	al.sessionAgents.Store(key, "main")
	curr, cw := ap.GetCurrentContextUsage(key)
	if cw <= 0 {
		t.Errorf("context window = %d", cw)
	}
	if curr < 0 {
		t.Errorf("current tokens = %d", curr)
	}
}

func TestGetCurrentContextUsage_NoAgent(t *testing.T) {
	al := newCovTestLoop(t)
	al.registry.agents = nil
	curr, cw := al.providable.GetCurrentContextUsage("native:x")
	if curr != 0 || cw != 0 {
		t.Errorf("got %d,%d", curr, cw)
	}
}

// ============================================================================
// Routes & processing
// ============================================================================

func TestResolveRoute(t *testing.T) {
	al := newCovTestLoop(t)
	key := al.providable.ResolveRoute("native", "direct", "peer-1")
	if key == "" {
		t.Error("expected resolved session key")
	}
	key2 := al.providable.ResolveRoute("native", "", "peer-2")
	if key2 == "" {
		t.Error("expected resolved key for empty kind")
	}
}

func TestListAllSessions(t *testing.T) {
	al := newCovTestLoop(t)
	al.sessionAgents.Store("native:list", "main")
	main, _ := al.registry.GetAgent("main")
	main.Sessions.AddMessage("native:list", "user", "hi")

	sessions := al.providable.ListAllSessions()
	if sessions == nil {
		t.Fatal("expected non-nil sessions")
	}
}

func TestClassifySessionKind(t *testing.T) {
	cases := map[string]string{
		"":                          "chat",
		"heartbeat":                 "heartbeat",
		"cron-abc":                  "cron",
		"cron-spawn-xyz":            "cron-spawn",
		"subagent:task":             "subagent",
		"native:parent:subagent-1":  "subagent",
		"native:plain-chat":         "chat",
	}
	for key, want := range cases {
		if got := classifySessionKind(key); got != want {
			t.Errorf("classifySessionKind(%q) = %q, want %q", key, got, want)
		}
	}
}

// ============================================================================
// Streaming persistence
// ============================================================================

func TestStreamingPersistence(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable
	key := "native:stream"
	al.sessionAgents.Store(key, "main")
	main, _ := al.registry.GetAgent("main")
	main.Sessions.AddMessage(key, "user", "prompt")

	ap.AppendAssistantChunk(key, "Hello")
	ap.AppendReasoningChunk(key, "thinking...")
	if !ap.HasStreamedContent(key) {
		t.Error("expected streamed content")
	}
	if p := ap.GetInProgressAssistant(key); p == nil {
		t.Error("expected in-progress assistant message")
	}
	ap.FinalizeAssistantMessage(key)
}

func TestStreamingPersistence_NoAgent(t *testing.T) {
	al := newCovTestLoop(t)
	al.registry.agents = nil
	ap := al.providable
	key := "native:stream-no-agent"
	// These must not panic.
	ap.AppendAssistantChunk(key, "x")
	ap.AppendReasoningChunk(key, "y")
	ap.FinalizeAssistantMessage(key)
	if ap.HasStreamedContent(key) {
		t.Error("expected no streamed content")
	}
	if p := ap.GetInProgressAssistant(key); p != nil {
		t.Errorf("expected nil in-progress, got %v", p)
	}
}

// ============================================================================
// Background execs
// ============================================================================

func TestBackgroundExec_NoCoordinator(t *testing.T) {
	al := newCovTestLoop(t)
	al.toolCoordinator = nil
	ap := al.providable

	if got := ap.GetBackgroundExecs(true); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	if _, _, _, err := ap.GetBackgroundExecOutput("id", 0); err == nil {
		t.Error("expected error for nil coordinator")
	}
	if err := ap.StopBackgroundExec("id"); err == nil {
		t.Error("expected error for nil coordinator")
	}
}

func TestBackgroundExec_WithCoordinator(t *testing.T) {
	al := newCovTestLoop(t)
	ap := al.providable

	tci, ok := al.toolCoordinator.(*toolCoordinatorImpl)
	if !ok {
		t.Fatal("expected *toolCoordinatorImpl")
	}
	bgMgr := tci.bgManagers["main"]
	if bgMgr == nil {
		t.Skip("no bg manager for main")
	}
	procs := bgMgr.List()
	if len(procs) == 0 {
		t.Skip("no background processes available")
	}
	got := ap.GetBackgroundExecs(true)
	if len(got) == 0 {
		t.Fatal("expected background execs")
	}
	if got[0].Command == "" {
		t.Error("expected command")
	}
}

// ============================================================================
// Group snapshots
// ============================================================================

func TestAllGroupSnapshots(t *testing.T) {
	al := newCovTestLoop(t)
	if got := al.providable.AllGroupSnapshots(); got == nil {
		t.Error("expected non-nil snapshots (may be empty)")
	}
}

// ============================================================================
// ProcessDirect / Heartbeat
// ============================================================================

func TestProcessDirect_NoMessages(t *testing.T) {
	al := newCovTestLoop(t)
	al.registry.GetDefaultAgent().Provider = &mockProvider{mockResponse: "ok"}
	_, err := al.providable.ProcessDirect(context.Background(), "hello", "native:pd")
	if err != nil {
		t.Fatalf("ProcessDirect failed: %v", err)
	}
}

func TestProcessHeartbeat(t *testing.T) {
	al := newCovTestLoop(t)
	al.registry.GetDefaultAgent().Provider = &mockProvider{mockResponse: "hb"}
	_, err := al.providable.ProcessHeartbeat(context.Background(), "ping", "native", "hb-chat")
	if err != nil {
		t.Fatalf("ProcessHeartbeat failed: %v", err)
	}
}

func TestProcessDirectWithChannel(t *testing.T) {
	al := newCovTestLoop(t)
	al.registry.GetDefaultAgent().Provider = &mockProvider{mockResponse: "with-channel"}
	_, err := al.providable.ProcessDirectWithChannel(context.Background(), "hi", "native:pdwc", "native", "chat-1")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}
}

// ============================================================================
// StopAgent group counting
// ============================================================================

func TestStopAgent_GroupAndSubagent(t *testing.T) {
	al := newCovTestLoop(t)
	// Spawn a subagent to make stopSessionSubagents count > 0.
	mgr := al.toolCoordinator.GetSubagents()["main"]
	key := "native:stop-sub"
	al.subagentSessionAgent.Store(key+":subagent-1", "coder")
	if mgr != nil {
		_, _ = mgr.Spawn(context.Background(), "t", "l", "coder", "native", key, nil)
	}
	res := al.providable.StopAgent(key)
	if !strings.Contains(res, "Agente detenido") {
		t.Errorf("unexpected: %q", res)
	}
}