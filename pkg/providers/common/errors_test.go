// Lele - Ultra-lightweight personal AI agent
// Copyright (c) 2026 Lele contributors

package common

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// hdr builds an http.Header from alternating key/value pairs.
func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func TestParseRetryAfter(t *testing.T) {
	// A future HTTP-date, computed at table-construction time so the test is
	// not sensitive to when it runs.
	future := time.Now().Add(45 * time.Second).UTC().Format(http.TimeFormat)
	farFuture := time.Now().Add(24 * time.Hour).UTC().Format(http.TimeFormat)
	past := time.Now().Add(-30 * time.Second).UTC().Format(http.TimeFormat)

	tests := []struct {
		name string
		head http.Header
		want time.Duration
	}{
		{"delta seconds", hdr("Retry-After", "30"), 30 * time.Second},
		{"zero", hdr("Retry-After", "0"), 0},
		{"absent", hdr(), 0},
		{"nil header", nil, 0},
		{"not a number and not a date", hdr("Retry-After", "not-a-number"), 0},
		{"negative seconds", hdr("Retry-After", "-5"), 0},
		{"http-date in the past", hdr("Retry-After", past), 0},
		{"http-date in the future", hdr("Retry-After", future), -1}, // range-checked below
		{"http-date far in the future is clamped", hdr("Retry-After", farFuture), MaxRetryAfter},
		{"absurd value is clamped", hdr("Retry-After", "999999"), MaxRetryAfter},
		{"fractional seconds", hdr("Retry-After", "1.5"), 1500 * time.Millisecond},
		{"surrounding whitespace", hdr("Retry-After", "  12 "), 12 * time.Second},
		{"retry-after-ms", hdr("retry-after-ms", "1500"), 1500 * time.Millisecond},
		{"retry-after-ms zero", hdr("retry-after-ms", "0"), 0},
		{"x-ratelimit-reset-requests fractional", hdr("x-ratelimit-reset-requests", "12.5"), 12500 * time.Millisecond},
		{"x-ratelimit-reset", hdr("x-ratelimit-reset", "8"), 8 * time.Second},
		{"x-ratelimit-reset clamped", hdr("x-ratelimit-reset", "3600"), MaxRetryAfter},
		// Precedence: the standard header wins when it parses, even if the
		// secondary sources disagree.
		{"Retry-After wins over ms", hdr("Retry-After", "30", "retry-after-ms", "1500"), 30 * time.Second},
		{"ms wins over ratelimit-reset", hdr("retry-after-ms", "1500", "x-ratelimit-reset-requests", "12.5"), 1500 * time.Millisecond},
		// An unparsable primary falls through to the secondary source.
		{"falls through to ms", hdr("Retry-After", "garbage", "retry-after-ms", "2000"), 2 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRetryAfter(tt.head)
			if tt.want < 0 {
				// HTTP-date has one-second resolution, so the remaining wait is
				// 45s minus however long the test took to get here.
				if got <= 40*time.Second || got > 45*time.Second {
					t.Errorf("ParseRetryAfter(future date) = %v, want ~45s", got)
				}
				return
			}
			if got != tt.want {
				t.Errorf("ParseRetryAfter() = %v, want %v", got, tt.want)
			}
			if got < 0 {
				t.Errorf("ParseRetryAfter() returned negative duration %v", got)
			}
			if got > MaxRetryAfter {
				t.Errorf("ParseRetryAfter() = %v, exceeds clamp %v", got, MaxRetryAfter)
			}
		})
	}
}

// TestNewAPIError_MessageFormats pins the exact text providers have always
// emitted. pkg/providers/error_classifier_test.go and fallback_test.go match on
// "Status: %d", so these strings are part of the contract.
func TestNewAPIError_MessageFormats(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		body   string
		want   string
		status int
		hint   time.Duration
	}{
		{
			name:   "no URL (openai_compat Chat, azure)",
			status: http.StatusTooManyRequests,
			body:   "rate limited",
			want:   "API request failed:\n  Status: 429\n  Body:   rate limited",
		},
		{
			name:   "with URL (anthropic_messages, openai_compat ChatStream)",
			url:    "https://api.example.com/v1/messages",
			status: http.StatusBadGateway,
			body:   "bad gateway",
			want:   "API request failed:\n  Status: 502\n  Body:   bad gateway\n URL: https://api.example.com/v1/messages",
		},
		{
			name:   "hint does not leak into the message",
			status: http.StatusTooManyRequests,
			body:   "slow down",
			hint:   30 * time.Second,
			want:   "API request failed:\n  Status: 429\n  Body:   slow down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retryAU := ""
			if tt.hint > 0 {
				retryAU = "30"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if retryAU != "" {
					w.Header().Set("Retry-After", retryAU)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			resp, err := http.Get(server.URL)
			if err != nil {
				t.Fatalf("http.Get() error = %v", err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}

			apiErr := NewAPIError(resp, body, tt.url)
			if got := apiErr.Error(); got != tt.want {
				t.Errorf("Error() =\n%q\nwant\n%q", got, tt.want)
			}
			if apiErr.HTTPStatus() != tt.status {
				t.Errorf("HTTPStatus() = %d, want %d", apiErr.HTTPStatus(), tt.status)
			}
			if apiErr.RetryAfterHint() != tt.hint {
				t.Errorf("RetryAfterHint() = %v, want %v", apiErr.RetryAfterHint(), tt.hint)
			}
			// It must be reachable through errors.As even when wrapped.
			var target *APIError
			if !errors.As(fmt.Errorf("call: %w", apiErr), &target) {
				t.Error("wrapped *APIError not found by errors.As")
			}
		})
	}
}

// TestAPIErrorTruncatedBodyMatchesHandleErrorResponse guards the refactor of
// HandleErrorResponse: the preview truncation stays in the caller, so the text
// is unchanged while the error is now structured.
func TestAPIErrorTruncatedBodyMatchesHandleErrorResponse(t *testing.T) {
	long := strings.Repeat("x", 500)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(long))
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get() error = %v", err)
	}
	defer resp.Body.Close()

	err = HandleErrorResponse(resp, server.URL)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("HandleErrorResponse() returned %T, want *APIError", err)
	}
	if !strings.HasPrefix(apiErr.Error(), "API request failed:\n  Status: 400\n  Body:   ") {
		t.Errorf("unexpected message prefix: %q", apiErr.Error())
	}
	if !strings.HasSuffix(apiErr.Body, "...") {
		t.Errorf("body should be truncated with an ellipsis, got %q", apiErr.Body)
	}
}

// TestHandleErrorResponse_HTMLKeepsDescriptiveError: the HTML path must still
// return the dedicated message, not an *APIError.
func TestHandleErrorResponse_HTMLKeepsDescriptiveError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>gateway</body></html>"))
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get() error = %v", err)
	}
	defer resp.Body.Close()

	err = HandleErrorResponse(resp, server.URL)
	if _, isAPI := err.(*APIError); isAPI {
		t.Fatal("HTML responses must not become *APIError")
	}
	if !strings.Contains(err.Error(), "HTML instead of JSON") {
		t.Errorf("expected HTML error message, got %v", err)
	}
}

// TestAPIErrorUnwrap covers the optional base error.
func TestAPIErrorUnwrap(t *testing.T) {
	base := errors.New("dial tcp: broken pipe")
	apiErr := &APIError{StatusCode: 502, Body: "bad gateway"}
	apiErr.WithError(base)
	if !errors.Is(apiErr, base) {
		t.Error("Unwrap should expose the base error")
	}
	if apiErr.Unwrap() != base {
		t.Error("Unwrap() != base")
	}
}

// TestAPIErrorMessageOverride covers the HTML-style descriptive messages that
// keep their own wording.
func TestAPIErrorMessageOverride(t *testing.T) {
	apiErr := &APIError{
		StatusCode: 502,
		Message:    "custom description",
	}
	if apiErr.Error() != "custom description" {
		t.Errorf("Error() = %q, want %q", apiErr.Error(), "custom description")
	}
	if apiErr.HTTPStatus() != 502 {
		t.Errorf("HTTPStatus() = %d, want 502", apiErr.HTTPStatus())
	}
}

// TestNewAPIErrorNilResponse documents the defensive zero-value path.
func TestNewAPIErrorNilResponse(t *testing.T) {
	apiErr := NewAPIError(nil, []byte("boom"), "")
	if apiErr.HTTPStatus() != 0 {
		t.Errorf("HTTPStatus() = %d, want 0", apiErr.HTTPStatus())
	}
	if apiErr.RetryAfterHint() != 0 {
		t.Errorf("RetryAfterHint() = %v, want 0", apiErr.RetryAfterHint())
	}
	if apiErr.Body != "boom" {
		t.Errorf("Body = %q, want %q", apiErr.Body, "boom")
	}
}
