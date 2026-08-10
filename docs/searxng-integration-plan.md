# SearXNG Integration Plan

## Overview

Add SearXNG as a self-hosted search provider for lele's `web_search` tool. SearXNG is a privacy-respecting metasearch engine that aggregates results from 70+ search engines. The integration allows users with a local SearXNG instance (e.g. `https://search.mapachoshome.com/`) to use it as the primary search backend — no API keys required, fully self-hosted, and privacy-first.

## Current Architecture

The search system lives in `pkg/tools/web.go` and follows a provider pattern:

```
SearchProvider interface { Search(ctx, query, count) (string, error) }
├── BraveSearchProvider        (API key required)
├── DuckDuckGoSearchProvider   (HTML scraping, fragile)
└── PerplexitySearchProvider   (API key required)
```

Config flows through 4 layers:
1. `config.go` — runtime structs (`WebToolsConfig`, `BraveConfig`, etc.)
2. `document_types.go` — editable structs with `SecretValue` (`EditableWebToolsConfig`, etc.)
3. `document_parse_tools.go` — JSON parsing with secret resolution
4. `document_convert.go` — bidirectional conversion (editable ↔ runtime)
5. `document_defaults.go` — default document values

Wiring: `tool_coordinator.go:368` reads config → builds `WebSearchToolOptions` → `NewWebSearchTool()` picks first enabled provider.

Priority: Perplexity > Brave > DuckDuckGo.

## SearXNG API

**Endpoint:** `GET /search?q={query}&format=json`

**Parameters:**
- `q` (required) — search query
- `format` — `json` (must be enabled in instance `settings.yml` under `search.formats`)
- `categories` — `general`, `images`, `news`, `science`, `it`, etc.
- `language` — language code (e.g. `es`, `en`, `auto`)
- `pageno` — page number (default 1)
- `time_range` — `day`, `month`, `year`
- `safesearch` — `0` (off), `1` (moderate), `2` (strict)

**JSON Response:**
```json
{
  "query": "...",
  "number_of_results": 42,
  "results": [
    {
      "url": "https://...",
      "title": "...",
      "content": "Snippet text...",
      "engine": "google",
      "score": 5.0
    }
  ],
  "suggestions": [],
  "unresponsive_engines": [["engine_name", "error_msg"], ...]
}
```

**Note:** The instance must have JSON format enabled:
```yaml
# settings.yml
search:
  formats:
    - html
    - json    # ← required
```

## Implementation

### Phase 1: SearXNG Provider (pkg/tools/web.go)

Add `SearXNGSearchProvider` struct implementing `SearchProvider`:

```go
type SearXNGSearchProvider struct {
    instanceURL string            // e.g. "https://search.mapachoshome.com"
    categories  string            // e.g. "general" (default)
    language    string            // e.g. "auto" (default)
    safesearch  int               // 0, 1, 2 (default 0)
}

func (p *SearXNGSearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
    // GET {instanceURL}/search?q={query}&format=json&categories={categories}&language={language}&pageno=1
    // Parse JSON response
    // Extract results[0..count-1]: title, url, content
    // Format as numbered list (same output format as other providers)
}
```

Key details:
- `format=json` must be requested — return a clear error if 403 is received ("SearXNG JSON format not enabled in instance settings")
- Timeout: 15s (slightly longer than others since it aggregates multiple engines)
- No API key needed
- Respect the `count` parameter (cap at `len(results)`)

### Phase 2: Config Types

**`pkg/config/config.go`** — add runtime struct:
```go
type SearXNGConfig struct {
    Enabled    bool   `json:"enabled" env:"LELE_TOOLS_WEB_SEARXNG_ENABLED"`
    InstanceURL string `json:"instance_url" env:"LELE_TOOLS_WEB_SEARXNG_INSTANCE_URL"`
    Categories string `json:"categories" env:"LELE_TOOLS_WEB_SEARXNG_CATEGORIES"`
    Language   string `json:"language" env:"LELE_TOOLS_WEB_SEARXNG_LANGUAGE"`
    SafeSearch int    `json:"safesearch" env:"LELE_TOOLS_WEB_SEARXNG_SAFESEARCH"`
    MaxResults int    `json:"max_results" env:"LELE_TOOLS_WEB_SEARXNG_MAX_RESULTS"`
}
```

Add to `WebToolsConfig`:
```go
type WebToolsConfig struct {
    Brave      BraveConfig      `json:"brave"`
    DuckDuckGo DuckDuckGoConfig `json:"duckduckgo"`
    Perplexity PerplexityConfig `json:"perplexity"`
    SearXNG    SearXNGConfig    `json:"searxng"`    // NEW
}
```

**`pkg/config/document_types.go`** — add editable struct:
```go
type EditableSearXNGConfig struct {
    Enabled      bool   `json:"enabled"`
    InstanceURL  string `json:"instance_url"`
    Categories   string `json:"categories"`
    Language     string `json:"language"`
    SafeSearch   int    `json:"safesearch"`
    MaxResults   int    `json:"max_results"`
}
```

Add to `EditableWebToolsConfig`:
```go
type EditableWebToolsConfig struct {
    Brave      EditableBraveConfig      `json:"brave"`
    DuckDuckGo DuckDuckGoConfig         `json:"duckduckgo"`
    Perplexity EditablePerplexityConfig `json:"perplexity"`
    SearXNG    EditableSearXNGConfig    `json:"searxng"`    // NEW
}
```

### Phase 3: Config Parsing & Conversion

**`pkg/config/document_parse_tools.go`** — parse SearXNG section:
```go
if searxngRaw, ok := toolsRaw["searxng"]; ok {
    var cfg EditableSearXNGConfig
    json.Unmarshal(searxngRaw, &cfg)
    // No secrets to resolve (no API key)
    doc.Tools.Web.SearXNG = cfg
}
```

**`pkg/config/document_convert.go`** — bidirectional:
- `Editable → Runtime`: `SearXNGConfig{Enabled, InstanceURL, Categories, Language, SafeSearch, MaxResults}`
- `Runtime → Editable`: mirror (no secrets)

**`pkg/config/document_defaults.go`** — add default:
```go
SearXNG: EditableSearXNGConfig{
    Enabled:    false,
    InstanceURL: "",
    Categories: "general",
    Language:   "auto",
    SafeSearch: 0,
    MaxResults: 5,
},
```

**`pkg/config/config.go`** default config:
```go
SearXNG: SearXNGConfig{
    Enabled:    false,
    InstanceURL: "",
    Categories: "general",
    Language:   "auto",
    SafeSearch: 0,
    MaxResults: 5,
},
```

### Phase 4: Tool Wiring

**`pkg/tools/web.go`** — update `WebSearchToolOptions`:
```go
type WebSearchToolOptions struct {
    // ... existing fields ...
    SearXNGEnabled    bool
    SearXNGInstanceURL string
    SearXNGCategories string
    SearXNGLanguage   string
    SearXNGSafeSearch int
    SearXNGMaxResults int
}
```

Update `NewWebSearchTool` priority chain:
```
Priority: Perplexity > Brave > SearXNG > DuckDuckGo
```

SearXNG sits between Brave and DuckDuckGo because:
- Self-hosted = no API costs, no rate limits
- Aggregates multiple engines (better than DDG HTML scraping)
- No API key needed
- But depends on instance availability

**`pkg/agent/tool_coordinator.go`** — pass SearXNG config:
```go
SearXNGEnabled:     cfg.Tools.Web.SearXNG.Enabled,
SearXNGInstanceURL: cfg.Tools.Web.SearXNG.InstanceURL,
SearXNGCategories:  cfg.Tools.Web.SearXNG.Categories,
SearXNGLanguage:    cfg.Tools.Web.SearXNG.Language,
SearXNGSafeSearch:  cfg.Tools.Web.SearXNG.SafeSearch,
SearXNGMaxResults:  cfg.Tools.Web.SearXNG.MaxResults,
```

### Phase 5: Config Example & Documentation

**`config/config.example.json`** — add SearXNG section under `tools.web`:
```json
{
  "tools": {
    "web": {
      "brave": { ... },
      "perplexity": { ... },
      "searxng": {
        "enabled": false,
        "instance_url": "https://search.mapachoshome.com",
        "categories": "general",
        "language": "auto",
        "safesearch": 0,
        "max_results": 5
      }
    }
  }
}
```

### Phase 6: Tests

**`pkg/tools/web_test.go`** — new tests:
- `TestSearXNGSearchProvider_Search_Success` — mock HTTP server returning JSON
- `TestSearXNGSearchProvider_Search_403_JSONDisabled` — clear error on 403
- `TestSearXNGSearchProvider_Search_Timeout` — timeout handling
- `TestSearXNGSearchProvider_Search_EmptyResults` — no results
- `TestSearXNGSearchProvider_Search_CountCapping` — respects count param
- `TestSearXNGSearchProvider_Search_NetworkError` — connection refused

**`pkg/config/config_test.go`** — config roundtrip:
- `TestLoadConfig_SearXNGDefaults` — defaults applied
- `TestLoadConfig_SearXNGEnabled` — custom values parse correctly

## SearXNG Instance Setup Note

The user's instance at `https://search.mapachoshome.com/` must have JSON format enabled. In `settings.yml`:

```yaml
search:
  formats:
    - html
    - json
```

Without this, the API returns 403 Forbidden. The provider will return a descriptive error message guiding the user.

## Files to Modify

| File | Change |
|---|---|
| `pkg/tools/web.go` | Add `SearXNGSearchProvider`, update options + priority |
| `pkg/config/config.go` | Add `SearXNGConfig`, update `WebToolsConfig` |
| `pkg/config/document_types.go` | Add `EditableSearXNGConfig`, update `EditableWebToolsConfig` |
| `pkg/config/document_parse_tools.go` | Parse `searxng` section |
| `pkg/config/document_convert.go` | Bidirectional conversion |
| `pkg/config/document_defaults.go` | Default values |
| `pkg/config/document_helpers.go` | Merge overlay (if needed) |
| `pkg/agent/tool_coordinator.go` | Pass SearXNG config to options |
| `config/config.example.json` | Add searxng section |
| `pkg/tools/web_test.go` | SearXNG provider tests |
| `pkg/config/config_test.go` | Config parsing tests |

## Estimated Effort

~200 lines of new code across 7 files. Low complexity — follows existing patterns exactly. No new dependencies (stdlib `net/http` + `encoding/json` only).

## Future Considerations

- **Multiple SearXNG instances:** failover if primary is down (not in v1)
- **Category presets:** allow `categories` per query type (e.g. `news` for time-sensitive queries)
- **Engine selection:** SearXNG supports `engines=google,duckduckgo,...` parameter — could be a config option
- **Instance health check:** periodic ping to verify instance availability
