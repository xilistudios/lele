package channels

import (
	"encoding/json"
	"net/http"

	"github.com/xilistudios/lele/pkg/keyring"
)

// secretInput is the request body for creating/updating a secret.
type secretInput struct {
	Name        string   `json:"name"`
	Value       string   `json:"value"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Scope       []string `json:"scope"`
}

// keyringAvailable reports whether the keyring service is wired up, writing an
// error response and returning false if it is not.
func (n *NativeChannel) keyringAvailable(w http.ResponseWriter) bool {
	if n.keyringService == nil {
		writeError(w, http.StatusServiceUnavailable, "keyring service not available", "keyring_unavailable")
		return false
	}
	return true
}

// handleSecretsList returns metadata for all secrets (no values).
// GET /api/v1/secrets
func (n *NativeChannel) handleSecretsList(w http.ResponseWriter, r *http.Request) {
	if !n.keyringAvailable(w) {
		return
	}
	secrets, err := n.keyringService.ListAll()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "keyring_list_failed")
		return
	}
	if secrets == nil {
		secrets = []keyring.SecretMeta{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"secrets": secrets,
		"status":  n.keyringService.Status(),
	})
}

// handleSecretGet returns a single secret's value (for UI "reveal").
// GET /api/v1/secrets/{name}
func (n *NativeChannel) handleSecretGet(w http.ResponseWriter, r *http.Request) {
	if !n.keyringAvailable(w) {
		return
	}
	name := r.PathValue("name")
	meta, ok := n.findSecretMeta(name)
	if !ok {
		writeError(w, http.StatusNotFound, "secret not found", "secret_not_found")
		return
	}
	value, err := n.keyringService.GetRaw(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "keyring_get_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"secret": meta,
		"value":  value,
	})
}

// handleSecretCreate creates or updates a secret.
// POST /api/v1/secrets
func (n *NativeChannel) handleSecretCreate(w http.ResponseWriter, r *http.Request) {
	if !n.keyringAvailable(w) {
		return
	}
	var input secretInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_body")
		return
	}
	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required", "missing_name")
		return
	}
	if input.Value == "" {
		writeError(w, http.StatusBadRequest, "value is required", "missing_value")
		return
	}
	if err := n.keyringService.SetFromUI(input.Name, input.Value, input.Description, input.Tags, input.Scope, "webui"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "keyring_set_failed")
		return
	}
	meta, _ := n.findSecretMeta(input.Name)
	writeJSON(w, http.StatusCreated, map[string]interface{}{"secret": meta})
}

// handleSecretDelete removes a secret.
// DELETE /api/v1/secrets/{name}
func (n *NativeChannel) handleSecretDelete(w http.ResponseWriter, r *http.Request) {
	if !n.keyringAvailable(w) {
		return
	}
	name := r.PathValue("name")
	if err := n.keyringService.DeleteFromUI(name, "webui"); err != nil {
		if err == keyring.ErrNotFound {
			writeError(w, http.StatusNotFound, "secret not found", "secret_not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error(), "keyring_delete_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": name})
}

// handleSecretsStatus returns backend info and secret count.
// GET /api/v1/secrets/status
func (n *NativeChannel) handleSecretsStatus(w http.ResponseWriter, r *http.Request) {
	if !n.keyringAvailable(w) {
		return
	}
	writeJSON(w, http.StatusOK, n.keyringService.Status())
}

// handleSecretsAudit returns the in-memory access audit log.
// GET /api/v1/secrets/audit
func (n *NativeChannel) handleSecretsAudit(w http.ResponseWriter, r *http.Request) {
	if !n.keyringAvailable(w) {
		return
	}
	records := n.keyringService.AuditLog()
	if records == nil {
		records = []keyring.AccessRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"audit": records})
}

// findSecretMeta looks up a secret's metadata by name.
func (n *NativeChannel) findSecretMeta(name string) (keyring.SecretMeta, bool) {
	all, err := n.keyringService.ListAll()
	if err != nil {
		return keyring.SecretMeta{}, false
	}
	for _, s := range all {
		if s.Name == name {
			return s, true
		}
	}
	return keyring.SecretMeta{}, false
}
