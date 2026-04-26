package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	cfg := &Config{
		Host: "127.0.0.1",
		Port: 0, // Use random port for testing
	}

	s := New(cfg)
	if s == nil {
		t.Fatal("New() returned nil")
	}
	if s.http == nil {
		t.Error("http.Server not initialized")
	}
	if s.mux == nil {
		t.Error("mux not initialized")
	}
	if s.checks == nil {
		t.Error("checks map not initialized")
	}
}

func TestAddr(t *testing.T) {
	cfg := &Config{
		Host: "0.0.0.0",
		Port: 8080,
	}

	s := New(cfg)
	expected := "0.0.0.0:8080"
	if s.Addr() != expected {
		t.Errorf("Addr() = %q, want %q", s.Addr(), expected)
	}
}

func TestMux(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 0})
	if s.Mux() == nil {
		t.Error("Mux() returned nil")
	}
}

func TestRegisterHealth(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 0})
	s.RegisterHealth()

	// Test /health endpoint
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("/health status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() == "" {
		t.Error("/health response body is empty")
	}

	// Test /ready endpoint (not ready yet)
	req = httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/ready status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestSetReady(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 0})
	s.RegisterHealth()

	// Initially not ready
	s.mu.RLock()
	ready := s.ready
	s.mu.RUnlock()
	if ready {
		t.Error("server should not be ready initially")
	}

	// Set ready
	s.SetReady(true)

	s.mu.RLock()
	ready = s.ready
	s.mu.RUnlock()
	if !ready {
		t.Error("server should be ready after SetReady(true)")
	}

	// Test /ready returns OK
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("/ready status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRegisterCheck(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 0})
	s.RegisterHealth()

	s.RegisterCheck("database", "ok", "connected")
	s.RegisterCheck("cache", "fail", "connection refused")

	// Ready should fail because one check is failing
	s.SetReady(true)
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/ready status = %d, want %d (should fail due to failing check)", rec.Code, http.StatusServiceUnavailable)
	}

	// Update check to pass
	s.RegisterCheck("cache", "ok", "connected")

	req = httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("/ready status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRegisterWebUI(t *testing.T) {
	// Create a simple in-memory filesystem for testing
	// This is a minimal test - real SPA testing would require actual files
	s := New(&Config{Host: "127.0.0.1", Port: 0})

	// Create a simple test filesystem
	distFS := http.Dir("testdata") // Will fail if no testdata, but tests the registration

	s.RegisterWebUI(distFS)

	// Just verify it doesn't panic - actual SPA serving is tested separately
}

func TestRegisterWebhook(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 0})

	called := false
	handler := func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}

	s.RegisterWebhook("/webhook/test", handler)

	req := httptest.NewRequest(http.MethodPost, "/webhook/test", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if !called {
		t.Error("webhook handler was not called")
	}
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 0})
	s.mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	s.securityHeadersMiddleware(s.mux).ServeHTTP(rec, req)

	tests := []struct {
		header, expected string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"X-Xss-Protection", "1; mode=block"},
		{"Referrer-Policy", "strict-origin-when-cross-origin"},
	}

	for _, tt := range tests {
		got := rec.Header().Get(tt.header)
		if got != tt.expected {
			t.Errorf("%s = %q, want %q", tt.header, got, tt.expected)
		}
	}
}

func TestCORSMiddleware_AllowedOrigin(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 0})
	s.mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	allowedOrigins := []string{
		"http://localhost",
		"http://localhost:3005",
		"http://localhost:8080",
		"http://localhost:12345",
		"http://127.0.0.1:3005",
		"http://127.0.0.1:8080",
		"http://0.0.0.0:3005",
		"http://0.0.0.0:8080",
		"tauri://localhost",
		"https://tauri.localhost",
	}

	for _, origin := range allowedOrigins {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		s.corsMiddleware(s.mux).ServeHTTP(rec, req)

		got := rec.Header().Get("Access-Control-Allow-Origin")
		if got != origin {
			t.Errorf("Origin %q: Access-Control-Allow-Origin = %q, want %q", origin, got, origin)
		}
	}
}

func TestCORSMiddleware_DisallowedOrigin(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 0})
	s.mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	disallowedOrigins := []string{
		"http://evil.com",
		"https://attacker.com",
		"http://localhost.evil.com",
	}

	for _, origin := range disallowedOrigins {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		s.corsMiddleware(s.mux).ServeHTTP(rec, req)

		got := rec.Header().Get("Access-Control-Allow-Origin")
		if got != "" {
			t.Errorf("Origin %q should not be allowed, got Access-Control-Allow-Origin = %q", origin, got)
		}
	}
}

func TestCORSMiddleware_OptionsRequest(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 0})
	s.mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.Header.Set("Origin", "http://localhost:3005")
	rec := httptest.NewRecorder()
	s.corsMiddleware(s.mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("OPTIONS status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify CORS headers are set
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("Access-Control-Allow-Methods not set")
	}
	if rec.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Error("Access-Control-Allow-Headers not set")
	}
}

func TestIsOriginAllowed(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 0})

	tests := []struct {
		origin   string
		expected bool
	}{
		{"http://localhost", true},
		{"http://localhost:3005", true},
		{"http://localhost:8080", true},
		{"http://localhost:65535", true},
		{"http://127.0.0.1:3005", true},
		{"http://127.0.0.1:8080", true},
		{"http://0.0.0.0:3005", true},
		{"http://0.0.0.0:8080", true},
		{"tauri://localhost", true},
		{"https://tauri.localhost", true},
		{"http://evil.com", false},
		{"https://attacker.com", false},
		{"http://localhost.evil.com", false},
		{"", false},
		{"not-a-url", false},
	}

	for _, tt := range tests {
		got := s.isOriginAllowed(tt.origin)
		if got != tt.expected {
			t.Errorf("isOriginAllowed(%q) = %v, want %v", tt.origin, got, tt.expected)
		}
	}
}

func TestStartStop(t *testing.T) {
	cfg := &Config{
		Host: "127.0.0.1",
		Port: 0, // Random port
	}
	s := New(cfg)
	s.RegisterHealth()

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start()
	}()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Stop server
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()

	if err := s.Stop(stopCtx); err != nil {
		t.Errorf("Stop() error = %v", err)
	}

	// Wait for Start to return
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("Start() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Start() did not return after Stop()")
	}
}

func TestSPAHandler(t *testing.T) {
	// Test that isAPIPath correctly identifies API paths
	tests := []struct {
		path     string
		expected bool
	}{
		{"/api/v1/chat", true},
		{"/api/v1/ws", true},
		{"/api/anything", true},
		{"/webhook/line", true},
		{"/webhook/custom", true},
		{"/", false},
		{"/index.html", false},
		{"/assets/main.js", false},
		{"/chat", false},
	}

	for _, tt := range tests {
		got := isAPIPath(tt.path)
		if got != tt.expected {
			t.Errorf("isAPIPath(%q) = %v, want %v", tt.path, got, tt.expected)
		}
	}
}
