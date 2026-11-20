// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package providers

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers/common"
)

// The bridge is two ES modules. They are embedded (never written by hand at
// runtime) so lele stays a single binary, and extracted side by side because
// bridge.mjs imports ./bridge_lib.mjs relatively:
//
//	lele agent loop (Go)
//	  └─ persistent child: node bridge.mjs   (JSON lines over stdio)
//	       └─ @anthropic-ai/claude-agent-sdk query()
//	            └─ claude CLI (subscription auth)
//
// bridge_test.mjs is deliberately NOT embedded: it is a node unit test.
//
//go:embed claudecode/bridge.mjs claudecode/bridge_lib.mjs
var claudeBridgeAssets embed.FS

// Bridge protocol constants. Wire documentation lives in the spec and in
// claudecode/bridge.mjs; these names are the only strings Go needs.
const (
	claudeBridgeModel = "claude-code"

	claudeBridgeEventReady     = "ready"
	claudeBridgeEventFatal     = "fatal"
	claudeBridgeEventChunk     = "chunk"
	claudeBridgeEventReasoning = "reasoning"
	claudeBridgeEventResult    = "result"
	claudeBridgeEventError     = "error"
	claudeBridgeEventLog       = "log"

	claudeBridgeRequestInit   = "init"
	claudeBridgeRequestChat   = "chat"
	claudeBridgeRequestCancel = "cancel"

	claudeBridgeDirPrefix = "lele-claude-bridge-"

	// claudeBridgeInitTimeout bounds the init -> ready handshake. Node + ESM
	// resolution of a globally installed SDK is fast on a warm cache and a few
	// hundred ms cold; anything near this bound means something is wrong.
	claudeBridgeInitTimeout = 30 * time.Second
	// claudeBridgeCloseTimeout bounds "stdin closed" -> "process gone" before
	// the child is killed, and the follow-up wait for the reap.
	claudeBridgeCloseTimeout = 3 * time.Second
	// claudeBridgeDrainTimeout bounds how long a fatal line read just before
	// EOF may take to reach the init waiter (see waitForExitHandshake).
	claudeBridgeDrainTimeout = time.Second
	// claudeBridgeNpmRootTimeout bounds `npm root -g` (best effort discovery).
	claudeBridgeNpmRootTimeout = 5 * time.Second
	// claudeBridgeEventBuffer is the per-request event queue. Streaming events
	// are consumed as they arrive, so this only absorbs a short burst (chunks
	// and the terminating result emitted back to back).
	claudeBridgeEventBuffer = 32

	claudeBridgeEnvNodeBin    = "LELE_NODE_BIN"
	claudeBridgeEnvSDKPath    = "CLAUDE_AGENT_SDK_PATH"
	claudeBridgeEnvClaudePath = "CLAUDE_CODE_PATH"
	claudeBridgeEnvNodePath   = "NODE_PATH"
)

// claudeBridgeStrippedEnvVars are removed from the child environment.
//
// The provider authenticates with the user's Claude Code *subscription*, so
// anything that could redirect the CLI to an API key, a proxy or a cloud
// provider (and thereby silently bill a different account, or fail with 401
// because a stale key is picked up) must not be inherited. Keep this list in
// sync with the spec.
var claudeBridgeStrippedEnvVars = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_CUSTOM_HEADERS",
	"ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_SKIP_BEDROCK_AUTH",
	"CLAUDE_CODE_SKIP_VERTEX_AUTH",
}

// errClaudeCodeProviderClosed is returned by a provider whose Close was called.
var errClaudeCodeProviderClosed = errors.New("the Claude Code provider is closed") // --- protocol payloads -----------------------------------------------------

// claudeBridgeInitRequest is the first line written to the child. It resolves
// the SDK (search paths are candidate node_modules dirs) and pins the claude
// CLI binary when one was found.
type claudeBridgeInitRequest struct {
	Type           string   `json:"type"`
	SDKSearchPaths []string `json:"sdk_search_paths"`
	ClaudePath     string   `json:"claude_path"`
}

// claudeBridgeChatRequest is one model step. Messages are lele
// protocoltypes.Message JSON verbatim (the bridge understands both the plain
// string content and the content_parts form); system messages have already
// been folded into System because the SDK takes a single system prompt.
type claudeBridgeChatRequest struct {
	Type     string           `json:"type"`
	ID       string           `json:"id"`
	System   string           `json:"system"`
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools"`
	Model    string           `json:"model"`
	Stream   bool             `json:"stream"`
}

// claudeBridgeCancelRequest aborts one in-flight chat.
type claudeBridgeCancelRequest struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// claudeBridgeEvent is the union of every event the bridge writes to stdout.
// One struct keeps the read loop simple; the field a given type uses is
// documented per field.
type claudeBridgeEvent struct {
	Type string `json:"type"`
	// ID is empty for the process-scoped events (ready, fatal, log).
	ID string `json:"id,omitempty"`
	// Text carries a chunk (assistant delta) or a reasoning (thinking delta).
	Text string `json:"text,omitempty"`
	// Message carries a fatal/error description or a log line.
	Message string `json:"message,omitempty"`
	// Class is the error classification of an `error` event.
	Class string `json:"class,omitempty"`
	// Level is the severity of a `log` event.
	Level string `json:"level,omitempty"`
	// SDKVersion and ClaudePath are set on `ready`.
	SDKVersion string `json:"sdk_version,omitempty"`
	ClaudePath string `json:"claude_path,omitempty"`
	// Result payload. tool_calls, reasoning and usage are ABSENT when empty.
	Content      string                  `json:"content,omitempty"`
	Reasoning    string                  `json:"reasoning,omitempty"`
	ToolCalls    []claudeBridgeToolCall  `json:"tool_calls,omitempty"`
	FinishReason string                  `json:"finish_reason,omitempty"`
	Usage        *claudeBridgeUsageBlock `json:"usage,omitempty"`
}

// claudeBridgeToolCall is one captured tool_use block. Unlike the OpenAI wire
// format, Arguments is a JSON object, not a JSON string: the bridge reports the
// block input exactly as the model produced it.
type claudeBridgeToolCall struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// claudeBridgeUsageBlock mirrors the SDK usage block.
type claudeBridgeUsageBlock struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
} // --- provider --------------------------------------------------------------

// ClaudeCodeProvider implements LLMProvider (and StreamingLLMProvider) on top
// of a persistent `node bridge.mjs` child that drives the Claude Agent SDK.
//
// It keeps lele's own agent loop: the SDK query is one model step with all
// built-in Claude Code tools disabled. Tool calls are captured by the bridge
// and handed back with finish_reason "tool_calls"; lele executes them and
// re-enters Chat with the appended history. The bridge replays the whole
// conversation on every call (no --resume, no session files).
//
// The child is started lazily on the first Chat/ChatStream and restarted on
// the next call after it dies, so a crash never needs a process restart.
type ClaudeCodeProvider struct {
	workspace string
	nodeBin   string

	// startMu serializes (re)starts; mu guards the fields below it.
	startMu sync.Mutex
	mu      sync.Mutex
	proc    *claudeProc
	closed  bool

	// commandOverride replaces the spawned argv. Test seam only (the unit
	// tests use a Go helper process instead of node).
	commandOverride []string
	// sdkPathOverride replaces the discovered node_modules candidates. Test
	// seam only, so tests never shell out to npm.
	sdkPathOverride []string

	// sdkPathsOnce caches the discovery: it runs `npm root -g`, and a bridge
	// restart must not pay for it again.
	sdkPathsOnce sync.Once
	sdkPaths     []string
}

// Compile-time contract checks.
var (
	_ LLMProvider          = (*ClaudeCodeProvider)(nil)
	_ StreamingLLMProvider = (*ClaudeCodeProvider)(nil)
	_ io.Closer            = (*ClaudeCodeProvider)(nil)
)

// NewClaudeCodeProvider creates a provider bound to workspace (the child's
// working directory). Nothing is spawned until the first call.
func NewClaudeCodeProvider(workspace string) *ClaudeCodeProvider {
	return &ClaudeCodeProvider{
		workspace: workspace,
		nodeBin:   claudeBridgeNodeBin(),
	}
}

// newClaudeCodeProviderWithBridge builds a provider that spawns command
// instead of `node bridge.mjs`. Unit tests point it at a Go helper binary that
// speaks the JSON-lines protocol, so no node, SDK or claude CLI is needed.
func newClaudeCodeProviderWithBridge(workspace string, command []string) *ClaudeCodeProvider {
	p := NewClaudeCodeProvider(workspace)
	p.commandOverride = command
	return p
}

// GetDefaultModel returns the sentinel model of this provider: the bridge maps
// it (and "") to "let Claude Code pick its default".
func (p *ClaudeCodeProvider) GetDefaultModel() string {
	return claudeBridgeModel
}

// Chat implements LLMProvider. Chunk and reasoning events are ignored; only the
// terminal result matters.
//
// options is accepted for interface compatibility and ignored: every knob this
// provider honours (model, system prompt, tool set, cancellation) is carried by
// the protocol message itself.
func (p *ClaudeCodeProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	return p.chat(ctx, messages, tools, model, false, nil, nil)
}

// ChatStream implements StreamingLLMProvider. It mirrors openai_compat: a nil
// onChunk degrades to the non-streaming path, deltas are forwarded as they
// arrive with done=false, and `onChunk("", true)` is emitted exactly once, on
// the terminal result — never on a failure, where the caller is unwinding.
func (p *ClaudeCodeProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}, onChunk func(chunk string, done bool), onReasoning func(reasoningChunk string)) (*LLMResponse, error) {
	if onChunk == nil {
		return p.Chat(ctx, messages, tools, model, options)
	}
	return p.chat(ctx, messages, tools, model, true, onChunk, onReasoning)
}

// Close shuts the bridge down (stdin EOF, then kill after a grace period).
// The provider cannot be used afterwards.
func (p *ClaudeCodeProvider) Close() error {
	p.mu.Lock()
	proc := p.proc
	p.proc = nil
	p.closed = true
	p.mu.Unlock()

	if proc == nil {
		return nil
	}
	return proc.shutdown()
}

// chat is the single implementation behind Chat and ChatStream.
func (p *ClaudeCodeProvider) chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, stream bool, onChunk func(chunk string, done bool), onReasoning func(reasoningChunk string)) (*LLMResponse, error) {
	request := buildClaudeBridgeChatRequest(messages, tools, model, stream)

	proc, err := p.ensureBridge(ctx)
	if err != nil {
		return nil, err
	}

	return p.roundTrip(ctx, proc, request, stream, onChunk, onReasoning)
}

// buildClaudeBridgeChatRequest converts lele messages into the bridge request:
// messages flagged ExcludeFromContext are dropped (they are UI/persistence
// artefacts and must never reach the model) and system messages are folded
// into the single system prompt the SDK accepts.
func buildClaudeBridgeChatRequest(messages []Message, tools []ToolDefinition, model string, stream bool) *claudeBridgeChatRequest {
	request := &claudeBridgeChatRequest{
		Type:     claudeBridgeRequestChat,
		ID:       uuid.NewString(),
		Messages: make([]Message, 0, len(messages)),
		Tools:    make([]ToolDefinition, 0, len(tools)),
		Model:    model,
		Stream:   stream,
	}

	var system []string
	for _, message := range messages {
		if message.ExcludeFromContext {
			continue
		}
		if message.Role == "system" {
			if text := strings.TrimSpace(message.Content); text != "" {
				system = append(system, text)
			}
			continue
		}
		request.Messages = append(request.Messages, message)
	}
	request.System = strings.Join(system, "\n\n")

	if len(tools) > 0 {
		request.Tools = append(request.Tools, tools...)
	}

	return request
}

// roundTrip writes one chat request and waits for its terminal event.
func (p *ClaudeCodeProvider) roundTrip(ctx context.Context, proc *claudeProc, request *claudeBridgeChatRequest, stream bool, onChunk func(chunk string, done bool), onReasoning func(reasoningChunk string)) (*LLMResponse, error) {
	pending := proc.register(request.ID)
	defer proc.unregister(request.ID)

	if err := proc.send(request); err != nil {
		return nil, p.failoverError(FailoverUnknown, request.Model,
			fmt.Errorf("sending a chat request to the Claude Code bridge: %w", err))
	}

	for {
		var event claudeBridgeEvent
		select {
		case <-ctx.Done():
			// Tell the bridge to abort the query; it emits nothing more for
			// this id and the caller has already given up. The deferred
			// unregister keeps the pending map from leaking the entry.
			proc.cancel(request.ID)
			return nil, ctx.Err()
		case event = <-pending.events:
		case <-proc.exitDone:
			// The child is gone: fail this call. A terminal event may still be
			// queued (the bridge answered and then exited), so prefer it.
			select {
			case event = <-pending.events:
			default:
				return nil, p.failoverError(FailoverUnknown, request.Model, proc.exitError())
			}
		}

		switch event.Type {
		case claudeBridgeEventChunk:
			if stream && onChunk != nil && event.Text != "" {
				onChunk(event.Text, false)
			}
		case claudeBridgeEventReasoning:
			if stream && onReasoning != nil && event.Text != "" {
				onReasoning(event.Text)
			}
		case claudeBridgeEventResult:
			response := convertClaudeBridgeResult(event)
			if stream && onChunk != nil {
				onChunk("", true)
			}
			return response, nil
		case claudeBridgeEventError:
			return nil, p.bridgeError(request.Model, event)
		default:
			logger.DebugCF("claudecode", "ignoring unexpected bridge event", map[string]interface{}{
				"type": event.Type,
				"id":   event.ID,
			})
		}
	}
}

// bridgeError turns an `error` event into a classified provider error. The
// message is passed through verbatim: for auth failures the bridge already
// appended the "log in with your subscription" hint.
func (p *ClaudeCodeProvider) bridgeError(model string, event claudeBridgeEvent) error {
	message := strings.TrimSpace(event.Message)
	if message == "" {
		message = "the Claude Code bridge reported an error without a message"
	}
	return p.failoverError(claudeBridgeFailoverReason(event.Class), model, errors.New(message))
}

// failoverError wraps err so the fallback chain can classify it.
func (p *ClaudeCodeProvider) failoverError(reason FailoverReason, model string, err error) *FailoverError {
	return &FailoverError{
		Reason:   reason,
		Provider: claudeBridgeModel,
		Model:    model,
		Wrapped:  err,
	}
}

// claudeBridgeFailoverReason maps the bridge error classes 1:1 onto the
// failover reasons (the classes exist for exactly this mapping).
func claudeBridgeFailoverReason(class string) FailoverReason {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case "auth":
		return FailoverAuth
	case "rate_limit":
		return FailoverRateLimit
	case "overloaded":
		return FailoverOverloaded
	case "timeout":
		return FailoverTimeout
	case "format":
		return FailoverFormat
	default:
		return FailoverUnknown
	}
} // --- response conversion ---------------------------------------------------

// convertClaudeBridgeResult maps a `result` event onto lele's LLMResponse.
//
// Tool-call construction follows openai_compat.parseResponse: the decoded
// arguments map plus the truncation flag, with Function.Arguments carrying the
// same payload as a JSON string (the shape openai_compat's SSE path and
// anthropic_messages build from a block-based response — the case this provider
// is closest to). CanonicalToolCalls normalises either shape before the
// assistant message is persisted, so no call site can observe the difference.
func convertClaudeBridgeResult(event claudeBridgeEvent) *LLMResponse {
	response := &LLMResponse{
		Content:          event.Content,
		ReasoningContent: event.Reasoning,
		Reasoning:        event.Reasoning,
		FinishReason:     event.FinishReason,
	}
	if response.FinishReason == "" {
		response.FinishReason = "stop"
	}

	for _, call := range event.ToolCalls {
		argumentsJSON, err := json.Marshal(call.Arguments)
		if err != nil {
			// Not reachable for a decoded JSON object; degrade to empty
			// arguments rather than dropping the call.
			argumentsJSON = []byte("{}")
		}
		arguments, truncated := common.DecodeToolCallArgumentsTruncated(argumentsJSON, call.Name)
		response.ToolCalls = append(response.ToolCalls, ToolCall{
			ID:                 call.ID,
			Type:               "function",
			Name:               call.Name,
			Arguments:          arguments,
			ArgumentsTruncated: truncated,
			Function: &FunctionCall{
				Name:      call.Name,
				Arguments: string(argumentsJSON),
			},
		})
	}

	if event.Usage != nil {
		usage := event.Usage
		// PromptTokens is the *total* billed input: the SDK reports cached
		// tokens separately, exactly like the Anthropic Messages API.
		promptTokens := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
		response.Usage = &UsageInfo{
			PromptTokens:             promptTokens,
			CompletionTokens:         usage.OutputTokens,
			TotalTokens:              promptTokens + usage.OutputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
		}
	}

	return response
}

// --- process management ----------------------------------------------------

// ensureBridge returns the live bridge, starting it if there is none. A dead
// bridge is simply replaced: request state lives in the child, and every chat
// carries its full history, so a restart loses nothing.
func (p *ClaudeCodeProvider) ensureBridge(ctx context.Context) (*claudeProc, error) {
	p.startMu.Lock()
	defer p.startMu.Unlock()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errClaudeCodeProviderClosed
	}
	proc := p.proc
	p.mu.Unlock()

	if proc != nil {
		return proc, nil
	}

	proc, err := p.startBridge(ctx)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	if p.closed {
		// Close() raced the start: never leak the child.
		p.mu.Unlock()
		_ = proc.shutdown()
		return nil, errClaudeCodeProviderClosed
	}
	p.proc = proc
	p.mu.Unlock()

	return proc, nil
}

// forgetBridge drops a dead child so the next call restarts it.
func (p *ClaudeCodeProvider) forgetBridge(proc *claudeProc) {
	p.mu.Lock()
	if p.proc == proc {
		p.proc = nil
	}
	p.mu.Unlock()
}

// startBridge extracts the assets, spawns the child and performs the
// init -> ready handshake.
//
// The command is deliberately not exec.CommandContext: the child serves many
// requests and must outlive the context of the call that happened to start it.
// ctx only bounds the handshake here; per-request cancellation goes through the
// `cancel` protocol message.
func (p *ClaudeCodeProvider) startBridge(ctx context.Context) (*claudeProc, error) {
	command, err := p.bridgeCommand()
	if err != nil {
		return nil, err
	}

	searchPaths := p.sdkSearchPaths()
	claudePath := claudeCodeExecutablePath()

	cmd := exec.Command(command[0], command[1:]...)
	if p.workspace != "" {
		cmd.Dir = p.workspace
	}
	cmd.Env = claudeCodeChildEnv(searchPaths)

	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("creating the Claude Code bridge stdin pipe: %w", err)
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		closeAll(stdinRead, stdinWrite)
		return nil, fmt.Errorf("creating the Claude Code bridge stdout pipe: %w", err)
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		closeAll(stdinRead, stdinWrite, stdoutRead, stdoutWrite)
		return nil, fmt.Errorf("creating the Claude Code bridge stderr pipe: %w", err)
	}

	// Own pipes instead of StdinPipe/StdoutPipe: cmd.Wait closes the pipes it
	// created, which would race the reader goroutines below.
	cmd.Stdin = stdinRead
	cmd.Stdout = stdoutWrite
	cmd.Stderr = stderrWrite

	if err := cmd.Start(); err != nil {
		closeAll(stdinRead, stdinWrite, stdoutRead, stdoutWrite, stderrRead, stderrWrite)
		return nil, fmt.Errorf("starting the Claude Code bridge (%s): %w", strings.Join(command, " "), err)
	}
	// The child holds its own ends now.
	closeAll(stdinRead, stdoutWrite, stderrWrite)

	proc := &claudeProc{
		cmd:      cmd,
		stdin:    stdinWrite,
		pending:  make(map[string]*claudePending),
		initCh:   make(chan claudeBridgeEvent, 4),
		exitDone: make(chan struct{}),
		readDone: make(chan struct{}),
		onExit:   p.forgetBridge,
	}

	go proc.pumpStderr(stderrRead)
	go proc.readLoop(stdoutRead)
	go proc.wait()

	if err := proc.send(&claudeBridgeInitRequest{
		Type:           claudeBridgeRequestInit,
		SDKSearchPaths: searchPaths,
		ClaudePath:     claudePath,
	}); err != nil {
		_ = proc.shutdown()
		return nil, fmt.Errorf("initializing the Claude Code bridge: %w", err)
	}

	return p.waitForReady(ctx, proc)
}

// waitForReady blocks until the bridge reports ready (or fatal).
func (p *ClaudeCodeProvider) waitForReady(ctx context.Context, proc *claudeProc) (*claudeProc, error) {
	timer := time.NewTimer(claudeBridgeInitTimeout)
	defer timer.Stop()

	for {
		select {
		case event := <-proc.initCh:
			switch event.Type {
			case claudeBridgeEventReady:
				logger.DebugCF("claudecode", "Claude Code bridge ready", map[string]interface{}{
					"sdk_version": event.SDKVersion,
					"claude_path": event.ClaudePath,
				})
				return proc, nil
			case claudeBridgeEventFatal:
				_ = proc.shutdown()
				// Verbatim: the message is the actionable install hint, and it
				// is NOT a failover-class failure (another provider will not
				// fix a missing SDK), so it stays a plain error.
				return nil, errors.New(strings.TrimSpace(event.Message))
			}
			logger.DebugCF("claudecode", "ignoring unexpected bridge event during init", map[string]interface{}{
				"type": event.Type,
			})
		case <-proc.exitDone:
			// The child answered (fatal) and exited, or just died. Give the
			// reader a bounded moment to publish a line it read before EOF so
			// the install hint wins over a generic crash report.
			select {
			case <-proc.readDone:
			case <-time.After(claudeBridgeDrainTimeout):
			}
			select {
			case event := <-proc.initCh:
				if event.Type == claudeBridgeEventFatal {
					_ = proc.shutdown()
					return nil, errors.New(strings.TrimSpace(event.Message))
				}
			default:
			}
			exitErr := proc.exitError()
			_ = proc.shutdown()
			return nil, fmt.Errorf("the Claude Code bridge exited during init: %w", exitErr)
		case <-timer.C:
			_ = proc.shutdown()
			return nil, fmt.Errorf("the Claude Code bridge did not become ready within %s", claudeBridgeInitTimeout)
		case <-ctx.Done():
			_ = proc.shutdown()
			return nil, ctx.Err()
		}
	}
}

// bridgeCommand returns the argv of the child process.
func (p *ClaudeCodeProvider) bridgeCommand() ([]string, error) {
	if len(p.commandOverride) > 0 {
		return p.commandOverride, nil
	}
	dir, err := claudeBridgeDir()
	if err != nil {
		return nil, err
	}
	return []string{p.nodeBin, filepath.Join(dir, "bridge.mjs")}, nil
} // --- child process ---------------------------------------------------------

// claudePending is one in-flight chat: the queue the read loop fills and the
// roundTrip consumes.
type claudePending struct {
	events chan claudeBridgeEvent
}

// claudeProc is a running bridge child. One read loop dispatches stdout events
// by request id; a second goroutine drains stderr to the debug log; a third
// reaps the process and publishes the exit.
type claudeProc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]*claudePending

	// initCh carries the id-less process events (ready/fatal).
	initCh chan claudeBridgeEvent

	// exitDone closes once cmd.Wait returned; readDone once stdout hit EOF.
	exitDone chan struct{}
	readDone chan struct{}

	exitMu  sync.Mutex
	exitErr error

	stderrMu   sync.Mutex
	stderrTail string

	onExit func(*claudeProc)
}

// send writes one JSON line. It fails fast once the child is gone.
func (b *claudeProc) send(payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding a bridge request: %w", err)
	}
	data = append(data, '\n')

	select {
	case <-b.exitDone:
		return b.exitError()
	default:
	}

	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if _, err := b.stdin.Write(data); err != nil {
		return err
	}
	return nil
}

// cancel aborts one in-flight chat. Unknown ids are ignored by the bridge, so a
// cancel that races a finished request is harmless.
func (b *claudeProc) cancel(id string) {
	if err := b.send(&claudeBridgeCancelRequest{Type: claudeBridgeRequestCancel, ID: id}); err != nil {
		logger.DebugCF("claudecode", "sending a bridge cancel failed", map[string]interface{}{
			"id":    id,
			"error": err.Error(),
		})
	}
}

// register reserves a queue for one request id.
func (b *claudeProc) register(id string) *claudePending {
	pending := &claudePending{events: make(chan claudeBridgeEvent, claudeBridgeEventBuffer)}
	b.pendingMu.Lock()
	b.pending[id] = pending
	b.pendingMu.Unlock()
	return pending
}

// unregister drops the queue (idempotent: the caller may race a cancellation).
func (b *claudeProc) unregister(id string) {
	b.pendingMu.Lock()
	delete(b.pending, id)
	b.pendingMu.Unlock()
}

// readLoop turns stdout into events until EOF.
func (b *claudeProc) readLoop(stdout io.ReadCloser) {
	defer close(b.readDone)
	defer stdout.Close()

	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, err := reader.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			b.dispatch(trimmed)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logger.DebugC("claudecode", "reading the Claude Code bridge stdout failed: "+err.Error())
			}
			return
		}
	}
}

// dispatch routes one decoded stdout line.
func (b *claudeProc) dispatch(line string) {
	var event claudeBridgeEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		logger.DebugCF("claudecode", "ignoring a malformed bridge event", map[string]interface{}{
			"error": err.Error(),
			"line":  truncateForLog(line, 512),
		})
		return
	}

	switch event.Type {
	case claudeBridgeEventLog:
		b.logEvent(event)
		return
	case claudeBridgeEventReady, claudeBridgeEventFatal:
		// Process-scoped: only the handshake waits for these, and a second one
		// (never sent by the bridge) is dropped rather than blocking the loop.
		select {
		case b.initCh <- event:
		default:
		}
		return
	}

	if event.ID == "" {
		logger.DebugCF("claudecode", "ignoring a bridge event without an id", map[string]interface{}{
			"type": event.Type,
		})
		return
	}

	b.pendingMu.Lock()
	pending := b.pending[event.ID]
	b.pendingMu.Unlock()
	if pending == nil {
		// Cancelled or abandoned request: the caller already returned.
		return
	}

	select {
	case pending.events <- event:
	case <-b.exitDone:
	}
}

// logEvent forwards a bridge diagnostic line to the logger.
func (b *claudeProc) logEvent(event claudeBridgeEvent) {
	message := strings.TrimSpace(event.Message)
	if message == "" {
		return
	}
	switch strings.ToLower(event.Level) {
	case "warn":
		logger.WarnC("claudecode", message)
	case "error":
		logger.ErrorC("claudecode", message)
	default:
		logger.DebugC("claudecode", message)
	}
}

// pumpStderr logs the child's stderr at debug level and keeps the tail so a
// crash can be reported with its own diagnostics.
func (b *claudeProc) pumpStderr(stderr io.ReadCloser) {
	defer stderr.Close()

	reader := bufio.NewReaderSize(stderr, 16*1024)
	for {
		line, err := reader.ReadString('\n')
		if text := strings.TrimSpace(line); text != "" {
			logger.DebugC("claudecode", text)
			b.appendStderr(text)
		}
		if err != nil {
			return
		}
	}
}

func (b *claudeProc) appendStderr(line string) {
	b.stderrMu.Lock()
	defer b.stderrMu.Unlock()
	if b.stderrTail != "" {
		b.stderrTail += "\n"
	}
	b.stderrTail += line
	if len(b.stderrTail) > 4096 {
		b.stderrTail = b.stderrTail[len(b.stderrTail)-4096:]
	}
}

func (b *claudeProc) stderrTailText() string {
	b.stderrMu.Lock()
	defer b.stderrMu.Unlock()
	return b.stderrTail
}

// wait reaps the child and publishes the exit.
func (b *claudeProc) wait() {
	err := b.cmd.Wait()
	b.exitMu.Lock()
	b.exitErr = err
	b.exitMu.Unlock()
	close(b.exitDone)

	if b.onExit != nil {
		b.onExit(b)
	}
}

// exitError describes why the child is gone. Pending calls are failed with it,
// because the bridge aborts in-flight chats silently on stdin EOF/SIGINT/
// SIGTERM: Go must never wait for an answer that will not come.
func (b *claudeProc) exitError() error {
	b.exitMu.Lock()
	err := b.exitErr
	b.exitMu.Unlock()

	message := "the Claude Code bridge exited unexpectedly"
	if err != nil {
		message = fmt.Sprintf("the Claude Code bridge exited: %v", err)
	}
	if tail := b.stderrTailText(); tail != "" {
		message += ": " + truncateForLog(tail, 1024)
	}
	return errors.New(message)
}

// shutdown closes stdin (the protocol's shutdown signal), then kills the child
// if it has not exited within the grace period.
func (b *claudeProc) shutdown() error {
	if b.stdin != nil {
		_ = b.stdin.Close()
	}

	select {
	case <-b.exitDone:
		return nil
	case <-time.After(claudeBridgeCloseTimeout):
	}

	if b.cmd.Process != nil {
		if err := b.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			logger.DebugC("claudecode", "killing the Claude Code bridge failed: "+err.Error())
		}
	}

	select {
	case <-b.exitDone:
	case <-time.After(claudeBridgeCloseTimeout):
		logger.WarnC("claudecode", "the Claude Code bridge did not exit after being killed")
	}
	return nil
}

func closeAll(closers ...io.Closer) {
	for _, closer := range closers {
		if closer != nil {
			_ = closer.Close()
		}
	}
}

// truncateForLog bounds a diagnostic string so one huge payload cannot flood
// the log.
func truncateForLog(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
} // --- embedded assets -------------------------------------------------------

// claudeBridgeAssetFiles are extracted in this order; the directory name covers
// both so a change to either invalidates the cache.
var claudeBridgeAssetFiles = []string{"bridge.mjs", "bridge_lib.mjs"}

var (
	claudeBridgeDirOnce sync.Once
	claudeBridgeDirPath string
	errClaudeBridgeDir  error
)

// claudeBridgeDir extracts the embedded bridge once per process and returns the
// directory holding bridge.mjs and bridge_lib.mjs side by side.
//
// The embedded assets are compile-time constants, so caching the result in a
// package-level sync.Once is safe and keeps two providers (or a restart) from
// rewriting the same files.
func claudeBridgeDir() (string, error) {
	claudeBridgeDirOnce.Do(func() {
		claudeBridgeDirPath, errClaudeBridgeDir = extractClaudeBridge()
	})
	return claudeBridgeDirPath, errClaudeBridgeDir
}

// extractClaudeBridge writes the bridge into
// <os.TempDir()>/lele-claude-bridge-<sha256[:12]>/ (0700, files 0600).
//
// Content-addressing makes concurrent lele processes converge on the same
// directory, and comparing before writing means an already-correct checkout is
// left untouched.
func extractClaudeBridge() (string, error) {
	contents := make([][]byte, len(claudeBridgeAssetFiles))
	digest := sha256.New()
	for i, name := range claudeBridgeAssetFiles {
		data, err := claudeBridgeAssets.ReadFile("claudecode/" + name)
		if err != nil {
			return "", fmt.Errorf("reading the embedded bridge asset %q: %w", name, err)
		}
		contents[i] = data
		digest.Write([]byte(name))
		digest.Write([]byte{0})
		digest.Write(data)
		digest.Write([]byte{0})
	}

	dir := filepath.Join(os.TempDir(), claudeBridgeDirPrefix+hex.EncodeToString(digest.Sum(nil))[:12])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating the Claude Code bridge directory %q: %w", dir, err)
	}

	for i, name := range claudeBridgeAssetFiles {
		path := filepath.Join(dir, name)
		if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, contents[i]) {
			continue
		}
		if err := os.WriteFile(path, contents[i], 0o600); err != nil {
			return "", fmt.Errorf("writing the Claude Code bridge asset %q: %w", path, err)
		}
	}

	return dir, nil
}

// --- environment -----------------------------------------------------------

// claudeBridgeNodeBin resolves the node binary: LELE_NODE_BIN, else "node".
func claudeBridgeNodeBin() string {
	if configured := strings.TrimSpace(os.Getenv(claudeBridgeEnvNodeBin)); configured != "" {
		return configured
	}
	return "node"
}

// claudeCodeExecutablePath resolves the claude CLI: CLAUDE_CODE_PATH, else the
// PATH lookup, else "" (the SDK falls back to its own resolution).
func claudeCodeExecutablePath() string {
	if configured := strings.TrimSpace(os.Getenv(claudeBridgeEnvClaudePath)); configured != "" {
		return configured
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		return ""
	}
	return path
}

// sdkSearchPaths lists the candidate node_modules directories handed to the
// bridge. NODE_PATH alone cannot satisfy an ESM `import`, so these paths are
// the mechanism; Go still exports NODE_PATH as a courtesy (see
// claudeCodeChildEnv).
func (p *ClaudeCodeProvider) sdkSearchPaths() []string {
	if p.sdkPathOverride != nil {
		return p.sdkPathOverride
	}

	p.sdkPathsOnce.Do(func() {
		p.sdkPaths = discoverClaudeSDKSearchPaths()
	})
	return p.sdkPaths
}

// discoverClaudeSDKSearchPaths builds the candidate list, most specific first.
func discoverClaudeSDKSearchPaths() []string {
	paths := make([]string, 0, 5)
	if configured := strings.TrimSpace(os.Getenv(claudeBridgeEnvSDKPath)); configured != "" {
		paths = append(paths, configured)
	}
	if root := npmGlobalRoot(); root != "" {
		paths = append(paths, root)
	}
	paths = append(paths, "/opt/homebrew/lib/node_modules", "/usr/local/lib/node_modules")
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".npm-global", "lib", "node_modules"))
	}

	return dedupeStrings(paths)
}

// npmGlobalRoot asks npm for its global node_modules directory. Best effort: a
// missing npm, a timeout or a non-zero exit all just mean "no candidate".
func npmGlobalRoot() string {
	ctx, cancel := context.WithTimeout(context.Background(), claudeBridgeNpmRootTimeout)
	defer cancel()

	output, err := exec.CommandContext(ctx, "npm", "root", "-g").Output()
	if err != nil {
		logger.DebugC("claudecode", "`npm root -g` failed, ignoring: "+err.Error())
		return ""
	}
	// Keep the last non-empty line: npm may print notices before the path.
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// claudeCodeChildEnv builds the child environment: the parent's, minus the API
// key / proxy / provider switches that would defeat subscription auth, plus a
// NODE_PATH aggregated from the search paths.
func claudeCodeChildEnv(searchPaths []string) []string {
	stripped := make(map[string]struct{}, len(claudeBridgeStrippedEnvVars))
	for _, name := range claudeBridgeStrippedEnvVars {
		stripped[name] = struct{}{}
	}

	inherited := os.Environ()
	env := make([]string, 0, len(inherited)+1)
	inheritedNodePath := ""
	for _, entry := range inherited {
		name, value := entry, ""
		if index := strings.IndexByte(entry, '='); index >= 0 {
			name, value = entry[:index], entry[index+1:]
		}
		// Compare upper-cased: an inherited `anthropic_api_key` (possible on
		// case-insensitive environments and in hand-written shell profiles) must
		// not sneak past the strip list.
		if _, drop := stripped[strings.ToUpper(name)]; drop {
			continue
		}
		if strings.ToUpper(name) == claudeBridgeEnvNodePath {
			inheritedNodePath = value
			continue
		}
		env = append(env, entry)
	}

	nodePath := strings.Join(searchPaths, string(os.PathListSeparator))
	switch {
	case inheritedNodePath != "" && nodePath != "":
		nodePath = inheritedNodePath + string(os.PathListSeparator) + nodePath
	case inheritedNodePath != "":
		nodePath = inheritedNodePath
	}
	if nodePath != "" {
		env = append(env, claudeBridgeEnvNodePath+"="+nodePath)
	}

	return env
}

// dedupeStrings drops empty entries and repeats, preserving order: the order is
// the resolution priority the bridge walks.
func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
