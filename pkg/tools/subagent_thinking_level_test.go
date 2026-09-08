// Lele - Ultra-lightweight personal AI agent
// T4 (per-agent thinking level): the subagent LLM options must carry the
// TARGET agent's resolved thinking level as a "reasoning" entry, with the
// manager (parent) level as fallback only when no agent-context callback
// exists. Semantics mirror pkg/agent/llm_caller.go buildLLMOptions:
//   "off" -> {"enabled": false}, low|medium|high -> {"effort": level,
//   "enabled": true}, "" -> key absent.
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package tools

import (
	"testing"
)

// reasoningEquals compares the "reasoning" entry of llmOptions against the
// expected map (or asserts absence when wantReasoning is false).
func reasoningEquals(t *testing.T, opts map[string]any, wantReasoning bool, want map[string]any) {
	t.Helper()
	got, present := opts["reasoning"]
	if !wantReasoning {
		if present {
			t.Fatalf("reasoning key = %v, want it absent", got)
		}
		return
	}
	if !present {
		t.Fatal("reasoning key absent, want it present")
	}
	gotMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %#v, want map[string]any", got)
	}
	if len(gotMap) != len(want) {
		t.Fatalf("reasoning = %#v, want %#v", gotMap, want)
	}
	for k, v := range want {
		if gotMap[k] != v {
			t.Errorf("reasoning[%q] = %v, want %v", k, gotMap[k], v)
		}
	}
}

// resolveLLMOptionsWithCtx runs resolveAgentConfig against a manager whose
// context callback returns exactly the given AgentContextInfo.
func resolveLLMOptionsWithCtx(ctxInfo AgentContextInfo) map[string]any {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 10)
	sm.SetAgentContextCallback(func(string) AgentContextInfo { return ctxInfo })
	_, _, _, _, llmOptions, _ := sm.resolveAgentConfig("target")
	return llmOptions
}

func TestResolveAgentConfig_ThinkingLevel(t *testing.T) {
	tests := []struct {
		name          string
		managerLevel  string // SetThinkingLevel on the manager (parent default)
		ctxLevel      string // AgentContextInfo.ThinkingLevel (target resolved)
		hasCallback   bool
		wantReasoning bool
		want          map[string]any
	}{
		{
			name: "target low emits effort", managerLevel: "", ctxLevel: "low", hasCallback: true,
			wantReasoning: true, want: map[string]any{"effort": "low", "enabled": true},
		},
		{
			name: "target off explicitly disables", managerLevel: "", ctxLevel: "off", hasCallback: true,
			wantReasoning: true, want: map[string]any{"enabled": false},
		},
		{
			// Manager default is the fallback ONLY when no context callback
			// exists (e.g. standalone/cron managers): with a callback present
			// the target's resolved level always wins, see the next case.
			name: "no callback falls back to manager medium", managerLevel: "medium", hasCallback: false,
			wantReasoning: true, want: map[string]any{"effort": "medium", "enabled": true},
		},
		{
			// Design decision: the target's resolved level WINS even when it is
			// "". Empty means the target has no config level (agents.defaults was
			// already folded into its resolved value upstream), so the parent's
			// manager default must not leak into it.
			name: "empty target overrides manager high: no reasoning key", managerLevel: "high", ctxLevel: "", hasCallback: true,
			wantReasoning: false,
		},
		{
			name: "no callback uses manager default", managerLevel: "low", hasCallback: false,
			wantReasoning: true, want: map[string]any{"effort": "low", "enabled": true},
		},
		{
			name: "no callback and empty manager default: no reasoning key", managerLevel: "", hasCallback: false,
			wantReasoning: false,
		},
		{
			// Defensive guard: invalid stored values cannot occur (config
			// validation + resolveAgentThinkingLevel normalize), but they must
			// degrade to "" (no opinion), never to a bogus effort on the wire.
			name: "invalid level treated as empty", managerLevel: "ultra", ctxLevel: "", hasCallback: false,
			wantReasoning: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 10)
			sm.SetThinkingLevel(tt.managerLevel)
			if tt.hasCallback {
				info := tt.ctxLevel
				sm.SetAgentContextCallback(func(string) AgentContextInfo {
					return AgentContextInfo{ThinkingLevel: info}
				})
			}
			_, _, _, _, llmOptions, _ := sm.resolveAgentConfig("target")
			reasoningEquals(t, llmOptions, tt.wantReasoning, tt.want)
		})
	}
}

// TestResolveAgentConfig_ThinkingLevelWithLLMOptions pins the extended build
// condition: adding reasoning must not disturb max_tokens/temperature, and
// reasoning alone (without any tokens/temperature set) must still produce a
// non-nil llmOptions map.
func TestResolveAgentConfig_ThinkingLevelWithLLMOptions(t *testing.T) {
	// reasoning + explicit manager LLM options: all three keys present.
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 10)
	sm.SetLLMOptions(2048, 0.6)
	sm.SetThinkingLevel("high")
	_, _, _, _, llmOptions, _ := sm.resolveAgentConfig("target")
	if llmOptions["max_tokens"] != 2048 {
		t.Errorf("max_tokens = %v, want 2048", llmOptions["max_tokens"])
	}
	if llmOptions["temperature"] != 0.6 {
		t.Errorf("temperature = %v, want 0.6", llmOptions["temperature"])
	}
	reasoningEquals(t, llmOptions, true, map[string]any{"effort": "high", "enabled": true})

	// Only reasoning (no SetLLMOptions, no ctx tokens/temperature): map is
	// created by the reasoning branch and carries ONLY the reasoning key.
	sm2 := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 10)
	sm2.SetThinkingLevel("off")
	_, _, _, _, only, _ := sm2.resolveAgentConfig("target")
	if len(only) != 1 {
		t.Fatalf("llmOptions = %#v, want only the reasoning key", only)
	}
	reasoningEquals(t, only, true, map[string]any{"enabled": false})

	// Regression: no LLM options and no thinking level at all -> nil map,
	// exactly the pre-change behavior.
	sm3 := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 10)
	_, _, _, _, none, _ := sm3.resolveAgentConfig("target")
	if none != nil {
		t.Errorf("llmOptions = %#v, want nil when nothing is configured", none)
	}

	// Target ctx max_tokens/temperature still win over manager defaults, and
	// the target's thinking level rides along unchanged.
	sm4 := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 10)
	sm4.SetLLMOptions(1024, 0.3)
	sm4.SetThinkingLevel("low")
	sm4.SetAgentContextCallback(func(string) AgentContextInfo {
		return AgentContextInfo{MaxTokens: 4096, Temperature: 0.9, ThinkingLevel: "medium"}
	})
	_, _, _, _, mixed, _ := sm4.resolveAgentConfig("target")
	if mixed["max_tokens"] != 4096 {
		t.Errorf("max_tokens = %v, want target override 4096", mixed["max_tokens"])
	}
	if mixed["temperature"] != 0.9 {
		t.Errorf("temperature = %v, want target override 0.9", mixed["temperature"])
	}
	reasoningEquals(t, mixed, true, map[string]any{"effort": "medium", "enabled": true})
}

// TestThinkingReasoningOptions pins the level -> wire mapping helper directly,
// including the defensive normalization of stray casing/whitespace.
func TestThinkingReasoningOptions(t *testing.T) {
	cases := []struct {
		in    string
		want  map[string]any
		isNil bool
	}{
		{in: "", isNil: true},
		{in: "default", isNil: true},
		{in: "none", isNil: true},
		{in: "bogus", isNil: true},
		{in: "off", want: map[string]any{"enabled": false}},
		{in: "OFF", want: map[string]any{"enabled": false}},
		{in: " low ", want: map[string]any{"effort": "low", "enabled": true}},
		{in: "MEDIUM", want: map[string]any{"effort": "medium", "enabled": true}},
		{in: "high", want: map[string]any{"effort": "high", "enabled": true}},
	}
	for _, tt := range cases {
		got := thinkingReasoningOptions(tt.in)
		if tt.isNil {
			if got != nil {
				t.Errorf("thinkingReasoningOptions(%q) = %#v, want nil", tt.in, got)
			}
			continue
		}
		if len(got) != len(tt.want) {
			t.Errorf("thinkingReasoningOptions(%q) = %#v, want %#v", tt.in, got, tt.want)
			continue
		}
		for k, v := range tt.want {
			if got[k] != v {
				t.Errorf("thinkingReasoningOptions(%q)[%q] = %v, want %v", tt.in, k, got[k], v)
			}
		}
	}
}
