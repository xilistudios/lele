package security

import (
	"net/http"
	"strings"
)

func BuildCSP(r *http.Request) string {
	hosts := make([]string, 0, 2)
	if r != nil && r.Host != "" {
		h := r.Host
		if i := strings.LastIndex(h, ":"); i > strings.LastIndex(h, "]") {
			if port := h[i+1:]; port != "" && isAllDigits(port) {
				h = h[:i]
			}
		}
		if isSafeHostToken(h) && (!strings.Contains(h, ":") || strings.HasPrefix(h, "[")) {
			hosts = append(hosts, "ws://"+h, "wss://"+h)
		}
	}
	connect := "'self'"
	if len(hosts) > 0 {
		connect = "'self' " + strings.Join(hosts, " ")
	}
	return "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; connect-src " + connect + "; " +
		"font-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
}

func SecurityHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-XSS-Protection", "0")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Content-Security-Policy", BuildCSP(r))
}

func Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			SecurityHeaders(w, r)
			next.ServeHTTP(w, r)
		})
	}
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func isSafeHostToken(h string) bool {
	if h == "" {
		return false
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '-' || c == '_' || c == ':' || c == '[' || c == ']':
		default:
			return false
		}
	}
	return true
}
