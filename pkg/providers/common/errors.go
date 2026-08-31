// Lele - Ultra-lightweight personal AI agent
// Copyright (c) 2026 Lele contributors

package common

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MaxRetryAfter caps how long we are willing to wait because a server asked
// for it. Providers are not trusted to be sane: a typo'd or hostile
// Retry-After of "86400" would otherwise park a session for a day. Exported so
// the retry layer (T3) can clamp the same way when it consumes the hint.
const MaxRetryAfter = 120 * time.Second

// APIError carries the structured failure of an HTTP request to an LLM API.
//
// Why: providers used to return fmt.Errorf("API request failed:\n  Status: %d
// ..."), which flattened the response into a string. The error classifier then
// had to rebuild the status code with a regex over that string, so a "429"
// occurring anywhere in the body was mistaken for the real status, and the
// server's Retry-After hint - the only number that says how long to actually
// wait - was thrown away at the source.
//
// Error() keeps the historical text format so existing tests, logs and message
// matching are unaffected; the structured data travels alongside it.
type APIError struct {
	StatusCode int
	RetryAfter time.Duration // server's wait hint, 0 if no usable hint was sent
	Body       string        // already-truncated body, rendered verbatim by Error()
	URL        string        // endpoint or apiBase, empty when the caller has none
	Message    string        // optional verbatim message overriding the canonical format

	err error // optional underlying error, exposed through Unwrap
}

// Error renders the message providers have always emitted. The two canonical
// shapes are pinned by tests in pkg/providers (error_classifier_test.go,
// fallback_test.go) and by log scrapers, so they must not drift:
//
//	"API request failed:\n  Status: %d\n  Body:   %s"
//	"API request failed:\n  Status: %d\n  Body:   %s\n URL: %s"
func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.URL != "" {
		return fmt.Sprintf("API request failed:\n  Status: %d\n  Body:   %s\n URL: %s",
			e.StatusCode, e.Body, e.URL)
	}
	return fmt.Sprintf("API request failed:\n  Status: %d\n  Body:   %s",
		e.StatusCode, e.Body)
}

// Unwrap exposes an underlying error when one exists (e.g. a transport error
// wrapped around the API failure).
func (e *APIError) Unwrap() error { return e.err }

// HTTPStatus returns the exact HTTP status code of the failed response.
func (e *APIError) HTTPStatus() int { return e.StatusCode }

// RetryAfterHint returns how long the server asked us to wait (0 = no hint).
// Consumers may use it as a *floor* for backoff, never as a hard schedule:
// see ParseRetryAfter for the trust ceiling applied here.
func (e *APIError) RetryAfterHint() time.Duration { return e.RetryAfter }

// WithError attaches an underlying error (returned by Unwrap) without changing
// the rendered message.
func (e *APIError) WithError(err error) *APIError {
	e.err = err
	return e
}

// NewAPIError builds an *APIError from a non-200 response and its already-read
// body. The body is rendered verbatim (no truncation): callers that truncate
// (see HandleErrorResponse) do so before calling, so each provider keeps the
// observable error text it has always had.
func NewAPIError(resp *http.Response, body []byte, apiBase string) *APIError {
	if resp == nil {
		return &APIError{Body: string(body), URL: apiBase}
	}
	return &APIError{
		StatusCode: resp.StatusCode,
		RetryAfter: ParseRetryAfter(resp.Header),
		Body:       string(body),
		URL:        apiBase,
	}
}

// ParseRetryAfter extracts, as a duration, how long the caller should wait
// before retrying. Sources are consulted in decreasing trust order and the
// first one that parses wins (even if it yields 0, which means "retry now"):
//
//  1. Retry-After: delta-seconds ("30", also fractional) or HTTP-date
//     ("Wed, 21 Oct 2015 07:28:00 GMT"), resolved against time.Now().
//  2. retry-after-ms: milliseconds, sent by OpenAI and Anthropic.
//  3. x-ratelimit-reset-requests / x-ratelimit-reset: fractional seconds from
//     OpenAI-compatible rate limiters.
//
// The result is always clamped to [0, MaxRetryAfter]. A missing, unparsable or
// negative hint yields 0, which callers read as "no hint".
func ParseRetryAfter(h http.Header) time.Duration {
	if h == nil {
		return 0
	}

	if d, ok := parseRetryAfterHeader(h.Get("Retry-After")); ok {
		return clampRetryAfter(d)
	}
	if ms := h.Get("retry-after-ms"); ms != "" {
		if v, err := strconv.ParseFloat(ms, 64); err == nil {
			return clampRetryAfter(time.Duration(v * float64(time.Millisecond)))
		}
	}
	for _, name := range []string{"x-ratelimit-reset-requests", "x-ratelimit-reset"} {
		if v := h.Get(name); v != "" {
			if secs, err := strconv.ParseFloat(v, 64); err == nil {
				return clampRetryAfter(time.Duration(secs * float64(time.Second)))
			}
		}
	}

	return 0
}

// parseRetryAfter handles the Retry-After grammar (RFC 9110 §10.2.3): either a
// non-negative number of seconds or an HTTP-date. ok is false when the value
// is absent or unparsable, so the caller can fall through to another header.
func parseRetryAfterHeader(v string) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		return time.Duration(secs * float64(time.Second)), true
	}
	if when, err := http.ParseTime(v); err == nil {
		return time.Until(when), true
	}
	return 0, false
}

// clampRetryAfter keeps the hint inside [0, MaxRetryAfter].
func clampRetryAfter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	if d > MaxRetryAfter {
		return MaxRetryAfter
	}
	return d
}
