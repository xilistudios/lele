package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

const DefaultRemoteBase = "https://raw.githubusercontent.com/xilistudios/lele/main/catalog"

func RemoteBase() string {
	return remoteBaseFromEnv()
}

type downloadState struct {
	mu        sync.Mutex
	running   bool
	lastError error
	lastAt    time.Time
}

var (
	download = &downloadState{}

	ensureOnce       sync.Once
	providerEnsureMu sync.Mutex
	providerInflight = map[string]bool{}
)

func LastDownload() (time.Time, error) {
	download.mu.Lock()
	defer download.mu.Unlock()
	return download.lastAt, download.lastError
}

func IsDownloading() bool {
	download.mu.Lock()
	defer download.mu.Unlock()
	return download.running
}

type Options struct {
	BaseURL    string
	HTTPClient *http.Client
	Providers  []string
}

func (o Options) withDefaults() Options {
	if o.BaseURL == "" {
		o.BaseURL = RemoteBase()
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	return o
}

func Ensure() {
	if offlineMode() {
		_ = LoadIndexFromCache()
		return
	}
	loadOnce.Do(func() {
		if err := LoadIndexFromCache(); err == nil {
			return
		}
		EnsureAsync()
	})
}

func EnsureAsync() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		_ = Refresh(ctx, Options{})
	}()
}

func EnsureProviderAsync(provider string) {
	key := normalizeType(provider)
	if key == "" || HasCachedProvider(key) {
		return
	}
	providerEnsureMu.Lock()
	if providerInflight[key] {
		providerEnsureMu.Unlock()
		return
	}
	providerInflight[key] = true
	providerEnsureMu.Unlock()

	go func() {
		defer func() {
			providerEnsureMu.Lock()
			delete(providerInflight, key)
			providerEnsureMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = FetchProvider(ctx, key, Options{})
	}()
}

func Refresh(ctx context.Context, opts Options) error {
	opts = opts.withDefaults()

	download.mu.Lock()
	if download.running {
		download.mu.Unlock()
		return fmt.Errorf("catalog refresh already running")
	}
	download.running = true
	download.mu.Unlock()

	defer func() {
		download.mu.Lock()
		download.running = false
		download.mu.Unlock()
	}()

	idx, err := fetchIndex(ctx, opts)
	if err != nil {
		download.mu.Lock()
		download.lastError = err
		download.mu.Unlock()
		return err
	}

	if err := writeIndexCache(*idx); err != nil {
		_ = err
	}
	setIndex(idx)

	ids := opts.Providers
	if len(ids) == 0 {
		ids = make([]string, 0, len(idx.Providers))
		for id := range idx.Providers {
			ids = append(ids, id)
		}
	}

	live := make(map[string]Provider, len(ids))
	var firstErr error
	fetched := 0
	for _, id := range ids {
		key := normalizeType(id)
		p, err := fetchProviderFile(ctx, opts, key)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if cached := loadProviderFromDisk(key); cached != nil {
				live[key] = *cached
			}
			continue
		}
		if err := writeProviderCache(*p); err == nil {
			fetched++
		}
		live[key] = *p
	}

	mu.Lock()
	liveProviders = live
	mu.Unlock()

	download.mu.Lock()
	download.lastAt = time.Now()
	download.lastError = firstErr
	download.mu.Unlock()

	if firstErr != nil && fetched == 0 {
		return firstErr
	}
	return nil
}

func FetchProvider(ctx context.Context, provider string, opts Options) error {
	opts = opts.withDefaults()
	key := normalizeType(provider)
	if key == "" {
		return fmt.Errorf("empty provider")
	}

	if loadIndex() == nil {
		if idx, err := fetchIndex(ctx, opts); err == nil {
			_ = writeIndexCache(*idx)
			setIndex(idx)
		}
	}

	p, err := fetchProviderFile(ctx, opts, key)
	if err != nil {
		return err
	}
	if err := writeProviderCache(*p); err != nil {
		return err
	}

	mu.Lock()
	liveProviders[key] = *p
	mu.Unlock()
	return nil
}

func fetchIndex(ctx context.Context, opts Options) (*Index, error) {
	url := opts.BaseURL + "/index.json"
	data, err := httpGet(ctx, opts.HTTPClient, url)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog index: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse catalog index: %w", err)
	}
	if idx.Providers == nil {
		return nil, fmt.Errorf("catalog index has no providers")
	}
	norm := make(map[string]IndexEntry, len(idx.Providers))
	for k, e := range idx.Providers {
		id := normalizeType(k)
		e.ID = id
		if e.File == "" {
			e.File = path.Join("providers", id+".json")
		}
		norm[id] = e
	}
	idx.Providers = norm
	return &idx, nil
}

func fetchProviderFile(ctx context.Context, opts Options, key string) (*Provider, error) {
	file := "providers/" + key + ".json"
	if idx := loadIndex(); idx != nil {
		if e, ok := idx.Providers[key]; ok && e.File != "" {
			file = e.File
		}
	}
	file = strings.TrimPrefix(file, "/")
	if strings.Contains(file, "..") {
		return nil, fmt.Errorf("invalid provider file path %q", file)
	}

	url := opts.BaseURL + "/" + file
	data, err := httpGet(ctx, opts.HTTPClient, url)
	if err != nil {
		return nil, fmt.Errorf("fetch provider %s: %w", key, err)
	}
	var p Provider
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse provider %s: %w", key, err)
	}
	p.ID = normalizeType(p.ID)
	if p.ID == "" {
		p.ID = key
	}
	return &p, nil
}

func httpGet(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "lele-catalog/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func remoteBaseFromEnv() string {
	return envOr("LELE_CATALOG_BASE_URL", DefaultRemoteBase)
}
