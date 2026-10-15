package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/config"
)

// runStatusConfig points LELE_CONFIG_DIR at a throwaway dir, writes raw as that
// dir's config.json and returns the captured stdout of statusCmd() together
// with the config path.
//
// The auth store is pinned to the legacy JSON backend (auth.UseStore(nil)) so
// the optional "OAuth/Token Auth:" block cannot leak in from a process-wide
// SQLite repo another test may have installed: a fresh machine has no
// credentials, which is exactly the state this test simulates.
func runStatusConfig(t *testing.T, raw string) (out string, configPath string) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	auth.UseStore(nil)

	configPath = filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write %s: %v", configPath, err)
	}

	return captureOutput(statusCmd), configPath
}

// TestStatusCmdNilProvidersSectionDoesNotPanic is the regression guard for the
// SIGSEGV `lele status` used to take on a fresh machine: config.LoadConfig
// deliberately sets cfg.Providers = nil when config.json has no usable
// "providers" section, and `lele auth logout` writes "providers": null on a
// fresh machine (ProvidersConfig.MarshalJSON serialises a data-less section as
// null, which loads back as nil), so a nil section is a normal CLI-produced
// state, not corruption. statusCmd() used to dereference it unguarded
// (status.go:43).
//
// The three shapes below are the three ways a config can end up with a nil
// section: no key at all ({}), an explicit null written by `auth logout`, and an
// empty section ({} is ignored by LoadConfig, see the len(providersRaw) > 2
// check). All three must print every provider as "not set".
func TestStatusCmdNilProvidersSectionDoesNotPanic(t *testing.T) {
	shapes := []struct {
		name string
		raw  string
	}{
		{"empty object config", `{}`},
		{"explicit null providers written by auth logout", `{"providers":null}`},
		{"empty providers object", `{"providers":{}}`},
	}

	wantLines := []string{
		"OpenRouter API: not set",
		"Anthropic API: not set",
		"OpenAI API: not set",
		"Gemini API: not set",
		"Zhipu API: not set",
		"Groq API: not set",
		"vLLM/Local: not set",
	}

	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			// The test itself is the no-panic proof: a SIGSEGV here fails the
			// test with the panic trace instead of being swallowed.
			out, configPath := runStatusConfig(t, shape.raw)

			// Guard against vacuity: for {} and {"providers":{}} the section
			// reaches nil only through LoadConfig's nil-ing block, so if a
			// future loader stopped nil-ing it these subtests would silently
			// stop covering the panic. Reuse the auth tests' precondition
			// (auth_test.go) instead of reimplementing it.
			assertNilProvidersPrecondition(t, shape.raw)

			for _, want := range wantLines {
				if !strings.Contains(out, want+"\n") {
					t.Errorf("statusCmd() output is missing %q\noutput:\n%s", want, out)
				}
			}

			// The Model: block (and the rest of statusCmd) must still run: the
			// guard must degrade gracefully, not abort the command early.
			if !strings.Contains(out, "Model: ") {
				t.Errorf("statusCmd() output is missing the Model: line\noutput:\n%s", out)
			}

			// The byte-identical file shows statusCmd does not WRITE the
			// config; it cannot prove providersForDisplay is allocation-free
			// (a mutant that set cfg.Providers would still pass, since nothing
			// saves). That read-only, non-allocating shape holds by inspection:
			// providersForDisplay is a 4-line pure function handing back a fresh
			// &config.ProvidersConfig{} local, never a shared pointer.
			after, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("read %s after statusCmd(): %v", configPath, err)
			}
			if string(after) != shape.raw {
				t.Errorf("statusCmd() mutated %s\nbefore: %q\nafter:  %q", configPath, shape.raw, string(after))
			}
		})
	}
}

// statusConfiguredConfig seeds a config that DOES carry a providers section:
// an OpenAI API key (so one line prints ✓, the interesting case), an
// OAuth-ish auth_method on anthropic (the shape `auth login` writes, which must
// not add or change any status line) and a vLLM api_base (the only seeded value
// that flips status.go's hasVLLM guard, so its inner fmt.Printf — the second,
// otherwise unreachable VLLM.APIBase read — is actually exercised).
const statusConfiguredConfig = `{
  "agents": {"defaults": {"model": "gpt-4o-mini"}},
  "providers": {
    "openai": {"api_key": "sk-test-openai"},
    "anthropic": {"auth_method": "oauth"},
    "vllm": {"api_base": "http://localhost:8000/v1"}
  }
}`

// TestStatusCmdConfiguredProvidersOutputIsPinned pins the normal case byte for
// byte: with a providers section present, `lele status` must print exactly what
// it printed before the nil guard, in the same order, with the same strings.
// The assertion is a single full-output comparison (not a per-line contains),
// so any added, dropped, reordered or reworded line fails here.
//
// The header is machine dependent and is rebuilt from the same sources the
// production code uses instead of a literal: `version` is a package variable
// help_test.go overwrites without restoring, the config dir is a per-run
// t.TempDir(), and buildTime is empty in tests. The workspace value is taken
// from config.LoadConfig just like statusCmd does. Everything from the
// "Model:" line down is one literal block, and that is the region the nil guard
// touches.
func TestStatusCmdConfiguredProvidersOutputIsPinned(t *testing.T) {
	out, _ := runStatusConfig(t, statusConfiguredConfig)

	cfg, err := config.LoadConfig(getConfigPath())
	if err != nil {
		t.Fatalf("config.LoadConfig(%s): %v", getConfigPath(), err)
	}

	var want strings.Builder
	fmt.Fprintf(&want, "%s lele Status\n", logo)
	fmt.Fprintf(&want, "Version: %s\n", formatVersion())
	if build, _ := formatBuildInfo(); build != "" {
		fmt.Fprintf(&want, "Build: %s\n", build)
	}
	want.WriteString("\n")
	fmt.Fprintf(&want, "Config: %s ✓\n", getConfigPath())
	fmt.Fprintf(&want, "Workspace: %s ✗\n", cfg.WorkspacePath())
	want.WriteString(
		"Model: gpt-4o-mini\n" +
			"OpenRouter API: not set\n" +
			"Anthropic API: not set\n" +
			"OpenAI API: ✓\n" +
			"Gemini API: not set\n" +
			"Zhipu API: not set\n" +
			"Groq API: not set\n" +
			"vLLM/Local: ✓ http://localhost:8000/v1\n",
	)

	if out != want.String() {
		t.Errorf("statusCmd() output changed\ngot:\n%q\nwant:\n%q", out, want.String())
	}
}
