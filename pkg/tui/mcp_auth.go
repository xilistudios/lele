package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// This file is the TUI face of pkg/mcp's interactive OAuth 2.1 engine
// (pkg/mcp/auth.go): ModalMCPAuth starts a flow (and launches the
// authorization URL in the user's browser), shows that URL, polls the flow to
// completion, accepts a manually pasted code and cancels cleanly. It talks to
// pkg/mcp DIRECTLY (no REST), like the rest of this file set does.
//
// Three rules shape it:
//
//  1. Update NEVER BLOCKS. Everything that reads the disk (Discover) or the
//     network (StartAuth, Submit) runs inside a tea.Cmd goroutine and reports
//     back with a message. Those goroutines never call i18n.T: the messages
//     carry sentinel errors and the Update handlers translate them (the
//     mcpToggleResultMsg precedent).
//
//  2. NOTHING IS ORPHANED. The engine flow owns a loopback listener until it
//     is cancelled, so every exit — ESC, another /mcp (resetModal), a message
//     that arrives after the modal moved on — must cancel it. mcpAuthCancel is
//     the single release point and mcpAuthSeq is the generation counter that
//     makes a late message a no-op instead of a leak.
//
//  3. NO SECRET IS KEPT. A pasted code is cleared from the input the moment it
//     is parsed and is never carried in a message; the engine's Submit is
//     called with it and its (value-free) error is the only thing that comes
//     back.

// ── Modal state machine ────────────────────────────────────────────────

// The ModalMCPAuth states:
//
//	mcpAuthIdle     no flow (never opened, cancelled or closed)
//	mcpAuthStarting the start command is in flight (URL not known yet)
//	mcpAuthPending  the flow is live: URL on screen, ENTER submits, ticking
//	mcpAuthSucceeded the flow completed; the list was reloaded
//	mcpAuthFailed   the flow was refused or failed (terminal)
const (
	mcpAuthIdle int = iota
	mcpAuthStarting
	mcpAuthPending
	mcpAuthSucceeded
	mcpAuthFailed
)

// mcpAuthStatus* mirror the three strings AuthFlow.Status reports ("pending" |
// "done" | "error"). pkg/mcp keeps its own copies unexported, so these are the
// documented values, spelled once.
const (
	mcpAuthStatusPending = "pending"
	mcpAuthStatusDone    = "done"
	mcpAuthStatusError   = "error"
)

// mcpAuthPollInterval is how often a live flow is re-checked while pending.
const mcpAuthPollInterval = time.Second

// mcpAuthModalOverhead is what ModalContainer spends on rows that carry no
// body: the top and bottom border rows plus its vertical padding.
const mcpAuthModalOverhead = 4

// ── Async messages ─────────────────────────────────────────────────────

// mcpAuthStartMsg is the result of one start attempt (mcpAuthStartCmd). err is
// either nil, one of pkg/mcp's sentinels, or one of this file's own
// errMCPAuth* sentinels: the Update handler localizes them, so the goroutine
// that produces this message never touches i18n.
type mcpAuthStartMsg struct {
	seq  int // mcpAuthSeq generation this attempt belongs to
	name string
	flow mcpAuthFlow
	err  error
	// openErr is what the browser opener reported for the authorization URL
	// (nil when no opener ran or it launched fine). It is NOT a flow failure:
	// the URL stays on screen and CTRL+Y still copies it, so the modal only
	// uses it to explain WHY no browser appeared.
	openErr error
}

// mcpAuthPollMsg is one poll tick of a live flow. seq identifies the flow it
// was scheduled for: a tick of a flow that is gone is dropped and NEVER
// re-armed.
type mcpAuthPollMsg struct{ seq int }

// mcpAuthSubmitMsg is the result of one manual completion (ENTER). It carries
// the engine's error only — never the code that produced it.
type mcpAuthSubmitMsg struct {
	seq int
	err error
}

// TUI-local sentinels carried by the messages above (translated in Update).
//
// The manual-paste outcomes are NOT here: they are the engine's own
// (mcp.ErrAuthPaste*), because the rule that produces them is the engine's
// (see the "Manual input parsing" section).
var (
	// errMCPAuthNoServer: the server is absent from Discover.Servers, i.e. it
	// is disabled or invalid — there is no definition to authorize through.
	errMCPAuthNoServer = errors.New("mcp auth: server is not a discoverable active server")
)

// ── Engine seams ───────────────────────────────────────────────────────

// mcpAuthFlow is the subset of *mcp.AuthFlow the modal drives. It is an
// interface (rather than the concrete type) only so startMCPAuthFlow can be
// substituted in tests: *mcp.AuthFlow satisfies it as-is, no adapter needed.
//
// RedirectURI() is deliberately NOT part of it: the modal shows and copies the
// authorization URL, and the redirect URI is the engine's business (it is the
// bound loopback port or the configured override).
//
// Done() IS part of it because Status()'s "error" is ambiguous by design: a
// failed token exchange records "error" while leaving the flow OPEN and
// retryable, and only Done() tells that apart from the terminal failures
// (cancel, timeout, a one-shot callback exchange failure) — see the AuthFlow
// doc. Without it a poll would declare a retryable failure terminal and hide
// the retry the engine still supports.
type mcpAuthFlow interface {
	URL() string
	Status() (string, error)
	Done() bool
	Submit(ctx context.Context, code, state string) error
	Cancel() error
}

// startMCPAuthFlow is the seam through which the modal starts the engine, so
// tests can drive the whole modal without a network (the engine probes and
// registers against the real server before it returns). Substituting it is the
// documented way to exercise the start path in package tests.
var startMCPAuthFlow = func(ctx context.Context, name string, srv mcp.ServerConfig, store *mcp.AuthStore, opts mcp.AuthOptions) (mcpAuthFlow, error) {
	return mcp.StartAuth(ctx, name, srv, store, opts)
}

// mcpAuthOpenURL launches the authorization URL in the user's browser. The
// engine never spawns a process itself: it takes an opener (AuthOptions.OpenURL)
// so the CALLER decides, which is exactly what makes this substitutable in
// tests and what the CLI already does (cmd/lele/mcp.go, with --no-browser as a
// single nil).
//
// The TUI used to hand the engine a nil opener on the grounds that a remote
// terminal has no browser — but that made the common case (a local terminal)
// needlessly manual. The browser is now launched automatically AND the URL is
// still displayed and copyable (CTRL+Y), so a headless or SSH session loses
// nothing: the launch simply fails, the modal says so, and the copy path stays.
var mcpAuthOpenURL = auth.OpenBrowser

// ── Entry point: "a" in ModalMCPDetail ─────────────────────────────────

// mcpAuthStartFromDetail starts the authorization of the server the detail
// modal is showing. It is a no-op for anything that is not a remote entry: the
// refusal is inline (mcpAuthError, rendered by renderMCPDetail) and NO flow is
// started. On success the modal has already switched to ModalMCPAuth by the
// time this returns; the blocking half (Discover + StartAuth) is the returned
// command.
func (m *Model) mcpAuthStartFromDetail() tea.Cmd {
	name := m.mcpDetailName
	w, _, ok := m.mcpWinnerRow(name)
	if !ok {
		// No inventory snapshot to say whether the entry is remote: the
		// detail is about to fall back to the list. Nothing to start, and
		// starting anyway could authorize a server the user cannot see.
		return nil
	}
	s := w.Summary
	if s.Kind != "remote" && s.URL == "" {
		m.mcpAuthError = fmt.Sprintf(i18n.T("tui.mcpAuthNotApplicable"), name)
		return nil
	}

	m.mcpAuthSeq++ // invalidates every message of a previous attempt
	m.mcpAuthName = name
	m.mcpAuthState = mcpAuthStarting
	m.mcpAuthFlow = nil
	m.mcpAuthURL = ""
	m.mcpAuthFeedback = ""
	m.mcpAuthError = ""
	// The input is reused by this modal as the paste area for the code. Focus
	// is not cosmetic: textinput.Update DROPS every key while blurred, so
	// without it the pasted code would never reach the widget (the shared
	// input is left blurred by the modals that Blur it on close).
	m.textInput.SetValue("")
	m.textInput.Placeholder = i18n.T("tui.mcpAuthPlaceholder")
	m.syncTextInputEcho()
	m.textInput.Focus()
	m.modalMode = ModalMCPAuth
	return m.mcpAuthStartCmd(name)
}

// mcpAuthStartCmd builds the command that resolves the server's winning
// ServerConfig and starts its flow. Discover reads the three mcp.json layers,
// so it runs in the command goroutine; the paths are resolved HERE (in Update)
// and captured by value, because m.mcpPaths() reads Model fields and a command
// goroutine runs concurrently with Update — reading them there would be a data
// race (and every Model field the closure needs is captured, never read).
func (m *Model) mcpAuthStartCmd(name string) tea.Cmd {
	paths, ok := m.mcpPaths()
	seq := m.mcpAuthSeq
	if !ok {
		return func() tea.Msg {
			return mcpAuthStartMsg{seq: seq, name: name, err: errMCPUnavailable}
		}
	}
	return func() tea.Msg {
		store := mcp.NewAuthStore(paths.LeleDir)
		disc, _ := mcp.Discover(paths) // warnings are for logs, the result is usable
		srv, found := disc.Servers[name]
		if !found {
			// Disabled and invalid entries are excluded from Servers: there is
			// no active definition this flow could authorize through.
			return mcpAuthStartMsg{seq: seq, name: name, err: errMCPAuthNoServer}
		}
		// The opener is INJECTED by the engine's contract (StartAuth never
		// spawns a process itself). It runs inside the engine's pre-flow phase
		// — synchronously, on THIS goroutine — so recording its outcome in a
		// local costs no lock and no race: openErr is written by the opener
		// and read below, after StartAuth returned.
		var openErr error
		opener := func(u string) error {
			openErr = mcpAuthOpenURL(u)
			return openErr
		}
		flow, err := startMCPAuthFlow(context.Background(), name, srv, store, mcp.AuthOptions{OpenURL: opener})
		if err != nil {
			return mcpAuthStartMsg{seq: seq, name: name, err: err}
		}
		return mcpAuthStartMsg{seq: seq, name: name, flow: flow, openErr: openErr}
	}
}

// mcpAuthSubmitCmd exchanges the pasted code for a token inside a command:
// Submit performs the network round-trip and must never run in Update. The
// flow and the generation are captured now, so the goroutine touches no Model
// field. The code itself travels as an argument and is never carried back.
func (m *Model) mcpAuthSubmitCmd(code, state string) tea.Cmd {
	flow, seq := m.mcpAuthFlow, m.mcpAuthSeq
	return func() tea.Msg {
		return mcpAuthSubmitMsg{seq: seq, err: flow.Submit(context.Background(), code, state)}
	}
}

// mcpAuthPollCmd schedules the next status check of the live flow.
func (m *Model) mcpAuthPollCmd() tea.Cmd {
	seq := m.mcpAuthSeq
	return tea.Tick(mcpAuthPollInterval, func(time.Time) tea.Msg {
		return mcpAuthPollMsg{seq: seq}
	})
}

// ── Message handlers (i18n happens here, never in the goroutines) ──────

// mcpAuthSentinelText localizes the engine's fixed sentinels. "" means the
// error is not one of them. (The engine guarantees no value ever appears in an
// error, so an unmapped error is safe to print — see mcpAuth*ErrorText.)
//
// EVERY sentinel a flow can report must appear here: an unmapped one falls
// through to the engine's ENGLISH sentence, which is exactly what leaked into
// es/pt while ErrAuthProbeFailed was missing.
func mcpAuthSentinelText(err error) string {
	switch {
	case errors.Is(err, mcp.ErrAuthNotSupported):
		return i18n.T("tui.mcpAuthErrNotSupported")
	case errors.Is(err, mcp.ErrAuthNotRequired):
		return i18n.T("tui.mcpAuthErrNotRequired")
	case errors.Is(err, mcp.ErrAuthStoreDisabled):
		return i18n.T("tui.mcpAuthErrStoreDisabled")
	case errors.Is(err, mcp.ErrAuthProbeFailed):
		// The transport/probe sentinel: the server never completed a
		// handshake, so it is dead, unreachable or misaddressed — NOT a server
		// that needs no login, and NOT "authentication required". The engine's
		// own text for this one is an English sentence, and it is the only
		// sentinel an unreachable server reports (a HANGING one becomes
		// ErrAuthTimedOut instead), so it must be mapped here or es/pt users
		// read English.
		return i18n.T("tui.mcpAuthErrProbeFailed")
	case errors.Is(err, mcp.ErrAuthCanceled):
		return i18n.T("tui.mcpAuthErrCanceled")
	case errors.Is(err, mcp.ErrAuthTimedOut):
		return i18n.T("tui.mcpAuthErrTimedOut")
	}
	return ""
}

// mcpAuthStartErrorText renders a StartAuth failure. The two TUI-local
// sentinels name the server; the engine's own errors are localized when they
// are one of its sentinels and fall back to their (value-free) text otherwise.
func mcpAuthStartErrorText(name string, err error) string {
	switch {
	case errors.Is(err, errMCPUnavailable):
		return i18n.T("tui.mcpUnavailable")
	case errors.Is(err, errMCPAuthNoServer):
		return fmt.Sprintf(i18n.T("tui.mcpAuthServerUnknown"), name)
	}
	if t := mcpAuthSentinelText(err); t != "" {
		return t
	}
	return fmt.Sprintf(i18n.T("tui.mcpAuthStartFailed"), err)
}

// mcpAuthSubmitErrorText renders a Submit failure: the same sentinel mapping,
// then the engine's own value-free string (pkg/mcp never puts a code, a token
// or a secret in an error).
func mcpAuthSubmitErrorText(err error) string {
	if t := mcpAuthSentinelText(err); t != "" {
		return t
	}
	return fmt.Sprintf(i18n.T("tui.mcpAuthSubmitFailed"), err)
}

// mcpAuthTerminalErrorText renders the error a TERMINAL flow reports: an
// unknown one is reduced to the fixed "authorization failed" line rather than
// echoed, because a terminal error is displayed, not retried.
func mcpAuthTerminalErrorText(err error) string {
	if t := mcpAuthSentinelText(err); t != "" {
		return t
	}
	return i18n.T("tui.mcpAuthFailed")
}

// mcpAuthInputErrorText renders the manual-paste failures. The TUI keeps NO
// copy of the paste rule: the outcomes are the engine's canonical ones
// (mcp.ParseAuthPaste) and only the WORDING lives here, one i18n key per
// outcome. A refusal is the only outcome that names something outside the
// paste: the authorization server's own error code, and only when the engine's
// sanitizer kept it (Code == "" means "nothing safe to echo").
func mcpAuthInputErrorText(err error) string {
	switch {
	case errors.Is(err, mcp.ErrAuthPasteEmpty):
		return i18n.T("tui.mcpAuthEmptyCode")
	case errors.Is(err, mcp.ErrAuthPasteUnrecognized), errors.Is(err, mcp.ErrAuthPasteNoCode):
		// One sentence for both, which the grammar explicitly allows: both
		// mean "there is no authorization code in what you pasted".
		return i18n.T("tui.mcpAuthBadInput")
	}
	var denied *mcp.AuthPasteDeniedError
	if errors.As(err, &denied) {
		if denied.Code == "" {
			return i18n.T("tui.mcpAuthRefusedUnknown")
		}
		return fmt.Sprintf(i18n.T("tui.mcpAuthRefused"), denied.Code)
	}
	return i18n.T("tui.mcpAuthBadInput")
}

// handleMCPAuthStart adopts the flow the start command produced.
func (m *Model) handleMCPAuthStart(msg mcpAuthStartMsg) tea.Cmd {
	if msg.seq != m.mcpAuthSeq || m.modalMode != ModalMCPAuth || m.mcpAuthState != mcpAuthStarting {
		// The flow appeared after the modal moved on (ESC, another /mcp, a
		// different modal): release it HERE. Dropping it would pin its
		// loopback listener for the whole five-minute budget.
		if msg.flow != nil {
			_ = msg.flow.Cancel()
		}
		return nil
	}
	if msg.err != nil {
		m.mcpAuthState = mcpAuthFailed
		m.mcpAuthError = mcpAuthStartErrorText(msg.name, msg.err)
		return nil
	}
	m.mcpAuthFlow = msg.flow
	m.mcpAuthURL = msg.flow.URL()
	m.mcpAuthState = mcpAuthPending
	m.mcpAuthFeedback = ""
	m.mcpAuthError = ""
	// The browser was launched inside the engine's pre-flow phase: report the
	// outcome without making it fatal. A failed launch is NOT an authorization
	// failure — the URL is on screen and CTRL+Y copies it — so it lands on the
	// inline (non-terminal) line, while the copy hint stays advertised below.
	if msg.openErr != nil {
		m.mcpAuthError = i18n.T("tui.mcpAuthBrowserFailed")
	} else {
		m.mcpAuthFeedback = i18n.T("tui.mcpAuthBrowserOpened")
	}
	return m.mcpAuthPollCmd()
}

// handleMCPAuthPoll re-checks a live flow. A tick that arrives for a flow that
// is gone is dropped.
//
// The three outcomes:
//
//   - "done" (or a Submit that already finished) is the one-shot success;
//   - "error" WITH Done() is terminal (cancel, timeout, a callback exchange
//     that can no longer be retried): the modal fails and the tick chain stops;
//   - "error" WITHOUT Done() is a RETRYABLE token-exchange failure: the flow is
//     still open and ENTER is still valid, so the modal stays pending with the
//     engine's message and keeps ticking (the eventual timeout turns into a
//     Done() error and is reported then).
//
// "pending" and the retryable branch are the only ones that re-arm the tick, so
// the chain is alive exactly while the flow is still actionable.
func (m *Model) handleMCPAuthPoll(msg mcpAuthPollMsg) tea.Cmd {
	if msg.seq != m.mcpAuthSeq || m.modalMode != ModalMCPAuth ||
		m.mcpAuthState != mcpAuthPending || m.mcpAuthFlow == nil {
		return nil
	}
	status, err := m.mcpAuthFlow.Status()
	switch status {
	case mcpAuthStatusDone:
		m.mcpAuthFinishDone()
		return nil
	case mcpAuthStatusError:
		if m.mcpAuthFlow.Done() {
			m.mcpAuthState = mcpAuthFailed
			m.mcpAuthError = mcpAuthTerminalErrorText(err)
			return nil
		}
		// Retryable: keep the affordance, keep watching.
		m.mcpAuthError = mcpAuthSubmitErrorText(err)
	}
	return m.mcpAuthPollCmd()
}

// handleMCPAuthSubmit applies the outcome of a manual completion.
//
// A failure keeps the modal in mcpAuthPending: the engine records the failure
// but leaves the flow OPEN (a token-exchange failure is retryable — Done()
// stays false), so ENTER can be pressed again with a fresh code without
// restarting anything. The still-ticking poll sees the same "error" but reads
// Done() and therefore keeps the modal retryable too (see handleMCPAuthPoll)
// instead of flipping it to the terminal failed state.
func (m *Model) handleMCPAuthSubmit(msg mcpAuthSubmitMsg) tea.Cmd {
	if msg.seq != m.mcpAuthSeq || m.modalMode != ModalMCPAuth {
		return nil
	}
	if msg.err != nil {
		m.mcpAuthError = mcpAuthSubmitErrorText(msg.err)
		return nil
	}
	m.mcpAuthFinishDone()
	return nil
}

// mcpAuthFinishDone performs the ONE-SHOT "the flow succeeded" transition: it
// marks the modal done, writes the feedback line, re-wires the agent loop and
// reloads the list. It reports whether it fired.
//
// The one-shot guard is what makes "SyncMCPServers ran exactly once per flow"
// true no matter HOW MANY success observations arrive: a successful manual
// ENTER and a loopback-driven poll can both land in the same generation (the
// poll that is already in flight when the user submits, a duplicated delivery
// of the submit result), and the callers' own "still pending?" checks are not
// enough — handleMCPAuthSubmit accepts any message of the current generation.
func (m *Model) mcpAuthFinishDone() bool {
	if m.mcpAuthState != mcpAuthPending {
		return false
	}
	m.mcpAuthState = mcpAuthSucceeded
	m.mcpAuthError = ""
	m.mcpAuthFeedback = fmt.Sprintf(i18n.T("tui.mcpAuthSuccess"), m.mcpAuthName)
	// mcp.json is not config-watched: this pass is what makes the freshly
	// stored credential visible to the tool/prompt surface (same call the
	// toggle path makes).
	syncMCPServersFor(m.agentLoop)
	m.loadMCPList()
	return true
}

// mcpAuthCancel releases the live flow and clears every auth field. It is the
// ONE release point (ESC, /mcp, resetModal): the flow owns a loopback listener
// until Cancel, so forgetting this is a leak of a pinned port for the flow's
// whole budget. The generation bump invalidates every in-flight message, so a
// late start message cancels itself instead of being adopted.
func (m *Model) mcpAuthCancel() {
	hadFlow := m.mcpAuthFlow != nil
	if hadFlow {
		_ = m.mcpAuthFlow.Cancel()
		m.mcpAuthFlow = nil
	}
	m.mcpAuthSeq++
	// The paste area may still hold an unparsed authorization code (the user
	// pasted it and cancelled): a credential-shaped value must not survive in
	// a widget that the next modal reuses. Blur also drops the cursor.
	//
	// This is done ONLY when an authorization was actually in play
	// (mcpAuthCancel is also reached from resetModal, i.e. on every modal
	// open): blurring the shared input unconditionally would silently break
	// the form modals that expect it focused.
	if hadFlow || m.mcpAuthState != mcpAuthIdle {
		m.textInput.SetValue("")
		m.textInput.Placeholder = ""
		m.textInput.Blur()
	}
	m.mcpAuthState = mcpAuthIdle
	m.mcpAuthName = ""
	m.mcpAuthURL = ""
	m.mcpAuthFeedback = ""
	m.mcpAuthError = ""
}

// ── Keys ───────────────────────────────────────────────────────────────

// handleMCPAuthKey owns the keys of ModalMCPAuth: ENTER submits the pasted
// code, CTRL+Y copies the authorization URL, ESC cancels the flow and returns
// to the DETAIL (the screen the flow was started from). Every other key is
// passed through (handled=false) so it reaches the shared text input: that is
// exactly why isFormModal reports ModalMCPAuth as a form modal.
//
// "q" is one of those other keys — a REGULAR CHARACTER, not a shortcut. The
// widget holds a pasted authorization code or redirect URL, and both may
// legitimately contain a "q"; cancelling on it would abort a live flow and
// free its loopback port in the middle of a paste. This is the rule
// handlers_modal.go states for every form modal ("q" must not close
// form-based modals); ESC is the only cancel key here, and it is the one the
// hint advertises.
func (m *Model) handleMCPAuthKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		m.mcpAuthCancel()
		m.modalMode = ModalMCPDetail
		return nil, true

	case "enter":
		if m.mcpAuthState != mcpAuthPending || m.mcpAuthFlow == nil {
			// Nothing to submit yet (the flow is still starting) or anymore
			// (terminal): swallow the key, no engine call.
			return nil, true
		}
		code, state, err := mcp.ParseAuthPaste(m.textInput.Value())
		if err != nil {
			// Nothing is submitted: the engine parser answers ("", "") with
			// every error, so there is no fabricated code to feed the flow.
			m.mcpAuthError = mcpAuthInputErrorText(err)
			return nil, true
		}
		// A pasted code is credential-shaped: drop it from the widget as soon
		// as it has been parsed (it travels to the engine as an argument and
		// never comes back in a message).
		m.textInput.SetValue("")
		m.mcpAuthError = ""
		m.mcpAuthFeedback = ""
		return m.mcpAuthSubmitCmd(code, state), true

	case "ctrl+y":
		if m.mcpAuthURL == "" {
			m.mcpAuthError = i18n.T("tui.mcpAuthCopyFailed")
			return nil, true
		}
		// Same helper (and the same "copied / nothing to copy" reporting) the
		// chat's CTRL+Y uses.
		copyToClipboard(m.mcpAuthURL)
		m.mcpAuthError = ""
		m.mcpAuthFeedback = i18n.T("tui.mcpAuthCopied")
		return nil, true
	}
	return nil, false
}

// ── Manual input parsing ───────────────────────────────────────────────

// The paste grammar lives in exactly ONE place: pkg/mcp.ParseAuthPaste, the
// engine's canonical implementation of the frozen rule set
// (.lele/tasks/mcp-oauth-paste-grammar.md: the full redirect URL, the bare
// query string, the bare code and the two-token "code state" paste; a refusal
// URL, a returned `error` parameter, a bare code outside the base64url alphabet
// and a parameter-shaped paste holding none of code/state/error are refused).
//
// This file used to carry a SECOND copy of that rule, and a client review
// proved the copies DISAGREED: the same paste was submitted by one surface,
// refused by another, or submitted as a different code — this copy had no
// code-shape check at all, so `!!!` or a page of browser prose was POSTed to
// the token endpoint as the authorization code instead of reaching the local
// "Could not read an authorization code from that paste" sentence the CLI and
// the WebUI already reached. There is deliberately no private parser here any
// more: the submit site calls the engine helper and only the WORDING of an
// outcome is the TUI's (mcpAuthInputErrorText).
//
// The engine's contract is what makes the delegation safe: on EVERY error the
// returned code and state are "", so a refused paste can never be submitted as
// a fabricated code — the flow is simply not fed.

// ── Rendering ──────────────────────────────────────────────────────────

// mcpAuthErrorStyle is the style of the inline error line (the form modals use
// the same foreground for their "✗" line).
var mcpAuthErrorStyle = lipgloss.NewStyle().Foreground(PrimaryColor)

// mcpAuthFeedbackLine renders the inline line(s) shared by the detail and the
// auth modal: the informational one (URL copied, flow finished) in the success
// style, the error one (refusal, engine failure) with the "✗" marker. "" when
// there is nothing to show.
func (m *Model) mcpAuthFeedbackLine() string {
	var sb strings.Builder
	if m.mcpAuthFeedback != "" {
		sb.WriteString(SuccessStyle.Render("  "+m.mcpAuthFeedback) + "\n")
	}
	if m.mcpAuthError != "" {
		sb.WriteString(mcpAuthErrorStyle.Render("  ✗ " + m.mcpAuthError))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// mcpAuthURLWidth is the cell budget of one wrapped URL line: the modal is
// rendered at m.width-10 (the detail's convention) and ModalContainer spends
// six more columns on its border and horizontal padding, with a small margin
// so a wide rune at the cut point cannot overflow the box.
func (m *Model) mcpAuthURLWidth() int {
	w := m.width - 16
	if w < 20 {
		w = 20
	}
	return w
}

// hardWrapCells splits s into lines of at most cells display columns, breaking
// INSIDE a word. An authorization URL has no spaces, so the word-based
// wrapText would leave it on one oversized line.
func hardWrapCells(s string, cells int) []string {
	if cells <= 0 {
		return []string{s}
	}
	var out []string
	var cur strings.Builder
	width := 0
	forEachGraphemeCluster(s, func(cluster string, w int) {
		if width > 0 && width+w > cells {
			out = append(out, cur.String())
			cur.Reset()
			width = 0
		}
		cur.WriteString(cluster)
		width += w
	})
	if cur.Len() > 0 || len(out) == 0 {
		out = append(out, cur.String())
	}
	return out
}

// mcpAuthBody builds the modal's content (title, state-specific block and the
// shared feedback line) as rendered lines.
func (m *Model) mcpAuthBody() []string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(m.modalTitleFor(ModalMCPAuth)) + "\n")
	sb.WriteString(TitleStyle.Render(m.mcpAuthName) + "\n\n")

	switch m.mcpAuthState {
	case mcpAuthStarting:
		sb.WriteString(CommentColorStyle.Render("  "+i18n.T("tui.mcpAuthStarting")) + "\n")
	case mcpAuthSucceeded, mcpAuthFailed:
		sb.WriteString(m.mcpAuthFeedbackLine() + "\n")
	default: // mcpAuthPending (and idle, which cannot be on screen)
		sb.WriteString(mcpAuthErrorStyle.Render(i18n.T("tui.mcpAuthURLLabel")+":") + "\n")
		for _, line := range hardWrapCells(m.mcpAuthURL, m.mcpAuthURLWidth()) {
			sb.WriteString("  " + line + "\n")
		}
		sb.WriteString("\n" + i18n.T("tui.mcpAuthInstructions") + "\n\n")
		sb.WriteString(m.textInputView() + "\n")
		if line := m.mcpAuthFeedbackLine(); line != "" {
			sb.WriteString("\n" + line + "\n")
		}
	}
	sb.WriteString("\n")
	if m.mcpAuthState == mcpAuthPending {
		// The two actions of the live state, above the shared close hint.
		sb.WriteString(HelpStyle.Render("  "+i18n.T("tui.mcpAuthHintEnter")) + "\n")
		sb.WriteString(HelpStyle.Render("  "+i18n.T("tui.mcpAuthHintCopy")) + "\n")
	}
	// The last hint says what ESC REALLY does in this state — the same split
	// the WebUI makes on the same condition ("Cancel" while the flow is
	// pending, "Close" otherwise). While the flow is live (or still starting)
	// ESC cancels it and releases the loopback listener, so the cancel wording
	// is accurate; once the flow is over (succeeded/failed) Cancel() is a
	// no-op and the key only closes the modal, so promising a cancel there
	// would be the over-promise this split removes.
	hint := i18n.T("tui.mcpAuthHintClose")
	if m.mcpAuthState == mcpAuthSucceeded || m.mcpAuthState == mcpAuthFailed {
		hint = i18n.T("tui.mcpAuthHintClosed")
	}
	sb.WriteString(HelpStyle.Render("  " + hint))
	return strings.Split(sb.String(), "\n")
}

// mcpAuthBox renders the ModalMCPAuth box, height-budgeted HONESTLY: the body
// is truncated so that the container's own rows (border + vertical padding)
// still fit inside the terminal, and the finished box is clamped again as a
// hard guarantee. The detail renderer's lack of such a clamp is a known wart;
// copying it would clip the bottom border mid-frame on a short terminal.
func (m *Model) mcpAuthBox() string {
	lines := m.mcpAuthBody()
	maxBody := m.height - mcpAuthModalOverhead
	if maxBody < 1 {
		maxBody = 1
	}
	if len(lines) > maxBody {
		lines = lines[:maxBody]
	}
	width := m.width - 10
	if width < 20 {
		width = 20
	}
	box := ModalContainer.Width(width).Render(strings.Join(lines, "\n"))
	return clampLineCount(box, m.height)
}

// clampLineCount truncates s to at most max lines (max <= 0 leaves it alone).
func clampLineCount(s string, max int) string {
	if max <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	return strings.Join(lines[:max], "\n")
}

// renderMCPAuth paints ModalMCPAuth into a full frame.
func (m *Model) renderMCPAuth() string {
	return m.paintFrame(m.mcpAuthBox())
}

// ── Auth visibility & sign out ─────────────────────────────────────────
//
// The list and the detail must tell the user where a REMOTE server stands (a
// sign-in is owed / one is stored) and let them take the credential back.
// Both answers come from pkg/mcp's own verbs — CheckAuth and Logout — through
// the SAME in-process store the flow above uses (mcp.NewAuthStore(paths.LeleDir)):
// no REST round-trip, no second way of finding the lele directory, and never a
// token, a client secret or any other record field in a rendered line.

// mcpAuthVerdicts resolves the auth verdict of every name of an inventory.
//
// The config comes from Discover — the same layer precedence and ${VAR}
// expansion the manager dials — and NOT from the inventory row: an
// EntryState.Summary is a display slice that never carries the auth block.
// Disabled and invalid names are excluded by Discover and therefore get no
// entry at all: an inert row must not advertise an auth state.
//
// A store that is not Enabled() (an empty LeleDir) yields an EMPTY map and no
// error: there is nothing to report, and the /mcp modal must keep working —
// the zero verdict renders no marker and the sign-out then refuses with
// errMCPAuthUnavailableStore.
func mcpAuthVerdicts(paths mcp.Paths, inv mcp.Inventory) map[string]mcp.AuthStatus {
	verdicts := make(map[string]mcp.AuthStatus)
	if len(inv.Winner) == 0 {
		return verdicts
	}
	store := mcp.NewAuthStore(paths.LeleDir)
	if !store.Enabled() {
		return verdicts
	}
	// Discover's warning error is ignored here, exactly like mcpAuthStartCmd
	// ignores it: warnings belong to the loaders and the logs, never to the
	// modal — its RESULT is what the modal needs, and the entries it could
	// read still get a verdict.
	disc, _ := mcp.Discover(paths)
	for name := range inv.Winner {
		srv, ok := disc.Servers[name]
		if !ok {
			continue
		}
		verdicts[name] = mcp.CheckAuth(store, name, srv)
	}
	return verdicts
}

// mcpAuthSuffix is the list marker of one verdict: "" when there is nothing to
// say (the server needs no auth and has none stored, or it is not a
// discoverable active server). Pure — the i18n lookup happens HERE so
// formatMCPItem stays table-testable.
//
// THE ONE RENDER RULE, shared with the detail block (pkg/tui/mcp.go) and with
// the WebUI's chip (AgentMCPServerRow), so ONE verdict can never render two
// different answers:
//
//	authenticated ⇒ "authenticated";  else required ⇒ "needs auth";  else nothing
//
// Authenticated is checked FIRST because that is what mcp.CheckAuth means by
// it — "a usable credential exists, no sign-in is needed" — and because
// Authenticated is ORTHOGONAL to Required (pkg/mcp/auth.go says so explicitly,
// and buildAuthRuntime anticipates "a client id with AuthRequired false"
// residue). A verdict carrying a token while Required was false used to render
// NOTHING here while the WebUI rendered its "Authenticated" chip for the same
// DTO.
func mcpAuthSuffix(st mcp.AuthStatus) string {
	switch {
	case st.Authenticated:
		return " " + i18n.T("tui.mcpAuthOK")
	case st.Required:
		return " " + i18n.T("tui.mcpAuthRequired")
	default:
		return ""
	}
}

// mcpAuthMethodLabel names the carrier of the credential for the detail's
// "Auth" line. Method is "" for a server that answered 401 without declaring
// any carrier (AuthStatus documents why): the line still says something honest
// instead of inventing "oauth".
func mcpAuthMethodLabel(method string) string {
	if method == "" {
		return i18n.T("tui.mcpDetailNone")
	}
	return method
}

// errMCPAuthUnavailableStore: the sign-out has no credential store to act on
// (the paths resolved to an empty LeleDir). A nil/disabled store is not an
// error the user can act on, and pkg/mcp's ErrAuthStoreDisabled would surface
// as an engine message ("MCP authorization storage is unavailable") rather than
// as the "nothing to sign out of here" refusal this is.
var errMCPAuthUnavailableStore = errors.New("mcp auth: no credential store available")

// errMCPAuthNoCredential: the entry has nothing stored to sign out of. The
// store's Clear is idempotent (deleting an absent record is not an error), so
// without this check the TUI would report "signed out" for a server it never
// touched — a success line for a deletion that never happened.
var errMCPAuthNoCredential = errors.New("mcp auth: no stored credential")

// mcpAuthLogoutResult is the result of one sign-out, carried to update() as a
// mcpToggleResultMsg (see the doc of that type: it is the single MCP mutation
// result wired into the dispatch, and both mutations end with the same tail).
func mcpAuthLogoutResult(name string, err error) mcpToggleResultMsg {
	return mcpToggleResultMsg{name: name, logout: true, err: err}
}

// mcpAuthLogoutCmd builds the command that signs one server out: the paths are
// resolved NOW (cheap, no I/O, in Update) and captured by value, exactly like
// mcpAuthStartCmd — a command goroutine must not read Model fields.
func (m *Model) mcpAuthLogoutCmd(name string) tea.Cmd {
	paths, ok := m.mcpPaths()
	if !ok {
		return func() tea.Msg {
			return mcpAuthLogoutResult(name, errMCPUnavailable)
		}
	}
	return mcpAuthLogoutCmdFor(paths, name)
}

// mcpAuthLogoutCmdFor signs name out of the store at paths.LeleDir inside a
// command: Logout re-reads and rewrites mcp-auth.json, which must not run in
// Update (the mcpToggleCmdFor rule).
//
// The credential is looked up BEFORE the delete, because mcp.Logout is
// idempotent and cannot tell "deleted" from "there was nothing": an entry with
// no stored token is refused with errMCPAuthNoCredential (a clear refusal,
// never a silent no-op and never a false success). Only the PRESENCE of a
// record is inspected — no field of it is read out, rendered or logged.
func mcpAuthLogoutCmdFor(paths mcp.Paths, name string) tea.Cmd {
	return func() tea.Msg {
		store := mcp.NewAuthStore(paths.LeleDir)
		if !store.Enabled() {
			return mcpAuthLogoutResult(name, errMCPAuthUnavailableStore)
		}
		if rec, ok := store.Record(name); !ok || rec.Token == nil {
			return mcpAuthLogoutResult(name, errMCPAuthNoCredential)
		}
		return mcpAuthLogoutResult(name, mcp.Logout(store, name))
	}
}

// handleMCPAuthLogoutResult applies one sign-out.
//
// On success it re-wires the agent loop (the token store is not config-watched
// either: that pass is what makes "logged out" visible to the live connection,
// the same call the toggle path makes) and reloads the list so the row marker
// matches the store again.
//
// The line goes to BOTH feedback surfaces, because a sign-out can be fired from
// the list (which renders m.mcpFeedback) or from the detail (which renders
// mcpAuthFeedbackLine), and the result may even land after the modal moved on.
// One of them is always the screen the user is looking at, so the outcome is
// never invisible.
func (m *Model) handleMCPAuthLogoutResult(name string, err error) tea.Cmd {
	var line string
	switch {
	case err == nil:
		line = fmt.Sprintf(i18n.T("tui.mcpAuthLogoutDone"), name)
		syncMCPServersFor(m.agentLoop)
	case errors.Is(err, errMCPUnavailable):
		line = i18n.T("tui.mcpUnavailable")
	case errors.Is(err, errMCPAuthUnavailableStore):
		line = i18n.T("tui.mcpAuthUnavailable")
	case errors.Is(err, errMCPAuthNoCredential):
		line = fmt.Sprintf(i18n.T("tui.mcpAuthLogoutNone"), name)
	default:
		line = fmt.Sprintf(i18n.T("tui.mcpAuthLogoutFailed"), name, err)
	}
	m.mcpFeedback = line
	if m.modalMode == ModalMCPDetail {
		m.mcpAuthFeedback, m.mcpAuthError = "", line
		if err == nil {
			m.mcpAuthFeedback, m.mcpAuthError = line, ""
		}
	}
	// Always reload: after a failure the store is untouched and the marker must
	// keep saying exactly that.
	m.loadMCPList()
	return m.tickCmd()
}
