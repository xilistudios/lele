package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mymmrac/telego"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
)

type senderCapturingTool struct {
	name string

	mu      sync.Mutex
	senders []string
}

func (s *senderCapturingTool) Name() string        { return s.name }
func (s *senderCapturingTool) Description() string { return "sender-capturing test tool" }
func (s *senderCapturingTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}

func (s *senderCapturingTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	s.mu.Lock()
	s.senders = append(s.senders, tools.ToolSenderFromCtx(ctx))
	n := len(s.senders)
	s.mu.Unlock()

	if n == 1 {
		return &tools.ToolResult{
			ForLLM: "requires approval",
			ApprovalRequired: &tools.ApprovalInfo{
				Command: "rm -rf /tmp/x",
				Reason:  "test: dangerous command",
			},
		}
	}
	return tools.SilentResult("approved and executed")
}

func (s *senderCapturingTool) observedSenders() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.senders...)
}

type senderTestProvider struct {
	mu       sync.Mutex
	toolCall providers.ToolCall
	repeats  int
	calls    int
}

func (p *senderTestProvider) Chat(ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call <= p.repeats {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{p.toolCall}}, nil
	}
	return &providers.LLMResponse{Content: "listo", ToolCalls: []providers.ToolCall{}}, nil
}

func (p *senderTestProvider) GetDefaultModel() string { return "mock-sender-model" }

type sequencedSenderTestProvider struct {
	mu        sync.Mutex
	responses []*providers.LLMResponse
	calls     int
}

func (p *sequencedSenderTestProvider) Chat(ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	call := p.calls
	p.calls++
	p.mu.Unlock()
	if call < len(p.responses) {
		return p.responses[call], nil
	}
	return &providers.LLMResponse{Content: "listo", ToolCalls: []providers.ToolCall{}}, nil
}

func (p *sequencedSenderTestProvider) GetDefaultModel() string { return "mock-sender-model" }

func newSenderTestLoop(t *testing.T) *AgentLoop {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
		Providers: &config.ProvidersConfig{
			Anthropic: config.ProviderConfig{APIKey: "test-key"},
		},
	}
	return NewAgentLoop(cfg, bus.NewMessageBus())
}

func nativeApprovalID(msg bus.OutboundMessage) string {
	if msg.Event != "approval.request" || msg.Metadata == nil {
		return ""
	}
	return msg.Metadata["id"]
}

func chatApprovalID(msg bus.OutboundMessage) string {
	markup, ok := msg.ReplyMarkup.(*telego.InlineKeyboardMarkup)
	if !ok {
		return ""
	}
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if strings.HasPrefix(button.CallbackData, "approval:approve:") {
				return strings.TrimPrefix(button.CallbackData, "approval:approve:")
			}
		}
	}
	return ""
}

func captureApproval(t *testing.T, am *channels.ApprovalManager, outbound func(context.Context) (bus.OutboundMessage, bool), approvalIDOf func(bus.OutboundMessage) string) <-chan *channels.PendingApproval {
	t.Helper()
	observed := make(chan *channels.PendingApproval, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for {
			msg, ok := outbound(ctx)
			if !ok {
				return
			}
			id := approvalIDOf(msg)
			if id == "" {
				continue
			}
			approval := am.GetApproval(id)
			if approval == nil {
				continue
			}
			observed <- approval
			if _, err := am.HandleApproval(id, true); err != nil {
				t.Errorf("approving %q failed: %v", id, err)
			}
			return
		}
	}()
	return observed
}

func captureApprovalRequester(t *testing.T, am *channels.ApprovalManager, outbound func(context.Context) (bus.OutboundMessage, bool)) <-chan *channels.PendingApproval {
	t.Helper()
	return captureApproval(t, am, outbound, nativeApprovalID)
}

func captureChatApproval(t *testing.T, am *channels.ApprovalManager, outbound func(context.Context) (bus.OutboundMessage, bool)) <-chan *channels.PendingApproval {
	t.Helper()
	return captureApproval(t, am, outbound, chatApprovalID)
}

func TestToolExecutor_ThreadsSenderID(t *testing.T) {
	am := channels.NewApprovalManager()
	al := &AgentLoop{
		bus:             bus.NewMessageBus(),
		verboseManager:  session.NewVerboseManager(),
		approvalManager: am,
	}

	fake := &senderCapturingTool{name: "exec"}
	registry := tools.NewToolRegistry()
	registry.Register(fake)
	agent := &AgentInstance{ID: "agent-sender", Sessions: session.NewSessionManager(), Tools: registry}

	observed := captureApprovalRequester(t, am, al.bus.SubscribeOutbound)

	result, err := newToolExecutor(al).Execute(toolExecOptions{
		ctx:        context.Background(),
		agent:      agent,
		tc:         providers.ToolCall{ID: "call-1", Name: "exec"},
		channel:    channels.ChannelName,
		chatID:     "555",
		sessionKey: "telegram:555",
		senderID:   "111|alice",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned nil result")
	}

	select {
	case approval := <-observed:
		if approval.RequesterID != "" {
			t.Fatalf("approval.RequesterID = %q, want empty: a native turn is identified by its client, not by a requester", approval.RequesterID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no approval request was published")
	}

	senders := fake.observedSenders()
	if len(senders) == 0 || senders[0] != "111|alice" {
		t.Fatalf("tool context senders = %v, want the first call to carry %q", senders, "111|alice")
	}
}

func TestToolExecutor_RecordsRequesterOnTelegramSurface(t *testing.T) {
	am := channels.NewApprovalManager()
	al := &AgentLoop{
		bus:             bus.NewMessageBus(),
		verboseManager:  session.NewVerboseManager(),
		approvalManager: am,
	}

	fake := &senderCapturingTool{name: "exec"}
	registry := tools.NewToolRegistry()
	registry.Register(fake)
	agent := &AgentInstance{ID: "agent-sender", Sessions: session.NewSessionManager(), Tools: registry}

	observed := captureChatApproval(t, am, al.bus.SubscribeOutbound)

	result, err := newToolExecutor(al).Execute(toolExecOptions{
		ctx:        context.Background(),
		agent:      agent,
		tc:         providers.ToolCall{ID: "call-1", Name: "exec"},
		channel:    "telegram",
		chatID:     "-100123",
		sessionKey: "agent:main:telegram:group:-100123",
		senderID:   "111",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned nil result")
	}

	select {
	case approval := <-observed:
		if approval.RequesterID != "111" {
			t.Fatalf("approval.RequesterID = %q, want the telegram sender %q", approval.RequesterID, "111")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no approval request was published to the chat")
	}
}

func TestMessageProcessor_ThreadsSenderToApproval(t *testing.T) {
	al := newSenderTestLoop(t)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}

	fake := &senderCapturingTool{name: "exec"}
	agent.Tools.Register(fake)
	agent.Provider = &senderTestProvider{
		toolCall: providers.ToolCall{
			ID:        "call_approval",
			Name:      "exec",
			Arguments: map[string]interface{}{"command": "rm -rf /tmp/x"},
		},
		repeats: 1,
	}

	observed := captureChatApproval(t, al.approvalManager, al.bus.SubscribeOutbound)

	msg := bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "111",
		ChatID:     "-100123",
		Content:    "corre el comando peligroso",
		SessionKey: "agent:main:telegram:group:-100123",
		Metadata:   map[string]string{"message_id": "21"},
		DedupeID:   "sender-plumbing",
	}
	if _, err := al.messageProcessor.processMessage(context.Background(), msg); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	select {
	case approval := <-observed:
		if approval.RequesterID != "111" {
			t.Fatalf("approval.RequesterID = %q, want the inbound sender %q", approval.RequesterID, "111")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the inbound turn never requested an approval")
	}

	senders := fake.observedSenders()
	if len(senders) == 0 || senders[0] != "111" {
		t.Fatalf("tool context senders = %v, want the inbound sender %q", senders, "111")
	}
}

func TestGoalContinuation_ThreadsSenderToApproval(t *testing.T) {
	al := newSenderTestLoop(t)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}

	fake := &senderCapturingTool{name: "exec"}
	agent.Tools.Register(fake)
	agent.Provider = &sequencedSenderTestProvider{
		responses: []*providers.LLMResponse{
			{Content: "arranco con el objetivo", ToolCalls: []providers.ToolCall{}},
			{ToolCalls: []providers.ToolCall{{
				ID:        "call_continuation",
				Name:      "exec",
				Arguments: map[string]interface{}{"command": "rm -rf /tmp/x"},
			}}},
		},
	}

	const sessionKey = "agent:main:telegram:group:-100123"
	const senderID = "111"

	al.goalManager.Set(sessionKey, "continuar el objetivo", 5)

	observed := captureChatApproval(t, al.approvalManager, al.bus.SubscribeOutbound)

	turnDone := make(chan error, 1)
	go func() {
		_, err := al.messageProcessor.processMessage(context.Background(), bus.InboundMessage{
			Channel:    "telegram",
			SenderID:   senderID,
			ChatID:     "-100123",
			Content:    "empeza el objetivo",
			SessionKey: sessionKey,
			DedupeID:   "goal-continuation-sender",
		})
		turnDone <- err
	}()

	select {
	case approval := <-observed:
		al.goalManager.Clear(sessionKey)
		if approval.RequesterID != senderID {
			t.Fatalf("approval.RequesterID = %q, want the requesting sender %q: a goal continuation must not drop the sender", approval.RequesterID, senderID)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the goal continuation never requested an approval")
	}

	select {
	case err := <-turnDone:
		if err != nil {
			t.Fatalf("processMessage: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the turn never finished after the approval")
	}

	senders := fake.observedSenders()
	if len(senders) == 0 || senders[0] != senderID {
		t.Fatalf("tool context senders = %v, want the continuation turn to carry %q", senders, senderID)
	}
}

func TestSystemMessageTurn_ThreadsSenderToApproval(t *testing.T) {
	al := newSenderTestLoop(t)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}

	fake := &senderCapturingTool{name: "exec"}
	agent.Tools.Register(fake)
	agent.Provider = &senderTestProvider{
		toolCall: providers.ToolCall{
			ID:        "call_system",
			Name:      "exec",
			Arguments: map[string]interface{}{"command": "rm -rf /tmp/x"},
		},
		repeats: 1,
	}

	observed := captureChatApproval(t, al.approvalManager, al.bus.SubscribeOutbound)

	msg := bus.InboundMessage{
		Channel:    "system",
		SenderID:   "111",
		ChatID:     "telegram:-100123",
		Content:    "resultado de la tarea en segundo plano",
		SessionKey: "agent:main:telegram:group:-100123",
		Metadata:   map[string]string{"message_id": "31"},
	}
	if _, err := al.messageProcessor.processSystemMessage(context.Background(), msg); err != nil {
		t.Fatalf("processSystemMessage: %v", err)
	}

	select {
	case approval := <-observed:
		if approval.RequesterID != "111" {
			t.Fatalf("approval.RequesterID = %q, want the system message sender %q", approval.RequesterID, "111")
		}
	default:
		senders := fake.observedSenders()
		t.Fatalf("the system turn never requested an approval (senders = %v)", senders)
	}
}
func observeNativeApproval(t *testing.T, al *AgentLoop) <-chan string {
	t.Helper()

	observed := make(chan string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for {
			msg, ok := al.bus.SubscribeOutbound(ctx)
			if !ok {
				return
			}
			if id := nativeApprovalID(msg); id != "" {
				observed <- id
				return
			}
		}
	}()
	return observed
}

func TestNativeTurn_ApprovalStaysResolvableForItsSession(t *testing.T) {
	al := newSenderTestLoop(t)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}

	fake := &senderCapturingTool{name: "exec"}
	agent.Tools.Register(fake)
	agent.Provider = &senderTestProvider{
		toolCall: providers.ToolCall{
			ID:        "call_native",
			Name:      "exec",
			Arguments: map[string]interface{}{"command": "rm -rf /tmp/x"},
		},
		repeats: 1,
	}

	const sessionKey = "native:client-session"
	const clientID = "875f804ab60792a630f85d1d52493d8a"

	pending := observeNativeApproval(t, al)

	turnDone := make(chan error, 1)
	go func() {
		_, err := al.messageProcessor.processMessage(context.Background(), bus.InboundMessage{
			Channel:    channels.ChannelName,
			SenderID:   clientID,
			ChatID:     "client-session",
			Content:    "corre el comando peligroso",
			SessionKey: sessionKey,
			DedupeID:   "native-approval-authority",
		})
		turnDone <- err
	}()

	var approvalID string
	select {
	case approvalID = <-pending:
	case <-time.After(15 * time.Second):
		t.Fatal("the native turn never requested an approval")
	}

	approval := al.approvalManager.GetApproval(approvalID)
	if approval == nil {
		t.Fatal("the published approval id is not pending")
	}
	if approval.RequesterID != "" {
		t.Fatalf("approval.RequesterID = %q, want empty: the client is the session owner, not a per-chat requester", approval.RequesterID)
	}

	if _, err := al.approvalManager.HandleApprovalForSession(approvalID, true, sessionKey); err != nil {
		t.Fatalf("HandleApprovalForSession() error = %v, want the native endpoint to resolve its own approval", err)
	}

	select {
	case err := <-turnDone:
		if err != nil {
			t.Fatalf("the native turn failed after the approval: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the native turn never resumed after the approval")
	}
}
