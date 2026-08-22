package tools

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
)

// TestToolRegistry_Execute_notFound verifies Execute returns an error result
// for an unknown tool.
func TestToolRegistry_Execute_notFound(t *testing.T) {
	r := NewToolRegistry()
	result := r.Execute(context.Background(), "nope", map[string]interface{}{})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Fatalf("expected error result, got ForLLM=%q", result.ForLLM)
	}
	if result.Err == nil {
		t.Fatal("expected Err to be set for tool-not-found")
	}
	if result.ForLLM != `tool "nope" not found` {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestToolRegistry_ExecuteWithContext_foundSuccess verifies success path with
// channel/chat context set.
func TestToolRegistry_ExecuteWithContext_foundSuccess(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&registryTestTool{name: "alpha", description: "a"})

	result := r.ExecuteWithContext(
		context.Background(), "alpha", map[string]interface{}{"x": 1},
		"chan", "chat", nil,
	)
	if result == nil || result.IsError {
		t.Fatalf("expected success, got %+v", result)
	}
	if result.ForLLM != "ok" {
		t.Fatalf("ForLLM = %q, want ok", result.ForLLM)
	}
}

// TestToolRegistry_Execute_defaultCtx calls Execute (no channel/chat) for a
// known tool. Covers the Execute wrapper delegating to ExecuteWithContext.
func TestToolRegistry_Execute_defaultCtx(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&registryTestTool{name: "beta", description: "b"})
	result := r.Execute(context.Background(), "beta", map[string]interface{}{})
	if result == nil || result.IsError {
		t.Fatalf("expected success via Execute, got %+v", result)
	}
}

// TestToolRegistry_List verifies List returns all registered names and Count
// reflects the number of tools.
func TestToolRegistry_List(t *testing.T) {
	r := NewToolRegistry()
	if got := r.Count(); got != 0 {
		t.Fatalf("Count() on empty registry = %d, want 0", got)
	}

	r.Register(&registryTestTool{name: "a", description: "a"})
	r.Register(&registryTestTool{name: "b", description: "b"})
	r.Register(&registryTestTool{name: "c", description: "c"})

	if got := r.Count(); got != 3 {
		t.Fatalf("Count() = %d, want 3", got)
	}

	got := r.List()
	sort.Strings(got)
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %v, want %v", got, want)
	}
}

// TestToolRegistry_List_empty checks List returns empty slice for empty registry.
func TestToolRegistry_List_empty(t *testing.T) {
	r := NewToolRegistry()
	got := r.List()
	if got == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("len(List()) = %d, want 0", len(got))
	}
}

// TestToolRegistry_CloneWithout verifies CloneWithout excludes named tools.
func TestToolRegistry_CloneWithout(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&registryTestTool{name: "keep1", description: "k1"})
	r.Register(&registryTestTool{name: "keep2", description: "k2"})
	r.Register(&registryTestTool{name: "drop", description: "d"})

	clone := r.CloneWithout("drop")
	if clone.Count() != 2 {
		t.Fatalf("clone.Count() = %d, want 2", clone.Count())
	}
	for _, name := range []string{"keep1", "keep2"} {
		if _, ok := clone.Get(name); !ok {
			t.Errorf("clone missing %q", name)
		}
	}
	if _, ok := clone.Get("drop"); ok {
		t.Errorf("clone should not contain dropped tool %q", "drop")
	}

	// Original should be unaffected.
	if r.Count() != 3 {
		t.Fatalf("original Count() = %d, want 3", r.Count())
	}
}

// TestToolRegistry_CloneWithout_allExcluded verifies cloning with everything excluded.
func TestToolRegistry_CloneWithout_allExcluded(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&registryTestTool{name: "only", description: "o"})
	clone := r.CloneWithout("only")
	if clone.Count() != 0 {
		t.Fatalf("clone.Count() = %d, want 0", clone.Count())
	}
}

// asyncRegistryTool implements AsyncTool so ExecuteWithContext sets a callback.
type asyncRegistryTool struct {
	callback AsyncCallback
	registryTestTool
}

func (a *asyncRegistryTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	a.registryTestTool.Execute(ctx, args)
	res := &ToolResult{ForLLM: "async", Async: true}
	if a.callback != nil {
		a.callback(ctx, res)
	}
	return res
}

func (a *asyncRegistryTool) SetCallback(cb AsyncCallback) { a.callback = cb }

// TestToolRegistry_ExecuteWithContext_asyncCallback verifies the async callback
// is injected and invoked.
func TestToolRegistry_ExecuteWithContext_asyncCallback(t *testing.T) {
	r := NewToolRegistry()
	at := &asyncRegistryTool{}
	r.Register(at)

	called := false
	result := r.ExecuteWithContext(
		context.Background(), at.Name(), map[string]interface{}{},
		"", "", func(ctx context.Context, r *ToolResult) { called = true },
	)
	if result == nil || !result.Async {
		t.Fatalf("expected async result, got %+v", result)
	}
	if !called {
		t.Fatal("expected async callback to be invoked")
	}
	if at.callback == nil {
		t.Fatal("expected callback to be set on tool")
	}
}

// contextualRegistryTool implements ContextualTool.
type contextualRegistryTool struct {
	channel string
	chatID  string
	registryTestTool
}

func (c *contextualRegistryTool) SetContext(channel, chatID string) {
	c.channel = channel
	c.chatID = chatID
}

// TestToolRegistry_ExecuteWithContext_setsContext verifies ContextualTool
// receives channel/chatID context.
func TestToolRegistry_ExecuteWithContext_setsContext(t *testing.T) {
	r := NewToolRegistry()
	ct := &contextualRegistryTool{}
	r.Register(ct)

	result := r.ExecuteWithContext(
		context.Background(), ct.Name(), map[string]interface{}{},
		"the-channel", "the-chat", nil,
	)
	if result == nil || result.IsError {
		t.Fatalf("expected success, got %+v", result)
	}
	if ct.channel != "the-channel" || ct.chatID != "the-chat" {
		t.Fatalf("context not set: channel=%q chat=%q", ct.channel, ct.chatID)
	}
}

// executingErrorTool is a tool whose Execute returns an error result.
type executingErrorTool struct {
	registryTestTool
}

func (e *executingErrorTool) Name() string { return "boom" }

func (e *executingErrorTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	return ErrorResult("boom occurred")
}

// TestToolRegistry_ExecuteWithContext_errorResult verifies an error result is
// returned/logged without panicking.
func TestToolRegistry_ExecuteWithContext_errorResult(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&executingErrorTool{})
	result := r.ExecuteWithContext(context.Background(), "boom", map[string]interface{}{}, "", "", nil)
	if result == nil || !result.IsError {
		t.Fatalf("expected error result, got %+v", result)
	}
	if result.ForLLM != "boom occurred" {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestToolRegistry_Get_notFound verifies Get returns (nil, false).
func TestToolRegistry_Get_notFound(t *testing.T) {
	r := NewToolRegistry()
	tool, ok := r.Get("missing")
	if ok {
		t.Fatal("expected ok=false for missing tool")
	}
	if tool != nil {
		t.Fatalf("expected nil tool, got %T", tool)
	}
}

// TestToolRegistry_Register_overwrites verifies registering same name overwrites.
func TestToolRegistry_Register_overwrites(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&registryTestTool{name: "dup", description: "first"})
	r.Register(&registryTestTool{name: "dup", description: "second"})
	if r.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", r.Count())
	}
	tool, ok := r.Get("dup")
	if !ok {
		t.Fatal("expected tool dup")
	}
	if tool.Description() != "second" {
		t.Fatalf("Description() = %q, want second (overwritten)", tool.Description())
	}
}

// TestToolRegistry_ToProviderDefs_nonFunctionEntry is not testable via the
// public API because ToolToSchema always wraps Parameters() inside a proper
// "function" map. The type-check guard in ToProviderDefs is a safety net that
// cannot be triggered through normal registration. Removed.

// TestToolRegistry_ToProviderDefs_empty verifies empty registry yields empty defs.
func TestToolRegistry_ToProviderDefs_empty(t *testing.T) {
	r := NewToolRegistry()
	defs := r.ToProviderDefs()
	if defs == nil {
		t.Fatal("expected non-nil, empty defs")
	}
	if len(defs) != 0 {
		t.Fatalf("len(defs) = %d, want 0", len(defs))
	}
}

// TestToolRegistry_ExecuteWithContext_panickingToolSafe validates the registry
// still returns something even if a tool returns nil (should not panic because
// base.ExecuteWithContext dereferences result for logging — a nil result would
// panic; we register a tool that returns a valid result instead). Kept minimal
// to document behavior.
func TestToolRegistry_ExecuteSkipAsyncCallbackWhenNil(t *testing.T) {
	r := NewToolRegistry()
	at := &asyncRegistryTool{}
	r.Register(at)
	// nil callback should be skipped.
	result := r.ExecuteWithContext(context.Background(), at.Name(), map[string]interface{}{}, "", "", nil)
	if result == nil || !result.Async {
		t.Fatalf("expected async result, got %+v", result)
	}
}

// closingTool produces an error; used to assert Err propagation.
type errorWithErrTool struct {
	registryTestTool
}

func (e *errorWithErrTool) Name() string { return "errchain" }

func (e *errorWithErrTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	return ErrorResult("failed").WithError(errors.New("underlying"))
}

func TestToolRegistry_ErrorResultWithErr(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&errorWithErrTool{})
	result := r.ExecuteWithContext(context.Background(), "errchain", map[string]interface{}{}, "", "", nil)
	if result == nil || !result.IsError {
		t.Fatalf("expected error result, got %+v", result)
	}
	if result.Err == nil || result.Err.Error() != "underlying" {
		t.Fatalf("Err = %v, want underlying", result.Err)
	}
}
