package common

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var ErrMissingWireSession = errors.New("wire session id missing")

const MaxRetryAfter = 120 * time.Second

type APIError struct {
	StatusCode int
	RetryAfter time.Duration
	Body       string
	URL        string
	Message    string

	err error
}

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

func (e *APIError) Unwrap() error { return e.err }

func (e *APIError) HTTPStatus() int { return e.StatusCode }

func (e *APIError) RetryAfterHint() time.Duration { return e.RetryAfter }

func (e *APIError) WithError(err error) *APIError {
	e.err = err
	return e
}

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

func ParseRetryAfter(h http.Header) time.Duration {
	if h == nil {
		return 0
	}

	if d, ok := parseRetryAfterHeader(h.Get("Retry-After")); ok {
		return clampRetryAfter(d)
	}
	if ms := h.Get("Retry-After-Ms"); ms != "" {
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

func clampRetryAfter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	if d > MaxRetryAfter {
		return MaxRetryAfter
	}
	return d
}
