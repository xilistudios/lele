// Package server provides a unified HTTP server that consolidates all HTTP
// services (API, Web UI, health checks, webhooks) under a single port.
package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/health"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/security"
)

// Server centralizes all HTTP routing under one http.Server.
type Server struct {
	cfg        *Config
	http       *http.Server
	mux        *http.ServeMux
	checks     map[string]health.Check
	startTime  time.Time
	ready      bool
	actualAddr string
	mu         sync.RWMutex
}

// Config holds server configuration.
type Config struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// LeleDir is used for CORS origin matching (e.g., localhost origins).
	LeleDir string `json:"-"`
}

// RouteRegistrar is implemented by channels that need to register HTTP routes.
type RouteRegistrar interface {
	RegisterRoutes(mux *http.ServeMux)
}

// New creates a new unified Server.
func New(cfg *Config) *Server {
	mux := http.NewServeMux()
	s := &Server{
		cfg:       cfg,
		mux:       mux,
		checks:    make(map[string]health.Check),
		startTime: time.Now(),
	}

	// Handler chain: Security Headers -> CORS -> Compression -> Mux.
	//
	// The compressor wraps the mux rather than being installed per route, so
	// every later-registered route (channels, webhooks) gets it for free. It
	// sits inside CORS so it only sees real handler responses, and it
	// self-disables on Connection: Upgrade/Upgrade: headers instead of
	// matching paths — see compressionMiddleware for why the header test is
	// the property that actually protects the /api/v1/ws hijack.
	handler := s.securityHeadersMiddleware(s.corsMiddleware(compressionMiddleware(mux)))

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	s.http = &http.Server{
		Addr:    addr,
		Handler: handler,

		// ReadHeaderTimeout bounds how long a client may take to send its
		// request head: without it a slowloris connection can hold a slot
		// open indefinitely. It only covers the head, so it never truncates
		// a legitimately slow upload body.
		ReadHeaderTimeout: 10 * time.Second,

		// ReadTimeout additionally covers the request body.
		ReadTimeout: 30 * time.Second,

		// WriteTimeout is left at 30s on purpose. It is the deadline for the
		// whole response, which would kill long-lived SSE streams — but the
		// streaming handlers clear it per request with
		// http.NewResponseController(w).SetWriteDeadline (GW-L9 in
		// pkg/channels; the compression wrapper implements Unwrap so that
		// still reaches the real writer). Lowering it here would break
		// streams that do not clear it, and hijacked connections (WebSocket)
		// do not use it at all.
		WriteTimeout: 30 * time.Second,

		// IdleTimeout reaps keep-alive connections that go quiet; without it
		// they are only bounded by ReadTimeout.
		IdleTimeout: 120 * time.Second,
	}

	return s
}

// Mux returns the underlying http.ServeMux for route registration.
func (s *Server) Mux() *http.ServeMux {
	return s.mux
}

// Addr returns the listen address.
func (s *Server) Addr() string {
	return s.http.Addr
}

// RegisterHealth registers /health and /ready endpoints.
func (s *Server) RegisterHealth() {
	s.mux.HandleFunc("/health", s.healthHandler)
	s.mux.HandleFunc("/ready", s.readyHandler)
}

// RegisterWebUI serves the embedded frontend SPA from the given fs.FS.
// The distFS should be rooted at the web/dist directory.
//
// distFS may be any http.FileSystem (http.FS over an embed.FS, http.Dir, a
// test MapFS...). The ETag memo is keyed on (name, size, modTime), so a mutable
// backing store keeps working: a replaced file is a new key and therefore gets
// a new validator instead of a stale one.
func (s *Server) RegisterWebUI(distFS http.FileSystem) {
	spaHandler := newSPAHandler(distFS)
	s.mux.Handle("/", spaHandler)
}

// RegisterWebhook registers an external webhook handler at the given path.
func (s *Server) RegisterWebhook(path string, handler http.HandlerFunc) {
	s.mux.HandleFunc(path, handler)
}

// RegisterCheck adds a health check.
func (s *Server) RegisterCheck(name string, status string, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks[name] = health.Check{
		Name:      name,
		Status:    status,
		Message:   message,
		Timestamp: time.Now(),
	}
}

// SetReady sets the server readiness state.
func (s *Server) SetReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = ready
}

// Start begins listening. Call after all routes are registered.
func (s *Server) Start() error {
	s.mu.Lock()
	s.ready = true
	s.actualAddr = s.http.Addr
	s.mu.Unlock()

	logger.InfoCF("server", "Starting unified server", map[string]interface{}{
		"address": s.http.Addr,
	})

	if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Serve begins serving on an externally provided listener. This enables
// dynamic port allocation (e.g., port 0); the actual bound address is
// available via ActualAddr() once serving begins.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	s.ready = true
	s.actualAddr = ln.Addr().String()
	s.mu.Unlock()

	actual := ln.Addr().String()
	logger.InfoCF("server", "Starting unified server", map[string]interface{}{
		"address": actual,
	})

	if err := s.http.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// ActualAddr returns the actual bound address (e.g. "127.0.0.1:54321").
// If the server hasn't begun serving on a dynamic listener yet, it falls back
// to the statically configured address.
func (s *Server) ActualAddr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.actualAddr != "" {
		return s.actualAddr
	}
	return s.http.Addr
}

// ActualPort returns the port parsed from ActualAddr(). It returns 0 if the
// address cannot be parsed.
func (s *Server) ActualPort() int {
	_, portStr, err := net.SplitHostPort(s.ActualAddr())
	if err != nil {
		return 0
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return 0
	}
	return port
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.ready = false
	s.mu.Unlock()

	logger.InfoC("server", "Shutting down server")

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.http.Shutdown(shutdownCtx)
}

// --- Middleware ---

// securityHeadersMiddleware applies the single canonical CSP (pkg/security).
// WEB-M13: the policy is now request-aware — connect-src pins the websocket
// endpoints to the serving host instead of the old `ws: wss:` wildcard that
// allowed exfiltration to any host.
func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		security.SecurityHeaders(w, r)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if origin != "" {
			// Allow common localhost origins and same-origin
			if s.isOriginAllowed(origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
		}

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) isOriginAllowed(origin string) bool {
	// Allow Tauri-specific origins
	switch origin {
	case "tauri://localhost", "https://tauri.localhost":
		return true
	}

	// Parse URL for localhost-based origins
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	// Allow localhost, 127.0.0.1, and 0.0.0.0 on any port
	hostname := u.Hostname()
	return hostname == "localhost" ||
		hostname == "127.0.0.1" ||
		hostname == "0.0.0.0"
}

// --- Health Handlers ---

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	uptime := time.Since(s.startTime)
	fmt.Fprintf(w, `{"status":"ok","uptime":"%s"}`, uptime.String())
}

func (s *Server) readyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	s.mu.RLock()
	ready := s.ready
	checks := make(map[string]health.Check, len(s.checks))
	for k, v := range s.checks {
		checks[k] = v
	}
	s.mu.RUnlock()

	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"status":"not ready"}`)
		return
	}

	for _, check := range checks {
		if check.Status == "fail" {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"status":"not ready"}`)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	uptime := time.Since(s.startTime)
	fmt.Fprintf(w, `{"status":"ready","uptime":"%s"}`, uptime.String())
}

// --- SPA Handler ---

// spaHandler serves a single-page application from an http.FileSystem,
// falling back to index.html for client-side routing.
//
// Static-asset delivery (cache validators, Cache-Control, 304 handling) lives
// in static.go; this type owns routing plus the one-off/memoised state
// (index body, per-path ETags).
type spaHandler struct {
	fs     http.FileSystem
	index  []byte
	loaded bool
	once   sync.Once

	// indexETag is the strong validator for the index.html SPA fallback body.
	indexETag string

	// etagMu guards etags: per-(name, size, modTime) strong validators,
	// computed lazily by the first request for a key and then reused. See
	// etagForPath for the single-flight and eviction rules.
	etagMu sync.RWMutex
	etags  map[etagKey]*etagEntry
	// etagCap bounds etags (see defaultETagMemoCap). A field rather than a
	// constant so tests can shrink it.
	etagCap int
}

func newSPAHandler(fs http.FileSystem) *spaHandler {
	return &spaHandler{
		fs:      fs,
		etags:   make(map[etagKey]*etagEntry),
		etagCap: defaultETagMemoCap,
	}
}

// loadIndex reads index.html once and keeps it in memory for the SPA
// fallback. io.ReadAll is mandatory here: http.File gives no full-read
// guarantee, so a single f.Read() could return fewer bytes than requested and
// silently serve a truncated (zero-padded) index. On any error the handler
// stays unloaded and requests are answered with 404 instead of a corrupt page.
func (h *spaHandler) loadIndex() {
	f, err := h.fs.Open("index.html")
	if err != nil {
		return
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return
	}

	h.index = data
	h.indexETag = strongETag(data)
	h.loaded = true
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// API routes don't get SPA fallback — they should be registered first
	// and will be matched by more specific patterns.
	path := r.URL.Path

	// Don't serve SPA for API/webhook paths
	if isAPIPath(path) {
		http.NotFound(w, r)
		return
	}

	// Try to serve the exact file. The handle is streamed directly (no
	// per-request http.FileServer construction) with its cache validators.
	name := strings.TrimPrefix(path, "/")
	if f, err := h.fs.Open(name); err == nil {
		if info, statErr := f.Stat(); statErr == nil && !info.IsDir() {
			// info keys the ETag memo, so a changed file cannot keep serving
			// a stale validator.
			h.serveStaticFile(w, r, path, f, info) // takes ownership of f
			return
		}
		f.Close()
	}

	// Fall back to index.html for SPA routing
	h.once.Do(h.loadIndex)
	if h.loaded {
		h.serveIndex(w, r)
		return
	}

	// Last resort: index.html is unreadable, so there is no SPA to route into.
	//
	// This deliberately used to be http.FileServer(h.fs), which was wrong in
	// two ways: (1) it rebuilt a FileServer on every request just to handle a
	// path that is already known to miss on disk, and (2) pointing a
	// FileServer at the *root* of an unreadable/broken FS makes it serve
	// whatever it can still reach — directory listings and /index.html
	// redirects — i.e. it can leak tree contents instead of failing closed.
	// A root-level FS that cannot produce index.html is a broken build, not a
	// browsable directory, so answer 404. Requests for real files never reach
	// this branch (they are streamed above), and isAPIPath already 404s
	// /api/* and /webhook/* before any fallback.
	http.NotFound(w, r)
}

func isAPIPath(path string) bool {
	return len(path) >= 5 && path[:5] == "/api/" ||
		len(path) >= 9 && path[:9] == "/webhook/"
}
