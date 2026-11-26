package tools

import (
	"context"
	"encoding/json"
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
		{"127.255.255.254", false},
		{"::1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"172.31.255.255", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"fe80::1", false},
		{"0.0.0.0", false},
		{"::", false},
		{"::127.0.0.1", false},
		{"::ffff:127.0.0.1", false},
		{"224.0.0.1", false},
		{"239.255.255.255", false},
		{"240.0.0.1", false},
		{"255.255.255.255", false},
		{"ff02::1", false},
		{"fc00::1", false},
		{"100.100.100.200", false},
		{"100.64.0.0", false},
		{"100.64.0.1", false},
		{"100.127.255.255", false},
		{"198.18.0.0", false},
		{"198.19.255.255", false},
		{"192.0.0.1", false},
		{"192.0.2.1", false},
		{"198.51.100.1", false},
		{"203.0.113.1", false},
		{"fec0::1", false},
		{"2001:db8::1", false},
		{"64:ff9b::7f00:1", false},
		{"64:ff9b::a9fe:a9fe", false},
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"100.63.255.255", true},
		{"100.128.0.0", true},
		{"198.17.255.255", true},
		{"198.20.0.0", true},
		{"2606:4700::1111", true},
		{"64:ff9b::808:808", true},
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

func redirectRequest(t *testing.T, raw string) *http.Request {
	t.Helper()

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return &http.Request{URL: u}
}

func redirectHops(t *testing.T, n int) []*http.Request {
	t.Helper()

	via := make([]*http.Request, n)
	for i := range via {
		via[i] = redirectRequest(t, "https://example.com/previous")
	}
	return via
}

func TestWebFetch_DefaultBlocksPrivateAndTestSeamAllowsIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("loopback body"))
	}))
	defer server.Close()

	production := NewWebFetchTool(50000)
	if production.allowPrivate {
		t.Fatal("NewWebFetchTool must not enable the private-address seam: production blocks non-public targets")
	}

	res := production.Execute(context.Background(), map[string]interface{}{"url": server.URL})
	if !res.IsError || !strings.Contains(res.ForLLM, "access denied") {
		t.Fatalf("production tool reached a loopback target: error=%v result=%q", res.IsError, res.ForLLM)
	}

	seam := NewWebFetchTool(50000)
	seam.allowPrivate = true
	res = seam.Execute(context.Background(), map[string]interface{}{"url": server.URL})
	if res.IsError {
		t.Fatalf("the test-only allowPrivate seam must let a loopback httptest server answer, got: %s", res.ForLLM)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(res.ForUser), &payload); err != nil {
		t.Fatalf("seam fetch returned an unreadable payload (%v): %q", err, res.ForUser)
	}
	if text, _ := payload["text"].(string); text != "loopback body" {
		t.Fatalf("seam fetch text = %q, want the loopback body: %q", text, res.ForUser)
	}
}

func TestWebFetchRedirectRevalidation(t *testing.T) {
	blocked := []string{
		"http://169.254.169.254/latest/meta-data/iam/security-credentials/",
		"http://127.0.0.1:18790/api/v1/auth/clients",
		"http://10.1.2.3/internal",
		"http://localhost:8080/admin",
		"ftp://example.com/file",
	}
	for _, target := range blocked {
		err := checkFetchRedirect(redirectRequest(t, target), redirectHops(t, 1))
		if err == nil {
			t.Errorf("redirect to %s was accepted, want it refused", target)
			continue
		}
		if !strings.Contains(err.Error(), "access denied") {
			t.Errorf("redirect to %s: error = %v, want it to mention 'access denied'", target, err)
		}
	}

	if err := checkFetchRedirect(redirectRequest(t, "https://example.com/next"), redirectHops(t, 1)); err != nil {
		t.Errorf("redirect to a public URL was refused: %v", err)
	}

	if err := checkFetchRedirect(redirectRequest(t, "https://example.com/next"), redirectHops(t, 5)); err == nil {
		t.Error("sixth redirect was accepted, want the five-hop cap enforced")
	}
}

func TestWebFetchClientWiresRedirectRevalidation(t *testing.T) {
	client := newFetchHTTPClient(false)
	if client.CheckRedirect == nil {
		t.Fatal("fetch client has no CheckRedirect hook")
	}

	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:18790/api/v1/auth/pair",
		"http://100.100.100.200/latest/meta-data/",
	} {
		if err := client.CheckRedirect(redirectRequest(t, target), redirectHops(t, 1)); err == nil {
			t.Errorf("fetch client accepted a redirect to %s", target)
		}
	}

	if err := client.CheckRedirect(redirectRequest(t, "https://example.com/next"), redirectHops(t, 1)); err != nil {
		t.Errorf("fetch client refused a redirect to a public URL: %v", err)
	}

	if err := client.CheckRedirect(redirectRequest(t, "https://example.com/next"), redirectHops(t, maxFetchRedirects)); err == nil {
		t.Errorf("fetch client accepted a %dth redirect hop", maxFetchRedirects+1)
	}
}
