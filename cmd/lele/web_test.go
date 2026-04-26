package main

import (
	"testing"
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
