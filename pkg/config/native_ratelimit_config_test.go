package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeRateLimitDefaultsToEnabled(t *testing.T) {
	rl := DefaultConfig().Channels.Native.RateLimit

	if !rl.Enabled {
		t.Error("DefaultConfig().Channels.Native.RateLimit.Enabled = false, want the secure default true")
	}
	if rl.PinPerMinute != DefaultNativeRateLimitPinPerMinute {
		t.Errorf("PinPerMinute = %d, want %d", rl.PinPerMinute, DefaultNativeRateLimitPinPerMinute)
	}
	if rl.PairPerMinute != DefaultNativeRateLimitPairPerMinute {
		t.Errorf("PairPerMinute = %d, want %d", rl.PairPerMinute, DefaultNativeRateLimitPairPerMinute)
	}
	if rl.RefreshPerMinute != DefaultNativeRateLimitRefreshPerMinute {
		t.Errorf("RefreshPerMinute = %d, want %d", rl.RefreshPerMinute, DefaultNativeRateLimitRefreshPerMinute)
	}
	if rl.APIPerMinute != DefaultNativeRateLimitAPIPerMinute {
		t.Errorf("APIPerMinute = %d, want %d", rl.APIPerMinute, DefaultNativeRateLimitAPIPerMinute)
	}
	if rl.WSMessagesPerMinute != DefaultNativeRateLimitWSMessagesPerMinute {
		t.Errorf("WSMessagesPerMinute = %d, want %d", rl.WSMessagesPerMinute, DefaultNativeRateLimitWSMessagesPerMinute)
	}
	if rl.APIPerMinute != 600 || rl.WSMessagesPerMinute != 600 {
		t.Errorf("API/WS defaults = %d/%d per minute, want the generous 600/600", rl.APIPerMinute, rl.WSMessagesPerMinute)
	}
}

func TestNativeRateLimitRoundTripsThroughSerializable(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Channels.Native.RateLimit = NativeRateLimitConfig{
		Enabled:      true,
		PinPerMinute: 3,
	}

	raw, err := json.Marshal(doc.toSerializable())
	if err != nil {
		t.Fatalf("marshal serializable: %v", err)
	}

	got := DefaultConfig()
	if err := json.Unmarshal(raw, got); err != nil {
		t.Fatalf("unmarshal into config: %v", err)
	}

	rl := got.Channels.Native.RateLimit
	if !rl.Enabled {
		t.Error("Enabled = false after round-trip, want true")
	}
	if rl.PinPerMinute != 3 {
		t.Errorf("PinPerMinute = %d after round-trip, want 3", rl.PinPerMinute)
	}
}

func TestNativeRateLimitSurvivesSaveEditableDocument(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Channels.Native.RateLimit = NativeRateLimitConfig{
		Enabled:      true,
		PinPerMinute: 3,
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("saved config is not valid JSON: %s", raw)
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal top-level: %v", err)
	}
	channels, ok := parsed["channels"].(map[string]any)
	if !ok {
		t.Fatal("channels section missing from saved config")
	}
	native, ok := channels["native"].(map[string]any)
	if !ok {
		t.Fatal("native section missing from saved config")
	}
	rl, ok := native["rate_limit"].(map[string]any)
	if !ok {
		t.Fatal("rate_limit missing from saved native config")
	}
	if rl["pin_per_minute"] != float64(3) {
		t.Errorf("pin_per_minute = %v, want 3", rl["pin_per_minute"])
	}
	if rl["enabled"] != true {
		t.Errorf("enabled = %v, want true", rl["enabled"])
	}
}

func TestNativeRateLimitToConfigPreservesBlock(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Channels.Native.RateLimit = NativeRateLimitConfig{
		Enabled:             true,
		PinPerMinute:        7,
		PairPerMinute:       4,
		RefreshPerMinute:    15,
		APIPerMinute:        100,
		WSMessagesPerMinute: 50,
	}

	cfg, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}

	got := cfg.Channels.Native.RateLimit
	if !got.Enabled {
		t.Error("Enabled = false after ToConfig, want true")
	}
	if got.PinPerMinute != 7 {
		t.Errorf("PinPerMinute = %d, want 7", got.PinPerMinute)
	}
	if got.PairPerMinute != 4 {
		t.Errorf("PairPerMinute = %d, want 4", got.PairPerMinute)
	}
	if got.RefreshPerMinute != 15 {
		t.Errorf("RefreshPerMinute = %d, want 15", got.RefreshPerMinute)
	}
	if got.APIPerMinute != 100 {
		t.Errorf("APIPerMinute = %d, want 100", got.APIPerMinute)
	}
	if got.WSMessagesPerMinute != 50 {
		t.Errorf("WSMessagesPerMinute = %d, want 50", got.WSMessagesPerMinute)
	}
}

func TestNativeRateLimitEditableDocumentFromConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Channels.Native.RateLimit.PinPerMinute = 7

	doc := editableDocumentFromConfig(cfg)

	if doc.Channels.Native.RateLimit.PinPerMinute != 7 {
		t.Errorf("doc.RateLimit.PinPerMinute = %d, want 7", doc.Channels.Native.RateLimit.PinPerMinute)
	}
}

func TestNativeRateLimitApplyDefaultsFillsZeroRates(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Channels.Native.RateLimit = NativeRateLimitConfig{
		Enabled: true,
	}

	applyDefaults(doc)

	rl := doc.Channels.Native.RateLimit
	if !rl.Enabled {
		t.Error("applyDefaults changed Enabled from true to false")
	}
	if rl.PinPerMinute != DefaultNativeRateLimitPinPerMinute {
		t.Errorf("PinPerMinute = %d, want %d", rl.PinPerMinute, DefaultNativeRateLimitPinPerMinute)
	}
	if rl.PairPerMinute != DefaultNativeRateLimitPairPerMinute {
		t.Errorf("PairPerMinute = %d, want %d", rl.PairPerMinute, DefaultNativeRateLimitPairPerMinute)
	}
	if rl.RefreshPerMinute != DefaultNativeRateLimitRefreshPerMinute {
		t.Errorf("RefreshPerMinute = %d, want %d", rl.RefreshPerMinute, DefaultNativeRateLimitRefreshPerMinute)
	}
	if rl.APIPerMinute != DefaultNativeRateLimitAPIPerMinute {
		t.Errorf("APIPerMinute = %d, want %d", rl.APIPerMinute, DefaultNativeRateLimitAPIPerMinute)
	}
	if rl.WSMessagesPerMinute != DefaultNativeRateLimitWSMessagesPerMinute {
		t.Errorf("WSMessagesPerMinute = %d, want %d", rl.WSMessagesPerMinute, DefaultNativeRateLimitWSMessagesPerMinute)
	}
}

func TestNativeRateLimitApplyDefaultsKeepsDisabled(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Channels.Native.RateLimit = NativeRateLimitConfig{
		Enabled: false,
	}

	applyDefaults(doc)

	if doc.Channels.Native.RateLimit.Enabled {
		t.Error("applyDefaults flipped Enabled from false to true")
	}
}

func TestNativeRateLimitEnvOverrides(t *testing.T) {
	t.Setenv("LELE_CHANNELS_NATIVE_RATE_LIMIT_ENABLED", "true")
	t.Setenv("LELE_CHANNELS_NATIVE_RATE_LIMIT_PIN_PER_MINUTE", "3")

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	rl := cfg.Channels.Native.RateLimit
	if !rl.Enabled {
		t.Error("Enabled = false despite env override, want true")
	}
	if rl.PinPerMinute != 3 {
		t.Errorf("PinPerMinute = %d despite env override, want 3", rl.PinPerMinute)
	}
}

func TestNativeRateLimitValidateRejectsNegative(t *testing.T) {
	t.Run("negative_pin", func(t *testing.T) {
		doc := defaultEditableDocument()
		doc.Channels.Native.RateLimit.PinPerMinute = -1

		errs := ValidateEditableDocument(doc)
		var found bool
		for _, e := range errs {
			if e.Path == "channels.native.rate_limit.pin_per_minute" && e.Code == "invalid_range" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected invalid_range error for pin_per_minute, got %v", errs)
		}
	})

	t.Run("zero_is_valid", func(t *testing.T) {
		doc := defaultEditableDocument()
		doc.Channels.Native.RateLimit.PinPerMinute = 0

		errs := ValidateEditableDocument(doc)
		for _, e := range errs {
			if e.Path == "channels.native.rate_limit.pin_per_minute" {
				t.Errorf("pin_per_minute=0 should not produce an error, got %v", e)
			}
		}
	})

	t.Run("disabled_with_negative_pair", func(t *testing.T) {
		doc := defaultEditableDocument()
		doc.Channels.Native.RateLimit = NativeRateLimitConfig{
			Enabled:       false,
			PairPerMinute: -5,
		}

		errs := ValidateEditableDocument(doc)
		var found bool
		for _, e := range errs {
			if e.Path == "channels.native.rate_limit.pair_per_minute" && e.Code == "invalid_range" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected invalid_range error for pair_per_minute even when disabled, got %v", errs)
		}
	})
}

func TestRateLimit_UnsetLoadDefaultsEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !loaded.Channels.Native.RateLimit.Enabled {
		t.Error("LoadConfig({}).Channels.Native.RateLimit.Enabled = false, want true")
	}
	if loaded.Channels.Native.RateLimit.APIPerMinute != 600 {
		t.Errorf("APIPerMinute = %d, want 600", loaded.Channels.Native.RateLimit.APIPerMinute)
	}
	if loaded.Channels.Native.RateLimit.WSMessagesPerMinute != 600 {
		t.Errorf("WSMessagesPerMinute = %d, want 600", loaded.Channels.Native.RateLimit.WSMessagesPerMinute)
	}
}

func TestRateLimit_ExplicitFalsePreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	document := `{"channels":{"native":{"rate_limit":{"enabled":false,"pin_per_minute":2}}}}`
	if err := os.WriteFile(path, []byte(document), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Channels.Native.RateLimit.Enabled {
		t.Error("RateLimit.Enabled = true for an explicit false, want false")
	}
	if cfg.Channels.Native.RateLimit.PinPerMinute != 2 {
		t.Errorf("PinPerMinute = %d, want the explicit 2", cfg.Channels.Native.RateLimit.PinPerMinute)
	}

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	value, ok := jsonPathValue(t, readSavedJSON(t, path), "channels", "native", "rate_limit", "enabled")
	if !ok || value != false {
		t.Fatalf("saved rate_limit.enabled = %v (present %v), want the explicit false kept", value, ok)
	}
}

func TestRateLimit_JSONNullTreatedUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	document := `{"channels":{"native":{"rate_limit":{"enabled":null}}}}`
	if err := os.WriteFile(path, []byte(document), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Channels.Native.RateLimit.Enabled {
		t.Error("RateLimit.Enabled = false for a JSON null, want the unset default true")
	}

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	value, ok := jsonPathValue(t, readSavedJSON(t, path), "channels", "native", "rate_limit", "enabled")
	if !ok || value != nil {
		t.Fatalf("saved rate_limit.enabled = %v (present %v), want the null kept untouched", value, ok)
	}
}

func TestRateLimit_UnsetSaveReloadStaysUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"language":"en"}`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	parsed := readSavedJSON(t, path)
	requireAbsentJSONPath(t, parsed, "channels", "native", "rate_limit")

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig (reload): %v", err)
	}
	if !reloaded.Channels.Native.RateLimit.Enabled {
		t.Error("reloaded RateLimit.Enabled = false, want the unset default true")
	}
}

func TestRateLimit_InvalidTypeSurfacesError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	document := `{"channels":{"native":{"rate_limit":{"enabled":"yes"}}}}`
	if err := os.WriteFile(path, []byte(document), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("LoadConfig with a string rate_limit.enabled succeeded, want a type error")
	}
}
