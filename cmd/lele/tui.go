package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/tui"

	tea "github.com/charmbracelet/bubbletea"
)

var tuiExitJoinGrace = 2 * time.Second

type tuiCloser interface {
	Close()
}

func shutdownTUI(model tuiCloser, cancel context.CancelFunc, loop turnJoiner) {
	if model != nil {
		model.Close()
	}
	if cancel != nil {
		cancel()
	}
	if loop == nil {
		return
	}
	if err := loop.StopWithin(tuiExitJoinGrace); err != nil {
		logger.WarnCF("tui", "TUI exit: agent teardown abandoned with work in flight",
			map[string]interface{}{"error": err.Error(), "grace": tuiExitJoinGrace.String()})
	}
}

func tuiCmd(sessionID string) {
	logger.SetQuiet(true)

	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	setupFileLogging(cfg)

	msgBus := bus.NewMessageBus()

	dbPath := defaultDBPath()
	s, storeCleanup, storeErr := openSharedStore(dbPath, "tui")
	if storeErr != nil {
		fmt.Fprintf(os.Stderr, "Failed to open SQLite store at %s: %v — falling back to JSON storage\n", dbPath, storeErr)
	}
	if s == nil {
		defer storeCleanup()
		tui.SetUsageRepo(nil)
	} else {
		tui.SetUsageRepo(s.Usage())
	}
	agentLoop := agent.NewAgentLoopWithStore(cfg, msgBus, s)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		if err := agentLoop.Run(ctx); err != nil {
			fmt.Printf("Agent loop error: %v\n", err)
		}
	}()

	sessionMgr := agentLoop.SessionManager()
	if sessionMgr == nil {
		fmt.Println("Error: session manager not initialized")
		os.Exit(1)
	}

	tuiModel := tui.NewModel(cfg.Snapshot(), agentLoop, sessionMgr, sessionID)
	defer shutdownTUI(tuiModel, cancel, agentLoop)
	if err := tuiModel.StartKeepAwakeIfEnabled(); err != nil {
		logger.WarnC("tui", err.Error())
	}

	restoreAutowrap := tui.DisableAutowrap(tuiTerminalOutput())
	defer restoreAutowrap()

	p := tea.NewProgram(tuiModel, tea.WithAltScreen())
	finalModel, err := p.Run()
	restoreAutowrap()
	if err != nil {
		fmt.Printf("Error running TUI: %v\n", err)
		shutdownTUI(tuiModel, cancel, agentLoop)
		os.Exit(1)
	}

	shutdownTUI(tuiModel, cancel, agentLoop)

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

func tuiTerminalOutput() io.Writer {
	fi, err := os.Stdout.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return nil
	}
	return os.Stdout
}
