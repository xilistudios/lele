package update

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// errBody simulates a body that fails mid-read.
type errBody struct {
	data []byte
	err  error
}

func (e *errBody) Read(p []byte) (int, error) {
	if len(e.data) == 0 {
		return 0, e.err
	}
	n := copy(p, e.data)
	e.data = e.data[n:]
	return n, nil
}

func (e *errBody) Close() error { return nil }

// TestDownloadFile_TransportError covers the client.Do error branch.
func TestDownloadFile_TransportError(t *testing.T) {
	d := &Downloader{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport boom")
	})}}
	dst := filepath.Join(t.TempDir(), "out")
	if err := d.downloadFile(context.Background(), "http://x/a", dst, 0, nil); err == nil {
		t.Fatal("expected transport error")
	}
}

// TestDownloadFile_HTTPStatusError covers the non-200 branch.
func TestDownloadFile_HTTPStatusError(t *testing.T) {
	d := &Downloader{Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return cannedResponse(req, 404, ""), nil
	})}}
	dst := filepath.Join(t.TempDir(), "out")
	if err := d.downloadFile(context.Background(), "http://x/a", dst, 0, nil); err == nil {
		t.Fatal("expected http status error")
	}
}

// TestDownloadFile_CreateError covers the os.Create failure branch.
func TestDownloadFile_CreateError(t *testing.T) {
	d := &Downloader{Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return cannedResponse(req, 200, "data"), nil
	})}}
	dst := filepath.Join(t.TempDir(), "nodir", "out") // parent missing
	if err := d.downloadFile(context.Background(), "http://x/a", dst, 0, nil); err == nil {
		t.Fatal("expected create error")
	}
}

// TestDownloadFile_NonProgress_Error covers the io.Copy error branch when
// progress == nil by using a reading body that fails partway.
func TestDownloadFile_NonProgress_CopyError(t *testing.T) {
	d := &Downloader{Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    200,
			Status:        "200 OK",
			Header:        http.Header{},
			Body:          &errBody{data: []byte("some"), err: errors.New("read boom")},
			ContentLength: 4,
			Request:       req,
		}, nil
	})}}
	dst := filepath.Join(t.TempDir(), "out")
	if err := d.downloadFile(context.Background(), "http://x/a", dst, 4, nil); err == nil {
		t.Fatal("expected copy error")
	}
}

// TestDownloadFile_Progress_WriteError covers the write error inside the
// progress loop by writing to an unwritable destination.
func TestDownloadFile_Progress_WriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; cannot simulate permission-denied writes")
	}
	dir := t.TempDir()
	readonly := filepath.Join(dir, "ro")
	if err := os.Mkdir(readonly, 0555); err != nil {
		t.Fatal(err)
	}
	// Force a short write that fails by making progress non-nil and the
	// destination within a read-only dir.
	d := &Downloader{Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("payload")),
			Request:    req,
		}, nil
	})}}
	dst := filepath.Join(readonly, "out")
	err := d.downloadFile(context.Background(), "http://x/a", dst, 0, func(int64, int64) {})
	if err == nil {
		t.Fatal("expected write error in progress loop")
	}
}

// TestDownloadFile_Progress_ReadError covers the readErr inside the loop.
func TestDownloadFile_Progress_ReadError(t *testing.T) {
	d := &Downloader{Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       &errBody{data: []byte("abc"), err: errors.New("read boom")},
			Request:    req,
		}, nil
	})}}
	dst := filepath.Join(t.TempDir(), "out")
	if err := d.downloadFile(context.Background(), "http://x/a", dst, 1, func(int64, int64) {}); err == nil {
		t.Fatal("expected read error in progress loop")
	}
}

// TestVerifyChecksum_OpenArchiveError covers os.Open failure on archive.
func TestVerifyChecksum_OpenArchiveError(t *testing.T) {
	dir := t.TempDir()
	checksums := writeChecksums(t, dir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  lele.tar.gz\n")
	if err := verifyChecksum(filepath.Join(dir, "missing.tar.gz"), checksums, "lele.tar.gz"); err == nil {
		t.Fatal("expected open archive error")
	}
}
