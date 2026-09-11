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

func TestDefaultAPIBaseByType_Hardcoded(t *testing.T) {
	cases := map[string]string{
		"openai":          "https://api.openai.com/v1",
		"xai":             "https://api.x.ai/v1",
		"nous":            "https://inference-api.nousresearch.com/v1",
		"lmstudio":        "http://127.0.0.1:1234/v1",
		"vercel":          "https://ai-gateway.vercel.sh/v1",
		"huggingface":     "https://router.huggingface.co/v1",
		"grok":            "https://api.x.ai/v1",
		"kimi_for_coding": "https://api.kimi.com/coding/v1",
	}
	for typ, want := range cases {
		if got := DefaultAPIBaseByType(typ); got != want {
			t.Errorf("DefaultAPIBaseByType(%q)=%q want %q", typ, got, want)
		}
	}
}

func TestKnownProviderTypes_WithoutCache(t *testing.T) {
	ResetLive()
	ids := KnownProviderTypes()
	if len(ids) == 0 {
		t.Fatal("expected hardcoded provider ids")
	}
	found := false
	for _, id := range ids {
		if id == "openai" {
			found = true
		}
	}
	if !found {
		t.Fatalf("openai missing from %v", ids)
	}
}

func TestModelsForProvider_LoadsPerProviderFile(t *testing.T) {
	ResetLive()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// Point cache under temp home.
	// CacheDir uses UserHomeDir which reads $HOME on unix.
	provDir := filepath.Join(CacheDir(), "providers")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := Provider{
		ID: "openai", Name: "OpenAI", Type: "openai",
		APIBase: "https://api.openai.com/v1",
		Models: []Model{
			{ID: "gpt-4o", ContextWindow: 128000, MaxOutput: 16384, Vision: true},
		},
	}
	data, _ := json.Marshal(p)
	if err := os.WriteFile(filepath.Join(provDir, "openai.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	models := ModelsForProvider("openai")
	if len(models) != 1 || models[0].ID != "gpt-4o" {
		t.Fatalf("models = %+v", models)
	}
	d, ok := DefaultsFor("openai", "gpt-4o")
	if !ok || d.ContextWindow != 128000 || !d.Vision {
		t.Fatalf("defaults = %+v ok=%v", d, ok)
	}
}

func TestRefresh_DownloadsIndexAndProviders(t *testing.T) {
	ResetLive()
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Index{
			Version: 1,
			Providers: map[string]IndexEntry{
				"xai": {ID: "xai", Name: "xAI", APIBase: "https://api.x.ai/v1", File: "providers/xai.json", ModelCount: 1},
			},
		})
	})
	mux.HandleFunc("/providers/xai.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Provider{
			ID: "xai", Name: "xAI", Type: "openai", APIBase: "https://api.x.ai/v1",
			Models: []Model{{ID: "grok-4", ContextWindow: 256000, ThinkingLevels: []string{"high"}}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := Refresh(context.Background(), Options{BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if !HasCachedIndex() {
		t.Fatal("index not cached")
	}
	if !HasCachedProvider("xai") {
		t.Fatal("xai not cached")
	}
	models := ModelsForProvider("xai")
	if len(models) != 1 || models[0].ID != "grok-4" {
		t.Fatalf("models after refresh: %+v", models)
	}
	// Only xai file was requested — ensure openai was not written.
	if HasCachedProvider("openai") {
		t.Fatal("unexpected openai cache file")
	}
}

func TestFetchProvider_Single(t *testing.T) {
	ResetLive()
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Index{
			Version: 1,
			Providers: map[string]IndexEntry{
				"anthropic": {ID: "anthropic", File: "providers/anthropic.json"},
			},
		})
	})
	mux.HandleFunc("/providers/anthropic.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Provider{
			ID: "anthropic", Models: []Model{{ID: "claude-sonnet-4", ContextWindow: 200000}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := FetchProvider(context.Background(), "anthropic", Options{BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("FetchProvider: %v", err)
	}
	models := ModelsForProvider("anthropic")
	if len(models) != 1 {
		t.Fatalf("models: %+v", models)
	}
}

func TestPrefetch_UsesFreshDiskCache(t *testing.T) {
	ResetLive()
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	idx := Index{
		Version: 1,
		Providers: map[string]IndexEntry{
			"xai": {ID: "xai", APIBase: "https://api.x.ai/v1"},
		},
	}
	data, _ := json.Marshal(idx)
	if err := os.MkdirAll(CacheDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(IndexPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}

	err := Prefetch(context.Background(), PrefetchOptions{
		URL:        "http://127.0.0.1:0/disabled",
		TTL:        time.Hour,
		HTTPClient: &http.Client{Timeout: 50 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("Prefetch with fresh cache: %v", err)
	}
	if DefaultAPIBaseByType("xai") != "https://api.x.ai/v1" {
		t.Fatalf("index api base not applied: %q", DefaultAPIBaseByType("xai"))
	}
}

func TestSearchModels(t *testing.T) {
	ResetLive()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	p := Provider{
		ID: "deepseek",
		Models: []Model{
			{ID: "deepseek-chat", ContextWindow: 65536},
			{ID: "deepseek-reasoner", ContextWindow: 65536, Reasoning: true},
		},
	}
	data, _ := json.Marshal(p)
	_ = os.MkdirAll(filepath.Join(CacheDir(), "providers"), 0o755)
	_ = os.WriteFile(filepath.Join(CacheDir(), "providers", "deepseek.json"), data, 0o644)

	hits := SearchModels("deepseek", "reasoner")
	if len(hits) != 1 || hits[0].ID != "deepseek-reasoner" {
		t.Fatalf("hits: %+v", hits)
	}
}

func TestSetCacheDir_OverrideAndReset(t *testing.T) {
	original := CacheDir()

	dir := filepath.Join(t.TempDir(), "custom-catalog")
	SetCacheDir(dir)
	t.Cleanup(ResetCacheDir)

	if got := CacheDir(); got != dir {
		t.Fatalf("CacheDir() after SetCacheDir = %q, want %q", got, dir)
	}

	ResetCacheDir()
	if got := CacheDir(); got == dir {
		t.Fatalf("CacheDir() after ResetCacheDir still = %q, should revert", got)
	}
	if got := CacheDir(); got != original {
		t.Fatalf("CacheDir() after ResetCacheDir = %q, want %q (original)", got, original)
	}
}
