package channels

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
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
