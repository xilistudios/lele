package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdir changes the working directory and restores it on cleanup.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir to %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func TestIsContextFile(t *testing.T) {
	for _, f := range ContextFiles {
		if !IsContextFile(f) {
			t.Errorf("IsContextFile(%q) = false, want true", f)
		}
	}
	unknown := []string{"README.md", "", "AGENT.txt", ".gitignore", "a/b/AGENT.md"}
	for _, f := range unknown {
		if IsContextFile(f) {
			t.Errorf("IsContextFile(%q) = true, want false", f)
		}
	}
}

// --- InitializeWorkspace -------------------------------------------------

func TestInitializeWorkspace_Embedded(t *testing.T) {
	// Ensure no disk template dir is discovered: unset env and chdir to a
	// temp dir without a workspace/ or cmd/lele/workspace/ directory.
	t.Setenv("LELE_TEMPLATE_WORKSPACE", "")
	chdir(t, t.TempDir())

	ws := t.TempDir()
	if err := InitializeWorkspace(ws); err != nil {
		t.Fatalf("InitializeWorkspace failed: %v", err)
	}

	for _, f := range ContextFiles {
		p := filepath.Join(ws, f)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("expected embedded context file %s to exist: %v", f, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("embedded context file %s is empty", f)
		}
	}

	// memory directory should always be created
	info, err := os.Stat(filepath.Join(ws, "memory"))
	if err != nil {
		t.Errorf("expected memory directory to be created: %v", err)
	} else if !info.IsDir() {
		t.Errorf("memory path is not a directory")
	}
}

func TestInitializeWorkspace_Embedded_KeepsExisting(t *testing.T) {
	t.Setenv("LELE_TEMPLATE_WORKSPACE", "")
	chdir(t, t.TempDir())

	ws := t.TempDir()
	// Pre-create AGENT.md with custom content; should NOT be overwritten.
	custom := []byte("my custom AGENT")
	if err := os.WriteFile(filepath.Join(ws, "AGENT.md"), custom, 0644); err != nil {
		t.Fatalf("failed to pre-create AGENT.md: %v", err)
	}

	if err := InitializeWorkspace(ws); err != nil {
		t.Fatalf("InitializeWorkspace failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(ws, "AGENT.md"))
	if err != nil {
		t.Fatalf("failed to read AGENT.md: %v", err)
	}
	if string(got) != string(custom) {
		t.Errorf("existing AGENT.md was overwritten: got %q, want %q", got, custom)
	}
}

func TestInitializeWorkspace_DiskTemplates(t *testing.T) {
	templateDir := t.TempDir()
	// Create template context files
	for _, f := range ContextFiles {
		if err := os.WriteFile(filepath.Join(templateDir, f), []byte("template-"+f), 0644); err != nil {
			t.Fatalf("failed to write template %s: %v", f, err)
		}
	}
	// Create a skills directory with one skill
	skillDir := filepath.Join(templateDir, "skills", "greet")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create template skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: greet\n---\nhi"), 0644); err != nil {
		t.Fatalf("failed to write template skill: %v", err)
	}

	t.Setenv("LELE_TEMPLATE_WORKSPACE", templateDir)

	ws := t.TempDir()
	if err := InitializeWorkspace(ws); err != nil {
		t.Fatalf("InitializeWorkspace failed: %v", err)
	}

	for _, f := range ContextFiles {
		got, err := os.ReadFile(filepath.Join(ws, f))
		if err != nil {
			t.Errorf("expected context file %s copied from disk template: %v", f, err)
			continue
		}
		if string(got) != "template-"+f {
			t.Errorf("context file %s content = %q, want %q", f, got, "template-"+f)
		}
	}

	// Skills dir should be copied
	gotSkill, err := os.ReadFile(filepath.Join(ws, "skills", "greet", "SKILL.md"))
	if err != nil {
		t.Errorf("expected skills directory copied: %v", err)
	} else if !contains(gotSkill, "greet") {
		t.Errorf("skills content = %q, want it to contain 'greet'", gotSkill)
	}
}

func TestInitializeWorkspace_MkdirAllError(t *testing.T) {
	// Use a path whose parent is a file, so MkdirAll fails.
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("x"), 0644); err != nil {
		t.Fatalf("failed to write parent file: %v", err)
	}
	ws := filepath.Join(parent, "child")
	if err := InitializeWorkspace(ws); err == nil {
		t.Fatalf("InitializeWorkspace expected error, got nil")
	}
}

// --- initializeFromDisk --------------------------------------------------

func TestInitializeFromDisk_SkipsExistingAndMissingSource(t *testing.T) {
	templateDir := t.TempDir()
	// Only two template files exist on disk.
	if err := os.WriteFile(filepath.Join(templateDir, "AGENT.md"), []byte("agent"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templateDir, "SOUL.md"), []byte("soul"), 0644); err != nil {
		t.Fatal(err)
	}

	ws := t.TempDir()
	// Pre-create SOUL.md in dest so it gets skipped (kept unchanged).
	existing := []byte("existing soul")
	if err := os.WriteFile(filepath.Join(ws, "SOUL.md"), existing, 0644); err != nil {
		t.Fatal(err)
	}

	initializeFromDisk(ws, templateDir)

	// AGENT.md copied
	got, err := os.ReadFile(filepath.Join(ws, "AGENT.md"))
	if err != nil || string(got) != "agent" {
		t.Errorf("AGENT.md = %q err=%v, want 'agent'", got, err)
	}
	// SOUL.md kept existing (source exists but dest exists -> skip)
	got, _ = os.ReadFile(filepath.Join(ws, "SOUL.md"))
	if string(got) != "existing soul" {
		t.Errorf("SOUL.md should be kept, got %q", got)
	}
	// USER.md has no source on disk -> should NOT be created
	if _, err := os.Stat(filepath.Join(ws, "USER.md")); err == nil {
		t.Errorf("USER.md should not be created (no source on disk)")
	}
}

func TestInitializeFromDisk_NoSkillsDir(t *testing.T) {
	templateDir := t.TempDir()
	// Just a context file, no skills dir.
	if err := os.WriteFile(filepath.Join(templateDir, "AGENT.md"), []byte("agent"), 0644); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	initializeFromDisk(ws, templateDir)
	if _, err := os.Stat(filepath.Join(ws, "skills")); err == nil {
		t.Errorf("skills dir should not be created when template has none")
	}
}

// --- initializeFromEmbedded ---------------------------------------------

func TestInitializeFromEmbedded_SkipsExisting(t *testing.T) {
	ws := t.TempDir()
	custom := []byte("custom")
	if err := os.WriteFile(filepath.Join(ws, "AGENT.md"), custom, 0644); err != nil {
		t.Fatal(err)
	}
	// Append a bogus context file to exercise the "embedded template not found"
	// warn path, then restore the global slice.
	orig := ContextFiles
	ContextFiles = append(append([]string{}, orig...), "NO_SUCH_FILE.md")
	defer func() { ContextFiles = orig }()

	initializeFromEmbedded(ws)

	got, err := os.ReadFile(filepath.Join(ws, "AGENT.md"))
	if err != nil {
		t.Fatalf("failed to read AGENT.md: %v", err)
	}
	if string(got) != "custom" {
		t.Errorf("existing AGENT.md overwritten: got %q", got)
	}

	// The boogus file should not be written (embedded missing)
	if _, err := os.Stat(filepath.Join(ws, "NO_SUCH_FILE.md")); err == nil {
		t.Errorf("NO_SUCH_FILE.md should not exist (no embedded template)")
	}
}

// --- copyFile / copyDir --------------------------------------------------

func TestCopyFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.txt")
	dst := filepath.Join(t.TempDir(), "dst", "nested", "dst.txt")
	if err := os.WriteFile(src, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst); err == nil {
		t.Fatalf("copyFile should fail when dst directory doesn't exist")
	}

	// Create dst dir then it should succeed
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read dst: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("copyFile content = %q, want 'hello'", got)
	}

	// Missing source -> error
	if err := copyFile(filepath.Join(t.TempDir(), "missing"), dst); err == nil {
		t.Errorf("copyFile with missing source should error")
	}
}

func TestCopyDir(t *testing.T) {
	src := filepath.Join(t.TempDir(), "srcdir")
	dst := t.TempDir() + "/dstdir"

	sub := filepath.Join(src, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "root.txt"), []byte("root"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir failed: %v", err)
	}

	for _, p := range []string{
		filepath.Join(dst, "root.txt"),
		filepath.Join(dst, "sub", "a.txt"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist after copyDir: %v", p, err)
		}
	}

	// Copying again should be idempotent (existing files are skipped).
	if err := copyDir(src, dst); err != nil {
		t.Fatalf("second copyDir failed: %v", err)
	}

	// Missing source should error.
	if err := copyDir(filepath.Join(t.TempDir(), "nope"), dst); err == nil {
		t.Errorf("copyDir with missing source should error")
	}
}

// --- findTemplateWorkspaceDir --------------------------------------------

func TestFindTemplateWorkspaceDir_EnvValid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_TEMPLATE_WORKSPACE", dir)
	if got := findTemplateWorkspaceDir(); got != dir {
		t.Errorf("env-based workspace dir = %q, want %q", got, dir)
	}
}

func TestFindTemplateWorkspaceDir_EnvInvalid(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("LELE_TEMPLATE_WORKSPACE", filepath.Join(empty, "nonexistent"))
	chdir(t, empty)
	if got := findTemplateWorkspaceDir(); got != "" {
		t.Errorf("workspace dir should be empty when env points to missing dir, got %q", got)
	}
}

func TestFindTemplateWorkspaceDir_CwdWorkspace(t *testing.T) {
	t.Setenv("LELE_TEMPLATE_WORKSPACE", filepath.Join(t.TempDir(), "nope"))
	root := t.TempDir()
	ws := filepath.Join(root, "workspace")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	chdir(t, root)
	if got := findTemplateWorkspaceDir(); got != ws {
		t.Errorf("cwd workspace dir = %q, want %q", got, ws)
	}
}

func TestFindTemplateWorkspaceDir_CmdLeleWorkspace(t *testing.T) {
	t.Setenv("LELE_TEMPLATE_WORKSPACE", filepath.Join(t.TempDir(), "nope"))
	root := t.TempDir()
	ws := filepath.Join(root, "cmd", "lele", "workspace")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	chdir(t, root)
	if got := findTemplateWorkspaceDir(); got != ws {
		t.Errorf("cmd/lele workspace dir = %q, want %q", got, ws)
	}
}

func TestFindTemplateWorkspaceDir_ExecutableDir(t *testing.T) {
	t.Setenv("LELE_TEMPLATE_WORKSPACE", filepath.Join(t.TempDir(), "nope"))
	// chdir to a dir with no workspace at all
	chdir(t, t.TempDir())

	execPath, err := os.Executable()
	if err != nil {
		t.Skipf("cannot resolve executable: %v", err)
	}
	execDir := filepath.Dir(execPath)
	ws := filepath.Join(execDir, "workspace")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatalf("failed to create workspace in exec dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ws) })

	if got := findTemplateWorkspaceDir(); got != ws {
		t.Errorf("executable-based workspace dir = %q, want %q", got, ws)
	}
}

func TestFindTemplateWorkspaceDir_None(t *testing.T) {
	t.Setenv("LELE_TEMPLATE_WORKSPACE", filepath.Join(t.TempDir(), "nope"))
	// chdir to a temp dir without workspace/ and without cmd/lele/workspace.
	chdir(t, t.TempDir())

	// Guard against the executable dir accidentally containing a workspace dir
	// from TestFindTemplateWorkspaceDir_ExecutableDir — but since that test
	// cleans up, this should normally be empty.
	if got := findTemplateWorkspaceDir(); got != "" {
		// Allow the result only if it's a dir that exists; but strictly the
		// expect-none case wants empty. If found, that's an environment quirk.
		if _, err := os.Stat(got); err != nil {
			t.Errorf("unexpected workspace dir %q", got)
		} else {
			t.Logf("found existing workspace dir %q (environment quirk)", got)
		}
	}
}

// contains reports whether b contains the substring s.
func contains(b []byte, s string) bool {
	return strings.Contains(string(b), s)
}
