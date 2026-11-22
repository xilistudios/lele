package server

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func gzipRequest(h http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeGzip(t *testing.T, body []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer zr.Close()

	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("reading gzip body: %v (truncated or corrupt stream)", err)
	}
	return out
}

func decodeGzipPartial(t *testing.T, body []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer zr.Close()

	out, err := io.ReadAll(zr)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		t.Fatalf("decoding a partially delivered gzip stream: %v", err)
	}
	return out
}

func fixedResponseHandler(status int, contentType string, declared bool, body []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if declared {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}
		w.WriteHeader(status)
		if len(body) > 0 {
			_, _ = w.Write(body)
		}
	})
}

type hijackableRecorder struct {
	*httptest.ResponseRecorder
	conn     net.Conn
	hijacked bool
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return h.conn, nil, nil
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushRecorder) Flush() {
	f.flushes++
	f.ResponseRecorder.Flush()
}

type headerWriteRecorder struct {
	*httptest.ResponseRecorder
	headerWrites int
}

func (n *headerWriteRecorder) WriteHeader(code int) {
	n.headerWrites++
	n.ResponseRecorder.WriteHeader(code)
}

func hasVary(rec *httptest.ResponseRecorder, value string) bool {
	return strings.Contains(rec.Header().Get("Vary"), value)
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlineCalls int
	lastDeadline  time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadlineCalls++
	d.lastDeadline = t
	return nil
}

func TestCompressibleContentType(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"text/html; charset=utf-8", true},
		{"text/css; charset=utf-8", true},
		{"text/plain", true},
		{"text/javascript; charset=utf-8", true},
		{"application/javascript", true},
		{"text/javascript", true},
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"image/svg+xml", true},
		{"application/manifest+json", true},
		{"Image/SVG+XML", true},

		{"", false},
		{"text/event-stream", false},
		{"text/event-stream; charset=utf-8", false},
		{"application/octet-stream", false},
		{"application/wasm", false},
		{"image/png", false},
		{"image/jpeg", false},
		{"font/woff2", false},
		{"application/zip", false},
		{"application/gzip", false},
		{"video/mp4", false},
	}

	for _, tt := range tests {
		t.Run(tt.ct, func(t *testing.T) {
			if got := compressibleContentType(tt.ct); got != tt.want {
				t.Errorf("compressibleContentType(%q) = %v, want %v", tt.ct, got, tt.want)
			}
		})
	}
}

func TestAcceptsGzip(t *testing.T) {
	tests := []struct {
		header string
		want   bool
	}{
		{"gzip", true},
		{"GZIP", true},
		{"gzip, deflate, br", true},
		{"deflate, gzip;q=0.5", true},
		{"br, gzip", true},
		{"*", true},
		{"gzip;q=1.0", true},
		{"deflate", false},
		{"br", false},
		{"identity", false},
		{"", false},
		{"gzip;q=0", false},
		{"gzip;q=0.0", false},
		{"deflate, gzip;q=0", false},
	}

	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			if got := acceptsGzip(tt.header); got != tt.want {
				t.Errorf("acceptsGzip(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestAddVaryIsIdempotent(t *testing.T) {
	h := http.Header{}
	addVary(h, "Accept-Encoding")
	addVary(h, "Accept-Encoding")
	addVary(h, "accept-encoding")

	if got := h.Values("Vary"); len(got) != 1 || got[0] != "Accept-Encoding" {
		t.Errorf("Vary = %q, want exactly one Accept-Encoding entry", got)
	}

	h.Add("Vary", "Accept")
	addVary(h, "Accept-Encoding")
	if got := len(h.Values("Vary")); got != 2 {
		t.Errorf("Vary entries = %d, want 2 (existing entry must be preserved)", got)
	}
}

func TestIsPrecompressedPath(t *testing.T) {
	for _, p := range []string{"/assets/font.woff2", "/x.WOFF2", "/a/b.PNG", "/logo.svg.gz"} {
		if !isPrecompressedPath(p) {
			t.Errorf("isPrecompressedPath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"/assets/main.js", "/index.html", "/manifest.webmanifest", "/"} {
		if isPrecompressedPath(p) {
			t.Errorf("isPrecompressedPath(%q) = true, want false", p)
		}
	}
}

func TestCompressionMiddlewareCompressesEligibleTypes(t *testing.T) {
	body := bytes.Repeat([]byte("export const x = 1;\n"), 300)
	for _, ct := range []string{
		"text/html; charset=utf-8",
		"text/css; charset=utf-8",
		"text/javascript; charset=utf-8",
		"application/javascript",
		"application/json",
		"image/svg+xml",
		"application/manifest+json",
	} {
		t.Run(ct, func(t *testing.T) {
			rec := gzipRequest(compressionMiddleware(fixedResponseHandler(http.StatusOK, ct, true, body)), http.MethodGet, "/app.js", nil)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
				t.Fatalf("Content-Encoding = %q, want gzip", got)
			}
			if got := rec.Header().Get("Content-Length"); got != "" {
				t.Errorf("Content-Length = %q, want it dropped (encoded length is unknown)", got)
			}
			if !hasVary(rec, "Accept-Encoding") {
				t.Error("Vary: Accept-Encoding missing on the compressed response")
			}
			if got := decodeGzip(t, rec.Body.Bytes()); !bytes.Equal(got, body) {
				t.Errorf("decoded body differs from the original (%d vs %d bytes)", len(got), len(body))
			}
			if rec.Body.Len() >= len(body) {
				t.Errorf("encoded body = %d bytes, want less than the %d raw bytes", rec.Body.Len(), len(body))
			}
		})
	}
}

func TestCompressionMiddlewareSkipsIneligibleTypes(t *testing.T) {
	body := bytes.Repeat([]byte("z"), 8*1024)
	for _, ct := range []string{
		"",
		"text/event-stream",
		"application/octet-stream",
		"application/wasm",
		"image/png",
		"font/woff2",
	} {
		t.Run(ct, func(t *testing.T) {
			rec := gzipRequest(compressionMiddleware(fixedResponseHandler(http.StatusOK, ct, true, body)), http.MethodGet, "/blob", nil)

			if got := rec.Header().Get("Content-Encoding"); got != "" {
				t.Fatalf("Content-Encoding = %q, want none", got)
			}
			if !bytes.Equal(rec.Body.Bytes(), body) {
				t.Error("body was altered")
			}
		})
	}
}

func TestCompressionMiddlewareSkipsPrecompressedPaths(t *testing.T) {
	body := bytes.Repeat([]byte("wOF2"), 2048)

	for _, path := range []string{"/assets/font.woff2", "/assets/logo.png"} {
		t.Run(path, func(t *testing.T) {
			rec := gzipRequest(compressionMiddleware(fixedResponseHandler(http.StatusOK, "text/plain; charset=utf-8", true, body)), http.MethodGet, path, nil)

			if got := rec.Header().Get("Content-Encoding"); got != "" {
				t.Errorf("Content-Encoding = %q, want none", got)
			}
			if !bytes.Equal(rec.Body.Bytes(), body) {
				t.Error("body was altered")
			}
		})
	}
}

func TestCompressionMiddlewareSkipsBodyThatIsAlreadyEncoded(t *testing.T) {
	body := bytes.Repeat([]byte("already encoded"), 512)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Encoding", "br")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})

	rec := gzipRequest(compressionMiddleware(h), http.MethodGet, "/encoded", nil)

	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Errorf("Content-Encoding = %q, want the handler's br to be preserved", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Error("body was re-encoded or altered")
	}
}

func TestCompressionMiddlewareSkipsSmallResponses(t *testing.T) {
	small := []byte("hello, not worth a gzip header")

	tests := []struct {
		name     string
		declared bool
	}{
		{"content-length declared", true},
		{"length unknown (finish decides)", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := gzipRequest(compressionMiddleware(fixedResponseHandler(http.StatusOK, "text/plain; charset=utf-8", tt.declared, small)), http.MethodGet, "/small.txt", nil)

			if got := rec.Header().Get("Content-Encoding"); got != "" {
				t.Fatalf("Content-Encoding = %q, want none for a %d byte body", got, len(small))
			}
			if got := rec.Body.String(); got != string(small) {
				t.Errorf("body = %q, want %q", got, string(small))
			}
			if !hasVary(rec, "Accept-Encoding") {
				t.Error("Vary: Accept-Encoding missing")
			}
		})
	}

	rec := gzipRequest(compressionMiddleware(fixedResponseHandler(http.StatusOK, "text/plain", true, small)), http.MethodGet, "/small.txt", nil)
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(small)) {
		t.Errorf("Content-Length = %q, want %d", got, len(small))
	}
}

func TestCompressionMiddlewareBodyRoundTripsExactly(t *testing.T) {
	body := make([]byte, 32*1024)
	rnd := rand.New(rand.NewSource(1))
	if _, err := rnd.Read(body); err != nil {
		t.Fatalf("seeding random body: %v", err)
	}

	rec := gzipRequest(compressionMiddleware(fixedResponseHandler(http.StatusOK, "application/json", false, body)), http.MethodGet, "/api/v1/blob", nil)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := decodeGzip(t, rec.Body.Bytes()); !bytes.Equal(got, body) {
		t.Error("decoded body is not byte-identical to the original")
	}
}

func TestCompressionMiddlewareSkipsNotModified(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"22deff-9f8f0bb0e2e2c4f5"`)
		w.Header().Set("Cache-Control", cacheControlImmutable)
		w.WriteHeader(http.StatusNotModified)
	})

	rec := gzipRequest(compressionMiddleware(h), http.MethodGet, "/app.js", nil)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 body = %q, want empty", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none on a 304", got)
	}
	if !hasVary(rec, "Accept-Encoding") {
		t.Error("Vary: Accept-Encoding missing on the 304 (caches need it to key variants)")
	}
	if got := rec.Header().Get("ETag"); got == "" {
		t.Error("ETag was dropped from the 304")
	}
}

func TestCompressionMiddlewareSkipsPartialContent(t *testing.T) {
	body := bytes.Repeat([]byte("r"), 4096)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Range", "bytes 0-4095/8192")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body)
	})

	rec := gzipRequest(compressionMiddleware(h), http.MethodGet, "/app.js", nil)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none for a range response", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Error("range body was altered")
	}
}

func TestCompressionMiddlewareHandlerThatWritesNothing(t *testing.T) {
	rec := &headerWriteRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/empty", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	h := compressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	}))
	h.ServeHTTP(rec, req)

	if rec.headerWrites != 0 {
		t.Errorf("WriteHeader was called %d times, want 0", rec.headerWrites)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

func TestCompressionMiddlewareFlushDelegatesPartialOutput(t *testing.T) {
	partial := []byte("partial output")
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	h := compressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, err := w.Write(partial); err != nil {
			t.Errorf("Write() error = %v", err)
		}

		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapped writer does not implement http.Flusher")
		}
		f.Flush()

		if got := rec.Body.String(); got != string(partial) {
			t.Errorf("after Flush the client has %q, want %q", got, string(partial))
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if rec.flushes != 1 {
		t.Errorf("Flush reached the client %d times, want 1", rec.flushes)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q: a flushed-before-threshold response must stay plain", got)
	}
	if got := rec.Body.String(); got != string(partial) {
		t.Errorf("body = %q, want %q", got, string(partial))
	}
}

func TestCompressionMiddlewareFlushOfEncodedStream(t *testing.T) {
	big := bytes.Repeat([]byte("compress me "), 4096)
	tail := []byte(`{"tail":true}`)

	var afterFlush int
	var afterFlushDecoded []byte
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	h := compressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(big); err != nil {
			t.Errorf("Write() error = %v", err)
		}

		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapped writer does not implement http.Flusher")
		}
		f.Flush()
		afterFlush = rec.Body.Len()
		afterFlushDecoded = decodeGzipPartial(t, rec.Body.Bytes())

		_, _ = w.Write(tail)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if afterFlush == 0 {
		t.Error("Flush pushed no encoded bytes: the gzip stream was not flushed")
	}
	if !bytes.Equal(afterFlushDecoded, big) {
		t.Errorf("after Flush the client can decode %d of %d bytes", len(afterFlushDecoded), len(big))
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := decodeGzip(t, rec.Body.Bytes()); !bytes.Equal(got, append(append([]byte{}, big...), tail...)) {
		t.Error("decoded stream is not byte-identical to what the handler wrote")
	}
}

func TestCompressionMiddlewareUpgradeDetection(t *testing.T) {
	tests := []struct {
		name        string
		headers     map[string]string
		wantWrapped bool
	}{
		{"connection upgrade", map[string]string{"Connection": "Upgrade", "Upgrade": "websocket"}, false},
		{"upgrade inside a token list", map[string]string{"Connection": "keep-alive, Upgrade", "Upgrade": "websocket"}, false},
		{"upgrade header alone (h2c)", map[string]string{"Upgrade": "h2c"}, false},
		{"keep-alive is not an upgrade", map[string]string{"Connection": "keep-alive"}, true},
		{"no connection header", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, client := net.Pipe()
			defer server.Close()
			defer client.Close()

			rec := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder(), conn: server}
			var wrapped bool
			var hijackErr error

			h := compressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, wrapped = w.(*compressWriter)
				if _, _, err := w.(http.Hijacker).Hijack(); err != nil {
					hijackErr = err
				}
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
			req.Header.Set("Accept-Encoding", "gzip")
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			h.ServeHTTP(rec, req)

			if wrapped != tt.wantWrapped {
				t.Errorf("wrapped = %v, want %v", wrapped, tt.wantWrapped)
			}
			if hijackErr != nil {
				t.Errorf("Hijack() error = %v, want nil (the recorder is hijackable)", hijackErr)
			}
			if !rec.hijacked {
				t.Error("the underlying writer was not hijacked")
			}
			if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
				t.Errorf("Vary = %q, want Accept-Encoding", got)
			}
		})
	}
}

func TestCompressionMiddlewareNotWrappedWhenRequestIneligible(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		headers map[string]string
	}{
		{"no accept-encoding", http.MethodGet, nil},
		{"brotli only", http.MethodGet, map[string]string{"Accept-Encoding": "br"}},
		{"gzip explicitly refused", http.MethodGet, map[string]string{"Accept-Encoding": "gzip;q=0"}},
		{"head request", http.MethodHead, map[string]string{"Accept-Encoding": "gzip"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			var same bool

			h := compressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				same = w == http.ResponseWriter(rec)
				if _, ok := w.(*compressWriter); ok {
					t.Error("ineligible request got a wrapping writer")
				}
			}))

			req := httptest.NewRequest(tt.method, "/app.js", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			h.ServeHTTP(rec, req)

			if !same {
				t.Error("the handler did not receive the original ResponseWriter")
			}
		})
	}
}

func TestCompressionMiddlewareHijackDelegates(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	rec := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder(), conn: server}

	h := compressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("wrapped writer does not implement http.Hijacker")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("Hijack() error = %v", err)
		}
		if conn != server {
			t.Error("Hijack() did not return the underlying connection")
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if !rec.hijacked {
		t.Error("underlying Hijack was not called")
	}
}

func TestCompressionMiddlewareHijackUnsupportedReturnsError(t *testing.T) {
	var hijackErr error

	h := compressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("wrapped writer does not implement http.Hijacker")
		}
		_, _, hijackErr = hj.Hijack()
	}))

	rec := gzipRequest(h, http.MethodGet, "/api/v1/ws", nil)

	if hijackErr == nil {
		t.Fatal("Hijack() on a non-hijackable writer returned no error")
	}
	if !errors.Is(hijackErr, http.ErrNotSupported) {
		t.Errorf("Hijack() error = %v, want http.ErrNotSupported", hijackErr)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (no panic, no broken response)", rec.Code)
	}
}

func TestCompressionMiddlewareHijackFinishesEncodedBodyFirst(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	rec := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder(), conn: server}
	body := bytes.Repeat([]byte("hijack me "), 2048)
	proto := []byte("RAW-PROTOCOL-BYTES")
	var encodedLen int

	h := compressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, err := w.Write(body); err != nil {
			t.Errorf("Write() error = %v", err)
		}
		if _, _, err := w.(http.Hijacker).Hijack(); err != nil {
			t.Errorf("Hijack() error = %v", err)
		}

		encodedLen = rec.Body.Len()
		_, _ = rec.ResponseRecorder.Write(proto)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if !rec.hijacked {
		t.Fatal("the underlying writer was not hijacked")
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := rec.Body.Len(); got != encodedLen+len(proto) {
		t.Fatalf("body length = %d, want %d: %d byte(s) were written after the hijack",
			got, encodedLen+len(proto), got-encodedLen-len(proto))
	}
	if !bytes.HasSuffix(rec.Body.Bytes(), proto) {
		t.Error("the handler's protocol bytes were overwritten after the hijack")
	}
	if got := decodeGzip(t, rec.Body.Bytes()[:encodedLen]); !bytes.Equal(got, body) {
		t.Errorf("decoded pre-hijack body differs from the original (%d vs %d bytes)", len(got), len(body))
	}
}

func TestServerHandlerChainCompressesStaticResponses(t *testing.T) {
	fsys := spaTestFS()
	bundle := bytes.Repeat([]byte("const x=1;console.log('bundle');"), 4096)
	fsys["assets/bundle.0badc0de.js"] = &fstest.MapFile{Data: bundle}

	s := New(&Config{Host: "127.0.0.1", Port: 0})
	s.RegisterWebUI(http.FS(fsys))

	req := httptest.NewRequest(http.MethodGet, "/assets/bundle.0badc0de.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip (is compressionMiddleware wired in?)", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := decodeGzip(t, rec.Body.Bytes()); !bytes.Equal(got, bundle) {
		t.Error("the bundle served by the full chain is not byte-identical to the original")
	}
}

func TestCompressionMiddlewareSPAStaticAsset(t *testing.T) {
	fsys := spaTestFS()
	bundle := bytes.Repeat([]byte("const x=1;console.log('bundle');"), 4096)
	fsys["assets/bundle.0badc0de.js"] = &fstest.MapFile{Data: bundle}

	h := compressionMiddleware(newSPAHandler(http.FS(fsys)))
	const path = "/assets/bundle.0badc0de.js"

	t.Run("200 is compressed and keeps its validators", func(t *testing.T) {
		rec := gzipRequest(h, http.MethodGet, path, nil)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding = %q, want gzip", got)
		}
		if got := rec.Header().Get("Cache-Control"); got != cacheControlImmutable {
			t.Errorf("Cache-Control = %q, want %q", got, cacheControlImmutable)
		}
		if rec.Header().Get("ETag") == "" {
			t.Error("ETag is empty")
		}
		if !hasVary(rec, "Accept-Encoding") {
			t.Error("Vary: Accept-Encoding missing on the 200")
		}
		if got := decodeGzip(t, rec.Body.Bytes()); !bytes.Equal(got, bundle) {
			t.Error("the served bundle is not byte-identical to the original")
		}
		if rec.Body.Len() >= len(bundle) {
			t.Errorf("encoded = %d bytes, want less than %d", rec.Body.Len(), len(bundle))
		}
	})

	var etag string
	t.Run("304 keeps Vary and stays unencoded", func(t *testing.T) {
		first := gzipRequest(h, http.MethodGet, path, nil)
		etag = first.Header().Get("ETag")

		rec := gzipRequest(h, http.MethodGet, path, map[string]string{"If-None-Match": etag})

		if rec.Code != http.StatusNotModified {
			t.Fatalf("status = %d, want 304", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("304 body = %q, want empty", rec.Body.String())
		}
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding = %q, want none on a 304", got)
		}
		if !hasVary(rec, "Accept-Encoding") {
			t.Error("Vary: Accept-Encoding missing on the 304")
		}
	})

	t.Run("identity client still gets Vary and the raw bytes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding = %q, want none", got)
		}
		if !bytes.Equal(rec.Body.Bytes(), bundle) {
			t.Error("identity body differs from the original")
		}
		if !hasVary(rec, "Accept-Encoding") {
			t.Error("Vary: Accept-Encoding missing for the identity variant")
		}
	})

	t.Run("small asset is not compressed", func(t *testing.T) {
		rec := gzipRequest(h, http.MethodGet, "/index_abc.12345678.js", nil)

		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding = %q, want none", got)
		}
		if got := rec.Body.String(); got != "console.log('entry-hashed')" {
			t.Errorf("body = %q, want the fixture content", got)
		}
	})

	t.Run("precompressed asset is not re-encoded", func(t *testing.T) {
		rec := gzipRequest(h, http.MethodGet, "/assets/font.f2.woff2", nil)

		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding = %q, want none for a .woff2", got)
		}
		if rec.Body.String() != "wOF2" {
			t.Errorf("body = %q, want the fixture content", rec.Body.String())
		}
	})
}

func TestCompressionMiddlewareSupportsResponseController(t *testing.T) {
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	var panicVal any
	var writeDeadlineErr error

	h := compressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { panicVal = recover() }()
		rc := http.NewResponseController(w)
		writeDeadlineErr = rc.SetWriteDeadline(time.Time{})
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/stream", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if panicVal != nil {
		t.Errorf("ResponseController use panicked: %v", panicVal)
	}
	if writeDeadlineErr != nil {
		t.Errorf("SetWriteDeadline() = %v, want it to reach the unwrapped writer", writeDeadlineErr)
	}
	if rec.deadlineCalls != 1 {
		t.Errorf("SetWriteDeadline reached the real writer %d times, want 1", rec.deadlineCalls)
	}
	if !rec.lastDeadline.IsZero() {
		t.Errorf("deadline = %v, want the zero time (clear the deadline)", rec.lastDeadline)
	}
}
