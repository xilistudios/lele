package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/catalog"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/security"
	"github.com/xilistudios/lele/pkg/skills"
)

func seedCatalogForTest(t *testing.T) {
	t.Helper()
	t.Setenv("LELE_CATALOG_OFFLINE", "1")
	cacheDir := filepath.Join(t.TempDir(), ".lele", "cache", "catalog")
	catalog.SetCacheDir(cacheDir)
	t.Cleanup(catalog.ResetCacheDir)
	catalog.ResetLive()
	if err := catalog.SeedProvider(catalog.Provider{
		ID: "openai", Name: "OpenAI", Type: "openai",
		APIBase: "https://api.openai.com/v1",
		Models: []catalog.Model{
			{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000, MaxOutput: 16384, Vision: true},
			{ID: "gpt-4.1", Name: "GPT-4.1", ContextWindow: 1000000, MaxOutput: 32768, Vision: true},
		},
	}); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
}

func newCatalogTestServer(t *testing.T, named map[string]config.NamedProviderConfig) *nativeTestServer {
	t.Helper()
	seedCatalogForTest(t)

	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 18793
	cfg.Channels.Native.TokenExpiryDays = 30
	cfg.Channels.Native.PinExpiryMinutes = 5
	cfg.Channels.Native.MaxClients = 5
	cfg.Channels.Native.SessionExpiryDays = 30
	cfg.Providers.Named = named

	msgBus := bus.NewMessageBus()
	loop := newNativeTestAgentLoop(cfg)
	auth, err := NewAuthManager(&cfg.Channels.Native, t.TempDir())
	if err != nil {
		t.Fatalf("NewAuthManager() error = %v", err)
	}

	channel := &NativeChannel{
		base:             NewBaseChannel(ChannelName, cfg.Channels.Native, msgBus, []string{}),
		cfg:              &cfg.Channels.Native,
		auth:             auth,
		bus:              msgBus,
		agentLoop:        loop,
		wsClients:        make(map[string]*WSClient),
		pinLimiter:       newRateLimiter(10, time.Minute),
		pairLimiter:      newRateLimiter(5, time.Minute),
		refreshLimiter:   newRateLimiter(20, time.Minute),
		authLogLimiter:   newRateLimiter(6, time.Minute),
		apiLimiter:       newRateLimiter(120, time.Minute),
		wsMessageLimiter: newRateLimiter(30, time.Minute),
		skillsLoader:     &skills.SkillsLoader{},
	}

	mux := http.NewServeMux()
	channel.RegisterRoutes(mux)
	server := httptest.NewServer(security.Middleware()(mux))
	t.Cleanup(server.Close)

	pending, err := auth.GeneratePIN("Test Desktop")
	if err != nil {
		t.Fatalf("GeneratePIN() error = %v", err)
	}
	client, token, _, err := auth.PairWithPIN(pending.PIN, "Test Desktop")
	if err != nil {
		t.Fatalf("PairWithPIN() error = %v", err)
	}

	return &nativeTestServer{
		channel:  channel,
		loop:     loop,
		bus:      msgBus,
		server:   server,
		token:    token,
		clientID: client.ClientID,
	}
}

func catalogAuthedGet(t *testing.T, ts *nativeTestServer, path string) (*http.Response, *json.Decoder) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.server.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp, json.NewDecoder(resp.Body)
}

func TestCatalogProvidersEndpoint(t *testing.T) {
	seedCatalogForTest(t)
	ts := newNativeTestServer(t)

	resp, dec := catalogAuthedGet(t, ts, "/api/v1/catalog/providers")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload CatalogProvidersResponse
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Providers) == 0 {
		t.Fatal("expected at least one catalog provider")
	}

	var openai *CatalogProviderSummary
	for i := range payload.Providers {
		if payload.Providers[i].ID == "openai" {
			openai = &payload.Providers[i]
			break
		}
	}
	if openai == nil {
		t.Fatal("openai missing from catalog providers")
	}
	if openai.ModelCount <= 0 {
		t.Fatalf("openai model_count = %d, want > 0", openai.ModelCount)
	}
	if openai.APIBase == "" {
		t.Fatal("openai api_base empty")
	}
}

func TestCatalogModelsByProvider(t *testing.T) {
	seedCatalogForTest(t)
	ts := newNativeTestServer(t)

	resp, dec := catalogAuthedGet(t, ts, "/api/v1/catalog/models?provider=openai")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload CatalogModelsResponse
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Provider != "openai" {
		t.Fatalf("provider = %q, want openai", payload.Provider)
	}
	if len(payload.Models) == 0 {
		t.Fatal("expected catalog models for openai")
	}
	foundMeta := false
	for _, m := range payload.Models {
		if m.ID == "" {
			t.Fatal("model with empty id")
		}
		if m.ContextWindow > 0 {
			foundMeta = true
		}
	}
	if !foundMeta {
		t.Fatal("expected at least one model with context_window")
	}
}

func TestCatalogModelsSearch(t *testing.T) {
	seedCatalogForTest(t)
	ts := newNativeTestServer(t)

	resp, dec := catalogAuthedGet(t, ts, "/api/v1/catalog/models?provider=openai&q=gpt")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload CatalogModelsResponse
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Query != "gpt" {
		t.Fatalf("query = %q, want gpt", payload.Query)
	}
	if len(payload.Models) == 0 {
		t.Fatal("expected search hits for q=gpt on openai")
	}
	for _, m := range payload.Models {
		if !strings.Contains(strings.ToLower(m.ID), "gpt") &&
			!strings.Contains(strings.ToLower(m.Name), "gpt") {
			t.Fatalf("unexpected search hit %q", m.ID)
		}
	}
}

func TestCatalogModelsMissingProviderParam(t *testing.T) {
	seedCatalogForTest(t)
	ts := newNativeTestServer(t)

	resp, _ := catalogAuthedGet(t, ts, "/api/v1/catalog/models")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestCatalogModelsUnknownProvider(t *testing.T) {
	seedCatalogForTest(t)
	ts := newNativeTestServer(t)

	resp, _ := catalogAuthedGet(t, ts, "/api/v1/catalog/models?provider=not-a-real-provider")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestCatalogPrefetchEndpointMocked(t *testing.T) {
	seedCatalogForTest(t)
	ts := newNativeTestServer(t)

	orig := catalogPrefetchFn
	t.Cleanup(func() { catalogPrefetchFn = orig })

	called := false
	catalogPrefetchFn = func(ctx context.Context, opts catalog.PrefetchOptions) error {
		called = true
		return nil
	}

	req, err := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/catalog/prefetch", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !called {
		t.Fatal("catalogPrefetchFn was not invoked")
	}

	var payload CatalogPrefetchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if !payload.OK {
		t.Fatal("expected ok=true")
	}
	if payload.Providers <= 0 || payload.Models <= 0 {
		t.Fatalf("stats = %d providers / %d models, want > 0", payload.Providers, payload.Models)
	}
}

func TestCatalogPrefetchEndpointError(t *testing.T) {
	ts := newNativeTestServer(t)

	orig := catalogPrefetchFn
	t.Cleanup(func() { catalogPrefetchFn = orig })
	catalogPrefetchFn = func(ctx context.Context, opts catalog.PrefetchOptions) error {
		return context.DeadlineExceeded
	}

	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/catalog/prefetch", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
}

func TestCatalogPrefetchEndpointConflict(t *testing.T) {
	ts := newNativeTestServer(t)

	orig := catalogPrefetchFn
	t.Cleanup(func() { catalogPrefetchFn = orig })
	catalogPrefetchFn = func(ctx context.Context, opts catalog.PrefetchOptions) error {
		return errPrefetchRunning
	}

	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/catalog/prefetch", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusConflict)
	}
}

var errPrefetchRunning = &prefetchRunningError{}

type prefetchRunningError struct{}

func (e *prefetchRunningError) Error() string { return "catalog prefetch already running" }

func TestProviderModelsOfflineFallbackNoAPIKey(t *testing.T) {
	ts := newCatalogTestServer(t, map[string]config.NamedProviderConfig{
		"openai": {
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{},
		},
	})

	resp, dec := catalogAuthedGet(t, ts, "/api/v1/providers/openai/models")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ProviderModelsResponse
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Source != "catalog" {
		t.Fatalf("source = %q, want catalog", payload.Source)
	}
	if len(payload.Models) == 0 {
		t.Fatal("expected catalog models offline")
	}
	if payload.Models[0].ID == "" {
		t.Fatal("model id empty")
	}
	enriched := false
	for _, m := range payload.Models {
		if m.ContextWindow > 0 {
			enriched = true
			break
		}
	}
	if !enriched {
		t.Fatal("expected catalog enrichment on offline models")
	}
}

func TestProviderModelsLiveFetchFailureFallsBackToCatalog(t *testing.T) {
	ts := newCatalogTestServer(t, map[string]config.NamedProviderConfig{
		"openai": {
			Type: "openai",
			ProviderConfig: config.ProviderConfig{
				APIKey:  "test-key",
				APIBase: "https://models-fetch-should-fail.invalid/v1",
			},
		},
	})

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/providers/openai/models", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d (catalog fallback)", resp.StatusCode, http.StatusOK)
	}

	var payload ProviderModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Source != "catalog" {
		t.Fatalf("source = %q, want catalog", payload.Source)
	}
	if len(payload.Models) == 0 {
		t.Fatal("expected catalog fallback models")
	}
}

func TestProviderModelsSSRFBlockedFallsBackToCatalog(t *testing.T) {
	ts := newCatalogTestServer(t, map[string]config.NamedProviderConfig{
		"openai": {
			Type: "openai",
			ProviderConfig: config.ProviderConfig{
				APIKey:  "test-key",
				APIBase: "http://127.0.0.1:9/v1",
			},
		},
	})

	resp, dec := catalogAuthedGet(t, ts, "/api/v1/providers/openai/models")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ProviderModelsResponse
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Source != "catalog" {
		t.Fatalf("source = %q, want catalog", payload.Source)
	}
	if len(payload.Models) == 0 {
		t.Fatal("expected catalog models after SSRF block")
	}
}

func TestEnrichProviderModelInfo(t *testing.T) {
	models := catalog.ModelsForProvider("openai")
	if len(models) == 0 {
		t.Skip("openai catalog empty")
	}
	var target catalog.Model
	for _, m := range models {
		if m.ContextWindow > 0 {
			target = m
			break
		}
	}
	if target.ID == "" {
		t.Skip("no openai model with context window")
	}

	info := ProviderModelInfo{ID: target.ID, Object: "model", OwnedBy: "openai"}
	enrichProviderModelInfo(&info, "openai")

	if info.ContextWindow != target.ContextWindow {
		t.Fatalf("context_window = %d, want %d", info.ContextWindow, target.ContextWindow)
	}
	if info.MaxOutput != target.MaxOutput {
		t.Fatalf("max_output = %d, want %d", info.MaxOutput, target.MaxOutput)
	}
	if info.Vision != target.Vision {
		t.Fatalf("vision = %v, want %v", info.Vision, target.Vision)
	}
	if info.Reasoning != target.Reasoning {
		t.Fatalf("reasoning = %v, want %v", info.Reasoning, target.Reasoning)
	}
	if len(info.ThinkingLevels) != len(target.ThinkingLevels) {
		t.Fatalf("thinking_levels = %v, want %v", info.ThinkingLevels, target.ThinkingLevels)
	}
	if info.Object != "model" || info.OwnedBy != "openai" {
		t.Fatalf("existing fields clobbered: %+v", info)
	}
	if info.ID != target.ID {
		t.Fatalf("id = %q, want %q", info.ID, target.ID)
	}
}

func TestEnrichProviderModelInfoUnknownModel(t *testing.T) {
	info := ProviderModelInfo{ID: "totally-unknown-model-xyz", Object: "model"}
	enrichProviderModelInfo(&info, "openai")
	if info.ContextWindow != 0 || info.Reasoning || info.Vision {
		t.Fatalf("unexpected enrichment: %+v", info)
	}
	if info.ID != "totally-unknown-model-xyz" {
		t.Fatalf("id changed: %q", info.ID)
	}
}

func TestCatalogModelsToInfos(t *testing.T) {
	infos := catalogModelsToInfos([]catalog.Model{
		{ID: "m1", Name: "Model One", ContextWindow: 128000, MaxOutput: 8192, Vision: true},
	})
	if len(infos) != 1 {
		t.Fatalf("len = %d, want 1", len(infos))
	}
	if infos[0].ID != "m1" || infos[0].Name != "Model One" || infos[0].ContextWindow != 128000 {
		t.Fatalf("unexpected info: %+v", infos[0])
	}
	if infos[0].Object != "model" {
		t.Fatalf("object = %q, want model", infos[0].Object)
	}
}

func TestCatalogEndpointsRequireAuth(t *testing.T) {
	ts := newNativeTestServer(t)

	paths := []string{
		"/api/v1/catalog/providers",
		"/api/v1/catalog/models?provider=openai",
	}
	for _, path := range paths {
		req, _ := http.NewRequest(http.MethodGet, ts.server.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do(%s) error = %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET %s status = %d, want %d", path, resp.StatusCode, http.StatusUnauthorized)
		}
	}

	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/catalog/prefetch", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(prefetch) error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST prefetch status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestCatalogProvidersEndpointServesEveryKnownType(t *testing.T) {
	t.Setenv("LELE_CATALOG_OFFLINE", "1")
	catalog.SetCacheDir(t.TempDir())
	t.Cleanup(catalog.ResetCacheDir)
	catalog.ResetLive()

	ts := newNativeTestServer(t)
	resp, dec := catalogAuthedGet(t, ts, "/api/v1/catalog/providers")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload CatalogProvidersResponse
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}

	byID := make(map[string]CatalogProviderSummary, len(payload.Providers))
	for _, p := range payload.Providers {
		byID[p.ID] = p
	}

	for _, id := range catalog.KnownProviderTypes() {
		p, ok := byID[id]
		if !ok {
			t.Errorf("catalog providers response is missing known id %q", id)
			continue
		}
		if catalog.IsBaseLessProviderType(id) {
			if p.APIBase != "" {
				t.Errorf("%q api_base = %q, want empty (base-less type)", id, p.APIBase)
			}
			continue
		}
		if p.APIBase == "" {
			t.Errorf("%q api_base is empty: the wizard would ship a tile with no endpoint", id)
		}
	}

	for _, id := range []string{
		"vercel", "xai", "lmstudio", "ollama_cloud", "huggingface", "stepfun",
		"minimax", "mistral", "together", "fireworks", "cerebras", "gmi",
		"arcee", "novita", "shengsuanyun", "nanogpt", "perplexity",
		"siliconflow", "chutes", "nous", "vllm", "qwen_portal",
		"tencent_tokenhub", "xiaomi", "kimi_for_coding", "alibaba",
		"alibaba_coding_plan", "alibaba_token_plan", "alibaba_token_plan_cn",
		"bedrock", "azure_foundry",
	} {
		p, ok := byID[id]
		if !ok {
			t.Errorf("issue #404 id %q missing from the catalog providers response", id)
			continue
		}
		if !catalog.IsBaseLessProviderType(id) && p.APIBase == "" {
			t.Errorf("issue #404 id %q has no api_base", id)
		}
		if p.BaseLess != catalog.IsBaseLessProviderType(id) {
			t.Errorf("%q base_less = %v, want %v", id, p.BaseLess, catalog.IsBaseLessProviderType(id))
		}
	}
}

func TestProviderModelsForUnconfiguredTypeServesCatalog(t *testing.T) {
	ts := newCatalogTestServer(t, nil)
	if err := catalog.SeedProvider(catalog.Provider{
		ID: "xai", Name: "xAI", Type: "openai",
		APIBase: "https://api.x.ai/v1",
		Models:  []catalog.Model{{ID: "grok-4", Name: "Grok 4"}},
	}); err != nil {
		t.Fatalf("seed xai: %v", err)
	}

	resp, dec := catalogAuthedGet(t, ts, "/api/v1/providers/xai/models")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ProviderModelsResponse
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Provider != "xai" {
		t.Errorf("provider = %q, want xai", payload.Provider)
	}
	if payload.Source != "catalog" {
		t.Errorf("source = %q, want catalog", payload.Source)
	}
	if len(payload.Models) != 1 || payload.Models[0].ID != "grok-4" {
		t.Fatalf("models = %+v, want the seeded grok-4", payload.Models)
	}
}

func TestProviderModelsForKnownTypeWithoutModelsStill404(t *testing.T) {
	ts := newCatalogTestServer(t, nil)

	resp, _ := catalogAuthedGet(t, ts, "/api/v1/providers/vercel/models")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestProviderModelsForUnknownNameStill404(t *testing.T) {
	ts := newCatalogTestServer(t, nil)

	resp, _ := catalogAuthedGet(t, ts, "/api/v1/providers/definitely-not-a-provider/models")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}
func seedOpencodeGoCatalog(t *testing.T) {
	t.Helper()
	if err := catalog.SeedProvider(catalog.Provider{
		ID: "opencode_go", Name: "OpenCode Go", Type: "openai",
		APIBase: "https://opencode.ai/zen/go/v1",
		Models: []catalog.Model{
			{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", ContextWindow: 1050000, ToolCall: true, Protocol: catalog.ProtocolResponses},
			{ID: "grok-4.7", Name: "Grok 4.7", ContextWindow: 500000, ToolCall: true, Protocol: catalog.ProtocolResponses},
			{ID: "glm-5.1", Name: "GLM-5.1", ContextWindow: 204800, ToolCall: true},
			{ID: "minimax-m2.7", Name: "MiniMax M2.7", ContextWindow: 204800, ToolCall: true, Protocol: catalog.ProtocolAnthropicMessages},
			{ID: "minimax-m3.1-flash", Name: "MiniMax M3.1 Flash", ContextWindow: 1000000, ToolCall: true},
		},
	}); err != nil {
		t.Fatalf("seed opencode_go: %v", err)
	}
}

func TestCatalogModelsFiltersUnsupportedProtocols(t *testing.T) {
	ts := newCatalogTestServer(t, nil)
	seedOpencodeGoCatalog(t)

	resp, dec := catalogAuthedGet(t, ts, "/api/v1/catalog/models?provider=opencode_go")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload CatalogModelsResponse
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Models) == 0 {
		t.Fatal("expected the usable opencode_go models")
	}

	got := make([]string, 0, len(payload.Models))
	for _, m := range payload.Models {
		got = append(got, m.ID)
		if !m.SupportsChatCompletions() {
			t.Errorf("picker offers %q (protocol %q): the endpoint answers 400 ModelProtocolUnsupported", m.ID, m.Protocol)
		}
	}
	for _, unwanted := range []string{"gpt-5.6-luna", "grok-4.7", "minimax-m2.7"} {
		for _, id := range got {
			if id == unwanted {
				t.Errorf("picker offers unsupported id %q (models = %v)", unwanted, got)
			}
		}
	}
	if len(got) != 2 || got[0] != "glm-5.1" || got[1] != "minimax-m3.1-flash" {
		t.Fatalf("models = %v, want [glm-5.1 minimax-m3.1-flash]", got)
	}
}

func TestCatalogModelsFirstEntryIsUsable(t *testing.T) {
	ts := newCatalogTestServer(t, nil)
	seedOpencodeGoCatalog(t)

	resp, dec := catalogAuthedGet(t, ts, "/api/v1/catalog/models?provider=opencode_go")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload CatalogModelsResponse
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Models) == 0 {
		t.Fatal("expected at least one usable model")
	}
	first := payload.Models[0]
	if !first.SupportsChatCompletions() {
		t.Fatalf("first picker entry is %q (protocol %q): it must speak chat/completions", first.ID, first.Protocol)
	}
	if first.ID == "gpt-5.6-luna" {
		t.Fatalf("first picker entry is the protocol-unsupported %q", first.ID)
	}
}

func TestSearchModelsKeepsUnsupportedProtocols(t *testing.T) {
	newCatalogTestServer(t, nil)
	seedOpencodeGoCatalog(t)

	var found bool
	for _, m := range catalog.SearchModels("opencode_go", "") {
		if m.ID == "gpt-5.6-luna" {
			found = true
		}
	}
	if !found {
		t.Fatal("catalog.SearchModels() dropped a protocol-unsupported model: the filter belongs to the picker handler, not the shared lookup")
	}
}

func TestProviderModelsCatalogSourceFiltersUnsupportedProtocols(t *testing.T) {
	ts := newCatalogTestServer(t, nil)
	seedOpencodeGoCatalog(t)

	resp, dec := catalogAuthedGet(t, ts, "/api/v1/providers/opencode_go/models")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ProviderModelsResponse
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Source != "catalog" {
		t.Fatalf("source = %q, want catalog: an unconfigured provider is the new-user path from issue #405", payload.Source)
	}

	got := make([]string, 0, len(payload.Models))
	for _, m := range payload.Models {
		got = append(got, m.ID)
	}
	for _, unwanted := range []string{"gpt-5.6-luna", "grok-4.7", "minimax-m2.7"} {
		for _, id := range got {
			if id == unwanted {
				t.Errorf("provider model picker offers %q (models = %v): the endpoint answers 400 ModelProtocolUnsupported", unwanted, got)
			}
		}
	}
	if len(got) != 2 || got[0] != "glm-5.1" || got[1] != "minimax-m3.1-flash" {
		t.Fatalf("models = %v, want [glm-5.1 minimax-m3.1-flash]", got)
	}
}

func TestProviderModelsLiveSourceFiltersCatalogMarkedIDs(t *testing.T) {
	seedCatalogForTest(t)
	seedOpencodeGoCatalog(t)

	body := []byte(`{"data":[
		{"id":"gpt-5.6-luna","object":"model","owned_by":"opencode"},
		{"id":"grok-4.7","object":"model","owned_by":"opencode"},
		{"id":"glm-5.1","object":"model","owned_by":"opencode"},
		{"id":"minimax-m2.7","object":"model","owned_by":"opencode"},
		{"id":"live-only-unmeasured","object":"model","owned_by":"opencode"}
	]}`)

	models, err := liveProviderModels(body, "opencode_go")
	if err != nil {
		t.Fatalf("liveProviderModels() error = %v", err)
	}

	got := make([]string, 0, len(models))
	for _, m := range models {
		got = append(got, m.ID)
	}
	want := []string{"glm-5.1", "live-only-unmeasured"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("models = %v, want %v: catalog-marked ids leave, ids the catalog does not know pass through", got, want)
	}
	if models[0].ContextWindow != 204800 {
		t.Errorf("glm-5.1 context_window = %d, want the catalog value 204800", models[0].ContextWindow)
	}
}

func TestLiveProviderModelsParseError(t *testing.T) {
	if _, err := liveProviderModels([]byte("not json"), "opencode_go"); err == nil {
		t.Fatal("liveProviderModels() accepted a malformed payload")
	}
}

func TestProviderModelInfoCarriesProtocol(t *testing.T) {
	info := providerModelFromCatalog(catalog.Model{ID: "grok-4.7", Protocol: catalog.ProtocolResponses})
	if info.Protocol != catalog.ProtocolResponses {
		t.Fatalf("protocol = %q, want %q", info.Protocol, catalog.ProtocolResponses)
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"protocol":"responses"`) {
		t.Fatalf("Marshal() = %s, want the protocol marker on the wire so a client can filter", data)
	}
}
