// Lele - Ultra-lightweight personal AI agent
// Regression tests for the SESSION scope of MCP tools: a load performed by one
// chat must never appear in the context of another chat served by the same
// agent.
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

// sessionScopeWiring builds an agent wired to a real mcp.Manager over the fake
// dialer (no process spawned) and returns the coordinator plus the agent, so a
// test can act as distinct chats of the SAME agent.
func sessionScopeWiring(t *testing.T) (*toolCoordinatorImpl, *AgentInstance) {
	t.Helper()
	_, tc, agent, _ := e2eMCPWiring(t)
	return tc, agent
}

// TestMCPLoad_IsSessionScoped is THE regression for "un MCP cargado por un
// agente aparece en el contexto de las demás sesiones": session A loads the
// demo server, and session B — same agent — must neither see the remote tools
// in its tool definitions nor be able to call them.
func TestMCPLoad_IsSessionScoped(t *testing.T) {
	tc, agent := sessionScopeWiring(t)
	ctx := context.Background()

	layerA := tc.sessionTools(agent, "chat-a")
	layerB := tc.sessionTools(agent, "chat-b")

	// Before any load, both sessions see the same (base) tool set.
	baseDefs := len(agent.Tools.ToProviderDefs())
	if got := len(layerA.ToProviderDefs()); got != baseDefs+1 {
		t.Fatalf("session A defs = %d, want %d (base + load_mcp_tools)", got, baseDefs+1)
	}

	// Session A loads the server.
	loader := mcpSessionLoader(t, tc, agent, "chat-a")
	if res := loader.Execute(ctx, map[string]interface{}{"server": "demo"}); res.IsError {
		t.Fatalf("load in session A failed: %s", res.ForLLM)
	}

	// A sees the remote tools…
	for _, name := range []string{"mcp_demo_echo", "mcp_demo_summary"} {
		if _, ok := layerA.Get(name); !ok {
			t.Errorf("session A must see %s after loading it", name)
		}
	}
	// …B does not, not even as a definition.
	namesB := make([]string, 0)
	for _, def := range layerB.ToProviderDefs() {
		if strings.HasPrefix(def.Function.Name, "mcp_") {
			namesB = append(namesB, def.Function.Name)
		}
	}
	if len(namesB) != 0 {
		t.Errorf("session B sees MCP tools it never loaded: %v", namesB)
	}
	if _, ok := layerB.Get("mcp_demo_echo"); ok {
		t.Error("session B can resolve a tool loaded by session A")
	}
	// The agent-wide registry latches nothing either.
	if _, ok := agent.Tools.Get("mcp_demo_echo"); ok {
		t.Error("the loaded tool leaked into the agent-wide registry")
	}

	// Executing through B's view fails: the model cannot call what it cannot see,
	// and any stray call must not reach the server.
	res := layerB.Execute(ctx, "mcp_demo_echo", map[string]interface{}{"message": "x"})
	if !res.IsError {
		t.Error("session B must not be able to execute a tool loaded by session A")
	}

	// A's tool is still callable from A.
	out := layerA.Execute(ctx, "mcp_demo_echo", map[string]interface{}{"message": "hola"})
	if out.IsError || out.ForLLM != "fake:echo" {
		t.Errorf("session A remote call = %+v, want fake:echo", out)
	}
}

// TestMCPLoad_SameSessionKeepsToolsAcrossTurns pins that the layer is stable
// per session: every turn of a chat resolves the SAME registry, so what it
// loaded stays available without loading it again.
func TestMCPLoad_SameSessionKeepsToolsAcrossTurns(t *testing.T) {
	tc, agent := sessionScopeWiring(t)
	ctx := context.Background()

	first := tc.sessionTools(agent, "chat-a")
	loader := mcpSessionLoader(t, tc, agent, "chat-a")
	if res := loader.Execute(ctx, map[string]interface{}{"server": "demo"}); res.IsError {
		t.Fatalf("load failed: %s", res.ForLLM)
	}

	// A later turn resolves the session again — same layer, tools still there.
	second := tc.sessionTools(agent, "chat-a")
	if second != first {
		t.Fatal("the same session must resolve the same tool layer on every turn")
	}
	if _, ok := second.Get("mcp_demo_echo"); !ok {
		t.Error("the tools loaded in a previous turn are gone")
	}
	// The loader is bound to that same layer: a second load reports everything
	// as already present (no duplicates, no new layer).
	res := mcpSessionLoader(t, tc, agent, "chat-a").Execute(ctx, map[string]interface{}{"server": "demo"})
	if res.IsError {
		t.Fatalf("reload failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "0 newly registered, 2 already present") {
		t.Errorf("reload output = %s, want 0 newly registered / 2 already present", res.ForLLM)
	}
}

// TestMCPLoad_DropSessionLeavesOtherSessionsAlone pins the cleanup path used by
// /clear and /new: dropping one session's layer must not disturb the others.
func TestMCPLoad_DropSessionLeavesOtherSessionsAlone(t *testing.T) {
	tc, agent := sessionScopeWiring(t)
	ctx := context.Background()

	for _, key := range []string{"chat-a", "chat-b"} {
		loader := mcpSessionLoader(t, tc, agent, key)
		if res := loader.Execute(ctx, map[string]interface{}{"server": "demo"}); res.IsError {
			t.Fatalf("load in %s failed: %s", key, res.ForLLM)
		}
	}

	tc.dropSessionTools("chat-a")

	if _, ok := tc.sessionTools(agent, "chat-a").Get("mcp_demo_echo"); ok {
		t.Error("session A kept its tools after dropSessionTools")
	}
	if _, ok := tc.sessionTools(agent, "chat-b").Get("mcp_demo_echo"); !ok {
		t.Error("dropping session A must not touch session B's layer")
	}
}

// TestMCPLoad_ToolExecutorUsesSessionRegistry pins the execution half of the
// wiring: toolExecutor must resolve (and run) tools through the session view it
// is handed, not through the shared agent registry.
func TestMCPLoad_ToolExecutorUsesSessionRegistry(t *testing.T) {
	_, tc, agent, _ := e2eMCPWiring(t)
	ctx := context.Background()

	layerA := tc.sessionTools(agent, "chat-a")
	loader := mcpSessionLoader(t, tc, agent, "chat-a")
	if res := loader.Execute(ctx, map[string]interface{}{"server": "demo"}); res.IsError {
		t.Fatalf("load failed: %s", res.ForLLM)
	}

	executor := newToolExecutor(tc.al)
	tc2 := providers.ToolCall{ID: "call-1", Name: "mcp_demo_echo", Arguments: map[string]interface{}{"message": "hola"}}

	// Session A: the call reaches the (fake) MCP server.
	res, err := executor.Execute(toolExecOptions{
		ctx:        ctx,
		agent:      agent,
		registry:   layerA,
		tc:         tc2,
		channel:    "test",
		chatID:     "chat-a",
		sessionKey: "chat-a",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("session A call failed: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "fake:echo") {
		t.Errorf("session A result = %q, want the fake server answer", res.ForLLM)
	}

	// Session B: the very same tool call finds no such tool.
	res, err = executor.Execute(toolExecOptions{
		ctx:        ctx,
		agent:      agent,
		registry:   tc.sessionTools(agent, "chat-b"),
		tc:         tc2,
		channel:    "test",
		chatID:     "chat-b",
		sessionKey: "chat-b",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("session B must not execute a tool it never loaded, got %+v", res)
	}
}

// TestMCPLoad_ManagerCloseDropsEverySessionLayer pins teardown: the layers hold
// tools bound to the manager, so closing it must leave no session offering
// them.
func TestMCPLoad_ManagerCloseDropsEverySessionLayer(t *testing.T) {
	tc, agent := sessionScopeWiring(t)
	ctx := context.Background()

	loader := mcpSessionLoader(t, tc, agent, "chat-a")
	if res := loader.Execute(ctx, map[string]interface{}{"server": "demo"}); res.IsError {
		t.Fatalf("load failed: %s", res.ForLLM)
	}
	if tc.mcpManagers.layers.len() == 0 {
		t.Fatal("expected at least one live session layer")
	}

	if err := tc.closeMCPManagers(); err != nil {
		t.Fatalf("closeMCPManagers(): %v", err)
	}

	if got := tc.mcpManagers.layers.len(); got != 0 {
		t.Errorf("%d session layers survived the manager teardown", got)
	}
	if got := tc.sessionTools(agent, "chat-a"); got != agent.Tools {
		t.Error("after teardown a session must fall back to the base registry")
	}
}

// TestSessionToolSet_LRURetiresTheOldestLayer pins the bound on the layer map: a
// long-lived process cannot accumulate one layer per session forever.
func TestSessionToolSet_LRURetiresTheOldestLayer(t *testing.T) {
	set := newSessionToolSet()
	build := func() *tools.ToolRegistry { return tools.NewScopedRegistry(tools.NewToolRegistry()) }

	for i := 0; i < maxSessionToolLayers; i++ {
		set.getOrCreate("agent", "sess-"+strconv.Itoa(i), build)
	}
	if got := set.len(); got != maxSessionToolLayers {
		t.Fatalf("len() = %d, want %d", got, maxSessionToolLayers)
	}

	// Touch the first session so it becomes the most recently used one, then
	// overflow the cap: a middle/last session must be the one dropped.
	set.getOrCreate("agent", "sess-0", build)
	newest := set.getOrCreate("agent", "sess-new", build)
	if newest == nil {
		t.Fatal("getOrCreate returned no registry")
	}

	if got := set.len(); got != maxSessionToolLayers {
		t.Errorf("len() = %d after overflow, want the %d cap", got, maxSessionToolLayers)
	}
	set.mu.Lock()
	_, keptFresh := set.entries[sessionToolKey{agentID: "agent", sessionKey: "sess-0"}]
	_, keptNew := set.entries[sessionToolKey{agentID: "agent", sessionKey: "sess-new"}]
	set.mu.Unlock()
	if !keptFresh {
		t.Error("the recently used layer was retired instead of an older one")
	}
	if !keptNew {
		t.Error("the new layer was not stored")
	}
}

// TestSessionToolSet_DropAgentIsScopedToThatAgent pins that retiring one agent's
// manager leaves another agent's layers alone.
func TestSessionToolSet_DropAgentIsScopedToThatAgent(t *testing.T) {
	set := newSessionToolSet()
	build := func() *tools.ToolRegistry { return tools.NewScopedRegistry(tools.NewToolRegistry()) }

	set.getOrCreate("main", "chat-a", build)
	set.getOrCreate("worker", "chat-a", build)

	set.dropAgent("main")

	if set.len() != 1 {
		t.Fatalf("len() = %d after dropAgent, want 1 (the other agent's layer)", set.len())
	}
	set.mu.Lock()
	_, kept := set.entries[sessionToolKey{agentID: "worker", sessionKey: "chat-a"}]
	set.mu.Unlock()
	if !kept {
		t.Error("dropAgent removed another agent's layer")
	}
}

// TestMCPLoad_TurnDefinitionsAreSessionScoped is the end-to-end wiring check:
// it runs REAL turns (the production llmRunner) for two sessions of the same
// agent and inspects the tool definitions handed to the provider. Only the
// session that called load_mcp_tools may see the remote tools.
func TestMCPLoad_TurnDefinitionsAreSessionScoped(t *testing.T) {
	_, tc, agent, _ := e2eMCPWiring(t)

	// Record the exact tool definitions of every provider call.
	var seen [][]string
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(_ context.Context, _ []providers.Message, defs []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
			names := make([]string, 0, len(defs))
			for _, def := range defs {
				names = append(names, def.Function.Name)
			}
			seen = append(seen, names)
			return &providers.LLMResponse{Content: "ok", ToolCalls: []providers.ToolCall{}}, nil
		},
	}

	runner := newLLMRunner(tc.al)
	turn := func(sessionKey string) []string {
		t.Helper()
		before := len(seen)
		if _, err := runner.runAgentLoop(context.Background(), agent, processOptions{
			SessionKey:      sessionKey,
			Channel:         "test",
			ChatID:          sessionKey,
			UserMessage:     "hello",
			DefaultResponse: "ok",
			SendResponse:    false,
		}); err != nil {
			t.Fatalf("runAgentLoop(%s): %v", sessionKey, err)
		}
		if len(seen) != before+1 {
			t.Fatalf("expected exactly one provider call for %s, got %d", sessionKey, len(seen)-before)
		}
		return seen[len(seen)-1]
	}

	hasMCP := func(names []string) []string {
		out := make([]string, 0)
		for _, name := range names {
			if strings.HasPrefix(name, "mcp_") {
				out = append(out, name)
			}
		}
		return out
	}

	// Turn 1 — session A, nothing loaded yet.
	first := turn("chat-a")
	if got := hasMCP(first); len(got) != 0 {
		t.Fatalf("session A already sees MCP tools before loading: %v", got)
	}

	// Session A loads the server.
	loader := mcpSessionLoader(t, tc, agent, "chat-a")
	if res := loader.Execute(context.Background(), map[string]interface{}{"server": "demo"}); res.IsError {
		t.Fatalf("load failed: %s", res.ForLLM)
	}

	// Turn 2 — a DIFFERENT session must not see what A loaded.
	if got := hasMCP(turn("chat-b")); len(got) != 0 {
		t.Errorf("session B's tool definitions include MCP tools it never loaded: %v", got)
	}

	// Turn 3 — session A still sees them (the layer survives the turn).
	names := hasMCP(turn("chat-a"))
	if len(names) != 2 {
		t.Fatalf("session A sees %v, want the 2 loaded MCP tools", names)
	}
	want := map[string]bool{"mcp_demo_echo": true, "mcp_demo_summary": true}
	for _, name := range names {
		if !want[name] {
			t.Errorf("unexpected MCP tool in session A's definitions: %s", name)
		}
	}
}
