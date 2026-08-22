package main

import (
	"context"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/tools"
)

// mockJobExecutor implements tools.JobExecutor for cron helper testing.
type mockJobExecutor struct {
	content string
}

func (m *mockJobExecutor) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	m.content = content
	return "ok", nil
}

func TestSetupCronTool(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	if cfg.Agents.Defaults.Workspace == "" {
		cfg.Agents.Defaults.Workspace = dir
	}

	al := agent.NewAgentLoop(cfg, bus.NewMessageBus())
	executor := &mockJobExecutor{}
	svc := setupCronTool(executor, al, bus.NewMessageBus(), dir, false, 30*time.Second, cfg)

	if svc == nil {
		t.Fatal("setupCronTool returned nil service")
	}
	if executor.content != "" {
		t.Fatalf("executor should not have run yet")
	}
	_ = tools.CronTool{}
}
