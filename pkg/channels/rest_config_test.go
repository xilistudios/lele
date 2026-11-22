package channels

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func TestRestConfig_Get(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/config", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Metadata.ConfigPath == "" {
		t.Fatal("expected non-empty config path in metadata")
	}
}

func TestRestConfig_Validate(t *testing.T) {
	ts := newNativeTestServer(t)

	body := mustMarshal(ConfigValidateRequest{
		Config: config.EditableDocument{
			Agents: config.EditableAgentsConfig{
				Defaults: config.EditableAgentDefaults{
					Workspace:         "/tmp/workspace",
					Provider:          "openai",
					Model:             "gpt-4",
					MaxTokens:         8192,
					MaxToolIterations: 20,
				},
			},
			Channels: config.EditableChannelsConfig{
				Native: config.EditableNativeConfig{Enabled: true, Port: 8080},
			},
		},
	})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/config/validate", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var payload ConfigValidateResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if !payload.Valid {
		t.Fatal("expected valid=true for a valid config")
	}
}

func TestRestConfig_ValidateInvalid(t *testing.T) {
	ts := newNativeTestServer(t)

	body := mustMarshal(ConfigValidateRequest{
		Config: map[string]interface{}{"invalid": true},
	})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/config/validate", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestRestConfig_PutInvalidBody(t *testing.T) {
	ts := newNativeTestServer(t)

	req, _ := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/config", strings.NewReader("not-json"))
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

func TestRestConfig_PutHappyPath(t *testing.T) {
	tmpConfigPath := writeTempConfig(t)
	ts := newNativeTestServerWithConfigPath(t, tmpConfigPath)

	body, _ := json.Marshal(ConfigUpdateRequest{
		Config: config.EditableDocument{
			Agents: config.EditableAgentsConfig{
				Defaults: config.EditableAgentDefaults{
					Workspace:         "/tmp/workspace",
					Provider:          "openai",
					Model:             "gpt-4",
					MaxTokens:         8192,
					MaxToolIterations: 20,
				},
			},
			Channels: config.EditableChannelsConfig{
				Native: config.EditableNativeConfig{Enabled: true, Port: 8080},
			},
		},
	})
	req, _ := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, readBody(resp))
	}

	var payload ConfigUpdateResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	if payload.Config == nil {
		t.Fatal("expected non-nil config in response")
	}
}

func TestRestConfig_PutInvalidConfig(t *testing.T) {
	tmpConfigPath := writeTempConfig(t)
	ts := newNativeTestServerWithConfigPath(t, tmpConfigPath)

	body := mustMarshal(ConfigUpdateRequest{
		Config: config.EditableDocument{
			Agents: config.EditableAgentsConfig{
				Defaults: config.EditableAgentDefaults{
					Workspace: "",
					Provider:  "",
					Model:     "",
				},
			},
		},
	})
	req, _ := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 200 or 422; body=%s", resp.StatusCode, readBody(resp))
	}
}

func writeTempConfig(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	configPath := tmpDir + "/config.json"
	data := []byte(`{"channels":{"native":{"enabled":true}}}`)
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	return configPath
}

func TestRestConfig_PutPartialPreservesUnsentSections(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/config.json"
	seedConfig := `{
  "channels": {
    "native": {
      "enabled": true,
      "port": 18790,
      "cors_origins": ["http://example.local", "http://app.local"]
    },
    "web": {
      "enabled": true
    }
  },
  "agents": {
    "defaults": {
      "workspace": "/tmp/workspace",
      "provider": "openrouter",
      "model": "deepseek-v4-pro",
      "max_tokens": 8192,
      "max_tool_iterations": 20,
      "max_read_lines": 500,
      "subagent_timeout_minutes": 30,
      "subagent_max_retries": 2
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(seedConfig), 0600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	ts := newNativeTestServerWithConfigPath(t, configPath)

	body, _ := json.Marshal(ConfigUpdateRequest{
		Config: map[string]interface{}{
			"agents": map[string]interface{}{
				"defaults": map[string]interface{}{
					"workspace":                "/tmp/workspace",
					"provider":                 "openrouter",
					"model":                    "deepseek-v4-pro",
					"max_tokens":               4096,
					"max_tool_iterations":      20,
					"max_read_lines":           500,
					"subagent_timeout_minutes": 30,
					"subagent_max_retries":     2,
				},
			},
		},
	})
	req, _ := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, readBody(resp))
	}

	runtime, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error = %v", err)
	}

	if !runtime.Channels.Web.Enabled {
		t.Error("channels.web.enabled = false after partial PUT, want true (overlay must preserve it)")
	}

	if len(runtime.Channels.Native.CORSOrigins) != 2 {
		t.Errorf("cors_origins has %d entries, want 2 (partial PUT must not wipe to default); got %v",
			len(runtime.Channels.Native.CORSOrigins), runtime.Channels.Native.CORSOrigins)
	}
	if runtime.Channels.Native.CORSOrigins[0] != "http://example.local" {
		t.Errorf("cors_origins[0] = %q, want \"http://example.local\"", runtime.Channels.Native.CORSOrigins[0])
	}

	if runtime.Agents.Defaults.MaxTokens != 4096 {
		t.Errorf("agents.defaults.max_tokens = %d, want 4096 (the change from PUT)", runtime.Agents.Defaults.MaxTokens)
	}

	savedData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	var saved map[string]interface{}
	if err := json.Unmarshal(savedData, &saved); err != nil {
		t.Fatalf("Unmarshal saved config error: %v", err)
	}
	if channelsRaw, ok := saved["channels"]; ok {
		if channels, ok := channelsRaw.(map[string]interface{}); ok {
			if webRaw, ok := channels["web"]; ok {
				if web, ok := webRaw.(map[string]interface{}); ok {
					if web["enabled"] == false {
						t.Error("raw file has web.enabled=false — the partial PUT bug is still present")
					}
				}
			}
		}
	}
}
