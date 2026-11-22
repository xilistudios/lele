package channels

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsAllowedWorkspacePath_ExportedWrapperMatchesTheRESTGuard(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	home, homeErr := os.UserHomeDir()

	cases := []string{
		"/tmp/lele-probe/mcp.json",
		"/var/folders/lele-probe/mcp.json",
		"/var/tmp/lele-probe/mcp.json",
		"/lele-probe/mcp.json",
		filepath.Join(cwd, "sub", "mcp.json"),
		"",
	}
	if homeErr == nil && home != "" {
		cases = append(cases, filepath.Join(home, "x", "mcp.json"))
	}
	for _, p := range cases {
		if got, want := IsAllowedWorkspacePath(p), isAllowedWorkspacePath(p); got != want {
			t.Errorf("IsAllowedWorkspacePath(%q) = %v, but the unexported predicate says %v",
				p, got, want)
		}
	}

	probe := filepath.Join("/var/tmp", "lele-probe", "mcp.json")
	if IsAllowedWorkspacePath(probe) {
		wd, _ := os.Getwd()
		t.Fatalf("probe path %q is accepted (cwd=%q home=%q): the TUI guard test would prove nothing on this box",
			probe, wd, home)
	}
}
