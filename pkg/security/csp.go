// Package security holds HTTP hardening primitives shared by the gateway
// server and channel routers. It exists so the Content-Security-Policy that
// production actually sends has exactly ONE definition (WEB-M13): previously
// pkg/server sent a weaker policy than a dead copy in pkg/channels, and only
// the dead copy was unit-tested.
package security

import (
	"net/http"
	"strings"
)

// BuildCSP returns the Content-Security-Policy for a request.
//
// script-src 'self' (no 'unsafe-inline'): the only previously inline script —
// the theme flash prevention block in web/index.html — was extracted to
// web/public/theme-init.js and is now loaded as an external same-origin
// script, so the XSS kill switch can stay closed. style-src keeps
// 'unsafe-inline' because React components set inline style attributes
// (13 sites under web/src).
//
// connect-src is pinned to the host actually serving the request: the WebUI
// only ever fetches/XHRs same-origin and opens a WebSocket to the same host
// (web/src/services/ws/client.ts derives the ws URL from window.location),
// so a bare `ws: wss:` wildcard is unnecessary and would let any injected
// script exfiltrate to attacker-controlled websocket endpoints.
func BuildCSP(r *http.Request) string {
	hosts := make([]string, 0, 2)
	if r != nil && r.Host != "" {
		h := r.Host
		if i := strings.LastIndex(h, ":"); i > strings.LastIndex(h, "]") {
			h = h[:i] // strip port; keep IPv6 bracket form intact
		}
		hosts = append(hosts, "ws://"+h, "wss://"+h)
	}
	connect := "'self'"
	if len(hosts) > 0 {
		connect = "'self' " + strings.Join(hosts, " ")
	}
	return "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; connect-src " + connect + "; " +
		"font-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
}

// SecurityHeaders sets the hardened response headers for one request.
func SecurityHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-XSS-Protection", "1; mode=block")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Content-Security-Policy", BuildCSP(r))
}

// Middleware returns an http.Handler wrapper applying SecurityHeaders to every
// response. Shared by pkg/server (live) and tests so both exercise the same
// policy that ships.
func Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			SecurityHeaders(w, r)
			next.ServeHTTP(w, r)
		})
	}
}
