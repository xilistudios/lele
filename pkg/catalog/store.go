package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Index struct {
	Version   int                   `json:"version"`
	UpdatedAt string                `json:"updated_at,omitempty"`
	Source    string                `json:"source,omitempty"`
	Providers map[string]IndexEntry `json:"providers"`
}

type IndexEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	APIBase    string `json:"api_base,omitempty"`
	File       string `json:"file,omitempty"`
	ModelCount int    `json:"model_count,omitempty"`
}

var (
	indexMu          sync.RWMutex
	memIndex         *Index
	loadOnce         sync.Once
	cacheDirOverride string
)

func SetCacheDir(dir string) {
	indexMu.Lock()
	cacheDirOverride = dir
	indexMu.Unlock()
}

func ResetCacheDir() {
	indexMu.Lock()
	cacheDirOverride = ""
	indexMu.Unlock()
}

func CacheDir() string {
	indexMu.RLock()
	if cacheDirOverride != "" {
		d := cacheDirOverride
		indexMu.RUnlock()
		return d
	}
	indexMu.RUnlock()

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "lele-cache", "catalog")
	}
	return filepath.Join(home, ".lele", "cache", "catalog")
}

func IndexPath() string {
	return filepath.Join(CacheDir(), "index.json")
}

func ProviderCachePath(provider string) string {
	key := normalizeType(provider)
	return filepath.Join(CacheDir(), "providers", key+".json")
}

func LoadIndexFromCache() error {
	data, err := os.ReadFile(IndexPath())
	if err != nil {
		return err
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return err
	}
	if idx.Providers == nil {
		idx.Providers = map[string]IndexEntry{}
	}
	norm := make(map[string]IndexEntry, len(idx.Providers))
	for k, e := range idx.Providers {
		id := normalizeType(k)
		e.ID = id
		if e.File == "" {
			e.File = "providers/" + id + ".json"
		}
		norm[id] = e
	}
	idx.Providers = norm

	indexMu.Lock()
	memIndex = &idx
	indexMu.Unlock()
	return nil
}

func loadIndex() *Index {
	indexMu.RLock()
	defer indexMu.RUnlock()
	return memIndex
}

func setIndex(idx *Index) {
	indexMu.Lock()
	memIndex = idx
	indexMu.Unlock()
}

func IndexSnapshot() *Index {
	return loadIndex()
}

func RestoreIndex(idx *Index) {
	setIndex(idx)
}

func loadProviderFromDisk(key string) *Provider {
	path := ProviderCachePath(key)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var p Provider
	if err := json.Unmarshal(data, &p); err != nil {
		return nil
	}
	p.ID = normalizeType(p.ID)
	if p.ID == "" {
		p.ID = key
	}
	return &p
}

func writeProviderCache(p Provider) error {
	key := normalizeType(p.ID)
	p.ID = key
	return writeJSONAtomic(ProviderCachePath(key), p)
}

func writeIndexCache(idx Index) error {
	return writeJSONAtomic(IndexPath(), idx)
}

func HasCachedProvider(provider string) bool {
	key := normalizeType(provider)
	_, err := os.Stat(ProviderCachePath(key))
	return err == nil
}

func HasCachedIndex() bool {
	_, err := os.Stat(IndexPath())
	return err == nil
}

func ApplyLive(providers map[string]Provider) {
	live := make(map[string]Provider, len(providers))
	for k, p := range providers {
		id := normalizeType(k)
		p.ID = id
		live[id] = p
		_ = writeProviderCache(p)
	}
	mu.Lock()
	liveProviders = live
	mu.Unlock()
}

func ResetLive() {
	mu.Lock()
	liveProviders = map[string]Provider{}
	mu.Unlock()
	indexMu.Lock()
	memIndex = nil
	loadOnce = sync.Once{}
	indexMu.Unlock()
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
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func SeedProvider(p Provider) error {
	key := normalizeType(p.ID)
	p.ID = key
	if err := writeProviderCache(p); err != nil {
		return err
	}
	mu.Lock()
	liveProviders[key] = p
	mu.Unlock()
	return nil
}
