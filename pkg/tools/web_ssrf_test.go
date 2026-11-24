package tools

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIsPublicIP(t *testing.T) {
	cases := []struct {
		ip     string
		public bool
	}{
		{"127.0.0.1", false},
		{"::1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"fe80::1", false},
		{"0.0.0.0", false},
		{"::ffff:127.0.0.1", false},
		{"8.8.8.8", true},
		{"2606:4700::1111", true},
	}

	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("net.ParseIP(%q) returned nil", tc.ip)
		}
		if got := isPublicIP(ip); got != tc.public {
			t.Errorf("isPublicIP(%s) = %v, want %v", tc.ip, got, tc.public)
		}
	}
}

func TestWebFetchDialRejectsLoopbackResolution(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", server.URL, err)
	}

	client := newFetchHTTPClient(false)
	resp, err := client.Get("http://localhost:" + serverURL.Port() + "/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected the fetch client to refuse a hostname that resolves to loopback")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("error = %v, want it to mention 'access denied'", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("loopback server received %d request(s), want 0", n)
	}
}

func TestWebFetchRedirectRevalidation(t *testing.T) {
	request := func(raw string) *http.Request {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", raw, err)
		}
		return &http.Request{URL: u}
	}
	hops := func(n int) []*http.Request {
		via := make([]*http.Request, n)
		for i := range via {
			via[i] = request("https://example.com/previous")
		}
		return via
	}

	blocked := []string{
		"http://169.254.169.254/latest/meta-data/iam/security-credentials/",
		"http://127.0.0.1:18790/api/v1/auth/clients",
		"http://10.1.2.3/internal",
		"http://localhost:8080/admin",
		"ftp://example.com/file",
	}
	for _, target := range blocked {
		err := checkFetchRedirect(request(target), hops(1))
		if err == nil {
			t.Errorf("redirect to %s was accepted, want it refused", target)
			continue
		}
		if !strings.Contains(err.Error(), "access denied") {
			t.Errorf("redirect to %s: error = %v, want it to mention 'access denied'", target, err)
		}
	}

	if err := checkFetchRedirect(request("https://example.com/next"), hops(1)); err != nil {
		t.Errorf("redirect to a public URL was refused: %v", err)
	}

	if err := checkFetchRedirect(request("https://example.com/next"), hops(5)); err == nil {
		t.Error("sixth redirect was accepted, want the five-hop cap enforced")
	}
}
