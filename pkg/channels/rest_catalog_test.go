package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/catalog"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/skills"
)

// seedCatalogForTest plants a minimal openai catalog file so offline fallback
// and autocomplete tests do not require network or an embedded catalog.
func seedCatalogForTest(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
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

// newCatalogTestServer builds a NativeChannel test server with custom named providers.
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
		apiLimiter:       newRateLimiter(120, time.Minute),
		wsMessageLimiter: newRateLimiter(30, time.Minute),
		skillsLoader:     &skills.SkillsLoader{},
	}

	mux := http.NewServeMux()
	channel.RegisterRoutes(mux)
	server := httptest.NewServer(channel.corsMiddleware(channel.securityHeadersMiddleware(mux)))
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
	// Mirrors catalog.Prefetch's concurrent-caller error text.
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

// errPrefetchRunning mirrors catalog.Prefetch's concurrent-caller error text.
var errPrefetchRunning = &prefetchRunningError{}

type prefetchRunningError struct{}

func (e *prefetchRunningError) Error() string { return "catalog prefetch already running" }

func TestProviderModelsOfflineFallbackNoAPIKey(t *testing.T) {
	// Provider exists but has no api_key — catalog models must be returned.
	ts := newCatalogTestServer(t, map[string]config.NamedProviderConfig{
		"openai": {
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{}, // no key
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
	// id field must stay present for existing clients.
	if payload.Models[0].ID == "" {
		t.Fatal("model id empty")
	}
	// At least some catalog models carry enrichment metadata.
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
	// api_base is a public HTTPS host that will fail DNS/connect; catalog wins.
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
	// Localhost api_base is blocked by the SSRF guard; catalog is still served.
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
	// Find a real catalog model to enrich against.
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
	// Existing fields preserved.
	if info.Object != "model" || info.OwnedBy != "openai" {
		t.Fatalf("existing fields clobbered: %+v", info)
	}
	// id field unchanged (client compat).
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
