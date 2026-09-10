package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// modelsDevURL is the upstream models.dev registry used for prefetch.
const modelsDevURL = "https://models.dev/api.json"

// DefaultPrefetchTTL is how long a disk cache is considered fresh.
const DefaultPrefetchTTL = 24 * time.Hour

// PrefetchOptions controls a models.dev refresh.
type PrefetchOptions struct {
	// URL overrides the upstream catalog URL (tests).
	URL string
	// TTL defaults to DefaultPrefetchTTL when zero.
	TTL time.Duration
	// HTTPClient defaults to a 30s client.
	HTTPClient *http.Client
	// CachePath defaults to CachePath().
	CachePath string
	// Providers limits conversion to these provider IDs (empty = all mapped).
	Providers []string
}

type prefetchState struct {
	mu        sync.Mutex
	running   bool
	lastError error
	lastAt    time.Time
}

var prefetch = &prefetchState{}

// LastPrefetch returns the time of the last successful refresh and any error.
func LastPrefetch() (time.Time, error) {
	prefetch.mu.Lock()
	defer prefetch.mu.Unlock()
	return prefetch.lastAt, prefetch.lastError
}

// IsPrefetchFresh reports whether the default disk cache is newer than ttl.
func IsPrefetchFresh(ttl time.Duration) bool {
	return IsCacheFileFresh(CachePath(), ttl)
}

// IsCacheFileFresh reports whether the file at path is newer than ttl.
func IsCacheFileFresh(path string, ttl time.Duration) bool {
	if path == "" {
		path = CachePath()
	}
	if ttl <= 0 {
		ttl = DefaultPrefetchTTL
	}
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(st.ModTime()) < ttl
}

// Prefetch fetches models.dev, converts known providers, merges into the live
// overlay, and writes the disk cache. Safe for concurrent callers (single-flight).
func Prefetch(ctx context.Context, opts PrefetchOptions) error {
	if opts.URL == "" {
		opts.URL = modelsDevURL
	}
	if opts.TTL <= 0 {
		opts.TTL = DefaultPrefetchTTL
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.CachePath == "" {
		opts.CachePath = CachePath()
	}

	prefetch.mu.Lock()
	if prefetch.running {
		prefetch.mu.Unlock()
		return fmt.Errorf("catalog prefetch already running")
	}
	prefetch.running = true
	prefetch.mu.Unlock()

	defer func() {
		prefetch.mu.Lock()
		prefetch.running = false
		prefetch.mu.Unlock()
	}()

	// Prefer fresh disk cache.
	if IsCacheFileFresh(opts.CachePath, opts.TTL) {
		if err := LoadDiskCache(opts.CachePath); err == nil {
			prefetch.mu.Lock()
			prefetch.lastAt = time.Now()
			prefetch.lastError = nil
			prefetch.mu.Unlock()
			return nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
	if err != nil {
		prefetch.mu.Lock()
		prefetch.lastError = err
		prefetch.mu.Unlock()
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "lele-catalog/1.0")

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		// Fall back to stale disk cache.
		if loadErr := LoadDiskCache(opts.CachePath); loadErr == nil {
			prefetch.mu.Lock()
			prefetch.lastAt = time.Now()
			prefetch.lastError = err
			prefetch.mu.Unlock()
			return nil
		}
		prefetch.mu.Lock()
		prefetch.lastError = err
		prefetch.mu.Unlock()
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		err := fmt.Errorf("models.dev returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		if loadErr := LoadDiskCache(opts.CachePath); loadErr == nil {
			prefetch.mu.Lock()
			prefetch.lastAt = time.Now()
			prefetch.lastError = err
			prefetch.mu.Unlock()
			return nil
		}
		prefetch.mu.Lock()
		prefetch.lastError = err
		prefetch.mu.Unlock()
		return err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		prefetch.mu.Lock()
		prefetch.lastError = err
		prefetch.mu.Unlock()
		return err
	}

	converted, err := ConvertModelsDev(body, opts.Providers)
	if err != nil {
		prefetch.mu.Lock()
		prefetch.lastError = err
		prefetch.mu.Unlock()
		return err
	}

	ApplyLive(converted)

	// Persist converted snapshot (not raw models.dev) so offline reload is small
	// and schema-stable. Also keep raw for debugging when space allows — we
	// store the converted form only.
	if err := writeJSONAtomic(opts.CachePath, converted); err != nil {
		// Non-fatal: live overlay is already applied.
		prefetch.mu.Lock()
		prefetch.lastError = err
		prefetch.mu.Unlock()
		return nil
	}

	prefetch.mu.Lock()
	prefetch.lastAt = time.Now()
	prefetch.lastError = nil
	prefetch.mu.Unlock()
	return nil
}

// LoadDiskCache reads a previously converted snapshot and applies it as live overlay.
func LoadDiskCache(path string) error {
	if path == "" {
		path = CachePath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if len(s.Providers) == 0 {
		return fmt.Errorf("empty catalog cache")
	}
	ApplyLive(s)
	return nil
}

// ApplyLive replaces the live overlay (prefetched providers) without touching
// the embedded snapshot.
func ApplyLive(s Snapshot) {
	live := make(map[string]Provider, len(s.Providers))
	for k, p := range s.Providers {
		id := normalizeType(k)
		p.ID = id
		// Merge: keep embedded curated models if live list is empty.
		if len(p.Models) == 0 {
			if emb, ok := snapshot.Providers[id]; ok {
				p.Models = emb.Models
			}
		}
		live[id] = p
	}
	mu.Lock()
	liveProviders = live
	mu.Unlock()
}

// ConvertModelsDev transforms raw models.dev JSON into a lele Snapshot for the
// providers we know how to map. allowIDs empty means all mappable providers.
func ConvertModelsDev(raw []byte, allowIDs []string) (Snapshot, error) {
	var src map[string]struct {
		Name   string `json:"name"`
		API    string `json:"api"`
		Models map[string]struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Attachment   bool   `json:"attachment"`
			Reasoning    bool   `json:"reasoning"`
			ToolCall     bool   `json:"tool_call"`
			Status       string `json:"status"`
			ReasoningOpt []struct {
				Type   string   `json:"type"`
				Values []string `json:"values"`
			} `json:"reasoning_options"`
			Modalities struct {
				Input []string `json:"input"`
			} `json:"modalities"`
			Limit struct {
				Context int `json:"context"`
				Output  int `json:"output"`
			} `json:"limit"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &src); err != nil {
		return Snapshot{}, fmt.Errorf("parse models.dev: %w", err)
	}

	allow := map[string]bool{}
	for _, id := range allowIDs {
		allow[normalizeType(id)] = true
	}

	out := Snapshot{
		Version:   1,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Source:    "models.dev",
		Providers: map[string]Provider{},
	}

	for leleID, mdevID := range mdevToLele {
		if len(allow) > 0 && !allow[leleID] {
			continue
		}
		srcP, ok := src[mdevID]
		if !ok {
			continue
		}
		p := Provider{
			ID:      leleID,
			Name:    srcP.Name,
			Type:    "openai",
			APIBase: srcP.API,
		}
		if base := DefaultAPIBaseByType(leleID); base != "" {
			p.APIBase = base
		}
		if t := transportByLeleID(leleID); t != "" {
			p.Type = t
		}
		models := make([]Model, 0, len(srcP.Models))
		for _, m := range srcP.Models {
			if m.ID == "" || m.Status == "deprecated" {
				continue
			}
			vision := m.Attachment
			for _, in := range m.Modalities.Input {
				if strings.EqualFold(in, "image") {
					vision = true
				}
			}
			var levels []string
			for _, opt := range m.ReasoningOpt {
				if !strings.EqualFold(opt.Type, "effort") {
					continue
				}
				for _, v := range opt.Values {
					v = strings.ToLower(strings.TrimSpace(v))
					if v == "max" {
						v = "high"
					}
					if v == "low" || v == "medium" || v == "high" {
						levels = appendUnique(levels, v)
					}
				}
			}
			if m.Reasoning && len(levels) == 0 {
				levels = []string{"low", "medium", "high"}
			}
			models = append(models, Model{
				ID:             m.ID,
				Name:           m.Name,
				ContextWindow:  m.Limit.Context,
				MaxOutput:      m.Limit.Output,
				Vision:         vision,
				ThinkingLevels: levels,
				Reasoning:      m.Reasoning,
				ToolCall:       m.ToolCall,
			})
		}
		sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
		p.Models = models
		out.Providers[leleID] = p
	}

	if len(out.Providers) == 0 {
		return Snapshot{}, fmt.Errorf("no mappable providers in models.dev payload")
	}
	return out, nil
}

// mdevToLele maps lele catalog IDs to models.dev provider IDs.
var mdevToLele = map[string]string{
	"openai":              "openai",
	"anthropic":           "anthropic",
	"openrouter":          "openrouter",
	"groq":                "groq",
	"deepseek":            "deepseek",
	"gemini":              "google",
	"zhipu":               "zhipuai",
	"zai":                 "zai",
	"zai_coding_plan":     "zai-coding-plan",
	"moonshot":            "moonshotai-cn",
	"kimi_for_coding":     "kimi-for-coding",
	"nvidia":              "nvidia",
	"ollama_cloud":        "ollama-cloud",
	"chutes":              "chutes",
	"alibaba":             "alibaba",
	"alibaba_coding_plan": "alibaba-coding-plan",
	"xai":                 "xai",
	"lmstudio":            "lmstudio",
	"stepfun":             "stepfun-ai-step-plan",
	"minimax":             "minimax",
	"minimax_cn":          "minimax-cn",
	"vercel":              "vercel",
	"opencode":            "opencode",
	"opencode_go":         "opencode-go",
	"huggingface":         "huggingface",
	"novita":              "novita-ai",
	"xiaomi":              "xiaomi",
	"tencent_tokenhub":    "tencent-tokenhub",
	"arcee":               "arcee",
	"gmi":                 "gmicloud",
	"cerebras":            "cerebras",
	"together":            "togetherai",
	"fireworks":           "fireworks-ai",
	"mistral":             "mistral",
	"siliconflow":         "siliconflow",
	"perplexity":          "perplexity-agent",
	"azure_foundry":       "azure",
	"bedrock":             "amazon-bedrock",
}

func transportByLeleID(id string) string {
	switch normalizeType(id) {
	case "anthropic", "minimax", "minimax_cn":
		return "anthropic"
	default:
		return "openai"
	}
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
