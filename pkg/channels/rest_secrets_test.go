package channels

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/keyring"
)

// newSecretsTestServer creates a native test server with a file-backed keyring
// service attached, so tests never touch the OS keychain.
func newSecretsTestServer(t *testing.T) *nativeTestServer {
	t.Helper()
	ts := newNativeTestServer(t)
	dir := t.TempDir()
	svc := keyring.NewService(keyring.ServiceConfig{
		Enabled:      true,
		VaultPath:    filepath.Join(dir, "keyring.enc"),
		Backend:      keyring.BackendFile,
		AuditLogSize: 100,
		LeleDir:      dir,
	})
	ts.channel.SetKeyringService(svc)
	return ts
}

func doSecretsRequest(t *testing.T, ts *nativeTestServer, method, path string, body interface{}) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, ts.server.URL+path, reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	return resp
}

func TestSecretsAPI_CRUD(t *testing.T) {
	ts := newSecretsTestServer(t)

	// Create a secret.
	resp := doSecretsRequest(t, ts, http.MethodPost, "/api/v1/secrets", secretInput{
		Name:        "openai.api_key",
		Value:       "sk-test-123",
		Description: "OpenAI key",
		Tags:        []string{"provider"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	resp.Body.Close()

	// List secrets — value must not be present.
	resp = doSecretsRequest(t, ts, http.MethodGet, "/api/v1/secrets", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var listPayload struct {
		Secrets []map[string]interface{} `json:"secrets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listPayload); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	resp.Body.Close()
	if len(listPayload.Secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(listPayload.Secrets))
	}
	if _, hasValue := listPayload.Secrets[0]["value"]; hasValue {
		t.Error("list response must not include secret value")
	}

	// Get secret value.
	resp = doSecretsRequest(t, ts, http.MethodGet, "/api/v1/secrets/openai.api_key", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var getPayload struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&getPayload); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	resp.Body.Close()
	if getPayload.Value != "sk-test-123" {
		t.Errorf("value = %q, want sk-test-123", getPayload.Value)
	}

	// Delete the secret.
	resp = doSecretsRequest(t, ts, http.MethodDelete, "/api/v1/secrets/openai.api_key", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	resp.Body.Close()

	// Get after delete → 404.
	resp = doSecretsRequest(t, ts, http.MethodGet, "/api/v1/secrets/openai.api_key", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get-after-delete status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	resp.Body.Close()
}

func TestSecretsAPI_StatusAndAudit(t *testing.T) {
	ts := newSecretsTestServer(t)

	// Create a secret, then access it via the agent path to generate an audit
	// entry (UI operations are trusted and not audited by design).
	resp := doSecretsRequest(t, ts, http.MethodPost, "/api/v1/secrets", secretInput{
		Name:  "github.token",
		Value: "ghp_x",
	})
	resp.Body.Close()

	// Seed an audit record through the agent-scoped getter.
	if _, err := ts.channel.keyringService.GetForAgent("github.token", "coder", "sess-1"); err != nil {
		t.Fatalf("GetForAgent: %v", err)
	}

	// Status endpoint.
	resp = doSecretsRequest(t, ts, http.MethodGet, "/api/v1/secrets/status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var status map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	resp.Body.Close()
	if status["backend"] != keyring.BackendFile {
		t.Errorf("backend = %v, want %q", status["backend"], keyring.BackendFile)
	}

	// Audit endpoint.
	resp = doSecretsRequest(t, ts, http.MethodGet, "/api/v1/secrets/audit", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audit status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var auditPayload struct {
		Audit []map[string]interface{} `json:"audit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&auditPayload); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	resp.Body.Close()
	if len(auditPayload.Audit) == 0 {
		t.Error("expected at least one audit record")
	}
}

func TestSecretsAPI_CreateValidation(t *testing.T) {
	ts := newSecretsTestServer(t)

	// Missing name.
	resp := doSecretsRequest(t, ts, http.MethodPost, "/api/v1/secrets", secretInput{Value: "x"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing-name status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	resp.Body.Close()

	// Missing value.
	resp = doSecretsRequest(t, ts, http.MethodPost, "/api/v1/secrets", secretInput{Name: "foo"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing-value status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	resp.Body.Close()
}
func TestSecretsAPI_DeleteNotFound(t *testing.T) {
	ts := newSecretsTestServer(t)

	resp := doSecretsRequest(t, ts, http.MethodDelete, "/api/v1/secrets/does-not-exist", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete-missing status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	resp.Body.Close()
}

func TestSecretsAPI_CreateInvalidBody(t *testing.T) {
	ts := newSecretsTestServer(t)

	req, err := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/secrets", bytes.NewBufferString("{not json"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid-body status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestSecretsAPI_KeyringUnavailable(t *testing.T) {
	// newNativeTestServer does not attach a keyring service.
	ts := newNativeTestServer(t)

	// List, get, create, delete and status all depend on keyring availability.
	for _, tc := range []struct {
		method string
		path   string
		body   interface{}
	}{
		{http.MethodGet, "/api/v1/secrets", nil},
		{http.MethodGet, "/api/v1/secrets/foo", nil},
		{http.MethodPost, "/api/v1/secrets", secretInput{Name: "x", Value: "y"}},
		{http.MethodDelete, "/api/v1/secrets/foo", nil},
		{http.MethodGet, "/api/v1/secrets/status", nil},
		{http.MethodGet, "/api/v1/secrets/audit", nil},
	} {
		resp := doSecretsRequest(t, ts, tc.method, tc.path, tc.body)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s %s status = %d, want 503", tc.method, tc.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
