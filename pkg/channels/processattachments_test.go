package channels

// Tests for the attachment containment boundary enforced by processAttachments.
//
// Incident (2026-10-17): LeleDir traversed a symlink (~/.lele -> /storage/lele),
// so the incoming attachment path resolved to /storage/lele/... while
// absUploadDir stayed /home/alfredo/.lele/... . The check compared a RESOLVED
// path against an UNRESOLVED directory, so every attachment was discarded with
// nothing but a WARN: the agent received the text without the file.
//
// The NativeChannel is built with the same helper pattern as upload_test.go and
// sendfile_view_test.go (see newTestNativeChannel); every path used here lives
// under t.TempDir() so no test depends on the machine's real ~/.lele.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
)

// newTestNativeChannel returns a NativeChannel whose LeleDir is leleDir. It
// reuses newNativeTestServer (upload_test.go / sendfile_view_test.go pattern)
// instead of hand-rolling a second constructor.
func newTestNativeChannel(t *testing.T, leleDir string) *NativeChannel {
	t.Helper()
	ts := newNativeTestServer(t)
	ts.channel.cfg.LeleDir = leleDir
	return ts.channel
}

// symlinkedLeleDir creates a hermetic LeleDir that goes through a symlink:
//
//	<link> -> <real>                       (both inside t.TempDir())
//	<real>/tmp/uploads/                    (the upload staging directory)
//
// It returns the symlinked spelling (<link>, what the client sends), the real
// directory (<real>) and the uploads directory inside the real one.
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

// TestProcessAttachments_AcceptsUploadUnderSymlinkedLeleDir is the regression
// test for the 2026-10-17 incident. Mutations M1 and M5 of §5.5-A turn it red
// (see the mutation table at the end of this file). The path handed to processAttachments is
// the one /api/v1/files/upload returned, i.e. the UNRESOLVED path through the
// symlinked LeleDir — passing the resolved one would not reproduce the bug.
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

// TestProcessAttachments_AcceptsUploadUnderPlainLeleDir is the negative control
// for the test above: the same scenario without a symlink in LeleDir. It passes
// with the old code too, which proves that the symlink is the only difference.
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

// TestProcessAttachments_ContainmentMatrix pins the containment boundary row by
// row (PLAN §9-B). Before the fix S2 was rejected (the incident) and S4 was
// accepted (raw prefix test against a sibling directory); every other row must
// keep its verdict.
//
// The whole tree lives under t.TempDir(): <real> is the real LeleDir and <link>
// is the symlink the client's paths go through.
func TestProcessAttachments_ContainmentMatrix(t *testing.T) {
	link, real, uploads := symlinkedLeleDir(t)
	validPath := filepath.Join(uploads, "abcd1234_valid.txt")
	if err := os.WriteFile(validPath, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	// S4: sibling of the uploads directory, sharing its prefix.
	siblingDir := filepath.Join(real, "tmp", "uploads-evil")
	if err := os.MkdirAll(siblingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	siblingPath := filepath.Join(siblingDir, "evil.txt")
	if err := os.WriteFile(siblingPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// S3: a file that is not under leleDir at all.
	outsidePath := filepath.Join(t.TempDir(), "top-secret.txt")
	if err := os.WriteFile(outsidePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// S6: an existing file outside uploads but inside leleDir, reached through
	// ".." — the escape must be rejected by containment, not by a resolution
	// failure (the plan's "/etc/passwd" spelling would not exist here and would
	// be rejected one branch earlier).
	secretPath := filepath.Join(real, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// S7: symlink inside uploads pointing outside it.
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
				// D3: the returned path is the resolved one, and D4: Temporary
				// must agree with the containment verdict (it decides move vs
				// copy in persistAttachment).
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

// TestProcessAttachments_UnresolvablePathIsLogged covers the S8 row's second
// half (mutation M7 turns it red): a path that cannot be resolved used to be dropped by a silent
// `continue`, leaving no trace anywhere. The WARN must name the path.
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
		logger.SetLogsPath(prevBase) // don't leave a deleted temp dir behind
		logger.SetQuiet(false)
		logger.SetLevel(prevLevel)
	})

	link, _, uploads := symlinkedLeleDir(t)
	n := newTestNativeChannel(t, link)
	missing := filepath.Join(uploads, "does-not-exist.txt")

	if got := n.processAttachments([]string{missing}, "sess"); len(got) != 0 {
		t.Fatalf("got %d attachments for a missing file, want 0", len(got))
	}

	// WARN lines land in errors-<date>.log (INFO goes to info-<date>.log).
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

// --- T1.8 mutation results (PLAN §5.5-A) ---
//
// Every mutation below was applied alone to the fixed tree, compiled (go vet)
// and reverted before the next one; the listed test must turn RED. Measured on
// 2026-10-17 (go1.25.5, linux/arm64), all nine rows executed:
//
// mutation M1 (canonicalContainmentRoot = filepath.Abs, no EvalSymlinks)
//   -> TestProcessAttachments_AcceptsUploadUnderSymlinkedLeleDir FAILS ok
// mutation M2 (resolvedForContainment = filepath.Abs, no resolution)
//   -> TestIsContainedIn_SymlinkedRoot FAILS ok
//   -> TestProcessAttachments_ContainmentMatrix/S7 passes: NOT observable at
//      this boundary, because processAttachments resolves the path itself
//      (filepath.EvalSymlinks) before calling isContainedIn, so the helper's
//      own resolution is a redundant second line of defence there. Kept as
//      documented redundancy (D2), not as a gap in the boundary.
// mutation M3 (isContainedIn via strings.HasPrefix, no Rel)
//   -> TestProcessAttachments_ContainmentMatrix/S4 FAILS ok
//   -> (also TestIsContainedIn_RejectsSiblingPrefix)
// mutation M4 (Rel, but the ".." remainder is not rejected)
//   -> TestIsContainedIn_RejectsParentTraversal FAILS ok
// mutation M5 (Temporary back to strings.HasPrefix(absPath, <lele>/tmp/uploads))
//   -> TestProcessAttachments_AcceptsUploadUnderSymlinkedLeleDir FAILS ok
// mutation M6 (ancestor-walk returns abs instead of Join(resolved, rest))
//   -> TestIsContainedIn_SymlinkedRoot FAILS ok
// mutation M7 (EvalSymlinks failure continues without logging)
//   -> TestProcessAttachments_UnresolvablePathIsLogged FAILS ok
// mutation M8 (handleFileView without canonicalizing the root)
//   -> TestFileView_SymlinkedLeleDirServesUpload FAILS ok
// mutation M9 (stageAttachment without canonicalizing the staging dir)
//   -> TestStageAttachment_IdempotentUnderSymlinkedLeleDir FAILS ok
