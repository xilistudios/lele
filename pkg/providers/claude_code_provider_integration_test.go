//go:build integration

package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
	exec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests drive the REAL stack: node -> claudecode/bridge.mjs ->
// @anthropic-ai/claude-agent-sdk -> claude CLI, authenticated with the user's
// Claude Code subscription.
//
// Run with: go test -tags=integration ./pkg/providers/ -run ClaudeCode -v
//
// Three things must hold on the machine, and each one that does not turns the
// suite into a skip with an actionable message instead of a failure:
//  1. a node binary (LELE_NODE_BIN, else `node`) — the bridge is JavaScript;
//  2. a resolvable Agent SDK (CLAUDE_AGENT_SDK_PATH, `npm root -g`, the usual
//     global prefixes, or NODE_PATH) — lele never npm-installs anything;
//  3. live credentials: a logged-in `claude` (the CLI or the one bundled with
//     the SDK). Credentials are probed with one short Chat because there is no
//     reliable file-level way to ask "is this subscription logged in?".
//
// Everything else (protocol, streaming, cancellation, env sanitization) is
// covered without node by claude_code_provider_test.go and by
// `node --test pkg/providers/claudecode/bridge_test.mjs`.

// claudeIntegrationTimeout bounds a single Chat/ChatStream against the real
// model. A cold CLI start plus one turn comfortably fits; anything longer is a
// hang worth failing on.
const claudeIntegrationTimeout = 120 * time.Second

// claudeIntegrationPingPrompt is deliberately trivial: the provider's job is
// transport, so the assertion is "a round trip happened", not model quality.
const claudeIntegrationPingPrompt = "Respond with only the word 'pong'."

// claudeIntegrationOutcome records the one-time environment probe. Empty means
// "run the tests"; otherwise the field explains what to do about it.
type claudeIntegrationOutcome struct {
	// skip is set when the machine cannot run these tests at all.
	skip string
	// failure is set when the environment looks usable but the provider broke,
	// which must fail the suite rather than silently skip it.
	failure error
}

var (
	claudeIntegrationProbeOnce    sync.Once
	claudeIntegrationProbeOutcome claudeIntegrationOutcome
)

// requireClaudeCodeIntegration skips the calling test (or fails it, if the
// environment is usable but broken) unless the real stack can be exercised.
func requireClaudeCodeIntegration(t *testing.T) {
	t.Helper()

	// The probe runs once per test binary: it costs a real subscription call,
	// and every test shares the same preconditions.
	claudeIntegrationProbeOnce.Do(func() {
		claudeIntegrationProbeOutcome = probeClaudeCodeIntegration()
	})

	outcome := claudeIntegrationProbeOutcome
	if outcome.skip != "" {
		t.Skip(outcome.skip)
	}
	if outcome.failure != nil {
		t.Fatalf("the Claude Code provider failed its credential probe: %v", outcome.failure)
	}
}

// probeClaudeCodeIntegration checks node, the SDK and the subscription, in that
// order, and reports what a human has to fix when something is missing.
func probeClaudeCodeIntegration() claudeIntegrationOutcome {
	nodeBin := claudeBridgeNodeBin()
	nodePath, err := exec.LookPath(nodeBin)
	if err != nil {
		return claudeIntegrationOutcome{skip: fmt.Sprintf(
			"node binary %q not found: the Claude Code provider needs node >= 18.14 (install node or set LELE_NODE_BIN): %v",
			nodeBin, err)}
	}

	searchPaths := claudeIntegrationSDKSearchPaths()
	sdkDir := claudeIntegrationSDKPackageDir(searchPaths)
	if sdkDir == "" {
		return claudeIntegrationOutcome{skip: fmt.Sprintf(
			"the Claude Agent SDK (@anthropic-ai/claude-agent-sdk) is not resolvable; "+
				"install it with `npm install -g @anthropic-ai/claude-agent-sdk` or set CLAUDE_AGENT_SDK_PATH "+
				"to the node_modules directory that contains it (searched: %s)",
			strings.Join(searchPaths, ", "))}
	}

	workspace, err := os.MkdirTemp("", "lele-claude-probe-")
	if err != nil {
		return claudeIntegrationOutcome{failure: fmt.Errorf("creating the probe workspace: %w", err)}
	}
	defer os.RemoveAll(workspace)

	provider := NewClaudeCodeProvider(workspace)
	provider.sdkPathOverride = searchPaths
	defer func() { _ = provider.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), claudeIntegrationTimeout)
	defer cancel()

	if _, err := provider.Chat(ctx, []Message{{Role: "user", Content: claudeIntegrationPingPrompt}}, nil, "", nil); err != nil {
		if reason := claudeIntegrationSkipReason(nodePath, sdkDir, err); reason != "" {
			return claudeIntegrationOutcome{skip: reason}
		}
		return claudeIntegrationOutcome{failure: err}
	}

	return claudeIntegrationOutcome{}
}

// claudeIntegrationSkipReason turns a probe failure into a skip message when it
// describes an environment problem rather than a bug: missing credentials (the
// common case on CI and on a fresh machine), or an SDK that passed the file
// check but cannot actually be imported.
func claudeIntegrationSkipReason(nodePath, sdkDir string, err error) string {
	// Both messages name the node and SDK that were used: the usual confusion is
	// "I am logged in, but against a different install".
	environment := fmt.Sprintf("node %s, SDK %s", nodePath, sdkDir)
	message := strings.ToLower(err.Error())

	var failover *FailoverError
	credentialsMissing := errors.As(err, &failover) && failover.Reason == FailoverAuth
	if !credentialsMissing {
		// A plain error (not classified as auth) can still be a login problem:
		// the CLI reports it on stderr, which the bridge folds into the message.
		for _, marker := range []string{"not logged in", "please run /login", "unauthorized", "authentication"} {
			if strings.Contains(message, marker) {
				credentialsMissing = true
				break
			}
		}
	}
	if credentialsMissing {
		return fmt.Sprintf(
			"no Claude Code subscription credentials on this machine: run `claude` and log in with your Claude "+
				"subscription (an API key is deliberately not used), then re-run (%s). Probe error: %v",
			environment, err)
	}

	if strings.Contains(message, "claude agent sdk not found") {
		return fmt.Sprintf("the Claude Agent SDK at %s could not be imported by node %s: %v", sdkDir, nodePath, err)
	}

	return ""
}

// newClaudeIntegrationProvider builds a provider that uses exactly the SDK
// search paths the probe validated.
//
// The override is a test seam, not a shortcut: it lets an SDK reached through
// NODE_PATH (which the production discovery does not consult) work here, so the
// suite tests the same resolution the probe verified.
func newClaudeIntegrationProvider(t *testing.T) *ClaudeCodeProvider {
	t.Helper()

	provider := NewClaudeCodeProvider(t.TempDir())
	provider.sdkPathOverride = claudeIntegrationSDKSearchPaths()
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Logf("closing the Claude Code provider: %v", err)
		}
	})

	return provider
}

// claudeIntegrationSDKSearchPaths mirrors the provider's discovery and adds
// NODE_PATH entries, so a developer can point the suite at a local
// node_modules without a global install.
func claudeIntegrationSDKSearchPaths() []string {
	paths := discoverClaudeSDKSearchPaths()
	paths = append(paths, filepath.SplitList(os.Getenv(claudeBridgeEnvNodePath))...)
	return dedupeStrings(paths)
}

// claudeIntegrationSDKPackageDir returns the Agent SDK package directory found
// under one of dirs, or "" when there is none.
//
// A candidate may be a node_modules directory (the normal case, and what
// CLAUDE_AGENT_SDK_PATH documents) or the package directory itself, because
// both spellings are natural for a human to export.
func claudeIntegrationSDKPackageDir(dirs []string) string {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		pkgDir := filepath.Join(dir, "@anthropic-ai", "claude-agent-sdk")
		if _, err := os.Stat(filepath.Join(pkgDir, "package.json")); err == nil {
			return pkgDir
		}
		if dir == "@anthropic-ai/claude-agent-sdk" || filepath.Base(dir) == "claude-agent-sdk" {
			if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
				return dir
			}
		}
	}
	return ""
}

// TestClaudeCodeIntegration_Ping asserts the simplest complete round trip: one
// user message in, assistant text plus usage out.
func TestClaudeCodeIntegration_Ping(t *testing.T) {
	requireClaudeCodeIntegration(t)

	provider := newClaudeIntegrationProvider(t)

	ctx, cancel := context.WithTimeout(context.Background(), claudeIntegrationTimeout)
	defer cancel()

	resp, err := provider.Chat(ctx, []Message{{Role: "user", Content: claudeIntegrationPingPrompt}}, nil, "", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if resp == nil {
		t.Fatal("Chat() returned a nil response")
	}

	t.Logf("content=%q finish_reason=%q usage=%+v", resp.Content, resp.FinishReason, resp.Usage)

	if !strings.Contains(strings.ToLower(resp.Content), "pong") {
		t.Errorf("Content = %q, expected it to contain 'pong' (case-insensitive)", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if resp.ToolCalls != nil {
		t.Errorf("ToolCalls = %v, want none for a plain text turn", resp.ToolCalls)
	}

	// Usage proves the SDK reports real billing data through the bridge, and
	// that the cache counters are folded into the prompt total.
	if resp.Usage == nil {
		t.Fatal("Usage = nil, want the SDK usage block")
	}
	if resp.Usage.PromptTokens <= 0 {
		t.Errorf("PromptTokens = %d, want > 0", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want > 0", resp.Usage.CompletionTokens)
	}
	if want := resp.Usage.PromptTokens + resp.Usage.CompletionTokens; resp.Usage.TotalTokens != want {
		t.Errorf("TotalTokens = %d, want prompt+completion = %d", resp.Usage.TotalTokens, want)
	}
}

// TestClaudeCodeIntegration_ToolRoundTrip asserts the capture-and-interrupt
// contract end to end: the model asks for a lele tool, nothing executes inside
// Claude Code, and replaying the transcript with the tool result yields the
// final answer.
func TestClaudeCodeIntegration_ToolRoundTrip(t *testing.T) {
	requireClaudeCodeIntegration(t)

	provider := newClaudeIntegrationProvider(t)
	tools := []ToolDefinition{claudeIntegrationWeatherTool()}

	ctx, cancel := context.WithTimeout(context.Background(), claudeIntegrationTimeout)
	defer cancel()

	prompt := Message{Role: "user", Content: "What is the weather in Paris? Use the get_weather tool."}
	first, err := provider.Chat(ctx, []Message{prompt}, tools, "", nil)
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}
	if first == nil {
		t.Fatal("first Chat() returned a nil response")
	}

	t.Logf("first turn: content=%q finish_reason=%q tool_calls=%+v", first.Content, first.FinishReason, first.ToolCalls)

	if first.FinishReason != "tool_calls" {
		t.Fatalf("FinishReason = %q, want %q (the bridge must capture the tool_use block)", first.FinishReason, "tool_calls")
	}
	if len(first.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1 (%+v)", len(first.ToolCalls), first.ToolCalls)
	}

	call := first.ToolCalls[0]
	if call.ID == "" {
		t.Error("ToolCall.ID is empty, want the model's tool_use id")
	}
	if call.Name != "get_weather" {
		t.Errorf("ToolCall.Name = %q, want %q (the mcp__lele__ prefix must be stripped)", call.Name, "get_weather")
	}
	// Both wire shapes are populated: the decoded map the agent loop executes
	// and the JSON string Function.Arguments (openai_compat's convention).
	if call.Function == nil {
		t.Fatal("ToolCall.Function = nil, want the function call payload")
	}
	if call.Function.Name != "get_weather" {
		t.Errorf("ToolCall.Function.Name = %q, want %q", call.Function.Name, "get_weather")
	}
	if !strings.Contains(call.Function.Arguments, "city") {
		t.Errorf("ToolCall.Function.Arguments = %q, want it to carry the city argument", call.Function.Arguments)
	}
	city, _ := call.Arguments["city"].(string)
	if !strings.Contains(strings.ToLower(city), "paris") {
		t.Errorf("ToolCall.Arguments[city] = %q, want it to mention Paris", city)
	}

	// Replay: the assistant turn that asked for the tool, then lele's result.
	conversation := []Message{
		prompt,
		{
			Role:             "assistant",
			Content:          first.Content,
			ReasoningContent: first.ReasoningContent,
			ToolCalls:        first.ToolCalls,
		},
		{
			Role:       "tool",
			ToolCallID: call.ID,
			Content:    "sunny, 22C",
		},
	}

	second, err := provider.Chat(ctx, conversation, tools, "", nil)
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if second == nil {
		t.Fatal("second Chat() returned a nil response")
	}

	t.Logf("second turn: content=%q finish_reason=%q", second.Content, second.FinishReason)

	if second.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", second.FinishReason, "stop")
	}
	answer := strings.ToLower(second.Content)
	if !strings.Contains(answer, "22") && !strings.Contains(answer, "sunny") {
		t.Errorf("Content = %q, expected it to mention the tool result ('sunny, 22C')", second.Content)
	}
}

// TestClaudeCodeIntegration_ChatStream asserts streaming wiring: deltas arrive
// as they are produced and `done` is signalled exactly once, on the terminal
// result.
func TestClaudeCodeIntegration_ChatStream(t *testing.T) {
	requireClaudeCodeIntegration(t)

	provider := newClaudeIntegrationProvider(t)

	ctx, cancel := context.WithTimeout(context.Background(), claudeIntegrationTimeout)
	defer cancel()

	// The provider invokes these callbacks synchronously from ChatStream, so
	// plain counters are safe (no goroutine crosses this boundary).
	var (
		chunks       []string
		chunksAfterD bool
		doneCount    int
	)

	resp, err := provider.ChatStream(
		ctx,
		[]Message{{Role: "user", Content: claudeIntegrationPingPrompt}},
		nil,
		"",
		nil,
		func(chunk string, done bool) {
			if done {
				doneCount++
				return
			}
			if doneCount > 0 {
				chunksAfterD = true
			}
			if chunk != "" {
				chunks = append(chunks, chunk)
			}
		},
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if resp == nil {
		t.Fatal("ChatStream() returned a nil response")
	}

	t.Logf("chunks=%d done=%d streamed=%q content=%q finish_reason=%q",
		len(chunks), doneCount, strings.Join(chunks, ""), resp.Content, resp.FinishReason)

	if len(chunks) == 0 {
		t.Error("received no chunks, want at least one assistant delta")
	}
	if doneCount != 1 {
		t.Errorf("done signalled %d times, want exactly 1", doneCount)
	}
	if chunksAfterD {
		t.Error("received a chunk after the done signal, want done to be terminal")
	}
	if !strings.Contains(strings.ToLower(resp.Content), "pong") {
		t.Errorf("Content = %q, expected it to contain 'pong' (case-insensitive)", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
}

// claudeIntegrationWeatherTool is the fake tool the round-trip test exposes.
// Its JSON Schema is passed to the model verbatim, so it is a realistic
// "object with one required string" shape.
func claudeIntegrationWeatherTool() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        "get_weather",
			Description: "Report the current weather for a city.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city": map[string]interface{}{
						"type":        "string",
						"description": "City name, for example Paris.",
					},
				},
				"required": []interface{}{"city"},
			},
		},
	}
}
