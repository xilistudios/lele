package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xilistudios/lele/pkg/config"
)

// These tests exercise the Go side of the Claude Code provider without node, the
// Agent SDK or the claude CLI: the provider is pointed at *this test binary*
// re-executed as a helper process that speaks the same JSON-lines protocol as
// the external bridge (xilistudios/lele-claude-bridge, bridge.mjs) (the stdlib
// helper-process pattern).
//
// The bridge protocol itself is covered by `node --test bridge_test.mjs` in that
// repository; the contract tested here is the one Go depends on: handshake,
// dispatch by id, streaming, cancellation, crash recovery and env sanitization.

const (
	claudeFakeBridgeEnv    = "GO_WANT_CLAUDE_BRIDGE_HELPER"
	claudeFakeScenarioEnv  = "CLAUDE_FAKE_BRIDGE_SCENARIO"
	claudeFakeClassEnv     = "CLAUDE_FAKE_BRIDGE_CLASS"
	claudeFakeMessageEnv   = "CLAUDE_FAKE_BRIDGE_MESSAGE"
	claudeFakeCounterEnv   = "CLAUDE_FAKE_BRIDGE_COUNTER"
	claudeFakeFatalMessage = "Claude Agent SDK not found. Install it (npm install -g @anthropic-ai/claude-agent-sdk)."
)

// TestClaudeBridgeHelperProcess is not a test: when the provider spawns this
// binary with the helper env set, it becomes the fake bridge.
func TestClaudeBridgeHelperProcess(t *testing.T) {
	if os.Getenv(claudeFakeBridgeEnv) != "1" {
		t.Skip("helper process: only runs when the provider re-executes the test binary")
	}
	runClaudeFakeBridge()
	// Never let the test framework write to the protocol stream.
	os.Exit(0)
}

// runClaudeFakeBridge is the fake bridge main loop.
func runClaudeFakeBridge() {
	scenario := os.Getenv(claudeFakeScenarioEnv)
	starts := recordClaudeFakeBridgeStart()
	out := json.NewEncoder(os.Stdout)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var envelope struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			continue
		}

		switch envelope.Type {
		case "init":
			if scenario == "fatal" {
				_ = out.Encode(map[string]string{"type": "fatal", "message": claudeFakeFatalMessage})
				os.Exit(1)
			}
			_ = out.Encode(map[string]string{
				"type":        "ready",
				"sdk_version": "fake-sdk-1.0.0",
				"claude_path": "/fake/bin/claude",
			})
		case "cancel":
			// The real bridge aborts and emits nothing more; Go has already
			// returned, so silence is the faithful behaviour.
		case "chat":
			if !claudeFakeChat(out, scenario, starts, line, envelope.ID) {
				return
			}
		}
	}
}

// claudeFakeChat answers one chat request. It reports whether the process should
// keep serving.
func claudeFakeChat(out *json.Encoder, scenario string, starts int, line, id string) bool {
	switch scenario {
	case "crash_once":
		if starts == 1 {
			os.Exit(1)
		}
		return claudeFakeStopResult(out, id)
	case "crash_always":
		os.Exit(1)
	case "hang":
		// Never answer: the caller is expected to cancel.
	case "error":
		class := os.Getenv(claudeFakeClassEnv)
		message := os.Getenv(claudeFakeMessageEnv)
		if message == "" {
			message = "the bridge failed: " + class
		}
		_ = out.Encode(map[string]string{"type": "error", "id": id, "message": message, "class": class})
	case "stream":
		_ = out.Encode(map[string]string{"type": "chunk", "id": id, "text": "po"})
		_ = out.Encode(map[string]string{"type": "reasoning", "id": id, "text": "thinking"})
		_ = out.Encode(map[string]string{"type": "chunk", "id": id, "text": "ng"})
		_ = out.Encode(map[string]any{
			"type":          "result",
			"id":            id,
			"content":       "pong",
			"reasoning":     "thinking",
			"finish_reason": "stop",
			"usage":         fakeClaudeUsage(),
		})
	case "tool_calls":
		_ = out.Encode(map[string]any{
			"type":          "result",
			"id":            id,
			"content":       "let me check",
			"finish_reason": "tool_calls",
			"tool_calls": []map[string]any{
				{"id": "toolu_01", "name": "get_weather", "arguments": map[string]any{"city": "Paris", "days": 2}},
				{"id": "toolu_02", "name": "noop", "arguments": map[string]any{}},
			},
		})
	case "echo_env":
		_ = out.Encode(map[string]any{
			"type":          "result",
			"id":            id,
			"content":       strings.Join(os.Environ(), "\n"),
			"finish_reason": "stop",
		})
	case "echo_request":
		_ = out.Encode(map[string]any{
			"type":          "result",
			"id":            id,
			"content":       line,
			"finish_reason": "stop",
		})
	default:
		return claudeFakeStopResult(out, id)
	}
	return true
}

// claudeFakeStopResult emits the canonical text-only result.
func claudeFakeStopResult(out *json.Encoder, id string) bool {
	_ = out.Encode(map[string]any{
		"type":          "result",
		"id":            id,
		"content":       "pong",
		"finish_reason": "stop",
		"usage":         fakeClaudeUsage(),
	})
	return true
}

// fakeClaudeUsage is deliberately asymmetric (10 uncached + 2 cache-write + 3
// cache-read input tokens, 5 output) so the PromptTokens arithmetic is testable.
func fakeClaudeUsage() map[string]int {
	return map[string]int{
		"input_tokens":                10,
		"output_tokens":               5,
		"cache_creation_input_tokens": 2,
		"cache_read_input_tokens":     3,
	}
}

// recordClaudeFakeBridgeStart appends one mark to the start counter file and
// returns how many starts have happened, so a test can tell a restart from the
// original process.
func recordClaudeFakeBridgeStart() int {
	path := os.Getenv(claudeFakeCounterEnv)
	if path == "" {
		return 1
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 1
	}
	_, _ = file.WriteString("start\n")
	_ = file.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		return 1
	}
	return strings.Count(string(data), "start")
}

// --- test helpers ----------------------------------------------------------

// newTestClaudeProvider returns a provider whose child is this test binary in
// the given scenario. sdkPathOverride keeps the discovery off npm/network.
func newTestClaudeProvider(t *testing.T, scenario string) *ClaudeCodeProvider {
	t.Helper()

	t.Setenv(claudeFakeBridgeEnv, "1")
	t.Setenv(claudeFakeScenarioEnv, scenario)

	p := newClaudeCodeProviderWithBridge("", []string{os.Args[0], "-test.run=TestClaudeBridgeHelperProcess"})
	p.sdkPathOverride = []string{"/fake/node_modules"}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// claudeBridgeOf returns the live child of a provider.
func claudeBridgeOf(t *testing.T, p *ClaudeCodeProvider) *claudeProc {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.proc == nil {
		t.Fatal("provider has no running bridge")
	}
	return p.proc
}

// startTestClaudeBridge spawns the child without sending a chat, so a test can
// observe request registration before calling the provider.
func startTestClaudeBridge(t *testing.T, p *ClaudeCodeProvider) {
	t.Helper()
	if _, err := p.ensureBridge(context.Background()); err != nil {
		t.Fatalf("starting the fake bridge: %v", err)
	}
}

// pendingCount reports how many requests the child still tracks.
func (b *claudeProc) pendingCount() int {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	return len(b.pending)
}

// userMessage is the smallest valid conversation.
func userMessage(text string) []Message {
	return []Message{{Role: "user", Content: text}}
}

// requireFailoverError asserts err is a *FailoverError with the given reason.
func requireFailoverError(t *testing.T, err error, want FailoverReason) *FailoverError {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want a *FailoverError with reason %q", want)
	}
	var failover *FailoverError
	if !errors.As(err, &failover) {
		t.Fatalf("error = %v (%T), want *FailoverError", err, err)
	}
	if failover.Reason != want {
		t.Fatalf("FailoverError.Reason = %q, want %q (error: %v)", failover.Reason, want, err)
	}
	if failover.Provider != "claude-code" {
		t.Fatalf("FailoverError.Provider = %q, want %q", failover.Provider, "claude-code")
	}
	return failover
}

// fileCounterPath allocates a start-counter file for the crash tests.
func fileCounterPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "starts.log")
	t.Setenv(claudeFakeCounterEnv, path)
	return path
}

// --- tests: lifecycle ------------------------------------------------------

func TestClaudeCodeProvider_GetDefaultModel(t *testing.T) {
	p := NewClaudeCodeProvider(t.TempDir())
	if got := p.GetDefaultModel(); got != "claude-code" {
		t.Fatalf("GetDefaultModel() = %q, want %q", got, "claude-code")
	}
}

func TestClaudeCodeProvider_NewResolvesNodeBinFromEnv(t *testing.T) {
	t.Setenv(claudeBridgeEnvNodeBin, "/custom/node")
	p := NewClaudeCodeProvider("/workspace")
	if p.nodeBin != "/custom/node" {
		t.Fatalf("nodeBin = %q, want %q", p.nodeBin, "/custom/node")
	}

	t.Setenv(claudeBridgeEnvNodeBin, "")
	if p := NewClaudeCodeProvider("/workspace"); p.nodeBin != "node" {
		t.Fatalf("nodeBin = %q, want %q", p.nodeBin, "node")
	}
}

// TestClaudeCodeProvider_ChatStopResult covers init -> ready plus the plain
// text result, including the usage arithmetic.
func TestClaudeCodeProvider_ChatStopResult(t *testing.T) {
	p := newTestClaudeProvider(t, "stop")

	response, err := p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if response.Content != "pong" {
		t.Errorf("Content = %q, want %q", response.Content, "pong")
	}
	if response.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", response.FinishReason, "stop")
	}
	if len(response.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %d, want 0", len(response.ToolCalls))
	}

	if response.Usage == nil {
		t.Fatal("Usage = nil, want the mapped usage block")
	}
	// PromptTokens is the *total* billed input (uncached + cache write + cache
	// read), matching the Anthropic Messages convention.
	if got := response.Usage.PromptTokens; got != 15 {
		t.Errorf("Usage.PromptTokens = %d, want 15", got)
	}
	if got := response.Usage.CompletionTokens; got != 5 {
		t.Errorf("Usage.CompletionTokens = %d, want 5", got)
	}
	if got := response.Usage.TotalTokens; got != 20 {
		t.Errorf("Usage.TotalTokens = %d, want 20", got)
	}
	if got := response.Usage.CacheReadInputTokens; got != 3 {
		t.Errorf("Usage.CacheReadInputTokens = %d, want 3", got)
	}
	if got := response.Usage.CacheCreationInputTokens; got != 2 {
		t.Errorf("Usage.CacheCreationInputTokens = %d, want 2", got)
	}
}

// TestClaudeCodeProvider_ChatToolCalls asserts the ToolCall shape matches the
// openai_compat/anthropic_messages convention: ID + Name + decoded Arguments +
// Function.Arguments JSON string (the bridge sends an object, not a string).
func TestClaudeCodeProvider_ChatToolCalls(t *testing.T) {
	p := newTestClaudeProvider(t, "tool_calls")

	response, err := p.Chat(context.Background(), userMessage("weather?"), []ToolDefinition{{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        "get_weather",
			Description: "Get the weather",
			Parameters:  map[string]interface{}{"type": "object"},
		},
	}}, "claude-code", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if response.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want %q", response.FinishReason, "tool_calls")
	}
	if response.Content != "let me check" {
		t.Errorf("Content = %q, want %q", response.Content, "let me check")
	}
	if len(response.ToolCalls) != 2 {
		t.Fatalf("ToolCalls = %d, want 2", len(response.ToolCalls))
	}

	first := response.ToolCalls[0]
	if first.ID != "toolu_01" {
		t.Errorf("ToolCalls[0].ID = %q, want %q", first.ID, "toolu_01")
	}
	if first.Type != "function" {
		t.Errorf("ToolCalls[0].Type = %q, want %q", first.Type, "function")
	}
	if first.Name != "get_weather" {
		t.Errorf("ToolCalls[0].Name = %q, want %q", first.Name, "get_weather")
	}
	if first.Function == nil {
		t.Fatal("ToolCalls[0].Function = nil, want the canonical function call")
	}
	if first.Function.Name != "get_weather" {
		t.Errorf("ToolCalls[0].Function.Name = %q, want %q", first.Function.Name, "get_weather")
	}
	// The arguments object must round-trip unchanged.
	if first.Arguments["city"] != "Paris" {
		t.Errorf("ToolCalls[0].Arguments[city] = %v, want Paris", first.Arguments["city"])
	}
	if first.Arguments["days"] != float64(2) {
		t.Errorf("ToolCalls[0].Arguments[days] = %v (%T), want 2", first.Arguments["days"], first.Arguments["days"])
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(first.Function.Arguments), &decoded); err != nil {
		t.Fatalf("Function.Arguments = %q is not valid JSON: %v", first.Function.Arguments, err)
	}
	if decoded["city"] != "Paris" {
		t.Errorf("Function.Arguments city = %v, want Paris", decoded["city"])
	}
	if first.ArgumentsTruncated {
		t.Error("ToolCalls[0].ArgumentsTruncated = true, want false for a complete payload")
	}

	// An empty arguments object must survive as "{}", never as "null".
	second := response.ToolCalls[1]
	if second.Function == nil || second.Function.Arguments != "{}" {
		t.Errorf("ToolCalls[1].Function.Arguments = %v, want {}", second.Function)
	}

	// The canonical persistence shape must accept what we produced.
	canonical := CanonicalToolCalls(response.ToolCalls)
	if len(canonical) != 2 {
		t.Fatalf("CanonicalToolCalls() kept %d of 2 calls", len(canonical))
	}
	if canonical[0].Function == nil || canonical[0].Function.Arguments != first.Function.Arguments {
		t.Errorf("canonical arguments = %v, want %q", canonical[0].Function, first.Function.Arguments)
	}
} // --- tests: streaming ------------------------------------------------------

// TestClaudeCodeProvider_ChatStream covers delta forwarding and the "done is
// signalled exactly once" contract the agent loop relies on.
func TestClaudeCodeProvider_ChatStream(t *testing.T) {
	p := newTestClaudeProvider(t, "stream")

	var (
		mu        sync.Mutex
		chunks    []string
		reasoning []string
		doneCount int
	)

	response, err := p.ChatStream(context.Background(), userMessage("ping"), nil, "claude-code", nil,
		func(chunk string, done bool) {
			mu.Lock()
			defer mu.Unlock()
			if done {
				doneCount++
				return
			}
			chunks = append(chunks, chunk)
		},
		func(chunk string) {
			mu.Lock()
			defer mu.Unlock()
			reasoning = append(reasoning, chunk)
		})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(chunks, ""); got != "pong" {
		t.Errorf("streamed chunks = %q, want %q", got, "pong")
	}
	if got := strings.Join(reasoning, ""); got != "thinking" {
		t.Errorf("streamed reasoning = %q, want %q", got, "thinking")
	}
	if doneCount != 1 {
		t.Errorf("done signalled %d times, want exactly 1", doneCount)
	}
	if response.Content != "pong" {
		t.Errorf("Content = %q, want %q", response.Content, "pong")
	}
	if response.ReasoningContent != "thinking" {
		t.Errorf("ReasoningContent = %q, want %q", response.ReasoningContent, "thinking")
	}
}

// TestClaudeCodeProvider_ChatStreamNilOnChunkFallsBackToChat keeps the
// openai_compat convention: no chunk callback means a non-streaming request.
func TestClaudeCodeProvider_ChatStreamNilOnChunkFallsBackToChat(t *testing.T) {
	p := newTestClaudeProvider(t, "stop")

	response, err := p.ChatStream(context.Background(), userMessage("ping"), nil, "claude-code", nil, nil, nil)
	if err != nil {
		t.Fatalf("ChatStream(nil onChunk) error = %v", err)
	}
	if response.Content != "pong" {
		t.Errorf("Content = %q, want %q", response.Content, "pong")
	}
}

// TestClaudeCodeProvider_ChatStreamNoChunksStillSignalsDone guards the case
// where the model answers without streaming any delta: the terminal result must
// still close the caller's message.
func TestClaudeCodeProvider_ChatStreamNoChunksStillSignalsDone(t *testing.T) {
	p := newTestClaudeProvider(t, "stop")

	var doneCount int
	_, err := p.ChatStream(context.Background(), userMessage("ping"), nil, "claude-code", nil,
		func(chunk string, done bool) {
			if done {
				doneCount++
			}
		}, nil)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if doneCount != 1 {
		t.Fatalf("done signalled %d times, want exactly 1", doneCount)
	}
}

// TestClaudeCodeProvider_ChatIgnoresDeltas asserts Chat never forwards chunk or
// reasoning events (it passes no callbacks at all).
func TestClaudeCodeProvider_ChatIgnoresDeltas(t *testing.T) {
	p := newTestClaudeProvider(t, "stream")

	response, err := p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if response.Content != "pong" {
		t.Errorf("Content = %q, want %q (the terminal result is authoritative)", response.Content, "pong")
	}
	if response.ReasoningContent != "thinking" {
		t.Errorf("ReasoningContent = %q, want %q", response.ReasoningContent, "thinking")
	}
}

// --- tests: errors ---------------------------------------------------------

func TestClaudeCodeProvider_ErrorClassesMapToFailoverReasons(t *testing.T) {
	tests := []struct {
		class string
		want  FailoverReason
	}{
		{"auth", FailoverAuth},
		{"rate_limit", FailoverRateLimit},
		{"overloaded", FailoverOverloaded},
		{"timeout", FailoverTimeout},
		{"format", FailoverFormat},
		{"unknown", FailoverUnknown},
		{"something-new", FailoverUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.class, func(t *testing.T) {
			p := newTestClaudeProvider(t, "error")
			t.Setenv(claudeFakeClassEnv, tt.class)
			t.Setenv(claudeFakeMessageEnv, "boom")

			_, err := p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil)
			failover := requireFailoverError(t, err, tt.want)
			if failover.Model != "claude-code" {
				t.Errorf("FailoverError.Model = %q, want %q", failover.Model, "claude-code")
			}
			if !strings.Contains(err.Error(), "boom") {
				t.Errorf("error %q does not carry the bridge message", err.Error())
			}
		})
	}
}

// TestClaudeCodeProvider_AuthMessageIsVerbatim pins the requirement that Go does
// not decorate auth failures: the bridge already appended the login hint.
func TestClaudeCodeProvider_AuthMessageIsVerbatim(t *testing.T) {
	const hint = "Run 'claude' and log in with your Claude subscription (no API key)."

	p := newTestClaudeProvider(t, "error")
	t.Setenv(claudeFakeClassEnv, "auth")
	t.Setenv(claudeFakeMessageEnv, "Invalid API key. "+hint)

	_, err := p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil)
	requireFailoverError(t, err, FailoverAuth)

	if count := strings.Count(err.Error(), hint); count != 1 {
		t.Fatalf("login hint appears %d times in %q, want exactly 1", count, err.Error())
	}
}

// TestClaudeCodeProvider_FatalInitIsPlainError checks that a missing SDK is
// reported verbatim and NOT as a failover-class failure (another provider will
// not fix a missing install).
func TestClaudeCodeProvider_FatalInitIsPlainError(t *testing.T) {
	p := newTestClaudeProvider(t, "fatal")

	_, err := p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil)
	if err == nil {
		t.Fatal("Chat() error = nil, want the fatal init error")
	}
	if err.Error() != claudeFakeFatalMessage {
		t.Errorf("error = %q, want %q verbatim", err.Error(), claudeFakeFatalMessage)
	}
	var failover *FailoverError
	if errors.As(err, &failover) {
		t.Errorf("error is a *FailoverError (%q), want a plain error", failover.Reason)
	}
}

// --- tests: cancellation ---------------------------------------------------

// TestClaudeCodeProvider_CancelReturnsContextError asserts the cancellation
// contract: cancel the query, return ctx.Err(), leave no pending entry behind.
func TestClaudeCodeProvider_CancelReturnsContextError(t *testing.T) {
	p := newTestClaudeProvider(t, "hang")
	startTestClaudeBridge(t, p)

	// Warm the bridge so the pending assertion looks at the request, not at the
	// handshake (the child is started lazily by the first call).
	proc := claudeBridgeOf(t, p)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := p.Chat(ctx, userMessage("ping"), nil, "claude-code", nil)
		errCh <- err
	}()

	waitForPending(t, proc, 1)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Chat() error = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Chat() did not return after the context was cancelled")
	}

	waitForPending(t, proc, 0)
}

// TestClaudeCodeProvider_CancelMidStreamStopsDeltas makes sure a cancelled
// streaming request stops calling back.
func TestClaudeCodeProvider_CancelMidStreamStopsDeltas(t *testing.T) {
	p := newTestClaudeProvider(t, "hang")
	startTestClaudeBridge(t, p)
	proc := claudeBridgeOf(t, p)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	chunks := 0
	errCh := make(chan error, 1)
	go func() {
		_, err := p.ChatStream(ctx, userMessage("ping"), nil, "claude-code", nil,
			func(chunk string, done bool) {
				mu.Lock()
				chunks++
				mu.Unlock()
			}, nil)
		errCh <- err
	}()

	waitForPending(t, proc, 1)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ChatStream() error = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ChatStream() did not return after the context was cancelled")
	}

	mu.Lock()
	defer mu.Unlock()
	if chunks != 0 {
		t.Errorf("onChunk called %d times after cancellation, want 0", chunks)
	}
}

// waitForPending blocks until the child tracks exactly want requests. It makes
// the cancel tests deterministic: the cancel must be sent after the chat is
// registered.
func waitForPending(t *testing.T, proc *claudeProc, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if proc.pendingCount() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("pending requests = %d, want %d", proc.pendingCount(), want)
} // --- tests: crash recovery -------------------------------------------------

// TestClaudeCodeProvider_CrashFailsRequestAndRestarts covers the invariant that
// a dead child must fail the in-flight call (the bridge aborts silently on EOF)
// and that the next call transparently spawns a new one.
func TestClaudeCodeProvider_CrashFailsRequestAndRestarts(t *testing.T) {
	// The counter file is what tells the fake bridge "this is the first start",
	// so it crashes exactly once.
	fileCounterPath(t)
	p := newTestClaudeProvider(t, "crash_once")

	_, err := p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil)
	if err == nil {
		t.Fatal("Chat() error = nil, want the crash to fail the request")
	}
	requireFailoverError(t, err, FailoverUnknown)
	if !strings.Contains(err.Error(), "exited") {
		t.Errorf("error = %q, want it to mention the bridge exiting", err.Error())
	}

	// The dead child must be forgotten, so the next call restarts.
	response, err := p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil)
	if err != nil {
		t.Fatalf("Chat() after the crash error = %v, want a restart", err)
	}
	if response.Content != "pong" {
		t.Errorf("Content = %q, want %q", response.Content, "pong")
	}
}

// TestClaudeCodeProvider_CrashIsCounted proves the restart really is a new
// process (the fake bridge logs every start).
func TestClaudeCodeProvider_CrashIsCounted(t *testing.T) {
	counter := fileCounterPath(t)
	p := newTestClaudeProvider(t, "crash_once")

	if _, err := p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil); err == nil {
		t.Fatal("first Chat() error = nil, want a crash")
	}
	if _, err := p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil); err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if _, err := p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil); err != nil {
		t.Fatalf("third Chat() error = %v", err)
	}

	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("reading the start counter: %v", err)
	}
	if starts := strings.Count(string(data), "start"); starts != 2 {
		t.Fatalf("bridge starts = %d, want 2 (crash + one restart)", starts)
	}
}

// TestClaudeCodeProvider_ConcurrentChatsSingleProcess checks one child serves
// several requests and that events are dispatched by id, not by arrival order.
func TestClaudeCodeProvider_ConcurrentChatsSingleProcess(t *testing.T) {
	counter := fileCounterPath(t)
	p := newTestClaudeProvider(t, "stop")

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, errs[index] = p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Chat() #%d error = %v", i, err)
		}
	}

	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("reading the start counter: %v", err)
	}
	if starts := strings.Count(string(data), "start"); starts != 1 {
		t.Fatalf("bridge starts = %d, want 1 (one child serves every request)", starts)
	}
}

// --- tests: Close ----------------------------------------------------------

func TestClaudeCodeProvider_CloseIsTerminal(t *testing.T) {
	p := newTestClaudeProvider(t, "stop")

	if _, err := p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	if _, err := p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil); !errors.Is(err, errClaudeCodeProviderClosed) {
		t.Fatalf("Chat() after Close error = %v, want errClaudeCodeProviderClosed", err)
	}
}

// --- tests: request shape --------------------------------------------------

// TestClaudeCodeProvider_SystemExtractionAndExcludeFilter asserts the request
// the bridge receives: system messages folded into one system prompt, UI-only
// messages dropped, everything else passed through verbatim.
func TestClaudeCodeProvider_SystemExtractionAndExcludeFilter(t *testing.T) {
	p := newTestClaudeProvider(t, "echo_request")

	messages := []Message{
		{Role: "system", Content: "You are lele."},
		{Role: "user", Content: "first"},
		{Role: "system", Content: "Be brief."},
		{Role: "assistant", Content: "an error box", ExcludeFromContext: true, ErrorDetail: "boom"},
		{Role: "assistant", Content: "hi"},
		{Role: "user", Content: "second"},
	}
	tools := []ToolDefinition{{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        "read_file",
			Description: "Read a file",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
	}}

	response, err := p.Chat(context.Background(), messages, tools, "claude-opus-4-7", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	var sent claudeBridgeChatRequest
	if err := json.Unmarshal([]byte(response.Content), &sent); err != nil {
		t.Fatalf("decoding the echoed request: %v (payload: %s)", err, response.Content)
	}

	if sent.Type != "chat" {
		t.Errorf("type = %q, want %q", sent.Type, "chat")
	}
	if sent.ID == "" {
		t.Error("id is empty; the bridge keys its in-flight map by it")
	}
	if _, err := uuid.Parse(sent.ID); err != nil {
		t.Errorf("id = %q is not a uuid: %v", sent.ID, err)
	}
	if sent.System != "You are lele.\n\nBe brief." {
		t.Errorf("system = %q, want the concatenated system messages", sent.System)
	}
	if sent.Model != "claude-opus-4-7" {
		t.Errorf("model = %q, want it passed through verbatim", sent.Model)
	}
	if sent.Stream {
		t.Error("stream = true, want false for Chat()")
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Function.Name != "read_file" {
		t.Errorf("tools = %+v, want the read_file definition", sent.Tools)
	}

	wantRoles := []string{"user", "assistant", "user"}
	if len(sent.Messages) != len(wantRoles) {
		t.Fatalf("messages = %d, want %d (%+v)", len(sent.Messages), len(wantRoles), sent.Messages)
	}
	for i, role := range wantRoles {
		if sent.Messages[i].Role != role {
			t.Errorf("messages[%d].Role = %q, want %q", i, sent.Messages[i].Role, role)
		}
	}
	if sent.Messages[0].Content != "first" || sent.Messages[2].Content != "second" {
		t.Errorf("messages content = %q / %q, want %q / %q",
			sent.Messages[0].Content, sent.Messages[2].Content, "first", "second")
	}
	for _, message := range sent.Messages {
		if message.ExcludeFromContext {
			t.Errorf("an ExcludeFromContext message reached the bridge: %+v", message)
		}
	}
}

// TestClaudeCodeProvider_StreamFlagIsSent checks ChatStream asks the bridge for
// deltas (includePartialMessages).
func TestClaudeCodeProvider_StreamFlagIsSent(t *testing.T) {
	p := newTestClaudeProvider(t, "echo_request")

	response, err := p.ChatStream(context.Background(), userMessage("ping"), nil, "claude-code", nil,
		func(string, bool) {}, nil)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	var sent claudeBridgeChatRequest
	if err := json.Unmarshal([]byte(response.Content), &sent); err != nil {
		t.Fatalf("decoding the echoed request: %v", err)
	}
	if !sent.Stream {
		t.Error("stream = false, want true for ChatStream()")
	}
	if sent.Model != "claude-code" {
		t.Errorf("model = %q, want the sentinel passed through verbatim", sent.Model)
	}
}

// TestClaudeCodeProvider_ToolsOmittedWhenEmpty keeps the wire payload small and
// the MCP server unwired when there is nothing to expose.
func TestClaudeCodeProvider_ToolsOmittedWhenEmpty(t *testing.T) {
	request := buildClaudeBridgeChatRequest(userMessage("ping"), nil, "", false)
	if request.Tools == nil {
		t.Fatal("Tools = nil, want an empty slice (the bridge expects an array)")
	}
	if len(request.Tools) != 0 {
		t.Fatalf("Tools = %d, want 0", len(request.Tools))
	}
	if request.Messages == nil {
		t.Fatal("Messages = nil, want an empty slice")
	}
} // --- tests: environment ----------------------------------------------------

// TestClaudeCodeChildEnv_StripsAPIKeyVariables asserts the child cannot inherit
// anything that would defeat subscription auth.
func TestClaudeCodeChildEnv_StripsAPIKeyVariables(t *testing.T) {
	for _, name := range claudeBridgeStrippedEnvVars {
		t.Setenv(name, "leaked-value")
	}
	t.Setenv(claudeBridgeEnvNodePath, "/preexisting/node_modules")

	env := claudeCodeChildEnv([]string{"/first/node_modules", "/second/node_modules"})

	for _, name := range claudeBridgeStrippedEnvVars {
		if value, ok := lookupEnv(env, name); ok {
			t.Errorf("%s is present in the child env (value %q), want it stripped", name, value)
		}
	}

	nodePath, ok := lookupEnv(env, claudeBridgeEnvNodePath)
	if !ok {
		t.Fatalf("%s is absent from the child env, want it set", claudeBridgeEnvNodePath)
	}
	want := "/preexisting/node_modules" + string(os.PathListSeparator) + "/first/node_modules" + string(os.PathListSeparator) + "/second/node_modules"
	if nodePath != want {
		t.Errorf("%s = %q, want %q", claudeBridgeEnvNodePath, nodePath, want)
	}
}

// TestClaudeCodeChildEnv_KeepsUnrelatedVariables makes sure the strip list does
// not become a whitelist.
func TestClaudeCodeChildEnv_KeepsUnrelatedVariables(t *testing.T) {
	t.Setenv("LELE_CLAUDE_TEST_KEEP_ME", "kept")

	env := claudeCodeChildEnv(nil)
	if value, ok := lookupEnv(env, "LELE_CLAUDE_TEST_KEEP_ME"); !ok || value != "kept" {
		t.Fatalf("LELE_CLAUDE_TEST_KEEP_ME = %q (present %v), want %q", value, ok, "kept")
	}
	if _, ok := lookupEnv(env, claudeBridgeEnvNodePath); ok {
		t.Error("NODE_PATH was set although there were no search paths")
	}
}

// TestClaudeCodeProvider_ChildEnvIsSanitized is the end-to-end version: the fake
// bridge echoes its own environment.
func TestClaudeCodeProvider_ChildEnvIsSanitized(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-leaked")
	t.Setenv("ANTHROPIC_BASE_URL", "https://leaked.example.com")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")

	p := newTestClaudeProvider(t, "echo_env")

	response, err := p.Chat(context.Background(), userMessage("ping"), nil, "claude-code", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	for _, leaked := range []string{"ANTHROPIC_API_KEY=sk-ant-leaked", "ANTHROPIC_BASE_URL=https://leaked.example.com", "CLAUDE_CODE_USE_BEDROCK=1"} {
		if strings.Contains(response.Content, leaked) {
			t.Errorf("child env still contains %q", leaked)
		}
	}
	if !strings.Contains(response.Content, "NODE_PATH=/fake/node_modules") {
		t.Errorf("child env does not carry NODE_PATH=/fake/node_modules:\n%s", firstLines(response.Content, 5))
	}
}

// --- tests: discovery helpers ---------------------------------------------

func TestClaudeCodeProvider_SDKSearchPathsPutTheEnvOverrideFirst(t *testing.T) {
	t.Setenv(claudeBridgeEnvSDKPath, "/from/env")

	p := NewClaudeCodeProvider("/workspace")
	paths := p.sdkSearchPaths()

	if len(paths) == 0 || paths[0] != "/from/env" {
		t.Fatalf("sdkSearchPaths() = %v, want %q first", paths, "/from/env")
	}
	for _, want := range []string{"/opt/homebrew/lib/node_modules", "/usr/local/lib/node_modules"} {
		if !containsString(paths, want) {
			t.Errorf("sdkSearchPaths() = %v, want it to include %q", paths, want)
		}
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if want := filepath.Join(home, ".npm-global", "lib", "node_modules"); !containsString(paths, want) {
			t.Errorf("sdkSearchPaths() = %v, want it to include %q", paths, want)
		}
	}

	// A duplicate must collapse: the bridge walks the list in order.
	if deduped := dedupeStrings([]string{"/a", "/a", "", "/b"}); len(deduped) != 2 || deduped[0] != "/a" || deduped[1] != "/b" {
		t.Fatalf("dedupeStrings() = %v, want [/a /b]", deduped)
	}
}

// TestClaudeCodeExecutablePathHonoursEnv pins the CLAUDE_CODE_PATH override and
// the fallback to the PATH lookup.
func TestClaudeCodeExecutablePathHonoursEnv(t *testing.T) {
	t.Setenv(claudeBridgeEnvClaudePath, "/opt/custom/claude")
	if got := claudeCodeExecutablePath(); got != "/opt/custom/claude" {
		t.Fatalf("claudeCodeExecutablePath() = %q, want the env override", got)
	}

	// Blank override means "not configured": fall back to the PATH lookup, which
	// may legitimately find nothing (the SDK then resolves the CLI itself).
	t.Setenv(claudeBridgeEnvClaudePath, "  ")
	want, err := exec.LookPath("claude")
	if err != nil {
		want = ""
	}
	if got := claudeCodeExecutablePath(); got != want {
		t.Fatalf("claudeCodeExecutablePath() = %q, want %q", got, want)
	}
}

// --- tests: bridge resolution ----------------------------------------------

// TestClaudeBridgeDirHonoursEnvOverride pins LELE_CLAUDE_BRIDGE_PATH: a
// directory, a direct path to bridge.mjs, and whitespace-only (= unset).
func TestClaudeBridgeDirHonoursEnvOverride(t *testing.T) {
	dir := t.TempDir()
	writeClaudeBridgeStub(t, dir, claudeBridgeRequiredFiles...)

	t.Setenv(claudeBridgeEnvBridgePath, dir)
	got, err := claudeBridgeDir()
	if err != nil {
		t.Fatalf("claudeBridgeDir() error = %v", err)
	}
	if got != dir {
		t.Fatalf("claudeBridgeDir() = %q, want %q", got, dir)
	}

	// Both spellings are natural: the env value may point at the script.
	t.Setenv(claudeBridgeEnvBridgePath, filepath.Join(dir, "bridge.mjs"))
	if got, err = claudeBridgeDir(); err != nil || got != dir {
		t.Fatalf("claudeBridgeDir() = (%q, %v), want (%q, nil)", got, err, dir)
	}

	// Blank means "not configured", so the default layout decides.
	leleDir := t.TempDir()
	defaultDir := filepath.Join(leleDir, claudeBridgeSubdir)
	writeClaudeBridgeStub(t, defaultDir, claudeBridgeRequiredFiles...)
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	t.Setenv(claudeBridgeEnvBridgePath, "   ")
	if got, err = claudeBridgeDir(); err != nil || got != defaultDir {
		t.Fatalf("claudeBridgeDir() = (%q, %v), want (%q, nil)", got, err, defaultDir)
	}
}

// TestClaudeBridgeDirDefaultRespectsLeleConfigDir pins the default install dir:
// <lele-dir>/claude-bridge, with LELE_CONFIG_DIR relocating lele's state.
func TestClaudeBridgeDirDefaultRespectsLeleConfigDir(t *testing.T) {
	leleDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	t.Setenv(claudeBridgeEnvBridgePath, "")

	// Negative control: without the files the same layout must not resolve, so
	// the assertion below proves resolution rather than an inherited env value.
	if got, err := claudeBridgeDir(); err == nil {
		t.Fatalf("claudeBridgeDir() = %q, want an error when the bridge is absent", got)
	}

	want := filepath.Join(leleDir, claudeBridgeSubdir)
	writeClaudeBridgeStub(t, want, claudeBridgeRequiredFiles...)
	got, err := claudeBridgeDir()
	if err != nil {
		t.Fatalf("claudeBridgeDir() error = %v", err)
	}
	if got != want {
		t.Fatalf("claudeBridgeDir() = %q, want %q", got, want)
	}
}

// TestClaudeBridgeDirMissingBridgeIsPlainActionableError pins the failure
// contract: the message names the directory, the exact list of files that are
// missing, the install one-liner and the env knob, and it is a plain error,
// never a FailoverError (another provider cannot fix a missing install).
func TestClaudeBridgeDirMissingBridgeIsPlainActionableError(t *testing.T) {
	empty := t.TempDir()
	onlyFirst := t.TempDir()
	writeClaudeBridgeStub(t, onlyFirst, "bridge.mjs")
	onlySecond := t.TempDir()
	writeClaudeBridgeStub(t, onlySecond, "bridge_lib.mjs")
	bothDirs := t.TempDir()
	for _, name := range claudeBridgeRequiredFiles {
		if err := os.MkdirAll(filepath.Join(bothDirs, name), 0o755); err != nil {
			t.Fatalf("creating a directory named %q: %v", name, err)
		}
	}

	tests := []struct {
		name    string
		dir     string
		missing string
	}{
		{name: "absent install", dir: filepath.Join(empty, "never-installed"), missing: "bridge.mjs, bridge_lib.mjs"},
		{name: "half install without bridge_lib", dir: onlyFirst, missing: "bridge_lib.mjs"},
		{name: "half install without bridge", dir: onlySecond, missing: "bridge.mjs"},
		{name: "directories instead of modules", dir: bothDirs, missing: "bridge.mjs, bridge_lib.mjs"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(claudeBridgeEnvBridgePath, test.dir)

			got, err := claudeBridgeDir()
			if err == nil {
				t.Fatalf("claudeBridgeDir() = %q, want an error", got)
			}

			message := err.Error()
			for _, want := range []string{
				test.dir,
				"(missing " + test.missing + ")",
				"lele-claude-bridge/main/install.sh",
				claudeBridgeEnvBridgePath,
			} {
				if !strings.Contains(message, want) {
					t.Errorf("error %q does not mention %q", message, want)
				}
			}

			var failover *FailoverError
			if errors.As(err, &failover) {
				t.Errorf("error %v is a FailoverError, want a plain error", err)
			}
		})
	}
}

// TestClaudeCodeProvider_BridgeCommandUsesNodeAndResolvedDir pins the argv:
// <node> <install dir>/bridge.mjs, and that a missing install never yields one.
func TestClaudeCodeProvider_BridgeCommandUsesNodeAndResolvedDir(t *testing.T) {
	dir := t.TempDir()
	writeClaudeBridgeStub(t, dir, claudeBridgeRequiredFiles...)
	t.Setenv(claudeBridgeEnvBridgePath, dir)
	t.Setenv(claudeBridgeEnvNodeBin, "/custom/node")

	p := NewClaudeCodeProvider("/workspace")
	command, err := p.bridgeCommand()
	if err != nil {
		t.Fatalf("bridgeCommand() error = %v", err)
	}
	if len(command) != 2 {
		t.Fatalf("command = %v, want 2 entries", command)
	}
	if command[0] != "/custom/node" {
		t.Errorf("command[0] = %q, want %q", command[0], "/custom/node")
	}
	if want := filepath.Join(dir, "bridge.mjs"); command[1] != want {
		t.Errorf("command[1] = %q, want %q", command[1], want)
	}
	if _, err := os.Stat(command[1]); err != nil {
		t.Errorf("the resolved bridge script is missing on disk: %v", err)
	}

	// A missing install fails before any argv exists.
	t.Setenv(claudeBridgeEnvBridgePath, filepath.Join(t.TempDir(), "never-installed"))
	if command, err := p.bridgeCommand(); err == nil {
		t.Fatalf("bridgeCommand() = %v, want an error when the bridge is absent", command)
	}
}

// TestClaudeCodeProvider_SDKSearchPathsIncludeInstalledBridge pins the
// candidate order around the installed bridge: CLAUDE_AGENT_SDK_PATH first, then
// the bridge's own node_modules, which is where install.sh puts the SDK.
func TestClaudeCodeProvider_SDKSearchPathsIncludeInstalledBridge(t *testing.T) {
	dir := t.TempDir()
	writeClaudeBridgeStub(t, dir, claudeBridgeRequiredFiles...)
	bridgeNodeModules := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(bridgeNodeModules, 0o755); err != nil {
		t.Fatalf("creating %q: %v", bridgeNodeModules, err)
	}

	t.Setenv(claudeBridgeEnvBridgePath, dir)
	t.Setenv(claudeBridgeEnvSDKPath, "/from/env")

	paths := NewClaudeCodeProvider("/workspace").sdkSearchPaths()
	if len(paths) < 2 {
		t.Fatalf("sdkSearchPaths() = %v, want at least 2 entries", paths)
	}
	if paths[0] != "/from/env" {
		t.Errorf("paths[0] = %q, want %q", paths[0], "/from/env")
	}
	if paths[1] != bridgeNodeModules {
		t.Errorf("paths[1] = %q, want %q", paths[1], bridgeNodeModules)
	}

	// Without an install the candidate must not appear: a stray path would make
	// the integration probe claim an SDK it cannot import.
	leleDir := t.TempDir()
	t.Setenv(claudeBridgeEnvBridgePath, "")
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	for _, path := range NewClaudeCodeProvider("/workspace").sdkSearchPaths() {
		if strings.Contains(path, claudeBridgeSubdir) {
			t.Errorf("sdkSearchPaths() = %v, want no bridge candidate when the bridge is absent", path)
		}
	}
}

// --- small helpers ---------------------------------------------------------

// lookupEnv finds one NAME=VALUE entry in an exec-style environment.
func lookupEnv(env []string, name string) (string, bool) {
	for _, entry := range env {
		if index := strings.IndexByte(entry, '='); index >= 0 && entry[:index] == name {
			return entry[index+1:], true
		}
	}
	return "", false
}

// writeClaudeBridgeStub lays out an install: the named files under dir, with
// dummy content.
func writeClaudeBridgeStub(t *testing.T, dir string, names ...string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %q: %v", dir, err)
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("// stub\n"), 0o644); err != nil {
			t.Fatalf("writing %q: %v", path, err)
		}
	}
}

// firstLines keeps failure output readable.
func firstLines(text string, count int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > count {
		lines = lines[:count]
	}
	return strings.Join(lines, "\n")
}

// containsString reports whether values holds want.
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// --- tests: factory wiring -------------------------------------------------

// TestCreateProvider_ClaudeCodeAliases keeps every alias of the provider type
// routed to the Claude Code provider. Both selection sites are exercised: the
// defaults path and the per-candidate path used by fallback chains.
func TestCreateProvider_ClaudeCodeAliases(t *testing.T) {
	for _, alias := range []string{"claude-cli", "claude-code", "claudecode"} {
		t.Run(alias, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Agents.Defaults.Provider = alias
			cfg.Agents.Defaults.Workspace = "/test/ws"

			provider, err := CreateProvider(cfg)
			if err != nil {
				t.Fatalf("CreateProvider(%s) error = %v", alias, err)
			}
			claudeProvider, ok := provider.(*ClaudeCodeProvider)
			if !ok {
				t.Fatalf("CreateProvider(%s) returned %T, want *ClaudeCodeProvider", alias, provider)
			}
			if claudeProvider.workspace != "/test/ws" {
				t.Errorf("workspace = %q, want %q", claudeProvider.workspace, "/test/ws")
			}
			if got := claudeProvider.GetDefaultModel(); got != "claude-code" {
				t.Errorf("GetDefaultModel() = %q, want %q", got, "claude-code")
			}

			candidate, err := CreateProviderForCandidate(cfg, alias)
			if err != nil {
				t.Fatalf("CreateProviderForCandidate(%s) error = %v", alias, err)
			}
			if _, ok := candidate.(*ClaudeCodeProvider); !ok {
				t.Fatalf("CreateProviderForCandidate(%s) returned %T, want *ClaudeCodeProvider", alias, candidate)
			}
		})
	}
}

// TestCreateProvider_ClaudeCodeDefaultWorkspace checks an empty workspace falls
// back to the configured workspace path (the child's working directory).
func TestCreateProvider_ClaudeCodeDefaultWorkspace(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "claude-cli"
	cfg.Agents.Defaults.Workspace = ""

	provider, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider error = %v", err)
	}
	claudeProvider, ok := provider.(*ClaudeCodeProvider)
	if !ok {
		t.Fatalf("returned %T, want *ClaudeCodeProvider", provider)
	}
	if claudeProvider.workspace != cfg.WorkspacePath() {
		t.Errorf("workspace = %q, want %q (default from WorkspacePath)", claudeProvider.workspace, cfg.WorkspacePath())
	}
}

// TestCreateProvider_DocumentedConfigExample loads the exact config snippet the
// docs ship (docs/agents-models-providers.md, section "Claude Code") so the
// documented shape cannot drift away from what the factory actually selects.
//
// It goes through config.LoadConfig rather than building the struct by hand on
// purpose: the snippet is what a user pastes into config.json, so the JSON
// round trip is part of what has to keep working.
func TestCreateProvider_DocumentedConfigExample(t *testing.T) {
	const documentedConfig = `{
  "agents": {
    "defaults": {
      "provider": "claude-code",
      "model": "claude-code"
    }
  }
}`

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(documentedConfig), 0o600); err != nil {
		t.Fatalf("writing the documented config: %v", err)
	}

	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("config.LoadConfig() error = %v", err)
	}

	provider, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v (the documented config must need no api key)", err)
	}
	claudeProvider, ok := provider.(*ClaudeCodeProvider)
	if !ok {
		t.Fatalf("CreateProvider() returned %T, want *ClaudeCodeProvider", provider)
	}
	if got := claudeProvider.GetDefaultModel(); got != "claude-code" {
		t.Errorf("GetDefaultModel() = %q, want %q", got, "claude-code")
	}
}
