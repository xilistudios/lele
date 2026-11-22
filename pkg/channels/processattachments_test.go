package channels

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
)

func newTestNativeChannel(t *testing.T, leleDir string) *NativeChannel {
	t.Helper()
	ts := newNativeTestServer(t)
	ts.channel.cfg.LeleDir = leleDir
	return ts.channel
}

func symlinkedLeleDir(t *testing.T) (link, real, uploads string) {
	t.Helper()
	real = t.TempDir()
	link = filepath.Join(t.TempDir(), "lele")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	uploads = filepath.Join(real, "tmp", "uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatal(err)
	}
	return link, real, uploads
}

func TestProcessAttachments_AcceptsUploadUnderSymlinkedLeleDir(t *testing.T) {
	link, _, uploads := symlinkedLeleDir(t)
	const name = "abcd1234_report.txt"
	if err := os.WriteFile(filepath.Join(uploads, name), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	n := newTestNativeChannel(t, link)
	got := n.processAttachments([]string{filepath.Join(link, "tmp", "uploads", name)}, "sess")

	if len(got) != 1 {
		t.Fatalf("attachment rejected through a symlinked LeleDir: got %d, want 1", len(got))
	}
	if !got[0].Temporary {
		t.Errorf("Temporary=false: persistAttachment would copy instead of move, leaking the staging file")
	}
}

func TestProcessAttachments_AcceptsUploadUnderPlainLeleDir(t *testing.T) {
	leleDir := t.TempDir()
	uploads := filepath.Join(leleDir, "tmp", "uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatal(err)
	}
	const name = "abcd1234_report.txt"
	if err := os.WriteFile(filepath.Join(uploads, name), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	n := newTestNativeChannel(t, leleDir)
	got := n.processAttachments([]string{filepath.Join(uploads, name)}, "sess")

	if len(got) != 1 {
		t.Fatalf("attachment rejected under a plain LeleDir: got %d, want 1", len(got))
	}
	if !got[0].Temporary {
		t.Errorf("Temporary=false for an upload under a plain LeleDir")
	}
}

func TestProcessAttachments_ContainmentMatrix(t *testing.T) {
	link, real, uploads := symlinkedLeleDir(t)
	validPath := filepath.Join(uploads, "abcd1234_valid.txt")
	if err := os.WriteFile(validPath, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	siblingDir := filepath.Join(real, "tmp", "uploads-evil")
	if err := os.MkdirAll(siblingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	siblingPath := filepath.Join(siblingDir, "evil.txt")
	if err := os.WriteFile(siblingPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	outsidePath := filepath.Join(t.TempDir(), "top-secret.txt")
	if err := os.WriteFile(outsidePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	secretPath := filepath.Join(real, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	sneakyPath := filepath.Join(uploads, "sneaky.txt")
	if err := os.Symlink(outsidePath, sneakyPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	n := newTestNativeChannel(t, link)

	cases := []struct {
		id     string
		desc   string
		path   string
		accept bool
	}{
		{"S1", "plain path under uploads", validPath, true},
		{"S2", "path through the symlinked LeleDir", filepath.Join(link, "tmp", "uploads", "abcd1234_valid.txt"), true},
		{"S3", "file outside leleDir", outsidePath, false},
		{"S4", "sibling directory sharing the prefix", siblingPath, false},
		{"S5", "dot-dot that normalizes back inside uploads", filepath.Join(uploads, "..", "uploads", "abcd1234_valid.txt"), true},
		{"S6", "traversal escaping uploads", filepath.Join(uploads, "..", "..", "secret.txt"), false},
		{"S7", "symlink inside uploads pointing outside", sneakyPath, false},
		{"S8", "nonexistent file under uploads", filepath.Join(uploads, "does-not-exist.txt"), false},
	}

	for _, tc := range cases {
		t.Run(tc.id+"_"+strings.ReplaceAll(tc.desc, " ", "_"), func(t *testing.T) {
			got := n.processAttachments([]string{tc.path}, "sess")
			if tc.accept {
				if len(got) != 1 {
					t.Fatalf("%s (%s): got %d attachments, want 1", tc.id, tc.desc, len(got))
				}
				if got[0].Path != validPath {
					t.Errorf("%s (%s): Path = %q, want the resolved %q", tc.id, tc.desc, got[0].Path, validPath)
				}
				if !got[0].Temporary {
					t.Errorf("%s (%s): Temporary = false, want true (accepted uploads are staged in place)", tc.id, tc.desc)
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("%s (%s): got %d attachments, want 0 (path must be rejected)", tc.id, tc.desc, len(got))
			}
		})
	}
}

func TestProcessAttachments_UnresolvablePathIsLogged(t *testing.T) {
	logDir := t.TempDir()
	prevLevel := logger.GetLevel()
	prevBase := logger.GetLogsPath()
	logger.SetQuiet(true)
	logger.SetLevel(logger.INFO)
	if err := logger.EnableMultiFileLogging(logDir); err != nil {
		t.Fatalf("EnableMultiFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLogsPath(prevBase)
		logger.SetQuiet(false)
		logger.SetLevel(prevLevel)
	})

	link, _, uploads := symlinkedLeleDir(t)
	n := newTestNativeChannel(t, link)
	missing := filepath.Join(uploads, "does-not-exist.txt")

	if got := n.processAttachments([]string{missing}, "sess"); len(got) != 0 {
		t.Fatalf("got %d attachments for a missing file, want 0", len(got))
	}

	logPath := filepath.Join(logDir, "errors-"+time.Now().Format("2006-01-02")+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read %s: %v", logPath, err)
	}
	content := string(data)
	if !strings.Contains(content, "Attachment path cannot be resolved") {
		t.Errorf("no WARN for the unresolvable path in %s:\n%s", logPath, content)
	}
	if !strings.Contains(content, missing) {
		t.Errorf("WARN does not name the path %q:\n%s", missing, content)
	}
	if !strings.Contains(content, `"level":"WARN"`) {
		t.Errorf("the line must be a WARN:\n%s", content)
	}
}
