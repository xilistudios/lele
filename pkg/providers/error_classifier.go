package providers

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

// errorPattern defines a single pattern (string or regex) for error classification.
type errorPattern struct {
	substring string
	regex     *regexp.Regexp
}

func substr(s string) errorPattern { return errorPattern{substring: s} }
func rxp(r string) errorPattern    { return errorPattern{regex: regexp.MustCompile("(?i)" + r)} }

// Error patterns organized by FailoverReason, matching OpenClaw production (~40 patterns).
var (
	rateLimitPatterns = []errorPattern{
		rxp(`rate[_ ]limit`),
		substr("too many requests"),
		substr("429"),
		substr("exceeded your current quota"),
		rxp(`exceeded.*quota`),
		rxp(`resource has been exhausted`),
		rxp(`resource.*exhausted`),
		substr("resource_exhausted"),
		substr("quota exceeded"),
		substr("usage limit"),
	}

	overloadedPatterns = []errorPattern{
		rxp(`overloaded_error`),
		rxp(`"type"\s*:\s*"overloaded_error"`),
		substr("overloaded"),
	}

	timeoutPatterns = []errorPattern{
		substr("timeout"),
		substr("timed out"),
		substr("deadline exceeded"),
		substr("context deadline exceeded"),
	}

	billingPatterns = []errorPattern{
		rxp(`\b402\b`),
		substr("payment required"),
		substr("insufficient credits"),
		substr("credit balance"),
		substr("plans & billing"),
		substr("insufficient balance"),
	}

	authPatterns = []errorPattern{
		rxp(`invalid[_ ]?api[_ ]?key`),
		substr("incorrect api key"),
		substr("invalid token"),
		substr("authentication"),
		substr("re-authenticate"),
		substr("oauth token refresh failed"),
		substr("unauthorized"),
		substr("forbidden"),
		substr("access denied"),
		substr("expired"),
		substr("token has expired"),
		rxp(`\b401\b`),
		rxp(`\b403\b`),
		substr("no credentials found"),
		substr("no api key found"),
	}

	// networkPatterns covers transport-level failures: Go's net/http, the TLS
	// stack, the DNS resolver and the error strings SDKs emit when the wire
	// breaks mid-stream. None of them say anything about the request being
	// wrong or the credentials being bad, so they are all transient and are
	// mapped to FailoverTimeout (which already means "retry with backoff").
	//
	// This group exists because a *missing* classification used to be fatal:
	// see ClassifyError, which now defaults unknown errors to transient.
	// Deliberately avoids the word "expired" (authPatterns matches it) and any
	// form of "canceled", which stays terminal (see isCancellation).
	networkPatterns = []errorPattern{
		substr("unexpected eof"),
		substr("eof"),
		substr("connection reset"),
		substr("connection refused"),
		substr("broken pipe"),
		substr("no such host"),
		substr("server misbehaving"),
		substr("network is unreachable"),
		substr("connection semiclosed"),
		substr("use of closed network connection"),
		substr("tls handshake"),
		substr("tls: use of closed connection"),
		substr("malformed http response"),
		substr("http: server gave whitespace"),
		substr("wsarecv"),
		substr("wsasend"),
		substr("operation timed out"),
		substr("i/o timeout"),
		substr("read: connection"),
		substr("dial tcp"),
		substr("stream disconnected"),
		substr("stream ended before completion"),
		substr("go stream error"),
		substr("incomplete json"),
		substr("unexpected end of json input"),
	}

	formatPatterns = []errorPattern{
		substr("string should match pattern"),
		substr("tool_use.id"),
		substr("tool_use_id"),
		substr("messages.1.content.1.tool_use.id"),
		substr("invalid request format"),
	}

	imageDimensionPatterns = []errorPattern{
		rxp(`image dimensions exceed max`),
	}

	imageSizePatterns = []errorPattern{
		rxp(`image exceeds.*mb`),
	}

	// Transient HTTP status codes that map to timeout (server-side failures).
	transientStatusCodes = map[int]bool{
		500: true, 502: true, 503: true,
		521: true, 522: true, 523: true, 524: true,
		529: true,
	}
)

// ClassifyError classifies an error into a FailoverError with reason.
//
// Default-to-transient: an error that matches no known pattern is classified as
// FailoverUnknown, which callers treat as retriable. Returning nil for unknown
// errors used to be fatalistic - it made FallbackChain abort without trying the
// next candidate and killed the agent's transient-retry loop on perfectly
// recoverable transport failures (unexpected EOF, connection reset, TLS
// handshake timeout, malformed HTTP responses, ...).
//
// nil is returned only for:
//   - err == nil
//   - context cancellation (user abort / /stop), which is always terminal.
func ClassifyError(err error, provider, model string) *FailoverError {
	if err == nil {
		return nil
	}

	// Context cancellation: user abort, never fallback. errors.Is (plus the
	// canonical message) so a fmt.Errorf("%w") wrapper cannot escape it.
	if isCancellation(err) {
		return nil
	}

	// Context deadline exceeded: treat as timeout, always fallback.
	if errors.Is(err, context.DeadlineExceeded) {
		return &FailoverError{
			Reason:   FailoverTimeout,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	// Structured path first: a provider that gives us the status code and the
	// server's wait hint as data must not be re-parsed out of its own message.
	// This is checked before any message matching because the message of an
	// API error embeds the response body, and a body containing e.g. "401" or
	// "invalid api key" is about the *request that just failed*, whereas the
	// status code is what the server actually answered with.
	if fe, ok := classifyStructured(err, provider, model); ok {
		return fe
	}

	msg := strings.ToLower(err.Error())

	// Image dimension/size errors: non-retriable, non-fallback.
	if IsImageDimensionError(msg) || IsImageSizeError(msg) {
		return &FailoverError{
			Reason:   FailoverFormat,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	// Try HTTP status code extraction first.
	if status := extractHTTPStatus(msg); status > 0 {
		if reason := classifyByStatus(status); reason != "" {
			return &FailoverError{
				Reason:   reason,
				Provider: provider,
				Model:    model,
				Status:   status,
				Wrapped:  err,
			}
		}
	}

	// Message pattern matching (priority order from OpenClaw).
	if reason := classifyByMessage(msg); reason != "" {
		return &FailoverError{
			Reason:   reason,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	// Context-window overflow is evaluated LAST among the known reasons, on
	// purpose: its generic patterns ("token limit", "too long", "context
	// length") also occur inside quota and rate-limit bodies, e.g.
	// "429 ... you exceeded your daily token limit". Checking overflow earlier
	// would eclipse those and mark a transient quota error as terminal, which
	// is precisely the failure mode this file exists to eliminate.
	//
	// The payload is too large for the model, so the failure repeats identically
	// no matter how often it is retried or which candidate receives it; it is a
	// request-size problem like the image cases above, hence FailoverFormat.
	// Without this branch the error would fall through to FailoverUnknown and
	// the chain would burn its whole retry budget on an oversized prompt instead
	// of surfacing it to the caller's summarization/compaction path, which is the
	// only thing that can actually fix it (see llmCaller.executeWithRetry).
	if IsContextOverflowError(err) {
		return &FailoverError{
			Reason:   FailoverFormat,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	// Unknown error: assume transient. Retrying a hiccup is cheap; killing a
	// session because we did not recognise a string is not.
	return &FailoverError{
		Reason:   FailoverUnknown,
		Provider: provider,
		Model:    model,
		Wrapped:  err,
	}
}

// httpStatusError is the local, minimal contract a provider error may
// implement to hand the classifier structured data instead of prose.
//
// It is deliberately an interface rather than a direct dependency on
// *common.APIError: the classifier stays decoupled from the concrete type (and
// from the common package's import graph), so any provider, SDK shim or test
// double that exposes these two accessors gets the structured path for free.
type httpStatusError interface {
	error
	HTTPStatus() int
	RetryAfterHint() time.Duration
}

// classifyStructured tries to classify err from its structured fields.
// ok is false when err does not carry an HTTP status, so the caller falls back
// to the message-based heuristics used by providers that only return strings
// (bedrock, claude_cli, codex, antigravity, github_copilot, ...).
//
// A status that maps to a known reason wins outright. An unmapped status
// (404, 409, 413, ...) still consults the body patterns, and if nothing
// matches it becomes FailoverUnknown - transient - because an unrecognised
// status says nothing about the request being wrong.
func classifyStructured(err error, provider, model string) (fe *FailoverError, ok bool) {
	var hs httpStatusError
	if !errors.As(err, &hs) {
		return nil, false
	}
	status := hs.HTTPStatus()
	if status <= 0 {
		return nil, false
	}
	hint := hs.RetryAfterHint()
	if hint < 0 {
		hint = 0
	}

	fe = &FailoverError{
		Provider:   provider,
		Model:      model,
		Status:     status,
		RetryAfter: hint,
		Wrapped:    err,
	}
	if reason := classifyByStatus(status); reason != "" {
		fe.Reason = reason
		return fe, true
	}
	if reason := classifyByMessage(strings.ToLower(err.Error())); reason != "" {
		fe.Reason = reason
		return fe, true
	}
	// Body patterns exhausted: the remaining checks are the terminal,
	// request-shape ones, in the same order as the string path.
	//
	// Image dimension/size complaints are checked before overflow (as in the
	// string path) and both are terminal: the same payload fails at every
	// candidate, so FailoverFormat beats the FailoverUnknown fallback.
	lower := strings.ToLower(err.Error())
	if IsImageDimensionError(lower) || IsImageSizeError(lower) {
		fe.Reason = FailoverFormat
		return fe, true
	}
	// Context-window overflow is evaluated LAST among the known reasons so
	// quota bodies that mention "token limit" are not mistaken for a
	// prompt-size problem (see ClassifyError).
	if IsContextOverflowError(err) {
		fe.Reason = FailoverFormat
		return fe, true
	}
	fe.Reason = FailoverUnknown
	return fe, true
}

// isCancellation reports whether err represents a context cancellation, which
// is always terminal (the user pressed /stop or the run was aborted).
//
// errors.Is covers sentinels wrapped with %w (including *url.Error, which
// unwraps to its transport error). The message check covers SDKs that flatten
// the cancellation into a plain string and lose the sentinel.
func isCancellation(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "context canceled")
}

// classifyByStatus maps HTTP status codes to FailoverReason.
func classifyByStatus(status int) FailoverReason {
	switch {
	case status == 401 || status == 403:
		return FailoverAuth
	case status == 402:
		return FailoverBilling
	case status == 408:
		return FailoverTimeout
	case status == 429:
		return FailoverRateLimit
	case status == 400:
		return FailoverFormat
	case transientStatusCodes[status]:
		return FailoverTimeout
	}
	return ""
}

// classifyByMessage matches error messages against patterns.
// Priority order matters (from OpenClaw classifyFailoverReason).
func classifyByMessage(msg string) FailoverReason {
	if matchesAny(msg, rateLimitPatterns) {
		return FailoverRateLimit
	}
	if matchesAny(msg, overloadedPatterns) {
		return FailoverRateLimit // Overloaded treated as rate_limit
	}
	if matchesAny(msg, billingPatterns) {
		return FailoverBilling
	}
	if matchesAny(msg, timeoutPatterns) {
		return FailoverTimeout
	}
	// Transport failures are transient: mapped to timeout so they get the same
	// backoff/retry treatment instead of failing fast. Placed after the
	// provider-facing groups (rate_limit/overloaded/billing/timeout) so a body
	// that mentions both "429" and a broken pipe is still a rate limit, and
	// before auth/format so "TLS handshake timeout" never reads as a
	// credential problem.
	if matchesAny(msg, networkPatterns) {
		return FailoverTimeout
	}
	if matchesAny(msg, authPatterns) {
		return FailoverAuth
	}
	if matchesAny(msg, formatPatterns) {
		return FailoverFormat
	}
	return ""
}

// extractHTTPStatus extracts an HTTP status code from an error message.
// Looks for patterns like "status: 429", "status 401", "HTTP 429".
//
// This is the FALLBACK path, kept on purpose: providers that do not build a
// common.APIError (bedrock, claude_cli, codex, antigravity, github_copilot and
// every SDK that only returns strings) still surface their status code inside
// the message text, and this is the only way to recover it. It is inherently
// fragile - a "429" inside a response body is indistinguishable from the real
// status here - which is why ClassifyError always tries the structured path
// (classifyStructured) first. New providers should return *common.APIError
// instead of relying on this.
func extractHTTPStatus(msg string) int {
	// Common patterns in Go HTTP error messages
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`status[:\s]+(\d{3})`),
		regexp.MustCompile(`HTTP[/\s]+\d*\.?\d*\s+(\d{3})`),
	}

	for _, p := range patterns {
		if m := p.FindStringSubmatch(msg); len(m) > 1 {
			return parseDigits(m[1])
		}
	}

	return 0
}

// IsImageDimensionError returns true if the message indicates an image dimension error.
func IsImageDimensionError(msg string) bool {
	return matchesAny(msg, imageDimensionPatterns)
}

// IsImageSizeError returns true if the message indicates an image file size error.
func IsImageSizeError(msg string) bool {
	return matchesAny(msg, imageSizePatterns)
}

// matchesAny checks if msg matches any of the patterns.
func matchesAny(msg string, patterns []errorPattern) bool {
	for _, p := range patterns {
		if p.regex != nil {
			if p.regex.MatchString(msg) {
				return true
			}
		} else if p.substring != "" {
			if strings.Contains(msg, p.substring) {
				return true
			}
		}
	}
	return false
}

// parseDigits converts a string of digits to an int.
func parseDigits(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}
