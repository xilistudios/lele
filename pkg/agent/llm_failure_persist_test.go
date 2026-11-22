package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

const llmFailureDetail = "invalid request format: LLM call failed after retries"

const legacyErrorPrefix = "❌ Error processing message: "

type scriptedTurnProvider struct {
	mu       sync.Mutex
	failing  bool
	once     sync.Once
	onFail   func()
	requests [][]providers.Message
	answer   string
}

func (p *scriptedTurnProvider) fails() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failing = true
}

func (p *scriptedTurnProvider) succeeds() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failing = false
}

func (p *scriptedTurnProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	failing := p.failing
	p.mu.Unlock()

	if !failing {
		return &providers.LLMResponse{Content: p.answer, ToolCalls: []providers.ToolCall{}}, nil
	}
	p.once.Do(func() {
		if p.onFail != nil {
			p.onFail()
		}
	})
	return nil, errors.New(llmFailureDetail)
}

func (p *scriptedTurnProvider) GetDefaultModel() string { return "mock-scripted-model" }

func (p *scriptedTurnProvider) requestsSeen() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]providers.Message, len(p.requests))
	for i, r := range p.requests {
		out[i] = append([]providers.Message(nil), r...)
	}
	return out
}

func runOneTurn(t *testing.T, msgBus *bus.MessageBus, msg bus.InboundMessage) []bus.OutboundMessage {
	t.Helper()
	if !msgBus.PublishInbound(msg) {
		t.Fatal("inbound publish rejected")
	}
	return collectOutbound(t, msgBus, 60*time.Second)
}

func findFailureOutbound(t *testing.T, outbound []bus.OutboundMessage) bus.OutboundMessage {
	t.Helper()
	for _, m := range outbound {
		if strings.Contains(m.Content, "Error processing message") ||
			strings.Contains(m.Content, "Error resuming message") {
			return m
		}
	}
	t.Fatalf("the provider failure never reached the user: %+v", outbound)
	return bus.OutboundMessage{}
}

func newLLMFailureLoop(t *testing.T, provider providers.LLMProvider) (*AgentLoop, *bus.MessageBus) {
	t.Helper()
	al, msgBus := newDurableInboundTestLoop(t, provider)
	impl, ok := al.llmRunner.(*llmRunnerImpl)
	if !ok {
		t.Fatalf("unexpected llm runner type %T", al.llmRunner)
	}
	impl.retryWait = instantRetryWait
	return al, msgBus
}

func startLoop(t *testing.T, al *AgentLoop) {
	t.Helper()
	runCtx, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()
	t.Cleanup(func() { stopRun(t, cancelRun, done) })
}

func TestLLMFailureTurn(t *testing.T) {
	const partialAnswer = "partial answer the user already saw"

	type failureCase struct {
		name           string
		prepare        func(agent *AgentInstance, sessionKey string)
		inbound        func() bus.InboundMessage
		wantHistoryLen int
		wantDraft      bool
	}

	cases := []failureCase{
		{
			name:           "plain failure",
			wantHistoryLen: 2,
		},
		{
			name:           "streamed draft is kept as its own message",
			wantHistoryLen: 3,
			wantDraft:      true,
		},
		{
			name: "empty streaming draft placeholder is reused",
			prepare: func(agent *AgentInstance, sessionKey string) {
				agent.Sessions.AppendReasoningChunk(sessionKey, "")
			},
			wantHistoryLen: 2,
		},
		{
			name: "inbound without metadata still marks the live completion",
			inbound: func() bus.InboundMessage {
				msg := spooledMessage()
				msg.Metadata = nil
				return msg
			},
			wantHistoryLen: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &scriptedTurnProvider{answer: "unused"}
			provider.fails()
			al, msgBus := newLLMFailureLoop(t, provider)
			agent := al.registry.GetDefaultAgent()
			if agent == nil {
				t.Fatal("no default agent")
			}
			sessionKey := al.ResolveSessionKey("telegram:123")

			if tc.wantDraft {
				provider.onFail = func() {
					agent.Sessions.AppendAssistantChunk(sessionKey, partialAnswer)
				}
			}
			if tc.prepare != nil {
				tc.prepare(agent, sessionKey)
			}
			if tc.inbound == nil {
				tc.inbound = spooledMessage
			}

			startLoop(t, al)

			msg := tc.inbound()
			outbound := runOneTurn(t, msgBus, msg)

			failure := findFailureOutbound(t, outbound)
			if failure.Event != "" {
				t.Errorf("failure event = %q, want \"\" (turn-completion semantics must not change)", failure.Event)
			}
			if !strings.HasPrefix(failure.Content, legacyErrorPrefix) ||
				!strings.Contains(failure.Content, llmFailureDetail) {
				t.Errorf("failure content = %q, want the legacy %q prefix carrying %q",
					failure.Content, legacyErrorPrefix, llmFailureDetail)
			}
			if got := failure.Metadata["error"]; got != "true" {
				t.Errorf("outbound metadata error = %q, want \"true\" so the WebUI paints the box live", got)
			}
			if failure.Channel != msg.Channel || failure.ChatID != msg.ChatID {
				t.Errorf("failure routing = %q/%q, want %q/%q",
					failure.Channel, failure.ChatID, msg.Channel, msg.ChatID)
			}

			history := agent.Sessions.GetHistory(sessionKey)
			if len(history) != tc.wantHistoryLen {
				t.Fatalf("history has %d messages, want %d: %+v", len(history), tc.wantHistoryLen, history)
			}

			persisted := history[len(history)-1]
			if persisted.Role != "assistant" {
				t.Errorf("persisted failure role = %q, want assistant", persisted.Role)
			}
			if persisted.Content != failure.Content {
				t.Errorf("persisted Content = %q, want the same legacy ❌ text the channel received (%q)",
					persisted.Content, failure.Content)
			}
			if !strings.Contains(persisted.ErrorDetail, llmFailureDetail) {
				t.Errorf("persisted ErrorDetail = %q, want it to carry %q",
					persisted.ErrorDetail, llmFailureDetail)
			}
			if strings.Contains(persisted.ErrorDetail, legacyErrorPrefix) {
				t.Errorf("ErrorDetail = %q must be the raw failure, not the legacy ❌ text",
					persisted.ErrorDetail)
			}
			if !persisted.ExcludeFromContext {
				t.Error("the failure message must be excluded from the LLM context")
			}
			if persisted.Streaming {
				t.Error("the persisted failure must not be a streaming message")
			}

			if tc.wantDraft {
				draft := history[len(history)-2]
				if draft.Role != "assistant" || draft.Content != partialAnswer {
					t.Fatalf("the streamed draft was lost or replaced: %+v", draft)
				}
				if draft.Streaming {
					t.Error("the sealed streamed draft must not stay flagged as streaming")
				}
				if draft.ErrorDetail != "" {
					t.Error("the error must be appended as a new message, not merged into the streamed draft")
				}
			}
		})
	}
}

func TestLLMFailureResumedTurn(t *testing.T) {
	provider := &scriptedTurnProvider{answer: "unused"}
	provider.fails()
	al, msgBus := newLLMFailureLoop(t, provider)

	yes := true
	al.cfg().Session.Resume = &yes
	al.SetInboundDurability(&fakeDurability{})
	if al.dbStore == nil {
		t.Fatal("expected a SQLite store so turn markers can be written")
	}

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}
	msg := spooledMessage()
	sessionKey := al.ResolveSessionKey(msg.SessionKey)

	agent.Sessions.AddMessage(sessionKey, "user", msg.Content)
	al.writeTurnMarker(sessionKey, turnPhaseInbound, 0, func(m *TurnMarker) {
		m.DedupeID = msg.DedupeID
		m.SpoolID = msg.SpoolID
		m.Channel = msg.Channel
		m.ChatID = msg.ChatID
		m.MsgID = msg.Metadata["message_id"]
	})
	if marker, ok := al.getTurnMarker(sessionKey); !ok || marker.DedupeID != msg.DedupeID {
		t.Fatalf("turn marker not seeded (ok=%v marker=%+v)", ok, marker)
	}

	startLoop(t, al)

	outbound := runOneTurn(t, msgBus, msg)

	failure := findFailureOutbound(t, outbound)
	if failure.Event != "" {
		t.Errorf("resumed failure event = %q, want \"\"", failure.Event)
	}
	if !strings.Contains(failure.Content, "Error resuming message") {
		t.Errorf("resumed failure content = %q, want the resume wording", failure.Content)
	}
	if got := failure.Metadata["error"]; got != "true" {
		t.Errorf("outbound metadata error = %q, want \"true\"", got)
	}

	history := agent.Sessions.GetHistory(sessionKey)
	persisted := history[len(history)-1]
	if !strings.Contains(persisted.ErrorDetail, llmFailureDetail) {
		t.Errorf("resumed failure ErrorDetail = %q, want it to carry %q", persisted.ErrorDetail, llmFailureDetail)
	}
	if !persisted.ExcludeFromContext {
		t.Error("the resumed failure must be excluded from the LLM context")
	}
	if countErrorRows(history) != 1 {
		t.Errorf("want exactly one persisted failure row, got %d: %+v", countErrorRows(history), history)
	}
}

func TestLLMFailureNeverReachesTheModel(t *testing.T) {
	provider := &scriptedTurnProvider{answer: "second answer"}
	provider.fails()

	al, msgBus := newLLMFailureLoop(t, provider)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}
	sessionKey := al.ResolveSessionKey("telegram:123")

	startLoop(t, al)

	runOneTurn(t, msgBus, spooledMessage())
	if countErrorRows(agent.Sessions.GetHistory(sessionKey)) != 1 {
		t.Fatalf("setup failed: the first turn did not persist a failure: %+v",
			agent.Sessions.GetHistory(sessionKey))
	}

	second := spooledMessage()
	second.Content = "second question"
	second.DedupeID = "dedupe-78"
	second.SpoolID = 78
	runOneTurn(t, msgBus, second)

	requests := provider.requestsSeen()
	if len(requests) < 2 {
		t.Fatalf("provider saw %d requests, want at least 2", len(requests))
	}
	last := requests[len(requests)-1]
	if len(last) == 0 {
		t.Fatal("the second turn sent an empty request")
	}
	for _, m := range last {
		if m.ErrorDetail != "" {
			t.Errorf("the failure detail reached the model: %+v", m)
		}
		if strings.Contains(m.Content, llmFailureDetail) {
			t.Errorf("the failure text was replayed to the model: %q", m.Content)
		}
		if strings.Contains(m.Content, legacyErrorPrefix) {
			t.Errorf("the legacy ❌ error text was replayed to the model: %q", m.Content)
		}
	}
}

func TestLLMFailureIsDurable(t *testing.T) {
	provider := &scriptedTurnProvider{answer: "unused"}
	provider.fails()
	al, msgBus := newLLMFailureLoop(t, provider)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}
	sessionKey := al.ResolveSessionKey("telegram:123")

	startLoop(t, al)
	runOneTurn(t, msgBus, spooledMessage())

	if al.dbStore == nil {
		t.Fatal("expected a SQLite store")
	}
	reopened := session.NewSessionManager()
	reopened.SetSessionRepo(al.dbStore.Sessions())

	persisted := reopened.GetHistory(sessionKey)
	if got := countErrorRows(persisted); got != 1 {
		t.Fatalf("the persisted failure was not written to disk: %d failure rows after reload: %+v",
			got, persisted)
	}
}

func countErrorRows(history []providers.Message) int {
	n := 0
	for _, m := range history {
		if m.ErrorDetail != "" {
			n++
		}
	}
	return n
}
