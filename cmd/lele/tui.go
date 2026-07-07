package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func tuiCmd() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	msgBus := bus.NewMessageBus()
	agentLoop := agent.NewAgentLoop(cfg, msgBus)

	// Start agent loop background processing
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := agentLoop.Run(ctx); err != nil {
			fmt.Printf("Agent loop error: %v\n", err)
		}
	}()
	defer agentLoop.Stop()

	// Get shared session manager path from AgentLoop setup logic
	unifiedSessionsDir := filepath.Join(config.GetLeleDir(), "sessions")
	sessionMgr := session.NewSessionManager(unifiedSessionsDir)

	// Migrate sessions to unified location just in case
	for _, agentID := range agentLoop.GetProvidable().ListAvailableAgentIDs() {
		if agentInfo, ok := agentLoop.GetProvidable().GetAgentInfo(agentID); ok {
			oldSessionsDir := filepath.Join(agentInfo.Workspace, "sessions")
			session.MigrateFromWorkspace(oldSessionsDir, unifiedSessionsDir)
		}
	}

	// Initialize the TUI model
	tuiModel := tui.NewModel(cfg, agentLoop, sessionMgr)

	// Run bubbletea program in AltScreen mode for full terminal experience
	p := tea.NewProgram(tuiModel, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
