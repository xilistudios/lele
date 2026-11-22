package providers

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/providers/common"
)

type errorPattern struct {
	substring string
	regex     *regexp.Regexp
}

func substr(s string) errorPattern { return errorPattern{substring: s} }
func rxp(r string) errorPattern    { return errorPattern{regex: regexp.MustCompile("(?i)" + r)} }

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

	transientStatusCodes = map[int]bool{
		500: true, 502: true, 503: true,
		521: true, 522: true, 523: true, 524: true,
		529: true,
	}
)

func ClassifyError(err error, provider, model string) *FailoverError {
	if err == nil {
		return nil
	}

	if isCancellation(err) {
		return nil
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return &FailoverError{
			Reason:   FailoverTimeout,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	if errors.Is(err, common.ErrMissingWireSession) {
		return &FailoverError{
			Reason:   FailoverFormat,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	if fe, ok := classifyStructured(err, provider, model); ok {
		return fe
	}

	msg := strings.ToLower(err.Error())

	if IsImageDimensionError(msg) || IsImageSizeError(msg) {
		return &FailoverError{
			Reason:   FailoverFormat,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

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

	if reason := classifyByMessage(msg); reason != "" {
		return &FailoverError{
			Reason:   reason,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	if IsContextOverflowError(err) {
		return &FailoverError{
			Reason:   FailoverFormat,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	return &FailoverError{
		Reason:   FailoverUnknown,
		Provider: provider,
		Model:    model,
		Wrapped:  err,
	}
}

type httpStatusError interface {
	error
	HTTPStatus() int
	RetryAfterHint() time.Duration
}

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
	lower := strings.ToLower(err.Error())
	if IsImageDimensionError(lower) || IsImageSizeError(lower) {
		fe.Reason = FailoverFormat
		return fe, true
	}
	if IsContextOverflowError(err) {
		fe.Reason = FailoverFormat
		return fe, true
	}
	fe.Reason = FailoverUnknown
	return fe, true
}

func isCancellation(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "context canceled")
}

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
	case status == 413:
		return FailoverFormat
	case transientStatusCodes[status]:
		return FailoverTimeout
	}
	return ""
}

func classifyByMessage(msg string) FailoverReason {
	if matchesAny(msg, rateLimitPatterns) {
		return FailoverRateLimit
	}
	if matchesAny(msg, overloadedPatterns) {
		return FailoverRateLimit
	}
	if matchesAny(msg, billingPatterns) {
		return FailoverBilling
	}
	if matchesAny(msg, timeoutPatterns) {
		return FailoverTimeout
	}
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

func extractHTTPStatus(msg string) int {
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

func IsImageDimensionError(msg string) bool {
	return matchesAny(msg, imageDimensionPatterns)
}

func IsImageSizeError(msg string) bool {
	return matchesAny(msg, imageSizePatterns)
}

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

func parseDigits(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}
