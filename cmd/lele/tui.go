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

// tuiExitJoinGrace bounds the last join on in-flight agent work after the TUI
// closes. It is a safety net, not the expected path: cancelling the root
// context (see shutdownTUI) is what ends a running turn, and a turn that
// observes cancellation unwinds in milliseconds.
//
// It only matters for work that ignores cancellation entirely — a blocking tool
// call, a subagent running on its own context. Waiting for that is pointless
// when the process is leaving, so the grace expires, the teardown is abandoned
// (the store is left open and SQLite recovers the WAL on the next start) and
// the terminal comes back.
//
// A var (not a const) so tests can shrink it, matching finalTurnJoinGrace.
var tuiExitJoinGrace = 2 * time.Second

// tuiCloser is the part of *tui.Model the exit path needs: releasing the
// screen-bound state (the keep-awake inhibition) however the program returned.
// Tests substitute a fake to pin the order of the teardown. The join side of
// the contract is turnJoiner (gateway.go), shared with the gateway's teardown:
// both exit paths wait for in-flight turns the same bounded way.
type tuiCloser interface {
	Close()
}

// shutdownTUI releases everything the TUI owns and hands the terminal back as
// fast as possible. It is idempotent (Close, cancel and StopWithin all are), so
// calling it from the deferred safety net and again on the path that owns the
// exit code is harmless.
//
// Order matters, and it mirrors the gateway's graceful teardown
// (runGracefulShutdown + finalTurnJoinGrace):
//
//  1. Close drops the keep-awake inhibition — no reason to hold the display
//     awake while a join is pending.
//  2. Cancelling the root context is what actually aborts an in-flight turn:
//     the provider stream inherits that context, so the LLM request dies with
//     the TUI. Without it the join below waits out the whole turn (measured:
//     11.5 s to exit during a 15 s streamed response) because the unbounded
//     Stop() joins the turn goroutines.
//  3. The join is bounded so a turn that ignores cancellation cannot hold the
//     shell hostage.
//
// Trade-off, deliberately the same one pressing ESC twice already makes: the
// aborted turn's partial answer is discarded (history keeps the user message).
// That is what "quit now" means; a user who wants the answer keeps the TUI
// open.
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

	// Start agent loop background processing. The context is not deferred for
	// cancellation: shutdownTUI cancels it on every exit path, and cancelling
	// before the join is what makes the exit fast.
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		if err := agentLoop.Run(ctx); err != nil {
			fmt.Printf("Agent loop error: %v\n", err)
		}
	}()

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
	// released however the program returns (ctrl+c, /quit, render error). The
	// same call also aborts in-flight turns and joins them with a bounded
	// grace, so no exit path can leave the shell waiting on a stream.
	defer shutdownTUI(tuiModel, cancel, agentLoop)
	if err := tuiModel.StartKeepAwakeIfEnabled(); err != nil {
		// No "keep-awake: " prefix here: pkg/keepawake's starter already
		// prefixes its messages (see keepawake.go), so adding one logs a
		// doubled "keep-awake: keep-awake: …".
		logger.WarnC("tui", err.Error())
	}

	// Turn the terminal's autowrap off while the TUI owns the screen. A frame
	// line that the terminal measures one cell wider than the TUI does (East
	// Asian "ambiguous" glyphs such as ● or → drawn double width) would wrap
	// and shift every row below it, which the incremental renderer cannot see:
	// it then skips the rows it thinks are unchanged and leaves the previous
	// frame's status line and input placeholder on screen as ghost rows.
	restoreAutowrap := tui.DisableAutowrap(tuiTerminalOutput())
	defer restoreAutowrap()

	// Run bubbletea program in AltScreen mode.
	// Mouse capture starts ON for scroll-wheel and sidebar click support.
	// Users can hold Shift while clicking/dragging to bypass mouse capture
	// and use native terminal text selection. ctrl+t toggles mouse off as
	// a fallback for terminals that don't support Shift bypass.
	p := tea.NewProgram(tuiModel, tea.WithAltScreen())
	finalModel, err := p.Run()
	// Bubbletea has left the alternate screen: give the terminal its autowrap
	// mode back before anything else is printed. The resume hint below is wider
	// than a narrow terminal on purpose (it carries a full command line to copy),
	// and it would be clipped at the right margin while autowrap is off. The
	// deferred call is kept for the early returns between here and the end.
	restoreAutowrap()
	if err != nil {
		fmt.Printf("Error running TUI: %v\n", err)
		// os.Exit skips every defer, including the teardown above: run it
		// explicitly here so a render error still releases the keep-awake
		// inhibition, aborts the turn and closes the store. It is idempotent,
		// so running it again through the deferred path is harmless.
		shutdownTUI(tuiModel, cancel, agentLoop)
		os.Exit(1)
	}

	// Tear the runtime down before the resume hint: the join is the only thing
	// that can keep the shell from coming back, and the hint has to be the last
	// thing the user reads.
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

// tuiTerminalOutput returns the writer the TUI renders to — only when it is a
// real terminal. Escape sequences aimed at a redirected file or pipe would
// corrupt the output, and there is no screen to protect in that case.
func tuiTerminalOutput() io.Writer {
	fi, err := os.Stdout.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return nil
	}
	return os.Stdout
}
