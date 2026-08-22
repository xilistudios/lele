package main

import (
	"strings"
	"testing"
)

// TestOnboard_FullWithLocalProvider drives a full onboard flow using the local
// "Ollama" provider (no API key / no network validation), then accepts defaults
// through the rest of the wizard, saving the config.
func TestOnboard_FullWithLocalV4(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)

	// Provider steps:
	// 1. configureProviders first askSelect: "Show all providers" is option 9.
	// 2. Second askSelect (all shown): Ollama is index 8 (provider #9).
	//    - local => no API key prompt.
	//    - API Base
	//    - proxy? no
	//    - model aliases? no
	// 3. configure another provider? no
	// 4. configureAgentDefaults: default model (no configured models) -> enter
	//    "test:model", max tokens default(Enter), temp default(Enter), tool
	//    iters default(Enter).
	// 5. configureAdditionalAgents: no.
	// 6. Enable Web UI? no (skip PIN).
	// 7. printSummary.
	// 8. Save configuration? yes.
	// 9. maybeStartServices: web disabled => returns.
	p := newStdinPipe(t)
	p.feedLines(
		"9\n",                  // Show all providers
		"9\n",                  // select Ollama (index 8)
		"localhost:11434/v1\n", // API Base
		"n\n",                  // proxy?
		"n\n",                  // model aliases?
		"n\n",                  // configure another provider?
		"test:model\n",         // default model
		"\n",                   // max tokens default
		"\n",                   // temperature default
		"\n",                   // max tool iterations default
		"n\n",                  // add additional agents?
		"n\n",                  // enable Web UI?
		"y\n",                  // save configuration?
	)
	p.close()
	out := runCmd(func() { onboard() })

	if !strings.Contains(out, "lele is ready") {
		t.Errorf("expected onboarding completion, got: %s", out)
	}
	if !strings.Contains(out, "Configuration Summary") {
		t.Errorf("expected config summary, got: %s", out)
	}
}
