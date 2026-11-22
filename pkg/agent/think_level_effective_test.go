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

func TestGetEffectiveThinkLevel(t *testing.T) {
	tests := []struct {
		name       string
		agentLevel string
		sessionSet string
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

func TestResetAgentSession_ClearsPersistedModelAndThink(t *testing.T) {
	al, agent := newThinkLevelTestLoop(t, "")
	sessionKey := "telegram:777"

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

func TestSetThinkLevel_CanonicalLevels(t *testing.T) {
	tests := []struct {
		name        string
		level       string
		wantOK      bool
		wantGet     string
		wantPersist string
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

			if !providable.SetThinkLevel(sessionKey, "low") {
				t.Fatal("precondition: SetThinkLevel(low) failed")
			}

			ok := providable.SetThinkLevel(sessionKey, tc.level)
			if ok != tc.wantOK {
				t.Fatalf("SetThinkLevel(%q) = %v, want %v", tc.level, ok, tc.wantOK)
			}
			if !tc.wantOK {
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
