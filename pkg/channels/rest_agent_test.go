package channels

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/skills"
)

func TestHandleAgents(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload AgentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Agents) == 0 {
		t.Fatal("expected at least one agent")
	}
	if payload.Agents[0].ID == "" {
		t.Fatal("expected non-empty agent ID")
	}
}

func TestHandleAgentInfo(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/agents/main", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload NativeAgentInfo
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.ID != "main" {
		t.Fatalf("id = %q, want %q", payload.ID, "main")
	}
	if payload.Name == "" {
		t.Fatal("expected non-empty name")
	}
}

func TestHandleAgentInfo_NotFound(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/agents/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestHandleAgentStatus(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/agents/main/status", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload AgentStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.ID != "main" {
		t.Fatalf("id = %q, want %q", payload.ID, "main")
	}
}

func TestHandleAgentFiles_List(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.loop.workspace = t.TempDir()

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/agents/main/files", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload AgentFilesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Files == nil {
		t.Fatal("expected non-nil files")
	}
}

func TestHandleAgentFile_ReadWrite(t *testing.T) {
	ts := newNativeTestServer(t)
	tmpDir := t.TempDir()
	ts.loop.workspace = tmpDir

	// First write AGENT.md to the workspace so it's recognized as a context file
	agentFilePath := tmpDir + "/AGENT.md"
	if err := os.WriteFile(agentFilePath, []byte("test agent content"), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	// Read the file
	readReq, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/agents/main/files/AGENT.md", nil)
	readReq.Header.Set("Authorization", "Bearer "+ts.token)

	readResp, err := http.DefaultClient.Do(readReq)
	if err != nil {
		t.Fatalf("Read Do() error = %v", err)
	}
	defer readResp.Body.Close()

	if readResp.StatusCode != http.StatusOK {
		t.Fatalf("read status = %d, want %d", readResp.StatusCode, http.StatusOK)
	}

	var readPayload AgentFilesResponse
	if err := json.NewDecoder(readResp.Body).Decode(&readPayload); err != nil {
		t.Fatalf("Decode read error = %v", err)
	}
	if readPayload.Content != "test agent content" {
		t.Fatalf("content = %q, want %q", readPayload.Content, "test agent content")
	}

	// Write the file
	writeBody := mustMarshal(AgentFilesRequest{Content: "updated content"})
	writeReq, _ := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/agents/main/files/AGENT.md", strings.NewReader(string(writeBody)))
	writeReq.Header.Set("Authorization", "Bearer "+ts.token)

	writeResp, err := http.DefaultClient.Do(writeReq)
	if err != nil {
		t.Fatalf("Write Do() error = %v", err)
	}
	defer writeResp.Body.Close()

	if writeResp.StatusCode != http.StatusOK {
		t.Fatalf("write status = %d, want %d", writeResp.StatusCode, http.StatusOK)
	}

	// Verify the content was written
	var writePayload AgentFilesResponse
	if err := json.NewDecoder(writeResp.Body).Decode(&writePayload); err != nil {
		t.Fatalf("Decode write error = %v", err)
	}
	if len(writePayload.Files) == 0 {
		t.Fatal("expected at least one file")
	}
	if writePayload.Files[0].Name != "AGENT.md" {
		t.Fatalf("file name = %q, want %q", writePayload.Files[0].Name, "AGENT.md")
	}

	// Read back to confirm
	data, err := os.ReadFile(agentFilePath)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if string(data) != "updated content" {
		t.Fatalf("file content = %q, want %q", string(data), "updated content")
	}
}

func TestHandleAgentFile_NotAllowed(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.loop.workspace = t.TempDir()

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/agents/main/files/secret.txt", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestHandleAgentFiles_AgentNotFound(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/agents/nonexistent/files", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// --- B4: per-agent catalog endpoint (GET /api/v1/agents/{id}/catalog) -------

func TestHandleAgentCatalog_OK(t *testing.T) {
	ts := newNativeTestServer(t)

	resp, err := http.DefaultClient.Do(newAuthedRequest(t, http.MethodGet,
		ts.server.URL+"/api/v1/agents/main/catalog", ts.token))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload AgentCatalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}

	if payload.AgentID != "main" {
		t.Fatalf("agent_id = %q, want %q", payload.AgentID, "main")
	}
	// Tools must be non-empty and every entry must carry a readable name and
	// description (that is the whole point of the endpoint for the frontend).
	if len(payload.Tools) == 0 {
		t.Fatal("expected non-empty tools")
	}
	for _, tool := range payload.Tools {
		if tool.Name == "" || tool.Description == "" {
			t.Fatalf("tool entry has empty field: %+v", tool)
		}
	}
	// Deterministic ordering by name (frontend renders without sorting).
	for i := 1; i < len(payload.Tools); i++ {
		if payload.Tools[i-1].Name > payload.Tools[i].Name {
			t.Fatalf("tools not sorted by name: %q after %q",
				payload.Tools[i-1].Name, payload.Tools[i].Name)
		}
	}
	// Skills must always be present in the payload ([] when none installed).
	if payload.Skills == nil {
		t.Fatal("skills must be non-nil (empty array, not null)")
	}
}

func TestHandleAgentCatalog_ToolsDerivedFromRegistry(t *testing.T) {
	ts := newNativeTestServer(t)

	// Override the fake agent's "registry": the endpoint must echo exactly
	// what the agent loop reports (proving nothing is hardcoded server-side).
	ts.loop.agentTools = []AgentToolInfo{
		{Name: "zeta_tool", Description: "zeta does things"},
		{Name: "alpha_tool", Description: "alpha does things"},
	}

	resp, err := http.DefaultClient.Do(newAuthedRequest(t, http.MethodGet,
		ts.server.URL+"/api/v1/agents/main/catalog", ts.token))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload AgentCatalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Tools) != 2 {
		t.Fatalf("tools = %d, want 2 (%+v)", len(payload.Tools), payload.Tools)
	}
	want := [][2]string{{"alpha_tool", "alpha does things"}, {"zeta_tool", "zeta does things"}}
	for i, w := range want {
		if payload.Tools[i].Name != w[0] || payload.Tools[i].Description != w[1] {
			t.Fatalf("tools[%d] = %+v, want {%s, %s}", i, payload.Tools[i], w[0], w[1])
		}
	}
}

func TestHandleAgentCatalog_Skills(t *testing.T) {
	ts := newNativeTestServer(t)

	// Build a real skills loader over a temp workspace with two skills, one
	// disabled via the workspace config — the catalog must report both, with
	// the correct enabled flag and source.
	workspace := t.TempDir()
	writeTestSkill(t, workspace, "alpha-skill", "Alpha does things")
	writeTestSkill(t, workspace, "beta-skill", "Beta does things")
	if err := os.MkdirAll(filepath.Join(workspace, ".lele"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfgJSON := `{"skills":{"disabled":["beta-skill"]}}`
	if err := os.WriteFile(filepath.Join(workspace, ".lele", "workspace.json"), []byte(cfgJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ts.channel.skillsLoader = skills.NewSkillsLoader(workspace, "", "")

	resp, err := http.DefaultClient.Do(newAuthedRequest(t, http.MethodGet,
		ts.server.URL+"/api/v1/agents/main/catalog", ts.token))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload AgentCatalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Skills) != 2 {
		t.Fatalf("skills = %d, want 2 (%+v)", len(payload.Skills), payload.Skills)
	}
	byName := map[string]AgentCatalogSkill{}
	for _, s := range payload.Skills {
		byName[s.Name] = s
	}
	alpha, ok := byName["alpha-skill"]
	if !ok {
		t.Fatalf("alpha-skill missing from %+v", payload.Skills)
	}
	if alpha.Description != "Alpha does things" || alpha.Source != "workspace" || !alpha.Enabled {
		t.Fatalf("alpha-skill = %+v, want {desc=Alpha does things, source=workspace, enabled=true}", alpha)
	}
	beta, ok := byName["beta-skill"]
	if !ok {
		t.Fatalf("beta-skill missing from %+v", payload.Skills)
	}
	if beta.Enabled {
		t.Fatalf("beta-skill should be disabled: %+v", beta)
	}
}

func TestHandleAgentCatalog_AgentNotFound(t *testing.T) {
	ts := newNativeTestServer(t)

	resp, err := http.DefaultClient.Do(newAuthedRequest(t, http.MethodGet,
		ts.server.URL+"/api/v1/agents/nonexistent/catalog", ts.token))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	var payload APIError
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Code != "agent_not_found" {
		t.Fatalf("error code = %q, want %q", payload.Code, "agent_not_found")
	}
}

// newAuthedRequest builds a GET/PUT request with a bearer token header.
func newAuthedRequest(t *testing.T, method, url, token string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("NewRequest(%s %s): %v", method, url, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// writeTestSkill creates workspace/skills/<name>/SKILL.md with a minimal
// frontmatter so the skills loader picks it up.
func writeTestSkill(t *testing.T, workspace, name, description string) {
	t.Helper()
	dir := filepath.Join(workspace, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile SKILL.md: %v", err)
	}
}
