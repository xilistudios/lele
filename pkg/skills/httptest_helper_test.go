package skills

import (
	"net/http"
	"net/http/httptest"
	"strings"
)

// mockTransport is a RoundTripper that routes all requests to a test server,
// replacing the scheme/host of the request with the test server's address.
// The original transport is retained so it can be used for the actual request.
type mockTransport struct {
	serverURL  string
	originalRT http.RoundTripper
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rebuild the URL to point at the test server while keeping the path/query.
	u := *req.URL
	u.Scheme = "http"
	u.Host = strings.TrimPrefix(m.serverURL, "http://")
	u.Host = strings.TrimPrefix(u.Host, "https://")

	newReq := req.Clone(req.Context())
	newReq.URL = &u

	return m.originalRT.RoundTrip(newReq)
}

// withMockHTTP swaps http.DefaultTransport with a round tripper routing to the
// given httptest.Server, and restores it afterwards. Returns a cleanup function.
func withMockHTTP(server *httptest.Server) func() {
	original := http.DefaultTransport
	http.DefaultTransport = &mockTransport{serverURL: server.URL, originalRT: original}
	return func() {
		http.DefaultTransport = original
	}
}
