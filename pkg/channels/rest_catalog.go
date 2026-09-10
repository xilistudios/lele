package channels

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/catalog"
)

// catalogPrefetchFn is swappable in tests so prefetch can be mocked.
var catalogPrefetchFn = func(ctx context.Context, opts catalog.PrefetchOptions) error {
	return catalog.Prefetch(ctx, opts)
}

// CatalogProviderSummary is a lightweight catalog provider listing entry.
type CatalogProviderSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	APIBase    string `json:"api_base,omitempty"`
	ModelCount int    `json:"model_count"`
}

// CatalogProvidersResponse is GET /api/v1/catalog/providers.
type CatalogProvidersResponse struct {
	Providers []CatalogProviderSummary `json:"providers"`
}

// CatalogModelsResponse is GET /api/v1/catalog/models?provider=<id>[&q=].
type CatalogModelsResponse struct {
	Provider string          `json:"provider"`
	Query    string          `json:"query,omitempty"`
	Models   []catalog.Model `json:"models"`
}

// CatalogPrefetchResponse is POST /api/v1/catalog/prefetch.
type CatalogPrefetchResponse struct {
	OK        bool   `json:"ok"`
	Providers int    `json:"providers"`
	Models    int    `json:"models"`
	FetchedAt string `json:"fetched_at"`
}

// handleCatalogProviders lists known catalog providers with model counts.
// GET /api/v1/catalog/providers
func (n *NativeChannel) handleCatalogProviders(w http.ResponseWriter, r *http.Request) {
	providers := catalog.Providers()
	ids := make([]string, 0, len(providers))
	for id := range providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]CatalogProviderSummary, 0, len(ids))
	for _, id := range ids {
		p := providers[id]
		// Prefer live overlay model counts when a prefetch cache is loaded.
		modelCount := len(catalog.ModelsForProvider(id))
		if modelCount == 0 {
			modelCount = len(p.Models)
		}
		name := p.Name
		if name == "" {
			name = id
		}
		out = append(out, CatalogProviderSummary{
			ID:         id,
			Name:       name,
			Type:       p.Type,
			APIBase:    p.APIBase,
			ModelCount: modelCount,
		})
	}

	writeJSON(w, http.StatusOK, CatalogProvidersResponse{Providers: out})
}

// handleCatalogModels returns catalog models for a provider, optionally filtered.
// GET /api/v1/catalog/models?provider=<id>&q=<query>
func (n *NativeChannel) handleCatalogModels(w http.ResponseWriter, r *http.Request) {
	provider := strings.TrimSpace(getQueryParam(r, "provider"))
	q := strings.TrimSpace(getQueryParam(r, "q"))

	if provider == "" {
		writeError(w, http.StatusBadRequest, "provider query parameter is required", "provider_missing")
		return
	}

	if _, ok := catalog.ProviderByID(provider); !ok {
		// Provider may still exist only as a live overlay entry with models.
		if len(catalog.ModelsForProvider(provider)) == 0 {
			writeError(w, http.StatusNotFound, fmt.Sprintf("provider %q not found in catalog", provider), "provider_not_found")
			return
		}
	}

	models := catalog.SearchModels(provider, q)
	if models == nil {
		models = []catalog.Model{}
	}

	writeJSON(w, http.StatusOK, CatalogModelsResponse{
		Provider: provider,
		Query:    q,
		Models:   models,
	})
}

// handleCatalogPrefetch triggers a GitHub catalog refresh.
// POST /api/v1/catalog/prefetch
func (n *NativeChannel) handleCatalogPrefetch(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	if err := catalogPrefetchFn(ctx, catalog.PrefetchOptions{}); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "already running") {
			writeError(w, http.StatusConflict, msg, "prefetch_in_progress")
			return
		}
		writeError(w, http.StatusBadGateway, fmt.Sprintf("catalog prefetch failed: %v", err), "prefetch_failed")
		return
	}

	providerCount, modelCount := catalogModelStats()
	fetchedAt, _ := catalog.LastDownload()
	fetched := ""
	if !fetchedAt.IsZero() {
		fetched = fetchedAt.UTC().Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, CatalogPrefetchResponse{
		OK:        true,
		Providers: providerCount,
		Models:    modelCount,
		FetchedAt: fetched,
	})
}

// catalogModelStats counts providers and models, preferring the live overlay.
func catalogModelStats() (providers, models int) {
	ids := make(map[string]struct{})
	for id := range catalog.Providers() {
		ids[id] = struct{}{}
	}
	for _, id := range catalog.KnownProviderTypes() {
		ids[id] = struct{}{}
	}
	for id := range ids {
		providers++
		models += len(catalog.ModelsForProvider(id))
	}
	return providers, models
}

// providerModelFromCatalog maps a catalog model onto ProviderModelInfo.
func providerModelFromCatalog(c catalog.Model) ProviderModelInfo {
	return ProviderModelInfo{
		ID:             c.ID,
		Object:         "model",
		Name:           c.Name,
		ContextWindow:  c.ContextWindow,
		MaxOutput:      c.MaxOutput,
		Vision:         c.Vision,
		ThinkingLevels: c.ThinkingLevels,
		Reasoning:      c.Reasoning,
		ToolCall:       c.ToolCall,
	}
}

// catalogModelsToInfos converts catalog models for offline autocomplete responses.
func catalogModelsToInfos(models []catalog.Model) []ProviderModelInfo {
	out := make([]ProviderModelInfo, 0, len(models))
	for _, m := range models {
		out = append(out, providerModelFromCatalog(m))
	}
	return out
}

// enrichProviderModelInfo fills empty live-fetched fields from the catalog.
// catalogKeys are tried in order (typically provider type then provider name).
func enrichProviderModelInfo(info *ProviderModelInfo, catalogKeys ...string) {
	if info == nil {
		return
	}
	for _, key := range catalogKeys {
		if key == "" {
			continue
		}
		c, ok := catalog.FindModel(key, info.ID)
		if !ok {
			continue
		}
		if info.Name == "" {
			info.Name = c.Name
		}
		if info.ContextWindow == 0 {
			info.ContextWindow = c.ContextWindow
		}
		if info.MaxOutput == 0 {
			info.MaxOutput = c.MaxOutput
		}
		if !info.Vision {
			info.Vision = c.Vision
		}
		if len(info.ThinkingLevels) == 0 {
			info.ThinkingLevels = c.ThinkingLevels
		}
		if !info.Reasoning {
			info.Reasoning = c.Reasoning
		}
		if !info.ToolCall {
			info.ToolCall = c.ToolCall
		}
		return
	}
}
