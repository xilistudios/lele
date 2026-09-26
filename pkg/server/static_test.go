package server

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

// --- fixtures ---

// testIndexHTML is deliberately larger than 4 KiB so a short read (a single
// File.Read that returns fewer bytes than requested) shows up as a truncated or
// zero-padded body.
const testIndexHTMLSize = 8 * 1024

func testIndexHTML() []byte {
	const head = "<!doctype html><html><head><title>lele</title></head><body><div id=app></div>"
	const tail = "</body></html>"

	var b strings.Builder
	b.WriteString(head)
	for b.Len() < testIndexHTMLSize {
		b.WriteString("<!-- padding row to exceed the 4 KiB short-read threshold -->")
	}
	b.WriteString(tail)
	return []byte(b.String())
}

// spaTestFS is a MapFS shaped like web/dist: a big index.html, content-hashed
// chunks and a handful of names that must stay revalidated.
func spaTestFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":             &fstest.MapFile{Data: testIndexHTML()},
		"index_abc.12345678.js":  &fstest.MapFile{Data: []byte("console.log('entry-hashed')")},
		"index_def.87654321.css": &fstest.MapFile{Data: []byte("body{color:red}")},
		"inline.abcdef12.js":     &fstest.MapFile{Data: []byte("export default 1")},
		"assets/nested.0a1b2c3d.js": &fstest.MapFile{
			Data: []byte("export const nested = true"),
		},
		"assets/index-3f2a9b7c.js": &fstest.MapFile{Data: []byte("export const viteEntry = true")},
		"assets/plain.js":          &fstest.MapFile{Data: []byte("export const plain = true")},
		// Human-named assets whose short numeric suffix must not be mistaken
		// for a content hash.
		"assets/apple-touch-icon.180.png": &fstest.MapFile{Data: []byte{0x89, 'P', 'N', 'G'}},
		"assets/font.f2.woff2":            &fstest.MapFile{Data: []byte("wOF2")},
		"assets/icon.512.png":             &fstest.MapFile{Data: []byte{0x89, 'P', 'N', 'G'}},
		"theme-init.js":                   &fstest.MapFile{Data: []byte("document.documentElement.dataset.t='dark'")},
		"sw.js":                           &fstest.MapFile{Data: []byte("self.addEventListener('fetch',()=>{})")},
		"manifest.webmanifest":            &fstest.MapFile{Data: []byte(`{"name":"lele"}`)},
		"favicon.svg":                     &fstest.MapFile{Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")},
		"robots.txt":                      &fstest.MapFile{Data: []byte("User-agent: *")},
	}
}

func spaTestHandler(t *testing.T) *spaHandler {
	t.Helper()
	return newSPAHandler(http.FS(spaTestFS()))
}

func spaRequest(t *testing.T, h http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// --- isImmutableAssetPath ---

func TestIsImmutableAssetPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"entry chunk js", "index_2fd6.d55d2951.js", true},
		{"entry chunk css", "index_c1c3.7844a5e1.css", true},
		{"entry chunk with leading slash", "/index_abc.12345678.js", true},
		{"inline chunk", "inline.3697dbf9.js", true},
		{"inline chunk nested", "js/inline.3697dbf9.js", true},
		{"hashed asset under assets/", "assets/logo.aabbccdd.svg", true},
		{"hashed asset in assets/ subdir", "assets/icons/leaf.0a1b2c3d.png", true},
		{"hashed asset with leading slash", "/assets/logo.aabbccdd.svg", true},
		{"vite dash-form asset", "assets/index-3f2a9b7c.js", true},
		{"vite dash-form stylesheet", "assets/vendor-3f2a9b7c.css", true},
		{"long hash", "assets/main.9f8f0bb0e2e2c4f5.js", true},

		{"index.html", "index.html", false},
		{"root", "/", false},
		{"empty", "", false},
		{"theme-init.js", "theme-init.js", false},
		{"sw.js", "sw.js", false},
		{"manifest.webmanifest", "manifest.webmanifest", false},
		{"favicon.svg", "favicon.svg", false},
		{"robots.txt", "robots.txt", false},
		{"plain asset under assets/", "assets/plain.js", false},
		{"hashed-looking file outside assets/", "main.d55d2951.js", false},
		{"vite dash-form outside assets/", "main-3f2a9b7c.js", false},
		{"index chunk without hash segment", "index_abc.js", false},
		{"inline chunk wrong extension", "inline.3697dbf9.css", false},
		{"inline chunk without hash", "inline.js", false},
		{"entry chunk other extension", "index_abc.12345678.mjs", false},
		{"entry chunk non-hex hash", "index_abc.zzzzzzzz.js", false},
		{"uppercase hex is not the produced format", "index_ABC.12345678.js", false},
		{"directory named assets only", "assets/", false},

		// Short numeric suffixes are not hashes (the false positive this rule
		// was narrowed for): pinning these for a year would be the bug.
		{"apple touch icon size suffix", "assets/apple-touch-icon.180.png", false},
		{"font weight suffix", "assets/font.f2.woff2", false},
		{"icon size suffix", "assets/icon.512.png", false},
		{"six-digit hex run is below the 8-char minimum", "assets/icons/leaf.0a1b2c.d.png", false},
		{"seven-digit dash run is below the minimum", "assets/chunk-3f2a9b7.js", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isImmutableAssetPath(tt.path); got != tt.want {
				t.Errorf("isImmutableAssetPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// --- cache headers ---

func TestSPAHandlerHashedAssetIsImmutable(t *testing.T) {
	h := spaTestHandler(t)
	rec := spaRequest(t, h, http.MethodGet, "/index_abc.12345678.js", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != cacheControlImmutable {
		t.Errorf("Cache-Control = %q, want %q", got, cacheControlImmutable)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag is empty")
	}
	if strings.HasPrefix(etag, "W/") {
		t.Errorf("ETag = %q, want a strong validator", etag)
	}
	if etag != strongETag([]byte("console.log('entry-hashed')")) {
		t.Errorf("ETag = %q, not derived from the served bytes", etag)
	}
	if body := rec.Body.String(); body != "console.log('entry-hashed')" {
		t.Errorf("body = %q, want the fixture content", body)
	}
}

func TestSPAHandlerConditionalHashedAssetRequest(t *testing.T) {
	h := spaTestHandler(t)

	first := spaRequest(t, h, http.MethodGet, "/index_abc.12345678.js", nil)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first response has no ETag")
	}

	second := spaRequest(t, h, http.MethodGet, "/index_abc.12345678.js", map[string]string{
		"If-None-Match": etag,
	})

	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", second.Code)
	}
	if body := second.Body.String(); body != "" {
		t.Errorf("304 body = %q, want empty", body)
	}
	if got := second.Header().Get("ETag"); got != etag {
		t.Errorf("304 ETag = %q, want %q", got, etag)
	}
	if got := second.Header().Get("Cache-Control"); got != cacheControlImmutable {
		t.Errorf("304 Cache-Control = %q, want %q", got, cacheControlImmutable)
	}

	// A stale validator must still get the full body.
	stale := spaRequest(t, h, http.MethodGet, "/index_abc.12345678.js", map[string]string{
		"If-None-Match": `"0-deadbeef"`,
	})
	if stale.Code != http.StatusOK {
		t.Fatalf("stale If-None-Match status = %d, want 200", stale.Code)
	}
	if stale.Body.String() == "" {
		t.Error("stale If-None-Match returned an empty body")
	}
}

func TestSPAHandlerWildcardIfNoneMatch(t *testing.T) {
	h := spaTestHandler(t)
	rec := spaRequest(t, h, http.MethodGet, "/index_abc.12345678.js", map[string]string{
		"If-None-Match": "*",
	})
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 body = %q, want empty", rec.Body.String())
	}
}

func TestSPAHandlerDoesNotCacheNonHashedNames(t *testing.T) {
	tests := []struct {
		path string
	}{
		{"/theme-init.js"},
		{"/manifest.webmanifest"},
		{"/sw.js"},
		{"/favicon.svg"},
		{"/robots.txt"},
		{"/assets/plain.js"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := spaRequest(t, spaTestHandler(t), http.MethodGet, tt.path, nil)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != cacheControlNoCache {
				t.Errorf("Cache-Control = %q, want %q", got, cacheControlNoCache)
			}
			if rec.Header().Get("ETag") == "" {
				t.Error("ETag is empty, want a validator to revalidate with")
			}
		})
	}
}

// TestCacheControlForPath pins the allow-list decision for names that come
// from the real bundle: a numeric suffix such as .180 / .512 / .f2 must never
// be read as a content hash, because those files are mutable and would then be
// pinned in the browser cache for a year without revalidation.
func TestCacheControlForPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"assets/apple-touch-icon.180.png", cacheControlNoCache},
		{"assets/font.f2.woff2", cacheControlNoCache},
		{"assets/icon.512.png", cacheControlNoCache},
		{"assets/index-3f2a9b7c.js", cacheControlImmutable},
		{"index_c1c3.7844a5e1.css", cacheControlImmutable},
		{"index_c1c3.7844a5e1.js", cacheControlImmutable},
		{"sw.js", cacheControlNoCache},
		{"manifest.webmanifest", cacheControlNoCache},
		{"theme-init.js", cacheControlNoCache},
		{"favicon.svg", cacheControlNoCache},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := cacheControlForPath(tt.path); got != tt.want {
				t.Errorf("cacheControlForPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// TestSPAHandlerCacheControlForRealBundleNames is the same decision observed
// end to end, through the handler that actually sets the header.
func TestSPAHandlerCacheControlForRealBundleNames(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/assets/apple-touch-icon.180.png", cacheControlNoCache},
		{"/assets/font.f2.woff2", cacheControlNoCache},
		{"/assets/icon.512.png", cacheControlNoCache},
		{"/assets/index-3f2a9b7c.js", cacheControlImmutable},
		{"/index_abc.12345678.js", cacheControlImmutable},
		{"/index_def.87654321.css", cacheControlImmutable},
		{"/sw.js", cacheControlNoCache},
		{"/manifest.webmanifest", cacheControlNoCache},
		{"/theme-init.js", cacheControlNoCache},
		{"/favicon.svg", cacheControlNoCache},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := spaRequest(t, spaTestHandler(t), http.MethodGet, tt.path, nil)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.want {
				t.Errorf("Cache-Control = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSPAHandlerIndexRouteIsRevalidated(t *testing.T) {
	h := spaTestHandler(t)
	want := testIndexHTML()

	rec := spaRequest(t, h, http.MethodGet, "/", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != cacheControlNoCache {
		t.Errorf("Cache-Control = %q, want %q", got, cacheControlNoCache)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag is empty")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", got)
	}
	if rec.Body.Len() != len(want) {
		t.Fatalf("body length = %d, want %d", rec.Body.Len(), len(want))
	}
	if rec.Body.String() != string(want) {
		t.Error("body differs from the index.html fixture")
	}

	cond := spaRequest(t, h, http.MethodGet, "/", map[string]string{"If-None-Match": etag})
	if cond.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", cond.Code)
	}
	if cond.Body.Len() != 0 {
		t.Errorf("304 body = %q, want empty", cond.Body.String())
	}
	if cond.Header().Get("ETag") != etag || cond.Header().Get("Cache-Control") != cacheControlNoCache {
		t.Error("304 response is missing its validators")
	}
}

// The explicit /index.html request and the SPA fallback serve the same bytes,
// so they must advertise the same validator.
func TestSPAHandlerIndexETagIsStable(t *testing.T) {
	h := spaTestHandler(t)

	direct := spaRequest(t, h, http.MethodGet, "/index.html", nil)
	fallback := spaRequest(t, h, http.MethodGet, "/some/client/route", nil)

	if direct.Header().Get("ETag") == "" {
		t.Fatal("index.html has no ETag")
	}
	if direct.Header().Get("ETag") != fallback.Header().Get("ETag") {
		t.Errorf("ETag mismatch: /index.html = %q, /some/client/route = %q",
			direct.Header().Get("ETag"), fallback.Header().Get("ETag"))
	}
	if fallback.Header().Get("ETag") != strongETag(testIndexHTML()) {
		t.Error("index ETag is not derived from the index fixture bytes")
	}
}

func TestSPAHandlerClientRouteFallsBackToIndex(t *testing.T) {
	h := spaTestHandler(t)
	rec := spaRequest(t, h, http.MethodGet, "/some/client/route", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != cacheControlNoCache {
		t.Errorf("Cache-Control = %q, want %q", got, cacheControlNoCache)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("ETag is empty on the SPA fallback")
	}
	if rec.Body.String() != string(testIndexHTML()) {
		t.Error("fallback body is not index.html")
	}
}

// --- API/webhook routing must not fall back to the SPA ---

func TestSPAHandlerAPIPathsAreNotFound(t *testing.T) {
	h := spaTestHandler(t)

	for _, path := range []string{"/api/v1/anything", "/api/", "/webhook/custom"} {
		t.Run(path, func(t *testing.T) {
			rec := spaRequest(t, h, http.MethodGet, path, nil)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "<!doctype html>") {
				t.Error("API path fell back to index.html")
			}
		})
	}
}

// --- short-read regression ---

func TestSPAHandlerIndexIsNotTruncated(t *testing.T) {
	h := spaTestHandler(t)
	want := testIndexHTML()

	if len(want) <= 4*1024 {
		t.Fatalf("fixture is only %d bytes, must exceed 4 KiB to be meaningful", len(want))
	}

	rec := spaRequest(t, h, http.MethodGet, "/", nil)
	if rec.Body.Len() != len(want) {
		t.Fatalf("body length = %d, want %d", rec.Body.Len(), len(want))
	}

	h.once.Do(h.loadIndex)
	if len(h.index) != len(want) {
		t.Fatalf("memoised index length = %d, want %d", len(h.index), len(want))
	}
	if h.indexETag != strongETag(want) {
		t.Error("memoised index ETag does not match the full fixture bytes")
	}

	// The bytes past a 4 KiB single-read boundary must be intact, not zeros.
	if !strings.HasSuffix(string(h.index), "</html>") {
		t.Error("memoised index is missing its tail (truncated read)")
	}
}

// chunkedFS caps every Read at max bytes so each read returns far fewer bytes
// than requested — the exact condition that used to truncate index.html when it
// was loaded with a single Read call.
type chunkedFS struct {
	inner http.FileSystem
	max   int
}

func (c chunkedFS) Open(name string) (http.File, error) {
	f, err := c.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &chunkedFile{File: f, max: c.max}, nil
}

type chunkedFile struct {
	http.File
	max int
}

func (c *chunkedFile) Read(p []byte) (int, error) {
	if len(p) > c.max {
		p = p[:c.max]
	}
	return c.File.Read(p)
}

func TestSPAHandlerIndexSurvivesShortReads(t *testing.T) {
	want := testIndexHTML()
	h := newSPAHandler(chunkedFS{inner: http.FS(spaTestFS()), max: 1024})

	h.once.Do(h.loadIndex)
	if !h.loaded {
		t.Fatal("index.html was not loaded")
	}
	if len(h.index) != len(want) {
		t.Fatalf("index length = %d, want %d (short read not handled)", len(h.index), len(want))
	}
	if h.indexETag != strongETag(want) {
		t.Error("index ETag does not match the full file: content was read incompletely")
	}

	rec := spaRequest(t, h, http.MethodGet, "/", nil)
	if rec.Body.Len() != len(want) || rec.Body.String() != string(want) {
		t.Fatalf("served body length = %d, want %d", rec.Body.Len(), len(want))
	}
}

func TestSPAHandlerMissingIndexDoesNotServeGarbage(t *testing.T) {
	fsys := spaTestFS()
	delete(fsys, "index.html")
	h := newSPAHandler(http.FS(fsys))

	rec := spaRequest(t, h, http.MethodGet, "/some/client/route", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when index.html cannot be read", rec.Code)
	}

	// Real assets are still served.
	asset := spaRequest(t, h, http.MethodGet, "/index_abc.12345678.js", nil)
	if asset.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want 200", asset.Code)
	}
}

func TestSPAHandlerShortIndexIsStillServed(t *testing.T) {
	fsys := spaTestFS()
	fsys["index.html"] = &fstest.MapFile{Data: []byte("<!doctype html>")}
	h := newSPAHandler(http.FS(fsys))

	rec := spaRequest(t, h, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "<!doctype html>" {
		t.Fatalf("status = %d body = %q, want 200 and the fixture", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("ETag is empty for a tiny index.html")
	}
}

// --- ETag memoisation ---

// countingReadSeeker counts Read calls so memoisation can be observed directly.
type countingReadSeeker struct {
	r     *bytes.Reader
	reads int
}

func (c *countingReadSeeker) Read(p []byte) (int, error) {
	c.reads++
	return c.r.Read(p)
}

func (c *countingReadSeeker) Seek(offset int64, whence int) (int64, error) {
	return c.r.Seek(offset, whence)
}

func TestSPAHandlerETagForPathIsComputedOnce(t *testing.T) {
	h := newSPAHandler(http.FS(fstest.MapFS{}))
	data := []byte("console.log('hashed chunk')")
	key := etagKey{name: "/index_abc.12345678.js", size: int64(len(data))}

	first := &countingReadSeeker{r: bytes.NewReader(data)}
	etag, err := h.etagForPath(key, first)
	if err != nil {
		t.Fatalf("first etagForPath() error = %v", err)
	}
	if first.reads == 0 {
		t.Fatal("first call did not read the file")
	}
	if etag != strongETag(data) {
		t.Fatalf("etag = %q, want %q", etag, strongETag(data))
	}

	second := &countingReadSeeker{r: bytes.NewReader(data)}
	again, err := h.etagForPath(key, second)
	if err != nil {
		t.Fatalf("second etagForPath() error = %v", err)
	}
	if second.reads != 0 {
		t.Errorf("second call read the file %d times, want 0 (not memoised)", second.reads)
	}
	if again != etag {
		t.Errorf("etag = %q, want the memoised %q", again, etag)
	}
}

// The validator memo is keyed on (name, size, modTime), so a file that changed
// on disk must not keep serving the old tag: a client revalidating a stale
// entry would otherwise be answered 304 for bytes it never received.
func TestSPAHandlerETagMemoInvalidatedWhenFileChanges(t *testing.T) {
	fsys := spaTestFS()
	fsys["assets/chunk.01234567.js"] = &fstest.MapFile{
		Data:    []byte("aaaa"),
		ModTime: time.Unix(1700000000, 0),
	}
	h := newSPAHandler(http.FS(fsys))

	first := spaRequest(t, h, http.MethodGet, "/assets/chunk.01234567.js", nil)
	firstETag := first.Header().Get("ETag")
	if firstETag == "" {
		t.Fatal("first response has no ETag")
	}
	if first.Body.String() != "aaaa" {
		t.Fatalf("first body = %q, want %q", first.Body.String(), "aaaa")
	}

	// Same length, different bytes, newer mtime: the served bytes and the
	// validator must both move.
	fsys["assets/chunk.01234567.js"] = &fstest.MapFile{
		Data:    []byte("bbbb"),
		ModTime: time.Unix(1700000001, 0),
	}

	second := spaRequest(t, h, http.MethodGet, "/assets/chunk.01234567.js", nil)
	if second.Body.String() != "bbbb" {
		t.Fatalf("second body = %q, want %q", second.Body.String(), "bbbb")
	}
	if got := second.Header().Get("ETag"); got == firstETag {
		t.Errorf("ETag %q was reused after the file changed", got)
	}
	if got := second.Header().Get("ETag"); got != strongETag([]byte("bbbb")) {
		t.Errorf("ETag = %q, want the tag of the current bytes %q", got, strongETag([]byte("bbbb")))
	}

	// Revalidating with the stale tag must return the new body, not a 304.
	stale := spaRequest(t, h, http.MethodGet, "/assets/chunk.01234567.js", map[string]string{
		"If-None-Match": firstETag,
	})
	if stale.Code != http.StatusOK {
		t.Fatalf("stale If-None-Match status = %d, want 200", stale.Code)
	}
	if stale.Body.String() != "bbbb" {
		t.Errorf("stale revalidation body = %q, want %q", stale.Body.String(), "bbbb")
	}
}

// TestSPAHandlerETagMemoIsBounded covers the cap: RegisterWebUI accepts any
// http.FileSystem, so the memo must not grow with the number of distinct paths.
func TestSPAHandlerETagMemoIsBounded(t *testing.T) {
	h := spaTestHandler(t)
	h.etagCap = 2

	for _, p := range []string{
		"/index_abc.12345678.js",
		"/index_def.87654321.css",
		"/inline.abcdef12.js",
		"/theme-init.js",
		"/favicon.svg",
	} {
		rec := spaRequest(t, h, http.MethodGet, p, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", p, rec.Code)
		}
		if rec.Header().Get("ETag") == "" {
			t.Fatalf("GET %s has no ETag", p)
		}
	}

	h.etagMu.RLock()
	size := len(h.etags)
	h.etagMu.RUnlock()
	if size > h.etagCap {
		t.Errorf("memo holds %d entries, want <= %d (cap not enforced)", size, h.etagCap)
	}

	// An evicted path must still be served correctly, not 500.
	rec := spaRequest(t, h, http.MethodGet, "/index_abc.12345678.js", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "console.log('entry-hashed')" {
		t.Fatalf("re-request after eviction: status = %d body = %q", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("ETag"), strongETag([]byte("console.log('entry-hashed')")); got != want {
		t.Errorf("ETag after eviction = %q, want %q", got, want)
	}
}

// TestSPAHandlerETagForPathIsSingleFlight pins the cold-start behaviour: the
// first request for a path reads the asset to hash it, the concurrent ones must
// reuse that read instead of each io.ReadAll-ing hundreds of KB.
func TestSPAHandlerETagForPathIsSingleFlight(t *testing.T) {
	h := newSPAHandler(http.FS(fstest.MapFS{}))
	data := bytes.Repeat([]byte("console.log('chunk');"), 2048) // ~43 KB
	key := etagKey{name: "/assets/cold.0a1b2c3d.js", size: int64(len(data))}

	const callers = 16
	readers := make([]*countingReadSeeker, callers)
	var wg sync.WaitGroup
	errs := make(chan string, callers)

	wg.Add(callers)
	for i := 0; i < callers; i++ {
		readers[i] = &countingReadSeeker{r: bytes.NewReader(data)}
		go func(c *countingReadSeeker) {
			defer wg.Done()
			etag, err := h.etagForPath(key, c)
			if err != nil {
				errs <- fmt.Sprintf("etagForPath() error = %v", err)
				return
			}
			if etag != strongETag(data) {
				errs <- fmt.Sprintf("etag = %q, want %q", etag, strongETag(data))
			}
		}(readers[i])
	}
	wg.Wait()
	close(errs)

	for msg := range errs {
		t.Error(msg)
	}

	reading := 0
	for _, rd := range readers {
		if rd.reads > 0 {
			reading++
		}
	}
	if reading != 1 {
		t.Errorf("%d of %d callers read the file, want exactly 1 (single-flight)", reading, callers)
	}
}

// --- concurrency ---

func TestSPAHandlerETagMemoIsConcurrencySafe(t *testing.T) {
	h := spaTestHandler(t)
	paths := []string{
		"/index_abc.12345678.js",
		"/index_def.87654321.css",
		"/inline.abcdef12.js",
		"/assets/nested.0a1b2c3d.js",
		"/theme-init.js",
	}

	want := make(map[string]string, len(paths))
	for _, p := range paths {
		want[p] = spaRequest(t, newSPAHandler(http.FS(spaTestFS())), http.MethodGet, p, nil).
			Header().Get("ETag")
	}

	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, p := range paths {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
				if got := rec.Header().Get("ETag"); got != want[p] {
					errs <- fmt.Sprintf("ETag for %s = %q, want %q", p, got, want[p])
				}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for msg := range errs {
		t.Error(msg)
	}
}

// --- benchmark (no per-request FileServer / no per-request ETag recomputation) ---

func benchmarkSPAHandler(b *testing.B, path string) {
	h := newSPAHandler(http.FS(spaTestFS()))

	// Warm the memoised index + ETag so the measured path is the cached one.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}

func BenchmarkSPAHandlerStaticAsset(b *testing.B) {
	benchmarkSPAHandler(b, "/index_abc.12345678.js")
}

// BenchmarkSPAHandlerHarnessBaseline measures the harness itself (request +
// Recorder + a no-op handler) so the delta of the benchmark above is the
// handler's own cost: any per-request http.FileServer/ETag recomputation would
// show up as extra allocs/op there.
func BenchmarkSPAHandlerHarnessBaseline(b *testing.B) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/index_abc.12345678.js", nil)
		h.ServeHTTP(rec, req)
	}
}

func BenchmarkSPAHandlerIndexFallback(b *testing.B) {
	benchmarkSPAHandler(b, "/some/client/route")
}

func BenchmarkSPAHandlerConditional304(b *testing.B) {
	h := newSPAHandler(http.FS(spaTestFS()))

	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, httptest.NewRequest(http.MethodGet, "/index_abc.12345678.js", nil))
	etag := warm.Header().Get("ETag")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/index_abc.12345678.js", nil)
		req.Header.Set("If-None-Match", etag)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotModified {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}
