package config

import "testing"

func TestPromptCacheNormalizedTTL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "5m"},
		{"5m", "5m"},
		{"1h", "1h"},
		{"1H", "1h"},
		{" 1h ", "1h"},
		{"nonsense", "5m"},
	}
	for _, c := range cases {
		if got := (PromptCacheConfig{TTL: c.in}).NormalizedTTL(); got != c.want {
			t.Errorf("NormalizedTTL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPromptCacheDefaultsDisabled(t *testing.T) {
	// Zero value must keep caching off so behavior is unchanged by default.
	var d PromptCacheConfig
	if d.Enabled {
		t.Error("prompt cache must be opt-in")
	}
}
