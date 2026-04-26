// Package server provides a unified HTTP server that consolidates all HTTP
// services (API, Web UI, health checks, webhooks) under a single port.
package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/health"
	"github.com/xilistudios/lele/pkg/logger"
)

// Server centralizes all HTTP routing under one http.Server.
type Server struct {
	cfg       *Config
	http      *http.Server
	mux       *http.ServeMux
	checks    map[string]health.Check
	startTime time.Time
	ready     bool
	mu        sync.RWMutex
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

	// Build the handler chain: Security Headers -> CORS -> Mux
	handler := s.securityHeadersMiddleware(s.corsMiddleware(mux))

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	s.http = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
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
	s.mu.Unlock()

	logger.InfoCF("server", "Starting unified server", map[string]interface{}{
		"address": s.http.Addr,
	})

	if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
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

func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self' ws: wss:; frame-ancestors 'none'")
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
type spaHandler struct {
	fs     http.FileSystem
	index  []byte
	loaded bool
	once   sync.Once
}

func newSPAHandler(fs http.FileSystem) *spaHandler {
	return &spaHandler{fs: fs}
}

func (h *spaHandler) loadIndex() {
	f, err := h.fs.Open("index.html")
	if err != nil {
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return
	}

	h.index = make([]byte, info.Size())
	f.Read(h.index)
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

	// Try to serve the exact file
	f, err := h.fs.Open(path[1:]) // strip leading /
	if err == nil {
		defer f.Close()
		info, _ := f.Stat()
		if info != nil && !info.IsDir() {
			// Serve file with appropriate content type
			http.FileServer(h.fs).ServeHTTP(w, r)
			return
		}
	}

	// Fall back to index.html for SPA routing
	h.once.Do(h.loadIndex)
	if h.loaded {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(h.index)
		return
	}

	// Last resort: try FileServer
	http.FileServer(h.fs).ServeHTTP(w, r)
}

func isAPIPath(path string) bool {
	return len(path) >= 5 && path[:5] == "/api/" ||
		len(path) >= 9 && path[:9] == "/webhook/"
}
