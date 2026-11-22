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

var (
	ErrAuthNotSupported  = errors.New("server does not advertise a dynamic client registration endpoint")
	ErrAuthNotRequired   = errors.New("server did not request authentication")
	ErrAuthProbeFailed   = errors.New("authorization probe failed: the server did not complete the handshake")
	ErrAuthCanceled      = errors.New("authorization canceled")
	ErrAuthTimedOut      = errors.New("authorization timed out")
	ErrAuthStoreDisabled = errors.New("mcp auth store is disabled")
)

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
	errAuthListenerClosed   = errors.New("authorization listener was closed before a callback arrived")
)

const (
	authStatusPending = "pending"
	authStatusDone    = "done"
	authStatusError   = "error"
)

const defaultAuthTimeout = 5 * time.Minute

type AuthOptions struct {
	RedirectURI string
	Port        int
	Timeout     time.Duration
	OpenURL     func(string) error
}

type AuthFlow struct {
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

	done chan struct{}

	submitMu sync.Mutex

	mu       sync.Mutex
	status   string
	err      error
	finished bool
}

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

	authCtx, cancelAuth := context.WithTimeout(ctx, timeout)
	defer func() {
		if flow == nil {
			cancelAuth()
		}
	}()

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
			return fail(preFlowError(authCtx, probeFailedError(name)))
		}
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

	rec := AuthRecord{
		URL:          srv.URL,
		ClientID:     handler.GetClientID(),
		ClientSecret: handler.GetClientSecret(),
		RedirectURI:  redirectURI,
		AuthRequired: true,
	}
	if current, ok := store.Record(name); ok && current.URL == srv.URL {
		rec.Token = current.Token
	}
	if srv.Auth != nil {
		rec.ResourceMetadataURL = srv.Auth.ProtectedResourceMetadataURL
	}
	if err := store.Save(name, rec); err != nil {
		return fail(fmt.Errorf("%w: %v", errAuthPersistFailed, err))
	}

	if opts.OpenURL != nil {
		_ = opts.OpenURL(authURL)
	}

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

func (f *AuthFlow) URL() string { return f.authURL }

func (f *AuthFlow) RedirectURI() string { return f.redirectURI }

func (f *AuthFlow) Status() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.err
}

func (f *AuthFlow) Done() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.finished
}

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
	if err := f.store.TokenStore(f.name, f.srv.URL).SaveToken(ctx, tok); err != nil {
		werr := valueFreeError(err)
		f.recordFailure(werr)
		return werr
	}

	f.finish(nil)
	return nil
}

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

func (f *AuthFlow) Cancel() error {
	f.finish(ErrAuthCanceled)
	return nil
}

func (f *AuthFlow) run(ctx context.Context) {
	if f.loopback != nil {
		code, state, err := f.loopback.Await(ctx)
		if err != nil {
			f.finish(classifyAuthError(err))
			return
		}
		if serr := f.Submit(ctx, code, state); serr != nil {
			f.finish(serr)
		}
		return
	}
	select {
	case <-ctx.Done():
		f.finish(classifyAuthError(ctx.Err()))
	case <-f.done:
	}
}

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

func (f *AuthFlow) recordFailure(err error) {
	f.mu.Lock()
	if !f.finished {
		f.status = authStatusError
		f.err = err
	}
	f.mu.Unlock()
}

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

func oauthHandlerFrom(err error) (*transport.OAuthHandler, bool) {
	handler := client.GetOAuthHandler(err)
	return handler, handler != nil
}

func probeFailedError(name string) error {
	return fmt.Errorf("mcp auth %q: probe: %w", name, ErrAuthProbeFailed)
}

func preFlowError(budget context.Context, failure error) error {
	if errors.Is(budget.Err(), context.DeadlineExceeded) {
		return ErrAuthTimedOut
	}
	return failure
}

func resolveRedirectURI(store *AuthStore, name string, srv ServerConfig, opts AuthOptions) (string, error) {
	if opts.RedirectURI != "" {
		if !isAbsoluteHTTPURL(opts.RedirectURI) {
			return "", errAuthBadRedirectURI
		}
		return opts.RedirectURI, nil
	}
	if srv.Auth != nil && srv.Auth.RedirectURI != "" {
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

func isAbsoluteHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

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

func classifyAuthError(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ErrAuthTimedOut
	case errors.Is(err, context.Canceled):
		return ErrAuthCanceled
	case errors.Is(err, errAuthListenerClosed):
		return ErrAuthCanceled
	default:
		return valueFreeError(err)
	}
}

type oauthCodeError struct{ code string }

func (e *oauthCodeError) Error() string { return "authorization failed: " + e.code }

func valueFreeError(err error) error {
	if err == nil {
		return nil
	}
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

type flowTokenStore struct {
	mu  sync.Mutex
	tok *transport.Token
}

var _ transport.TokenStore = (*flowTokenStore)(nil)

func (s *flowTokenStore) GetToken(context.Context) (*transport.Token, error) {
	return nil, transport.ErrNoToken
}

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

func (s *flowTokenStore) token() *transport.Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tok == nil {
		return nil
	}
	cp := *s.tok
	return &cp
}

func Logout(store *AuthStore, name string) error {
	if store == nil || !store.Enabled() {
		return ErrAuthStoreDisabled
	}
	return store.Clear(name)
}

type AuthStatus struct {
	Required      bool
	Authenticated bool
	Method        string
}

func CheckAuth(store *AuthStore, name string, srv ServerConfig) AuthStatus {
	var st AuthStatus
	st.Authenticated = store != nil && store.Authenticated(name, srv.URL)
	challenged := store != nil && store.AuthRequired(name, srv.URL)
	switch {
	case srv.Auth != nil && srv.Auth.Type == typeAuthOAuth:
		st.Required = true
		st.Method = "oauth"
	case len(srv.Headers) > 0:
		st.Method = "headers"
		st.Required = challenged
	case challenged:
		st.Required = true
	}
	return st
}
