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

// This file holds the static-asset delivery policy used by spaHandler:
// the Cache-Control allow-list, the ETag validators and the small pure
// helpers they are built from.

const (
	// cacheControlImmutable is sent for content-hashed build artifacts. Their
	// name changes whenever their bytes change, so the browser (and any shared
	// cache) may keep them for a year without revalidating.
	cacheControlImmutable = "public, max-age=31536000, immutable"

	// cacheControlNoCache is sent for names that are stable across builds
	// (index.html, the service worker, manifests, favicons...). "no-cache"
	// means "store, but always revalidate": the browser sends If-None-Match and
	// normally gets a 304 instead of the whole file, so a new build is picked
	// up on the very next load.
	cacheControlNoCache = "no-cache"
)

var (
	// inline.<hex>.js — the Vite/rolldown inline entry chunk.
	reInlineChunk = regexp.MustCompile(`^inline\.[0-9a-f]+\.js$`)
	// index_<hex>.<hex>.js|css — the entry chunk and its stylesheet.
	reIndexChunk = regexp.MustCompile(`^index_[0-9a-f]+\.[0-9a-f]+\.(?:js|css)$`)
	// A content-hash segment right before the extension, introduced by "." (the
	// <name>.<hash>.<ext> style of rolldown/farm) or by "-" (the Vite
	// <name>-<hash>.<ext> style). At least 8 hex digits are required: build
	// tools emit 8+ characters, while the short numeric suffixes that show up in
	// hand-written asset names (apple-touch-icon.180.png, icon.512.png,
	// font.f2.woff2) are 1-3 characters and must not be mistaken for a hash.
	reHashedName = regexp.MustCompile(`[.-][0-9a-f]{8,}\.[A-Za-z0-9]+$`)
)

// isImmutableAssetPath reports whether name is a content-hashed build artifact
// and may therefore be cached "forever".
//
// The rule set is a heuristic, deliberately narrowed: it accepts the exact
// shapes the bundlers produce (inline.<hex>.js, index_<hex>.<hex>.js|css and,
// under an assets/ directory, a "." or "-" separated run of 8+ lowercase hex
// digits before the extension) and nothing else. A false positive would pin a
// mutable file (sw.js, manifest.webmanifest, a favicon or a human-named asset
// such as apple-touch-icon.180.png) in the browser cache for a year with no
// revalidation, which is much worse than a false negative (a hashed file that
// merely revalidates, or a file whose hash run is shorter than the minimum).
// The residual risk is a mutable name ending in an 8+ digit number
// (photo-20240115.jpg): it would be treated as hashed.
//
// name may be a URL path ("/x/y.js") or a bare FS name ("x/y.js").
//
// Immutable when:
//   - the basename matches inline.<hex>.js; or
//   - the basename matches index_<hex>.<hex>.js / index_<hex>.<hex>.css; or
//   - the path lives under an "assets/" directory and the basename carries a
//     content-hash run of 8 or more lowercase hex digits right before its
//     extension, separated from the rest by "." or "-" (aabbccdd.js,
//     index-3f2a9b7c.js).
//
// <hex> is lowercase [0-9a-f]+ (what Vite/rolldown emits) and is never empty.
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

// hasAssetsSegment reports whether any directory component of name is "assets".
func hasAssetsSegment(name string) bool {
	for _, dir := range strings.Split(path.Dir(name), "/") {
		if dir == "assets" {
			return true
		}
	}
	return false
}

// cacheControlForPath returns the Cache-Control value to send for name.
func cacheControlForPath(name string) string {
	if isImmutableAssetPath(name) {
		return cacheControlImmutable
	}
	return cacheControlNoCache
}

// strongETag builds a strong entity tag from the size and the FNV-64a hash of
// the served bytes, e.g. `"22deff-9f8f0bb0e2e2c4f5"`.
//
// Why FNV-64a and not a cryptographic digest: the ETag is only ever compared
// against the previous representation of the same path, and a mismatch of both
// the length and 64 bits of hash is not a realistic collision. FNV-64a is
// cheap and deterministic, so the tag is stable across processes and restarts
// for a given content (the bundle is compiled into the binary via embed).
// No W/ prefix: the tag describes the exact bytes we serve, so it is strong.
func strongETag(data []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(data)
	return fmt.Sprintf(`"%x-%x"`, len(data), h.Sum64())
}

// isNotModified reports whether r's If-None-Match header matches etag and the
// client may therefore get a 304. Comparison is the weak one required by
// RFC 9110 §13.1.2 ("W/" prefixes are ignored) and only applies to GET/HEAD.
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

// weakETagMatch compares two entity tags ignoring their weakness prefix.
func weakETagMatch(a, b string) bool {
	return strings.TrimPrefix(a, "W/") == strings.TrimPrefix(b, "W/")
}

// serveStaticFile streams one embedded asset together with its cache
// validators (ETag + Cache-Control). f is consumed and closed here. info is
// f's already-fetched stat, used to key the validator memo.
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
		// Validators and Cache-Control stay on the 304; the body is empty.
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// ServeContent adds Content-Type, Content-Length, Accept-Ranges and range
	// support. The zero modtime mirrors the embedded FS, whose ModTime() is the
	// zero time, so no (invalid) Last-Modified is emitted.
	http.ServeContent(w, r, path.Base(name), time.Time{}, f)
}

// etagKey identifies a memoised validator. The file's size and modification
// time are part of the key, not just its path: RegisterWebUI accepts any
// http.FileSystem, so the handler can sit in front of http.Dir, where a file
// may be replaced between two requests. Keying on the path alone would then
// keep answering with the validator of the *previous* bytes and a client that
// revalidates would be told "not modified" for content it never received.
//
// With the embedded FS the extra fields are constant (zero time), so keys
// behave exactly like the plain path did.
type etagKey struct {
	name    string
	size    int64
	modTime time.Time
}

// etagKeyFor builds the memo key of the file described by info.
func etagKeyFor(name string, info fs.FileInfo) etagKey {
	return etagKey{name: name, size: info.Size(), modTime: info.ModTime()}
}

// etagEntry is the single-flight cell for one etagKey. etag/err are written
// once, inside once.Do, and read only after it returns (sync.Once establishes
// the happens-before edge for every caller).
type etagEntry struct {
	once sync.Once
	etag string
	err  error
}

// defaultETagMemoCap bounds the validator memo. The embedded bundle is a few
// hundred files, so the cap is never reached in production; it exists because
// RegisterWebUI is exported and an http.Dir root could enumerate a much larger
// tree.
const defaultETagMemoCap = 1024

// etagForPath returns the strong ETag for the file described by key,
// computing it from the file contents on the first request for that key and
// memoising it afterwards.
//
// f is the caller's already-open handle. It is read only by the goroutine that
// wins the single-flight race (the first request for a cold path) and is rewound
// afterwards so the winner can stream the same handle from the start; every
// other caller reuses the memoised tag without touching its own handle.
//
// The single-flight matters: opening the WebUI fires a burst of parallel
// requests, and without it N concurrent cold requests for the same path would
// each io.ReadAll the whole asset (hundreds of KB) just to hash 8 bytes.
//
// The memo is bounded by defaultETagMemoCap entries; when it is full the map is
// dropped wholesale. Recomputing an evicted validator costs one read, so a
// blunt reset is preferred over LRU bookkeeping. A dropped entry can be
// recomputed twice concurrently (the new cell races the old one), which is
// harmless: both produce the same tag for the same key.
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
		// A failed read must not be memoised, or the path would answer 500
		// forever: drop the cell so the next request retries.
		h.etagMu.Lock()
		if h.etags[key] == e {
			delete(h.etags, key)
		}
		h.etagMu.Unlock()
		return "", e.err
	}

	return e.etag, nil
}

// etagEntryFor returns the memo cell for key, creating it (and enforcing the
// cap) on the first request for that key.
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

// serveIndex writes the SPA fallback body (the memoised index.html) for
// client-side routes, with the same validators as any other static asset.
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
