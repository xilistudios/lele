package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers/common"
)

func TestClassifyError_Nil(t *testing.T) {
	result := ClassifyError(nil, "openai", "gpt-4")
	if result != nil {
		t.Errorf("expected nil for nil error, got %+v", result)
	}
}

func TestClassifyError_ContextCanceled(t *testing.T) {
	result := ClassifyError(context.Canceled, "openai", "gpt-4")
	if result != nil {
		t.Errorf("expected nil for context.Canceled (user abort), got %+v", result)
	}
}

func TestClassifyError_ContextDeadlineExceeded(t *testing.T) {
	result := ClassifyError(context.DeadlineExceeded, "openai", "gpt-4")
	if result == nil {
		t.Fatal("expected non-nil for deadline exceeded")
	}
	if result.Reason != FailoverTimeout {
		t.Errorf("reason = %q, want timeout", result.Reason)
	}
}

func TestClassifyError_StatusCodes(t *testing.T) {
	tests := []struct {
		status int
		reason FailoverReason
	}{
		{401, FailoverAuth},
		{403, FailoverAuth},
		{402, FailoverBilling},
		{408, FailoverTimeout},
		{429, FailoverRateLimit},
		{400, FailoverFormat},
		{500, FailoverTimeout},
		{502, FailoverTimeout},
		{503, FailoverTimeout},
		{521, FailoverTimeout},
		{522, FailoverTimeout},
		{523, FailoverTimeout},
		{524, FailoverTimeout},
		{529, FailoverTimeout},
	}

	for _, tt := range tests {
		err := fmt.Errorf("API error: status: %d something went wrong", tt.status)
		result := ClassifyError(err, "test", "model")
		if result == nil {
			t.Errorf("status %d: expected non-nil", tt.status)
			continue
		}
		if result.Reason != tt.reason {
			t.Errorf("status %d: reason = %q, want %q", tt.status, result.Reason, tt.reason)
		}
	}
}

func TestClassifyError_RateLimitPatterns(t *testing.T) {
	patterns := []string{
		"rate limit exceeded",
		"rate_limit reached",
		"too many requests",
		"exceeded your current quota",
		"resource has been exhausted",
		"resource_exhausted",
		"quota exceeded",
		"usage limit reached",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "openai", "gpt-4")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", msg)
			continue
		}
		if result.Reason != FailoverRateLimit {
			t.Errorf("pattern %q: reason = %q, want rate_limit", msg, result.Reason)
		}
	}
}

func TestClassifyError_OverloadedPatterns(t *testing.T) {
	patterns := []string{
		"overloaded_error",
		`{"type": "overloaded_error"}`,
		"server is overloaded",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "anthropic", "claude")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", msg)
			continue
		}
		if result.Reason != FailoverRateLimit {
			t.Errorf("pattern %q: reason = %q, want rate_limit", msg, result.Reason)
		}
	}
}

func TestClassifyError_BillingPatterns(t *testing.T) {
	patterns := []string{
		"payment required",
		"insufficient credits",
		"credit balance too low",
		"plans & billing page",
		"insufficient balance",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "openai", "gpt-4")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", msg)
			continue
		}
		if result.Reason != FailoverBilling {
			t.Errorf("pattern %q: reason = %q, want billing", msg, result.Reason)
		}
	}
}

func TestClassifyError_TimeoutPatterns(t *testing.T) {
	patterns := []string{
		"request timeout",
		"connection timed out",
		"deadline exceeded",
		"context deadline exceeded",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "openai", "gpt-4")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", msg)
			continue
		}
		if result.Reason != FailoverTimeout {
			t.Errorf("pattern %q: reason = %q, want timeout", msg, result.Reason)
		}
	}
}

func TestClassifyError_AuthPatterns(t *testing.T) {
	patterns := []string{
		"invalid api key",
		"invalid_api_key",
		"incorrect api key",
		"invalid token",
		"authentication failed",
		"re-authenticate",
		"oauth token refresh failed",
		"unauthorized access",
		"forbidden",
		"access denied",
		"expired",
		"token has expired",
		"no credentials found",
		"no api key found",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "openai", "gpt-4")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", msg)
			continue
		}
		if result.Reason != FailoverAuth {
			t.Errorf("pattern %q: reason = %q, want auth", msg, result.Reason)
		}
	}
}

func TestClassifyError_FormatPatterns(t *testing.T) {
	patterns := []string{
		"string should match pattern",
		"tool_use.id is required",
		"invalid tool_use_id",
		"messages.1.content.1.tool_use.id must be valid",
		"invalid request format",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "anthropic", "claude")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", msg)
			continue
		}
		if result.Reason != FailoverFormat {
			t.Errorf("pattern %q: reason = %q, want format", msg, result.Reason)
		}
	}
}

func TestClassifyError_ImageDimensionError(t *testing.T) {
	err := errors.New("image dimensions exceed max allowed 2048x2048")
	result := ClassifyError(err, "openai", "gpt-4o")
	if result == nil {
		t.Fatal("expected non-nil for image dimension error")
	}
	if result.Reason != FailoverFormat {
		t.Errorf("reason = %q, want format", result.Reason)
	}
	if result.IsRetriable() {
		t.Error("image dimension error should not be retriable")
	}
}

func TestClassifyError_ImageSizeError(t *testing.T) {
	err := errors.New("image exceeds 20 mb limit")
	result := ClassifyError(err, "openai", "gpt-4o")
	if result == nil {
		t.Fatal("expected non-nil for image size error")
	}
	if result.Reason != FailoverFormat {
		t.Errorf("reason = %q, want format", result.Reason)
	}
}

func TestClassifyError_UnknownError(t *testing.T) {
	err := errors.New("some completely random error")
	result := ClassifyError(err, "openai", "gpt-4")
	if result == nil {
		t.Fatal("expected non-nil: unknown errors are transient by default")
	}
	if result.Reason != FailoverUnknown {
		t.Errorf("reason = %q, want unknown", result.Reason)
	}
}

func TestClassifyError_ProviderModelPropagation(t *testing.T) {
	err := errors.New("rate limit exceeded")
	result := ClassifyError(err, "my-provider", "my-model")
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Provider != "my-provider" {
		t.Errorf("provider = %q, want my-provider", result.Provider)
	}
	if result.Model != "my-model" {
		t.Errorf("model = %q, want my-model", result.Model)
	}
}

func TestFailoverError_IsRetriable(t *testing.T) {
	tests := []struct {
		reason    FailoverReason
		retriable bool
	}{
		{FailoverAuth, true},
		{FailoverRateLimit, true},
		{FailoverBilling, true},
		{FailoverTimeout, true},
		{FailoverOverloaded, true},
		{FailoverFormat, false},
		{FailoverUnknown, true},
	}

	for _, tt := range tests {
		fe := &FailoverError{Reason: tt.reason}
		if fe.IsRetriable() != tt.retriable {
			t.Errorf("IsRetriable(%q) = %v, want %v", tt.reason, fe.IsRetriable(), tt.retriable)
		}
	}
}

func TestFailoverError_ErrorString(t *testing.T) {
	fe := &FailoverError{
		Reason:   FailoverRateLimit,
		Provider: "openai",
		Model:    "gpt-4",
		Status:   429,
		Wrapped:  errors.New("too many requests"),
	}
	s := fe.Error()
	if s == "" {
		t.Error("expected non-empty error string")
	}
}

func TestFailoverError_Unwrap(t *testing.T) {
	inner := errors.New("inner error")
	fe := &FailoverError{Reason: FailoverTimeout, Wrapped: inner}
	if fe.Unwrap() != inner {
		t.Error("Unwrap should return wrapped error")
	}
}

func TestExtractHTTPStatus(t *testing.T) {
	tests := []struct {
		msg  string
		want int
	}{
		{"status: 429 rate limited", 429},
		{"status 401 unauthorized", 401},
		{"HTTP/1.1 502 Bad Gateway", 502},
		{"no status code here", 0},
		{"random number 12345", 0},
	}

	for _, tt := range tests {
		got := extractHTTPStatus(tt.msg)
		if got != tt.want {
			t.Errorf("extractHTTPStatus(%q) = %d, want %d", tt.msg, got, tt.want)
		}
	}
}

func TestIsImageDimensionError(t *testing.T) {
	if !IsImageDimensionError("image dimensions exceed max 4096x4096") {
		t.Error("should match image dimensions exceed max")
	}
	if IsImageDimensionError("normal error message") {
		t.Error("should not match normal error")
	}
}

func TestIsImageSizeError(t *testing.T) {
	if !IsImageSizeError("image exceeds 20 mb") {
		t.Error("should match image exceeds mb")
	}
	if IsImageSizeError("normal error message") {
		t.Error("should not match normal error")
	}
}

func TestClassifyError_TransportPatterns(t *testing.T) {
	cases := []string{
		"unexpected EOF",
		"EOF",
		"read tcp 192.168.0.10:45678->104.18.1.2:443: read: connection reset by peer",
		"dial tcp: lookup api.example.com: no such host",
		"net/http: TLS handshake timeout",
		`malformed HTTP response "\x00\x12"`,
		"net/http: server gave whitespace response after handshake",
		"tls: use of closed connection",
		"connect: connection refused",
		"write tcp: broken pipe",
		"network is unreachable",
		"read unix @->/var/run/docker.sock: use of closed network connection",
		"wsarecv: An existing connection was forcibly closed by the remote host.",
		"wsasend: unknown error",
		"dial tcp 1.2.3.4:443: connect: operation timed out",
		"read tcp 10.0.0.1:443: i/o timeout",
		"stream disconnected before completion",
		"stream ended before completion",
		"openai: go stream error",
		"incomplete json",
		"unexpected end of JSON input",
		"lookup api.openai.com: server misbehaving",
		"connection semiclosed read",
	}

	for _, msg := range cases {
		err := errors.New(msg)
		result := ClassifyError(err, "openai", "gpt-4")
		if result == nil {
			t.Errorf("transport %q: expected non-nil (default-to-transient)", msg)
			continue
		}
		if result.Reason != FailoverTimeout {
			t.Errorf("transport %q: reason = %q, want timeout", msg, result.Reason)
		}
		if !result.IsRetriable() {
			t.Errorf("transport %q: must be retriable", msg)
		}
		if !IsRetriableError(err) {
			t.Errorf("IsRetriableError(%q) = false, want true", msg)
		}
	}
}

func TestClassifyError_UnknownErrorIsTransient(t *testing.T) {
	err := errors.New("weird sdk failure xyz")
	result := ClassifyError(err, "openai", "gpt-4")
	if result == nil {
		t.Fatal("expected non-nil: unknown errors are transient by default")
	}
	if result.Reason != FailoverUnknown {
		t.Errorf("reason = %q, want unknown", result.Reason)
	}
	if !result.IsRetriable() {
		t.Error("unknown error must be retriable")
	}
	if !result.ShouldBackoff() {
		t.Error("unknown error must back off instead of hammering the provider")
	}
	if result.IsTerminal() {
		t.Error("unknown error must not be terminal")
	}
	if !IsRetriableError(err) {
		t.Error("IsRetriableError(unknown) = false, want true")
	}
	if result.Wrapped != err {
		t.Error("Wrapped must hold the original error")
	}
	if result.Provider != "openai" || result.Model != "gpt-4" {
		t.Errorf("provider/model = %q/%q, want openai/gpt-4", result.Provider, result.Model)
	}
}

func TestClassifyError_CanceledIsStillNil(t *testing.T) {
	cases := []error{
		context.Canceled,
		fmt.Errorf("wrap: %w", context.Canceled),
		fmt.Errorf("stream read: %w", context.Canceled),
		errors.New("context canceled"),
		fmt.Errorf("request failed: %s", "context canceled"),
	}

	for _, err := range cases {
		if result := ClassifyError(err, "openai", "gpt-4"); result != nil {
			t.Errorf("ClassifyError(%v) = %+v, want nil (cancellation is terminal)", err, result)
		}
		if IsRetriableError(err) {
			t.Errorf("IsRetriableError(%v) = true, want false", err)
		}
	}
}

func TestClassifyError_WrappedDeadlineExceeded(t *testing.T) {
	err := fmt.Errorf("provider call failed: %w", context.DeadlineExceeded)
	result := ClassifyError(err, "openai", "gpt-4")
	if result == nil {
		t.Fatal("expected non-nil for wrapped deadline exceeded")
	}
	if result.Reason != FailoverTimeout {
		t.Errorf("reason = %q, want timeout", result.Reason)
	}
	if !IsRetriableError(err) {
		t.Error("IsRetriableError(wrapped deadline) = false, want true")
	}
}

func TestClassifyError_MissingWireSessionIsTerminal(t *testing.T) {
	cases := []error{
		common.ErrMissingWireSession,
		fmt.Errorf("setting request headers: %w", common.ErrMissingWireSession),
	}
	for _, err := range cases {
		if IsRetriableError(err) {
			t.Errorf("IsRetriableError(%v) = true, want false", err)
		}
		result := ClassifyError(err, "opencode_go", "grok-4.7")
		if result == nil {
			t.Fatalf("ClassifyError(%v) = nil, want a classification", err)
		}
		if result.Reason != FailoverFormat {
			t.Errorf("ClassifyError(%v) reason = %q, want format", err, result.Reason)
		}
		if !result.IsTerminal() {
			t.Errorf("ClassifyError(%v) must be terminal", err)
		}
		if result.IsRetriable() {
			t.Errorf("ClassifyError(%v) must not be retriable", err)
		}
		if result.ShouldBackoff() {
			t.Errorf("ClassifyError(%v) must not back off", err)
		}
		if !errors.Is(result.Wrapped, common.ErrMissingWireSession) {
			t.Errorf("ClassifyError(%v).Wrapped = %v, must wrap the sentinel", err, result.Wrapped)
		}
	}
}

func TestClassifyError_PatternPriority(t *testing.T) {
	cases := []struct {
		msg  string
		want FailoverReason
	}{
		{"Post \"https://api.openai.com/v1/chat\": unexpected EOF (status: 429)", FailoverRateLimit},
		{"too many requests: connection reset by peer", FailoverRateLimit},
		{"overloaded, connection refused", FailoverRateLimit},
		{"payment required, broken pipe", FailoverBilling},
		{"net/http: TLS handshake timeout", FailoverTimeout},
		{"request timeout while reading stream", FailoverTimeout},
		{"read tcp: connection reset by peer", FailoverTimeout},
		{"malformed HTTP response", FailoverTimeout},
		{"stream disconnected before completion", FailoverTimeout},
		{"invalid api key", FailoverAuth},
		{"token has expired", FailoverAuth},
		{"expired subscription", FailoverAuth},
		{"invalid request format", FailoverFormat},
	}

	for _, tt := range cases {
		result := ClassifyError(errors.New(tt.msg), "openai", "gpt-4")
		if result == nil {
			t.Errorf("msg %q: expected non-nil", tt.msg)
			continue
		}
		if result.Reason != tt.want {
			t.Errorf("msg %q: reason = %q, want %q", tt.msg, result.Reason, tt.want)
		}
	}
}

func TestNetworkPatternsDoNotMatchExpired(t *testing.T) {
	for _, p := range networkPatterns {
		if strings.Contains(p.substring, "expired") || strings.Contains(p.substring, "cancel") {
			t.Errorf("network pattern %q must not overlap terminal vocabulary", p.substring)
		}
	}
	if got := classifyByMessage("token has expired"); got != FailoverAuth {
		t.Errorf("\"token has expired\" = %q, want auth", got)
	}
}

func TestFailoverError_ShouldBackoff(t *testing.T) {
	cases := []struct {
		reason FailoverReason
		want   bool
	}{
		{FailoverRateLimit, true},
		{FailoverOverloaded, true},
		{FailoverTimeout, true},
		{FailoverUnknown, true},
		{FailoverAuth, false},
		{FailoverBilling, false},
		{FailoverFormat, false},
	}

	for _, tt := range cases {
		fe := &FailoverError{Reason: tt.reason}
		if got := fe.ShouldBackoff(); got != tt.want {
			t.Errorf("ShouldBackoff(%q) = %v, want %v", tt.reason, got, tt.want)
		}
	}
}

func TestFailoverError_IsTerminal(t *testing.T) {
	cases := []struct {
		reason FailoverReason
		want   bool
	}{
		{FailoverAuth, true},
		{FailoverBilling, true},
		{FailoverFormat, true},
		{FailoverTimeout, false},
		{FailoverRateLimit, false},
		{FailoverOverloaded, false},
		{FailoverUnknown, false},
		{FailoverReason(""), false},
	}

	for _, tt := range cases {
		fe := &FailoverError{Reason: tt.reason}
		if got := fe.IsTerminal(); got != tt.want {
			t.Errorf("IsTerminal(%q) = %v, want %v", tt.reason, got, tt.want)
		}
	}
}

func TestFailoverError_IsRetriableVsIsTerminal(t *testing.T) {
	for _, reason := range []FailoverReason{FailoverAuth, FailoverBilling} {
		fe := &FailoverError{Reason: reason}
		if !fe.IsRetriable() {
			t.Errorf("IsRetriable(%q) = false, want true (next candidate may work)", reason)
		}
		if !fe.IsTerminal() {
			t.Errorf("IsTerminal(%q) = false, want true (retrying the same request is useless)", reason)
		}
	}
	fe := &FailoverError{Reason: FailoverFormat}
	if fe.IsRetriable() || !fe.IsTerminal() {
		t.Errorf("format: IsRetriable=%v IsTerminal=%v, want false/true", fe.IsRetriable(), fe.IsTerminal())
	}
}

func TestClassifyError_ContextOverflowIsTerminal(t *testing.T) {
	msgs := []string{
		"InvalidParameter: Total tokens of image and text exceed max message tokens",
		"error: code = 400 message = context_length_exceeded",
		"prompt is too long: 250000 tokens > 200000 maximum",
		"this model's maximum context length is 8192 tokens",
		"ValidationException: too many input tokens",
	}

	for _, msg := range msgs {
		err := errors.New(msg)
		result := ClassifyError(err, "bedrock", "claude")
		if result == nil {
			t.Errorf("overflow %q: expected non-nil", msg)
			continue
		}
		if result.Reason != FailoverFormat {
			t.Errorf("overflow %q: reason = %q, want format", msg, result.Reason)
		}
		if !result.IsTerminal() {
			t.Errorf("overflow %q: must be terminal", msg)
		}
		if result.IsRetriable() {
			t.Errorf("overflow %q: must not try another candidate", msg)
		}
		if IsRetriableError(err) {
			t.Errorf("IsRetriableError(%q) = true, want false", msg)
		}
	}
}

func TestClassifyError_DeadlineMentioningContextStaysTransient(t *testing.T) {
	err := errors.New("context deadline exceeded")
	result := ClassifyError(err, "openai", "gpt-4")
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Reason != FailoverTimeout {
		t.Errorf("reason = %q, want timeout", result.Reason)
	}
	if !IsRetriableError(err) {
		t.Error("deadline exceeded must be retriable")
	}
}

func TestClassifyError_QuotaNotEclipsedByOverflow(t *testing.T) {
	cases := []struct {
		msg  string
		want FailoverReason
	}{
		{"API request failed:\n  Status: 429\n  Body: you exceeded your daily token limit", FailoverRateLimit},
		{"429 too many requests: monthly token limit reached", FailoverRateLimit},
		{"status: 429 Your quota is too long to wait, retry later", FailoverRateLimit},
		{"API request failed:\n  Status: 400\n  Body: context_length_exceeded", FailoverFormat},
		{"prompt is too long: 250000 tokens > 200000 maximum", FailoverFormat},
	}

	for _, tt := range cases {
		result := ClassifyError(errors.New(tt.msg), "openai", "gpt-4")
		if result == nil {
			t.Errorf("msg %q: expected non-nil", tt.msg)
			continue
		}
		if result.Reason != tt.want {
			t.Errorf("msg %q: reason = %q, want %q", tt.msg, result.Reason, tt.want)
		}
		if tt.want == FailoverRateLimit && result.IsTerminal() {
			t.Errorf("msg %q: quota must not be terminal", tt.msg)
		}
	}
}

type stubAPIError struct {
	status int
	hint   time.Duration
	body   string
}

func (e *stubAPIError) Error() string {
	return fmt.Sprintf("API request failed:\n  Status: %d\n  Body:   %s", e.status, e.body)
}

func (e *stubAPIError) HTTPStatus() int               { return e.status }
func (e *stubAPIError) RetryAfterHint() time.Duration { return e.hint }

func TestClassifyError_Structured_RateLimitCarriesHint(t *testing.T) {
	err := &stubAPIError{status: 429, hint: 30 * time.Second, body: "slow down"}
	result := ClassifyError(err, "openai", "gpt-4o")
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Reason != FailoverRateLimit {
		t.Errorf("reason = %q, want %q", result.Reason, FailoverRateLimit)
	}
	if result.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", result.RetryAfter)
	}
	if result.Status != 429 {
		t.Errorf("Status = %d, want 429", result.Status)
	}
	if result.Wrapped != err {
		t.Error("Wrapped should be the original error")
	}
	if result.IsTerminal() {
		t.Error("rate limit must not be terminal")
	}
	if !result.ShouldBackoff() {
		t.Error("rate limit should back off")
	}
}

func TestClassifyError_Structured_BadGatewayIsTimeout(t *testing.T) {
	result := ClassifyError(&stubAPIError{status: 502, body: "<html>502</html>"}, "openai", "gpt-4o")
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Reason != FailoverTimeout {
		t.Errorf("reason = %q, want %q", result.Reason, FailoverTimeout)
	}
	if result.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0 (no hint sent)", result.RetryAfter)
	}
	if !IsRetriableError(result) {
		t.Error("502 must be retriable")
	}
}

func TestClassifyError_Structured_UnmappedStatusIsTransientNotFormat(t *testing.T) {
	result := ClassifyError(&stubAPIError{status: 404, body: "no route for this deployment"}, "openai", "gpt-4o")
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Reason != FailoverUnknown {
		t.Errorf("reason = %q, want %q", result.Reason, FailoverUnknown)
	}
	if result.Reason == FailoverFormat {
		t.Error("unmapped status must not be classified as format")
	}
	if result.Status != 404 {
		t.Errorf("Status = %d, want 404", result.Status)
	}
	if !result.IsRetriable() {
		t.Error("FailoverUnknown must stay retriable")
	}
}

func TestClassifyError_Structured_MappedStatusPrecedesQuotaBody(t *testing.T) {
	result := ClassifyError(&stubAPIError{status: 400, body: "error: rate limit exceeded, you exceeded your daily token limit"}, "openai", "gpt-4o")
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Reason != FailoverFormat {
		t.Errorf("reason = %q, want %q (mapped status must win over body patterns)", result.Reason, FailoverFormat)
	}
	if !result.IsTerminal() {
		t.Error("400+quota-body is terminal by design: the request itself was rejected")
	}
	viaMessage := ClassifyError(errors.New("you exceeded your daily token limit: rate limit exceeded"), "openai", "gpt-4o")
	if viaMessage == nil {
		t.Fatal("expected non-nil for message path")
	}
	if viaMessage.Reason != FailoverRateLimit {
		t.Errorf("message path reason = %q, want %q (body patterns decide when no status exists)", viaMessage.Reason, FailoverRateLimit)
	}
}

func TestClassifyError_Structured_BodyCannotSpoofStatus(t *testing.T) {
	err := &stubAPIError{status: 429, hint: 5 * time.Second, body: "invalid api key 401 unauthorized"}
	result := ClassifyError(err, "openai", "gpt-4o")
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Reason != FailoverRateLimit {
		t.Errorf("reason = %q, want %q (body text must not override the status)", result.Reason, FailoverRateLimit)
	}
	if result.IsTerminal() {
		t.Error("a 429 must not become terminal because its body mentions a key")
	}
}

func TestClassifyError_Structured_UnmappedStatusFallsBackToBodyPatterns(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   FailoverReason
	}{
		{"409 format body", 409, "invalid request format", FailoverFormat},
		{"404 quota body", 404, "exceeded your current quota", FailoverRateLimit},
		{"413 overflow body", 413, "prompt is too long: 250000 tokens > 200000 maximum", FailoverFormat},
		{"418 nothing matches", 418, "teapot", FailoverUnknown},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyError(&stubAPIError{status: tt.status, body: tt.body}, "openai", "gpt-4o")
			if result == nil {
				t.Fatal("expected non-nil")
			}
			if result.Reason != tt.want {
				t.Errorf("reason = %q, want %q", result.Reason, tt.want)
			}
		})
	}
}

func TestClassifyError_RequestBodyTooLargeIsFormatAndRecoverable(t *testing.T) {
	err := &stubAPIError{status: 400, body: `{"error":{"message":"failed to read request body: http: request body too large","type":"invalid_request_error"}}`}
	result := ClassifyError(err, "openai", "gpt-4o")
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Reason != FailoverFormat {
		t.Errorf("reason = %q, want %q", result.Reason, FailoverFormat)
	}
	if !result.IsTerminal() {
		t.Error("400 must stay terminal")
	}
	if !IsContextOverflowError(err) {
		t.Error("gateway body-too-large must be detected as overflow so shrink-and-retry runs")
	}
	if !IsRequestSizeError(err) {
		t.Error("gateway body-too-large must be a request-size error")
	}
}

func TestClassifyError_Status413IsFormat(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"413 payload too large body", "payload too large"},
		{"413 unrelated body", "teapot"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyError(&stubAPIError{status: 413, body: tt.body}, "openai", "gpt-4o")
			if result == nil {
				t.Fatal("expected non-nil")
			}
			if result.Reason != FailoverFormat {
				t.Errorf("reason = %q, want %q (413 is a request-shape rejection)", result.Reason, FailoverFormat)
			}
			if result.Status != 413 {
				t.Errorf("Status = %d, want 413", result.Status)
			}
			if !result.IsTerminal() {
				t.Error("413 must be terminal: the identical payload fails everywhere")
			}
		})
	}
}

func TestClassifyError_ImageRejectionIsRecoverable(t *testing.T) {
	err := &stubAPIError{status: 400, body: "image dimensions exceed max allowed size"}
	result := ClassifyError(err, "openai", "gpt-4o")
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Reason != FailoverFormat {
		t.Errorf("reason = %q, want %q", result.Reason, FailoverFormat)
	}
	if !result.IsTerminal() {
		t.Error("image dimension rejection must be terminal")
	}
	if !IsContextOverflowError(err) {
		t.Error("image dimension rejection must be detected as recoverable overflow")
	}
}

func TestClassifyError_Structured_ThroughWrappers(t *testing.T) {
	inner := &stubAPIError{status: 429, hint: 12 * time.Second, body: "too many requests"}
	wrapped := fmt.Errorf("chat completion: %w", inner)
	twice := fmt.Errorf("provider openai: %w", wrapped)

	for _, err := range []error{wrapped, twice} {
		result := ClassifyError(err, "openai", "gpt-4o")
		if result == nil {
			t.Fatalf("ClassifyError(%v) = nil, want non-nil", err)
		}
		if result.Reason != FailoverRateLimit {
			t.Errorf("reason = %q, want %q", result.Reason, FailoverRateLimit)
		}
		if result.RetryAfter != 12*time.Second {
			t.Errorf("RetryAfter = %v, want 12s", result.RetryAfter)
		}
	}
}

func TestClassifyError_Structured_CancellationStillTerminal(t *testing.T) {
	if result := ClassifyError(context.Canceled, "openai", "gpt-4o"); result != nil {
		t.Errorf("context.Canceled should classify as nil (terminal), got %+v", result)
	}
	if IsRetriableError(&stubAPIError{status: 429, body: "context canceled"}) {
		t.Error("a cancelled request must not be retried")
	}
}

func TestClassifyError_CommonAPIErrorSatisfiesStructuredPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
	}))
	defer server.Close()

	resp, err := http.Post(server.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST error = %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body error = %v", err)
	}

	apiErr := common.NewAPIError(resp, body, resp.Request.URL.String())
	result := ClassifyError(fmt.Errorf("chat: %w", apiErr), "openai", "gpt-4o")
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Reason != FailoverRateLimit {
		t.Errorf("reason = %q, want %q", result.Reason, FailoverRateLimit)
	}
	if result.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %v, want 7s", result.RetryAfter)
	}
	if result.Status != 429 {
		t.Errorf("Status = %d, want 429", result.Status)
	}
}

func TestClassifyError_StringPathStillWorksForUnmigratedProviders(t *testing.T) {
	msg := "API request failed:\n  Status: 429\n  Body: you exceeded your daily token limit"
	result := ClassifyError(errors.New(msg), "bedrock", "claude")
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Reason != FailoverRateLimit {
		t.Errorf("reason = %q, want %q", result.Reason, FailoverRateLimit)
	}
	if result.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0 (a string cannot carry a hint)", result.RetryAfter)
	}
}

func TestFailoverError_ErrorStringIncludesRetryAfter(t *testing.T) {
	inner := errors.New("too many requests")
	noHint := &FailoverError{Reason: FailoverRateLimit, Provider: "openai", Model: "gpt-4", Status: 429, Wrapped: inner}
	if strings.Contains(noHint.Error(), "retry_after") {
		t.Errorf("message must keep the historical format when there is no hint, got %q", noHint.Error())
	}
	if !strings.HasPrefix(noHint.Error(), "failover(rate_limit): provider=openai model=gpt-4 status=429: ") {
		t.Errorf("unexpected prefix: %q", noHint.Error())
	}

	hinted := &FailoverError{
		Reason:     FailoverRateLimit,
		Provider:   "openai",
		Model:      "gpt-4",
		Status:     429,
		RetryAfter: 30 * time.Second,
		Wrapped:    inner,
	}
	if !strings.Contains(hinted.Error(), " retry_after=30s:") {
		t.Errorf("expected retry_after in message, got %q", hinted.Error())
	}

	fractional := &FailoverError{Reason: FailoverRateLimit, Status: 429, RetryAfter: 1500 * time.Millisecond, Wrapped: inner}
	if !strings.Contains(fractional.Error(), " retry_after=1.5s:") {
		t.Errorf("expected fractional retry_after, got %q", fractional.Error())
	}
}
