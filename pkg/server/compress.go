package server

import (
	"bufio"
	"compress/gzip"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// This file holds the HTTP compression middleware used by the unified server.
//
// Why it exists: the WebUI bundle is ~1.27 MB of JS plus ~109 KB of CSS, served
// from the embedded FS. Without encoding, every first load, hard reload or
// cache miss ships those bytes raw over what is usually a localhost or LAN
// link — but also over a remote/tunneled one.
//
// The design goals, in priority order:
//
//  1. Never break the streaming endpoints that share the mux. /api/v1/ws is a
//     hijacked WebSocket upgrade and the SSE endpoints write incrementally and
//     call Flush(); both must keep working byte-for-byte. So the wrapper is
//     only installed when the *request* is eligible (see
//     requestEligibleForCompression) and, when installed, it implements
//     http.Flusher, http.Hijacker and Unwrap.
//  2. Never send a broken body. Every path through the wrapper closes the gzip
//     writer exactly once; a truncated gzip stream is worse than no
//     compression at all.
//  3. Never make a small response slower. Bodies below gzipMinSize are written
//     through unchanged, which also keeps their Content-Length intact.
//
// Only gzip is negotiated: it is the one coding the standard library can
// produce, and adding a brotli dependency for a gateway is not worth it.

// gzipMinSize is the smallest body we are willing to encode. Below it the
// gzip framing (header + trailing CRC/size) plus the lost Content-Length can
// cost more than the payload. It doubles as the size of the head buffer: writes
// are held until either this many bytes arrived (then the response is known to
// be worth compressing) or somebody flushes (then the response is streamed and
// must not be delayed or buffered).
const gzipMinSize = 1024

// precompressedExtensions lists extensions whose bytes are already compressed.
// The content-type allow-list below already excludes their media types
// (font/woff2, image/png, application/zip...), so this is defence in depth: a
// mislabelled response (say a .woff2 sent as application/octet-stream, or a
// handler that sets text/plain by hand) must still not be re-compressed, since
// gzip over gzip spends CPU to make the payload bigger.
var precompressedExtensions = map[string]bool{
	".woff2": true,
	".woff":  true,
	".png":   true,
	".jpg":   true,
	".jpeg":  true,
	".gif":   true,
	".ico":   true,
	".bz2":   true,
	".zip":   true,
	".gz":    true,
	".br":    true,
}

// compressibleContentType reports whether a response body with this
// Content-Type may be gzip-encoded.
//
// This is an allow-list, not "looks like text": compressing an already
// compressed or binary payload wastes CPU for no gain, so anything not
// explicitly listed stays untouched (including application/wasm and
// application/octet-stream).
//
// text/event-stream matches the text/* branch but is excluded on purpose: an
// SSE body is a sequence of small events that must reach the client as they
// happen, encoded per event. Compression would add framing work per event and
// some clients/proxies do not decode SSE incrementally, i.e. it can break the
// stream. It is also redundant with the flush rule below, which already keeps
// any flushed-before-threshold response uncompressed.
func compressibleContentType(ct string) bool {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		// No declared type: ServeContent/http.DetectContentType may still be
		// about to sniff one, but guessing here risks encoding a binary body.
		return false
	}
	if ct == "text/event-stream" {
		return false
	}

	switch ct {
	case "application/javascript",
		"text/javascript",
		"application/json",
		"image/svg+xml",
		"application/manifest+json":
		return true
	}
	return strings.HasPrefix(ct, "text/")
}

// isPrecompressedPath reports whether the request path points at a file whose
// bytes are already compressed (by extension).
func isPrecompressedPath(p string) bool {
	return precompressedExtensions[strings.ToLower(path.Ext(p))]
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip. An
// explicit "gzip;q=0" (and "*;q=0") is an outright refusal and wins over any
// other entry; a missing header means identity only.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		coding := strings.ToLower(strings.TrimSpace(fields[0]))
		if coding != "gzip" && coding != "*" {
			continue
		}

		q := 1.0
		for _, param := range fields[1:] {
			param = strings.TrimSpace(param)
			if !strings.HasPrefix(strings.ToLower(param), "q=") {
				continue
			}
			if v, err := strconv.ParseFloat(strings.TrimSpace(param[2:]), 64); err == nil {
				q = v
			}
		}

		if coding == "gzip" {
			return q > 0
		}
		if q > 0 {
			return true
		}
	}
	return false
}

// isUpgradeRequest reports whether r asks for a protocol upgrade (WebSocket,
// h2c, ...). Such a request is answered by hijacking the connection, so it must
// never see a wrapping ResponseWriter.
func isUpgradeRequest(r *http.Request) bool {
	if r.Header.Get("Upgrade") != "" {
		return true
	}
	return headerHasToken(r.Header.Get("Connection"), "upgrade")
}

// headerHasToken reports whether a comma-separated header (e.g. Connection)
// contains token, compared case-insensitively.
func headerHasToken(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// addVary appends value to the Vary header unless it is already listed.
func addVary(h http.Header, value string) {
	for _, existing := range h.Values("Vary") {
		for _, field := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(field), value) {
				return
			}
		}
	}
	h.Add("Vary", value)
}

// requestEligibleForCompression reports whether it is worth wrapping r's
// ResponseWriter at all. Requests that fail this test are passed to the next
// handler with the original, untouched ResponseWriter.
func requestEligibleForCompression(r *http.Request) bool {
	// WebSocket/h2c upgrades hijack the connection: no wrapper, no gzip.
	if isUpgradeRequest(r) {
		return false
	}
	// HEAD responses have no body to encode (RFC 9110 §9.3.2).
	if r.Method == http.MethodHead {
		return false
	}
	return acceptsGzip(r.Header.Get("Accept-Encoding"))
}

// compressionMiddleware gzip-encodes eligible responses produced by next.
//
// Placement: it wraps the mux directly (security headers -> CORS -> this ->
// mux). Keeping it inside the CORS middleware means the compressor only ever
// sees real handler responses (not the OPTIONS short-circuit), and keeping it
// outside the mux is what makes it cover every route with one instance.
//
// WebSocket safety is handled by self-disabling rather than by path matching:
// an upgrade request is detected from Connection/Upgrade and passed through
// unwrapped. A path-based exclusion would silently stop protecting
// /api/v1/ws if the endpoint were ever renamed, and would not cover a future
// second upgrade endpoint; the header test is the property that actually
// matters. On top of that the wrapper implements Hijack, so even a missed
// upgrade request could still complete (without compression) instead of
// panicking with "http.Hijacker interface is not supported".
func compressionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Vary is advertised on every response this middleware could have
		// encoded — including 304s and responses that ended up too small to
		// encode. Shared caches key their stored variants on it, so an
		// identity response that omits it could later be replayed to a client
		// that asked for gzip (and vice versa).
		addVary(w.Header(), "Accept-Encoding")

		if !requestEligibleForCompression(r) {
			next.ServeHTTP(w, r)
			return
		}

		cw := &compressWriter{ResponseWriter: w, req: r}
		// Deferred so the gzip stream is finished even if the handler panics.
		defer cw.finish()
		next.ServeHTTP(cw, r)
	})
}

// compressWriter buffers the response head until it can tell whether the body
// is worth encoding.
//
// It deliberately does NOT implement io.ReaderFrom: net/http would then use the
// optimized sendfile/CopyN path and hand the file straight to the socket,
// bypassing the encoder. Without it net/http falls back to a plain io.Copy
// loop through Write, which is the correct — if slower — route.
type compressWriter struct {
	http.ResponseWriter
	req *http.Request

	mode        compressMode
	wroteHeader bool
	status      int

	// buf holds the head of the body while the decision is pending. It is
	// dropped as soon as the mode is committed.
	buf []byte
	// gz is non-nil exactly when mode == modeGzip. Closed once, in finish.
	gz *gzip.Writer
}

type compressMode int

const (
	// modeUndecided: the head has not reached the client yet, the body is
	// being buffered while we wait for enough bytes to justify a decision.
	modeUndecided compressMode = iota
	// modePlain: the response is written through unchanged from now on.
	modePlain
	// modeGzip: the response is gzip-encoded.
	modeGzip
)

// Header returns the live header map of the wrapped writer: handlers must be
// able to set Content-Type/ETag/Cache-Control before the head is committed.
func (w *compressWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

// Unwrap exposes the wrapped writer to http.ResponseController so the SSE
// handlers keep working: they clear the server WriteTimeout with
// SetWriteDeadline, which reaches the real writer through this method. Without
// Unwrap, SetWriteDeadline would return ErrNotSupported and every SSE stream
// would be killed by WriteTimeout mid-body.
func (w *compressWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// WriteHeader inspects the status and the headers the handler set and commits
// the response to plain when it can never be compressed. Only when the response
// is a candidate does it keep the head back, so the body can be buffered.
func (w *compressWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status

	if !w.canCompress(status) {
		w.startPlain()
		return
	}

	// A declared small body stays as it is: encoding it would drop the
	// Content-Length (and the exact-size fast path that comes with it) to save
	// a few bytes it does not even have.
	if n, ok := contentLength(w.Header()); ok && n < gzipMinSize {
		w.startPlain()
	}
}

// Write buffers the first bytes of the body and starts encoding once the
// response proves to be big enough.
func (w *compressWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	switch w.mode {
	case modePlain:
		return w.ResponseWriter.Write(p)
	case modeGzip:
		return w.gz.Write(p)
	}

	w.buf = append(w.buf, p...)
	if len(w.buf) < gzipMinSize {
		return len(p), nil
	}

	// The body crossed the threshold: encode it, including what we buffered.
	if err := w.startGzip(); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush forwards the flush to the client. A flush means the handler wants the
// current bytes on the wire now — SSE and progress streams — so a still
// undecided response is committed to plain rather than encoded: buffering it
// into a gzip stream to save a few bytes would trade away exactly the latency
// the flush was asking for.
func (w *compressWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	switch w.mode {
	case modeGzip:
		// Flush the gzip stream first so the current events are encodable by
		// the client, then let the connection push them out.
		_ = w.gz.Flush()
	case modeUndecided:
		w.startPlain()
	}

	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack delegates to the wrapped writer. It reports http.ErrNotSupported
// (the same error net/http's ResponseController uses) instead of panicking when
// the underlying writer cannot hijack — http.ResponseWriter does not expose
// Hijack, so a type assertion is the only way to ask, and a failed assertion in
// a handler would otherwise look like a programming error.
//
// Whatever was written before the hijack is finished first (the gzip trailer,
// or the still buffered head verbatim): after this call the connection belongs
// to the handler, and the deferred finish() writing a trailer into it would
// corrupt the protocol the handler is about to speak.
func (w *compressWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	switch w.mode {
	case modeUndecided:
		w.startPlain()
	case modeGzip:
		w.gz.Close()
		w.gz = nil
		w.mode = modePlain
	}

	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

// canCompress reports whether a response with this status and these headers may
// be encoded. It is only consulted before any body byte is written.
func (w *compressWriter) canCompress(status int) bool {
	switch status {
	case http.StatusNotModified, http.StatusNoContent, http.StatusResetContent, http.StatusPartialContent:
		// 304 has no body; 204/205 must not carry one; 206 bytes are offsets
		// into the *unencoded* representation, so encoding them would make the
		// Content-Range meaningless.
		return false
	}
	if status < 200 {
		return false
	}

	h := w.Header()
	if h.Get("Content-Encoding") != "" {
		// Someone already encoded this body (handler or upstream); encoding it
		// again would double-encode it.
		return false
	}
	if h.Get("Content-Range") != "" {
		return false
	}
	if isPrecompressedPath(w.req.URL.Path) {
		return false
	}
	return compressibleContentType(h.Get("Content-Type"))
}

// startPlain commits the head and writes any buffered bytes through unchanged.
func (w *compressWriter) startPlain() {
	if w.mode != modeUndecided {
		return
	}
	w.mode = modePlain

	w.commitHeader()
	if len(w.buf) > 0 {
		_, _ = w.ResponseWriter.Write(w.buf)
		w.buf = nil
	}
}

// startGzip starts the encoder, commits the head with Content-Encoding: gzip
// and pushes the buffered head bytes through it.
func (w *compressWriter) startGzip() error {
	gz, err := gzip.NewWriterLevel(w.ResponseWriter, gzip.BestSpeed)
	if err != nil {
		return err
	}
	w.mode = modeGzip
	w.gz = gz

	// The encoded length is unknown until the stream ends, so the declared one
	// no longer applies: dropping it makes net/http use chunked encoding.
	w.Header().Del("Content-Length")
	// The ETag is left exactly as the handler set it (the strong validator
	// built by static.go's strongETag), so the identity and the gzip
	// representations of one body carry the SAME strong validator. RFC 9110
	// §8.8.3 reserves a strong validator for a single representation, so this
	// is technically outside the spec. Accepted here, deliberately: the two
	// variants are already kept apart by the `Vary: Accept-Encoding` every
	// response of this middleware advertises (compressionMiddleware), so an
	// HTTP cache keys them separately and cannot replay a gzip body to a
	// client that asked for identity (or vice versa); and this gateway is
	// same-origin, with no shared CDN in front that could normalise one
	// variant's ETag onto the other. Switching to a weak validator (or to a
	// per-encoding tag) would give up the strong 304 revalidation that makes
	// the identity path cheap, in exchange for a nit that cannot be observed
	// with this cache topology.
	w.Header().Set("Content-Encoding", "gzip")
	w.commitHeader()

	if len(w.buf) == 0 {
		return nil
	}
	_, err = gz.Write(w.buf)
	w.buf = nil
	return err
}

// commitHeader sends the status line to the client. It is a no-op if the
// handler never wrote one: net/http implicitly answers 200 in that case and we
// must not pre-empt it.
func (w *compressWriter) commitHeader() {
	if !w.wroteHeader {
		return
	}
	w.ResponseWriter.WriteHeader(w.status)
}

// finish resolves a still pending decision and closes the gzip stream exactly
// once. A handler that returns without writing enough bytes lands here, and so
// does a 304 or an empty 200.
func (w *compressWriter) finish() {
	switch w.mode {
	case modeUndecided:
		// Nothing was big enough (or nothing was written at all): the head and
		// the buffered bytes go out unchanged.
		w.startPlain()
	case modeGzip:
		// Close writes the gzip trailer. Skipping it would send a truncated
		// stream that clients reject.
		w.gz.Close()
		w.gz = nil
	}
}

// contentLength reports the declared Content-Length, if any.
func contentLength(h http.Header) (int64, bool) {
	v := h.Get("Content-Length")
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
