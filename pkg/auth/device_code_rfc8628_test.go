package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This file tests the generic RFC 8628 device grant with httptest servers and a
// fake clock/sleep so nothing here touches the network and every test is
// instantaneous and deterministic.
//
// The Narrow place: the polling loop sleeps *before* each poll (see
// LoginDeviceCodeRFC8628), so with the server sequence
// [authorization_pending, slow_down, success] the recorded sleeps are
// [i0, i0, i0+5s]: the 5 s back-off produced by the slow_down answer governs the
// sleep that follows it. (The task spec sketches that assertion as
// [i0, i0+5s, i0+5s], which would require the back-off of a response to affect
// the sleep *before* it — impossible with the same response order. The
// semantically load-bearing properties are asserted instead: one +5 s increment
// that persists into every later sleep.)

const (
	rfc8628TestDeviceCode    = "device-secret-code-xyz"
	rfc8628TestUserCode      = "ABCD-1234"
	rfc8628TestVerification  = "https://example.com/device"
	rfc8628TestVerificationC = "https://example.com/device?user_code=ABCD-1234"
	rfc8628TestAccessToken   = "access-token-1"
	rfc8628TestRefreshToken  = "refresh-token-1"
)

// rfc8628HTTPResponse is a canned answer for the fake server.
type rfc8628HTTPResponse struct {
	status int
	body   string
}

func rfc8628JSON(status int, body string) rfc8628HTTPResponse {
	return rfc8628HTTPResponse{status: status, body: body}
}

// rfc8628TestServer serves /device/code and /token, records every request and
// replays a fixed list of answers (a nil / empty list answers 200 {}).
type rfc8628TestServer struct {
	srv *httptest.Server

	mu             sync.Mutex
	deviceCodeHits int
	tokenHits      int
	deviceForm     url.Values
	pollForms      []url.Values
}

// newRFC8628TestServer starts the fake provider. device may be nil (200 {}),
// and when token is exhausted the server answers 500 with an unknown error code
// so a buggy polling loop fails fast instead of looping forever.
func newRFC8628TestServer(t *testing.T, device *rfc8628HTTPResponse, token ...rfc8628HTTPResponse) *rfc8628TestServer {
	t.Helper()

	s := &rfc8628TestServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("%s: method = %s, want POST", r.URL.Path, r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("%s: ParseForm: %v", r.URL.Path, err)
		}

		s.mu.Lock()
		defer s.mu.Unlock()

		switch r.URL.Path {
		case "/device/code":
			s.deviceCodeHits++
			s.deviceForm = rfc8628CloneValues(r.PostForm)
			if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
				t.Errorf("device code Content-Type = %q, want application/x-www-form-urlencoded", ct)
			}
			if device == nil {
				rfc8628WriteResponse(w, rfc8628JSON(http.StatusOK, "{}"))
				return
			}
			rfc8628WriteResponse(w, *device)
		case "/token":
			s.tokenHits++
			s.pollForms = append(s.pollForms, rfc8628CloneValues(r.PostForm))
			if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
				t.Errorf("token poll Content-Type = %q, want application/x-www-form-urlencoded", ct)
			}
			idx := s.tokenHits - 1
			if idx >= len(token) {
				rfc8628WriteResponse(w, rfc8628JSON(http.StatusInternalServerError, `{"error":"test-server-exhausted"}`))
				return
			}
			rfc8628WriteResponse(w, token[idx])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func rfc8628WriteResponse(w http.ResponseWriter, resp rfc8628HTTPResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.status)
	_, _ = io.WriteString(w, resp.body)
}

func rfc8628CloneValues(in url.Values) url.Values {
	out := make(url.Values, len(in))
	for k, vs := range in {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func (s *rfc8628TestServer) counts() (deviceCode, token int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deviceCodeHits, s.tokenHits
}

func (s *rfc8628TestServer) totalHits() int {
	deviceCode, token := s.counts()
	return deviceCode + token
}

func (s *rfc8628TestServer) recordedDeviceForm() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deviceForm
}

func (s *rfc8628TestServer) recordedPollForms() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.pollForms...)
}

// rfc8628TestConfig builds a config pointed at the fake server. Its
// DeviceVerifyURL doubles as the fallback exercised by
// TestRFC8628VerifyURLFallback.
func rfc8628TestConfig(s *rfc8628TestServer) OAuthProviderConfig {
	return OAuthProviderConfig{
		Name:            "test-provider",
		ClientID:        "test-client-id",
		Scopes:          "openid profile offline_access",
		DeviceCodeURL:   s.srv.URL + "/device/code",
		DeviceTokenURL:  s.srv.URL + "/token",
		DeviceVerifyURL: s.srv.URL + "/verify",
		HTTPClient:      s.srv.Client(),
	}
}

// rfc8628DeviceBody is a well-formed device authorization response.
func rfc8628DeviceBody(expiresIn, interval string) string {
	return fmt.Sprintf(
		`{"device_code":%q,"user_code":%q,"verification_uri":%q,"verification_uri_complete":%q,"expires_in":%s,"interval":%s}`,
		rfc8628TestDeviceCode, rfc8628TestUserCode, rfc8628TestVerification, rfc8628TestVerificationC, expiresIn, interval,
	)
}

// rfc8628Recorder is a fake clock + sleep: it never blocks and records every
// requested duration, so tests can assert both the back-off schedule and the
// number of polls without waiting.
type rfc8628Recorder struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
	// cancel, when set, is called on the first sleep to simulate a cancellation
	// arriving mid-flight.
	cancel func()
}

func newRFC8628Recorder() *rfc8628Recorder {
	return &rfc8628Recorder{now: time.Unix(1700000000, 0)}
}

func (r *rfc8628Recorder) nowFn() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

func (r *rfc8628Recorder) sleepFn(_ context.Context, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sleeps = append(r.sleeps, d)
	r.now = r.now.Add(d)
	if r.cancel != nil {
		cancel := r.cancel
		r.cancel = nil
		cancel()
	}
}

func (r *rfc8628Recorder) recordedSleeps() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.sleeps...)
}

func (r *rfc8628Recorder) options() DeviceCodeOptions {
	return DeviceCodeOptions{Now: r.nowFn, Sleep: r.sleepFn, Out: io.Discard}
}

func rfc8628AssertSleeps(t *testing.T, got, want []time.Duration) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("recorded sleeps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sleep[%d] = %s, want %s (all: %v)", i, got[i], want[i], got)
		}
	}
}

func TestRFC8628HappyPathPendingThenSuccess(t *testing.T) {
	server := newRFC8628TestServer(
		t,
		ptrRFC8628Response(rfc8628JSON(http.StatusOK, rfc8628DeviceBody("900", "1"))),
		rfc8628JSON(http.StatusBadRequest, `{"error":"authorization_pending"}`),
		rfc8628JSON(http.StatusBadRequest, `{"error":"authorization_pending"}`),
		rfc8628JSON(http.StatusOK, fmt.Sprintf(`{"access_token":%q,"refresh_token":%q,"expires_in":3600}`, rfc8628TestAccessToken, rfc8628TestRefreshToken)),
	)
	cfg := rfc8628TestConfig(server)

	collected := &strings.Builder{}
	recorder := newRFC8628Recorder()
	opts := recorder.options()
	opts.Out = collected

	cred, err := LoginDeviceCodeRFC8628(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("LoginDeviceCodeRFC8628: %v", err)
	}

	// (a) one device authorization request, three token polls.
	deviceCodeHits, tokenHits := server.counts()
	if deviceCodeHits != 1 {
		t.Errorf("device code endpoint hits = %d, want 1", deviceCodeHits)
	}
	if tokenHits != 3 {
		t.Errorf("token endpoint hits = %d, want 3", tokenHits)
	}

	// (b) the device request is a form POST with client_id and scope.
	deviceForm := server.recordedDeviceForm()
	if got := deviceForm.Get("client_id"); got != cfg.ClientID {
		t.Errorf("device client_id = %q, want %q", got, cfg.ClientID)
	}
	if got := deviceForm.Get("scope"); got != cfg.Scopes {
		t.Errorf("device scope = %q, want %q", got, cfg.Scopes)
	}
	if got := deviceForm.Get("grant_type"); got != "" {
		t.Errorf("device request must not carry grant_type, got %q", got)
	}

	// (c) every poll carries grant_type + device_code + client_id.
	for i, form := range server.recordedPollForms() {
		if got := form.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:device_code" {
			t.Errorf("poll %d grant_type = %q", i, got)
		}
		if got := form.Get("device_code"); got != rfc8628TestDeviceCode {
			t.Errorf("poll %d device_code = %q, want %q", i, got, rfc8628TestDeviceCode)
		}
		if got := form.Get("client_id"); got != cfg.ClientID {
			t.Errorf("poll %d client_id = %q, want %q", i, got, cfg.ClientID)
		}
	}

	// (d) the credential comes from the token response, tagged with cfg.Name.
	if cred == nil {
		t.Fatal("credential = nil, want non-nil")
	}
	if cred.AccessToken != rfc8628TestAccessToken {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, rfc8628TestAccessToken)
	}
	if cred.RefreshToken != rfc8628TestRefreshToken {
		t.Errorf("RefreshToken = %q, want %q", cred.RefreshToken, rfc8628TestRefreshToken)
	}
	if cred.Provider != cfg.Name {
		t.Errorf("Provider = %q, want %q", cred.Provider, cfg.Name)
	}

	// (e) instructions show the complete URI and the user code, never the device
	// code (a secret that could be redeemed by anyone who reads it).
	out := collected.String()
	if !strings.Contains(out, rfc8628TestVerificationC) {
		t.Errorf("output does not contain the verification_uri_complete:\n%s", out)
	}
	if !strings.Contains(out, rfc8628TestUserCode) {
		t.Errorf("output does not contain the user code:\n%s", out)
	}
	if strings.Contains(out, rfc8628TestDeviceCode) {
		t.Errorf("output leaks the device_code:\n%s", out)
	}

	// A 1 s server interval, floored by the 1 s default MinInterval.
	rfc8628AssertSleeps(t, recorder.recordedSleeps(), []time.Duration{time.Second, time.Second, time.Second})
}

func TestRFC8628SlowDownBacksOff(t *testing.T) {
	server := newRFC8628TestServer(
		t,
		ptrRFC8628Response(rfc8628JSON(http.StatusOK, rfc8628DeviceBody("900", "2"))),
		rfc8628JSON(http.StatusBadRequest, `{"error":"authorization_pending"}`),
		rfc8628JSON(http.StatusBadRequest, `{"error":"slow_down"}`),
		rfc8628JSON(http.StatusOK, `{"access_token":"`+rfc8628TestAccessToken+`"}`),
	)
	cfg := rfc8628TestConfig(server)

	recorder := newRFC8628Recorder()
	cred, err := LoginDeviceCodeRFC8628(context.Background(), cfg, recorder.options())
	if err != nil {
		t.Fatalf("LoginDeviceCodeRFC8628: %v", err)
	}
	if cred == nil {
		t.Fatal("credential = nil, want non-nil")
	}

	// i0 = 2 s (server): the sleep after authorization_pending keeps 2 s, the
	// slow_down answer adds exactly 5 s and the increment persists.
	rfc8628AssertSleeps(t, recorder.recordedSleeps(), []time.Duration{2 * time.Second, 2 * time.Second, 7 * time.Second})

	_, tokenHits := server.counts()
	if tokenHits != 3 {
		t.Errorf("token endpoint hits = %d, want 3", tokenHits)
	}
}

func TestRFC8628ExpiredTokenIsTerminal(t *testing.T) {
	server := newRFC8628TestServer(
		t,
		ptrRFC8628Response(rfc8628JSON(http.StatusOK, rfc8628DeviceBody("900", "1"))),
		rfc8628JSON(http.StatusBadRequest, `{"error":"expired_token"}`),
		rfc8628JSON(http.StatusOK, `{"access_token":"`+rfc8628TestAccessToken+`"}`),
	)
	cfg := rfc8628TestConfig(server)

	cred, err := LoginDeviceCodeRFC8628(context.Background(), cfg, newRFC8628Recorder().options())
	if err == nil {
		t.Fatal("error = nil, want an expired_token error")
	}
	if cred != nil {
		t.Errorf("credential = %+v, want nil", cred)
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error = %v, want it to mention expiry", err)
	}
	if strings.Contains(err.Error(), rfc8628TestDeviceCode) {
		t.Errorf("error leaks the device_code: %v", err)
	}
	_, tokenHits := server.counts()
	if tokenHits != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (polling must stop)", tokenHits)
	}
}

func TestRFC8628AccessDeniedSurfacesDescription(t *testing.T) {
	server := newRFC8628TestServer(
		t,
		ptrRFC8628Response(rfc8628JSON(http.StatusOK, rfc8628DeviceBody("900", "1"))),
		rfc8628JSON(http.StatusBadRequest, `{"error":"access_denied","error_description":"the user said no"}`),
	)
	cfg := rfc8628TestConfig(server)

	cred, err := LoginDeviceCodeRFC8628(context.Background(), cfg, newRFC8628Recorder().options())
	if err == nil {
		t.Fatal("error = nil, want an access_denied error")
	}
	if cred != nil {
		t.Errorf("credential = %+v, want nil", cred)
	}
	if !strings.Contains(err.Error(), "the user said no") {
		t.Errorf("error = %v, want it to include the error_description", err)
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %v, want it to mention the denial", err)
	}
	_, tokenHits := server.counts()
	if tokenHits != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (polling must stop)", tokenHits)
	}
}

func TestRFC8628HonorsExpiresIn(t *testing.T) {
	// The server keeps answering authorization_pending; once the canned answers
	// run out the test server replies 500 test-server-exhausted, so a loop that
	// ignored the deadline would fail on that error instead of hanging.
	server := newRFC8628TestServer(
		t,
		ptrRFC8628Response(rfc8628JSON(http.StatusOK, rfc8628DeviceBody("1", "1"))),
		rfc8628JSON(http.StatusBadRequest, `{"error":"authorization_pending"}`),
		rfc8628JSON(http.StatusBadRequest, `{"error":"authorization_pending"}`),
	)
	cfg := rfc8628TestConfig(server)

	cred, err := LoginDeviceCodeRFC8628(context.Background(), cfg, newRFC8628Recorder().options())
	if err == nil {
		t.Fatal("error = nil, want a timeout")
	}
	if cred != nil {
		t.Errorf("credential = %+v, want nil", cred)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %v, want it to announce the timeout", err)
	}
	_, tokenHits := server.counts()
	if tokenHits < 1 {
		t.Errorf("token endpoint hits = %d, want at least 1", tokenHits)
	}
	if tokenHits > 3 {
		t.Errorf("token endpoint hits = %d, want a bounded number", tokenHits)
	}
}

func TestRFC8628MissingEndpointsFailsFast(t *testing.T) {
	t.Run("no device code URL", func(t *testing.T) {
		server := newRFC8628TestServer(t, ptrRFC8628Response(rfc8628JSON(http.StatusOK, rfc8628DeviceBody("900", "1"))))
		cfg := rfc8628TestConfig(server)
		cfg.DeviceCodeURL = ""

		cred, err := LoginDeviceCodeRFC8628(context.Background(), cfg, newRFC8628Recorder().options())
		if err == nil {
			t.Fatal("error = nil, want a configuration error")
		}
		if cred != nil {
			t.Errorf("credential = %+v, want nil", cred)
		}
		if !strings.Contains(err.Error(), "DeviceCodeURL") {
			t.Errorf("error = %v, want it to name DeviceCodeURL", err)
		}
		if hits := server.totalHits(); hits != 0 {
			t.Errorf("HTTP hits = %d, want 0", hits)
		}
	})

	t.Run("no device token URL", func(t *testing.T) {
		server := newRFC8628TestServer(t, ptrRFC8628Response(rfc8628JSON(http.StatusOK, rfc8628DeviceBody("900", "1"))))
		cfg := rfc8628TestConfig(server)
		cfg.DeviceTokenURL = ""

		_, err := LoginDeviceCodeRFC8628(context.Background(), cfg, newRFC8628Recorder().options())
		if err == nil {
			t.Fatal("error = nil, want a configuration error")
		}
		if !strings.Contains(err.Error(), "DeviceTokenURL") {
			t.Errorf("error = %v, want it to name DeviceTokenURL", err)
		}
		if hits := server.totalHits(); hits != 0 {
			t.Errorf("HTTP hits = %d, want 0", hits)
		}
	})

	t.Run("no client ID", func(t *testing.T) {
		server := newRFC8628TestServer(t, ptrRFC8628Response(rfc8628JSON(http.StatusOK, rfc8628DeviceBody("900", "1"))))
		cfg := rfc8628TestConfig(server)
		cfg.ClientID = ""

		_, err := LoginDeviceCodeRFC8628(context.Background(), cfg, newRFC8628Recorder().options())
		if err == nil {
			t.Fatal("error = nil, want a configuration error")
		}
		if !strings.Contains(err.Error(), "ClientID") {
			t.Errorf("error = %v, want it to name ClientID", err)
		}
		if hits := server.totalHits(); hits != 0 {
			t.Errorf("HTTP hits = %d, want 0", hits)
		}
	})
}

func TestRFC8628MissingDeviceCodeInResponseIsError(t *testing.T) {
	server := newRFC8628TestServer(
		t,
		ptrRFC8628Response(rfc8628JSON(http.StatusOK, `{"user_code":"ABCD-1234","verification_uri":"https://example.com/device"}`)),
		rfc8628JSON(http.StatusOK, `{"access_token":"`+rfc8628TestAccessToken+`"}`),
	)
	cfg := rfc8628TestConfig(server)

	cred, err := LoginDeviceCodeRFC8628(context.Background(), cfg, newRFC8628Recorder().options())
	if err == nil {
		t.Fatal("error = nil, want a missing device_code error")
	}
	if cred != nil {
		t.Errorf("credential = %+v, want nil", cred)
	}
	if !strings.Contains(err.Error(), "missing device_code") {
		t.Errorf("error = %v, want it to mention the missing device_code", err)
	}
	if _, tokenHits := server.counts(); tokenHits != 0 {
		t.Errorf("token endpoint hits = %d, want 0 (no polling)", tokenHits)
	}
}

// rfc8628CountingTransport counts round trips so a test can prove the
// configured client — not http.DefaultClient — was used.
type rfc8628CountingTransport struct {
	base http.RoundTripper
	hits *int64
}

func (c *rfc8628CountingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt64(c.hits, 1)
	return c.base.RoundTrip(req)
}

func TestRFC8628UsesConfiguredHTTPClient(t *testing.T) {
	server := newRFC8628TestServer(
		t,
		ptrRFC8628Response(rfc8628JSON(http.StatusOK, rfc8628DeviceBody("900", "1"))),
		rfc8628JSON(http.StatusOK, `{"access_token":"`+rfc8628TestAccessToken+`"}`),
	)
	cfg := rfc8628TestConfig(server)

	var hits int64
	cfg.HTTPClient = &http.Client{Transport: &rfc8628CountingTransport{base: server.srv.Client().Transport, hits: &hits}}

	cred, err := LoginDeviceCodeRFC8628(context.Background(), cfg, newRFC8628Recorder().options())
	if err != nil {
		t.Fatalf("LoginDeviceCodeRFC8628: %v", err)
	}
	if cred == nil {
		t.Fatal("credential = nil, want non-nil")
	}
	if got := atomic.LoadInt64(&hits); got <= 0 {
		t.Errorf("configured HTTP client round trips = %d, want > 0", got)
	}
}

func TestRFC8628ContextCancellation(t *testing.T) {
	server := newRFC8628TestServer(
		t,
		ptrRFC8628Response(rfc8628JSON(http.StatusOK, rfc8628DeviceBody("900", "1"))),
		rfc8628JSON(http.StatusOK, `{"access_token":"`+rfc8628TestAccessToken+`"}`),
	)
	cfg := rfc8628TestConfig(server)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	recorder := newRFC8628Recorder()
	recorder.cancel = cancel // cancellation lands while the loop is sleeping
	opts := recorder.options()

	done := make(chan struct{})
	var (
		cred *AuthCredential
		err  error
	)
	go func() {
		defer close(done)
		cred, err = LoginDeviceCodeRFC8628(ctx, cfg, opts)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("LoginDeviceCodeRFC8628 did not return after cancellation")
	}

	if err == nil {
		t.Fatal("error = nil, want the context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error = %v, want it to mention the cancellation", err)
	}
	if cred != nil {
		t.Errorf("credential = %+v, want nil", cred)
	}
	if _, tokenHits := server.counts(); tokenHits != 0 {
		t.Errorf("token endpoint hits = %d, want 0", tokenHits)
	}
}

func TestRFC8628VerifyURLFallback(t *testing.T) {
	// The device response carries no URI at all: cfg.DeviceVerifyURL must be used
	// for the user-facing instructions instead of failing validation.
	server := newRFC8628TestServer(
		t,
		ptrRFC8628Response(rfc8628JSON(http.StatusOK, fmt.Sprintf(`{"device_code":%q,"user_code":%q,"expires_in":900,"interval":1}`, rfc8628TestDeviceCode, rfc8628TestUserCode))),
		rfc8628JSON(http.StatusOK, `{"access_token":"`+rfc8628TestAccessToken+`"}`),
	)
	cfg := rfc8628TestConfig(server)

	collected := &strings.Builder{}
	opts := newRFC8628Recorder().options()
	opts.Out = collected

	cred, err := LoginDeviceCodeRFC8628(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("LoginDeviceCodeRFC8628: %v", err)
	}
	if cred == nil {
		t.Fatal("credential = nil, want non-nil")
	}

	out := collected.String()
	if !strings.Contains(out, cfg.DeviceVerifyURL) {
		t.Errorf("output does not use the DeviceVerifyURL fallback:\n%s", out)
	}
	if !strings.Contains(out, rfc8628TestUserCode) {
		t.Errorf("output does not contain the user code:\n%s", out)
	}
	if strings.Contains(out, rfc8628TestDeviceCode) {
		t.Errorf("output leaks the device_code:\n%s", out)
	}
}

// ptrRFC8628Response is a tiny helper so the canned device response can be
// passed as *rfc8628HTTPResponse (nil means "empty 200 {}").
func ptrRFC8628Response(resp rfc8628HTTPResponse) *rfc8628HTTPResponse {
	return &resp
}
