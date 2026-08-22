package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildFakeLele compiles a tiny Go program that prints the given version
// when run as "<binary> version" and exits 0. Returns its binary path.
func buildFakeLele(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	prog := "package main\nimport (\"fmt\";\"os\")\n" +
		"func main(){if len(os.Args)>1 && os.Args[1]==\"version\"{fmt.Println(\"" + version + "\")}\n}\n"
	if err := os.WriteFile(src, []byte(prog), 0644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "lele")
	cmd := exec.Command("go", "build", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake lele: %v (%s)", err, out)
	}
	return bin
}

// tarGz builds a tar.gz archive with a single entry.
func tarGz(t *testing.T, entryName string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: entryName, Mode: 0755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// checksumsText builds the checksums file content for an archive.
func checksumsText(archive []byte, archiveName string) string {
	sum := sha256.Sum256(archive)
	return hex.EncodeToString(sum[:]) + "  " + archiveName + "\n"
}

// marshalReleaseJSON returns the JSON encoding of a Release.
func marshalReleaseJSON(rel Release) string {
	b, _ := json.Marshal(rel)
	return string(b)
}

// newUpdatePipeline wires a full update pipeline against a local HTTP
// server. /release returns the release JSON (with asset URLs pointing
// back to the same server), /archive serves a tar.gz containing the fake
// lele binary, and /checksums serves the SHA256 file.
//
// The returned Checker has an intercepting client returning the /release
// body for any request, so it can be dropped straight into an Updater.
// The Updater's Downloader fetches asset URLs over the real localhost
// network, which is safe.
func newUpdatePipeline(t *testing.T, fakeBin, archiveName, newTag, newVersion string) (*Checker, *httptest.Server) {
	t.Helper()
	content, err := os.ReadFile(fakeBin)
	if err != nil {
		t.Fatal(err)
	}
	archive := tarGz(t, "lele", content)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			rel := Release{
				Tag: newTag,
				Assets: []Asset{
					{Name: archiveName, URL: srv.URL + "/archive"},
					{Name: ChecksumsName(newVersion), URL: srv.URL + "/checksums"},
				},
			}
			w.Write([]byte(marshalReleaseJSON(rel)))
		case "/archive":
			w.Write(archive)
		case "/checksums":
			w.Write([]byte(checksumsText(archive, archiveName)))
		}
	}))

	checker := &Checker{
		Repo: "o/r",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := readURLBody(t, srv.URL+"/release")
			return cannedResponse(req, http.StatusOK, body), nil
		})},
	}
	return checker, srv
}

// readURLBody fetches url and returns the body as a string.
func readURLBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb bytes.Buffer
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if rerr != nil {
			break
		}
	}
	return sb.String()
}
