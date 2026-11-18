// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
)

// A turn whose LLM request fails after every retry used to vanish: the error
// text was published as a normal (never persisted) final answer, so the TUI
// reloaded an unchanged history and the WebUI saw it deleted by the refetch.
// These tests pin the fix: the failure is persisted as an assistant message
// carrying ErrorDetail (UI-only) with ExcludeFromContext=true, so both UIs can
// render an error box AND the text can never reach the model.

const testLLMErrorMsg = "❌ Error processing message: LLM call failed after retries: 429 Too Many Requests"

// newLLMFailureTestLoop builds the same real-bus/real-session-manager loop the
// durable-inbound tests use, so the persistence path is exercised end to end.
func newLLMFailureTestLoop(t *testing.T) (*AgentLoop, *bus.MessageBus) {
	t.Helper()
	return newDurableInboundTestLoop(t, &countingMockProvider{response: "unused"})
}

// TestPersistLLMFailure_PersistsExcludedErrorMessage covers the shape of the
// stored message: role assistant (so both UIs place it where the answer would
// have been), the legacy ❌ text in Content (plain-text channels), the failure
// detail in ErrorDetail and ExcludeFromContext=true.
func TestPersistLLMFailure_PersistsExcludedErrorMessage(t *testing.T) {
	al, _ := newLLMFailureTestLoop(t)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}

	sessionKey := al.ResolveSessionKey("telegram:123")
	agent.Sessions.AddMessage(sessionKey, "user", "hello")

	mp := newMessageProcessor(al)
	inbound := &bus.InboundMessage{Metadata: map[string]string{"message_id": "55"}}
	mp.persistLLMFailure(agent, sessionKey, testLLMErrorMsg, errors.New("429 Too Many Requests"), inbound)

	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) != 2 {
		t.Fatalf("history has %d messages, want 2 (user + persisted failure): %+v", len(history), history)
	}
	last := history[len(history)-1]
	if last.Role != "assistant" {
		t.Errorf("persisted failure role = %q, want assistant", last.Role)
	}
	if last.Content != testLLMErrorMsg {
		t.Errorf("Content = %q, want the legacy ❌ text (plain-text channels depend on it)", last.Content)
	}
	if last.ErrorDetail != "429 Too Many Requests" {
		t.Errorf("ErrorDetail = %q, want the raw failure detail", last.ErrorDetail)
	}
	if !last.ExcludeFromContext {
		t.Error("the failure message must be excluded from the LLM context")
	}
	if last.Streaming {
		t.Error("the persisted failure must not be a streaming message")
	}

	// The inbound marker is what loop.go turns into outbound Metadata["error"].
	if got := inbound.Metadata[llmErrorMetadataKey]; got != "true" {
		t.Errorf("inbound metadata %s = %q, want \"true\"", llmErrorMetadataKey, got)
	}

	// And the excluded row is dropped by the very filter that builds the
	// context, so the failure text can never be replayed to the model.
	for _, msg := range filterContextMessages(history) {
		if msg.ErrorDetail != "" {
			t.Error("the persisted failure reached the LLM context")
		}
	}
}

// TestPersistLLMFailure_KeepsStreamedTail is the streaming invariant: when the
// request failed AFTER streaming part of an answer, that partial text must
// survive as its own message instead of being overwritten in place by the error
// (AddFullMessage replaces a streaming assistant tail in place).
func TestPersistLLMFailure_KeepsStreamedTail(t *testing.T) {
	al, _ := newLLMFailureTestLoop(t)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}

	sessionKey := al.ResolveSessionKey("telegram:123")
	agent.Sessions.AddMessage(sessionKey, "user", "hello")
	agent.Sessions.AppendAssistantChunk(sessionKey, "partial answer that the user already saw")

	mp := newMessageProcessor(al)
	inbound := &bus.InboundMessage{Metadata: map[string]string{"message_id": "55"}}
	mp.persistLLMFailure(agent, sessionKey, testLLMErrorMsg, errors.New("stream broke"), inbound)

	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) != 3 {
		t.Fatalf("history has %d messages, want 3 (user + streamed tail + failure): %+v", len(history), history)
	}
	partial := history[1]
	if partial.Role != "assistant" || !strings.Contains(partial.Content, "partial answer") {
		t.Fatalf("streamed tail was lost or replaced: %+v", partial)
	}
	if partial.Streaming {
		t.Error("the sealed streamed tail must not stay flagged as streaming")
	}
	if partial.ErrorDetail != "" {
		t.Error("the error must be appended as a new message, not merged into the streamed tail")
	}
	last := history[2]
	if last.ErrorDetail != "stream broke" || !last.ExcludeFromContext {
		t.Fatalf("failure message not appended after the streamed tail: %+v", last)
	}
}

// TestPersistLLMFailure_EmptyStreamingDraftReusesSlot guards the other half of
// the invariant: a draft with no content is still a placeholder, so the failure
// takes its slot instead of leaving an orphan blank bubble behind.
func TestPersistLLMFailure_EmptyStreamingDraftReusesSlot(t *testing.T) {
	al, _ := newLLMFailureTestLoop(t)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}

	sessionKey := al.ResolveSessionKey("telegram:123")
	agent.Sessions.AddMessage(sessionKey, "user", "hello")
	// Creating the draft without content is what the very first streamed
	// think-chunk does: a placeholder with nothing visible in it.
	agent.Sessions.AppendReasoningChunk(sessionKey, "")

	mp := newMessageProcessor(al)
	inbound := &bus.InboundMessage{Metadata: map[string]string{"message_id": "55"}}
	mp.persistLLMFailure(agent, sessionKey, testLLMErrorMsg, errors.New("boom"), inbound)

	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) != 2 {
		t.Fatalf("history has %d messages, want 2 (user + failure in the placeholder slot): %+v", len(history), history)
	}
	if last := history[len(history)-1]; last.ErrorDetail != "boom" {
		t.Fatalf("failure message missing: %+v", last)
	}
}

// TestPersistLLMFailure_NilInputsAreSafe pins the defensive branches: the live
// UI marker must still be set when there is no session manager to write to, and
// a nil inbound message must not panic.
func TestPersistLLMFailure_NilInputsAreSafe(t *testing.T) {
	al, _ := newLLMFailureTestLoop(t)
	mp := newMessageProcessor(al)

	// No agent: nothing persisted, no panic.
	mp.persistLLMFailure(nil, "telegram:123", testLLMErrorMsg, errors.New("boom"), nil)

	// Nil metadata: nothing to tag, no panic.
	inbound := &bus.InboundMessage{}
	agent := al.registry.GetDefaultAgent()
	mp.persistLLMFailure(agent, al.ResolveSessionKey("telegram:123"), testLLMErrorMsg, errors.New("boom"), inbound)
	if inbound.Metadata != nil {
		t.Error("a nil metadata map must not be created as a side effect")
	}
}

// TestRun_LLMFailureOutboundIsMarkedAndPersisted is the end-to-end contract the
// WebUI depends on: the legacy error text still travels as a normal final
// answer (Event "", so every turn-completion path keeps working) but carries
// Metadata["error"]="true", and the same failure is already in the history the
// following refetch reads.
func TestRun_LLMFailureOutboundIsMarkedAndPersisted(t *testing.T) {
	al, msgBus := newDurableInboundTestLoop(t, &failingMockProvider{})

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()

	msg := spooledMessage()
	if !msgBus.PublishInbound(msg) {
		t.Fatal("inbound publish rejected")
	}

	var errorOutbound *bus.OutboundMessage
	deadline := time.Now().Add(60 * time.Second)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatal("turn.end never arrived")
		}
		got, ok := recvOutbound(t, msgBus, remaining)
		if !ok {
			t.Fatal("turn.end never arrived")
		}
		if got.Event == "turn.end" {
			break
		}
		if strings.Contains(got.Content, "Error processing message") {
			got := got
			errorOutbound = &got
		}
	}

	if errorOutbound == nil {
		t.Fatal("the provider failure never reached the user as an error response")
	}
	if errorOutbound.Event != "" {
		t.Errorf("error response event = %q, want \"\" (turn-completion semantics must not change)", errorOutbound.Event)
	}
	if got := errorOutbound.Metadata["error"]; got != "true" {
		t.Errorf("outbound metadata error = %q, want \"true\" so the WebUI paints the box live", got)
	}
	if errorOutbound.Channel != msg.Channel || errorOutbound.ChatID != msg.ChatID {
		t.Errorf("error routing = %q/%q, want %q/%q", errorOutbound.Channel, errorOutbound.ChatID, msg.Channel, msg.ChatID)
	}

	// The history the refetch reads already holds the failure.
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}
	sessionKey := al.ResolveSessionKey(msg.SessionKey)
	var found *providers.Message
	sawError := waitFor(t, 5*time.Second, func() bool {
		for _, m := range agent.Sessions.GetHistory(sessionKey) {
			if m.ErrorDetail != "" {
				m := m
				found = &m
				return true
			}
		}
		return false
	})
	if !sawError || found == nil {
		t.Fatal("the provider failure was never persisted to the session history")
	}
	if !found.ExcludeFromContext {
		t.Error("persisted failure must be excluded from the LLM context")
	}

	stopRun(t, cancelRun, done)
}
