// Lele - Ultra-lightweight personal AI agent
// Regression tests for issue #231: (*toolExecutor).Execute must enrich the
// execution context with the acting agent's identity exactly ONCE, before the
// approval branch, so the pre-approval probe, WaitForResponse and the
// post-approval retry all share that single enriched context.
//
// Pre-fix (commit 363d8d1), executeWithApproval read the raw opts.ctx, so
// tools.AgentToolContextFromCtx returned empty inside the exec tool,
// substituteSecrets fell back to agentID="unknown", and every keyring secret
// with a Scope failed with ErrAccessDenied in gateway mode (where
// approvalManager is always non-nil).
//
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
)

// ============================================================================
// Test doubles and helpers
// ============================================================================

// capturedCall records what a fake tool observed on one invocation.
type capturedCall struct {
	agentID    string
	sessionKey string
}

// ctxCapturingTool is a minimal tools.Tool that records the agent identity
// returned by tools.AgentToolContextFromCtx on every call and then resolves a
// scoped keyring secret exactly the way ExecTool.substituteSecrets does
// (including the agentID=="unknown" fallback). The invariant is therefore
// observable twice: via the recorded context AND via the secret outcome.
//
// When approvalFlow is true the first call returns ApprovalRequired (like the
// ExecTool guard in approval mode) so the executor enters the WaitForResponse
// branch; subsequent calls return a plain success result.
type ctxCapturingTool struct {
	name         string
	svc          *keyring.Service
	approvalFlow bool

	mu    sync.Mutex
	calls []capturedCall
}

func (c *ctxCapturingTool) Name() string        { return c.name }
func (c *ctxCapturingTool) Description() string { return "ctx-capturing test tool" }
func (c *ctxCapturingTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}

func (c *ctxCapturingTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	agentID, sessionKey := tools.AgentToolContextFromCtx(ctx)

	c.mu.Lock()
	c.calls = append(c.calls, capturedCall{agentID: agentID, sessionKey: sessionKey})
	n := len(c.calls)
	c.mu.Unlock()

	if c.approvalFlow && n == 1 {
		return &tools.ToolResult{
			ForLLM: "requires approval",
			ApprovalRequired: &tools.ApprovalInfo{
				Command: "test-command",
				Reason:  "test: fake guard trip",
			},
		}
	}

	return &tools.ToolResult{ForLLM: c.resolveScopedSecret(ctx)}
}

// resolveScopedSecret mirrors ExecTool.substituteSecrets: read the identity
// from the context, fall back to "unknown" when empty, then GetForAgent. It
// returns the secret value on success or "ERR:<reason>" on failure.
func (c *ctxCapturingTool) resolveScopedSecret(ctx context.Context) string {
	agentID, sessionKey := tools.AgentToolContextFromCtx(ctx)
	if agentID == "" {
		agentID = "unknown"
	}
	value, err := c.svc.GetForAgent("test.token", agentID, sessionKey)
	if err != nil {
		return "ERR:" + err.Error()
	}
	return value
}

func (c *ctxCapturingTool) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *ctxCapturingTool) call(i int) capturedCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[i]
}

// newSecretKeyring builds a file-backed keyring service (never touches the OS
// keychain — same pattern as newTestService in pkg/keyring and newTestKeyring
// in pkg/tools) holding one secret scoped to allowedAgent.
func newSecretKeyring(t *testing.T, allowedAgent string) *keyring.Service {
	t.Helper()
	dir := t.TempDir()
	svc := keyring.NewService(keyring.ServiceConfig{
		Enabled:      true,
		Backend:      keyring.BackendFile,
		VaultPath:    dir + "/keyring.enc",
		AuditLogSize: 100,
		LeleDir:      dir,
	})
	if err := svc.SetFromUI("test.token", "s3cr3t-value", "test", nil, []string{allowedAgent}, "test"); err != nil {
		t.Fatalf("SetFromUI failed: %v", err)
	}
	return svc
}

// capturingHarness bundles the executor, the fake tool, the agent and the
// session key used in a single Execute call.
type capturingHarness struct {
	te         *toolExecutor
	al         *AgentLoop
	fake       *ctxCapturingTool
	agent      *AgentInstance
	sessionKey string
}

// newCapturingHarness wires a toolExecutor whose approvalManager is non-nil
// (gateway-like) with a ctx-capturing fake registered under toolName.
func newCapturingHarness(t *testing.T, agentID, toolName string, svc *keyring.Service, approvalFlow bool) *capturingHarness {
	t.Helper()

	sessionKey := "native:test-exec-secret-ctx"

	fake := &ctxCapturingTool{name: toolName, svc: svc, approvalFlow: approvalFlow}
	registry := tools.NewToolRegistry()
	registry.Register(fake)

	al := &AgentLoop{
		bus:             bus.NewMessageBus(),
		verboseManager:  session.NewVerboseManager(),
		approvalManager: channels.NewApprovalManager(),
	}

	agent := &AgentInstance{
		ID:       agentID,
		Sessions: session.NewSessionManager(),
		Tools:    registry,
	}
	return &capturingHarness{
		te:         newToolExecutor(al),
		al:         al,
		fake:       fake,
		agent:      agent,
		sessionKey: sessionKey,
	}
}

// opts builds toolExecOptions for the harness with the given tool call.
func (h *capturingHarness) opts(tcName string, args map[string]interface{}) toolExecOptions {
	return toolExecOptions{
		ctx:        context.Background(),
		agent:      h.agent,
		sessionKey: h.sessionKey,
		channel:    channels.ChannelName,
		chatID:     "42",
		tc:         providers.ToolCall{ID: "call-" + tcName, Name: tcName, Arguments: args},
	}
}

// approveFromBus drains the outbound bus and approves the first
// "approval.request" event (native channel), returning a stop function.
func approveFromBus(t *testing.T, al *AgentLoop) func() {
	t.Helper()
	drainCtx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			msg, ok := al.bus.SubscribeOutbound(drainCtx)
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
			// Small delay so WaitForResponse is exercised too.
			time.Sleep(20 * time.Millisecond)
			if _, err := al.approvalManager.HandleApproval(id, true); err != nil {
				t.Errorf("HandleApproval(%q) failed: %v", id, err)
			}
			return
		}
	}()
	return cancel
}

// ============================================================================
// Test A — pre-approval probe branch (executeWithApproval, first call)
// ============================================================================

// TestToolExecutor_ApprovalProbeSeesAgentContext is the primary regression
// guard for issue #231. With approvalManager != nil and tc.Name == "exec",
// the executor must hand the enriched context to the pre-approval probe.
// Pre-fix, the probe received the raw opts.ctx: the fake recorded an empty
// agentID and the scoped secret resolution fell back to "unknown" and failed.
func TestToolExecutor_ApprovalProbeSeesAgentContext(t *testing.T) {
	const agentID = "agent-scoped-42"
	svc := newSecretKeyring(t, agentID)
	h := newCapturingHarness(t, agentID, "exec", svc, false)

	result, err := h.te.Execute(h.opts("exec", nil))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned nil result")
	}

	if h.fake.callCount() != 1 {
		t.Fatalf("expected exactly 1 tool invocation (no ApprovalRequired returned), got %d", h.fake.callCount())
	}
	call := h.fake.call(0)
	if call.agentID != agentID {
		t.Errorf("approval-probe branch saw agentID=%q, want %q (issue #231: raw opts.ctx leaked into executeWithApproval)", call.agentID, agentID)
	}
	if call.sessionKey != h.sessionKey {
		t.Errorf("approval-probe branch saw sessionKey=%q, want %q", call.sessionKey, h.sessionKey)
	}

	// End-to-end proof: the scoped secret must resolve via the real agent ID.
	// Pre-fix this was `ERR:...access denied...agent "unknown" is not allowed`.
	if result.ForLLM != "s3cr3t-value" {
		t.Errorf("tool result = %q, want %q (scoped secret must resolve with the acting agent's identity)", result.ForLLM, "s3cr3t-value")
	}
}

// ============================================================================
// Test B — cancellation must still propagate through the approval wait
// ============================================================================

// TestToolExecutor_ApprovalWaitHonorsContextCancel guards the critical
// property the fix must not break (a.k.a. /stop): the context handed to
// approval.WaitForResponse is still derived from the caller's context, so
// cancelling it aborts the wait instead of blocking for the full approval
// timeout (5 minutes by default).
//
// NOTE for reviewers: this is a non-regression guard for /stop, NOT a #231
// regression test — it also passes on baseline 363d8d1 (the pre-fix code
// already derived the wait context from the caller's). It exists so the
// single-context enrichment cannot silently break cancellation.
func TestToolExecutor_ApprovalWaitHonorsContextCancel(t *testing.T) {
	const agentID = "agent-cancel"
	svc := newSecretKeyring(t, agentID)
	h := newCapturingHarness(t, agentID, "exec", svc, true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := h.opts("exec", nil)
	opts.ctx = ctx

	// Cancel shortly after the approval request is published (no answer given).
	watch := make(chan struct{})
	go func() {
		defer close(watch)
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer drainCancel()
		for {
			msg, ok := h.al.bus.SubscribeOutbound(drainCtx)
			if !ok {
				return
			}
			if msg.Event == "approval.request" {
				time.Sleep(20 * time.Millisecond)
				cancel()
				return
			}
		}
	}()

	start := time.Now()
	result, err := h.te.Execute(opts)
	elapsed := time.Since(start)
	<-watch

	if elapsed > 10*time.Second {
		t.Fatalf("cancellation did not abort the approval wait (took %v)", elapsed)
	}
	if err == nil {
		t.Fatalf("expected Execute to surface the cancellation error, got result=%+v", result)
	}
	if result != nil {
		t.Errorf("expected nil result on cancellation, got %+v", result)
	}
}

// ============================================================================
// Test C — real ExecTool end-to-end through the approval flow
// ============================================================================

// TestToolExecutor_ApprovedExecToolResolvesScopedSecret runs the exact
// production flow: a real *tools.ExecTool in approval mode probes a
// guard-blockable command carrying an inline {{SECRET:...}} placeholder; a
// goroutine approves it; the post-approval retry (et.Execute(opts.ctx, ...),
// with the guard bypassed) must resolve the scoped secret with the acting
// agent's identity.
//
// Pre-fix the retry received the raw opts.ctx, substituteSecrets fell back to
// agentID="unknown", and the command aborted with ErrAccessDenied
// (IsError=true). Post-fix the command runs, echoes the secret value, and the
// keyring audit records the real agent ID and session key.
func TestToolExecutor_ApprovedExecToolResolvesScopedSecret(t *testing.T) {
	const agentID = "agent-exec-real"
	const sessionKey = "native:test-exec-real"
	svc := newSecretKeyring(t, agentID)

	// Run inside a temp dir and delete a scratch file there: "rm -rf <path>"
	// trips the deny guard (blockable → ApprovalRequired) while staying safe.
	workDir := t.TempDir()
	scratch := workDir + "/to-delete"
	if err := os.WriteFile(scratch, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	et := tools.NewExecTool(workDir, false)
	et.SetKeyringService(svc)
	et.SetSecretSubstitution(true)
	et.SetApprovalMode(true)
	et.SetTimeout(10 * time.Second)

	registry := tools.NewToolRegistry()
	registry.Register(et)

	al := &AgentLoop{
		bus:             bus.NewMessageBus(),
		verboseManager:  session.NewVerboseManager(),
		approvalManager: channels.NewApprovalManager(),
	}
	te := newToolExecutor(al)
	agent := &AgentInstance{ID: agentID, Sessions: session.NewSessionManager(), Tools: registry}

	// Placeholder name built by concatenation so log scrubbing never sees the
	// literal marker (same convention as the exec tool description).
	command := "echo {{SECRET:" + "test.token}}; rm -rf " + scratch

	stop := approveFromBus(t, al)
	defer stop()

	result, err := te.Execute(toolExecOptions{
		ctx:        context.Background(),
		agent:      agent,
		sessionKey: sessionKey,
		channel:    channels.ChannelName,
		chatID:     "42",
		tc: providers.ToolCall{
			ID:        "call-real",
			Name:      "exec",
			Arguments: map[string]interface{}{"command": command},
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned nil result")
	}
	if result.IsError {
		t.Fatalf("approved exec must succeed post-fix; got error result: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "s3cr3t-value") {
		t.Errorf("expected substituted secret value in output, got: %q", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "{{SECRET:") {
		t.Errorf("placeholder should have been substituted, got: %q", result.ForLLM)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Errorf("approved command should have run for real; scratch file still exists")
	}

	// The audit trail must show the real agent, not "unknown".
	var last *keyring.AccessRecord
	log := svc.AuditLog()
	for i := range log {
		if log[i].Action == "get" && log[i].SecretName == "test.token" {
			rec := log[i]
			last = &rec
		}
	}
	if last == nil {
		t.Fatal("expected a keyring 'get' audit record for test.token")
	}
	if last.AgentID != agentID {
		t.Errorf("audit record agent_id=%q, want %q", last.AgentID, agentID)
	}
	if last.SessionKey != sessionKey {
		t.Errorf("audit record session_key=%q, want %q", last.SessionKey, sessionKey)
	}
	if !last.Granted {
		t.Errorf("audit record granted=false, want true (scoped secret belongs to %q)", agentID)
	}
}

// ============================================================================
// Test D — structural invariant: ONE enriched context for every branch
// ============================================================================

// TestToolExecutor_SingleEnrichedContextAcrossBranches asserts the structural
// property behind the fix: WithAgentToolContext is applied once, BEFORE the
// approval branch forks, so a non-exec tool (else branch) and an exec tool
// (approval branch) observe the identical agent identity. If someone
// reintroduced a second, partially-wired context (execCtx) or dropped the
// enrichment from one branch, the two observations would diverge.
func TestToolExecutor_SingleEnrichedContextAcrossBranches(t *testing.T) {
	const agentID = "agent-invariant"
	svc := newSecretKeyring(t, agentID)

	// Branch 1: arbitrary non-exec tool name → plain ExecuteWithContext path.
	h1 := newCapturingHarness(t, agentID, "read_file", svc, false)
	res1, err := h1.te.Execute(h1.opts("read_file", nil))
	if err != nil {
		t.Fatalf("non-exec Execute returned error: %v", err)
	}

	// Branch 2: exec tool with non-nil approvalManager → executeWithApproval path.
	h2 := newCapturingHarness(t, agentID, "exec", svc, false)
	res2, err := h2.te.Execute(h2.opts("exec", nil))
	if err != nil {
		t.Fatalf("exec Execute returned error: %v", err)
	}

	if h1.fake.callCount() != 1 || h2.fake.callCount() != 1 {
		t.Fatalf("expected 1 invocation per branch, got %d and %d", h1.fake.callCount(), h2.fake.callCount())
	}

	c1 := h1.fake.call(0)
	c2 := h2.fake.call(0)
	if c1 != c2 {
		t.Errorf("branches diverged: non-exec saw %+v but exec saw %+v — Execute must build exactly ONE enriched context (issue #231)", c1, c2)
	}
	if c1.agentID != agentID || c1.sessionKey != h1.sessionKey {
		t.Errorf("both branches saw %+v, want agentID=%q sessionKey=%q", c1, agentID, h1.sessionKey)
	}

	// Both branches must also resolve the scoped secret successfully.
	if res1.ForLLM != "s3cr3t-value" {
		t.Errorf("non-exec branch result = %q, want scoped secret", res1.ForLLM)
	}
	if res2.ForLLM != "s3cr3t-value" {
		t.Errorf("exec/approval branch result = %q, want scoped secret", res2.ForLLM)
	}
}

// ============================================================================
// Test E — nil agent fails as an error, never as a panic
// ============================================================================

// TestToolExecutor_NilAgentReturnsError pins the defensive early return at
// the top of Execute: with opts.agent == nil the call must return
// (nil, error) instead of panicking. Pre-guard, publishExecuting
// dereferenced opts.agent.ID — and both execution branches dereference
// opts.agent.Tools — so a nil agent was an immediate nil-pointer panic
// regardless of the tool name or the approval branch taken.
//
// The (nil, error) contract is what the call sites handle: llm_runner.go
// records the error and still appends a placeholder tool result, and
// group_turn.go wraps it into the turn error — neither retries.
func TestToolExecutor_NilAgentReturnsError(t *testing.T) {
	te := newToolExecutor(&AgentLoop{
		bus:            bus.NewMessageBus(),
		verboseManager: session.NewVerboseManager(),
	})

	result, err := te.Execute(toolExecOptions{
		ctx:        context.Background(),
		agent:      nil,
		sessionKey: "native:test-nil-agent",
		channel:    channels.ChannelName,
		chatID:     "42",
		tc:         providers.ToolCall{ID: "call-nil", Name: "exec"},
	})
	if err == nil {
		t.Fatalf("expected an error for nil agent, got result=%+v err=nil", result)
	}
	if result != nil {
		t.Errorf("expected nil result alongside the error, got %+v", result)
	}
	if !strings.Contains(err.Error(), "exec") {
		t.Errorf("error %q should name the offending tool", err)
	}
	if !strings.Contains(err.Error(), "agent") {
		t.Errorf("error %q should mention the missing agent context", err)
	}
}
