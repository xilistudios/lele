package channels

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// detectMimeType — extension map and content-based detection branches.
func TestDetectMimeType_Branches(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		write func(t *testing.T, path string)
		want  string
	}{
		{"png ext", "/tmp/x/pic.png", nil, "image/png"},
		{"pdf ext", "/tmp/x/doc.pdf", nil, "application/pdf"},
		{"txt ext", "/tmp/x/note.txt", nil, "text/plain"},
		{"unknown ext missing file", "/tmp/x/noext.zzz", nil, "application/octet-stream"},
		{
			"unknown ext content detect",
			"/tmp/x/blob",
			func(t *testing.T, p string) {
				t.Helper()
				if err := os.WriteFile(p, []byte("\x89PNG\r\n\x1a\nfakeimagebytes"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			"image/png",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, filepath.Base(tc.path))
			if tc.write != nil {
				tc.write(t, p)
			}
			if got := detectMimeType(p); got != tc.want {
				t.Errorf("detectMimeType(%q) = %q, want %q", p, got, tc.want)
			}
		})
	}
}

// handleFileView — error branches.
func TestHandleFileView_Branches(t *testing.T) {
	leleDir := t.TempDir()

	newChannel := func() *NativeChannel {
		cfg := defaultNativeConfigForTest()
		cfg.LeleDir = leleDir
		auth, err := NewAuthManager(&cfg, t.TempDir())
		if err != nil {
			t.Fatalf("NewAuthManager: %v", err)
		}
		return &NativeChannel{cfg: &cfg, auth: auth}
	}

	// Missing path -> 400.
	t.Run("missing path", func(t *testing.T) {
		n := newChannel()
		rec := httptest.NewRecorder()
		n.handleFileView(rec, httptest.NewRequest(http.MethodGet, "/api/v1/files/view", nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	// Path outside leleDir -> 403 access denied.
	t.Run("access denied", func(t *testing.T) {
		n := newChannel()
		outside := filepath.Join(t.TempDir(), "secret.txt")
		if err := os.WriteFile(outside, []byte("s"), 0644); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/files/view?path="+outside, nil)
		n.handleFileView(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
	})

	// File missing within leleDir -> 404.
	t.Run("file not found", func(t *testing.T) {
		n := newChannel()
		missing := filepath.Join(leleDir, "nope.txt")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/files/view?path="+missing, nil)
		n.handleFileView(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	// Path is a directory -> 400.
	t.Run("is directory", func(t *testing.T) {
		n := newChannel()
		dir := filepath.Join(leleDir, "subdir")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/files/view?path="+dir, nil)
		n.handleFileView(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	// Valid file -> 200 with content.
	t.Run("valid file", func(t *testing.T) {
		n := newChannel()
		f := filepath.Join(leleDir, "hello.txt")
		if err := os.WriteFile(f, []byte("hello world"), 0644); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/files/view?path="+f, nil)
		n.handleFileView(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "hello world") {
			t.Errorf("body = %q", rec.Body.String())
		}
	})
}

// defaultNativeConfigForTest returns a NativeConfig with sane defaults for
// isolated handler unit tests.
func defaultNativeConfigForTest() config.NativeConfig {
	c := config.NativeConfig{}
	c.Host = "127.0.0.1"
	c.Port = 0
	c.TokenExpiryDays = 30
	c.PinExpiryMinutes = 5
	c.MaxClients = 5
	c.SessionExpiryDays = 30
	return c
}