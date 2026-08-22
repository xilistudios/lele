package main

import (
	"strings"
	"testing"
)

// TestConfigureProviders_ShowAllAndExit drives configureProviders through the
// "Show all providers" flow then selects to stop.
func TestConfigureProviders_ShowAllAndExitV4(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	registry := providerRegistry()
	_ = registry
	// First askSelect: options = first 8 + "[Show all providers]". Index of
	// "[Show all providers]" is 8 -> value 9.
	showAllIdx := 9
	// After Show all, options = all providers. Comic index 0 = first provider
	// (Anthropic). Then it configures and asks "Configure another provider?" -> n.
	p := newStdinPipe(t)
	p.feedLines(
		// Choose "Show all providers" (the 9th option = index 8 => line 9)
		"9\n",
		// Now all shown; choose index 1 (0-based) = 2nd provider? We'll decline
		// continuation. Pick provider #1 (Anthropic), which is a non-local
		// provider requiring API key.
		"1\n",
		"sk-test\n", // API key
		"localhost:1111\n", // API base (local -> validates true)
		"n\n", // proxy?
		"n\n", // model aliases?
		"n\n", // configure another provider?
	)
	p.close()
	out := runCmd(func() { configureProviders(cfg) })
	if !strings.Contains(out, "Provider Configuration") {
		t.Errorf("expected provider configuration header, got: %s", out)
	}
	_ = showAllIdx
}