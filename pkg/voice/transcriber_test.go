package voice

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAudioFixture(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "clip.ogg")
	if err := os.WriteFile(path, []byte("fake-ogg-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestTranscriber_APIBaseOverride(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hola","language":"es"}`))
	}))
	defer server.Close()

	tr := NewGroqTranscriber("key", server.URL+"/custom/v1/")
	result, err := tr.Transcribe(context.Background(), writeAudioFixture(t))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if gotPath != "/custom/v1/audio/transcriptions" {
		t.Fatalf("request path = %q, want the configured base to be used verbatim", gotPath)
	}
	if result.Text != "hola" {
		t.Fatalf("Text = %q, want hola", result.Text)
	}
}

func TestTranscriber_DefaultAPIBase(t *testing.T) {
	if got := NewGroqTranscriber("key", "").apiBase; got != defaultGroqAPIBase {
		t.Fatalf("apiBase = %q, want the Groq default %q", got, defaultGroqAPIBase)
	}
	if got := NewGroqTranscriber("key", "  ").apiBase; got != defaultGroqAPIBase {
		t.Fatalf("apiBase = %q for a blank override, want the Groq default %q", got, defaultGroqAPIBase)
	}
}

func TestTranscriber_CapsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"` + strings.Repeat("A", int(maxResponseBytes)+1024) + `"}`))
	}))
	defer server.Close()

	tr := NewGroqTranscriber("key", server.URL)

	if _, err := tr.Transcribe(context.Background(), writeAudioFixture(t)); err == nil {
		t.Fatal("a response larger than the cap was accepted whole")
	} else if !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("error = %v, want it to name the size cap", err)
	}
}

func TestTranscriber_ErrorBodyTruncated(t *testing.T) {
	hugeDetail := strings.Repeat("D", 5000)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"` + hugeDetail + `"}}`))
	}))
	defer server.Close()

	tr := NewGroqTranscriber("key", server.URL)
	logs := captureVoiceLogs(t)

	_, err := tr.Transcribe(context.Background(), writeAudioFixture(t))
	if err == nil {
		t.Fatal("a 400 response was treated as success")
	}

	if strings.Contains(err.Error(), hugeDetail) {
		t.Fatalf("the returned error carries the whole response body")
	}
	if got := len([]rune(err.Error())); got > maxErrorBodyRunes+128 {
		t.Fatalf("returned error carries %d runes, want the body truncated to ~%d", got, maxErrorBodyRunes)
	}

	out := logs()
	if !strings.Contains(out, "API error") {
		t.Fatalf("the API error was not logged: %q", out)
	}
	if strings.Contains(out, hugeDetail) {
		t.Fatalf("the log line carries the whole response body")
	}
	if !strings.Contains(out, strings.Repeat("D", maxErrorBodyRunes/2)) {
		t.Fatalf("the log line lost the beginning of the body: %q", out)
	}
}

func TestTranscriber_LogsPreviewNotFullTranscript(t *testing.T) {
	fullTranscript := strings.TrimSpace(strings.Repeat("palabra ", 400))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"` + fullTranscript + `"}`))
	}))
	defer server.Close()

	tr := NewGroqTranscriber("key", server.URL)
	logs := captureVoiceLogs(t)

	if _, err := tr.Transcribe(context.Background(), writeAudioFixture(t)); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if out := logs(); strings.Contains(out, fullTranscript) {
		t.Fatalf("the whole transcript reached the log")
	}
}

type countingSource struct {
	remaining int
	consumed  int
}

func (c *countingSource) Read(p []byte) (int, error) {
	if c.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > c.remaining {
		n = c.remaining
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	c.remaining -= n
	c.consumed += n
	return n, nil
}

func TestReadCappedBody_StopsAtTheCap(t *testing.T) {
	src := &countingSource{remaining: 4 * maxResponseBytes}

	body, err := readCappedBody(src)
	if err == nil {
		t.Fatalf("a %d-byte body was accepted, want a size error", len(body))
	}
	if !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("error = %v, want it to name the size cap", err)
	}
	if src.consumed > maxResponseBytes+1 {
		t.Fatalf("read %d bytes from a %d-byte body, want at most %d: the whole response was buffered", src.consumed, 4*maxResponseBytes, maxResponseBytes+1)
	}
}

func TestReadCappedBody_PassesThroughUnderTheCap(t *testing.T) {
	src := &countingSource{remaining: 1024}

	body, err := readCappedBody(src)
	if err != nil {
		t.Fatalf("readCappedBody: %v", err)
	}
	if len(body) != 1024 {
		t.Fatalf("read %d bytes, want 1024", len(body))
	}
}
