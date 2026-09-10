package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEmbeddedCatalogLoads(t *testing.T) {
	if len(Providers()) == 0 {
		t.Fatal("embedded catalog empty")
	}
	if _, ok := ProviderByID("openai"); !ok {
		t.Fatal("openai missing from embedded catalog")
	}
	models := ModelsForProvider("anthropic")
	if len(models) == 0 {
		t.Fatal("anthropic has no models")
	}
	found := false
	for _, m := range models {
		if m.ContextWindow > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("anthropic models lack context_window")
	}
}

func TestDefaultAPIBaseByType(t *testing.T) {
	cases := map[string]string{
		"openai":      "https://api.openai.com/v1",
		"xai":         "https://api.x.ai/v1",
		"nous":        "https://inference-api.nousresearch.com/v1",
		"lmstudio":    "http://127.0.0.1:1234/v1",
		"vercel":      "https://ai-gateway.vercel.sh/v1",
		"huggingface": "https://router.huggingface.co/v1",
		"grok":        "https://api.x.ai/v1", // alias
	}
	for typ, want := range cases {
		if got := DefaultAPIBaseByType(typ); got != want {
			t.Errorf("DefaultAPIBaseByType(%q)=%q want %q", typ, got, want)
		}
	}
}

func TestFindModelAndDefaults(t *testing.T) {
	m, ok := FindModel("openai", "gpt-4o")
	if !ok {
		// may be filtered; try any model
		models := ModelsForProvider("openai")
		if len(models) == 0 {
			t.Fatal("no openai models")
		}
		m, ok = FindModel("openai", models[0].ID)
		if !ok {
			t.Fatalf("FindModel failed for %s", models[0].ID)
		}
	}
	d, ok := DefaultsFor("openai", m.ID)
	if !ok {
		t.Fatalf("DefaultsFor %s failed", m.ID)
	}
	if d.ContextWindow <= 0 {
		t.Errorf("context window should be >0, got %d", d.ContextWindow)
	}
	if d.MaxTokens <= 0 {
		t.Errorf("max tokens should be >0, got %d", d.MaxTokens)
	}
}

func TestSearchModels(t *testing.T) {
	all := ModelsForProvider("deepseek")
	if len(all) == 0 {
		t.Skip("no deepseek models")
	}
	q := all[0].ID
	hits := SearchModels("deepseek", q)
	if len(hits) == 0 {
		t.Fatalf("search %q returned nothing", q)
	}
}

func TestConvertModelsDev(t *testing.T) {
	raw := map[string]any{
		"openai": map[string]any{
			"name": "OpenAI",
			"api":  "https://api.openai.com/v1",
			"models": map[string]any{
				"gpt-4o": map[string]any{
					"id":         "gpt-4o",
					"name":       "GPT-4o",
					"attachment": true,
					"tool_call":  true,
					"limit":      map[string]any{"context": 128000, "output": 16384},
					"modalities": map[string]any{"input": []string{"text", "image"}},
				},
				"o3": map[string]any{
					"id":        "o3",
					"name":      "o3",
					"reasoning": true,
					"reasoning_options": []map[string]any{
						{"type": "effort", "values": []string{"low", "medium", "high"}},
					},
					"limit": map[string]any{"context": 200000, "output": 100000},
				},
			},
		},
		"xai": map[string]any{
			"name":   "xAI",
			"api":    "https://api.x.ai/v1",
			"models": map[string]any{},
		},
	}
	data, _ := json.Marshal(raw)
	snap, err := ConvertModelsDev(data, []string{"openai", "xai"})
	if err != nil {
		t.Fatal(err)
	}
	p, ok := snap.Providers["openai"]
	if !ok || len(p.Models) != 2 {
		t.Fatalf("openai models = %+v", p.Models)
	}
	var o3 Model
	for _, m := range p.Models {
		if m.ID == "o3" {
			o3 = m
		}
	}
	if o3.ContextWindow != 200000 || len(o3.ThinkingLevels) != 3 {
		t.Fatalf("o3 metadata wrong: %+v", o3)
	}
}

func TestPrefetchUsesDiskCacheWhenFresh(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "models_dev.json")
	snap := Snapshot{
		Version:   1,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Source:    "test",
		Providers: map[string]Provider{
			"xai": {
				ID: "xai", Name: "xAI", Type: "openai",
				APIBase: "https://api.x.ai/v1",
				Models:  []Model{{ID: "grok-4", ContextWindow: 256000, MaxOutput: 64000}},
			},
		},
	}
	data, _ := json.Marshal(snap)
	if err := os.WriteFile(cache, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Point URL at a closed server so a stale-cache miss cannot hit models.dev.
	err := Prefetch(context.Background(), PrefetchOptions{
		URL:        "http://127.0.0.1:0/models-dev-disabled",
		CachePath:  cache,
		TTL:        time.Hour,
		HTTPClient: &http.Client{Timeout: 50 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	models := ModelsForProvider("xai")
	if len(models) != 1 || models[0].ID != "grok-4" {
		t.Fatalf("live overlay not applied: %+v", models)
	}
}

func TestPrefetchFetchesAndCaches(t *testing.T) {
	payload := map[string]any{
		"nous-portal": map[string]any{ // unmapped — ignored
			"name": "X", "models": map[string]any{},
		},
		"xai": map[string]any{
			"name": "xAI",
			"api":  "https://api.x.ai/v1",
			"models": map[string]any{
				"grok-4-fast": map[string]any{
					"id": "grok-4-fast", "name": "Grok 4 Fast",
					"limit": map[string]any{"context": 200000, "output": 32000},
				},
			},
		},
	}
	body, _ := json.Marshal(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cache := filepath.Join(dir, "models_dev.json")
	err := Prefetch(context.Background(), PrefetchOptions{
		URL:        srv.URL,
		CachePath:  cache,
		TTL:        time.Nanosecond, // force network
		HTTPClient: srv.Client(),
		Providers:  []string{"xai"},
	})
	if err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	models := ModelsForProvider("xai")
	if len(models) != 1 || models[0].ID != "grok-4-fast" {
		t.Fatalf("models after prefetch: %+v", models)
	}
	if models[0].ContextWindow != 200000 {
		t.Fatalf("context not converted: %+v", models[0])
	}
}
