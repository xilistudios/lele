package catalog

import (
	"context"
	"net/http"
	"os"
	"time"
)

type PrefetchOptions struct {
	URL        string
	TTL        time.Duration
	HTTPClient *http.Client
	CachePath  string
	Providers  []string
}

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

func IsPrefetchFresh(ttl time.Duration) bool {
	return IsCacheFileFresh(IndexPath(), ttl)
}

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

func LoadDiskCache(_ string) error {
	return LoadIndexFromCache()
}

const DefaultPrefetchTTL = 24 * time.Hour

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
