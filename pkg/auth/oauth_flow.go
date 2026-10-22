package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// This file adds a non-blocking OAuth flow engine for TUI callers. It is a
// deliberate mirror of loginBrowser (oauth.go) and of
// loginPasteCodeWithPKCEAndState (oauth_claude.go), with exactly six
// differences:
//
//  1. it never writes to stdout/stderr, never opens a browser and never prints
//     a headless hint (a TUI renders in the alternate screen; the caller
//     decides when/how to open the URL, e.g. from its own goroutine);
//  2. it never blocks: StartProviderFlow returns immediately and all waiting
//     (loopback callback, timeout) happens in a background goroutine; the token
//     exchange runs either through Submit (manual paste) or inside the
//     callback handler itself (loopback), never in the caller;
//  3. requests arriving after a terminal state are answered
//     400 "Login already finished" instead of being queued for a flow that no
//     longer exists;
//  4. manual mode also enforces a timeout (the blocking paste path has none),
//     so a paste box cannot leave a flow pending forever;
//  5. there is no paste-fallback when the callback bind fails or the flow
//     times out: the bind error is returned synchronously from
//     StartProviderFlow and the timeout surfaces as the terminal
//     ErrFlowTimeout status, both of which the TUI renders itself;
//  6. ErrFlowNoManualRedirect fails fast (StartProviderFlow refuses a
//     FlowModeManual preset with no hosted redirect) instead of silently
//     building an empty redirect_uri.
//
// Everything else — PKCE generation, state selection, listener binding order,
// the authorize URL, the callback handler semantics, the exchange params
// (including the State-only-when-StateIsVerifier rule), the paste grammar and
// the one-shot character of the exchange — reuses the package's existing
// helpers so the two flows cannot drift apart.

// FlowMode selects how the authorization code comes back.
type FlowMode int

const (
	// FlowModeLoopback binds a 127.0.0.1 listener that receives the OAuth
	// callback (the loginBrowser redirect).
	FlowModeLoopback FlowMode = iota
	// FlowModeManual uses the provider's hosted redirect (ManualRedirectURI);
	// the user pastes the code/URL back through Submit.
	FlowModeManual
)

// Flow status values returned by Status().
const (
	FlowStatusPending = "pending"
	FlowStatusDone    = "done"
	FlowStatusError   = "error"
)

// Terminal flow errors. They are sentinel values so callers can classify the
// failure with errors.Is; Submit/callback failures (e.g. a token-endpoint
// error) are returned wrapped around these where they apply, or as plain
// errors otherwise.
var (
	// ErrFlowCanceled is the terminal error set by Cancel.
	ErrFlowCanceled = errors.New("oauth: flow canceled")
	// ErrFlowTimeout is the terminal error set when the flow budget elapses
	// before the code arrived (the loginBrowser five-minute budget, configurable).
	ErrFlowTimeout = errors.New("oauth: flow timed out")
	// ErrFlowClosed is returned by Submit once the flow is already terminal:
	// the authorization code is single-use and is exchanged AT MOST ONCE.
	ErrFlowClosed = errors.New("oauth: flow already finished")
	// ErrFlowNoManualRedirect is returned by StartProviderFlow in FlowModeManual
	// for a preset without a hosted redirect (openai, google-antigravity).
	ErrFlowNoManualRedirect = errors.New("oauth: provider has no ManualRedirectURI for the manual flow")
	// ErrFlowNoCode is returned by Submit for an empty/blank paste.
	ErrFlowNoCode = errors.New("oauth: no authorization code entered")
	// ErrFlowStateMismatch is returned when a pasted state (Submit) does not
	// match this flow's state; it is also the terminal error of a loopback
	// callback that carried the wrong state.
	ErrFlowStateMismatch = errors.New("oauth: state mismatch")
)

// FlowOptions configures a ProviderFlow.
type FlowOptions struct {
	// Mode selects the loopback-listener flow (zero value) or the manual
	// paste-code flow.
	Mode FlowMode
	// Timeout is the budget for the flow to receive the authorization code.
	// Zero selects the loginBrowser budget (5 minutes).
	Timeout time.Duration
}

// flowTimeout returns the effective flow budget.
func (o FlowOptions) flowTimeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return 5 * time.Minute
}

// ProviderFlow is a non-blocking OAuth login flow. It is safe for concurrent
// use: Status/Done/Credential/URL/RedirectURI/Mode may be polled (e.g. from a
// bubbletea Update loop), Submit performs the synchronous token exchange and
// Cancel aborts the flow. Exactly one terminal transition ever happens.
type ProviderFlow struct {
	url         string
	redirectURI string
	mode        FlowMode
	pkce        PKCECodes
	state       string
	cfg         OAuthProviderConfig
	timeout     time.Duration

	mu       sync.Mutex
	status   string
	err      error
	cred     *AuthCredential
	exchange bool // exchange attempt claimed (one-shot guard)

	server   *http.Server  // loopback mode only
	listener net.Listener  // loopback mode only; owned by the server
	finished chan struct{} // closed exactly once with the terminal transition
	stopOnce sync.Once     // makes the listener release idempotent
}

// StartProviderFlow starts a non-blocking OAuth login flow and returns
// IMMEDIATELY (it performs no network I/O beyond binding the loopback
// listener). The caller is expected to open f.URL() in a browser itself.
//
// FlowModeLoopback binds the callback listener BEFORE building the authorize
// URL (the redirect_uri must carry the port the OS actually assigned), exactly
// like loginBrowser; the bind error is returned synchronously. FlowModeManual
// does not bind anything and requires cfg.ManualRedirectURI.
//
// ctx is currently IGNORED: nothing in setup is cancellable (the only I/O is
// net.Listen); the lifecycle is owned by Cancel. Passing an already-cancelled
// context does not panic and does not abort the flow: a TUI owns the flow
// lifecycle through Cancel, and silently killing a freshly started login from
// a stale context would be the wrong default.
//
// An out-of-range opts.Mode is rejected rather than silently falling back to
// loopback (binding a listener for a mode the caller did not ask for).
func StartProviderFlow(ctx context.Context, cfg OAuthProviderConfig, opts FlowOptions) (*ProviderFlow, error) {
	if ctx == nil {
		// ctx is ignored (see doc comment); tolerate nil so a caller with no
		// context can start a flow without panicking.
		ctx = context.Background()
	}
	_ = ctx // deliberately unused — see the "ctx is currently IGNORED" contract.

	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("generating PKCE: %w", err)
	}

	// Claude/jcode binds the OAuth `state` to the PKCE verifier
	// (cfg.StateIsVerifier); every other provider keeps the historical
	// random-state CSRF check — same split as loginBrowser/pasteFlowState.
	var state string
	if cfg.StateIsVerifier {
		state = pkce.CodeVerifier
	} else {
		state, err = generateState()
		if err != nil {
			return nil, fmt.Errorf("generating state: %w", err)
		}
	}

	f := &ProviderFlow{
		cfg:      cfg,
		pkce:     pkce,
		state:    state,
		mode:     opts.Mode,
		timeout:  opts.flowTimeout(),
		status:   FlowStatusPending,
		finished: make(chan struct{}),
	}

	switch opts.Mode {
	case FlowModeManual:
		if cfg.ManualRedirectURI == "" {
			return nil, fmt.Errorf("%w: provider %q", ErrFlowNoManualRedirect, cfg.Name)
		}
		f.redirectURI = cfg.ManualRedirectURI
		f.url = buildAuthorizeURL(cfg, pkce, state, cfg.ManualRedirectURI)
		// No listener, no server goroutine: the flow completes only through
		// Submit, but the timeout budget still applies (the flow can otherwise
		// be left pending forever while the TUI waits on a paste).
		go f.watchTimeout()
		return f, nil

	case FlowModeLoopback:
		// Bind before building the authorize URL so the redirect_uri carries
		// the real bound port (cfg.Port may be 0, an ephemeral port), exactly
		// like loginBrowser.
		listener, err := bindCallbackListener(cfg)
		if err != nil {
			return nil, fmt.Errorf("starting callback server on port %d: %w", cfg.Port, err)
		}
		f.listener = listener
		f.redirectURI = redirectURIForListener(cfg, listener)
		f.url = buildAuthorizeURL(cfg, pkce, state, f.redirectURI)

		mux := http.NewServeMux()
		mux.HandleFunc(oauthCallbackPath(cfg), f.handleCallback)
		f.server = &http.Server{Handler: mux}
		go func() {
			// The listener is closed first on every release path, so Serve
			// returns "use of closed network connection" (the Shutdown path's
			// http.ErrServerClosed is unreachable here — see release). Either
			// way the error is discarded: errors are values here — a TUI must
			// never get a stray fmt.Fprintf under its alternate screen; the
			// pending flow simply times out or is canceled.
			_ = f.server.Serve(listener)
		}()
		go f.watchTimeout()
		return f, nil

	default:
		return nil, fmt.Errorf("oauth: unknown flow mode %d", opts.Mode)
	}
}

// URL returns the authorize URL the caller must open. It is immutable after
// StartProviderFlow returns.
func (f *ProviderFlow) URL() string { return f.url }

// RedirectURI returns the redirect_uri used on the authorize URL AND on the
// exchange: the loopback redirect derived from the bound port in
// FlowModeLoopback, the hosted ManualRedirectURI in FlowModeManual.
func (f *ProviderFlow) RedirectURI() string { return f.redirectURI }

// Mode returns the flow mode the flow was started with.
func (f *ProviderFlow) Mode() FlowMode { return f.mode }

// Status reports the flow state: ("pending", nil) while the code has not
// arrived, ("done", nil) once a credential was obtained, ("error", err)
// afterwards. The error value is stable and wraps the sentinel the flow
// finished with (errors.Is works), except for exchange failures, which wrap
// the exchange error instead.
func (f *ProviderFlow) Status() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.err
}

// Done reports whether the flow reached a terminal state (done OR error).
// After it returns true, Status and Credential are stable forever.
func (f *ProviderFlow) Done() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status != FlowStatusPending
}

// Credential returns the obtained credential, or nil unless Status reports
// "done". The returned pointer is stable and safe to read concurrently.
func (f *ProviderFlow) Credential() *AuthCredential {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cred
}

// Submit exchanges user-supplied input for tokens, synchronously (the caller
// may run it on its own goroutine). The input grammar is the one of
// loginPasteCodeWithPKCEAndState, reused parser by parser:
//
//   - StateIsVerifier (Claude): parseAuthorizeCodeInput accepts a bare code, a
//     callback URL, a bare query or a CODE#STATE paste; a non-empty pasted
//     state that differs from this flow's PKCE verifier is rejected with
//     ErrFlowStateMismatch, and the exchange echoes the verifier as `state`.
//   - otherwise (Gemini): a callback-shaped line (looksLikeCallbackInput) must
//     carry THIS flow's random state (parseCallbackInputWithState) and a
//     mismatch is ErrFlowStateMismatch; any other line is used verbatim as the
//     code — including the deliberate CODE#STATE quirk (it is not
//     callback-shaped, so the hash is never split). The exchange carries no
//     `state` field.
//
// Submit works in BOTH modes (in loopback mode it is the "user copied the
// code" path; same PKCE/redirect, so the listener would have accepted it too).
// It is one-shot: once the flow is terminal — including a callback that got
// there first — it returns ErrFlowClosed and issues NO request. Note that
// ErrFlowClosed does NOT mean failure: in a Submit-vs-callback race the
// callback may have won the exchange, so the caller must then read Status()
// and Credential() before treating the paste as lost (the flow may be done
// with a credential). An input that is rejected before the exchange (blank =>
// ErrFlowNoCode, or a pasted state that does not belong to this attempt =>
// ErrFlowStateMismatch) consumes NOTHING: the flow stays pending and the user
// can simply paste again, which is what a TUI paste box needs. Only a claimed
// attempt (an actual token request) can turn the flow terminal. Submit is the
// only method allowed to block (token exchange); Status/Credential become
// terminal when it returns.
func (f *ProviderFlow) Submit(input string) error {
	// Terminal check FIRST: on a finished flow every paste — even a blank one —
	// reports ErrFlowClosed, so a caller that keeps its paste box open on
	// ErrFlowNoCode does not wait forever for a flow that already ended (a
	// won Submit-vs-callback race ends exactly like this).
	if f.Done() {
		return ErrFlowClosed
	}
	if strings.TrimSpace(input) == "" {
		return ErrFlowNoCode
	}

	// Resolve/validate BEFORE claiming the single exchange attempt: a mistyped
	// paste must not burn the flow's only shot. resolveSubmittedCode reads only
	// immutable fields (cfg, pkce, state), so calling it unlocked is safe.
	code, err := f.resolveSubmittedCode(input)
	if err != nil {
		return err
	}

	// One-shot admission: claiming the exchange flag is what makes a Submit
	// racing an incoming callback exchange the single-use code AT MOST ONCE.
	if !f.claimExchange() {
		return ErrFlowClosed
	}

	cred, err := f.doExchange(code)
	if err != nil {
		f.fail(err)
		return err
	}
	f.succeed(cred)
	return nil
}

// Cancel aborts the flow: it unbinds the loopback listener immediately (see
// release) and — when the flow was still pending — marks it terminal with
// ErrFlowCanceled. It is idempotent and NON-BLOCKING: it returns as soon as
// the port is free, never waiting for an in-flight token exchange (the server
// shutdown budget runs in the background), so a bubbletea Update loop may call
// it directly. It is safe at any time, including after success and from
// multiple goroutines (no panic, no double close). When another path already
// claimed the exchange (a callback or Submit in flight), Cancel returns nil
// WITHOUT cancelling that exchange: the flow will finish as done with the
// credential. A code that arrives after Cancel is never exchanged.
// Cancelling an already-successful flow keeps the credential and still
// guarantees the callback port is released (it already is: every terminal
// transition releases it, see release).
func (f *ProviderFlow) Cancel() error {
	f.mu.Lock()
	pending := f.status == FlowStatusPending
	f.mu.Unlock()
	if pending && f.claimExchange() {
		f.fail(ErrFlowCanceled)
	}
	// Unconditional: a flow that finished on its own must still have its
	// listener released, and release is idempotent.
	f.release()
	return nil
}

// release unbinds the callback port. It is idempotent and safe at any time.
//
// It is called by EVERY terminal transition as well as by Cancel, because the
// callback port must not outlive the flow: a TUI starts one login per connect
// attempt inside the same process, and providers such as openai pin the
// callback port (1455), so a listener leaked after a successful login would
// make the NEXT login fail to bind. Before this, only Cancel shut the server
// down and it returned early once the flow was terminal, so every finished
// flow leaked its port for the lifetime of the process.
//
// release NEVER blocks: the listener is closed inline (Close only stops
// Accept, it cannot block), and the server shutdown — which WAITS for
// in-flight handlers — runs in its own goroutine. Callers include the
// bubbletea Update loop (ESC => Cancel), so blocking there would freeze the
// whole UI for the 2s shutdown budget.
func (f *ProviderFlow) release() {
	f.stopOnce.Do(func() {
		// Unbind NOW: Close only stops Accept and cannot block, so the port is
		// free the moment Cancel/release returns (a TUI re-binds port 1455 for
		// the next login).
		if f.listener != nil {
			_ = f.listener.Close()
		}
		if f.server != nil {
			// Shutdown WAITS for handlers to finish. Running it inline would let
			// a slow token exchange block the caller (a bubbletea Update loop)
			// for the whole 2s budget, so it gets its own goroutine. The budget
			// matches loginBrowser's; the context is Background because release
			// must never block on the caller's ctx.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = f.server.Shutdown(ctx)
			}()
		}
	})
}

// claimExchange atomically claims the single exchange attempt of this flow.
// It reports false when the flow is already terminal (done or error) or when
// another exchange attempt holds the claim. This is the one-shot guard shared
// by Submit, the callback handler and Cancel.
func (f *ProviderFlow) claimExchange() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status != FlowStatusPending || f.exchange {
		return false
	}
	f.exchange = true
	return true
}

// succeed records the successful terminal transition and releases the callback
// port. It is a no-op when the flow is already terminal (the one-shot guard
// makes this unreachable, but the double-check keeps it panic-free by
// construction).
func (f *ProviderFlow) succeed(cred *AuthCredential) {
	f.mu.Lock()
	transitioned := f.status == FlowStatusPending
	if transitioned {
		f.status = FlowStatusDone
		f.cred = cred
		f.err = nil
		close(f.finished)
	}
	f.mu.Unlock()
	if transitioned {
		// Plain (non-goroutine) release: release itself never blocks (it closes
		// the listener inline and shuts the server down asynchronously), so it
		// is safe to call from inside the HTTP callback handler too.
		f.release()
	}
}

// fail records the failing terminal transition and releases the callback port.
// Like succeed, it is a no-op once the flow is terminal.
func (f *ProviderFlow) fail(err error) {
	f.mu.Lock()
	transitioned := f.status == FlowStatusPending
	if transitioned {
		f.status = FlowStatusError
		f.err = err
		close(f.finished)
	}
	f.mu.Unlock()
	if transitioned {
		f.release()
	}
}

// handleCallback is the loopback mux handler. Its semantics are the ones of
// loginBrowser's handler, kept literal so the two surfaces cannot drift:
//
//   - a state mismatch answers 400 and terminates the flow with a state-
//     mismatch error WITHOUT touching the token endpoint;
//   - a missing code answers 400 with the `error` query fallback (the value is
//     sanitized — control bytes stripped, length capped — before it reaches a
//     TUI that draws it in the alternate screen);
//   - the success page is written AND flushed BEFORE the exchange starts,
//     exactly like loginBrowser (net/http buffers the body until Flush or
//     handler return, so without the flush the browser would show nothing
//     while the synchronous exchange runs);
//   - the code funnels into the same one-shot exchange path as Submit, so a
//     callback racing a Submit exchanges at most once.
//
// Unlike loginBrowser, the handler ignores every request once the flow is
// terminal (a stray second callback must not overwrite a terminal state), and
// writes no logs (the TUI owns the screen).
func (f *ProviderFlow) handleCallback(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	pending := f.status == FlowStatusPending
	f.mu.Unlock()
	if !pending {
		http.Error(w, "Login already finished", http.StatusBadRequest)
		return
	}

	if r.URL.Query().Get("state") != f.state {
		http.Error(w, "State mismatch", http.StatusBadRequest)
		f.fail(fmt.Errorf("%w: callback state does not match this flow's state", ErrFlowStateMismatch))
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		errMsg := sanitizeCallbackError(r.URL.Query().Get("error"))
		http.Error(w, "No authorization code received", http.StatusBadRequest)
		f.fail(fmt.Errorf("no code received: %s", errMsg))
		return
	}

	if !f.claimExchange() {
		// Lost the race against Submit/Cancel/a previous callback: the code is
		// NOT exchanged again (loginBrowser had a buffered channel per flow;
		// the one-shot guard replaces it here). The success page is deliberately
		// NOT written for a loser: claiming first means this request cannot
		// tell the user "authentication successful" for an exchange another
		// path owns (and which may still fail). The body is still written so
		// the browser never sits on an empty 200 while the winning path
		// finishes.
		http.Error(w, "Login already in progress", http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, "<html><body><h2>Authentication successful!</h2><p>You can close this window.</p></body></html>")
	if fl, ok := w.(http.Flusher); ok {
		// Deliver the page BEFORE the (synchronous) token exchange below: net/http
		// buffers the body until the handler returns, so without the flush a
		// slow token endpoint leaves the browser on a blank tab until it times
		// out (loginBrowser returns from its handler immediately, so this is the
		// one place its behaviour does not transfer for free).
		fl.Flush()
	}

	cred, err := f.doExchange(code)
	if err != nil {
		f.fail(err)
		return
	}
	f.succeed(cred)
}

// sanitizeCallbackError makes an `error=` query value safe to render in a TUI
// (handleCallback turns it into the terminal error string). A crafted local
// redirect can fill it with control bytes (ESC sequences that clear the
// alternate screen, newlines that forge log/UI lines) or with megabytes of
// junk: bytes < 0x20 (and 0x7f) are stripped and the value is capped at
// callbackErrorMaxLen characters.
func sanitizeCallbackError(msg string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1 // drop C0 control bytes and DEL (keeps tab/newline out too)
		}
		return r
	}, truncateAtRune(msg, callbackErrorMaxLen))
}

// callbackErrorMaxLen is the cap sanitizeCallbackError applies to a query value.
const callbackErrorMaxLen = 120

// truncateAtRune cuts s to at most n RUNES (not bytes), so a multi-byte
// character is never split in the middle.
func truncateAtRune(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s { // i is the byte offset of each rune start
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// exchange performs the token exchange with loginBrowser's parameters: the
// loopback/manual redirect of THIS flow, the PKCE verifier, and the `state`
// field ONLY when cfg.StateIsVerifier (jcode sends it for Claude and only for
// Claude). It returns the exchange error unchanged — errors are values.
func (f *ProviderFlow) doExchange(code string) (*AuthCredential, error) {
	ex := exchangeParams{
		Code:         code,
		CodeVerifier: f.pkce.CodeVerifier,
		RedirectURI:  f.redirectURI,
	}
	if f.cfg.StateIsVerifier {
		ex.State = f.state
	}
	return exchangeCodeForTokensWithParams(f.cfg, ex)
}

// resolveSubmittedCode applies the paste grammar of
// loginPasteCodeWithPKCEAndState, with the SAME state rules:
//
//   - StateIsVerifier (Claude): parseAuthorizeCodeInput; a non-empty pasted
//     state that differs from the verifier is ErrFlowStateMismatch (the
//     exchange then substitutes the verifier as `state`, correction V8).
//   - otherwise (Gemini): callback-shaped input goes through
//     parseCallbackInputWithState and must carry this flow's random state;
//     anything else is the code verbatim (CODE#STATE quirk included).
//
// The error is the flow's typed sentinel (errors.Is-able) rather than the
// blocking flow's message string, because a TUI branches on identity, not on
// text; the blocking functions keep their own wording untouched.
//
// Reads only cfg/pkce/state (immutable after StartProviderFlow) — do not add
// mutable fields here: it is called WITHOUT the flow lock held.
func (f *ProviderFlow) resolveSubmittedCode(input string) (string, error) {
	line := strings.TrimSpace(input)

	if f.cfg.StateIsVerifier {
		code, pastedState, err := parseAuthorizeCodeInput(line)
		if err != nil {
			return "", err
		}
		if pastedState != "" && pastedState != f.pkce.CodeVerifier {
			return "", fmt.Errorf("%w: pasted state does not match this login attempt", ErrFlowStateMismatch)
		}
		return code, nil
	}

	code := line
	if looksLikeCallbackInput(line) {
		parsedCode, pastedState, err := parseCallbackInputWithState(line)
		if err != nil {
			return "", err
		}
		if pastedState != f.state {
			return "", fmt.Errorf("%w: pasted state does not match this login attempt", ErrFlowStateMismatch)
		}
		code = parsedCode
	}
	// jcode quirk, deliberately preserved: `CODE#STATE` is NOT callback-shaped
	// on this branch, so the whole line is used verbatim as the code.
	return code, nil
}

// watchTimeout turns the flow terminal with ErrFlowTimeout when the budget
// elapses while the flow is still pending — the non-blocking counterpart of
// loginBrowser's time.After(5 * time.Minute). It exits early when the flow
// finishes first (f.finished), so no goroutine outlives the flow.
func (f *ProviderFlow) watchTimeout() {
	timer := time.NewTimer(f.timeout)
	defer timer.Stop()
	select {
	case <-timer.C:
		if f.claimExchange() {
			f.fail(fmt.Errorf("%w: no authorization code received after %s", ErrFlowTimeout, f.timeout))
		}
	case <-f.finished:
	}
}
