package catalog

import (
	"context"
	"net/http"
	"os"
	"time"
)

// PrefetchOptions controls a catalog refresh from GitHub.
// Kept for API compatibility with callers that previously used models.dev.
type PrefetchOptions struct {
	// URL overrides the catalog base URL (tests). Maps to Options.BaseURL.
	URL string
	// TTL defaults to DefaultPrefetchTTL when zero.
	TTL time.Duration
	// HTTPClient defaults to a 30s client.
	HTTPClient *http.Client
	// CachePath is unused (cache layout is fixed); retained for callers.
	CachePath string
	// Providers limits which provider files to download (empty = all).
	Providers []string
}

// Prefetch loads disk cache if fresh enough, otherwise downloads from GitHub.
func Prefetch(ctx context.Context, opts PrefetchOptions) error {
	o := Options{
		BaseURL:    opts.URL,
		HTTPClient: opts.HTTPClient,
		Providers:  opts.Providers,
	}

	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultPrefetchTTL
	}
	if HasCachedIndex() && IsCacheFileFresh(IndexPath(), ttl) {
		if err := LoadIndexFromCache(); err == nil {
			return nil
		}
	}

	return Refresh(ctx, o)
}

// IsPrefetchFresh reports whether the default catalog index cache is newer than ttl.
func IsPrefetchFresh(ttl time.Duration) bool {
	return IsCacheFileFresh(IndexPath(), ttl)
}

// IsCacheFileFresh reports whether the file at path is newer than ttl.
func IsCacheFileFresh(path string, ttl time.Duration) bool {
	if path == "" {
		path = IndexPath()
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

// LoadDiskCache loads the catalog index (and is a no-op if missing).
// path is unused; retained for callers that passed a models.dev cache path.
func LoadDiskCache(_ string) error {
	return LoadIndexFromCache()
}

// DefaultPrefetchTTL is how long a disk cache is considered fresh.
const DefaultPrefetchTTL = 24 * time.Hour

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
