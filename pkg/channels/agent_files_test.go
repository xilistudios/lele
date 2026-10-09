package channels

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsAllowedWorkspacePath_ExportedWrapperMatchesTheRESTGuard pins the
// exported face pkg/tui consumes: it must be the SAME predicate the REST
// handlers gate path-bearing requests on (rest_mcp.go mcpGuardLayerFile →
// isAllowedWorkspacePath), not a copy that can drift. The unexported
// predicate's own behaviour and its 12 call sites are untouched by the
// export.
func TestIsAllowedWorkspacePath_ExportedWrapperMatchesTheRESTGuard(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	home, homeErr := os.UserHomeDir()

	cases := []string{
		"/tmp/lele-probe/mcp.json",
		"/var/folders/lele-probe/mcp.json",
		"/var/tmp/lele-probe/mcp.json", // NOT a root: must not match "/tmp/"
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

	// The concrete case the TUI refusal relies on: /var/tmp must be
	// rejected here (cwd is this package under $HOME, home is not /var/tmp).
	probe := filepath.Join("/var/tmp", "lele-probe", "mcp.json")
	if IsAllowedWorkspacePath(probe) {
		wd, _ := os.Getwd()
		t.Fatalf("probe path %q is accepted (cwd=%q home=%q): the TUI guard test would prove nothing on this box",
			probe, wd, home)
	}
}
