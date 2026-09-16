package agent

import (
	"os"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// TestHint_UnpinnedResendKeepsModel reproduces the WebUI bug where a model
// chosen from the chat dropdown silently reverted to the agent default.
//
// The WebUI attaches `agent_id` to EVERY websocket message (useMessages.ts
// sends currentAgentId with each send), derived from the agent it is currently
// displaying for the chat. For a session the user never pinned with /agent, the
// next message therefore re-sends the DEFAULT agent id.
//
// SetSessionAgent's guard only short-circuits when the session is explicitly
// pinned AND the id matches:
//
//	if _, pinned := ap.al.sessionAgentOverride(resolvedKey); pinned && currentAgentID == agentID {
//		return
//	}
//
// For an unpinned session `pinned` is false, so the guard never fired even
// though the effective agent already was the one being set. The call fell
// through to `sessionModels.Delete(resolvedKey)` plus a persisted
// `SetModel(resolvedKey, "")`. Since every agent shares one SessionManager
// (registry.SetSharedSessionManager), that cleared the selection globally and
// the dropdown snapped back to the agent default one message later.
//
// The fix separates intent structurally: send paths now call HintSessionAgent,
// which is a no-op when the session already resolves to that agent and never
// touches the model override. SetSessionAgent keeps its explicit-bind
// semantics, including the model reset that an actual agent switch needs.
//
// The pre-existing TestSetSessionAgent_PreservesModelWhenAgentUnchanged misses
// the bug because it pins the session first (SetSessionAgent(sessionKey,
// "agent1") before SetSessionModel), which is exactly the state that makes the
// old guard work.
func TestHint_UnpinnedResendKeepsModel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "default-model",
				Provider:          "test",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Model: &config.AgentModelConfig{Primary: "default-model"}},
				{ID: "agent1", Model: &config.AgentModelConfig{Primary: "agent1-model"}},
			},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"test": {
					ProviderConfig: config.ProviderConfig{APIKey: "test-key"},
					Models: map[string]config.ProviderModelConfig{
						"default-model": {Model: "default-model"},
						"agent1-model":  {Model: "agent1-model"},
						"custom-model":  {Model: "custom-model"},
					},
				},
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	sessionKey := "test-session:unpinned-model"

	// The user never ran /agent: the session has NO explicit pin. This is the
	// state every fresh WebUI chat starts in.
	if _, pinned := al.sessionAgentOverride(sessionKey); pinned {
		t.Fatal("session starts pinned — test setup broken")
	}

	// User picks "custom-model" from the chat dropdown.
	al.providable.SetSessionModel(sessionKey, "custom-model")
	if got := al.providable.GetSessionModel(sessionKey); got != "test:custom-model" {
		t.Fatalf("after dropdown selection: model = %q, want %q", got, "test:custom-model")
	}

	// Next message: the WebUI always sends agent_id, and for an unpinned
	// session it sends the default agent it is displaying.
	al.providable.HintSessionAgent(sessionKey, al.providable.GetDefaultAgentID())

	// The selection must survive.
	if got := al.providable.GetSessionModel(sessionKey); got != "test:custom-model" {
		t.Fatalf("model after redundant agent_id resend = %q, want %q "+
			"(a routing hint must not clear the dropdown selection)",
			got, "test:custom-model")
	}

	// A long run of messages must not degrade the selection either: the hint
	// path is idempotent.
	for i := 0; i < 5; i++ {
		al.providable.HintSessionAgent(sessionKey, al.providable.GetDefaultAgentID())
	}
	if got := al.providable.GetSessionModel(sessionKey); got != "test:custom-model" {
		t.Fatalf("model after repeated hints = %q, want %q", got, "test:custom-model")
	}

	// And the hint must not have created a bogus pin: an unpinned session stays
	// unpinned so route-derived agent selection keeps working.
	if _, pinned := al.sessionAgentOverride(sessionKey); pinned {
		t.Error("a routing hint pinned the session — routes can no longer win")
	}
}

// TestHint_AgentSwitchStillBindsAndKeepsModel covers the other half of the hint
// contract: when the hinted agent really differs (a client switching agents
// purely via agent_id, or a session whose route changed underneath it) the hint
// must still bind, while preserving the model the user explicitly chose.
func TestHint_AgentSwitchStillBindsAndKeepsModel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "default-model",
				Provider:          "test",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Model: &config.AgentModelConfig{Primary: "default-model"}},
				{ID: "agent1", Model: &config.AgentModelConfig{Primary: "agent1-model"}},
			},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"test": {
					ProviderConfig: config.ProviderConfig{APIKey: "test-key"},
					Models: map[string]config.ProviderModelConfig{
						"default-model": {Model: "default-model"},
						"agent1-model":  {Model: "agent1-model"},
						"custom-model":  {Model: "custom-model"},
					},
				},
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	sessionKey := "test-session:hint-switch"

	al.providable.SetSessionModel(sessionKey, "custom-model")
	al.providable.HintSessionAgent(sessionKey, "agent1")

	// The bind happened...
	if got := al.providable.GetSessionAgent(sessionKey); got != "agent1" {
		t.Fatalf("agent after hint = %q, want %q", got, "agent1")
	}
	// ...and it is a real pin, so later route resolution honours it.
	if id, pinned := al.sessionAgentOverride(sessionKey); !pinned || id != "agent1" {
		t.Fatalf("hint bind did not produce a pin: (%q,%v)", id, pinned)
	}
	// ...but the user's model choice survived, because a hint is not a switch.
	if got := al.providable.GetSessionModel(sessionKey); got != "test:custom-model" {
		t.Fatalf("model after hint bind = %q, want %q", got, "test:custom-model")
	}
}

// TestSetSessionAgent_ExplicitSwitchStillResetsModel guards the behaviour the
// hint path must NOT change: an explicit agent switch really does drop the model
// override, otherwise the previous agent's model leaks into the new agent.
func TestSetSessionAgent_ExplicitSwitchStillResetsModel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "default-model",
				Provider:          "test",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "agent1", Model: &config.AgentModelConfig{Primary: "agent1-model"}},
			},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"test": {
					ProviderConfig: config.ProviderConfig{APIKey: "test-key"},
					Models: map[string]config.ProviderModelConfig{
						"default-model": {Model: "default-model"},
						"agent1-model":  {Model: "agent1-model"},
						"custom-model":  {Model: "custom-model"},
					},
				},
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	sessionKey := "test-session:explicit-switch"

	al.providable.SetSessionModel(sessionKey, "custom-model")
	al.providable.SetSessionAgent(sessionKey, "agent1")

	if got := al.providable.GetSessionModel(sessionKey); got != "test:agent1-model" {
		t.Fatalf("model after explicit switch = %q, want the new agent's default %q",
			got, "test:agent1-model")
	}
}
