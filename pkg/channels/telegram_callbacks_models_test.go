package channels

import (
	"context"
	"testing"

	"github.com/mymmrac/telego"

	"github.com/xilistudios/lele/pkg/config"
)

// sampleCallbackQuery builds a CallbackQuery with an accessible message in a
// private chat belonging to the given user.
func sampleCallbackQuery(data string, fromID int64, username string, chatType string) telego.CallbackQuery {
	chatID := int64(5001)
	if chatType == "group" {
		chatID = 9001
	}
	return telego.CallbackQuery{
		ID:   "cb-1",
		Data: data,
		From: telego.User{ID: fromID, FirstName: "Tester", Username: username},
		Message: &telego.Message{
			MessageID: 42,
			Chat:      telego.Chat{ID: chatID, Type: chatType},
		},
	}
}

func TestFormatAgentSelectedMessage(t *testing.T) {
	info := AgentBasicInfo{Name: "Main Agent", ID: "main", Model: "gpt-4", Workspace: "workspace"}
	msg := formatAgentSelectedMessage(info, "main")
	if msg == "" {
		t.Fatal("expected non-empty message")
	}

	info2 := AgentBasicInfo{Name: "r", Model: "m", Workspace: "/custom", SkillsFilter: []string{"code"}}
	msg2 := formatAgentSelectedMessage(info2, "r")
	if msg2 == "" {
		t.Fatal("expected non-empty")
	}
}

func TestTelegramCallbackPeerInfo(t *testing.T) {
	if k, id := telegramCallbackPeerInfo(sampleCallbackQuery("x", 7, "u", "private")); k != "direct" || id != "7" {
		t.Errorf("private: %q %q", k, id)
	}
	if k, id := telegramCallbackPeerInfo(sampleCallbackQuery("x", 7, "u", "group")); k != "group" || id != "9001" {
		t.Errorf("group: %q %q", k, id)
	}
}

func TestTelegramHandleAgentCallback(t *testing.T) {
	loop := newNativeTestAgentLoop(config.DefaultConfig())
	ch, m := newMockTelegramChannel(t, nil, loop, nil)
	defer m.Close()

	// Message nil => returns nil early
	q := telego.CallbackQuery{ID: "c", Data: "agent:select:main", From: telego.User{ID: 1}}
	if err := ch.handleAgentCallback(context.Background(), q); err != nil {
		t.Fatalf("nil message: %v", err)
	}

	// Invalid data prefix
	q = sampleCallbackQuery("banana", 7, "u", "private")
	if err := ch.handleAgentCallback(context.Background(), q); err != nil {
		t.Fatalf("invalid data: %v", err)
	}

	// Valid select
	q = sampleCallbackQuery("agent:select:main", 7, "u", "private")
	if err := ch.handleAgentCallback(context.Background(), q); err != nil {
		t.Fatalf("select: %v", err)
	}

	// Nonexistent agent
	q = sampleCallbackQuery("agent:select:nope", 7, "u", "private")
	if err := ch.handleAgentCallback(context.Background(), q); err != nil {
		t.Fatalf("nonexistent: %v", err)
	}

	// Unknown action
	q = sampleCallbackQuery("agent:do:main", 7, "u", "private")
	if err := ch.handleAgentCallback(context.Background(), q); err != nil {
		t.Fatalf("unknown action: %v", err)
	}
	if !m.hadMethod("answerCallbackQuery") {
		t.Fatal("expected answerCallbackQuery")
	}
}

func TestTelegramHandleAgentCallback_NilAgentLoop(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()
	q := sampleCallbackQuery("agent:select:main", 7, "u", "private")
	if err := ch.handleAgentCallback(context.Background(), q); err != nil {
		t.Fatalf("nil agent loop: %v", err)
	}
}

func TestTelegramHandleVerboseCallback(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	loop := newNativeTestAgentLoop(config.DefaultConfig())
	ch, m := newMockTelegramChannel(t, nil, loop, nil)
	defer m.Close()

	q := sampleCallbackQuery("verbose:set:full", 7, "u", "private")
	if err := ch.handleVerboseCallback(context.Background(), q); err != nil {
		t.Fatalf("verbose set: %v", err)
	}

	// Invalid data
	q = sampleCallbackQuery("foo", 7, "u", "private")
	if err := ch.handleVerboseCallback(context.Background(), q); err != nil {
		t.Fatalf("invalid data: %v", err)
	}
	// Wrong prefix
	q = sampleCallbackQuery("verbose:bad:off", 7, "u", "private")
	_ = q
	if err := ch.handleVerboseCallback(context.Background(), sampleCallbackQuery("verbose:other:x", 7, "u", "private")); err != nil {
		t.Fatalf("unknown action: %v", err)
	}
}

func TestTelegramHandleVerboseCallback_NilAgentLoop(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()
	q := sampleCallbackQuery("verbose:set:off", 7, "u", "private")
	if err := ch.handleVerboseCallback(context.Background(), q); err != nil {
		t.Fatalf("nil agent loop: %v", err)
	}
}

func TestTelegramHandleThinkCallback(t *testing.T) {
	loop := newNativeTestAgentLoop(config.DefaultConfig())
	ch, m := newMockTelegramChannel(t, nil, loop, nil)
	defer m.Close()

	for _, level := range []string{"off", "low", "medium", "high", "bogus"} {
		q := sampleCallbackQuery("think:set:"+level, 7, "u", "private")
		if err := ch.handleThinkCallback(context.Background(), q); err != nil {
			t.Fatalf("think %s: %v", level, err)
		}
	}
	q := sampleCallbackQuery("think:bad:x", 7, "u", "private")
	if err := ch.handleThinkCallback(context.Background(), q); err != nil {
		t.Fatalf("bad think: %v", err)
	}
	if !m.hadMethod("editMessageText") {
		t.Fatal("expected editMessageText")
	}
}

// ---------------------------------------------------------------------------
// telegram_models.go
// ---------------------------------------------------------------------------

func TestTelegramHandleModelsCallback(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {Type: "openai", Models: map[string]config.ProviderModelConfig{
			"gpt-4":   {},
			"gpt-4o":  {},
			"gpt-4.1": {},
		}},
	}
	// Ensure GetNamed works.
	loop := newNativeTestAgentLoop(cfg)
	ch, m := newMockTelegramChannel(t, nil, loop, nil)
	defer m.Close()
	ch.config = cfg

	// Message nil => early return
	if err := ch.handleModelsCallback(context.Background(), telego.CallbackQuery{ID: "c", Data: "models:provider:openai", From: telego.User{ID: 1}}); err != nil {
		t.Fatalf("nil message: %v", err)
	}

	// provider action
	q := sampleCallbackQuery("models:provider:openai", 7, "u", "private")
	if err := ch.handleModelsCallback(context.Background(), q); err != nil {
		t.Fatalf("provider: %v", err)
	}
	// page action
	q = sampleCallbackQuery("models:page:openai:1", 7, "u", "private")
	if err := ch.handleModelsCallback(context.Background(), q); err != nil {
		t.Fatalf("page: %v", err)
	}
	// model action
	q = sampleCallbackQuery("models:model:openai:gpt-4", 7, "u", "private")
	if err := ch.handleModelsCallback(context.Background(), q); err != nil {
		t.Fatalf("model: %v", err)
	}
	// invalid len
	q = sampleCallbackQuery("models:only", 7, "u", "private")
	if err := ch.handleModelsCallback(context.Background(), q); err != nil {
		t.Fatalf("invalid len: %v", err)
	}
	if !m.hadMethod("answerCallbackQuery") {
		t.Fatal("expected answerCallbackQuery")
	}
}

func TestTelegramSendProviderModelsMenu_NoModels(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()
	loop := newNativeTestAgentLoop(config.DefaultConfig())
	ch.agentLoop = loop
	if err := ch.sendProviderModelsMenu(context.Background(), 1, 0, "zzz", 0); err != nil {
		t.Fatalf("no models: %v", err)
	}
	// Edit branch (messageID > 0)
	if err := ch.sendProviderModelsMenu(context.Background(), 1, 5, "zzz", 0); err != nil {
		t.Fatalf("edit branch: %v", err)
	}
}

func TestTelegramModelPageBounds(t *testing.T) {
	tests := []struct {
		total, page, per int
		wStart, wEnd     int
		wPage, wPages    int
	}{
		{0, 0, 6, 0, 0, 0, 1},
		{10, 0, 6, 0, 6, 0, 2},
		{10, 1, 6, 6, 10, 1, 2},
		{10, 5, 6, 6, 10, 1, 2}, // clamped
		{3, -1, 6, 0, 3, 0, 1},  // negative page
		{3, 0, 0, 0, 3, 0, 1},   // zero perPage -> default
	}
	for _, tt := range tests {
		s, e, p, n := modelPageBounds(tt.total, tt.page, tt.per)
		if s != tt.wStart || e != tt.wEnd || p != tt.wPage || n != tt.wPages {
			t.Errorf("bounds(%d,%d,%d) = (%d,%d,%d,%d) want (%d,%d,%d,%d)",
				tt.total, tt.page, tt.per, s, e, p, n, tt.wStart, tt.wEnd, tt.wPage, tt.wPages)
		}
	}
}

func TestTelegramApplySelectedModel(t *testing.T) {
	loop := newNativeTestAgentLoop(config.DefaultConfig())
	ch, m := newMockTelegramChannel(t, nil, loop, nil)
	defer m.Close()

	// nil message => false
	if ch.applySelectedModel(telego.CallbackQuery{From: telego.User{ID: 1}}, "openai", "gpt-4") {
		t.Fatal("expected false for nil message")
	}
	// Valid
	q := sampleCallbackQuery("models:model:openai:gpt-4", 7, "u", "private")
	if !ch.applySelectedModel(q, "openai", "gpt-4") {
		t.Fatal("expected true")
	}
}

func TestTelegramSelectedModelCommand(t *testing.T) {
	if got := selectedModelCommand("openai", "gpt-4"); got != "/model openai:gpt-4" {
		t.Errorf("got %q", got)
	}
	if got := selectedModelCommand("", "gpt-4"); got != "/model gpt-4" {
		t.Errorf("got %q", got)
	}
	if got := selectedModelCommand("openai", "openrouter:deepseek/r1"); got != "/model openrouter:deepseek/r1" {
		t.Errorf("got %q", got)
	}
	if !isModelReference("a:b") {
		t.Error("a:b should be model reference")
	}
	if isModelReference("ab") || isModelReference("a:") || isModelReference(":b") {
		t.Error("invalid model references")
	}
}

func TestTelegramCollapseModelsMenu(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()
	// nil message => nil
	if err := ch.collapseModelsMenu(context.Background(), telego.CallbackQuery{ID: "c", From: telego.User{ID: 1}}); err != nil {
		t.Fatalf("nil message: %v", err)
	}
	// with message
	q := sampleCallbackQuery("models:model:openai:gpt-4", 7, "u", "private")
	if err := ch.collapseModelsMenu(context.Background(), q); err != nil {
		t.Fatalf("collapse: %v", err)
	}
}

// ---------------------------------------------------------------------------
// telegram_approval.go
// ---------------------------------------------------------------------------

func TestTelegramHandleApprovalCallback(t *testing.T) {
	am := NewApprovalManager()
	ch, m := newMockTelegramChannel(t, nil, nil, am)
	defer m.Close()

	// nil manager
	ch.approvalManager = nil
	q := sampleCallbackQuery("approval:approve:1", 7, "u", "private")
	if err := ch.handleApprovalCallback(context.Background(), q); err != nil {
		t.Fatalf("nil manager: %v", err)
	}
	ch.approvalManager = am

	// invalid data
	q = sampleCallbackQuery("approval", 7, "u", "private")
	if err := ch.handleApprovalCallback(context.Background(), q); err != nil {
		t.Fatalf("invalid data: %v", err)
	}
	// unknown action
	q = sampleCallbackQuery("approval:foo:1", 7, "u", "private")
	if err := ch.handleApprovalCallback(context.Background(), q); err != nil {
		t.Fatalf("unknown action: %v", err)
	}
	// nonexistent approval
	q = sampleCallbackQuery("approval:approve:nope", 7, "u", "private")
	if err := ch.handleApprovalCallback(context.Background(), q); err != nil {
		t.Fatalf("nonexistent: %v", err)
	}
	// view nonexistent
	q = sampleCallbackQuery("approval:view:nope", 7, "u", "private")
	if err := ch.handleApprovalCallback(context.Background(), q); err != nil {
		t.Fatalf("view: %v", err)
	}
}

func TestTelegramHandleApprovalCallback_WithApproval(t *testing.T) {
	am := NewApprovalManager()
	ch, m := newMockTelegramChannel(t, nil, nil, am)
	defer m.Close()

	approvalID := am.CreateApproval("telegram:1", "/status", "test approval", 5001)
	if approvalID == nil {
		t.Fatal("expected approval")
	}
	q := sampleCallbackQuery("approval:approve:"+approvalID.ID, 7, "u", "private")
	if err := ch.handleApprovalCallback(context.Background(), q); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !m.hadMethod("editMessageText") {
		t.Fatal("expected editMessageText")
	}

	// view existing approval
	q = sampleCallbackQuery("approval:view:"+approvalID.ID, 7, "u", "private")
	if err := ch.handleApprovalCallback(context.Background(), q); err != nil {
		t.Fatalf("view: %v", err)
	}
}

func TestTelegramHandleCommandWithSession_Stop_AgentLoopNil(t *testing.T) {
	ch, m := newMockTelegramChannel(t, nil, nil, nil)
	defer m.Close()
	msg := sampleTelegoMessage("/stop", 100, 100)
	if err := ch.handleCommandWithSession(context.Background(), msg, "stop"); err != nil {
		t.Fatalf("stop: %v", err)
	}
}