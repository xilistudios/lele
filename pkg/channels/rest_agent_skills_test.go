// Tests for the per-agent skills endpoints (rest_agent_skills.go).
//
// The central risk these tests target is the bug the feature fixes: a skill
// request for agent X must never be answered from the DEFAULT workspace loader.
// So the tests run two agents with two real on-disk workspaces and assert that
// list / toggle / remove each touch only the addressed agent's directory.

package channels

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/skills"
)

// authedBodyRequest builds a request with a JSON body and bearer token
// (newAuthedRequest in rest_agent_test.go is bodyless, which install/toggle
// endpoints cannot use).
func authedBodyRequest(t *testing.T, method, url, token, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest(%s %s): %v", method, url, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// skillsTestLoop is a fake agent loop that also implements agentSkillsSource,
// handing each agent its own real loader over a temp workspace. invalidations
// records which agent had its prompt cache dropped, proving mutations bust the
// cache of the RIGHT agent.
type skillsTestLoop struct {
	*nativeTestAgentLoop
	loaders       map[string]*skills.SkillsLoader
	invalidations []string
	extraAgents   map[string]bool // agentIDs that exist but have no loader (unknown-builder case)
}

// GetAgentInfo widens the base fake (which only knows "main") so the agents
// this test creates pass the handler's existence check.
func (l *skillsTestLoop) GetAgentInfo(agentID string) (AgentBasicInfo, bool) {
	if _, ok := l.loaders[agentID]; ok {
		return AgentBasicInfo{ID: agentID, Name: agentID, Workspace: l.loaders[agentID].WorkspaceDir()}, true
	}
	if l.extraAgents[agentID] {
		return AgentBasicInfo{ID: agentID, Name: agentID}, true
	}
	return l.nativeTestAgentLoop.GetAgentInfo(agentID)
}

// ListAgentTools mirrors GetAgentInfo so the catalog endpoint (which needs
// both) recognises the same set of agents.
func (l *skillsTestLoop) ListAgentTools(agentID string) ([]AgentToolInfo, bool) {
	if _, ok := l.loaders[agentID]; ok || l.extraAgents[agentID] {
		return []AgentToolInfo{{Name: "exec", Description: "Execute a shell command"}}, true
	}
	return l.nativeTestAgentLoop.ListAgentTools(agentID)
}

func (l *skillsTestLoop) AgentSkills(agentID string) (loader *skills.SkillsLoader, invalidate func(), ok bool) {
	if _, exists := l.loaders[agentID]; exists {
		loader := l.loaders[agentID]
		return loader, func() { l.invalidations = append(l.invalidations, agentID) }, true
	}
	if l.extraAgents[agentID] {
		// Agent exists but exposes no context builder -> caller must fall back.
		return nil, nil, false
	}
	return nil, nil, false
}

// newSkillsTestServer builds a server whose loop knows agents "alpha" and
// "beta", each with its own workspace, plus a channel-level loader anchored on
// a THIRD directory (the default workspace). Any response that mentions the
// default workspace's skills proves the fallback leaked into an agent request.
func newSkillsTestServer(t *testing.T) (*nativeTestServer, *skillsTestLoop, map[string]string) {
	t.Helper()

	defaultWorkspace := t.TempDir()
	writeTestSkill(t, defaultWorkspace, "default-only", "lives in the default workspace")

	alpha := t.TempDir()
	writeTestSkill(t, alpha, "alpha-skill", "alpha private")
	beta := t.TempDir()
	writeTestSkill(t, beta, "beta-skill", "beta private")
	writeTestSkill(t, beta, "other-beta-skill", "beta private 2")

	ts := newNativeTestServer(t)
	// The channel's own loader models the pre-feature state: default workspace.
	ts.channel.skillsLoader = skills.NewSkillsLoader(defaultWorkspace, "", "")

	loop := &skillsTestLoop{
		nativeTestAgentLoop: ts.loop,
		loaders: map[string]*skills.SkillsLoader{
			"alpha": skills.NewSkillsLoader(alpha, "", ""),
			"beta":  skills.NewSkillsLoader(beta, "", ""),
		},
		extraAgents: map[string]bool{"builderless": true},
	}
	// GetAgentInfo in the base fake only knows "main"; widen it for this test.
	loop.nativeTestAgentLoop.workspace = defaultWorkspace
	ts.channel.agentLoop = loop
	ts.loop = loop.nativeTestAgentLoop

	return ts, loop, map[string]string{"alpha": alpha, "beta": beta, "default": defaultWorkspace}
}

// agentCatalogPayload mirrors GET /api/v1/agents/{id}/catalog — the single read
// endpoint of this feature (it carries skills with source/enabled/deletable, so
// no separate list route is needed).
type agentCatalogPayload struct {
	AgentID   string              `json:"agent_id"`
	Workspace string              `json:"workspace"`
	Skills    []AgentCatalogSkill `json:"skills"`
}

func getAgentCatalog(t *testing.T, ts *nativeTestServer, agentID string) (int, agentCatalogPayload) {
	t.Helper()
	resp, err := http.DefaultClient.Do(newAuthedRequest(t, http.MethodGet,
		ts.server.URL+"/api/v1/agents/"+agentID+"/catalog", ts.token))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	var payload agentCatalogPayload
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("Decode() error = %v body=%s", err, body)
		}
	}
	return resp.StatusCode, payload
}

func skillNames(infos []AgentCatalogSkill) []string {
	names := make([]string, 0, len(infos))
	for _, i := range infos {
		names = append(names, i.Name)
	}
	return names
}

// --- listing ---------------------------------------------------------------

// TestAgentCatalogSkills_ArePerAgent is the core regression test: listing agent
// alpha's skills must return alpha's workspace skills only, never the default
// workspace's (which is what the endpoint returned before this feature).
func TestAgentCatalogSkills_ArePerAgent(t *testing.T) {
	ts, _, dirs := newSkillsTestServer(t)

	status, payload := getAgentCatalog(t, ts, "alpha")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if payload.Workspace != dirs["alpha"] {
		t.Fatalf("workspace = %q, want agent's own %q", payload.Workspace, dirs["alpha"])
	}
	got := strings.Join(skillNames(payload.Skills), ",")
	if got != "alpha-skill" {
		t.Fatalf("alpha skills = %q, want only alpha-skill (default workspace leaked)", got)
	}
	if payload.Skills[0].Source != "workspace" || !payload.Skills[0].Deletable {
		t.Fatalf("workspace skill should be deletable, got %+v", payload.Skills[0])
	}
}

// TestAgentCatalogSkills_UnknownAgentIs404 keeps the existence check consistent
// with the rest of the /agents/{id} family.
func TestAgentCatalogSkills_UnknownAgentIs404(t *testing.T) {
	ts, _, _ := newSkillsTestServer(t)

	status, _ := getAgentCatalog(t, ts, "ghost")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

// TestAgentCatalogSkills_FallsBackToChannelLoader covers a loop without the
// optional capability: the request must still answer (with the channel's
// loader) rather than 500, preserving the old behaviour for other builds.
func TestAgentCatalogSkills_FallsBackToChannelLoader(t *testing.T) {
	ts := newNativeTestServer(t)
	defaultWorkspace := t.TempDir()
	writeTestSkill(t, defaultWorkspace, "default-only", "lives in the default workspace")
	ts.channel.skillsLoader = skills.NewSkillsLoader(defaultWorkspace, "", "")

	status, payload := getAgentCatalog(t, ts, "main")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if got := strings.Join(skillNames(payload.Skills), ","); got != "default-only" {
		t.Fatalf("fallback skills = %q, want default-only", got)
	}
}

// TestAgentCatalogSkills_DeletableFalseForGlobalSkills proves the server, not
// the client, decides what may be removed: a global skill visible to the agent
// is listed but flagged non-deletable.
func TestAgentCatalogSkills_DeletableFalseForGlobalSkills(t *testing.T) {
	ts, loop, dirs := newSkillsTestServer(t)

	globalDir := t.TempDir()
	writeTestSkill(t, globalDir, "shared-global", "shared with everyone")
	loop.loaders["gamma"] = skills.NewSkillsLoader(dirs["alpha"], filepath.Join(globalDir, "skills"), "")
	// gamma must be a known agent for the handler's existence check.
	loop.extraAgents["gamma"] = true

	status, payload := getAgentCatalog(t, ts, "gamma")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	byName := map[string]AgentCatalogSkill{}
	for _, s := range payload.Skills {
		byName[s.Name] = s
	}
	g, ok := byName["shared-global"]
	if !ok {
		t.Fatalf("global skill missing from listing: %+v", payload.Skills)
	}
	if g.Deletable {
		t.Fatal("global skill must not be deletable through an agent")
	}
	if g.Source != "global" {
		t.Fatalf("global skill source = %q, want global", g.Source)
	}
	if a, ok := byName["alpha-skill"]; !ok || !a.Deletable {
		t.Fatalf("workspace skill must be deletable: %+v", payload.Skills)
	}
}

// --- toggling --------------------------------------------------------------

// readWorkspaceDisabled reads <workspace>/skills-skills.json (the per-workspace
// skills config) and returns the disabled list, proving on disk which workspace
// a toggle actually wrote.
func readWorkspaceDisabled(t *testing.T, workspace string) []string {
	t.Helper()
	cfg, err := skills.LoadWorkspaceConfig(workspace)
	if err != nil {
		t.Fatalf("LoadWorkspaceConfig(%s): %v", workspace, err)
	}
	return cfg.Disabled
}

func toggleAgentSkill(t *testing.T, ts *nativeTestServer, agentID, skill string, enabled bool) int {
	t.Helper()
	body := fmt.Sprintf(`{"enabled":%t}`, enabled)
	resp, err := http.DefaultClient.Do(authedBodyRequest(t, http.MethodPut,
		ts.server.URL+"/api/v1/agents/"+agentID+"/skills/"+skill+"/toggle", ts.token, body))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestAgentSkillToggle_WritesAgentWorkspaceOnly is the write-path counterpart
// of the per-agent listing test: disabling a skill on beta must land in beta's
// config file and leave alpha's untouched.
func TestAgentSkillToggle_WritesAgentWorkspaceOnly(t *testing.T) {
	ts, _, dirs := newSkillsTestServer(t)

	if status := toggleAgentSkill(t, ts, "beta", "beta-skill", false); status != http.StatusOK {
		t.Fatalf("toggle off status = %d, want 200", status)
	}

	disabled := strings.Join(readWorkspaceDisabled(t, dirs["beta"]), ",")
	if disabled != "beta-skill" {
		t.Fatalf("beta disabled = %q, want beta-skill", disabled)
	}
	if alphaDisabled := readWorkspaceDisabled(t, dirs["alpha"]); len(alphaDisabled) != 0 {
		t.Fatalf("alpha config was written by a beta toggle: %v", alphaDisabled)
	}

	// Re-enabling removes it from the disabled list again.
	if status := toggleAgentSkill(t, ts, "beta", "beta-skill", true); status != http.StatusOK {
		t.Fatalf("toggle on status = %d, want 200", status)
	}
	if alphaDisabled := readWorkspaceDisabled(t, dirs["beta"]); len(alphaDisabled) != 0 {
		t.Fatalf("beta still disabled after re-enable: %v", alphaDisabled)
	}
}

// TestAgentSkillToggle_InvalidatesAgentPromptCache guards the second half of
// the fix: without invalidation the toggle would only take effect after a
// restart, because the <skills> block is baked into a cached prompt.
func TestAgentSkillToggle_InvalidatesAgentPromptCache(t *testing.T) {
	ts, loop, _ := newSkillsTestServer(t)

	if status := toggleAgentSkill(t, ts, "beta", "beta-skill", false); status != http.StatusOK {
		t.Fatalf("toggle status = %d, want 200", status)
	}
	if len(loop.invalidations) != 1 || loop.invalidations[0] != "beta" {
		t.Fatalf("invalidations = %v, want exactly [beta]", loop.invalidations)
	}
}

// TestAgentSkillToggle_UnknownAgentIs404 ensures a typo in the agent id cannot
// silently write the default workspace's config.
func TestAgentSkillToggle_UnknownAgentIs404(t *testing.T) {
	ts, _, dirs := newSkillsTestServer(t)

	if status := toggleAgentSkill(t, ts, "ghost", "beta-skill", false); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	if d := readWorkspaceDisabled(t, dirs["default"]); len(d) != 0 {
		t.Fatalf("unknown agent wrote the default workspace config: %v", d)
	}
}

// --- removing --------------------------------------------------------------

func removeAgentSkill(t *testing.T, ts *nativeTestServer, agentID, skill string) int {
	t.Helper()
	resp, err := http.DefaultClient.Do(newAuthedRequest(t, http.MethodDelete,
		ts.server.URL+"/api/v1/agents/"+agentID+"/skills/"+skill, ts.token))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestAgentSkillRemove_DeletesFromAgentWorkspaceOnly deletes through beta and
// asserts alpha's skill survives: the whole point of a per-agent remove.
func TestAgentSkillRemove_DeletesFromAgentWorkspaceOnly(t *testing.T) {
	ts, loop, dirs := newSkillsTestServer(t)

	if status := removeAgentSkill(t, ts, "beta", "beta-skill"); status != http.StatusOK {
		t.Fatalf("remove status = %d, want 200", status)
	}
	if _, err := os.Stat(filepath.Join(dirs["beta"], "skills", "beta-skill")); !os.IsNotExist(err) {
		t.Fatalf("beta-skill still on disk after removal: %v", err)
	}
	// The sibling skill of the same agent survives.
	if _, err := os.Stat(filepath.Join(dirs["beta"], "skills", "other-beta-skill")); err != nil {
		t.Fatalf("other-beta-skill was collateral damage: %v", err)
	}
	// Another agent's workspace is untouched.
	if _, err := os.Stat(filepath.Join(dirs["alpha"], "skills", "alpha-skill")); err != nil {
		t.Fatalf("alpha's skill must survive a beta removal: %v", err)
	}
	// The removal busts the prompt cache of the agent that changed, so the next
	// turn no longer advertises the deleted skill.
	if len(loop.invalidations) != 1 || loop.invalidations[0] != "beta" {
		t.Fatalf("invalidations = %v, want exactly [beta]", loop.invalidations)
	}
}

// TestAgentSkillRemove_RefusesGlobalSkill covers the shared-state hazard: a
// global skill visible to the agent must not be deletable through it.
func TestAgentSkillRemove_RefusesGlobalSkill(t *testing.T) {
	ts, loop, dirs := newSkillsTestServer(t)

	globalDir := t.TempDir()
	writeTestSkill(t, globalDir, "shared-global", "shared")
	loop.loaders["alpha"] = skills.NewSkillsLoader(dirs["alpha"], filepath.Join(globalDir, "skills"), "")

	if status := removeAgentSkill(t, ts, "alpha", "shared-global"); status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	if _, err := os.Stat(filepath.Join(globalDir, "skills", "shared-global")); err != nil {
		t.Fatalf("global skill was deleted despite 403: %v", err)
	}
}

// TestAgentSkillRemove_UnknownSkillIs404 distinguishes "not yours" from
// "not allowed".
func TestAgentSkillRemove_UnknownSkillIs404(t *testing.T) {
	ts, _, _ := newSkillsTestServer(t)

	if status := removeAgentSkill(t, ts, "alpha", "nope"); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

// --- install scope ---------------------------------------------------------

// TestInstallTarget covers scope resolution without touching the network: the
// workspace scope must write into the AGENT's dir and global into the shared
// dir, which is the difference between "install for this agent" and "install
// for everyone".
func TestInstallTarget(t *testing.T) {
	agentWorkspace := t.TempDir()
	globalDir := filepath.Join(t.TempDir(), "skills")
	loader := skills.NewSkillsLoader(agentWorkspace, globalDir, "")

	t.Run("default is workspace", func(t *testing.T) {
		installer, scope, bad := installTarget(loader, "")
		if bad != "" {
			t.Fatalf("unexpected error %q", bad)
		}
		if scope != workspaceSkillScope {
			t.Fatalf("scope = %q, want %q", scope, workspaceSkillScope)
		}
		if installer.Workspace() != agentWorkspace {
			t.Fatalf("installer workspace = %q, want %q", installer.Workspace(), agentWorkspace)
		}
	})

	t.Run("global targets shared dir parent", func(t *testing.T) {
		installer, scope, bad := installTarget(loader, globalSkillScope)
		if bad != "" {
			t.Fatalf("unexpected error %q", bad)
		}
		if scope != globalSkillScope {
			t.Fatalf("scope = %q, want %q", scope, globalSkillScope)
		}
		// The installer appends "skills" itself, so it must be handed the parent.
		if installer.Workspace() != filepath.Dir(globalDir) {
			t.Fatalf("installer workspace = %q, want %q", installer.Workspace(), filepath.Dir(globalDir))
		}
	})

	t.Run("unknown scope is rejected", func(t *testing.T) {
		installer, _, bad := installTarget(loader, "somewhere")
		if bad == "" {
			t.Fatal("expected error for unknown scope")
		}
		if installer != nil {
			t.Fatal("no installer must be built for an invalid scope")
		}
		if !strings.Contains(bad, "workspace") || !strings.Contains(bad, "global") {
			t.Fatalf("error should name the valid scopes, got %q", bad)
		}
	})

	t.Run("workspace scope without a workspace is rejected", func(t *testing.T) {
		_, _, bad := installTarget(skills.NewSkillsLoader("", globalDir, ""), "workspace")
		if bad == "" {
			t.Fatal("expected error when the agent has no workspace")
		}
	})

	t.Run("global scope without a global dir is rejected", func(t *testing.T) {
		_, _, bad := installTarget(skills.NewSkillsLoader(agentWorkspace, "", ""), "global")
		if bad == "" {
			t.Fatal("expected error when no global skills dir is configured")
		}
	})
}

// TestAgentSkillInstall_RejectsBeforeNetwork checks every guard that must fire
// before InstallFromGitHub is attempted (these tests never hit GitHub).
func TestAgentSkillInstall_RejectsBeforeNetwork(t *testing.T) {
	ts, _, _ := newSkillsTestServer(t)
	base := ts.server.URL + "/api/v1/agents/"

	cases := []struct {
		name  string
		agent string
		body  string
		want  int
	}{
		{"unknown agent", "ghost", `{"url":"a/b"}`, http.StatusNotFound},
		{"missing url", "alpha", `{}`, http.StatusBadRequest},
		{"blank url", "alpha", `{"url":"   "}`, http.StatusBadRequest},
		{"unknown scope", "alpha", `{"url":"a/b","scope":"nope"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.DefaultClient.Do(authedBodyRequest(t, http.MethodPost,
				base+tc.agent+"/skills/install", ts.token, tc.body))
			if err != nil {
				t.Fatalf("Do() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

// TestAgentSkillInstallBatch_RejectsBeforeNetwork, same for the batch route.
func TestAgentSkillInstallBatch_RejectsBeforeNetwork(t *testing.T) {
	ts, _, _ := newSkillsTestServer(t)
	base := ts.server.URL + "/api/v1/agents/"

	cases := []struct {
		name  string
		agent string
		body  string
		want  int
	}{
		{"unknown agent", "ghost", `{"repo":"a/b","skills":["x"]}`, http.StatusNotFound},
		{"missing repo", "alpha", `{"skills":["x"]}`, http.StatusBadRequest},
		{"missing skills", "alpha", `{"repo":"a/b"}`, http.StatusBadRequest},
		{"unknown scope", "alpha", `{"repo":"a/b","skills":["x"],"scope":"nope"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.DefaultClient.Do(authedBodyRequest(t, http.MethodPost,
				base+tc.agent+"/skills/install-batch", ts.token, tc.body))
			if err != nil {
				t.Fatalf("Do() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}
