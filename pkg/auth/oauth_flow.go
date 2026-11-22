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

type FlowMode int

const (
	FlowModeLoopback FlowMode = iota
	FlowModeManual
)

const (
	FlowStatusPending = "pending"
	FlowStatusDone    = "done"
	FlowStatusError   = "error"
)

var (
	ErrFlowCanceled         = errors.New("oauth: flow canceled")
	ErrFlowTimeout          = errors.New("oauth: flow timed out")
	ErrFlowClosed           = errors.New("oauth: flow already finished")
	ErrFlowNoManualRedirect = errors.New("oauth: provider has no ManualRedirectURI for the manual flow")
	ErrFlowNoCode           = errors.New("oauth: no authorization code entered")
	ErrFlowStateMismatch    = errors.New("oauth: state mismatch")
)

type FlowOptions struct {
	Mode    FlowMode
	Timeout time.Duration
}

func (o FlowOptions) flowTimeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return 5 * time.Minute
}

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
	exchange bool

	server   *http.Server
	listener net.Listener
	finished chan struct{}
	stopOnce sync.Once
}

func StartProviderFlow(ctx context.Context, cfg OAuthProviderConfig, opts FlowOptions) (*ProviderFlow, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	_ = ctx

	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("generating PKCE: %w", err)
	}

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
		go f.watchTimeout()
		return f, nil

	case FlowModeLoopback:
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
			_ = f.server.Serve(listener)
		}()
		go f.watchTimeout()
		return f, nil

	default:
		return nil, fmt.Errorf("oauth: unknown flow mode %d", opts.Mode)
	}
}

func (f *ProviderFlow) URL() string { return f.url }

func (f *ProviderFlow) RedirectURI() string { return f.redirectURI }

func (f *ProviderFlow) Mode() FlowMode { return f.mode }

func (f *ProviderFlow) Status() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.err
}

func (f *ProviderFlow) Done() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status != FlowStatusPending
}

func (f *ProviderFlow) Credential() *AuthCredential {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cred
}

func (f *ProviderFlow) Submit(input string) error {
	if f.Done() {
		return ErrFlowClosed
	}
	if strings.TrimSpace(input) == "" {
		return ErrFlowNoCode
	}

	code, err := f.resolveSubmittedCode(input)
	if err != nil {
		return err
	}

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

func (f *ProviderFlow) Cancel() error {
	f.mu.Lock()
	pending := f.status == FlowStatusPending
	f.mu.Unlock()
	if pending && f.claimExchange() {
		f.fail(ErrFlowCanceled)
	}
	f.release()
	return nil
}

func (f *ProviderFlow) release() {
	f.stopOnce.Do(func() {
		if f.listener != nil {
			_ = f.listener.Close()
		}
		if f.server != nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = f.server.Shutdown(ctx)
			}()
		}
	})
}

func (f *ProviderFlow) claimExchange() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status != FlowStatusPending || f.exchange {
		return false
	}
	f.exchange = true
	return true
}

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
		f.release()
	}
}

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
		http.Error(w, "Login already in progress", http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, "<html><body><h2>Authentication successful!</h2><p>You can close this window.</p></body></html>")
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}

	cred, err := f.doExchange(code)
	if err != nil {
		f.fail(err)
		return
	}
	f.succeed(cred)
}

func sanitizeCallbackError(msg string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, truncateAtRune(msg, callbackErrorMaxLen))
}

const callbackErrorMaxLen = 120

func truncateAtRune(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

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
	return code, nil
}

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
