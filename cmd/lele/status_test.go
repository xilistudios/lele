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
			out, configPath := runStatusConfig(t, shape.raw)

			assertNilProvidersPrecondition(t, shape.raw)

			for _, want := range wantLines {
				if !strings.Contains(out, want+"\n") {
					t.Errorf("statusCmd() output is missing %q\noutput:\n%s", want, out)
				}
			}

			if !strings.Contains(out, "Model: ") {
				t.Errorf("statusCmd() output is missing the Model: line\noutput:\n%s", out)
			}

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

const statusConfiguredConfig = `{
  "agents": {"defaults": {"model": "gpt-4o-mini"}},
  "providers": {
    "openai": {"api_key": "sk-test-openai"},
    "anthropic": {"auth_method": "oauth"},
    "vllm": {"api_base": "http://localhost:8000/v1"}
  }
}`

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
