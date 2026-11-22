package server

import (
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	cacheControlImmutable = "public, max-age=31536000, immutable"

	cacheControlNoCache = "no-cache"
)

var (
	reInlineChunk = regexp.MustCompile(`^inline\.[0-9a-f]+\.js$`)
	reIndexChunk  = regexp.MustCompile(`^index_[0-9a-f]+\.[0-9a-f]+\.(?:js|css)$`)
	reHashedName  = regexp.MustCompile(`[.-][0-9a-f]{8,}\.[A-Za-z0-9]+$`)
)

func isImmutableAssetPath(name string) bool {
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return false
	}

	base := path.Base(name)
	if reInlineChunk.MatchString(base) || reIndexChunk.MatchString(base) {
		return true
	}

	return hasAssetsSegment(name) && reHashedName.MatchString(base)
}

func hasAssetsSegment(name string) bool {
	for _, dir := range strings.Split(path.Dir(name), "/") {
		if dir == "assets" {
			return true
		}
	}
	return false
}

func cacheControlForPath(name string) string {
	if isImmutableAssetPath(name) {
		return cacheControlImmutable
	}
	return cacheControlNoCache
}

func strongETag(data []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(data)
	return fmt.Sprintf(`"%x-%x"`, len(data), h.Sum64())
}

func isNotModified(r *http.Request, etag string) bool {
	if etag == "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}

	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || weakETagMatch(candidate, etag) {
			return true
		}
	}
	return false
}

func weakETagMatch(a, b string) bool {
	return strings.TrimPrefix(a, "W/") == strings.TrimPrefix(b, "W/")
}

func (h *spaHandler) serveStaticFile(w http.ResponseWriter, r *http.Request, name string, f http.File, info fs.FileInfo) {
	defer f.Close()

	etag, err := h.etagForPath(etagKeyFor(name, info), f)
	if err != nil {
		http.Error(w, "failed to read embedded asset", http.StatusInternalServerError)
		return
	}

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", cacheControlForPath(name))

	if isNotModified(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	http.ServeContent(w, r, path.Base(name), time.Time{}, f)
}

type etagKey struct {
	name    string
	size    int64
	modTime time.Time
}

func etagKeyFor(name string, info fs.FileInfo) etagKey {
	return etagKey{name: name, size: info.Size(), modTime: info.ModTime()}
}

type etagEntry struct {
	once sync.Once
	etag string
	err  error
}

const defaultETagMemoCap = 1024

func (h *spaHandler) etagForPath(key etagKey, f io.ReadSeeker) (string, error) {
	e := h.etagEntryFor(key)

	e.once.Do(func() {
		data, err := io.ReadAll(f)
		if err != nil {
			e.err = err
			return
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			e.err = err
			return
		}
		e.etag = strongETag(data)
	})

	if e.err != nil {
		h.etagMu.Lock()
		if h.etags[key] == e {
			delete(h.etags, key)
		}
		h.etagMu.Unlock()
		return "", e.err
	}

	return e.etag, nil
}

func (h *spaHandler) etagEntryFor(key etagKey) *etagEntry {
	h.etagMu.RLock()
	e, ok := h.etags[key]
	h.etagMu.RUnlock()
	if ok {
		return e
	}

	h.etagMu.Lock()
	defer h.etagMu.Unlock()

	if e, ok := h.etags[key]; ok {
		return e
	}
	if len(h.etags) >= h.etagCap {
		h.etags = make(map[etagKey]*etagEntry)
	}

	e = &etagEntry{}
	h.etags[key] = e
	return e
}

func (h *spaHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("ETag", h.indexETag)
	w.Header().Set("Cache-Control", cacheControlNoCache)

	if isNotModified(r, h.indexETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(h.index)
}
