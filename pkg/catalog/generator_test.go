package catalog

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const generatorScript = "../../scripts/update_model_catalog.py"

type generatorRun struct {
	outDir string
	stdout string
}

func runCatalogGenerator(t *testing.T, outDir string, extraArgs ...string) generatorRun {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not available: skipping generator tests")
	}
	if _, err := os.Stat(generatorScript); err != nil {
		t.Fatalf("generator script missing: %v", err)
	}

	args := append([]string{generatorScript, "--output-dir", outDir}, extraArgs...)
	cmd := exec.Command(python, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("generator failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return generatorRun{outDir: outDir, stdout: stdout.String()}
}

func loadGeneratedProvider(t *testing.T, outDir, provider string) Provider {
	t.Helper()
	path := filepath.Join(outDir, "providers", provider+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated %s: %v", path, err)
	}
	var p Provider
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unmarshal generated %s: %v", path, err)
	}
	if len(p.Models) == 0 {
		t.Fatalf("generated %s has no models", path)
	}
	return p
}

func TestCatalogGeneratorPinsLiveIDsAndMarksProtocols(t *testing.T) {
	outDir := t.TempDir()
	runCatalogGenerator(t, outDir, "--source", "testdata/models.dev.fixture.json", "--providers", "opencode_go", "opencode")

	p := loadGeneratedProvider(t, outDir, "opencode_go")

	var freeIDs []string
	byID := make(map[string]Model, len(p.Models))
	for _, m := range p.Models {
		byID[m.ID] = m
		if strings.HasSuffix(strings.ToLower(m.ID), "-free") {
			freeIDs = append(freeIDs, m.ID)
		}
	}
	if len(freeIDs) > 0 {
		t.Errorf("generated opencode_go still offers free ids: %v", freeIDs)
	}

	for _, id := range []string{"gpt-5.6-luna", "grok-4.7"} {
		m, ok := byID[id]
		if !ok {
			t.Errorf("generated opencode_go is missing %q", id)
			continue
		}
		if m.Protocol != ProtocolResponses {
			t.Errorf("generated opencode_go[%q].Protocol = %q, want %q", id, m.Protocol, ProtocolResponses)
		}
	}

	for _, id := range []string{
		"glm-5.1", "grok-4.6", "kimi-k2.6", "minimax-m2.5", "minimax-m2.7",
		"minimax-m3.1-flash", "omen-alpha", "qwen3.6-plus", "qwen3.7-max",
	} {
		if _, ok := byID[id]; !ok {
			t.Errorf("generated opencode_go is missing pinned live id %q", id)
		}
	}

	if p.Models[0].ID != "glm-5.1" {
		t.Errorf("generated opencode_go first model = %q, want the first pinned id %q", p.Models[0].ID, "glm-5.1")
	}
	if !p.Models[0].SupportsChatCompletions() {
		t.Errorf("generated opencode_go first model %q does not speak chat/completions", p.Models[0].ID)
	}
	if len(p.Models) > 30 {
		t.Errorf("generated opencode_go has %d models, want at most the 30 configured limit", len(p.Models))
	}

	zen := loadGeneratedProvider(t, outDir, "opencode")
	for _, m := range zen.Models {
		if m.Protocol != "" {
			t.Errorf("generated opencode[%q].Protocol = %q: zen coverage is unverified, markers stay scoped to opencode_go", m.ID, m.Protocol)
		}
	}
}

func TestCatalogGeneratorIsDeterministic(t *testing.T) {
	outDir := t.TempDir()
	runCatalogGenerator(t, outDir, "--source", "testdata/models.dev.fixture.json", "--providers", "opencode_go", "opencode")
	first := snapshotDir(t, outDir)

	runCatalogGenerator(t, outDir, "--source", "testdata/models.dev.fixture.json", "--providers", "opencode_go", "opencode")
	second := snapshotDir(t, outDir)

	if len(first) != len(second) {
		t.Fatalf("file sets differ between runs: %d vs %d files", len(first), len(second))
	}
	for name, want := range first {
		got, ok := second[name]
		if !ok {
			t.Errorf("%s present in the first run but missing in the second", name)
			continue
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s changed between two identical runs\nfirst:\n%s\nsecond:\n%s", name, want, got)
		}
	}
}

func TestCatalogGeneratorLeavesOtherProvidersUntouched(t *testing.T) {
	outDir := t.TempDir()
	runCatalogGenerator(t, outDir, "--source", "testdata/models.dev.fixture.json", "--providers", "opencode_go")

	entries, err := os.ReadDir(filepath.Join(outDir, "providers"))
	if err != nil {
		t.Fatalf("read generated providers dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "opencode_go.json" {
		t.Fatalf("generated providers = %v, want only opencode_go.json", names)
	}

	var idx Index
	data, err := os.ReadFile(filepath.Join(outDir, "index.json"))
	if err != nil {
		t.Fatalf("read generated index: %v", err)
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("unmarshal generated index: %v", err)
	}
	if len(idx.Providers) != 1 {
		t.Fatalf("generated index lists %d providers, want 1", len(idx.Providers))
	}
	generated := loadGeneratedProvider(t, outDir, "opencode_go")
	if got, want := idx.Providers["opencode_go"].ModelCount, len(generated.Models); got != want {
		t.Fatalf("generated index opencode_go model_count = %d, provider file has %d models", got, want)
	}
}

func snapshotDir(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		out[rel] = data
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return out
}
