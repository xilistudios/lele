// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/store"
	"github.com/xilistudios/lele/pkg/tools"
)

// These tests pin the invariant that made the agent die in production: the
// tool calls a turn writes to the session and the tool results it writes next
// to them must describe the same calls. A provider that rejects the request
// with 400 "function.arguments must be in JSON format" is bad enough, but the
// real damage was that the rejected message was persisted, so every later turn
// replayed it and the session never recovered.

// wireToolCalls returns the tool calls of a persisted message as the provider
// would see them.
func wireToolCalls(t *testing.T, m providers.Message) []struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
} {
	t.Helper()
	out, err := json.Marshal(m.ToolCalls)
	if err != nil {
		t.Fatalf("marshal persisted tool calls: %v", err)
	}
	var calls []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal(out, &calls); err != nil {
		t.Fatalf("parse persisted tool calls: %v (%s)", err, out)
	}
	return calls
}

// assertSessionWireClean fails if anything persisted would be rejected by a
// strict provider. It checks the RAW stored values, not the marshaled wire,
// because the wire is normalized on write by design: a session that still holds
// function.arguments "null" in the store is only saved from the 400 by the
// sanitizer running again on every turn, which is exactly the fragility that
// broke production. Stored tool calls must already be canonical.
func assertSessionWireClean(t *testing.T, history []providers.Message) {
	t.Helper()

	declared := map[string]struct{}{}
	for _, m := range history {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			name := tc.FunctionName()
			if strings.TrimSpace(name) == "" {
				t.Fatalf("persisted a nameless tool call: %+v", tc)
			}
			if tc.Function == nil {
				t.Fatalf("persisted tool call %s without a function", tc.ID)
			}
			if strings.TrimSpace(tc.Function.Name) == "" {
				t.Fatalf("persisted function without a name: %+v", tc.Function)
			}
			args := strings.TrimSpace(tc.Function.Arguments)
			if !json.Valid([]byte(args)) || !strings.HasPrefix(args, "{") {
				t.Fatalf("stored arguments are not a JSON object: %q", args)
			}
			if tc.Arguments == nil {
				t.Fatalf("stored tool call %s has no decoded map: tools execute from it", tc.ID)
			}
			if tc.Type != "function" {
				t.Fatalf("stored tool call %s has type %q", tc.ID, tc.Type)
			}
			declared[tc.ID] = struct{}{}
		}
	}
	for _, m := range history {
		if m.Role == "tool" {
			if _, ok := declared[m.ToolCallID]; !ok {
				t.Fatalf("persisted an orphan tool result for call id %q", m.ToolCallID)
			}
		}
	}
}

// assertStoreHasNoPoisonedToolCalls reads the persisted rows directly, bypassing
// the session manager, because loading a message runs it through
// ToolCall.UnmarshalJSON, which normalises arguments on read. A store that still
// holds "arguments":"null" would keep producing 400s for any consumer that reads
// the rows as they are - the API, an export, a different build - so healing has
// to be written back, not merely applied to the in-memory copy.
func assertStoreHasNoPoisonedToolCalls(t *testing.T, s *store.Store, sessionKey string) {
	t.Helper()

	rows, err := s.DB().Query(
		"SELECT role, message FROM session_messages WHERE session_key = ? ORDER BY seq",
		sessionKey,
	)
	if err != nil {
		t.Fatalf("query stored messages: %v", err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var role, message string
		if err := rows.Scan(&role, &message); err != nil {
			t.Fatalf("scan stored message: %v", err)
		}
		count++
		if !strings.Contains(message, "tool_calls") {
			continue
		}
		// The legacy shape wrote the decoded map as a second top-level
		// "arguments" next to function.arguments, and json.Marshal of a nil map
		// wrote "null" inside it.
		if strings.Contains(message, `"arguments":"null"`) || strings.Contains(message, `"arguments":null`) {
			t.Fatalf("store still holds null arguments for %s: %s", sessionKey, message)
		}
		var parsed struct {
			ToolCalls []map[string]json.RawMessage `json:"tool_calls"`
		}
		if err := json.Unmarshal([]byte(message), &parsed); err != nil {
			t.Fatalf("stored message is not valid JSON: %v (%s)", err, message)
		}
		for _, tc := range parsed.ToolCalls {
			if _, ok := tc["arguments"]; ok {
				t.Fatalf("stored tool call carries a duplicate top-level arguments: %s", message)
			}
			fn, ok := tc["function"]
			if !ok {
				t.Fatalf("stored tool call has no function: %s", message)
			}
			var f struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(fn, &f); err != nil {
				t.Fatalf("stored function is not valid JSON: %v (%s)", err, message)
			}
			if strings.TrimSpace(f.Name) == "" {
				t.Fatalf("stored a nameless tool call: %s", message)
			}
			var args string
			if err := json.Unmarshal(f.Arguments, &args); err != nil {
				t.Fatalf("function.arguments is not a JSON string: %v (%s)", err, message)
			}
			if trimmed := strings.TrimSpace(args); !strings.HasPrefix(trimmed, "{") || !json.Valid([]byte(trimmed)) {
				t.Fatalf("stored arguments are not a JSON object: %q", args)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate stored messages: %v", err)
	}
	if count == 0 {
		t.Fatalf("no rows persisted for %s - the session was never saved", sessionKey)
	}
}

// A nameless tool call with nil arguments is the exact shape that poisoned
// sessions in production. It must never reach the session store.
func TestRunLLMIteration_NamelessToolCallIsNeverPersisted(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	executed := 0
	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "exec",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			executed++
			return tools.SilentResult("done")
		},
	})

	callCount := 0
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			if callCount == 1 {
				return &providers.LLMResponse{
					Content: "",
					ToolCalls: []providers.ToolCall{
						// The production shape: no name, nil arguments.
						{ID: "call_dead", Type: "function", Arguments: nil},
					},
				}, nil
			}
			return &providers.LLMResponse{Content: "recovered"}, nil
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "run it"},
	}
	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	content, _, err := runner.runLLMIteration(context.Background(), agent, messages, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "recovered" {
		t.Fatalf("the turn did not recover, got %q", content)
	}
	if executed != 0 {
		t.Fatalf("an invalid tool call was executed %d time(s)", executed)
	}

	history := agent.Sessions.GetHistory("test-session")
	assertSessionWireClean(t, history)
	for _, m := range history {
		if m.Role == "tool" {
			t.Fatalf("a tool result was persisted for a rejected call: %+v", m)
		}
	}

	// The model must be told to retry, otherwise it repeats the same malformed
	// call forever and the turn burns every iteration.
	var guided bool
	for _, m := range history {
		if m.Role == "user" && strings.Contains(m.Content, "malformed") {
			guided = true
		}
	}
	if !guided {
		t.Fatal("no retry guidance was persisted for the malformed call")
	}
}

// When one call in a batch is rejected, the surviving call must be the one
// executed and the tool result must reference the id that was persisted - a
// mismatch is an orphan tool message, which is a 400 in a different shape.
func TestRunLLMIteration_ToolResultsMatchPersistedCalls(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	var executedNames []string
	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "exec",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			executedNames = append(executedNames, "exec")
			return tools.SilentResult("exec ok")
		},
	})

	callCount := 0
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			if callCount == 1 {
				return &providers.LLMResponse{
					ToolCalls: []providers.ToolCall{
						{ID: "call_bad", Type: "function"}, // nameless, nil arguments
						{ID: "call_good", Type: "function", Name: "exec",
							Arguments: map[string]interface{}{"command": "ls"}},
					},
				}, nil
			}
			return &providers.LLMResponse{Content: "finished"}, nil
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "run both"},
	}
	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	if _, _, err := runner.runLLMIteration(context.Background(), agent, messages, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(executedNames) != 1 {
		t.Fatalf("expected exactly the valid call to run, executed: %v", executedNames)
	}

	history := agent.Sessions.GetHistory("test-session")
	assertSessionWireClean(t, history)

	var results []string
	for _, m := range history {
		if m.Role == "tool" {
			results = append(results, m.ToolCallID)
		}
	}
	if len(results) != 1 || results[0] != "call_good" {
		t.Fatalf("tool results must reference the surviving call, got %v", results)
	}
}

// Arguments that are not a JSON object must be normalised before persisting,
// and the decoded map tools execute from must be rebuilt from them.
func TestRunLLMIteration_PersistedToolCallsAreCanonical(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	var gotArgs map[string]interface{}
	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "read_file",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			gotArgs = args
			return tools.SilentResult("contents")
		},
	})

	callCount := 0
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			callCount++
			if callCount == 1 {
				return &providers.LLMResponse{
					ToolCalls: []providers.ToolCall{
						{
							ID:   "call_1",
							Type: "function",
							Name: "read_file",
							// The legacy shape: arguments already stringified.
							Function:  &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"/tmp/x"}`},
							Arguments: map[string]interface{}{"path": "/tmp/x"},
						},
					},
				}, nil
			}
			return &providers.LLMResponse{Content: "read it"}, nil
		},
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "read"},
	}
	opts := processOptions{
		SessionKey:   "test-session",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	if _, _, err := runner.runLLMIteration(context.Background(), agent, messages, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotArgs == nil || gotArgs["path"] != "/tmp/x" {
		t.Fatalf("tool executed with wrong arguments: %v", gotArgs)
	}

	history := agent.Sessions.GetHistory("test-session")
	assertSessionWireClean(t, history)

	var assistant *providers.Message
	for i := range history {
		if history[i].Role == "assistant" && len(history[i].ToolCalls) > 0 {
			assistant = &history[i]
		}
	}
	if assistant == nil {
		t.Fatal("no assistant tool-call message was persisted")
	}
	calls := wireToolCalls(t, *assistant)
	if len(calls) != 1 {
		t.Fatalf("expected 1 persisted call, got %d", len(calls))
	}
	if got := calls[0].Function.Arguments; got != `{"path":"/tmp/x"}` {
		t.Fatalf("arguments were rewritten: %q", got)
	}

	// The whole request must be marshalable without emitting a duplicate
	// top-level "arguments", which is what strict gateways reject.
	full, err := json.Marshal(assistant.ToolCalls)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var shape []map[string]json.RawMessage
	if err := json.Unmarshal(full, &shape); err != nil {
		t.Fatalf("parse wire: %v (%s)", err, full)
	}
	if _, ok := shape[0]["arguments"]; ok {
		t.Fatalf("wire carries a duplicate top-level arguments: %s", full)
	}
	if _, ok := shape[0]["name"]; ok {
		t.Fatalf("wire carries a duplicate top-level name: %s", full)
	}
}

// A session already poisoned on disk must be repaired on load, so the agent can
// answer again without the user having to /new.
func TestRunAgentLoop_HealsPoisonedSessionOnLoad(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	storePath := filepath.Join(tmpDir, "heal.db")
	s, err := store.Open(storePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	agent.Sessions.SetStore(s)

	sessionKey := "poisoned-session"
	poisoned := providers.ToolCall{
		ID:        "call_1",
		Type:      "function",
		Name:      "exec",
		Function:  &providers.FunctionCall{Name: "exec", Arguments: "null"},
		Arguments: nil,
	}
	// A second call with no name cannot be repaired by the reader-side
	// normalisation - there is no tool to name - so it only disappears if
	// healing is written back to the store.
	nameless := providers.ToolCall{
		ID:       "call_dead",
		Type:     "function",
		Function: &providers.FunctionCall{Name: "", Arguments: "{}"},
	}
	agent.Sessions.AddMessage(sessionKey, "user", "run it")
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{poisoned, nameless}})
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{Role: "tool", Content: "ok", ToolCallID: "call_1"})
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{Role: "tool", Content: "dead", ToolCallID: "call_dead"})
	if err := agent.Sessions.Save(sessionKey); err != nil {
		t.Fatalf("save: %v", err)
	}

	var sentMessages []providers.Message
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			sentMessages = append([]providers.Message(nil), messages...)
			return &providers.LLMResponse{Content: "alive again"}, nil
		},
	}

	_, err = runner.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         "test-channel",
		ChatID:          "test-chat-id",
		UserMessage:     "still there?",
		DefaultResponse: "Default",
		EnableSummary:   false,
		SendResponse:    false,
	})
	if err != nil {
		t.Fatalf("runAgentLoop: %v", err)
	}

	// What went to the provider must be clean, or the 400 comes back.
	var sawToolCall bool
	for _, m := range sentMessages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			sawToolCall = true
			for _, tc := range wireToolCalls(t, m) {
				args := strings.TrimSpace(tc.Function.Arguments)
				if !json.Valid([]byte(args)) || !strings.HasPrefix(args, "{") {
					t.Fatalf("replayed invalid arguments to the provider: %q", args)
				}
			}
		}
	}
	if !sawToolCall {
		t.Fatal("the poisoned turn disappeared from the context instead of being repaired")
	}

	// The repair must reach the store, not just the in-memory copy, otherwise
	// the session is broken again after a restart.
	reopened := session.NewSessionManager()
	reopened.SetStore(s)
	assertSessionWireClean(t, reopened.GetHistory(sessionKey))
	assertStoreHasNoPoisonedToolCalls(t, s, sessionKey)
}
