package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestServer_Serve_DynamicPort verifies that serving on a listener with a
// dynamically allocated port (0) reports the actual bound port and serves
// requests correctly.
func TestServer_Serve_DynamicPort(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 0})
	s.RegisterHealth()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on dynamic port: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Serve(ln)
	}()
	defer func() {
		s.Stop(context.Background())
		if err := <-errCh; err != nil {
			t.Errorf("Serve returned error: %v", err)
		}
	}()

	// Poll until the actual port is known.
	port := 0
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		port = s.ActualPort()
		if port > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if port == 0 {
		t.Fatal("ActualPort never became > 0 within timeout")
	}

	// Perform an HTTP GET against the /health endpoint.
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	// Read the body and assert it contains "ok".
	buf := new(strings.Builder)
	if _, err := io.Copy(buf, resp.Body); err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	if !strings.Contains(buf.String(), "ok") {
		t.Fatalf("expected body to contain \"ok\", got %q", buf.String())
	}
}

// TestServer_ActualPort_BeforeServe verifies that ActualPort falls back to the
// statically configured port when the server hasn't begun serving on a dynamic
// listener yet.
func TestServer_ActualPort_BeforeServe(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 3005})
	if got := s.ActualPort(); got != 3005 {
		t.Fatalf("expected ActualPort() == 3005, got %d", got)
	}
}
