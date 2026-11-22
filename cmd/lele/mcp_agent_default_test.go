package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMultiAgentConfigFile(t *testing.T, leleDir, defaultsWS, defaultID string, ids ...string) {
	t.Helper()
	agents := make([]string, 0, len(ids))
	for _, id := range ids {
		ws := filepath.Join(leleDir, "workspace-"+id)
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", ws, err)
		}
		agents = append(agents, fmt.Sprintf(`{"id":%q,"workspace":%q,"default":%t}`, id, ws, id == defaultID))
	}
	writeMCPTestFile(t, filepath.Join(leleDir, "config.json"),
		fmt.Sprintf(`{"agents":{"defaults":{"workspace":%q},"list":[%s]}}`,
			defaultsWS, strings.Join(agents, ",")))
}

func TestDefaultMCPAgentID_ImplicitMain(t *testing.T) {
	newMCPTestEnv(t)
	if got := defaultMCPAgentID(); got != "main" {
		t.Fatalf("defaultMCPAgentID() = %q, want %q", got, "main")
	}
}

func TestDefaultMCPAgentID_ConfiguredDefault(t *testing.T) {
	root := t.TempDir()
	leleDir := filepath.Join(root, "lele")
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	t.Chdir(root)
	writeMultiAgentConfigFile(t, leleDir, filepath.Join(root, "shared"), "software-engineer",
		"coder", "software-engineer", "vision")

	if got := defaultMCPAgentID(); got != "software-engineer" {
		t.Fatalf("defaultMCPAgentID() = %q, want %q", got, "software-engineer")
	}
}

func TestDefaultMCPAgentID_UnloadableConfig(t *testing.T) {
	leleDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	writeMCPTestFile(t, filepath.Join(leleDir, "config.json"), `{"agents":`)
	if got := defaultMCPAgentID(); got != "main" {
		t.Fatalf("defaultMCPAgentID() = %q, want the %q fallback", got, "main")
	}
}

func TestMCPList_MultiAgentConfig_Succeeds(t *testing.T) {
	root := t.TempDir()
	leleDir := filepath.Join(root, "lele")
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	t.Chdir(root)
	writeMultiAgentConfigFile(t, leleDir, filepath.Join(root, "shared"), "software-engineer",
		"coder", "software-engineer", "vision")

	agentWS := filepath.Join(leleDir, "workspace-software-engineer")
	writeMCPTestFile(t, filepath.Join(agentWS, "mcp.json"),
		`{"mcpServers":{"alpha":{"command":"alpha-bin"}}}`)

	out, code := runMCPSub(t, runMCPList)
	if code != 0 {
		t.Fatalf("lele mcp list exited %d on a multi-agent config; output:\n%s", code, out)
	}
	if strings.Contains(out, `unknown agent "main"`) {
		t.Fatalf("list still defaulted to %q:\n%s", "main", out)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("list did not read the configured default agent's layer; output:\n%s", out)
	}
}

func TestMCPPathsFor_UnknownAgentNamesTheDefault(t *testing.T) {
	root := t.TempDir()
	leleDir := filepath.Join(root, "lele")
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	t.Chdir(root)
	writeMultiAgentConfigFile(t, leleDir, filepath.Join(root, "shared"), "reviewer",
		"coder", "reviewer")

	_, err := mcpPathsFor("ghost")
	if err == nil {
		t.Fatal("mcpPathsFor(ghost) = nil error, want unknown agent")
	}
	msg := err.Error()
	for _, want := range []string{`unknown agent "ghost"`, "known agents: coder, reviewer", "default: reviewer"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want it to contain %q", msg, want)
		}
	}
}

func TestMCPHelp_NamesConfiguredDefault(t *testing.T) {
	root := t.TempDir()
	leleDir := filepath.Join(root, "lele")
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	t.Chdir(root)
	writeMultiAgentConfigFile(t, leleDir, filepath.Join(root, "shared"), "architect",
		"architect", "coder")

	out, code := runMCPSub(t, runMCP, "--help")
	if code != 0 {
		t.Fatalf("help exited %d", code)
	}
	if !strings.Contains(out, "agent layer (default: architect)") {
		t.Errorf("help does not name the configured default agent:\n%s", out)
	}
}
func TestParseMCPDefaults_ConfiguredDefault(t *testing.T) {
	root := t.TempDir()
	leleDir := filepath.Join(root, "lele")
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	t.Chdir(root)
	writeMultiAgentConfigFile(t, leleDir, filepath.Join(root, "shared"), "reviewer",
		"coder", "reviewer", "planner")

	const want = "reviewer"

	list, err := parseMCPListArgs(nil)
	if err != nil || list.agent != want {
		t.Errorf("list --agent = %q (%v), want %q", list.agent, err, want)
	}
	show, err := parseMCPShowArgs([]string{"x"})
	if err != nil || show.agent != want {
		t.Errorf("show --agent = %q (%v), want %q", show.agent, err, want)
	}
	add, err := parseMCPAddArgs([]string{"x", "--command", "/bin/true"})
	if err != nil || add.agent != want {
		t.Errorf("add --agent = %q (%v), want %q", add.agent, err, want)
	}
	remove, err := parseMCPRemoveArgs([]string{"x"})
	if err != nil || remove.agent != want {
		t.Errorf("remove --agent = %q (%v), want %q", remove.agent, err, want)
	}
	probe, err := parseMCPTestArgs([]string{"x"})
	if err != nil || probe.agent != want {
		t.Errorf("test --agent = %q (%v), want %q", probe.agent, err, want)
	}
	edit, err := parseMCPEditArgs([]string{"x"})
	if err != nil || edit.agent != want {
		t.Errorf("edit --agent = %q (%v), want %q", edit.agent, err, want)
	}
	auth, err := parseMCPAuthArgs([]string{"x"})
	if err != nil || auth.agent != want {
		t.Errorf("auth --agent = %q (%v), want %q", auth.agent, err, want)
	}
	logout, err := parseMCPLogoutArgs([]string{"x"})
	if err != nil || logout.agent != want {
		t.Errorf("logout --agent = %q (%v), want %q", logout.agent, err, want)
	}
}
