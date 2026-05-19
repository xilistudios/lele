package main

import (
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestParseWebServerOptions_Defaults(t *testing.T) {
	opts := parseWebServerOptions([]string{})

	if opts.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q", opts.Host, "0.0.0.0")
	}
	// Default port comes from EffectiveServerPort() which falls back to
	// Server.Port (default 8080) or legacy Gateway.Port (18790)
	if opts.Port <= 0 {
		t.Errorf("Port = %d, should be > 0", opts.Port)
	}
}

func TestParseWebServerOptions_CustomHost(t *testing.T) {
	opts := parseWebServerOptions([]string{"--host", "127.0.0.1"})

	if opts.Host != "127.0.0.1" {
		t.Errorf("Host = %q, want %q", opts.Host, "127.0.0.1")
	}
	if opts.Port <= 0 {
		t.Errorf("Port = %d, should be > 0", opts.Port)
	}
}

func TestParseWebServerOptions_CustomPort(t *testing.T) {
	opts := parseWebServerOptions([]string{"--port", "8080"})

	if opts.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q", opts.Host, "0.0.0.0")
	}
	if opts.Port != 8080 {
		t.Errorf("Port = %d, want %d", opts.Port, 8080)
	}
}

func TestParseWebServerOptions_BothCustom(t *testing.T) {
	opts := parseWebServerOptions([]string{"--host", "localhost", "--port", "9000"})

	if opts.Host != "localhost" {
		t.Errorf("Host = %q, want %q", opts.Host, "localhost")
	}
	if opts.Port != 9000 {
		t.Errorf("Port = %d, want %d", opts.Port, 9000)
	}
}

func TestParseWebServerOptions_InvalidPort(t *testing.T) {
	opts := parseWebServerOptions([]string{"--port", "invalid"})

	if opts.Port <= 0 {
		t.Errorf("Port should remain default for invalid input, got %d", opts.Port)
	}
}

func TestParseWebServerOptions_NegativePort(t *testing.T) {
	opts := parseWebServerOptions([]string{"--port", "-1"})

	if opts.Port <= 0 {
		t.Errorf("Port should remain default for negative port, got %d", opts.Port)
	}
}

func TestParseWebServerOptions_PortZero(t *testing.T) {
	opts := parseWebServerOptions([]string{"--port", "0"})

	if opts.Port <= 0 {
		t.Errorf("Port should remain default for zero port, got %d", opts.Port)
	}
}

func TestNetJoinHostPort(t *testing.T) {
	result := netJoinHostPort("localhost", 8080)
	expected := "localhost:8080"
	if result != expected {
		t.Errorf("netJoinHostPort() = %q, want %q", result, expected)
	}
}

func TestNetJoinHostPort_IPAddress(t *testing.T) {
	result := netJoinHostPort("192.168.1.1", 3000)
	expected := "192.168.1.1:3000"
	if result != expected {
		t.Errorf("netJoinHostPort() = %q, want %q", result, expected)
	}
}

func TestServeEmbeddedWebApp_Index(t *testing.T) {
	distFS, err := fs.Sub(embeddedFiles, "web/dist")
	if err != nil {
		t.Skip("Embedded web/dist not available in test")
	}
	handler := serveEmbeddedWebApp(distFS)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("GET /: status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("GET /: Content-Type = %q, want %q", ct, "text/html; charset=utf-8")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") && !strings.Contains(body, "<html") {
		t.Errorf("GET /: expected HTML, got %q", body[:min(len(body), 200)])
	}
}

func TestServeEmbeddedWebApp_ExistingFile(t *testing.T) {
	distFS, err := fs.Sub(embeddedFiles, "web/dist")
	if err != nil {
		t.Skip("Embedded web/dist not available in test")
	}
	handler := serveEmbeddedWebApp(distFS)

	req := httptest.NewRequest("GET", "/index.html", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("GET /index.html: status = %d, want 200", rec.Code)
	}
}

func TestServeEmbeddedWebApp_SPAFallback(t *testing.T) {
	distFS, err := fs.Sub(embeddedFiles, "web/dist")
	if err != nil {
		t.Skip("Embedded web/dist not available in test")
	}
	handler := serveEmbeddedWebApp(distFS)

	req := httptest.NewRequest("GET", "/nonexistent-page", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("GET /nonexistent: status = %d, want 200 (SPA fallback)", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("SPA fallback Content-Type = %q, want %q", ct, "text/html; charset=utf-8")
	}
}

func TestServeEmbeddedWebApp_TrailingSlash(t *testing.T) {
	distFS, err := fs.Sub(embeddedFiles, "web/dist")
	if err != nil {
		t.Skip("Embedded web/dist not available in test")
	}
	handler := serveEmbeddedWebApp(distFS)

	req := httptest.NewRequest("GET", "/icons/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Trailing slash should fall back to index.html
	if rec.Code == 200 {
		ct := rec.Header().Get("Content-Type")
		if ct != "text/html; charset=utf-8" {
			t.Errorf("Trailing slash Content-Type = %q, want text/html", ct)
		}
	}
}

func TestServeEmbeddedWebApp_NoEmbeddedFiles(t *testing.T) {
	// Create an empty in-memory filesystem
	emptyFS := fstest.MapFS{}
	handler := serveEmbeddedWebApp(emptyFS)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should return 404 when no files exist
	if rec.Code != 404 {
		t.Errorf("Empty FS: status = %d, want 404", rec.Code)
	}
}
