package server

import (
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// ---------------------------------------------------------------------------
// Start / Serve error paths
// ---------------------------------------------------------------------------

// TestStart_BindError verifies that Start returns an error when the configured
// address is already in use (coverage for the error branch of ListenAndServe).
func TestStart_BindError(t *testing.T) {
	// Occupy a port so ListenAndServe fails with a bind error.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve port: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	s := New(&Config{Host: "127.0.0.1", Port: port})

	if err := s.Start(); err == nil {
		t.Error("Start() expected an error for an in-use port, got nil")
	}
}

// TestServe_ClosedListener verifies that Serve returns an error when the
// underlying listener has already been closed (error branch of Serve).
func TestServe_ClosedListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	ln.Close() // close before serving

	s := New(&Config{Host: "127.0.0.1", Port: 0})
	if err := s.Serve(ln); err == nil {
		t.Error("Serve() expected an error for a closed listener, got nil")
	}
}

// ---------------------------------------------------------------------------
// ActualPort error paths
// ---------------------------------------------------------------------------

// TestActualPort_ErrorPaths covers the error branches of ActualPort: a value
// that cannot be split with SplitHostPort, and a port that is not numeric.
func TestActualPort_ErrorPaths(t *testing.T) {
	t.Run("invalid host-port", func(t *testing.T) {
		s := New(&Config{Host: "127.0.0.1", Port: 3005})
		s.actualAddr = "not-an-addr-no-colon"
		if got := s.ActualPort(); got != 0 {
			t.Errorf("ActualPort() = %d, want 0 for unparsable address", got)
		}
	})

	t.Run("non-numeric port", func(t *testing.T) {
		s := New(&Config{Host: "127.0.0.1", Port: 3005})
		s.actualAddr = "127.0.0.1:abc"
		if got := s.ActualPort(); got != 0 {
			t.Errorf("ActualPort() = %d, want 0 for non-numeric port", got)
		}
	})
}

// ---------------------------------------------------------------------------
// isOriginAllowed URL parse error path
// ---------------------------------------------------------------------------

// TestIsOriginAllowed_ParseError verifies that an origin that fails to parse
// as a URL is rejected.
func TestIsOriginAllowed_ParseError(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 0})

	// An incomplete IPv6 literal causes url.Parse to return an error.
	if s.isOriginAllowed("http://[::1") {
		t.Error("isOriginAllowed() = true, want false for unparsable URL")
	}
}

// ---------------------------------------------------------------------------
// SPA handler (loadIndex + ServeHTTP)
// ---------------------------------------------------------------------------

// spaFS is a custom http.FileSystem whose file Stat always fails. It is used
// to exercise the "Open succeeded but Stat failed" branch of loadIndex.
type statErrorFile struct{}

func (statErrorFile) Close() error { return nil }
func (statErrorFile) Read(p []byte) (int, error) {
	return 0, io.EOF
}
func (statErrorFile) Seek(off int64, whence int) (int64, error) {
	return 0, nil
}
func (statErrorFile) Readdir(count int) ([]fs.FileInfo, error) {
	return nil, nil
}
func (statErrorFile) Stat() (fs.FileInfo, error) {
	return nil, errors.New("stat error")
}

type statErrorFS struct{}

func (statErrorFS) Open(name string) (http.File, error) { return statErrorFile{}, nil }

func TestNewSPAHandler(t *testing.T) {
	h := newSPAHandler(http.Dir("."))
	if h == nil {
		t.Fatal("newSPAHandler returned nil")
	}
	if h.fs == nil {
		t.Error("newSPAHandler did not store the filesystem")
	}
}

func TestLoadIndex(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mfs := fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("hello index")},
		}
		h := newSPAHandler(http.FS(mfs))
		h.loadIndex()

		if !h.loaded {
			t.Error("loadIndex() did not set loaded=true")
		}
		if string(h.index) != "hello index" {
			t.Errorf("index = %q, want %q", string(h.index), "hello index")
		}
	})

	t.Run("open fails", func(t *testing.T) {
		// No index.html in the filesystem -> Open returns an error.
		mfs := fstest.MapFS{}
		h := newSPAHandler(http.FS(mfs))
		h.loadIndex()

		if h.loaded {
			t.Error("loadIndex() should not set loaded=true when Open fails")
		}
		if h.index != nil {
			t.Error("loadIndex() should leave index nil when Open fails")
		}
	})

	t.Run("stat fails", func(t *testing.T) {
		// Open succeeds but Stat errors -> loadIndex should bail out.
		h := newSPAHandler(statErrorFS{})
		h.loadIndex()

		if h.loaded {
			t.Error("loadIndex() should not set loaded=true when Stat fails")
		}
	})
}

func TestSPAHandler_ServeHTTP_APIPath(t *testing.T) {
	s := New(&Config{Host: "127.0.0.1", Port: 0})
	h := newSPAHandler(http.FS(fstest.MapFS{}))

	for _, path := range []string{"/api/v1/chat", "/webhook/line"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.corsMiddleware(h).ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
	}
}

func TestSPAHandler_ServeHTTP_ExactFile(t *testing.T) {
	mfs := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("index content")},
		"main.js":    &fstest.MapFile{Data: []byte("console.log('hi');")},
	}
	h := newSPAHandler(http.FS(mfs))
	s := New(&Config{Host: "127.0.0.1", Port: 0})

	req := httptest.NewRequest(http.MethodGet, "/main.js", nil)
	rec := httptest.NewRecorder()
	s.corsMiddleware(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /main.js status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "console.log('hi');" {
		t.Errorf("body = %q, want %q", got, "console.log('hi');")
	}
}

func TestSPAHandler_ServeHTTP_FileErrorThenIndex(t *testing.T) {
	// No file matches, index.html exists -> index is served.
	mfs := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("SPA index")},
	}
	h := newSPAHandler(http.FS(mfs))
	s := New(&Config{Host: "127.0.0.1", Port: 0})

	req := httptest.NewRequest(http.MethodGet, "/chat", nil)
	rec := httptest.NewRecorder()
	s.corsMiddleware(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /chat status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "SPA index" {
		t.Errorf("body = %q, want %q", got, "SPA index")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestSPAHandler_ServeHTTP_LastResort(t *testing.T) {
	// No index.html and no matching file -> falls back to FileServer.
	mfs := fstest.MapFS{
		"other.txt": &fstest.MapFile{Data: []byte("x")},
	}
	h := newSPAHandler(http.FS(mfs))
	s := New(&Config{Host: "127.0.0.1", Port: 0})

	req := httptest.NewRequest(http.MethodGet, "/chat", nil)
	rec := httptest.NewRecorder()
	s.corsMiddleware(h).ServeHTTP(rec, req)

	// FileServer will 404 since no index.html exists for SPA fallback.
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /chat status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSPAHandler_ServeHTTP_DirectoryPath(t *testing.T) {
	// Requesting a path that maps to a directory should NOT be served as a
	// file; it should fall through to index.html.
	mfs := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("dir index")},
		"sub":        &fstest.MapFile{Mode: fs.ModeDir},
	}
	h := newSPAHandler(http.FS(mfs))
	s := New(&Config{Host: "127.0.0.1", Port: 0})

	req := httptest.NewRequest(http.MethodGet, "/sub", nil)
	rec := httptest.NewRecorder()
	s.corsMiddleware(h).ServeHTTP(rec, req)

	if got := rec.Body.String(); got != "dir index" {
		t.Errorf("body = %q, want %q", got, "dir index")
	}
}
