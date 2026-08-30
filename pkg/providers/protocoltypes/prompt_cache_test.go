package protocoltypes

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCacheTTLOptions(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "5m"},
		{"5m", "5m"},
		{"1h", "1h"},
		{"1H", "1h"},
		{" 1h ", "1h"},
		{"bogus", "5m"},
	}
	for _, c := range cases {
		if got := CacheTTLOptions(map[string]interface{}{OptPromptCacheTTL: c.in}); got != c.want {
			t.Errorf("CacheTTLOptions(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := CacheTTLOptions(nil); got != "5m" {
		t.Errorf("CacheTTLOptions(nil) = %q, want 5m", got)
	}
}

func TestCacheEnabled(t *testing.T) {
	if CacheEnabled(nil) {
		t.Error("nil options must not enable cache")
	}
	if CacheEnabled(map[string]interface{}{OptPromptCache: false}) {
		t.Error("explicit false must not enable cache")
	}
	if CacheEnabled(map[string]interface{}{OptPromptCache: "true"}) {
		t.Error("non-bool must not enable cache")
	}
	if !CacheEnabled(map[string]interface{}{OptPromptCache: true}) {
		t.Error("true must enable cache")
	}
}

func TestCacheControlJSON(t *testing.T) {
	got := CacheControlJSON("1h")
	if got["type"] != "ephemeral" || got["ttl"] != "1h" {
		t.Errorf("CacheControlJSON(1h) = %v", got)
	}
	if CacheControlJSON("")["ttl"] != "5m" {
		t.Error("empty TTL must normalize to 5m")
	}
}

func TestNormalizeCacheUsage(t *testing.T) {
	u := &UsageInfo{PromptTokens: 1000, PromptTokensDetails: &PromptTokensDetails{CachedTokens: 800}}
	u.NormalizeCacheUsage()
	if u.CacheReadInputTokens != 800 {
		t.Errorf("CacheReadInputTokens = %d, want 800", u.CacheReadInputTokens)
	}
	// Anthropic-style fields must not be overwritten by the OpenAI fold.
	v := &UsageInfo{PromptTokens: 1000, CacheReadInputTokens: 500, PromptTokensDetails: &PromptTokensDetails{CachedTokens: 800}}
	v.NormalizeCacheUsage()
	if v.CacheReadInputTokens != 500 {
		t.Errorf("existing value must win: got %d, want 500", v.CacheReadInputTokens)
	}
	// nil-safe
	(*UsageInfo)(nil).NormalizeCacheUsage()
	(&UsageInfo{}).NormalizeCacheUsage()
}

func TestOpenAIUsageParsesCachedTokens(t *testing.T) {
	// Wire format emitted by OpenAI / OpenRouter / DeepSeek endpoints.
	raw := `{"prompt_tokens": 1000, "completion_tokens": 50, "total_tokens": 1050,
		"prompt_tokens_details": {"cached_tokens": 768}}`
	var u UsageInfo
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	u.NormalizeCacheUsage()
	if u.CacheReadInputTokens != 768 {
		t.Fatalf("cache read tokens not folded: %+v", u)
	}
	if got := u.CacheHitRate(); got < 0.76 || got > 0.77 {
		t.Errorf("CacheHitRate = %v, want ~0.767", got)
	}
}

func TestMessageMarshalHasNoCacheFields(t *testing.T) {
	// Generic message marshaling must never emit cache_control — breakpoints
	// are applied inside Anthropic-style providers, not on the wire protocol.
	b, err := json.Marshal(Message{Role: "user", Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "cache") {
		t.Errorf("unexpected cache field in %s", b)
	}
}
