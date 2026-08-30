package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/config"
)

// --- FIX 1: obVerifyKeyCmd body inspection ---

// newVerifyTestModel builds a minimal model whose config carries a single
// named provider pointing at baseURL, as obVerifyKeyCmd expects. It also
// injects an HTTP client whose transport redirects every request to srv
// (bypassing the localhost skip, since httptest always binds to 127.0.0.1).
func newVerifyTestModel(t *testing.T, baseURL string) *Model {
	t.Helper()
	cfg := testModelConfig(t)
	cfg.Providers.OpenAI = config.OpenAIProviderConfig{ProviderConfig: config.ProviderConfig{APIBase: baseURL, APIKey: "sk-test"}}
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIBase: baseURL, APIKey: "sk-test"},
			Models:         map[string]config.ProviderModelConfig{"gpt-4o": {Model: "gpt-4o"}},
		},
	}
	m := newTestModelWithConfig(t, cfg, true)
	return m
}

// redirectTransport redirects all requests to the given test server while
// keeping the original URL (so the provider's APIBase never looks local).
type redirectTransport struct{ srv *httptest.Server }

func (rt redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(rt.srv.URL, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

// TestObVerifyKeyCmd_ErrorBodyFails verifies a 200 response carrying a JSON
// error object is reported as a validation failure.
func TestObVerifyKeyCmd_ErrorBodyFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error": "invalid api key"}`))
	}))
	defer srv.Close()

	obVerifyHTTPClient = &http.Client{Transport: redirectTransport{srv}}
	defer func() { obVerifyHTTPClient = nil }()
	m := newVerifyTestModel(t, "http://api.example-service.test/v1")
	msg := m.obVerifyKeyCmd()()
	res, ok := msg.(obVerifyResultMsg)
	if !ok {
		t.Fatalf("unexpected msg type %T: %+v", msg, msg)
	}
	if res.success {
		t.Fatalf("success = true, want false for error body")
	}
	if res.err == nil || !strings.Contains(res.err.Error(), "invalid api key") {
		t.Fatalf("err = %v, want it to mention the provider error", res.err)
	}
}

// TestObVerifyKeyCmd_ValidModelsBodySucceeds verifies a 200 response with a
// well-formed models list is reported as success.
func TestObVerifyKeyCmd_ValidModelsBodySucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object": "list", "data": [{"id": "gpt-4o"}]}`))
	}))
	defer srv.Close()

	obVerifyHTTPClient = &http.Client{Transport: redirectTransport{srv}}
	defer func() { obVerifyHTTPClient = nil }()
	m := newVerifyTestModel(t, "http://api.example-service.test/v1")
	msg := m.obVerifyKeyCmd()()
	res, ok := msg.(obVerifyResultMsg)
	if !ok {
		t.Fatalf("unexpected msg type %T: %+v", msg, msg)
	}
	if !res.success {
		t.Fatalf("success = false, want true (err: %v)", res.err)
	}
}

// TestObVerifyKeyCmd_NonJSONBodySucceeds verifies a 200 response with a plain
// (non-JSON) body is tolerated as success.
func TestObVerifyKeyCmd_NonJSONBodySucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("OK - all systems go"))
	}))
	defer srv.Close()

	obVerifyHTTPClient = &http.Client{Transport: redirectTransport{srv}}
	defer func() { obVerifyHTTPClient = nil }()
	m := newVerifyTestModel(t, "http://api.example-service.test/v1")
	msg := m.obVerifyKeyCmd()()
	res, ok := msg.(obVerifyResultMsg)
	if !ok {
		t.Fatalf("unexpected msg type %T: %+v", msg, msg)
	}
	if !res.success {
		t.Fatalf("success = false, want true for non-JSON body (err: %v)", res.err)
	}
}

// TestObVerifyKeyCmd_403Succeeds verifies the documented behavior that a 403
// on /models is treated as a valid key.
func TestObVerifyKeyCmd_403Succeeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	obVerifyHTTPClient = &http.Client{Transport: redirectTransport{srv}}
	defer func() { obVerifyHTTPClient = nil }()
	m := newVerifyTestModel(t, "http://api.example-service.test/v1")
	msg := m.obVerifyKeyCmd()()
	res, ok := msg.(obVerifyResultMsg)
	if !ok {
		t.Fatalf("unexpected msg type %T: %+v", msg, msg)
	}
	if !res.success {
		t.Fatalf("success = false, want true for 403 (err: %v)", res.err)
	}
}

// TestCheckVerifyBody_Table covers checkVerifyBody directly, including the
// error-as-object variants.
func TestCheckVerifyBody_Table(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"empty", "", false},
		{"plain text", "pong", false},
		{"models list", `{"data":[{"id":"m1"}]}`, false},
		{"error string", `{"error":"invalid key"}`, true},
		{"error object message", `{"error":{"message":"bad key","type":"auth"}}`, true},
		{"error object error", `{"error":{"error":"denied"}}`, true},
		{"error empty string", `{"error":""}`, false},
		{"error empty object", `{"error":{}}`, false},
		{"malformed json", `{"error": "unclosed`, false}, // tolerated, not JSON
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkVerifyBody([]byte(tc.body))
			if (err != nil) != tc.wantErr {
				t.Fatalf("checkVerifyBody(%q) = %v, wantErr=%v", tc.body, err, tc.wantErr)
			}
		})
	}
}

// --- FIX 2: clipboard command timeout ---

// TestRunClipboardCmd_Timeout verifies runClipboardCmd returns promptly when
// the underlying command hangs, instead of blocking until the command exits.
func TestRunClipboardCmd_Timeout(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	ok := runClipboardCmd(ctx, "ignored", "sh", "-c", "sleep 5")
	elapsed := time.Since(start)

	if ok {
		t.Fatal("runClipboardCmd = true, want false for killed command")
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("runClipboardCmd took %v, want <2s (context kill)", elapsed)
	}
}

// TestRunClipboardCmd_MissingBinary verifies a command that is not on PATH
// simply reports failure without panicking.
func TestRunClipboardCmd_MissingBinary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if runClipboardCmd(ctx, "x", "definitely-not-a-real-binary-xyz") {
		t.Fatal("runClipboardCmd = true for missing binary, want false")
	}
}

// --- FIX 3: wrapText hard-breaking ---

// maxLineWidth returns the maximum visual width across all lines of s.
func maxLineWidth(s string) int {
	max := 0
	for _, line := range strings.Split(s, "\n") {
		if w := ansi.StringWidth(line); w > max {
			max = w
		}
	}
	return max
}

// TestWrapText_HardBreaksOverwideWord verifies a word wider than the limit is
// split into chunks that each fit within the limit.
func TestWrapText_HardBreaksOverwideWord(t *testing.T) {
	const limit = 10
	got := wrapText("aaaaaaaaaaaaaaaaaaaa", limit) // 20 a's

	if maxLineWidth(got) > limit {
		t.Fatalf("wrapText produced a line wider than %d:\n%s", limit, got)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || lines[0] != "aaaaaaaaaa" || lines[1] != "aaaaaaaaaa" {
		t.Fatalf("expected two 10-char chunks, got %q", got)
	}
}

// TestWrapText_OverwideWordFollowedByShortWord verifies the remainder of a
// hard-broken word stays on the current line and a following short word joins
// it when it fits.
func TestWrapText_OverwideWordFollowedByShortWord(t *testing.T) {
	const limit = 10
	// 13-char word: chunks to 10 + remainder "bbb"; "cc" (2 cols) then joins
	// the remainder ("bbb cc" = 6 cols <= 10).
	got := wrapText("aaaaaaaaaabbb cc", limit)

	if maxLineWidth(got) > limit {
		t.Fatalf("wrapText produced a line wider than %d:\n%s", limit, got)
	}
	if got != "aaaaaaaaaa\nbbb cc" {
		t.Fatalf("got %q, want %q", got, "aaaaaaaaaa\nbbb cc")
	}
}

// TestWrapText_ExactLimitWordNotSplit verifies a word exactly as wide as the
// limit is left intact.
func TestWrapText_ExactLimitWordNotSplit(t *testing.T) {
	got := wrapText("abcdefghij", 10)
	if got != "abcdefghij" {
		t.Fatalf("got %q, want unchanged %q", got, "abcdefghij")
	}
}

// TestWrapText_WideCJKRunesNotSplitMidRune verifies wide (width-2) runes are
// never cut in half and each output line fits the limit.
func TestWrapText_WideCJKRunesNotSplitMidRune(t *testing.T) {
	const limit = 5
	text := "你好世界测试" // 6 CJK runes, width 2 each = 12 columns
	got := wrapText(text, limit)

	if maxLineWidth(got) > limit {
		t.Fatalf("wrapText produced a line wider than %d:\n%s", limit, got)
	}
	// Re-joining the chunks must reproduce the original text exactly.
	if strings.ReplaceAll(got, "\n", "") != text {
		t.Fatalf("chunks lost runes: got %q, want %q", got, text)
	}
}

// TestWrapText_LongURLInSentence verifies a long URL inside a sentence is
// hard-broken and surrounding words still wrap normally.
func TestWrapText_LongURLInSentence(t *testing.T) {
	const limit = 20
	got := wrapText("see https://example.com/very/long/path/that/keeps/going/on forever", limit)

	if maxLineWidth(got) > limit {
		t.Fatalf("wrapText produced a line wider than %d:\n%s", limit, got)
	}
	// All content preserved (modulo wrapping).
	if strings.Count(strings.ReplaceAll(got, "\n", ""), "a") == 0 {
		t.Fatal("content lost during wrapping")
	}
}
