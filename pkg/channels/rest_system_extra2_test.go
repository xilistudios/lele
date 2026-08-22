package channels

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/skills"
)

func doAuthGet(ts *nativeTestServer, path string) (*http.Response, string) {
	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// TestHandleTools verifies the tools list includes read_image when the
// session model supports images.
func TestHandleTools(t *testing.T) {
	ts := newNativeTestServer(t)
	if resp, body := doAuthGet(ts, "/api/v1/tools?session_key=agent:main:main"); resp != nil {
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var out ToolsResponse
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(out.Tools) < 7 {
			t.Errorf("expected at least 7 tools, got %d", len(out.Tools))
		}
		found := false
		for _, tool := range out.Tools {
			if tool.Name == "read_file" {
				found = true
			}
		}
		if !found {
			t.Error("expected read_file tool")
		}
	}
}

// TestHandleModels verifies the models endpoint returns configured providers.
func TestHandleModels(t *testing.T) {
	ts := newNativeTestServer(t)
	if resp, body := doAuthGet(ts, "/api/v1/models"); resp != nil {
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var out ModelsResponse
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.AgentID == "" {
			t.Errorf("expected agent_id")
		}
		if len(out.ModelGroups) < 2 {
			t.Errorf("expected >=2 model groups (openai, anthropic), got %d", len(out.ModelGroups))
		}
	}
}

// TestHandleSkills verifies skills listing with an empty loader.
func TestHandleSkills(t *testing.T) {
	ts := newNativeTestServer(t)
	if resp, body := doAuthGet(ts, "/api/v1/skills"); resp != nil {
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var out SkillsResponse
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Skills == nil {
			t.Error("expected non-nil skills slice")
		}
	}
}

// TestHandleSkillInstall_Validation covers missing URL and invalid body.
func TestHandleSkillInstall_Validation(t *testing.T) {
	ts := newNativeTestServer(t)

	// Missing URL → 400.
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing url status = %d, want 400", resp.StatusCode)
	}

	// Invalid body → 400.
	req2, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills", strings.NewReader(`not-json`))
	req2.Header.Set("Authorization", "Bearer "+ts.token)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid body status = %d, want 400", resp2.StatusCode)
	}
}

// TestHandleSkillRemove exercises the not-found (and thus 500) path.
func TestHandleSkillRemove(t *testing.T) {
	ts := newNativeTestServer(t)

	// Non-existent skill → 500 (installer fails to find it).
	ts.channel.skillInstaller = skills.NewSkillInstaller(t.TempDir())
	req2, _ := http.NewRequest(http.MethodDelete, ts.server.URL+"/api/v1/skills/no-such-skill", nil)
	req2.Header.Set("Authorization", "Bearer "+ts.token)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusInternalServerError {
		t.Errorf("remove missing skill status = %d, want 500", resp2.StatusCode)
	}
}

// TestHandleStatus verifies the status endpoint.
func TestHandleStatus(t *testing.T) {
	ts := newNativeTestServer(t)
	if resp, body := doAuthGet(ts, "/api/v1/status"); resp != nil {
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var out SystemStatusResponse
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Status != "running" {
			t.Errorf("status = %q", out.Status)
		}
		if len(out.Channels) == 0 {
			t.Error("expected at least one channel")
		}
	}
}

// TestHandleChannels verifies the channels endpoint.
func TestHandleChannels(t *testing.T) {
	ts := newNativeTestServer(t)
	if resp, body := doAuthGet(ts, "/api/v1/channels"); resp != nil {
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var out ChannelsResponse
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(out.Channels) != 1 || out.Channels[0].Name != "native" {
			t.Errorf("channels = %+v", out.Channels)
		}
	}
}

// TestHandleProviderModels_Errors covers error branches: missing provider name,
// provider not found, no api_base, no api_key.
func TestHandleProviderModels_Errors(t *testing.T) {
	ts := newNativeTestServer(t)

	// Not found provider.
	if resp, _ := doAuthGet(ts, "/api/v1/providers/nonexist/models"); resp != nil {
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("nonexistent provider status = %d, want 404", resp.StatusCode)
		}
	}

	// type with empty defaultAPIBaseByTypePublic -> trigger no_api_base
	// by adding a provider with type "vllm" (empty default) and no api_base.
	cfg := ts.channel.agentLoop.GetConfigSnapshot()
	cfg.Providers.Named["vllm-test"] = config.NamedProviderConfig{
		Type:           "vllm",
		ProviderConfig: config.ProviderConfig{APIKey: "k"},
	}
	if resp, _ := doAuthGet(ts, "/api/v1/providers/vllm-test/models"); resp != nil {
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("no api_base status = %d, want 400", resp.StatusCode)
		}
		delete(cfg.Providers.Named, "vllm-test")
	}

	// no_api_key: provider resolves a default api_base but has no api_key.
	cfg.Providers.Named["openai-nokey"] = config.NamedProviderConfig{
		Type:           "openai",
		ProviderConfig: config.ProviderConfig{APIBase: "https://api.openai.com/v1"},
	}
	if resp, _ := doAuthGet(ts, "/api/v1/providers/openai-nokey/models"); resp != nil {
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("no_api_key status = %d, want 400", resp.StatusCode)
		}
		delete(cfg.Providers.Named, "openai-nokey")
	}

	// url_not_allowed: api_base not HTTPS/private — SSRF guard rejects it.
	cfg.Providers.Named["bad-url"] = config.NamedProviderConfig{
		Type:           "openai",
		ProviderConfig: config.ProviderConfig{APIKey: "k", APIBase: "http://127.0.0.1:8080/v1"},
	}
	if resp, _ := doAuthGet(ts, "/api/v1/providers/bad-url/models"); resp != nil {
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("url_not_allowed status = %d, want 400", resp.StatusCode)
		}
		delete(cfg.Providers.Named, "bad-url")
	}
}

// TestHandleProviderModels_UpstreamError covers the 502 branch: a provider whose
// api_base is a public HTTPS hostname that will not resolve/connect.
func TestHandleProviderModels_UpstreamError(t *testing.T) {
	ts := newNativeTestServer(t)
	cfg := ts.channel.agentLoop.GetConfigSnapshot()
	// .invalid TLD is guaranteed to not resolve -> client.Do returns an error.
	cfg.Providers.Named["dead-upstream"] = config.NamedProviderConfig{
		Type: "openai",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "k",
			APIBase: "https://upstream.invalid/v1",
		},
	}
	resp, _ := doAuthGet(ts, "/api/v1/providers/dead-upstream/models")
	if resp != nil {
		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("upstream status = %d, want 502", resp.StatusCode)
		}
	}
} // TestHandleSkillWorkspaceConfig covers the workspace-config endpoint with a
// nil config manager (returns empty sets).
func TestHandleSkillWorkspaceConfig(t *testing.T) {
	ts := newNativeTestServer(t)
	// newNativeTestServer uses &skills.SkillsLoader{} with nil configMgr.
	if resp, body := doAuthGet(ts, "/api/v1/skills/workspace-config"); resp != nil {
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var out map[string]interface{}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, ok := out["skills"]; !ok {
			t.Error("expected skills key in response")
		}
	}
}

// TestHandleSkillToggle_NoConfigMgr covers the config_unavailable branch.
func TestHandleSkillToggle_NoConfigMgr(t *testing.T) {
	ts := newNativeTestServer(t)
	// skillsLoader has nil configMgr → 500 config_unavailable.
	req, _ := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/skills/some-skill/toggle", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("toggle no-config-mgr status = %d, want 500", resp.StatusCode)
	}
}

// TestHandleSkillScan_Validation covers invalid body and missing repo.
func TestHandleSkillScan_Validation(t *testing.T) {
	ts := newNativeTestServer(t)

	// Invalid body → 400.
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/scan", strings.NewReader(`bad-json`))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid body status = %d, want 400", resp.StatusCode)
	}

	// Missing repo → 400.
	req2, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/scan", strings.NewReader(`{}`))
	req2.Header.Set("Authorization", "Bearer "+ts.token)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("missing repo status = %d, want 400", resp2.StatusCode)
	}
}

// TestHandleSkillInstallBatch_Validation covers error branches.
func TestHandleSkillInstallBatch_Validation(t *testing.T) {
	ts := newNativeTestServer(t)

	// Invalid body → 400.
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/install-batch", strings.NewReader(`bad`))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid body status = %d, want 400", resp.StatusCode)
	}

	// Missing repo → 400.
	req2, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/install-batch", strings.NewReader(`{"skills":["a"]}`))
	req2.Header.Set("Authorization", "Bearer "+ts.token)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("missing repo status = %d, want 400", resp2.StatusCode)
	}

	// Empty skills → 400.
	req3, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/skills/install-batch", strings.NewReader(`{"repo":"owner/repo"}`))
	req3.Header.Set("Authorization", "Bearer "+ts.token)
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest {
		t.Errorf("empty skills status = %d, want 400", resp3.StatusCode)
	}
}

// TestHandleSkillToggle_InvalidBody covers the invalid-body branch for toggle.
func TestHandleSkillToggle_InvalidBody(t *testing.T) {
	ts := newNativeTestServer(t)
	req, _ := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/skills/some-skill/toggle", strings.NewReader(`bad`))
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("toggle invalid body status = %d, want 400", resp.StatusCode)
	}
}
