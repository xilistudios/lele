package channels

import (
	"net/http/httptest"
	"testing"
)

func TestIsAllowedProviderURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"valid https", "https://api.openai.com/v1", true},
		{"valid https with path", "https://api.example.com/v1/beta", true},
		{"http scheme rejected", "http://api.example.com/v1", false},
		{"no scheme rejected", "api.example.com/v1", false},
		{"garbage rejected", "://bad", false},
		{"empty rejected", "", false},
		{"localhost rejected", "https://localhost:8080/v1", false},
		{"127.0.0.1 rejected", "https://127.0.0.1/v1", false},
		{"ipv6 loopback rejected", "https://[::1]/v1", false},
		{"private ip rejected", "https://192.168.1.5/v1", false},
		{"link-local rejected", "https://169.254.0.1/v1", false},
		{"loopback subnet rejected", "https://127.0.0.2/v1", false},
		{"public ip allowed", "https://8.8.8.8/v1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAllowedProviderURL(c.url); got != c.want {
				t.Errorf("isAllowedProviderURL(%q) = %v, want %v", c.url, got, c.want)
			}
		})
	}
}

func TestParsePagination(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		wantOffset int
		wantLimit  int
	}{
		{"no params", "", 0, 50},
		{"both set", "offset=10&limit=30", 10, 30},
		{"negative offset clamp", "offset=-5&limit=20", 0, 20},
		{"zero limit default", "offset=0&limit=0", 0, 50},
		{"large limit clamp", "offset=0&limit=500", 0, 50},
		{"non-numeric falls back", "offset=abc&limit=xyz", 0, 50},
		{"limit precise 1", "offset=3&limit=1", 3, 1},
		{"limit max 200", "offset=0&limit=200", 0, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/?"+c.query, nil)
			off, lim := parsePagination(req)
			if off != c.wantOffset || lim != c.wantLimit {
				t.Errorf("parsePagination(%q) = (%d,%d), want (%d,%d)", c.query, off, lim, c.wantOffset, c.wantLimit)
			}
		})
	}
}

func TestDefaultAPIBaseByTypePublic(t *testing.T) {
	if got := defaultAPIBaseByTypePublic("openai"); got != "https://api.openai.com/v1" {
		t.Errorf("openai = %q", got)
	}
	if got := defaultAPIBaseByTypePublic("gpt"); got != "https://api.openai.com/v1" {
		t.Errorf("gpt = %q", got)
	}
	if got := defaultAPIBaseByTypePublic("openrouter"); got != "https://openrouter.ai/api/v1" {
		t.Errorf("openrouter = %q", got)
	}
	if got := defaultAPIBaseByTypePublic("zhipu"); got != "https://open.bigmodel.cn/api/paas/v4" {
		t.Errorf("zhipu = %q", got)
	}
	if got := defaultAPIBaseByTypePublic("unknown_type"); got != "" {
		t.Errorf("unknown = %q, want empty", got)
	}
	// every listed type must return non-empty
	for _, tt := range []string{"groq", "nanogpt", "chutes", "alibaba", "alibaba_coding_plan", "gemini", "google", "shengsuanyun", "nvidia"} {
		if got := defaultAPIBaseByTypePublic(tt); got == "" {
			t.Errorf("%s returned empty base url", tt)
		}
	}
}
