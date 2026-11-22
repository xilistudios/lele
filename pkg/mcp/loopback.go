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

type listenFunc func(network, addr string) (net.Listener, error)

var defaultListen listenFunc = net.Listen

type loopbackResult struct {
	code      string
	state     string
	errString string
	err       error
}

type loopbackServer struct {
	ln          net.Listener
	srv         *http.Server
	result      chan loopbackResult
	once        sync.Once
	closeOnce   sync.Once
	closed      chan struct{}
	redirectURI string
	path        string
}

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
	l.srv = &http.Server{
		Handler:           http.HandlerFunc(l.handle),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() { _ = l.srv.Serve(ln) }()
	return l, nil
}

func (l *loopbackServer) RedirectURI() string { return l.redirectURI }

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
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	_ = r.ParseForm()
	q := r.Form
	res := loopbackResult{
		code:      q.Get("code"),
		state:     q.Get("state"),
		errString: q.Get("error"),
	}
	if res.errString == "" && res.code == "" {
		res.err = errAuthNoCallbackCode
	}
	writeCallbackPage(w, res.err == nil && res.errString == "")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	l.once.Do(func() { l.result <- res })
}

func (l *loopbackServer) Await(ctx context.Context) (code, state string, err error) {
	select {
	case res := <-l.result:
		return resultOf(res)
	default:
	}
	select {
	case res := <-l.result:
		return resultOf(res)
	case <-l.closed:
		return "", "", errAuthListenerClosed
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}

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
