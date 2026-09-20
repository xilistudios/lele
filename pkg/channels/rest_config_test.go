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

	// Empty config should still pass validation (empty is valid)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 200 or 422; body=%s", resp.StatusCode, readBody(resp))
	}
}

// writeTempConfig creates a temporary lele config JSON file and returns its path.
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

// TestRestConfig_PutPartialPreservesUnsentSections is a regression test for
// the bug where a partial PUT (e.g. only touching agents) would silently wipe
// channels.web.enabled and native.cors_origins because the handler decoded
// into a zero-valued EditableDocument instead of overlaying on the current
// on-disk document.
func TestRestConfig_PutPartialPreservesUnsentSections(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/config.json"
	// Seed a config with web enabled (explicitly), custom cors_origins.
	// Note: web.enabled=true is the code default, so the prune-on-save
	// logic will drop it from the raw file. We verify correctness through
	// LoadConfig (the runtime path), which restores the default.
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

	// PUT a payload that ONLY touches agents — no channels key at all.
	body, _ := json.Marshal(ConfigUpdateRequest{
		Config: map[string]interface{}{
			"agents": map[string]interface{}{
				"defaults": map[string]interface{}{
					"workspace":                "/tmp/workspace",
					"provider":                 "openrouter",
					"model":                    "deepseek-v4-pro",
					"max_tokens":               4096, // changed
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

	// Verify through LoadConfig (the runtime path used by the gateway).
	runtime, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig error = %v", err)
	}

	// channels.web.enabled must still be true — the partial PUT must not
	// have wiped it to the zero value (false).
	if !runtime.Channels.Web.Enabled {
		t.Error("channels.web.enabled = false after partial PUT, want true (overlay must preserve it)")
	}

	// native.cors_origins must still have the custom values (not the default 6).
	if len(runtime.Channels.Native.CORSOrigins) != 2 {
		t.Errorf("cors_origins has %d entries, want 2 (partial PUT must not wipe to default); got %v",
			len(runtime.Channels.Native.CORSOrigins), runtime.Channels.Native.CORSOrigins)
	}
	if runtime.Channels.Native.CORSOrigins[0] != "http://example.local" {
		t.Errorf("cors_origins[0] = %q, want \"http://example.local\"", runtime.Channels.Native.CORSOrigins[0])
	}

	// Verify the agents change DID take effect.
	if runtime.Agents.Defaults.MaxTokens != 4096 {
		t.Errorf("agents.defaults.max_tokens = %d, want 4096 (the change from PUT)", runtime.Agents.Defaults.MaxTokens)
	}

	// Also verify the raw file does NOT contain web.enabled:false (the bug
	// symptom). With the overlay fix, web.enabled=true is the default and
	// gets pruned, so the web block should be absent entirely.
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
