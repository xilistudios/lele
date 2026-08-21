package channels

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectMimeType_ByExtension(t *testing.T) {
	cases := map[string]string{
		"a.png":  "image/png",
		"b.jpg":  "image/jpeg",
		"c.jpeg": "image/jpeg",
		"d.gif":  "image/gif",
		"e.webp": "image/webp",
		"f.pdf":  "application/pdf",
		"g.txt":  "text/plain",
		"h.md":   "text/markdown",
		"i.csv":  "text/csv",
		"j.json": "application/json",
	}
	for name, want := range cases {
		if got := detectMimeType(name); got != want {
			t.Errorf("detectMimeType(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestDetectMimeType_UnknownExtAndMissingFile(t *testing.T) {
	// unknown extension + missing file => octet-stream
	if got := detectMimeType("/nonexistent/path.zzz"); got != "application/octet-stream" {
		t.Errorf("missing file = %q", got)
	}
	// no extension, missing file => octet-stream
	if got := detectMimeType("/nonexistent/filename"); got != "application/octet-stream" {
		t.Errorf("no ext missing = %q", got)
	}
	// uppercase extension handled via ToLower
	if got := detectMimeType("x.PNG"); got != "image/png" {
		t.Errorf("uppercase ext = %q", got)
	}
}

func TestDetectMimeType_ContentBased(t *testing.T) {
	dir := t.TempDir()
	// A PNG magic number file saved without a recognized extension.
	png := filepath.Join(dir, "logo.bin")
	if err := os.WriteFile(png, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectMimeType(png); got != "image/png" {
		t.Errorf("content-detect png = %q", got)
	}
}

func TestBoolPtr(t *testing.T) {
	if p := boolPtr(true); p == nil {
		t.Fatal("boolPtr(true) returned nil")
	} else if !*p {
		t.Fatal("boolPtr(true) = false")
	}
	if p := boolPtr(false); p == nil {
		t.Fatal("boolPtr(false) returned nil")
	} else if *p {
		t.Fatal("boolPtr(false) = true")
	}
}

func TestBuildInfoGetters(t *testing.T) {
	oldV, oldC, oldT := systemVersion, systemGitCommit, systemBuildTime
	defer func() { systemVersion, systemGitCommit, systemBuildTime = oldV, oldC, oldT }()

	SetBuildInfo("1.2.3", "abcdef", "2024-01-01T00:00:00Z")
	if currentBuildVersion() != "1.2.3" {
		t.Errorf("version = %q", currentBuildVersion())
	}
	if currentBuildCommit() != "abcdef" {
		t.Errorf("commit = %q", currentBuildCommit())
	}
	if currentBuildTime() != "2024-01-01T00:00:00Z" {
		t.Errorf("time = %q", currentBuildTime())
	}
}

func TestIsAllowedProviderURL_RandomURLs(t *testing.T) {
	// A few extra fuzz-style sanity cases beyond the table test.
	for _, u := range []string{
		"https://10.0.0.1/", "https://172.16.0.1/", "https://192.0.2.1/",
		"HTTPS://api.openai.com/", "https://api.openai.com:443/v1",
		"ftp://api.openai.com/",
	} {
		_ = isAllowedProviderURL(u)
	}
}