package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildCSPPinsWebsocketToRequestHost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://192.168.0.171:18790/", nil)
	csp := BuildCSP(r)
	if !strings.Contains(csp, "connect-src 'self' ws://192.168.0.171 wss://192.168.0.171;") {
		t.Fatalf("connect-src not pinned to serving host: %q", csp)
	}
	// The old wildcard form must be gone: it allowed exfiltrating to ANY
	// ws:// endpoint (WEB-M13).
	if strings.Contains(csp, "ws: wss:") {
		t.Fatalf("bare ws:/wss: wildcard survived: %q", csp)
	}
}

func TestBuildCSPStripsPort(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://localhost:3005/", nil)
	csp := BuildCSP(r)
	if !strings.Contains(csp, "ws://localhost wss://localhost") {
		t.Fatalf("port not stripped from connect-src hosts: %q", csp)
	}
	if strings.Contains(csp, "localhost:3005") {
		t.Fatalf("port leaked into connect-src: %q", csp)
	}
}

func TestBuildCSPNoInlineScripts(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.internal/", nil)
	csp := BuildCSP(r)
	scriptPart := ""
	for _, directive := range strings.Split(csp, ";") {
		if strings.HasPrefix(strings.TrimSpace(directive), "script-src") {
			scriptPart = directive
		}
	}
	// script-src MUST NOT carry 'unsafe-inline' — it is the XSS kill switch
	// (WEB-M12/M13). style-src intentionally does (React inline styles).
	if strings.Contains(scriptPart, "unsafe-inline") {
		t.Fatalf("script-src regained 'unsafe-inline': %q", scriptPart)
	}
	if !strings.Contains(csp, "style-src 'self' 'unsafe-inline'") {
		t.Fatalf("style-src lost its required 'unsafe-inline': %q", csp)
	}
}

func TestBuildCSPNoRequestHost(t *testing.T) {
	// Defensive: no host available -> 'self' only, never a broken directive.
	csp := BuildCSP(nil)
	if !strings.Contains(csp, "connect-src 'self';") {
		t.Fatalf("expected 'self'-only connect-src without host: %q", csp)
	}
}

func TestMiddlewareSetsCanonicalHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18790/api/v1/x", nil)
	Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})).ServeHTTP(rec, req)

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "ws://127.0.0.1") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("served CSP is not the canonical request-pinned policy: %q", csp)
	}
}

// Review nit #2: a bare unbracketed IPv6 Host ("::1") must not produce a
// malformed ws://: token — the host is skipped and 'self' covers same-origin.
func TestBuildCSPBareIPv6HostNoMalformedToken(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "::1"
	csp := BuildCSP(r)
	if strings.Contains(csp, "ws://:") {
		t.Errorf("CSP contains malformed ws://: token: %s", csp)
	}
	if !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("expected connect-src 'self' only, got: %s", csp)
	}
}

// Bracketed IPv6 with port must keep the bracketed host and strip only the
// numeric port.
func TestBuildCSPBracketedIPv6PortStripped(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "[::1]:8080"
	csp := BuildCSP(r)
	if !strings.Contains(csp, "ws://[::1] wss://[::1]") {
		t.Errorf("expected bracketed IPv6 host without port, got: %s", csp)
	}
}

// A non-numeric ":suffix" (bare IPv6 tail like "::1234") must NOT be treated
// as a port — stripping it would drop the host or mangle it.
func TestBuildCSPIPv6TailNotStrippedAsPort(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "example.com:8080"
	csp := BuildCSP(r)
	if !strings.Contains(csp, "ws://example.com wss://example.com") {
		t.Errorf("expected port stripped for normal host:port, got: %s", csp)
	}
}
