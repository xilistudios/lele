package voice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewGroqTranscriber(t *testing.T) {
	t.Run("with api key", func(t *testing.T) {
		tr := NewGroqTranscriber("sk-test")
		if tr == nil {
			t.Fatal("NewGroqTranscriber returned nil")
		}
		if tr.apiKey != "sk-test" {
			t.Errorf("apiKey = %q, want %q", tr.apiKey, "sk-test")
		}
		if tr.apiBase != "https://api.groq.com/openai/v1" {
			t.Errorf("apiBase = %q, want default", tr.apiBase)
		}
		if tr.httpClient == nil {
			t.Error("httpClient should not be nil")
		}
		if tr.httpClient.Timeout != 60*time.Second {
			t.Errorf("httpClient timeout = %v, want 60s", tr.httpClient.Timeout)
		}
	})
	t.Run("empty api key", func(t *testing.T) {
		tr := NewGroqTranscriber("")
		if tr.apiKey != "" {
			t.Errorf("apiKey = %q, want empty", tr.apiKey)
		}
	})
}

func TestIsAvailable(t *testing.T) {
	tests := []struct {
		name   string
		apiKey string
		want   bool
	}{
		{"with key", "sk-123", true},
		{"empty key", "", false},
		{"whitespace key", "   ", true}, // only checks non-empty
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := NewGroqTranscriber(tt.apiKey)
			if got := tr.IsAvailable(); got != tt.want {
				t.Errorf("IsAvailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

// writeAudioFile creates a small sample audio file in a temp dir.
func writeAudioFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.wav")
	if err := os.WriteFile(path, []byte("RIFF fake audio content"), 0644); err != nil {
		t.Fatalf("failed to write audio file: %v", err)
	}
	return path
}

func TestTranscribe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/audio/transcriptions") {
			t.Errorf("path = %s, want /audio/transcriptions", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer sk-test" {
			t.Errorf("auth = %q, want Bearer sk-test", auth)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/form-data") {
			t.Errorf("content-type = %q, want multipart", ct)
		}
		// Verify multipart contains the model field
		body := make([]byte, 0)
		buf := make([]byte, 512)
		for {
			n, err := r.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil {
				break
			}
		}
		if !strings.Contains(string(body), "whisper-large-v3") {
			t.Errorf("multipart body missing model field")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(TranscriptionResponse{
			Text:     "Hello world",
			Language: "en",
			Duration: 1.5,
		})
	}))
	defer srv.Close()

	tr := NewGroqTranscriber("sk-test")
	tr.apiBase = srv.URL

	resp, err := tr.Transcribe(context.Background(), writeAudioFile(t))
	if err != nil {
		t.Fatalf("Transcribe returned error: %v", err)
	}
	if resp.Text != "Hello world" {
		t.Errorf("text = %q, want %q", resp.Text, "Hello world")
	}
	if resp.Language != "en" {
		t.Errorf("language = %q, want en", resp.Language)
	}
	if resp.Duration != 1.5 {
		t.Errorf("duration = %v, want 1.5", resp.Duration)
	}
}

func TestTranscribe_OpenFileError(t *testing.T) {
	tr := NewGroqTranscriber("sk-test")
	_, err := tr.Transcribe(context.Background(), filepath.Join(t.TempDir(), "does-not-exist.wav"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "failed to open audio file") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTranscribe_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("invalid api key"))
	}))
	defer srv.Close()

	tr := NewGroqTranscriber("sk-test")
	tr.apiBase = srv.URL

	_, err := tr.Transcribe(context.Background(), writeAudioFile(t))
	if err == nil {
		t.Fatal("expected error for HTTP 400")
	}
	if !strings.Contains(err.Error(), "API error") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTranscribe_HTTPClientError(t *testing.T) {
	// Bad TLS server that fails during Do
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	tr := NewGroqTranscriber("sk-test")
	tr.apiBase = srv.URL

	_, err := tr.Transcribe(context.Background(), writeAudioFile(t))
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "failed to send request") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTranscribe_CanceledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// keep connection hanging
		select {}
	}))
	defer srv.Close()

	tr := NewGroqTranscriber("sk-test")
	tr.apiBase = srv.URL

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before request

	_, err := tr.Transcribe(ctx, writeAudioFile(t))
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestTranscribe_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{invalid json"))
	}))
	defer srv.Close()

	tr := NewGroqTranscriber("sk-test")
	tr.apiBase = srv.URL

	_, err := tr.Transcribe(context.Background(), writeAudioFile(t))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "failed to unmarshal response") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestTranscribe_InvalidURL exercises the NewRequestWithContext error branch.
func TestTranscribe_InvalidURL(t *testing.T) {
	tr := NewGroqTranscriber("sk-test")
	// A malformed API base produces an invalid URL that fails to parse.
	tr.apiBase = "http://%"

	_, err := tr.Transcribe(context.Background(), writeAudioFile(t))
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
	if !strings.Contains(err.Error(), "failed to create request") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestTranscribe_ReadBodyError exercises the io.ReadAll error branch by sending
// a body whose declared Content-Length exceeds what the server sends, causing
// an unexpected EOF when reading the response body.
func TestTranscribe_ReadBodyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "500")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("short")) // fewer bytes than Content-Length -> unexpected EOF
	}))
	defer srv.Close()

	tr := NewGroqTranscriber("sk-test")
	tr.apiBase = srv.URL

	_, err := tr.Transcribe(context.Background(), writeAudioFile(t))
	if err == nil {
		t.Fatal("expected error reading truncated body")
	}
	if !strings.Contains(err.Error(), "failed to read response") {
		t.Errorf("unexpected error: %v", err)
	}
}
func TestTranscribe_CoverageOfBranchErrors(t *testing.T) {
	// Use a path that opens but stat fails is tricky; use a normal file and a
	// server that reads the body to ensure multipart intermediate branches run.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read body to ensure copy happened, then return success.
		buf := make([]byte, 32*1024)
		for {
			_, err := r.Body.Read(buf)
			if err != nil {
				break
			}
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(TranscriptionResponse{Text: "ok"})
	}))
	defer srv.Close()

	tr := NewGroqTranscriber("sk-test")
	tr.apiBase = srv.URL

	resp, err := tr.Transcribe(context.Background(), writeAudioFile(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "ok" {
		t.Errorf("text = %q, want ok", resp.Text)
	}
}
