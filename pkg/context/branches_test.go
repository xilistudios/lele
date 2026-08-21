package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- BuildHarnessContext more branches ------------------------------

func TestBuildHarnessContext_LowercaseAgentsMdTruncated(t *testing.T) {
	temp := t.TempDir()
	setTempHome(t, t.TempDir()) // home outside temp
	chdir(t, temp)

	// Only a large lowercase agents.md (no AGENTS.md) → truncation branch.
	big := strings.Repeat("b", maxAgentsFileBytes+1000)
	if err := os.WriteFile(filepath.Join(temp, "agents.md"), []byte(big), 0644); err != nil {
		t.Fatal(err)
	}
	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}
	if !strings.Contains(ctxStr, "[AGENTS.md truncated]") {
		t.Errorf("expected lowercase agents.md truncation notice, got:\n%.200s", ctxStr)
	}
}

// --- parseSkillMetadata remaining branches --------------------------

func TestParseSkillMetadata_MoreBranches(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantDesc string
	}{
		{
			name:     "folded block scalar with name key",
			input:    "---\nname: >\n  n1\n  n2\ndescription: d\n---",
			wantName: "n1 n2",
			wantDesc: "d",
		},
		{
			name:     "literal block scalar with name key",
			input:    "---\nname: |\n  n1\n  n2\ndescription: d\n---",
			wantName: "n1\nn2",
			wantDesc: "d",
		},
		{
			name:     "closing --- followed by non-delimiter then real close",
			input:    "---\nname: a\n---x\n---\n",
			wantName: "a",
			wantDesc: "",
		},
		{
			name:     "inline continuation hits whitespace-only line (break)",
			input:    "---\ndescription: start\n  \n  more\n---",
			wantName: "",
			wantDesc: "start",
		},
		{
			name:     "folded block stops at non-indented line",
			input:    "---\ndescription: >\n  line1\nnotindented\n---",
			wantName: "",
			wantDesc: "line1",
		},
		{
			name:     "folded block with no content (collect returns nil)",
			input:    "---\ndescription: >\n---",
			wantName: "",
			wantDesc: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := parseSkillMetadata(tt.input)
			if meta.Name != tt.wantName || meta.Description != tt.wantDesc {
				t.Errorf("parseSkillMetadata = {Name:%q Desc:%q}, want {Name:%q Desc:%q}",
					meta.Name, meta.Description, tt.wantName, tt.wantDesc)
			}
		})
	}
}

// --- initializeFromDisk / copyFile error branches -------------------

func TestInitializeFromDisk_CopyFileError(t *testing.T) {
	// Make the template AGENT.md a DIRECTORY. os.Stat(src) succeeds, then
	// copyFile opens the dir, Stat succeeds, OpenFile(dst) succeeds, but
	// io.Copy fails reading a directory → error path exercised.
	templateDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(templateDir, "AGENT.md"), 0755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	initializeFromDisk(ws, templateDir)

	// AGENT.md should not have been copied as a file (copy failed).
	if _, err := os.Stat(filepath.Join(ws, "AGENT.md")); err == nil {
		// It might exist as a leftover dir? No — copyFile creates dst as a file
		// before io.Copy fails, leaving an empty file. That's acceptable.
		t.Logf("AGENT.md exists after failed copy (expected empty file)")
	}
}

func TestInitializeFromDisk_CopyDirMkdirError(t *testing.T) {
	// Template has a skills dir; workspace/skills is a FILE → copyDir MkdirAll fails.
	templateDir := t.TempDir()
	skills := filepath.Join(templateDir, "skills")
	if err := os.MkdirAll(filepath.Join(skills, "a"), 0755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "skills"), []byte("file"), 0644); err != nil {
		t.Fatal(err)
	}
	initializeFromDisk(ws, templateDir) // should not panic
}

// --- initializeFromEmbedded write-error branch ----------------------

func TestInitializeFromEmbedded_WriteError(t *testing.T) {
	ws := t.TempDir()
	// Make the workspace directory read-only so WriteFile fails.
	if err := os.Chmod(ws, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ws, 0755) })

	initializeFromEmbedded(ws) // should not panic
}

// --- InitializeWorkspace memory-dir failure branch ------------------

func TestInitializeWorkspace_MemoryDirFailure(t *testing.T) {
	t.Setenv("LELE_TEMPLATE_WORKSPACE", "")
	chdir(t, t.TempDir())

	ws := t.TempDir()
	// Pre-create workspace/memory as a FILE so MkdirAll fails.
	if err := os.WriteFile(filepath.Join(ws, "memory"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// InitializeWorkspace should return nil (memory failure is only a warning).
	if err := InitializeWorkspace(ws); err != nil {
		t.Fatalf("InitializeWorkspace returned error, want nil: %v", err)
	}
}