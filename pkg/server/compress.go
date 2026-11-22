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

const gzipMinSize = 1024

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

func compressibleContentType(ct string) bool {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
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

func isPrecompressedPath(p string) bool {
	return precompressedExtensions[strings.ToLower(path.Ext(p))]
}

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

func isUpgradeRequest(r *http.Request) bool {
	if r.Header.Get("Upgrade") != "" {
		return true
	}
	return headerHasToken(r.Header.Get("Connection"), "upgrade")
}

func headerHasToken(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

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

func requestEligibleForCompression(r *http.Request) bool {
	if isUpgradeRequest(r) {
		return false
	}
	if r.Method == http.MethodHead {
		return false
	}
	return acceptsGzip(r.Header.Get("Accept-Encoding"))
}

func compressionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addVary(w.Header(), "Accept-Encoding")

		if !requestEligibleForCompression(r) {
			next.ServeHTTP(w, r)
			return
		}

		cw := &compressWriter{ResponseWriter: w, req: r}
		defer cw.finish()
		next.ServeHTTP(cw, r)
	})
}

type compressWriter struct {
	http.ResponseWriter
	req *http.Request

	mode        compressMode
	wroteHeader bool
	status      int

	buf []byte
	gz  *gzip.Writer
}

type compressMode int

const (
	modeUndecided compressMode = iota
	modePlain
	modeGzip
)

func (w *compressWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *compressWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

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

	if n, ok := contentLength(w.Header()); ok && n < gzipMinSize {
		w.startPlain()
	}
}

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

	if err := w.startGzip(); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *compressWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	switch w.mode {
	case modeGzip:
		_ = w.gz.Flush()
	case modeUndecided:
		w.startPlain()
	}

	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

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

func (w *compressWriter) canCompress(status int) bool {
	switch status {
	case http.StatusNotModified, http.StatusNoContent, http.StatusResetContent, http.StatusPartialContent:
		return false
	}
	if status < 200 {
		return false
	}

	h := w.Header()
	if h.Get("Content-Encoding") != "" {
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

func (w *compressWriter) startGzip() error {
	gz, err := gzip.NewWriterLevel(w.ResponseWriter, gzip.BestSpeed)
	if err != nil {
		return err
	}
	w.mode = modeGzip
	w.gz = gz

	w.Header().Del("Content-Length")
	w.Header().Set("Content-Encoding", "gzip")
	w.commitHeader()

	if len(w.buf) == 0 {
		return nil
	}
	_, err = gz.Write(w.buf)
	w.buf = nil
	return err
}

func (w *compressWriter) commitHeader() {
	if !w.wroteHeader {
		return
	}
	w.ResponseWriter.WriteHeader(w.status)
}

func (w *compressWriter) finish() {
	switch w.mode {
	case modeUndecided:
		w.startPlain()
	case modeGzip:
		w.gz.Close()
		w.gz = nil
	}
}

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
