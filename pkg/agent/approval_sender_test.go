package agent

import (
	"context"
	"sync"
	"testing"
	"time"

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

func captureApprovalRequester(t *testing.T, am *channels.ApprovalManager, outbound func(context.Context) (bus.OutboundMessage, bool)) <-chan *channels.PendingApproval {
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
			if msg.Event != "approval.request" {
				continue
			}
			id := msg.Metadata["id"]
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
		if approval.RequesterID != "111|alice" {
			t.Fatalf("approval.RequesterID = %q, want the sender %q", approval.RequesterID, "111|alice")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no approval request was published")
	}

	senders := fake.observedSenders()
	if len(senders) == 0 || senders[0] != "111|alice" {
		t.Fatalf("tool context senders = %v, want the first call to carry %q", senders, "111|alice")
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

	observed := captureApprovalRequester(t, al.approvalManager, al.bus.SubscribeOutbound)

	msg := bus.InboundMessage{
		Channel:    channels.ChannelName,
		SenderID:   "111|alice",
		ChatID:     "555",
		Content:    "corre el comando peligroso",
		SessionKey: "native:555",
		Metadata:   map[string]string{"message_id": "21"},
		DedupeID:   "sender-plumbing",
	}
	if _, err := al.messageProcessor.processMessage(context.Background(), msg); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	select {
	case approval := <-observed:
		if approval.RequesterID != "111|alice" {
			t.Fatalf("approval.RequesterID = %q, want the inbound sender %q", approval.RequesterID, "111|alice")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the inbound turn never requested an approval")
	}

	senders := fake.observedSenders()
	if len(senders) == 0 || senders[0] != "111|alice" {
		t.Fatalf("tool context senders = %v, want the inbound sender %q", senders, "111|alice")
	}
}
