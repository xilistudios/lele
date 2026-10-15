package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
)

// This file is the interactive OAuth 2.1 authorization engine of ONE MCP
// server: it starts a flow, hands the caller the authorization URL, completes
// it through a loopback redirect or a manually submitted code, exposes the
// status, and can cancel or log out.
//
// Two invariants shape every line:
//
//  1. NO SECRET EVER LEAVES. A token, an authorization code, a code verifier
//     and a client secret are values this file must never log or put in an
//     error. Every error the flow returns is either one of the fixed
//     sentinels below or reduced to a value-free string (OAuth error codes
//     only, and only when they are a safe token).
//
//  2. THE FLOW DIALS WITH A STORE IT CONTROLS. flowTokenStore always reports
//     "no token" so a fresh authorization is produced even when a valid token
//     is already stored, and it captures the token mcp-go produces instead of
//     writing it to the real store. The stored credential is only REPLACED
//     when the flow's Submit succeeds, which also bumps the store revision
//     that retires the manager's cached connection; an abandoned flow leaves
//     it untouched.

var (
	// ErrAuthNotSupported marks a server whose metadata advertises no dynamic
	// client registration endpoint, so this flow has no way to obtain a client
	// id (the caller must pre-register one).
	ErrAuthNotSupported = errors.New("server does not advertise a dynamic client registration endpoint")
	// ErrAuthNotRequired marks a server that answered the handshake without
	// demanding credentials, so there is nothing to authorize. It means
	// exactly that: the PLAIN probe completed a handshake. A server that never
	// answered is ErrAuthProbeFailed instead (see below).
	ErrAuthNotRequired = errors.New("server did not request authentication")
	// ErrAuthProbeFailed marks a probe (or the OAuth dial that follows it) that
	// failed for a reason OTHER than an authorization challenge: an
	// unreachable host, a refused connection, a DNS failure, a typo'd URL or a
	// broken transport. It is deliberately distinct from ErrAuthNotRequired so
	// that a dead server is not reported as "no login needed", and the errors
	// wrapping it are value-free (the server NAME only, never the URL and
	// never the raw transport error, which may embed both).
	ErrAuthProbeFailed = errors.New("authorization probe failed: the server did not complete the handshake")
	// ErrAuthCanceled is the terminal error of a flow stopped by Cancel.
	ErrAuthCanceled = errors.New("authorization canceled")
	// ErrAuthTimedOut is the terminal error of a flow that never completed
	// within its budget.
	ErrAuthTimedOut = errors.New("authorization timed out")
	// ErrAuthStoreDisabled marks a flow attempted without a usable store: with
	// no lele directory the registration could not be persisted, so the flow
	// would be useless.
	ErrAuthStoreDisabled = errors.New("mcp auth store is disabled")
)

// Unexported, fixed errors used where the exported sentinels do not fit. They
// are variables (not inline errors.New calls) so tests can compare with
// errors.Is and so the "fixed, value-free string" rule is auditable in one
// place.
var (
	errAuthNoURL            = errors.New("authorization requires a remote (url) server")
	errAuthNoCallbackCode   = errors.New("authorization callback carried no code")
	errAuthRejected         = errors.New("authorization was rejected by the authorization server")
	errAuthStateMismatch    = errors.New("authorization state mismatch")
	errAuthEmptyCode        = errors.New("authorization code is empty")
	errAuthExchangeFailed   = errors.New("authorization token exchange failed")
	errAuthRegistrationDone = errors.New("dynamic client registration failed")
	errAuthNoToken          = errors.New("authorization succeeded without a token")
	errAuthStateGeneration  = errors.New("authorization could not generate a state")
	errAuthBadRedirectURI   = errors.New("authorization redirect uri is invalid")
	errAuthPersistFailed    = errors.New("failed to persist the authorization registration")
	// errAuthListenerClosed is what a loopback Await returns when the listener
	// was closed before a callback arrived (see loopbackServer.Close). It is
	// mapped onto ErrAuthCanceled by classifyAuthError: a torn-down listener
	// cannot deliver a code, so the flow's terminal outcome is the same as a
	// cancel.
	errAuthListenerClosed = errors.New("authorization listener was closed before a callback arrived")
)

// Status values reported by AuthFlow.Status. "pending" is the zero value the
// flow opens with; a re-tryable token-exchange failure sets "error" while
// leaving the flow open (a manual flow can retry), and a terminal outcome
// (success, cancel, timeout) also closes the flow's done channel.
const (
	authStatusPending = "pending"
	authStatusDone    = "done"
	authStatusError   = "error"
)

// defaultAuthTimeout bounds a flow when AuthOptions.Timeout is unset. It is
// long enough for a user to finish a browser login and short enough that a
// forgotten flow does not pin a listener forever.
const defaultAuthTimeout = 5 * time.Minute

// AuthOptions configures StartAuth. The zero value is usable: an ephemeral
// loopback listener on 127.0.0.1 and a five minute budget.
type AuthOptions struct {
	// RedirectURI overrides the configured redirect URI ("" ⇒ loopback on
	// 127.0.0.1, an ephemeral port unless Port says otherwise).
	RedirectURI string
	// Port pins the loopback port when a default loopback redirect URI is
	// built. 0 ⇒ an ephemeral port.
	Port int
	// Timeout bounds the whole flow, PRE-FLOW PHASE INCLUDED: it is the
	// wall-clock budget of StartAuth itself (the probe, the OAuth dial,
	// discovery, dynamic client registration and the authorization URL) and,
	// with whatever is left of it, of the flow the call returns. 0 ⇒ 5m.
	Timeout time.Duration
	// OpenURL opens the authorization URL for the user (a browser launch).
	// It is INJECTED because pkg/mcp must not spawn a browser itself and must
	// not import the caller's packages. nil means "the caller will display
	// URL()". A returned error is not fatal.
	OpenURL func(string) error
}

// AuthFlow is one in-flight interactive authorization. It is safe for
// concurrent use: Status/Wait/Cancel/Submit may be called from any goroutine.
type AuthFlow struct {
	// Set once at construction and read-only afterwards.
	handler     *transport.OAuthHandler
	verifier    string
	state       string
	loopback    *loopbackServer
	authURL     string
	redirectURI string

	name      string
	srv       ServerConfig
	store     *AuthStore
	flowStore *flowTokenStore
	cancel    context.CancelFunc

	// done is closed exactly once, when the flow reaches a terminal outcome.
	done chan struct{}

	// submitMu serialises completion attempts (the loopback callback and a
	// manual Submit) so two callers cannot drive the single-use code exchange
	// at the same time.
	submitMu sync.Mutex

	// mu guards status/err/finished.
	mu       sync.Mutex
	status   string
	err      error
	finished bool
}

// StartAuth begins the interactive OAuth 2.1 flow of one server.
//
// Sequence (the order matters — dynamic client registration sends redirect_uris
// and mcp-go passes RedirectURI verbatim into both the authorize URL and the
// token request, so the redirect URI must be FINAL before registration):
//
//  1. refuse a missing/disabled store (nothing could be persisted);
//  2. probe the server WITHOUT OAuth attached, so a healthy server can be told
//     apart from one demanding credentials (an OAuth-attached transport never
//     performs the HTTP call — contract fact 6 — so the probe is the only way
//     to observe "the server answered");
//  3. resolve the redirect URI (opts → config → stored record → loopback);
//  4. start the loopback listener when the redirect URI is http+loopback,
//     adopting the real bound port;
//  5. dial again with a fresh OAuth runtime (config/record values + the flow
//     token store + the FINAL redirect URI) and take the handler from the
//     challenge — which mcp-go reports from Start (SSE) or from Initialize
//     (streamable HTTP);
//  6. register a client when none is known, persist its id;
//  7. generate state/verifier/challenge and the authorization URL;
//  8. persist the registration (CARRYING OVER the existing token) so a later
//     dial can reuse it and an abandoned flow cannot log a working server out;
//  9. open the URL (non-fatal);
//  10. launch the flow goroutine under what is LEFT of the budget.
//
// ONE BUDGET BOUNDS THE WHOLE CALL. "Timeout bounds the whole flow" (see
// AuthOptions.Timeout) is only true if the pre-flow phase runs on that budget
// too: those steps used the caller's raw context — which all three production
// callers pass as context.Background() — and mcp-go builds an http.Client with
// no Timeout of its own, so a server that accepted the TCP connection and never
// answered blocked StartAuth before it ever produced an authorization URL. The
// CLI never printed the URL, the TUI stayed on "starting", and the gateway held
// the per-(agent, server) flow lock for good, so every later Start for that pair
// waited behind it. The phase therefore runs on ONE budget context, the flow
// goroutine INHERITS that same context (and what is left of it), and a phase
// that runs out of time reports ErrAuthTimedOut — the sentinel the flow itself
// reports when its context expires, because to the caller it is one event
// observed one step earlier. See preFlowError.
func StartAuth(ctx context.Context, name string, srv ServerConfig, store *AuthStore, opts AuthOptions) (flow *AuthFlow, err error) {
	if store == nil || !store.Enabled() {
		return nil, ErrAuthStoreDisabled
	}
	if srv.URL == "" || srv.Command != "" {
		return nil, errAuthNoURL
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultAuthTimeout
	}

	// The budget of the whole call (see the doc comment). Callers hand in
	// context.Background(), so this is the only thing standing between an
	// unresponsive server and a StartAuth that never returns.
	authCtx, cancelAuth := context.WithTimeout(ctx, timeout)
	defer func() {
		// The flow OWNS this context once it exists — it releases it in
		// finish(), next to the loopback listener — so only a call that failed
		// before the flow was built has to release the timer here.
		if flow == nil {
			cancelAuth()
		}
	}()

	// Step 2: a plain probe, WITHOUT OAuth attached. An OAuth-attached
	// transport never performs the HTTP call (contract fact 6), so the probe
	// is the only way to observe what a server answers when asked for nothing.
	// Three outcomes are deliberately distinguished:
	//
	//   - the handshake completes ⇒ the server needs no credentials at all
	//     (ErrAuthNotRequired, unchanged);
	//   - it fails with an authorization challenge ⇒ this is the server we are
	//     here for: continue with the flow;
	//   - it fails any other way (DNS, refused connection, a typo'd URL, a
	//     server that answers garbage) ⇒ ErrAuthProbeFailed. Reporting "the
	//     server did not request authentication" for a server that never
	//     answered sends the operator looking in the wrong place.
	switch probeErr := probePlain(authCtx, srv); {
	case probeErr == nil:
		return nil, ErrAuthNotRequired
	case !authRequiredError(probeErr):
		return nil, preFlowError(authCtx, probeFailedError(name))
	}

	redirectURI, err := resolveRedirectURI(store, name, srv, opts)
	if err != nil {
		return nil, err
	}

	var lb *loopbackServer
	if isLoopbackRedirect(redirectURI) {
		lb, err = startLoopback(redirectURI, defaultListen)
		if err != nil {
			return nil, err
		}
		redirectURI = lb.RedirectURI()
	}
	// Any failure past the listener must release the port.
	fail := func(e error) (*AuthFlow, error) {
		if lb != nil {
			_ = lb.Close()
		}
		return nil, e
	}

	flowStore := &flowTokenStore{}
	dialCfg := srv
	dialCfg.authRuntime = resolveFlowRuntime(store, name, srv, redirectURI, flowStore)

	handler, dialErr := dialOAuthHandler(authCtx, dialCfg)
	if handler == nil {
		if dialErr != nil {
			// The OAuth-attached dial failed and carried no challenge: same
			// treatment as a failed probe. The transport error is deliberately
			// NOT surfaced (it can embed the URL, and with it a secret): the
			// fixed, errors.Is-able ErrAuthProbeFailed is the report.
			return fail(preFlowError(authCtx, probeFailedError(name)))
		}
		// Defensive: the attached transport completed the handshake without a
		// challenge, so this server needs no credentials after all. (In
		// practice the probe above already returns ErrAuthNotRequired for such
		// a server, and an attached handler with no token challenges before
		// sending anything.)
		return fail(ErrAuthNotRequired)
	}

	if handler.GetClientID() == "" {
		md, mderr := handler.GetServerMetadata(authCtx)
		if mderr != nil {
			return fail(preFlowError(authCtx, valueFreeError(mderr)))
		}
		if md == nil || md.RegistrationEndpoint == "" {
			return fail(ErrAuthNotSupported)
		}
		if rerr := handler.RegisterClient(authCtx, clientName); rerr != nil {
			return fail(preFlowError(authCtx, errAuthRegistrationDone))
		}
	}

	state, err := transport.GenerateState()
	if err != nil {
		return fail(errAuthStateGeneration)
	}
	verifier, err := transport.GenerateCodeVerifier()
	if err != nil {
		return fail(errAuthStateGeneration)
	}
	challenge := transport.GenerateCodeChallenge(verifier)
	authURL, err := handler.GetAuthorizationURL(authCtx, state, challenge)
	if err != nil {
		return fail(preFlowError(authCtx, valueFreeError(err)))
	}

	// The stored registration carries the CURRENT token (when there is one and
	// it belongs to this URL) and nothing else changes: store.Save replaces the
	// WHOLE record, so writing the registration without the token would log out
	// a server that was working — and bump the revision, which makes the
	// manager retire the healthy connection and leaves buildAuthRuntime
	// attaching OAuth with no token, i.e. an unusable server until a login
	// completes. Carrying it over is safe: "fresh authorization" is enforced by
	// flowTokenStore.GetToken → ErrNoToken, not by the absence of a stored
	// token, so an abandoned or failed flow is a NO-OP for a logged-in server.
	rec := AuthRecord{
		URL:          srv.URL,
		ClientID:     handler.GetClientID(),
		ClientSecret: handler.GetClientSecret(),
		RedirectURI:  redirectURI,
		AuthRequired: true,
	}
	if current, ok := store.Record(name); ok && current.URL == srv.URL {
		// A record bound to another resource is a different server's
		// credential: it is not ours to keep.
		rec.Token = current.Token
	}
	if srv.Auth != nil {
		// Only an EXPLICITLY configured metadata URL is persisted. A URL this
		// flow did not receive from a real challenge must never be stored
		// (mcp-go's synthesized well-known URL is discovery detail, not state).
		rec.ResourceMetadataURL = srv.Auth.ProtectedResourceMetadataURL
	}
	if err := store.Save(name, rec); err != nil {
		return fail(fmt.Errorf("%w: %v", errAuthPersistFailed, err))
	}

	if opts.OpenURL != nil {
		// Not fatal: the caller still has URL().
		_ = opts.OpenURL(authURL)
	}

	// The flow INHERITS the budget context: the pre-flow phase above already
	// spent part of it, and f.cancel releases it in finish(), so a flow that is
	// never completed still cannot hold the timer past the deadline.
	f := &AuthFlow{
		handler:     handler,
		verifier:    verifier,
		state:       state,
		loopback:    lb,
		authURL:     authURL,
		redirectURI: redirectURI,
		name:        name,
		srv:         srv,
		store:       store,
		flowStore:   flowStore,
		cancel:      cancelAuth,
		done:        make(chan struct{}),
		status:      authStatusPending,
	}
	go f.run(authCtx)
	return f, nil
}

// URL returns the authorization URL the user must visit. The URL carries the
// public client id, the CSRF state and the PKCE challenge — never a secret.
func (f *AuthFlow) URL() string { return f.authURL }

// RedirectURI returns the redirect URI this flow registered and expects. For a
// loopback flow it is the REAL bound URI (the ephemeral port resolved).
func (f *AuthFlow) RedirectURI() string { return f.redirectURI }

// Status reports the flow state ("pending" | "done" | "error") and, when the
// state is "error", the value-free error that produced it.
//
// "error" is AMBIGUOUS BY DESIGN and must be read together with Done(), which
// reports whether the flow actually ended:
//
//   - a FAILED TOKEN EXCHANGE inside Submit (or a failed loopback exchange)
//     sets "error" while leaving the flow OPEN and retryable when the caller
//     still holds a code: Submit stays valid while !Done(), and a later
//     success moves the status to "done";
//   - Cancel, the timeout, and a one-shot loopback exchange failure set
//     "error" AND end the flow, so Done() is true and Submit only replays the
//     terminal error.
//
// A consumer that treats "error" as terminal WITHOUT checking Done() and then
// calls Wait() blocks until the flow's budget expires. The three-state set is
// deliberate and unchanged: terminality is the extra bit Done() exposes, not a
// fourth state.
func (f *AuthFlow) Status() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.err
}

// Done reports whether the flow reached a TERMINAL outcome — success, Cancel,
// the timeout, or a callback exchange that can no longer be retried. It is
// false while the flow is pending AND after a retryable token-exchange failure,
// which is exactly the distinction Status's "error" cannot make on its own.
//
// It is the predicate a surface needs before deciding to Wait(): with Done()
// true, Wait returns immediately; with Done() false, the caller must either
// Submit a code or Cancel instead of waiting for the budget to run out.
func (f *AuthFlow) Done() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.finished
}

// Submit completes the flow with a code obtained out of band (the manual path)
// or by the loopback callback.
//
// Semantics:
//   - a finished flow is idempotent: "done" returns nil, a terminal error
//     returns that error;
//   - a wrong (non-empty) state is rejected before anything else;
//   - a token-exchange failure is recorded (status "error") but leaves the
//     flow open, so a caller that still holds the code can retry — the
//     expected state is re-armed before every attempt because mcp-go consumes
//     it on the first call regardless of the outcome (finding 3).
//
// The error never contains the code, the token, the verifier or the secret.
func (f *AuthFlow) Submit(ctx context.Context, code, state string) error {
	if state != "" && state != f.state {
		return errAuthStateMismatch
	}
	if code == "" {
		return errAuthEmptyCode
	}

	f.submitMu.Lock()
	defer f.submitMu.Unlock()

	f.mu.Lock()
	if f.finished {
		err := f.err
		f.mu.Unlock()
		return err
	}
	f.mu.Unlock()

	// finding (3): re-arm the expected state before EVERY attempt.
	f.handler.SetExpectedState(f.state)

	if err := f.handler.ProcessAuthorizationResponse(ctx, code, state, f.verifier); err != nil {
		werr := valueFreeError(err)
		f.recordFailure(werr)
		return werr
	}

	tok := f.flowStore.token()
	if tok == nil {
		f.recordFailure(errAuthNoToken)
		return errAuthNoToken
	}
	// The credential becomes visible only here; SaveToken is a read-modify-write
	// that preserves the client_id/client_secret/redirect written by StartAuth.
	if err := f.store.TokenStore(f.name, f.srv.URL).SaveToken(ctx, tok); err != nil {
		werr := valueFreeError(err)
		f.recordFailure(werr)
		return werr
	}

	f.finish(nil)
	return nil
}

// Wait blocks until the flow reaches a terminal outcome (done/error/cancel/
// timeout) or ctx is done. It returns the flow's error (nil when done).
func (f *AuthFlow) Wait(ctx context.Context) error {
	select {
	case <-f.done:
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Cancel stops the flow, releases the loopback listener and marks a terminal
// ErrAuthCanceled (unless the flow already completed). It is idempotent.
func (f *AuthFlow) Cancel() error {
	f.finish(ErrAuthCanceled)
	return nil
}

// run is the flow goroutine: it waits for the loopback callback (or, for a
// manual flow, for the terminal context) and completes the exchange.
func (f *AuthFlow) run(ctx context.Context) {
	if f.loopback != nil {
		code, state, err := f.loopback.Await(ctx)
		if err != nil {
			f.finish(classifyAuthError(err))
			return
		}
		if serr := f.Submit(ctx, code, state); serr != nil {
			// The loopback callback is one-shot and the code is not exposed to
			// the caller, so a failed callback exchange is terminal.
			f.finish(serr)
		}
		return
	}
	// Manual flow: the caller holds the code and may Submit at any time; the
	// flow ends when the caller succeeds, cancels, or the budget runs out.
	select {
	case <-ctx.Done():
		f.finish(classifyAuthError(ctx.Err()))
	case <-f.done:
	}
}

// finish performs the one-shot transition to a terminal outcome, closes the
// listener, and cancels the flow context. Later calls are no-ops.
func (f *AuthFlow) finish(err error) {
	f.mu.Lock()
	if f.finished {
		f.mu.Unlock()
		return
	}
	f.finished = true
	f.err = err
	if err != nil {
		f.status = authStatusError
	} else {
		f.status = authStatusDone
	}
	close(f.done)
	lb := f.loopback
	f.mu.Unlock()

	if f.cancel != nil {
		f.cancel()
	}
	if lb != nil {
		_ = lb.Close()
	}
}

// recordFailure records a retryable failure without terminating the flow.
func (f *AuthFlow) recordFailure(err error) {
	f.mu.Lock()
	if !f.finished {
		f.status = authStatusError
		f.err = err
	}
	f.mu.Unlock()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// probePlain dials srv with a PLAIN transport (no OAuth attached) and returns
// the handshake error. The client is closed before returning.
func probePlain(ctx context.Context, srv ServerConfig) error {
	plain := srv
	plain.authRuntime = nil
	d := newMCPDialer()
	tr, err := d.buildTransport(plain)
	if err != nil {
		return err
	}
	cli := client.NewClient(tr)
	defer func() { _ = cli.Close() }()
	if err := cli.Start(ctx); err != nil {
		return err
	}
	_, err = cli.Initialize(ctx, initializeRequest())
	return err
}

// dialOAuthHandler dials srv with its OAuth runtime attached and returns the
// authorization handler carried by the challenge.
//
// The challenge does NOT always arrive from the same call: the streamable HTTP
// transport's Start is a no-op and reports it from the first request (our
// Initialize), while the SSE transport dials its event stream INSIDE Start and
// reports it from there (transport/client/sse.go). Both errors are therefore
// inspected, Start's first.
//
// Return value contract: a non-nil handler means "this server demands
// authorization"; (nil, nil) means "the handshake completed without a
// challenge"; (nil, err) means the dial failed for another reason and err is
// the underlying transport error (kept for the caller to classify, never
// surfaced verbatim).
//
// LIFETIME RULE: a captured *transport.OAuthHandler owns its own HTTP client
// and token store and outlives the client it was read from, so on THAT path the
// client is deliberately NOT closed — the flow keeps using the handler. Every
// OTHER path (transport build failure, a Start/Initialize error that yielded no
// handler, a clean handshake) closes the client before returning.
func dialOAuthHandler(ctx context.Context, srv ServerConfig) (*transport.OAuthHandler, error) {
	d := newMCPDialer()
	tr, err := d.buildTransport(srv)
	if err != nil {
		return nil, err
	}
	cli := client.NewClient(tr)

	startErr := cli.Start(ctx)
	var initErr error
	if startErr == nil {
		_, initErr = cli.Initialize(ctx, initializeRequest())
	}
	// Start's challenge first, then Initialize's: whichever yields a handler
	// wins, and the handler's lifetime is then owned by the flow (see above).
	if handler, ok := oauthHandlerFrom(startErr); ok {
		return handler, nil
	}
	if handler, ok := oauthHandlerFrom(initErr); ok {
		return handler, nil
	}
	_ = cli.Close()
	if startErr != nil {
		return nil, startErr
	}
	return nil, initErr
}

// oauthHandlerFrom maps an error to the OAuth handler it carries, if any:
// mcp-go embeds the handler in *transport.OAuthAuthorizationRequiredError
// (client/oauth.go GetOAuthHandler), and client.GetOAuthHandler already fails
// safe on nil and on any other error type.
func oauthHandlerFrom(err error) (*transport.OAuthHandler, bool) {
	handler := client.GetOAuthHandler(err)
	return handler, handler != nil
}

// probeFailedError is the fixed, value-free report of a probe (or an
// OAuth-attached dial) that failed for a reason other than an authorization
// challenge: an unreachable host, a refused connection, a typo'd URL, a broken
// server.
//
// Only the server NAME — which the caller itself supplied — is echoed. The
// transport's own error is deliberately NOT wrapped: it embeds the request URL
// (net/url errors, net/http errors and mcp-go's "failed to connect to SSE
// stream: ..." all do), and a URL can carry a token in its query string.
func probeFailedError(name string) error {
	return fmt.Errorf("mcp auth %q: probe: %w", name, ErrAuthProbeFailed)
}

// preFlowError reduces a failure of the pre-flow phase (the probe, the
// OAuth-attached dial, discovery, DCR, the authorization URL) onto the package's
// sentinels. Two things can end that phase and they must not be conflated:
//
//   - the SERVER answered something this flow cannot use: failure is the
//     caller's own report for it, already reduced and value-free (probeFailed
//     Error names the server, valueFreeError never echoes a body, the DCR
//     sentinel is a constant);
//   - the BUDGET ran out because the server did not answer at all: the report is
//     ErrAuthTimedOut, the very sentinel the flow goroutine produces when its
//     own context expires. One budget bounds the whole call (see StartAuth), so
//     to the caller this is one event — "the whole-flow budget expired" —
//     observed one step earlier, and it deserves the same answer.
//
// Checking budget.Err() (rather than the error) is deliberate: mcp-go wraps the
// context error inside net/http and fmt layers, and a cancellation that arrived
// with the deadline would otherwise read as an ordinary transport failure. Both
// outcomes are value-free: ErrAuthTimedOut is a fixed string.
func preFlowError(budget context.Context, failure error) error {
	if errors.Is(budget.Err(), context.DeadlineExceeded) {
		return ErrAuthTimedOut
	}
	return failure
}

// resolveRedirectURI applies the documented precedence: options, then an
// explicitly configured redirect URI, then the stored registration, then a
// default loopback URI (ephemeral port unless opts.Port pins one).
func resolveRedirectURI(store *AuthStore, name string, srv ServerConfig, opts AuthOptions) (string, error) {
	if opts.RedirectURI != "" {
		// mcp-go hands this value VERBATIM to /authorize and to the token
		// request and never validates it itself (transport has
		// ValidateRedirectURI, but the flow never calls it — see authtest's
		// contract note (5)), so the check has to live here: an overridden
		// redirect URI must be the only shape an authorization server could
		// redirect to, an absolute http/https URL with a host. A relative or
		// opaque value would produce an authorization URL no browser can
		// follow and a registration the AS rejects. The error is fixed and
		// value-free (the URI is not echoed).
		if !isAbsoluteHTTPURL(opts.RedirectURI) {
			return "", errAuthBadRedirectURI
		}
		return opts.RedirectURI, nil
	}
	if srv.Auth != nil && srv.Auth.RedirectURI != "" {
		// Not validated: this value comes from the user's own mcp.json (or the
		// CLI flag that filled it) and is a deliberate opt-in for a
		// pre-registered client, which is exactly the case where an AS-specific
		// shape (a custom scheme, a path the AS dictates) must be honoured.
		return srv.Auth.RedirectURI, nil
	}
	if rec, ok := store.Record(name); ok && rec.URL == srv.URL && rec.RedirectURI != "" {
		return rec.RedirectURI, nil
	}
	if opts.Port < 0 || opts.Port > 65535 {
		return "", errAuthBadRedirectURI
	}
	return fmt.Sprintf("http://127.0.0.1:%d/callback", opts.Port), nil
}

// resolveFlowRuntime builds the fresh OAuth runtime the flow dials with:
// config values win, empty fields fall back to the stored registration, and
// the redirect URI and token store are always the flow's own. It mirrors
// buildAuthRuntime's field resolution but WITHOUT its opt-in gate, because the
// flow knows (from the probe) that the server requires authorization.
func resolveFlowRuntime(store *AuthStore, name string, srv ServerConfig, redirectURI string, ts transport.TokenStore) *oauthRuntime {
	rt := &oauthRuntime{redirectURI: redirectURI, tokenStore: ts}
	if srv.Auth != nil {
		rt.clientID = srv.Auth.ClientID
		rt.clientSecret = srv.Auth.ClientSecret
		rt.scopes = srv.Auth.Scopes
		rt.authServerMetadataURL = srv.Auth.AuthServerMetadataURL
		rt.protectedResourceMetadataURL = srv.Auth.ProtectedResourceMetadataURL
	}
	if rec, ok := store.Record(name); ok && rec.URL == srv.URL {
		if rt.clientID == "" {
			rt.clientID = rec.ClientID
		}
		if rt.clientSecret == "" {
			rt.clientSecret = rec.ClientSecret
		}
		if rt.protectedResourceMetadataURL == "" {
			rt.protectedResourceMetadataURL = rec.ResourceMetadataURL
		}
	}
	return rt
}

// isAbsoluteHTTPURL reports whether raw is an absolute http or https URL with
// a host — the only shape an authorization server can redirect a browser to.
func isAbsoluteHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// isLoopbackRedirect reports whether raw is an http URI whose host is a
// loopback name, i.e. a URI this process can serve itself.
func isLoopbackRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}

// classifyAuthError maps a context error onto the flow's terminal sentinel.
func classifyAuthError(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ErrAuthTimedOut
	case errors.Is(err, context.Canceled):
		return ErrAuthCanceled
	case errors.Is(err, errAuthListenerClosed):
		// The listener that would have delivered the callback is gone, so
		// there is nothing left to wait for: same terminal outcome as a cancel.
		return ErrAuthCanceled
	default:
		return valueFreeError(err)
	}
}

// oauthCodeError is the ONE value-free way to report a specific OAuth error
// CODE (RFC 6749 §5.2: invalid_request, invalid_grant, access_denied, …).
//
// It is a TYPE rather than a plain errors.New/fmt.Errorf so the code survives a
// trip through classifyAuthError → valueFreeError, which reduces everything it
// does not recognise to a fixed message. The loopback server produces it for a
// browser callback that carried `error=` (a user DENIAL is the everyday case),
// and the classifier recognises that type instead of answering "authorization
// token exchange failed" — a message about an exchange that never ran
// (finding F5).
//
// SECRECY: the only way to build it is with a string sanitizeOAuthToken has
// already accepted (see resultOf and valueFreeError), so the ≤64 bytes its
// message may echo are attacker-BOUNDED — a registered or vendor error code —
// and never a response body, a token, a code, a verifier, a URL or a
// state/error_description value. It carries nothing else.
type oauthCodeError struct{ code string }

func (e *oauthCodeError) Error() string { return "authorization failed: " + e.code }

// valueFreeError reduces an arbitrary mcp-go/HTTP error to a fixed string.
//
// mcp-go's token/registration errors embed the response BODY (which may echo a
// code, a token or a client secret) and the request URL, so they are NEVER
// returned as-is. The only part kept is the OAuth error code, and only when it
// is a conservative token; everything else becomes a fixed message.
//
// The claim this makes is precisely: NO mcp-go-EMBEDDED RESPONSE BODY (and no
// URL, token, code or verifier) escapes. It is NOT "no attacker-controlled
// bytes escape" — see sanitizeOAuthToken for the trade-off deliberately taken
// there, and keep it in mind before widening any error's audience.
func valueFreeError(err error) error {
	if err == nil {
		return nil
	}
	// Already reduced by a producer that knew the code is safe (the loopback's
	// callback path): it is the one input this function cannot improve on, and
	// re-deriving it would DROP the code, which is the whole point of carrying
	// it this far.
	var coded *oauthCodeError
	if errors.As(err, &coded) && coded != nil {
		return coded
	}
	if code := oauthErrorCode(err); code != "" {
		if safe := sanitizeOAuthToken(code); safe != "" {
			return &oauthCodeError{code: safe}
		}
	}
	if errors.Is(err, transport.ErrInvalidState) {
		return errAuthStateMismatch
	}
	return errAuthExchangeFailed
}

// oauthErrorCode extracts the OAuth error CODE from an mcp-go error, or "".
//
// Both the value and the POINTER form are matched. mcp-go currently wraps the
// value (oauth.go's extractOAuthError does `%w` on an OAuthError), so the value
// check alone covers today's library; a pointer `*OAuthError` is one of the
// shapes the same type can arrive as (a caller-constructed error, a future
// library revision), and missing it would silently degrade a specific OAuth
// code to the generic "authorization token exchange failed" message.
func oauthErrorCode(err error) string {
	var value transport.OAuthError
	if errors.As(err, &value) {
		return value.ErrorCode
	}
	var pointer *transport.OAuthError
	if errors.As(err, &pointer) && pointer != nil {
		return pointer.ErrorCode
	}
	return ""
}

// sanitizeOAuthToken accepts a short token made only of characters an OAuth
// error code may contain. Anything else yields "" so the caller falls back to a
// fixed message instead of echoing attacker-controlled bytes.
//
// TRADE-OFF, stated so nobody mistakes this for a body filter: a real
// authorization server puts an error CODE in `error` (RFC 6749 §5.2 lists
// invalid_request, invalid_grant, …), and this accepts any ≤64-character run of
// [A-Za-z0-9_.-]. A HOSTILE AS can therefore echo up to 64 attacker-chosen
// bytes of its own choosing in an error message — never a response body, never
// a token, a code or a verifier, and never anything that did not come from the
// `error` field of a response we provoked. The alternative (an allow-list of
// the registered codes) would turn every new or vendor-specific code into an
// opaque "authorization failed", so the permissive-but-bounded rule wins. Any
// surface that shows this string to a user is showing ≤64 bytes the AS chose.
func sanitizeOAuthToken(s string) string {
	if s == "" || len(s) > 64 {
		return ""
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-', r == '.':
		default:
			return ""
		}
	}
	return s
}

// ---------------------------------------------------------------------------
// flow token store
// ---------------------------------------------------------------------------

// flowTokenStore is what the flow dials with. Its GetToken ALWAYS reports "no
// token" so the flow always produces a fresh authorization even when a (stale
// or valid) token is already stored — and its SaveToken captures the token
// mcp-go produces instead of writing it to the real store, so the flow controls
// when the credential becomes visible (and bumps the store revision).
type flowTokenStore struct {
	mu  sync.Mutex
	tok *transport.Token
}

var _ transport.TokenStore = (*flowTokenStore)(nil)

// GetToken always reports ErrNoToken: the flow must never silently reuse a
// stored credential.
func (s *flowTokenStore) GetToken(context.Context) (*transport.Token, error) {
	return nil, transport.ErrNoToken
}

// SaveToken captures a deep copy of the token mcp-go produced. It never
// touches the real store.
func (s *flowTokenStore) SaveToken(_ context.Context, t *transport.Token) error {
	if t == nil {
		return nil
	}
	cp := *t
	s.mu.Lock()
	s.tok = &cp
	s.mu.Unlock()
	return nil
}

// token returns a copy of the captured token, or nil.
func (s *flowTokenStore) token() *transport.Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tok == nil {
		return nil
	}
	cp := *s.tok
	return &cp
}

// ---------------------------------------------------------------------------
// logout & status
// ---------------------------------------------------------------------------

// Logout forgets everything stored about one server: the token AND the dynamic
// registration. It is the "forget this server" primitive.
func Logout(store *AuthStore, name string) error {
	if store == nil || !store.Enabled() {
		return ErrAuthStoreDisabled
	}
	return store.Clear(name)
}

// AuthStatus is what a surface needs to know about one server's auth without
// ever touching a token value.
type AuthStatus struct {
	// Required is true when the entry DECLARES auth (an auth block of type
	// "oauth") or a 401 was RECORDED for (name, url) in the auth store. It is
	// never inferred from anything else — in particular an entry's headers
	// alone do NOT make a server required, because MCP headers are routinely
	// non-credential (Accept, User-Agent, X-Client-Version).
	//
	// This is the frozen design rule
	// auth.required == auth.type == "oauth" || store.auth_required
	// (.lele/tasks/mcp-oauth-plan.md, C1), with the explicit note that a 401 is
	// recorded IN THE STORE ONLY — never written back into mcp.json.
	Required bool
	// Authenticated is true when a usable credential for this name+url exists
	// in the STORE — i.e. the next request needs no sign-in.
	Authenticated bool
	// Method is "oauth", "headers" or "" (none).
	Method string
}

// CheckAuth reports the auth state of ONE server. It never returns a token or
// any other secret; it answers yes/no questions only.
//
// Required follows the rule documented on AuthStatus.Required: a declared
// OAuth block or a RECORDED 401. Method is orthogonal and is reported as soon
// as there is a carrier to name, even when Required is false: "headers" tells
// a surface "credentials, if any, travel in headers; there is no OAuth login
// to offer", which is true whether or not a challenge was ever observed.
//
// Authenticated follows ONE rule, applied identically in every branch:
//
//	Authenticated = a usable credential for this name+url exists in the store,
//	i.e. no sign-in is needed.
//
// It is deliberately NOT "the entry declares OAuth". The same login can remedy
// several entry shapes — buildAuthRuntime attaches OAuth for a recorded
// challenge whatever the carrier is, header carrier included — so an entry that
// declares no method at all can still be signed in, and a surface that answered
// "not authenticated" for it kept offering an Authenticate button to a user who
// had already completed the flow. Because the answer is a fact about the STORE,
// it is computed once and never inferred from the entry's shape.
func CheckAuth(store *AuthStore, name string, srv ServerConfig) AuthStatus {
	var st AuthStatus
	// The store's verdict for (name, url): the same question in every branch
	// (see the doc comment above), asked once so no branch can drift. A nil
	// store ("no lele directory") holds no credential and answers "no".
	st.Authenticated = store != nil && store.Authenticated(name, srv.URL)
	// A 401 lives in the store ONLY (plan C1): it is never inferred from the
	// entry, so an entry whose headers are non-credential stays unflagged.
	challenged := store != nil && store.AuthRequired(name, srv.URL)
	switch {
	case srv.Auth != nil && srv.Auth.Type == typeAuthOAuth:
		st.Required = true
		st.Method = "oauth"
	case len(srv.Headers) > 0:
		// Headers alone are NOT evidence of auth. An MCP entry's headers are
		// frequently non-credential (Accept, User-Agent, X-Client-Version),
		// so Required is driven by the RECORDED challenge, not by their
		// presence. Do not "fix" this back to Required = true. Authenticated
		// is orthogonal to that warning: it is the store's answer, and a
		// header-carried entry whose login completed IS signed in.
		st.Method = "headers"
		st.Required = challenged
	case challenged:
		// No declared carrier, but a real 401 was recorded: the remedy is the
		// zero-config OAuth login, which is why Method stays "".
		st.Required = true
	}
	return st
}
