package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	stderr string
}

func runCatalogGeneratorCommand(t *testing.T, outDir string, extraArgs ...string) (generatorRun, error) {
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
	runErr := cmd.Run()
	return generatorRun{outDir: outDir, stdout: stdout.String(), stderr: stderr.String()}, runErr
}

func runCatalogGenerator(t *testing.T, outDir string, extraArgs ...string) generatorRun {
	t.Helper()
	run, err := runCatalogGeneratorCommand(t, outDir, extraArgs...)
	if err != nil {
		t.Fatalf("generator failed: %v\nstdout:\n%s\nstderr:\n%s", err, run.stdout, run.stderr)
	}
	return run
}

func loadSourceSnapshot(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var src map[string]any
	if err := json.Unmarshal(data, &src); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return src
}

func writeSourceSnapshot(t *testing.T, src map[string]any) string {
	t.Helper()
	data, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	path := filepath.Join(t.TempDir(), "models.dev.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	return path
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
	run := runCatalogGenerator(t, outDir, "--source", "testdata/models.dev.fixture.json", "--providers", "opencode_go", "opencode")

	if !strings.Contains(run.stderr, "unrelated provider 'alibaba'") {
		t.Errorf("stderr = %q, want a note naming the unrelated provider a pinned id's limits came from", run.stderr)
	}

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
	if len(p.Models) > 35 {
		t.Errorf("generated opencode_go has %d models, want at most the 35 configured limit", len(p.Models))
	}

	src := loadSourceSnapshot(t, "testdata/models.dev.fixture.json")
	offered := (src["opencode-go"].(map[string]any)["models"]).(map[string]any)
	for id, raw := range offered {
		record, _ := raw.(map[string]any)
		if record["status"] == "deprecated" || strings.HasSuffix(strings.ToLower(id), "-free") {
			continue
		}
		if _, ok := byID[id]; !ok {
			t.Errorf("generated opencode_go dropped %q: the source lists it as live and the cap is not reached, so nothing may be evicted silently", id)
		}
	}

	omen := byID["omen-alpha"]
	if omen.ContextWindow != 0 || omen.MaxOutput != 0 {
		t.Errorf("generated opencode_go[omen-alpha] = %d/%d context/max_output: no source publishes its limits, so the record must stay name-only instead of borrowing another vendor's numbers",
			omen.ContextWindow, omen.MaxOutput)
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

const seededIndex = `{
  "version": 1,
  "updated_at": "2026-01-01T00:00:00Z",
  "source": "models.dev",
  "providers": {
    "fake_a": {"id": "fake_a", "name": "Fake A", "type": "openai", "api_base": "https://a.invalid/v1", "file": "providers/fake_a.json", "model_count": 1},
    "fake_b": {"id": "fake_b", "name": "Fake B", "type": "openai", "api_base": "https://b.invalid/v1", "file": "providers/fake_b.json", "model_count": 1}
  }
}
`

func seedExistingCatalog(t *testing.T, outDir string) {
	t.Helper()
	providersDir := filepath.Join(outDir, "providers")
	if err := os.MkdirAll(providersDir, 0o755); err != nil {
		t.Fatalf("mkdir providers: %v", err)
	}
	for _, id := range []string{"fake_a", "fake_b"} {
		body := `{"id":"` + id + `","name":"` + id + `","type":"openai","models":[{"id":"m-"` + id + `"}]}`
		if err := os.WriteFile(filepath.Join(providersDir, id+".json"), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
	}
	if err := os.WriteFile(filepath.Join(outDir, "index.json"), []byte(seededIndex), 0o644); err != nil {
		t.Fatalf("write seeded index: %v", err)
	}
}

func loadGeneratedIndex(t *testing.T, outDir string) Index {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(outDir, "index.json"))
	if err != nil {
		t.Fatalf("read generated index: %v", err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("unmarshal generated index: %v", err)
	}
	return idx
}

func TestCatalogGeneratorLeavesOtherProvidersUntouched(t *testing.T) {
	outDir := t.TempDir()
	seedExistingCatalog(t, outDir)
	runCatalogGenerator(t, outDir, "--source", "testdata/models.dev.fixture.json", "--providers", "opencode_go")

	providersDir := filepath.Join(outDir, "providers")
	entries, err := os.ReadDir(providersDir)
	if err != nil {
		t.Fatalf("read generated providers dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := map[string]bool{"fake_a.json": true, "fake_b.json": true, "opencode_go.json": true}
	for _, name := range names {
		if !want[name] {
			t.Errorf("unexpected provider file %q", name)
		}
		delete(want, name)
	}
	for name := range want {
		t.Errorf("provider file %q disappeared from a --providers run", name)
	}

	idx := loadGeneratedIndex(t, outDir)
	for _, id := range []string{"fake_a", "fake_b"} {
		entry, ok := idx.Providers[id]
		if !ok {
			t.Errorf("index lost the unscoped provider %q: a --providers run must never truncate the index", id)
			continue
		}
		if entry.ModelCount != 1 {
			t.Errorf("index[%q].model_count = %d, want the untouched 1", id, entry.ModelCount)
		}
	}
	generated := loadGeneratedProvider(t, outDir, "opencode_go")
	if got, want := idx.Providers["opencode_go"].ModelCount, len(generated.Models); got != want {
		t.Fatalf("generated index opencode_go model_count = %d, provider file has %d models", got, want)
	}
}

func TestCatalogGeneratorRefusesToTruncateACorruptIndex(t *testing.T) {
	outDir := t.TempDir()
	providersDir := filepath.Join(outDir, "providers")
	if err := os.MkdirAll(providersDir, 0o755); err != nil {
		t.Fatalf("mkdir providers: %v", err)
	}
	corrupt := []byte(`{"version": 1, "providers": {"fake_a": `)
	indexPath := filepath.Join(outDir, "index.json")
	if err := os.WriteFile(indexPath, corrupt, 0o644); err != nil {
		t.Fatalf("write corrupt index: %v", err)
	}

	run, err := runCatalogGeneratorCommand(t, outDir, "--source", "testdata/models.dev.fixture.json", "--providers", "opencode_go")
	if err == nil {
		t.Fatalf("generator accepted a corrupt index and truncated it (%s)", strings.TrimSpace(run.stdout))
	}
	if !strings.Contains(run.stderr, "cannot read") {
		t.Errorf("stderr = %q, want it to name the unreadable index", run.stderr)
	}

	after, readErr := os.ReadFile(indexPath)
	if readErr != nil {
		t.Fatalf("read index after the failed run: %v", readErr)
	}
	if !bytes.Equal(after, corrupt) {
		t.Fatalf("index.json was rewritten by a failed run:\n%s", after)
	}
	if entries, readErr := os.ReadDir(providersDir); readErr != nil || len(entries) != 0 {
		t.Fatalf("providers dir = %v (err %v), want it untouched", entries, readErr)
	}
}

func TestCatalogGeneratorFailsOnMissingPinnedID(t *testing.T) {
	src := loadSourceSnapshot(t, "testdata/models.dev.fixture.json")
	for _, providerID := range []string{"opencode-go", "opencode"} {
		provider, _ := src[providerID].(map[string]any)
		models, _ := provider["models"].(map[string]any)
		delete(models, "glm-5.1")
	}
	source := writeSourceSnapshot(t, src)

	outDir := t.TempDir()
	run, err := runCatalogGeneratorCommand(t, outDir, "--source", source, "--providers", "opencode_go")
	if err == nil {
		t.Fatalf("generator shipped a catalog whose pinned first entry is unverified (stdout: %s)", strings.TrimSpace(run.stdout))
	}
	if !strings.Contains(run.stderr, "glm-5.1") {
		t.Errorf("stderr = %q, want it to name the missing pinned id", run.stderr)
	}
}

func TestCatalogGeneratorReportsCapEvictions(t *testing.T) {
	src := loadSourceSnapshot(t, "testdata/models.dev.fixture.json")
	provider, _ := src["opencode-go"].(map[string]any)
	models, _ := provider["models"].(map[string]any)
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("synthetic-filler-%02d", i)
		models[id] = map[string]any{
			"id":    id,
			"name":  id,
			"limit": map[string]any{"context": 1000, "output": 100},
		}
	}
	source := writeSourceSnapshot(t, src)

	outDir := t.TempDir()
	run := runCatalogGenerator(t, outDir, "--source", source, "--providers", "opencode_go")

	p := loadGeneratedProvider(t, outDir, "opencode_go")
	if len(p.Models) != 35 {
		t.Fatalf("generated opencode_go has %d models, want the 35 configured cap", len(p.Models))
	}
	present := make(map[string]bool, len(p.Models))
	for _, m := range p.Models {
		present[m.ID] = true
	}
	for _, id := range []string{
		"glm-5.1", "grok-4.6", "kimi-k2.6", "minimax-m2.5", "minimax-m2.7",
		"minimax-m3.1-flash", "omen-alpha", "qwen3.6-plus", "qwen3.7-max",
	} {
		if !present[id] {
			t.Errorf("cap eviction dropped pinned id %q", id)
		}
	}
	if !strings.Contains(run.stderr, "cap 35 drops") {
		t.Errorf("stderr = %q, want the run to report how many ids the cap dropped", run.stderr)
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
