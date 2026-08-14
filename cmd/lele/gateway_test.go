package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestParseGatewayFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     string
		desktop bool
		port    int
		debug   bool
	}{
		{name: "desktop flag", args: []string{"--desktop"}, desktop: true, port: -1},
		{name: "no args", args: []string{}, port: -1},
		{name: "port zero", args: []string{"--port", "0"}, port: 0},
		{name: "port and desktop", args: []string{"--port", "1234", "--desktop"}, desktop: true, port: 1234},
		{name: "debug flag", args: []string{"--debug"}, debug: true, port: -1},
		{name: "short debug flag", args: []string{"-d"}, debug: true, port: -1},
		{name: "invalid port ignored", args: []string{"--port", "abc"}, port: -1},
		{name: "desktop env var", args: []string{}, env: "1", desktop: true, port: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("LELE_DESKTOP", tt.env)
			}
			desktop, port, debug := parseGatewayFlags(tt.args)
			if desktop != tt.desktop {
				t.Errorf("desktop = %v, want %v", desktop, tt.desktop)
			}
			if port != tt.port {
				t.Errorf("port = %d, want %d", port, tt.port)
			}
			if debug != tt.debug {
				t.Errorf("debug = %v, want %v", debug, tt.debug)
			}
		})
	}
}

func TestEmitDesktopError(t *testing.T) {
	// Redirect stdout to capture the emitted machine-readable line.
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	emitDesktopError("already_running", map[string]interface{}{"pid": 1234})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	r.Close()

	line := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(line, "LELE_ERROR {") {
		t.Fatalf("output %q does not start with LELE_ERROR {", line)
	}

	payload := line[len("LELE_ERROR "):]
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v (line=%q)", err, line)
	}
	if m["code"] != "already_running" {
		t.Errorf("code = %v, want already_running", m["code"])
	}
	// pid may be a float64 after JSON round-trip.
	if pid, ok := m["pid"].(float64); !ok || pid != 1234 {
		t.Errorf("pid = %v, want 1234", m["pid"])
	}
}
