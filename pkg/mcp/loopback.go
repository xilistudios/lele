package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// This file is the loopback half of the interactive OAuth 2.1 flow
// (auth.go): the tiny HTTP server that receives the authorization server's
// redirect and hands the code back to the flow.
//
// It is deliberately minimal and self-contained for three reasons.
//
//  1. It is reachable only from this package: the type and its helpers are
//     unexported, so no surface can accidentally depend on a listener's
//     internals.
//
//  2. Binding is behind the listenFunc seam (defaultListen), so a test can
//     prove the bind error path and a bounded bind without touching the
//     network. Production always uses net.Listen wrapped in defaultListen.
//
//  3. It never logs the code, never echoes it into the page, and delivers the
//     result exactly once (sync.Once into a buffered channel) so a browser
//     that double-fetches the callback URL cannot drive two token exchanges.

// listenFunc is the listener seam: tests substitute a bounded/failing bind
// without touching the network.
type listenFunc func(network, addr string) (net.Listener, error)

// defaultListen is a var (not a direct call to net.Listen) so a test can swap
// it, exercise the bind-failure path, and restore it.
var defaultListen listenFunc = net.Listen

// loopbackResult is what one callback request delivers to the flow. Only the
// fields needed to complete (or reject) the flow are kept.
//
// There is deliberately no error_description here. The authorization server
// controls that parameter and may echo back whatever the client was sent, so
// the flow reads it nowhere and could not surface it anywhere: the only failure
// text ever produced from a callback is the sanitized OAuth error CODE. Keeping
// a field nothing reads would be a standing invitation to leak it later — see
// the parse site in handle, which says the same thing.
type loopbackResult struct {
	code      string
	state     string
	errString string
	err       error
}

// loopbackServer is the minimal callback listener. It serves exactly one path
// (the redirect URI's path) and 404s everything else.
type loopbackServer struct {
	ln          net.Listener
	srv         *http.Server
	result      chan loopbackResult // buffered (1): deliver never blocks the handler
	once        sync.Once           // deliver exactly once
	closeOnce   sync.Once           // Close is idempotent
	closed      chan struct{}       // closed by Close: wakes a pending Await
	redirectURI string              // the URI actually bound (real port)
	path        string              // the only path that is served
}

// startLoopback binds the redirect URI and starts serving it.
//
// The port is taken verbatim from redirectURI when it is non-zero, and an
// ephemeral port (":0") is requested otherwise; in both cases the returned
// server reports the REAL bound URI through RedirectURI(), because the
// authorization server must be told the exact URI that will receive the
// redirect and it has to appear verbatim later in the token request.
//
// Only http + loopback hosts reach this function (StartAuth decides), so no
// TLS is set up and a non-loopback bind would be a programming error.
func startLoopback(redirectURI string, listen listenFunc) (*loopbackServer, error) {
	if listen == nil {
		listen = defaultListen
	}
	u, err := url.Parse(redirectURI)
	if err != nil || u.Host == "" {
		return nil, errors.New("mcp auth: invalid loopback redirect uri")
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "0"
	}
	ln, err := listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		// The address is not a secret; the bind failure is.
		return nil, fmt.Errorf("mcp auth: loopback listener: %w", err)
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, errors.New("mcp auth: loopback listener is not tcp")
	}

	bound := *u
	bound.Host = net.JoinHostPort(host, strconv.Itoa(tcpAddr.Port))
	path := u.Path
	if path == "" {
		path = "/callback"
	}

	l := &loopbackServer{
		ln:          ln,
		result:      make(chan loopbackResult, 1),
		closed:      make(chan struct{}),
		redirectURI: bound.String(),
		path:        path,
	}
	// Timeouts are not decoration: the listener sits on a loopback port for the
	// life of the flow and a stuck client (a browser holding the connection
	// open) must not be able to pin a handler goroutine until the flow's
	// budget runs out. A callback is one tiny request, so the bounds are short.
	l.srv = &http.Server{
		Handler:           http.HandlerFunc(l.handle),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	go func() { _ = l.srv.Serve(ln) }()
	return l, nil
}

// RedirectURI returns the redirect URI as actually bound (the real port).
func (l *loopbackServer) RedirectURI() string { return l.redirectURI }

// handle serves the single callback path. Anything else is a 404: the listener
// exists for one redirect and nothing more. On the callback path only GET (the
// ordinary redirect) and POST (an AS that uses form_post) are accepted.
func (l *loopbackServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != l.path {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	// `error_description` is intentionally NOT read. It is attacker-controlled
	// prose (the authorization server may echo back whatever it was sent) and
	// the flow has no use for it: only the code in `error` is ever reported,
	// and only when sanitizeOAuthToken accepts it.
	res := loopbackResult{
		code:      q.Get("code"),
		state:     q.Get("state"),
		errString: q.Get("error"),
	}
	if res.errString == "" && res.code == "" {
		res.err = errAuthNoCallbackCode
	}
	// The page is written and FLUSHED before the result is handed over, and the
	// order matters: a rejecting callback is terminal, so the flow closes this
	// listener the moment it receives the result (Cancel/finish → Close), and
	// http.Server.Close tears down the connection the response is still sitting
	// in — the browser would see a bare connection reset instead of the page.
	// Flushing first keeps the page independent of how fast the flow reacts.
	writeCallbackPage(w, res.err == nil && res.errString == "")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	// Exactly one request wins: a browser that retries the callback URL must
	// not be able to enqueue a second exchange.
	l.once.Do(func() { l.result <- res })
}

// Await blocks until the callback arrives, the listener is closed, or ctx is
// done. The returned error is value-free: only the OAuth error code (when it is
// a safe token) is echoed, never error_description (which can carry whatever
// the client sent, including its redirect URI).
func (l *loopbackServer) Await(ctx context.Context) (code, state string, err error) {
	// A callback that ALREADY arrived wins over a concurrent Close: taking it
	// first keeps a Close racing the redirect from discarding a code the flow
	// could still have used.
	select {
	case res := <-l.result:
		return resultOf(res)
	default:
	}
	select {
	case res := <-l.result:
		return resultOf(res)
	case <-l.closed:
		// Close beat the callback: a closed listener can never deliver one, so
		// returning immediately keeps a waiter from blocking until the ctx
		// deadline (or forever, when the ctx has no deadline at all).
		return "", "", errAuthListenerClosed
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}

// resultOf turns one delivered callback into Await's return values. The error
// is value-free: only the OAuth error code (when it is a safe token) is echoed,
// never error_description.
//
// The code travels as *oauthCodeError rather than a plain fmt.Errorf for one
// reason: the flow's classifier (classifyAuthError → valueFreeError) reduces
// everything it does not recognise to a fixed message, and it recognises this
// type. A user DENIAL (`?error=access_denied`) is the everyday case — without
// the type the user was told "authorization token exchange failed" for an
// exchange that never ran (finding F5). The sanitizer is still the only door:
// anything it refuses falls back to the fixed errAuthRejected.
func resultOf(res loopbackResult) (code, state string, err error) {
	if res.err != nil {
		return "", "", res.err
	}
	if res.errString != "" {
		if c := sanitizeOAuthToken(res.errString); c != "" {
			return "", "", &oauthCodeError{code: c}
		}
		return "", "", errAuthRejected
	}
	return res.code, res.state, nil
}

// Close stops serving and releases the port. It is idempotent and never leaks
// the listener: http.Server.Close closes the listener it was given, and the
// extra ln.Close is a harmless double close kept as a belt-and-braces guard.
// It also wakes any pending Await (see there).
func (l *loopbackServer) Close() error {
	l.closeOnce.Do(func() {
		if l.closed != nil {
			close(l.closed)
		}
		if l.srv != nil {
			_ = l.srv.Close()
		}
		if l.ln != nil {
			_ = l.ln.Close()
		}
	})
	return nil
}

// writeCallbackPage writes the tiny self-contained page a browser shows after
// the redirect. Success and failure get distinct pages; neither contains the
// code or any other value.
func writeCallbackPage(w http.ResponseWriter, ok bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if ok {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, loopbackSuccessPage)
		return
	}
	w.WriteHeader(http.StatusBadRequest)
	_, _ = io.WriteString(w, loopbackFailurePage)
}

const loopbackSuccessPage = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Authorization complete</title></head>
<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;text-align:center">
<h1>Authorization complete</h1>
<p>You can close this window and return to lele.</p>
</body>
</html>`

const loopbackFailurePage = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Authorization failed</title></head>
<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;text-align:center">
<h1>Authorization failed</h1>
<p>Return to lele to see what went wrong and try again.</p>
</body>
</html>`
