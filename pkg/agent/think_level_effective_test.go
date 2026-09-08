// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

// newThinkLevelTestLoop builds an AgentLoop whose default agent carries the
// given resolved thinking level, isolated in a throwaway LELE_CONFIG_DIR so
// persisted session meta (SQLite) never touches the real store.
func newThinkLevelTestLoop(t *testing.T, agentLevel string) (*AgentLoop, *AgentInstance) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "think-level-test-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ThinkingLevel:     strPtrOrNil(agentLevel),
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}
	return al, agent
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// TestGetEffectiveThinkLevel covers the three-layer contract: session override
// wins, then the agent's resolved config level, then "default".
func TestGetEffectiveThinkLevel(t *testing.T) {
	tests := []struct {
		name       string
		agentLevel string // resolved level on the default agent (config)
		sessionSet string // level passed to SetThinkLevel ("" = never set)
		want       string
	}{
		{"nothing set", "", "", "default"},
		{"agent level only", "medium", "", "medium"},
		{"agent off only", "off", "", "off"},
		{"session wins over agent", "high", "low", "low"},
		{"session off wins over agent", "high", "off", "off"},
		{"session default falls through to agent", "high", "default", "high"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			al, agent := newThinkLevelTestLoop(t, tc.agentLevel)
			if agent.ThinkingLevel != tc.agentLevel {
				t.Fatalf("precondition: agent.ThinkingLevel = %q, want %q", agent.ThinkingLevel, tc.agentLevel)
			}
			sessionKey := "telegram:12345"
			providable := al.GetProvidable()
			if tc.sessionSet != "" {
				if !providable.SetThinkLevel(sessionKey, tc.sessionSet) {
					t.Fatalf("SetThinkLevel(%q) failed", tc.sessionSet)
				}
			}
			if got := providable.GetEffectiveThinkLevel(sessionKey); got != tc.want {
				t.Errorf("GetEffectiveThinkLevel() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGetThinkLevel_ContractUnchanged guards R7: the session-override getter
// used by /think and the WebUI chip must NOT resolve the agent level.
func TestGetThinkLevel_ContractUnchanged(t *testing.T) {
	al, _ := newThinkLevelTestLoop(t, "high")
	providable := al.GetProvidable()
	const sessionKey = "telegram:999"
	if got := providable.GetThinkLevel(sessionKey); got != "default" {
		t.Errorf("GetThinkLevel() = %q, want \"default\" (override-only contract)", got)
	}
	if got := providable.GetEffectiveThinkLevel(sessionKey); got != "high" {
		t.Errorf("GetEffectiveThinkLevel() = %q, want \"high\"", got)
	}
}

// TestStatusRenderers_ShowEffectiveLevel asserts both /status renderers show
// exactly the level buildLLMOptions will apply (agent config layer).
func TestStatusRenderers_ShowEffectiveLevel(t *testing.T) {
	al, agent := newThinkLevelTestLoop(t, "medium")
	const sessionKey = "telegram:4242"

	ch, ok := al.commandHandler.(*commandHandlerImpl)
	if !ok {
		t.Fatal("command handler is not *commandHandlerImpl")
	}
	status := ch.formatStatusResponse(agent, sessionKey, "telegram")
	if !strings.Contains(status, "Think: medium") {
		t.Errorf("command_handler /status does not show effective level: %s", status)
	}

	mp, ok := al.messageProcessor.(*messageProcessorImpl)
	if !ok {
		t.Fatal("message processor is not *messageProcessorImpl")
	}
	status2 := mp.formatStatusResponse(agent, sessionKey, "telegram")
	if !strings.Contains(status2, "Think: medium") {
		t.Errorf("message_processor /status does not show effective level: %s", status2)
	}

	// Session override must win in the display too, matching buildLLMOptions.
	if !al.GetProvidable().SetThinkLevel(sessionKey, "low") {
		t.Fatal("SetThinkLevel failed")
	}
	if status3 := ch.formatStatusResponse(agent, sessionKey, "telegram"); !strings.Contains(status3, "Think: low") {
		t.Errorf("command_handler /status ignores session override: %s", status3)
	}
	if status4 := mp.formatStatusResponse(agent, sessionKey, "telegram"); !strings.Contains(status4, "Think: low") {
		t.Errorf("message_processor /status ignores session override: %s", status4)
	}
}

// TestResetAgentSession_ClearsPersistedModelAndThink locks the /reset fix:
// after /clear, neither the in-memory sync.Maps nor the persisted session meta
// may keep an override — otherwise buildLLMOptions (thinking) or
// syncSessionModel (model) resurrects it on the very next turn.
func TestResetAgentSession_ClearsPersistedModelAndThink(t *testing.T) {
	al, agent := newThinkLevelTestLoop(t, "")
	sessionKey := "telegram:777"

	// Go through the real user path: SetThinkLevel persists "off" (and only
	// deletes the sync.Map entry), SetModel persists the model override.
	if !al.GetProvidable().SetThinkLevel(sessionKey, "off") {
		t.Fatal("SetThinkLevel(off) failed")
	}
	if err := agent.Sessions.SetModel(sessionKey, "other-model"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	al.sessionModels.Store(sessionKey, "other-model")

	if got := agent.Sessions.GetThinkingLevel(sessionKey); got != "off" {
		t.Fatalf("precondition: persisted thinking = %q, want \"off\"", got)
	}

	if err := al.resetAgentSession(agent, sessionKey); err != nil {
		t.Fatalf("resetAgentSession: %v", err)
	}

	if got := agent.Sessions.GetThinkingLevel(sessionKey); got != "" {
		t.Errorf("persisted thinking after reset = %q, want \"\"", got)
	}
	if got := agent.Sessions.GetModel(sessionKey); got != "" {
		t.Errorf("persisted model after reset = %q, want \"\"", got)
	}
	if _, ok := al.sessionThinking.Load(sessionKey); ok {
		t.Error("in-memory sessionThinking survived reset")
	}
	if _, ok := al.sessionModels.Load(sessionKey); ok {
		t.Error("in-memory sessionModels survived reset")
	}

	// No resurrection: with the agent having no thinking level and no model
	// ReasoningConfig, the next request must carry no reasoning key at all —
	// in particular NOT the {"enabled": false} a stale persisted "off" would
	// produce.
	caller := newLLMCaller(al)
	opts := llmCallOptions{
		ctx:        context.Background(),
		agent:      agent,
		messages:   []providers.Message{{Role: "user", Content: "hi"}},
		model:      "test-model",
		sessionKey: sessionKey,
	}
	if got := caller.buildLLMOptions(opts); got["reasoning"] != nil {
		t.Fatalf("reasoning key emitted after reset: %v", got["reasoning"])
	}
}

// TestSetThinkLevel_CanonicalLevels locks the review fix: SetThinkLevel must
// validate against config.NormalizeThinkingLevel instead of a duplicated
// inline whitelist, keeping strict parity for the empty string (rejected) while
// accepting "none" as an alias of "default" (clears the override, persists "").
func TestSetThinkLevel_CanonicalLevels(t *testing.T) {
	tests := []struct {
		name        string
		level       string
		wantOK      bool
		wantGet     string // GetThinkLevel after the call
		wantPersist string // persisted session-meta value
	}{
		{"empty rejected", "", false, "default", ""},
		{"bogus rejected", "bogus", false, "default", ""},
		{"off accepted", "off", true, "off", "off"},
		{"low accepted", "low", true, "low", "low"},
		{"medium accepted", "medium", true, "medium", "medium"},
		{"high accepted", "high", true, "high", "high"},
		{"default clears override", "default", true, "default", ""},
		{"none clears override", "none", true, "default", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			al, agent := newThinkLevelTestLoop(t, "high")
			providable := al.GetProvidable()
			const sessionKey = "telegram:5000"

			// Start from a real override so "clear" cases are meaningful.
			if !providable.SetThinkLevel(sessionKey, "low") {
				t.Fatal("precondition: SetThinkLevel(low) failed")
			}

			ok := providable.SetThinkLevel(sessionKey, tc.level)
			if ok != tc.wantOK {
				t.Fatalf("SetThinkLevel(%q) = %v, want %v", tc.level, ok, tc.wantOK)
			}
			if !tc.wantOK {
				// Rejected calls must leave the previous override untouched.
				if got := providable.GetThinkLevel(sessionKey); got != "low" {
					t.Errorf("rejected SetThinkLevel(%q) mutated state: GetThinkLevel = %q, want \"low\"", tc.level, got)
				}
				return
			}
			if got := providable.GetThinkLevel(sessionKey); got != tc.wantGet {
				t.Errorf("GetThinkLevel() = %q, want %q", got, tc.wantGet)
			}
			if got := agent.Sessions.GetThinkingLevel(sessionKey); got != tc.wantPersist {
				t.Errorf("persisted thinking = %q, want %q", got, tc.wantPersist)
			}
			// An override-free session must fall through to the agent level.
			wantEffective := tc.wantGet
			if wantEffective == "default" {
				wantEffective = "high"
			}
			if got := providable.GetEffectiveThinkLevel(sessionKey); got != wantEffective {
				t.Errorf("GetEffectiveThinkLevel() = %q, want %q", got, wantEffective)
			}
		})
	}
}

// TestThinkCommand_AcceptsNoneAlias guards the user-facing half of the "none"
// alias: /think none must clear the override and report it (not "Unknown think
// level"), and the rejection message must list "default" among valid levels.
func TestThinkCommand_AcceptsNoneAlias(t *testing.T) {
	al, agent := newThinkLevelTestLoop(t, "medium")
	ch, ok := al.commandHandler.(*commandHandlerImpl)
	if !ok {
		t.Fatal("command handler is not *commandHandlerImpl")
	}
	const sessionKey = "telegram:5100"
	providable := al.GetProvidable()

	if !providable.SetThinkLevel(sessionKey, "high") {
		t.Fatal("precondition: SetThinkLevel(high) failed")
	}

	if resp := ch.handleThinkCommand(sessionKey, []string{"none"}); !strings.Contains(resp, "DEFAULT") {
		t.Errorf("/think none did not report a cleared override: %q", resp)
	}
	if got := providable.GetThinkLevel(sessionKey); got != "default" {
		t.Errorf("after /think none, GetThinkLevel = %q, want \"default\"", got)
	}
	if got := agent.Sessions.GetThinkingLevel(sessionKey); got != "" {
		t.Errorf("after /think none, persisted thinking = %q, want \"\"", got)
	}

	if resp := ch.handleThinkCommand(sessionKey, []string{"bogus"}); !strings.Contains(resp, "Valid levels: default, off, low, medium, high") {
		t.Errorf("/think bogus error message does not list default: %q", resp)
	}
}
