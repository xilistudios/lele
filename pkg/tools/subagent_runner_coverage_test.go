package tools

import (
	"sync"
	"testing"
)

// TestExtractProgress covers the PROGRESS line extraction helper.
func TestExtractProgress(t *testing.T) {
	if got := extractProgress("PROGRESS: 3 of 5 files analyzed\nSTATUS: running"); got != "3 of 5 files analyzed" {
		t.Fatalf("got %q", got)
	}
	if got := extractProgress("  progress: indented\n"); got != "indented" {
		t.Fatalf("got %q", got)
	}
	if got := extractProgress("no progress line here"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := extractProgress(""); got != "" {
		t.Fatalf("got %q", got)
	}
	// Multi-line with the progress further down.
	if got := extractProgress("STATUS: running\nPROGRESS: half way\nSTATUS: completed"); got != "half way" {
		t.Fatalf("got %q", got)
	}
}

// TestIsTransientFailure covers the transient-failure detection helper.
func TestIsTransientFailure(t *testing.T) {
	transient := []string{
		"rate_limited: too many requests",
		"connection_error: refused",
		"http_timeout: deadline",
		"server_error: 500",
	}
	for _, msg := range transient {
		if !isTransientFailure(msg) {
			t.Errorf("expected %q transient", msg)
		}
	}
	if isTransientFailure("permanent failure") {
		t.Error("expected not transient")
	}
	if isTransientFailure("") {
		t.Error("expected not transient for empty")
	}
	// Case-insensitivity.
	if !isTransientFailure("SERVER_ERROR") {
		t.Error("expected case-insensitive transient")
	}
}

// TestResolveAgentConfig_NoContext covers defaults without agent context.
func TestResolveAgentConfig_NoContext(t *testing.T) {
	sm := NewSubagentManager(nil, "default-model", "/w", nil, 15)
	p, model, prompt, maxIter, _, _ := sm.resolveAgentConfig("coder")
	if model != "default-model" {
		t.Fatalf("model = %q", model)
	}
	if p != nil {
		t.Fatalf("provider should be nil default here (manager provider), got %T", p)
	}
	if maxIter != 15 {
		t.Fatalf("maxIter = %d", maxIter)
	}
	if prompt == "" {
		t.Fatal("prompt should be non-empty (default system prompt)")
	}
}

// TestResolveAgentConfig_AgentOverrides covers the agent-context override path.
func TestResolveAgentConfig_AgentOverrides(t *testing.T) {
	sm := NewSubagentManager(nil, "default-model", "/w", nil, 15)
	sm.SetLLMOptions(2000, 0.4)
	sm.getAgentContext = func(agentID string) AgentContextInfo {
		return AgentContextInfo{
			Model:               "agent-model",
			MaxIterations:       30,
			MaxTokens:           4000,
			Temperature:         0.7,
			Context:             "some agent context",
			Workspace:           "/agent-ws",
			Name:                "Coder",
			ContextWindow:       12000,
		}
	}

	p, model, prompt, maxIter, llmOpts, ctxWin := sm.resolveAgentConfig("coder")
	if model != "agent-model" {
		t.Fatalf("model = %q", model)
	}
	if p != nil {
		t.Fatalf("provider = %T", p)
	}
	if maxIter != 30 {
		t.Fatalf("maxIter = %d", maxIter)
	}
	if ctxWin != 12000 {
		t.Fatalf("contextWindow = %d", ctxWin)
	}
	if !containsStr(prompt, "some agent context") {
		t.Fatalf("prompt missing context: %q", prompt)
	}
	if !containsStr(prompt, "Coder (coder)") {
		t.Fatalf("prompt missing agent name: %q", prompt)
	}
	if llmOpts == nil {
		t.Fatal("expected llmOpts populated")
	}
	if llmOpts["max_tokens"] != 4000 {
		t.Fatalf("max_tokens = %v", llmOpts["max_tokens"])
	}
	if llmOpts["temperature"] != 0.7 {
		t.Fatalf("temperature = %v", llmOpts["temperature"])
	}
}

// TestResolveAgentConfig_EmptyContextFieldName covers the agentName fallback
// to agentID when Name is empty.
func TestResolveAgentConfig_EmptyAgentName(t *testing.T) {
	sm := NewSubagentManager(nil, "default", "/w", nil, 5)
	sm.getAgentContext = func(agentID string) AgentContextInfo {
		return AgentContextInfo{
			Model:   "m",
			Context: "ctx",
		}
	}
	_, _, prompt, _, _, _ := sm.resolveAgentConfig("coder")
	if !containsStr(prompt, "coder (coder)") {
		t.Fatalf("expected agentName fallback to agentID: %q", prompt)
	}
}

// TestResolveAgentConfig_EmptyModelUsesDefault covers the model fallback.
func TestResolveAgentConfig_EmptyModelUsesDefault(t *testing.T) {
	sm := NewSubagentManager(nil, "default-model", "/w", nil, 5)
	sm.getAgentContext = func(agentID string) AgentContextInfo {
		return AgentContextInfo{Context: "", Model: ""}
	}
	_, model, _, _, _, _ := sm.resolveAgentConfig("coder")
	if model != "default-model" {
		t.Fatalf("model = %q, want default-model", model)
	}
	// No max tokens/temp set on manager => llmOpts nil.
	sm2 := NewSubagentManager(nil, "d", "/w", nil, 5)
	_, _, _, _, llmOpts, _ := sm2.resolveAgentConfig("coder")
	if llmOpts != nil {
		t.Fatalf("llmOpts should be nil, got %v", llmOpts)
	}
}

// TestResolveAgentConfig_ProviderFromContext verifies the provider is read from
// the agent context when available (avoids nil deref in runTask).
func TestResolveAgentConfig_ProviderFromContext(t *testing.T) {
	sm := NewSubagentManager(nil, "default", "/w", nil, 5)
	sm.getAgentContext = func(agentID string) AgentContextInfo {
		return AgentContextInfo{Model: "m", Provider: &scriptedSubagentProvider{}}
	}
	p, _, _, _, _, _ := sm.resolveAgentConfig("coder")
	if p == nil {
		t.Fatal("expected non-nil provider from context")
	}
	if p.GetDefaultModel() != "test-model" {
		t.Fatalf("provider default model = %q", p.GetDefaultModel())
	}
}

// TestSubagentStatusConstants sanity-check the exported status strings.
func TestSubagentStatusConstants(t *testing.T) {
	if SubagentStatusCompleted != "completed" {
		t.Fatalf("SubagentStatusCompleted = %q", SubagentStatusCompleted)
	}
	if !isSubagentTerminalStatus(SubagentStatusCompleted) {
		t.Fatal("expected completed terminal")
	}
	task := &SubagentTask{mu: &sync.Mutex{}}
	if task.Delivered() {
		t.Fatal("unexpected delivered")
	}
}