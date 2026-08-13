package tui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/store"
)

func TestResumeSessionID_EmptyOnWelcomeWithNoMessages(t *testing.T) {
	m := newTestModel(t)
	m.showWelcome = true
	m.currentKey = ""

	if got := m.ResumeSessionID(); got != "" {
		t.Errorf("ResumeSessionID() = %q, want empty string", got)
	}
}

func TestResumeSessionID_WithActiveChat(t *testing.T) {
	m := newTestModel(t)
	uuid := "574f2fc5-3e50-4415-9e7d-aa70e4d4ab36"
	key := "tui:chat:" + uuid

	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.AddMessage(key, "user", "hola")
	m.currentKey = key
	m.showWelcome = false

	if got := m.ResumeSessionID(); got != uuid {
		t.Errorf("ResumeSessionID() = %q, want %q", got, uuid)
	}
}

func TestResumeSessionID_BareUUIDKey(t *testing.T) {
	m := newTestModel(t)
	uuid := "574f2fc5-3e50-4415-9e7d-aa70e4d4ab36"

	m.sessionMgr.GetOrCreate(uuid)
	m.sessionMgr.AddMessage(uuid, "user", "hola")
	m.currentKey = uuid
	m.showWelcome = false

	if got := m.ResumeSessionID(); got != uuid {
		t.Errorf("ResumeSessionID() = %q, want %q", got, uuid)
	}
}

func TestResumeSessionID_InSubagentReturnsParent(t *testing.T) {
	m := newTestModel(t)
	parentUUID := "574f2fc5-3e50-4415-9e7d-aa70e4d4ab36"
	parentKey := "tui:chat:" + parentUUID
	subagentKey := "native:" + parentKey + ":subagent-1"

	m.sessionMgr.GetOrCreate(parentKey)
	m.sessionMgr.AddMessage(parentKey, "user", "run subagent")
	m.parentSessionKey = parentKey
	m.currentKey = subagentKey
	m.showWelcome = false

	if got := m.ResumeSessionID(); got != parentUUID {
		t.Errorf("ResumeSessionID() = %q, want parent UUID %q", got, parentUUID)
	}
}

func TestNewModel_ResolvesBareUUIDAndSetsMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	dbPath := filepath.Join(tmpDir, "sessions.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer st.Close()

	sm := session.NewSessionManager()
	sm.SetStore(st)
	uuid := "574f2fc5-3e50-4415-9e7d-aa70e4d4ab36"
	fullKey := "tui:chat:" + uuid

	sm.GetOrCreate(fullKey)
	_ = sm.SetMode(fullKey, "chat")
	sm.AddMessage(fullKey, "user", "message 1")
	_ = sm.Save(fullKey)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Providers: &config.ProvidersConfig{},
	}
	msgBus := bus.NewMessageBus()
	al := agent.NewAgentLoop(cfg, msgBus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go al.Run(ctx)

	// Open model passing bare UUID
	m := NewModel(cfg, al, sm, uuid)

	if m.currentKey != fullKey {
		t.Errorf("m.currentKey = %q, want %q", m.currentKey, fullKey)
	}
	if m.showWelcome {
		t.Errorf("m.showWelcome = true, want false")
	}
	if m.currentMode != ModeChat {
		t.Errorf("m.currentMode = %v, want %v (ModeChat)", m.currentMode, ModeChat)
	}
}

func TestNewModel_ResolvesPrefixedKey(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	dbPath := filepath.Join(tmpDir, "sessions.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer st.Close()

	sm := session.NewSessionManager()
	sm.SetStore(st)
	uuid := "574f2fc5-3e50-4415-9e7d-aa70e4d4ab36"
	fullKey := "tui:chat:" + uuid

	sm.GetOrCreate(fullKey)
	sm.AddMessage(fullKey, "user", "message 1")
	_ = sm.Save(fullKey)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Providers: &config.ProvidersConfig{},
	}
	msgBus := bus.NewMessageBus()
	al := agent.NewAgentLoop(cfg, msgBus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go al.Run(ctx)

	// Open model passing full key
	m := NewModel(cfg, al, sm, fullKey)

	if m.currentKey != fullKey {
		t.Errorf("m.currentKey = %q, want %q", m.currentKey, fullKey)
	}
	if m.showWelcome {
		t.Errorf("m.showWelcome = true, want false")
	}
}
