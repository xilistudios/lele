package version

import (
	"runtime/debug"
	"sync"
	"testing"
)

func TestStripDirty(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"go toolchain +dirty suffix", "v0.7.10+dirty", "v0.7.10"},
		{"git describe -dirty suffix", "v0.7.10-dirty", "v0.7.10"},
		{"describe offset with +dirty", "v0.7.9-3-gabcdef1+dirty", "v0.7.9-3-gabcdef1"},
		{"clean version untouched", "v0.7.10", "v0.7.10"},
		{"plain semver untouched", "0.7.10", "0.7.10"},
		{"pre-release untouched", "v1.0.0-rc1", "v1.0.0-rc1"},
		{"marker not at end is kept", "v1.2.3-dirty.fix1", "v1.2.3-dirty.fix1"},
		{"marker in middle is kept", "dirty-v1.0.0", "dirty-v1.0.0"},
		{"empty string", "", ""},
		{"dev", "dev", "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripDirty(tt.in); got != tt.want {
				t.Errorf("StripDirty(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSetAndGet(t *testing.T) {
	Set("0.7.10")
	if got := Get(); got != "0.7.10" {
		t.Errorf("Get() = %q after Set, want %q", got, "0.7.10")
	}
}

func TestSetIgnoresInvalid(t *testing.T) {
	Set("1.2.3")
	Set("")
	Set("(devel)")
	Set("dev")
	if got := Get(); got != "1.2.3" {
		t.Errorf("Get() = %q, want 1.2.3 (invalid Set calls must be ignored)", got)
	}
}

func TestGetNeverEmpty(t *testing.T) {
	if got := Get(); got == "" {
		t.Error("Get() returned empty string")
	}
}

func TestGetNeverDirty(t *testing.T) {
	got := Get()
	if got == "v0.7.10+dirty" || got == "0.7.10-dirty" {
		t.Fatalf("Get() leaked dirty marker: %q", got)
	}
	if StripDirty(got) != got {
		t.Errorf("Get() = %q, which still carries a dirty marker", got)
	}
}

func TestGetFallbackMatchesStripBuildInfo(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		t.Skip("no build info available in this environment")
	}
	v := Get()
	if v == "" {
		t.Error("fallback Get() returned empty string")
	}
}

func TestConcurrentSetGet(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			Set("v0.0." + string(rune('A'+i%26)))
		}(i)
		go func() {
			defer wg.Done()
			_ = Get()
		}()
	}
	wg.Wait()
}
