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

	// Open the shared SQLite store via the single helper.
	dbPath := defaultDBPath()
	s, storeCleanup, storeErr := openSharedStore(dbPath, "tui")
	if storeErr != nil {
		fmt.Fprintf(os.Stderr, "Failed to open SQLite store at %s: %v — falling back to JSON storage\n", dbPath, storeErr)
	}
	if s == nil {
		defer storeCleanup()
	}
	agentLoop := agent.NewAgentLoopWithStore(cfg, msgBus, s)

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
	// The TUI mutates its config copy (providers, agents list) while agent
	// goroutines read through the loop's atomic pointer, so it must own a
	// private deep copy — passing `cfg` here reintroduces the data race.
	tuiModel := tui.NewModel(cfg.Snapshot(), agentLoop, sessionMgr, sessionID)
	// The inhibition is bound to the model, not to a screen: it must be
	// released however the program returns (ctrl+c, /quit, render error).
	defer tuiModel.Close()
	if err := tuiModel.StartKeepAwakeIfEnabled(); err != nil {
		// No "keep-awake: " prefix here: pkg/keepawake's starter already
		// prefixes its messages (see keepawake.go), so adding one logs a
		// doubled "keep-awake: keep-awake: …".
		logger.WarnC("tui", err.Error())
	}

	// Run bubbletea program in AltScreen mode.
	// Mouse capture starts ON for scroll-wheel and sidebar click support.
	// Users can hold Shift while clicking/dragging to bypass mouse capture
	// and use native terminal text selection. ctrl+t toggles mouse off as
	// a fallback for terminals that don't support Shift bypass.
	p := tea.NewProgram(tuiModel, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Printf("Error running TUI: %v\n", err)
		// os.Exit skips every defer, including the keep-awake release above:
		// release explicitly here so a render error really fulfils the
		// "ctrl+c, /quit or render error" promise. Close is idempotent, so
		// calling it here and again through the deferred path is harmless.
		tuiModel.Close()
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
