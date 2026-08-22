package utils

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIsAudioFile(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		contentType string
		want        bool
	}{
		{"mp3 extension", "song.mp3", "", true},
		{"wav extension", "clip.wav", "", true},
		{"ogg extension", "clip.ogg", "", true},
		{"m4a extension", "clip.m4a", "", true},
		{"flac extension", "clip.flac", "", true},
		{"aac extension", "clip.aac", "", true},
		{"wma extension", "clip.wma", "", true},
		{"uppercase extension", "CLIP.MP3", "", true},
		{"audio content type", "clip", "audio/mpeg", true},
		{"audio/ogg content type", "clip", "audio/ogg", true},
		{"application/ogg content type", "clip", "application/ogg", true},
		{"application/x-ogg content type", "clip", "application/x-ogg", true},
		{"uppercase content type", "clip", "AUDIO/mpeg", true},
		{"not audio extension", "file.txt", "", false},
		{"no extension", "file", "", false},
		{"not audio content type", "file", "image/png", false},
		{"empty everything", "", "", false},
		{"audio prefix not content type match", "file.png", "audio", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAudioFile(tc.filename, tc.contentType); got != tc.want {
				t.Errorf("IsAudioFile(%q, %q) = %v, want %v", tc.filename, tc.contentType, got, tc.want)
			}
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple name", "hello.txt", "hello.txt"},
		{"with path", "/etc/passwd", "passwd"},
		{"windows path", `C:\Users\a.txt`, "C:_Users_a.txt"},
		{"with dots", "..", ""},
		{"path traversal", "../../etc/passwd", "passwd"},
		{"empty", "", "."},
		{"just dots", "...", "."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeFilename(tc.in); got != tc.want {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDownloadFile_Success(t *testing.T) {
	content := []byte("downloaded-content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "val" {
			t.Errorf("expected extra header X-Test=val, got %q", r.Header.Get("X-Test"))
		}
		if r.Header.Get("Authorization") != "Bearer abc" {
			t.Errorf("expected Authorization header, got %q", r.Header.Get("Authorization"))
		}
		w.Write(content)
	}))
	defer server.Close()

	got := DownloadFile(server.URL, "audio.mp3", DownloadOptions{
		Timeout: 5 * time.Second,
		ExtraHeaders: map[string]string{
			"X-Test":        "val",
			"Authorization": "Bearer abc",
		},
		LoggerPrefix: "test",
	})
	if got == "" {
		t.Fatal("expected non-empty path")
	}
	defer os.Remove(got)

	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("downloaded content mismatch: got %q want %q", data, content)
	}
	// Filename should be preserved (sanitized) after UUID prefix
	if !strings.HasSuffix(got, "_audio.mp3") {
		t.Errorf("expected filename suffix _audio.mp3, got %q", got)
	}
}

func TestDownloadFile_Non200Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	if got := DownloadFile(server.URL, "missing.txt", DownloadOptions{}); got != "" {
		t.Errorf("expected empty path on non-200, got %q", got)
	}
}

func TestDownloadFile_RequestError(t *testing.T) {
	// Invalid URL triggers http.NewRequest error
	if got := DownloadFile("http://\x00invalid", "x.txt", DownloadOptions{}); got != "" {
		t.Errorf("expected empty path on request error, got %q", got)
	}
}

func TestDownloadFile_ClientError(t *testing.T) {
	// Connection refused server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	badURL := strings.Replace(server.URL, server.Listener.Addr().String(), "127.0.0.1:1", 1)
	server.Close()

	if got := DownloadFile(badURL, "x.txt", DownloadOptions{Timeout: time.Second}); got != "" {
		t.Errorf("expected empty path on client error, got %q", got)
	}
}

func TestDownloadFileSimple(t *testing.T) {
	content := []byte("simple")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer server.Close()

	got := DownloadFileSimple(server.URL, "simple.bin")
	if got == "" {
		t.Fatal("expected non-empty path")
	}
	defer os.Remove(got)
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(data) != "simple" {
		t.Errorf("content mismatch")
	}
}

func TestDownloadFile_WriteFailure(t *testing.T) {
	// Force an os.Create failure by making the media directory read-only.
	// The download succeeds from the server, but Create on the target path fails.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("content-that-will-not-be-written"))
	}))
	defer server.Close()

	// Point TMPDIR at a read-only path so os.TempDir()/mediaDir creation or file creation fails.
	origTmp := os.TempDir()
	defer os.Setenv("TMPDIR", os.Getenv("TMPDIR"))
	os.Setenv("TMPDIR", "/nonexistent_readonly_root")
	_ = origTmp

	got := DownloadFile(server.URL, "x.txt", DownloadOptions{LoggerPrefix: "test"})
	if got != "" {
		t.Errorf("expected empty path on file creation failure, got %q", got)
	}
}
