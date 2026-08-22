package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// TestApplyReplacements_MultipleMatches exercises the "old_text appears N times"
// error branch in ApplyReplacements (edit_utils.go:194-196) using a regex
// strategy that matches more than once.
func TestApplyReplacements_MultipleMatches(t *testing.T) {
	_, err := ApplyReplacements("foo foo bar", []ReplacementPair{{Old: "foo", New: "x"}}, multiMatchStrategy{})
	if err == nil {
		t.Fatal("expected error when old_text matches multiple times")
	}
	if err.Error() != "old_text appears 2 times: foo" {
		t.Errorf("unexpected error: %v", err)
	}
}

// multiMatchStrategy is a MatchStrategy that reports all occurrences so
// ApplyReplacements hits its multiple-match error branch.
type multiMatchStrategy struct{}

func (multiMatchStrategy) FindMatches(content, old string) []Match {
	var matches []Match
	start := 0
	for {
		idx := indexFrom(content, old, start)
		if idx < 0 {
			break
		}
		matches = append(matches, Match{Start: idx, End: idx + len(old)})
		start = idx + len(old)
	}
	return matches
}

func indexFrom(s, sub string, from int) int {
	for i := from; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestApplyReplacements_Overlap detects the overlapping replacement error
// branch (edit_utils.go:206-209).
func TestApplyReplacements_Overlap(t *testing.T) {
	_, err := ApplyReplacements("alpha beta", []ReplacementPair{
		{Old: "alpha", New: "X"},
		{Old: "alpha beta", New: "Y"},
	}, &ExactMatchStrategy{})
	if err == nil {
		t.Fatal("expected overlap error")
	}
}

// TestReadFileWithEncoding_NotFound exercises the os.ReadFile error branch of
// ReadFileWithEncoding (edit_utils.go:68-70).
func TestReadFileWithEncoding_NotFound(t *testing.T) {
	_, _, err := ReadFileWithEncoding(filepath.Join(t.TempDir(), "missing.txt"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestExecTool_NewExecToolWithConfig_DefaultPatternsWhenEmpty covers the shell
// branch where EnableDenyPatterns is true but CustomDenyPatterns is empty
// (shell.go:127-129) — falls back to default deny patterns.
func TestExecTool_NewExecToolWithConfig_DefaultPatternsWhenEmpty(t *testing.T) {
	cfg := &config.Config{Tools: config.ToolsConfig{Exec: config.ExecConfig{
		EnableDenyPatterns: true,
		CustomDenyPatterns: nil,
	}}}
	tool := NewExecToolWithConfig("/tmp", false, cfg)
	if len(tool.denyPatterns) != len(defaultDenyPatterns) {
		t.Errorf("expected default deny patterns, got %d", len(tool.denyPatterns))
	}
}

// TestApplyReplacements_NotFound exercises the "old_text not found" branch
// (edit_utils.go:191-193).
func TestApplyReplacements_NotFound(t *testing.T) {
	_, err := ApplyReplacements("hello world", []ReplacementPair{{Old: "nope", New: "x"}}, &ExactMatchStrategy{})
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

// TestFilesystem_ValidatePathNilWorkspace covers the workspace=="" early return.
func TestFilesystem_ValidatePathNilWorkspace(t *testing.T) {
	got, err := validatePath("/etc/passwd", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/etc/passwd" {
		t.Errorf("got %q, want /etc/passwd", got)
	}
}

// TestFilesystem_ValidatePath_AbsOutsideWorkspace exercises the restrict
// access-denied branch for an absolute path outside the workspace.
func TestFilesystem_ValidatePath_AbsOutsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	_, err := validatePath("/etc/passwd", ws, true)
	if err == nil {
		t.Fatal("expected access denied for path outside workspace")
	}
}

// TestFilesystem_ValidatePath_NonexistentUnderWorkspace exercises the
// resolveExistingAncestor path for a nested non-existent file.
func TestFilesystem_ValidatePath_NonexistentNested(t *testing.T) {
	ws := t.TempDir()
	got, err := validatePath("sub/dir/file.txt", ws, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = os.IsNotExist(err)
	if got == "" {
		t.Fatal("expected a resolved path")
	}
}
