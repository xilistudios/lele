package channels

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestHandleTools(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/tools", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ToolsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}

	// Verify common tools are present
	expectedTools := []string{"read_file", "write_file", "list_dir", "exec", "web_search", "web_fetch", "spawn"}
	toolNames := make(map[string]bool)
	for _, t := range payload.Tools {
		toolNames[t.Name] = true
	}
	for _, name := range expectedTools {
		if !toolNames[name] {
			t.Fatalf("expected tool %q not found", name)
		}
	}
}

func TestHandleModels(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Models) == 0 {
		t.Fatal("expected at least one model")
	}
}

func TestHandleSkills(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/skills", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SkillsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Skills == nil {
		t.Fatal("expected non-nil skills")
	}
}

func TestHandleStatus(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload SystemStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Status != "running" {
		t.Fatalf("status = %q, want %q", payload.Status, "running")
	}
	if payload.Uptime == "" {
		t.Fatal("expected non-empty uptime")
	}
}

func TestHandleChannels(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/channels", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ChannelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if len(payload.Channels) == 0 {
		t.Fatal("expected at least one channel")
	}
	if payload.Channels[0].Name == "" {
		t.Fatal("expected non-empty channel name")
	}
}

func TestHandleSkillInstall_NoURL(t *testing.T) {
	ts := newNativeTestServer(t)

	body := mustMarshal(SkillInstallRequest{URL: ""})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleSkillInstall_InvalidBody(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills", strings.NewReader("not-json"))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleSkillRemove_NoName(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.server.URL+"/api/v1/skills/name/ ", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	// No name in path will route to 404
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	// A request without a name will match a different route or 404
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleSkillsAvailable(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/skills/available", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	// This may fail if there's no network, but it must return a valid response
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 200 or 500", resp.StatusCode)
	}
}

func TestHandleSkillScan_MissingRepo(t *testing.T) {
	ts := newNativeTestServer(t)

	body := `{}`
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/scan", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleSkillScan_InvalidBody(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/scan", strings.NewReader("not-json"))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleSkillInstallBatch_MissingRepo(t *testing.T) {
	ts := newNativeTestServer(t)

	body := `{"skills": ["weather"]}`
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/install-batch", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleSkillInstallBatch_MissingSkills(t *testing.T) {
	ts := newNativeTestServer(t)

	body := `{"repo": "user/repo"}`
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/install-batch", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleSkillInstallBatch_EmptySkills(t *testing.T) {
	ts := newNativeTestServer(t)

	body := `{"repo": "user/repo", "skills": []}`
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/install-batch", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleSkillToggle_NoConfigManager(t *testing.T) {
	ts := newNativeTestServer(t)

	body := `{"enabled": true}`
	req, _ := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/skills/github/toggle", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	// Test server has no config manager initialized, so it returns 500
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
}

func TestHandleSkillToggle_InvalidBody(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/skills/github/toggle", strings.NewReader("not-json"))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandleSkillWorkspaceConfig(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/skills/workspace-config", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}

	// Should have a "skills" key with enabled/disabled
	skills, ok := payload["skills"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'skills' key in response")
	}
	if _, hasEnabled := skills["enabled"]; !hasEnabled {
		t.Fatal("expected 'enabled' in skills config")
	}
	if _, hasDisabled := skills["disabled"]; !hasDisabled {
		t.Fatal("expected 'disabled' in skills config")
	}
}

func TestIsAllowedProviderURL(t *testing.T) {
	tests := []struct {
		url   string
		allow bool
	}{
		{"https://api.openai.com/v1", true},
		{"https://api.groq.com/openai/v1", true},
		{"http://api.openai.com/v1", false}, // no HTTPS
		{"https://localhost:11434/v1", false},
		{"https://127.0.0.1:8080", false},
		{"https://192.168.1.1/v1", false}, // private IP
		{"https://10.0.0.1/v1", false},    // private IP
		{"https://[::1]:11434/v1", false}, // loopback
		{"", false},
		{"not-a-url", false},
	}

	for _, tt := range tests {
		got := isAllowedProviderURL(tt.url)
		if got != tt.allow {
			t.Errorf("isAllowedProviderURL(%q) = %v, want %v", tt.url, got, tt.allow)
		}
	}
}

func TestParsePagination(t *testing.T) {
	tests := []struct {
		offset string
		limit  string
		wantO  int
		wantL  int
	}{
		{"", "", 0, 50},
		{"10", "", 10, 50},
		{"", "5", 0, 5},
		{"-5", "300", 0, 50}, // negative offset → 0, over limit 200 → default 50
		{"0", "100", 0, 100},
	}

	for _, tt := range tests {
		req, _ := http.NewRequest(http.MethodGet, "/api/v1/chat/sessions?offset="+tt.offset+"&limit="+tt.limit, nil)
		offset, limit := parsePagination(req)
		if offset != tt.wantO {
			t.Errorf("parsePagination(offset=%q) = %d, want %d", tt.offset, offset, tt.wantO)
		}
		if limit != tt.wantL {
			t.Errorf("parsePagination(limit=%q) = %d, want %d", tt.limit, limit, tt.wantL)
		}
	}
}

func TestDefaultAPIBaseByTypePublic(t *testing.T) {
	tests := []struct {
		providerType string
		want         string
	}{
		{"openai", "https://api.openai.com/v1"},
		{"gpt", "https://api.openai.com/v1"},
		{"groq", "https://api.groq.com/openai/v1"},
		{"deepseek", "https://api.deepseek.com/v1"},
		{"ollama", "http://localhost:11434/v1"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		got := defaultAPIBaseByTypePublic(tt.providerType)
		if got != tt.want {
			t.Errorf("defaultAPIBaseByTypePublic(%q) = %q, want %q", tt.providerType, got, tt.want)
		}
	}
}
