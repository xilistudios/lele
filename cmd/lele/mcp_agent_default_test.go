package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression: on a multi-agent config every `lele mcp …` subcommand used to
// default --agent to the literal routing.DefaultAgentID ("main"). "main" only
// exists as an implicit agent when agents.list is EMPTY, so on a config whose
// list is [coder, explorer, …] the very first command failed with
//
//	✗ unknown agent "main" (known agents: coder, explorer, …)
//
// The default must be the configured default agent instead — the same one the
// runtime routes an unscoped message to (routing.ConfiguredDefaultAgentID).

// writeMultiAgentConfigFile writes a config.json whose agents.list has no
// "main" at all and marks <defaultID> as the default.
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

// TestDefaultMCPAgentID_ImplicitMain keeps the single-agent behaviour: with an
// empty agents.list the implicit agent is "main".
func TestDefaultMCPAgentID_ImplicitMain(t *testing.T) {
	newMCPTestEnv(t) // writes a defaults-only config.json
	if got := defaultMCPAgentID(); got != "main" {
		t.Fatalf("defaultMCPAgentID() = %q, want %q", got, "main")
	}
}

// TestDefaultMCPAgentID_ConfiguredDefault is the reported bug: the flag default
// is the configured default agent, not "main".
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

// TestDefaultMCPAgentID_UnloadableConfig: a broken config must not change the
// fallback into something unusable — the subcommand reports the load error with
// its own context, so the flag keeps the historical value.
func TestDefaultMCPAgentID_UnloadableConfig(t *testing.T) {
	leleDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	writeMCPTestFile(t, filepath.Join(leleDir, "config.json"), `{"agents":`) // malformed
	if got := defaultMCPAgentID(); got != "main" {
		t.Fatalf("defaultMCPAgentID() = %q, want the %q fallback", got, "main")
	}
}

// TestMCPList_MultiAgentConfig_Succeeds is the end-to-end shape of the report:
// `lele mcp list` on a config without "main" exits 0 and reads the agent layer
// of the CONFIGURED default agent (not of "main", which does not exist).
func TestMCPList_MultiAgentConfig_Succeeds(t *testing.T) {
	root := t.TempDir()
	leleDir := filepath.Join(root, "lele")
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	t.Chdir(root)
	writeMultiAgentConfigFile(t, leleDir, filepath.Join(root, "shared"), "software-engineer",
		"coder", "software-engineer", "vision")

	// A server in the configured default agent's own workspace must be visible:
	// that proves the wrong layer (defaults/shared) is not being read either.
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

// TestMCPPathsFor_UnknownAgentNamesTheDefault: an explicit --agent nobody has
// keeps failing, but the message now names the agent the command would have used
// on its own, which is the actionable half of the fix.
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

// TestMCPHelp_NamesConfiguredDefault: the --agent help line must not advertise
// "main" on a config that has no such agent.
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
} // TestParseMCPDefaults_ConfiguredDefault pins the actual value on an isolated
// multi-agent config: every --agent flag must default to the configured default
// agent. This is the regression the parser tests above only compare against the
// resolver for.
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
