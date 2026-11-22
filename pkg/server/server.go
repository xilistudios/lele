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

type Config struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	LeleDir string `json:"-"`
}

type RouteRegistrar interface {
	RegisterRoutes(mux *http.ServeMux)
}

func New(cfg *Config) *Server {
	mux := http.NewServeMux()
	s := &Server{
		cfg:       cfg,
		mux:       mux,
		checks:    make(map[string]health.Check),
		startTime: time.Now(),
	}

	handler := s.securityHeadersMiddleware(s.corsMiddleware(compressionMiddleware(mux)))

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	s.http = &http.Server{
		Addr:    addr,
		Handler: handler,

		ReadHeaderTimeout: 10 * time.Second,

		ReadTimeout: 30 * time.Second,

		WriteTimeout: 30 * time.Second,

		IdleTimeout: 120 * time.Second,
	}

	return s
}

func (s *Server) Mux() *http.ServeMux {
	return s.mux
}

func (s *Server) Addr() string {
	return s.http.Addr
}

func (s *Server) RegisterHealth() {
	s.mux.HandleFunc("/health", s.healthHandler)
	s.mux.HandleFunc("/ready", s.readyHandler)
}

func (s *Server) RegisterWebUI(distFS http.FileSystem) {
	spaHandler := newSPAHandler(distFS)
	s.mux.Handle("/", spaHandler)
}

func (s *Server) RegisterWebhook(path string, handler http.HandlerFunc) {
	s.mux.HandleFunc(path, handler)
}

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

func (s *Server) SetReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = ready
}

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

func (s *Server) ActualAddr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.actualAddr != "" {
		return s.actualAddr
	}
	return s.http.Addr
}

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

func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.ready = false
	s.mu.Unlock()

	logger.InfoC("server", "Shutting down server")

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.http.Shutdown(shutdownCtx)
}

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
	switch origin {
	case "tauri://localhost", "https://tauri.localhost":
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	hostname := u.Hostname()
	return hostname == "localhost" ||
		hostname == "127.0.0.1" ||
		hostname == "0.0.0.0"
}

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

type spaHandler struct {
	fs     http.FileSystem
	index  []byte
	loaded bool
	once   sync.Once

	indexETag string

	etagMu  sync.RWMutex
	etags   map[etagKey]*etagEntry
	etagCap int
}

func newSPAHandler(fs http.FileSystem) *spaHandler {
	return &spaHandler{
		fs:      fs,
		etags:   make(map[etagKey]*etagEntry),
		etagCap: defaultETagMemoCap,
	}
}

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
	path := r.URL.Path

	if isAPIPath(path) {
		http.NotFound(w, r)
		return
	}

	name := strings.TrimPrefix(path, "/")
	if f, err := h.fs.Open(name); err == nil {
		if info, statErr := f.Stat(); statErr == nil && !info.IsDir() {
			h.serveStaticFile(w, r, path, f, info)
			return
		}
		f.Close()
	}

	h.once.Do(h.loadIndex)
	if h.loaded {
		h.serveIndex(w, r)
		return
	}

	http.NotFound(w, r)
}

func isAPIPath(path string) bool {
	return len(path) >= 5 && path[:5] == "/api/" ||
		len(path) >= 9 && path[:9] == "/webhook/"
}
