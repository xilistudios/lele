package tools

import (
	"errors"
	"testing"
	"time"
)

func TestDefaultRetryConfig(t *testing.T) {
	config := DefaultRetryConfig()

	if config.MaxRetries != 3 {
		t.Errorf("expected MaxRetries=3, got %d", config.MaxRetries)
	}
	if config.BaseDelay != 5*time.Second {
		t.Errorf("expected BaseDelay=5s, got %v", config.BaseDelay)
	}
	if config.MaxDelay != 60*time.Second {
		t.Errorf("expected MaxDelay=60s, got %v", config.MaxDelay)
	}
	if config.Multiplier != 2.0 {
		t.Errorf("expected Multiplier=2.0, got %f", config.Multiplier)
	}
	if config.RetryOnAll != false {
		t.Errorf("expected RetryOnAll=false, got %v", config.RetryOnAll)
	}
}

func TestIsRetryableError_Timeout(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "timeout error",
			err:      errors.New("Client.Timeout exceeded while awaiting headers"),
			expected: true,
		},
		{
			name:     "deadline exceeded",
			err:      errors.New("context deadline exceeded"),
			expected: true,
		},
		{
			name:     "429 rate limit",
			err:      errors.New("429 Too Many Requests"),
			expected: true,
		},
		{
			name:     "rate limit text",
			err:      errors.New("rate limit exceeded"),
			expected: true,
		},
		{
			name:     "500 server error",
			err:      errors.New("500 Internal Server Error"),
			expected: true,
		},
		{
			name:     "502 bad gateway",
			err:      errors.New("502 Bad Gateway"),
			expected: true,
		},
		{
			name:     "503 service unavailable",
			err:      errors.New("503 Service Unavailable"),
			expected: true,
		},
		{
			name:     "connection refused",
			err:      errors.New("connection refused"),
			expected: true,
		},
		{
			name:     "connection reset",
			err:      errors.New("connection reset by peer"),
			expected: true,
		},
		{
			name:     "unexpected EOF",
			err:      errors.New("unexpected EOF"),
			expected: true,
		},
		{
			name:     "auth error - not retryable",
			err:      errors.New("401 Unauthorized: invalid API key"),
			expected: false,
		},
		{
			name:     "bad request - not retryable",
			err:      errors.New("400 Bad Request: invalid model"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(tt.err)
			if result != tt.expected {
				t.Errorf("isRetryableError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestCalculateDelay(t *testing.T) {
	config := RetryConfig{
		BaseDelay:  1 * time.Second,
		MaxDelay:   30 * time.Second,
		Multiplier: 2.0,
	}

	// Attempt 0: 1s * 2^0 = 1s
	delay := calculateDelay(config, 0)
	if delay != 1*time.Second {
		t.Errorf("attempt 0: expected 1s, got %v", delay)
	}

	// Attempt 1: 1s * 2^1 = 2s
	delay = calculateDelay(config, 1)
	if delay != 2*time.Second {
		t.Errorf("attempt 1: expected 2s, got %v", delay)
	}

	// Attempt 2: 1s * 2^2 = 4s
	delay = calculateDelay(config, 2)
	if delay != 4*time.Second {
		t.Errorf("attempt 2: expected 4s, got %v", delay)
	}

	// Attempt 3: 1s * 2^3 = 8s
	delay = calculateDelay(config, 3)
	if delay != 8*time.Second {
		t.Errorf("attempt 3: expected 8s, got %v", delay)
	}

	// Attempt 10: 1s * 2^10 = 1024s, but capped at 30s
	delay = calculateDelay(config, 10)
	if delay != 30*time.Second {
		t.Errorf("attempt 10: expected 30s (max), got %v", delay)
	}
}
