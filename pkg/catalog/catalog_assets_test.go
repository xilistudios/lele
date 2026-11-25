package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repoCatalogDir = "../../catalog"

func loadRepoProvider(t *testing.T, provider string) Provider {
	t.Helper()
	path := filepath.Join(repoCatalogDir, "providers", provider+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var p Provider
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	if len(p.Models) == 0 {
		t.Fatalf("%s has no models", path)
	}
	return p
}

func loadRepoIndex(t *testing.T) Index {
	t.Helper()
	path := filepath.Join(repoCatalogDir, "index.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return idx
}

func modelIDs(models []Model) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

func TestRepoCatalogOpencodeGoHasNoFreeModelIDs(t *testing.T) {
	p := loadRepoProvider(t, "opencode_go")
	for _, m := range p.Models {
		if strings.HasSuffix(strings.ToLower(m.ID), "-free") {
			t.Errorf("opencode_go offers %q: free-tier ids answer 400 ModelDeprecated or 402 upstream (issue #405)", m.ID)
		}
	}
}

func TestRepoCatalogOpencodeGoMarksMeasuredNonChatCompletionsIDs(t *testing.T) {
	measured := map[string]string{
		"gpt-5.6-luna":               ProtocolResponses,
		"grok-4.7":                   ProtocolResponses,
		"claude-haiku-5-5":           ProtocolAnthropicMessages,
		"gemini-4-argon-contributor": ProtocolGemini,
		"gpt-6-luna":                 ProtocolResponses,
		"grok-4.6":                   ProtocolResponses,
		"minimax-m2.7":               ProtocolAnthropicMessages,
		"muse-spark-1.2-contributor": ProtocolResponses,
		"muse-spark-1.3-contributor": ProtocolResponses,
	}
	p := loadRepoProvider(t, "opencode_go")

	byID := make(map[string]Model, len(p.Models))
	for _, m := range p.Models {
		byID[m.ID] = m
	}
	for id, want := range measured {
		m, ok := byID[id]
		if !ok {
			continue
		}
		if m.Protocol != want {
			t.Errorf("opencode_go[%q].Protocol = %q, want %q", id, m.Protocol, want)
		}
		if m.SupportsChatCompletions() {
			t.Errorf("opencode_go[%q] is offered to the chat/completions picker", id)
		}
	}
	for _, id := range []string{"gpt-5.6-luna", "grok-4.7"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("opencode_go lost the measured id %q: it must stay listed with its protocol marker", id)
		}
	}

	picker := make(map[string]bool, len(p.Models))
	for _, m := range ChatCompletionsModels(p.Models) {
		picker[m.ID] = true
	}
	for _, m := range p.Models {
		if m.Protocol == "" {
			continue
		}
		if _, ok := measured[m.ID]; !ok {
			t.Errorf("opencode_go[%q] carries protocol %q, which no measurement backs: add it to PROTOCOL_MARKERS with evidence or drop the marker", m.ID, m.Protocol)
		}
		if picker[m.ID] {
			t.Errorf("opencode_go[%q] is marked %q but the picker would still offer it", m.ID, m.Protocol)
		}
	}
}

func TestRepoCatalogOpencodeGoKeepsMeasuredWorkingIDs(t *testing.T) {
	measuredWorking := []string{
		"deepseek-v4-pro", "glm-5.3", "hy3", "hy4-preview", "step-5-preview",
	}
	p := loadRepoProvider(t, "opencode_go")
	present := make(map[string]bool, len(p.Models))
	for _, m := range p.Models {
		present[m.ID] = true
	}
	for _, id := range measuredWorking {
		if !present[id] {
			t.Errorf("opencode_go dropped %q: docs measure it answering chat/completions with HTTP 200, so the model cap must not evict it", id)
		}
	}

	for _, m := range p.Models {
		if m.ID != "omen-alpha" {
			continue
		}
		if m.ContextWindow != 0 || m.MaxOutput != 0 {
			t.Errorf("opencode_go[omen-alpha] = %d/%d context/max_output: no source publishes its limits, so the shipped record must stay name-only", m.ContextWindow, m.MaxOutput)
		}
	}
}

func TestRepoCatalogOpencodeGoKeepsMeasuredLiveIDs(t *testing.T) {
	live := []string{
		"glm-5.1", "grok-4.6", "kimi-k2.6", "minimax-m2.5", "minimax-m2.7",
		"minimax-m3.1-flash", "omen-alpha", "qwen3.6-plus", "qwen3.7-max",
	}
	p := loadRepoProvider(t, "opencode_go")
	present := make(map[string]bool, len(p.Models))
	for _, m := range p.Models {
		present[m.ID] = true
	}
	for _, id := range live {
		if !present[id] {
			t.Errorf("opencode_go is missing measured-live id %q (issue #405)", id)
		}
	}
	if t.Failed() {
		t.Logf("catalog ids: %v", modelIDs(p.Models))
	}
}

func TestRepoCatalogOpencodeGoFirstModelIsUsable(t *testing.T) {
	p := loadRepoProvider(t, "opencode_go")
	if !p.Models[0].SupportsChatCompletions() {
		t.Fatalf("first opencode_go model is %q (protocol %q): the picker's default entry must speak chat/completions",
			p.Models[0].ID, p.Models[0].Protocol)
	}
}

func TestRepoCatalogIndexCountsMatchProviderFiles(t *testing.T) {
	idx := loadRepoIndex(t)
	for id, entry := range idx.Providers {
		path := filepath.Join(repoCatalogDir, entry.File)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("index lists %q with unreadable file %q: %v", id, entry.File, err)
			continue
		}
		var p Provider
		if err := json.Unmarshal(data, &p); err != nil {
			t.Errorf("unmarshal %s: %v", path, err)
			continue
		}
		if entry.ModelCount != len(p.Models) {
			t.Errorf("index[%q].model_count = %d, provider file has %d models", id, entry.ModelCount, len(p.Models))
		}
	}
}
