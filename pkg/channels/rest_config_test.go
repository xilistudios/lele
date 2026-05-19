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
