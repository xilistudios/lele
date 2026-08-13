package main

import (
	"context"
	"fmt"
	"os"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func tuiCmd(sessionID string) {
	logger.SetQuiet(true)

	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	setupFileLogging(cfg)

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

	// Use the agent loop's shared session manager so the TUI and agent loop
	// operate on the same in-memory session state.
	sessionMgr := agentLoop.SessionManager()
	if sessionMgr == nil {
		fmt.Println("Error: session manager not initialized")
		os.Exit(1)
	}

	// Initialize the TUI model
	tuiModel := tui.NewModel(cfg, agentLoop, sessionMgr, sessionID)

	// Run bubbletea program in AltScreen mode.
	// Mouse capture starts ON for scroll-wheel and sidebar click support.
	// Users can hold Shift while clicking/dragging to bypass mouse capture
	// and use native terminal text selection. ctrl+t toggles mouse off as
	// a fallback for terminals that don't support Shift bypass.
	p := tea.NewProgram(tuiModel, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Printf("Error running TUI: %v\n", err)
		os.Exit(1)
	}

	var resumeID string
	if m, ok := finalModel.(*tui.Model); ok && m != nil {
		resumeID = m.ResumeSessionID()
	} else if tuiModel != nil {
		resumeID = tuiModel.ResumeSessionID()
	}

	if resumeID != "" {
		fmt.Printf("Resume with -s (or command below):\nlele --session=%s\n", resumeID)
	}
}
