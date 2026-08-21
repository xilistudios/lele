package health

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// startTestServer starts the Server's mux directly via httptest and returns it.
func startTestServer(s *Server) *httptest.Server {
	ts := httptest.NewServer(s.server.Handler)
	return ts
}

func TestNewServer(t *testing.T) {
	s := NewServer("127.0.0.1", 8080)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if s.ready {
		t.Errorf("ready should be false initially")
	}
	if s.checks == nil {
		t.Error("checks map should be initialized")
	}
	if s.server.Addr != "127.0.0.1:8080" {
		t.Errorf("addr = %q, want 127.0.0.1:8080", s.server.Addr)
	}
}

func TestHealthHandler(t *testing.T) {
	s := NewServer("127.0.0.1", 0)
	ts := startTestServer(s)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var body StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.Uptime == "" {
		t.Error("uptime should not be empty")
	}
}

func TestReadyHandler_NotReady(t *testing.T) {
	s := NewServer("127.0.0.1", 0)
	ts := startTestServer(s)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ready")
	if err != nil {
		t.Fatalf("GET /ready error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}

	var body StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if body.Status != "not ready" {
		t.Errorf("status = %q, want not ready", body.Status)
	}
}

func TestReadyHandler_Ready(t *testing.T) {
	s := NewServer("127.0.0.1", 0)
	s.SetReady(true)
	s.RegisterCheck("cache", func() (bool, string) { return true, "" })
	ts := startTestServer(s)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ready")
	if err != nil {
		t.Fatalf("GET /ready error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if body.Status != "ready" {
		t.Errorf("status = %q, want ready", body.Status)
	}
	if body.Checks == nil {
		t.Error("checks should be present when ready")
	}
	if ch, ok := body.Checks["cache"]; !ok {
		t.Error("passing check 'cache' should be present")
	} else if ch.Status != "ok" {
		t.Errorf("check status = %q, want ok", ch.Status)
	}
}

func TestReadyHandler_FailedCheck(t *testing.T) {
	s := NewServer("127.0.0.1", 0)
	s.SetReady(true)
	s.RegisterCheck("db", func() (bool, string) { return false, "connection refused" })
	ts := startTestServer(s)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ready")
	if err != nil {
		t.Fatalf("GET /ready error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when a check fails", resp.StatusCode)
	}

	var body StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if body.Status != "not ready" {
		t.Errorf("status = %q, want not ready", body.Status)
	}
	if ch, ok := body.Checks["db"]; !ok {
		t.Error("failed check 'db' should be present in response")
	} else if ch.Status != "fail" || ch.Message != "connection refused" {
		t.Errorf("check = %+v, want status=fail message='connection refused'", ch)
	}
}

func TestRegisterCheck(t *testing.T) {
	s := NewServer("127.0.0.1", 0)
	s.RegisterCheck("cache", func() (bool, string) { return true, "" })

	s.mu.RLock()
	ch, ok := s.checks["cache"]
	s.mu.RUnlock()
	if !ok {
		t.Fatal("check 'cache' not registered")
	}
	if ch.Name != "cache" {
		t.Errorf("name = %q, want cache", ch.Name)
	}
	if ch.Status != "ok" {
		t.Errorf("status = %q, want ok", ch.Status)
	}
	if ch.Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}
}

func TestRegisterCheck_Fail(t *testing.T) {
	s := NewServer("127.0.0.1", 0)
	s.RegisterCheck("disk", func() (bool, string) { return false, "disk full" })

	s.mu.RLock()
	ch, ok := s.checks["disk"]
	s.mu.RUnlock()
	if !ok {
		t.Fatal("check 'disk' not registered")
	}
	if ch.Status != "fail" {
		t.Errorf("status = %q, want fail", ch.Status)
	}
	if ch.Message != "disk full" {
		t.Errorf("message = %q, want 'disk full'", ch.Message)
	}
}

func TestRegisterCheck_Overwrite(t *testing.T) {
	s := NewServer("127.0.0.1", 0)
	s.RegisterCheck("db", func() (bool, string) { return false, "first" })
	s.RegisterCheck("db", func() (bool, string) { return true, "" })

	s.mu.RLock()
	ch, _ := s.checks["db"]
	s.mu.RUnlock()
	if ch.Status != "ok" {
		t.Errorf("status = %q, want overwritten to ok", ch.Status)
	}
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		ok   bool
		want string
	}{
		{true, "ok"},
		{false, "fail"},
	}
	for _, tt := range tests {
		if got := statusString(tt.ok); got != tt.want {
			t.Errorf("statusString(%v) = %q, want %q", tt.ok, got, tt.want)
		}
	}
}

func TestStartContext_Cancel(t *testing.T) {
	s := NewServer("127.0.0.1", 0)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := s.StartContext(ctx)
	if err != nil {
		t.Fatalf("StartContext should return nil on cancel/shutdown, got: %v", err)
	}
	// After StartContext, ready should be true (set before waiting).
	s.mu.RLock()
	ready := s.ready
	s.mu.RUnlock()
	if !ready {
		t.Error("ready should be true after StartContext")
	}
}

func TestStop(t *testing.T) {
	s := NewServer("127.0.0.1", 0)
	// Stop on a never-started server should return nil (shuts down gracefully).
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	s.mu.RLock()
	ready := s.ready
	s.mu.RUnlock()
	if ready {
		t.Error("ready should be false after Stop")
	}
}

func TestStart_ListenError(t *testing.T) {
	// Occupy a port first so ListenAndServe fails with "address already in use".
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open blocking listener: %v", err)
	}
	defer blocker.Close()

	port := blocker.Addr().(*net.TCPAddr).Port
	s := NewServer("127.0.0.1", port)

	if err := s.Start(); err == nil {
		t.Fatal("Start should return an error when the port is already in use")
	}
	// Start sets ready to true before attempting to listen.
	s.mu.RLock()
	if !s.ready {
		t.Error("ready should be true after Start (even on failure)")
	}
	s.mu.RUnlock()
}

func TestStartContext_LiveServer(t *testing.T) {
	// Start from a goroutine; using port 0 binds an ephemeral port but the
	// actual addr isn't exposed via the Server, so use the mux handler directly
	// (already covered). This test ensures StartContext returns a shutdown error
	// if the listener fails outright.
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open blocking listener: %v", err)
	}
	defer blocker.Close()
	port := blocker.Addr().(*net.TCPAddr).Port
	s2 := NewServer("127.0.0.1", port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = s2.StartContext(ctx)
	if err == nil {
		t.Fatal("StartContext should return an error when listen fails")
	}
}
