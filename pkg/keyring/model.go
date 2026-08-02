// Package keyring provides encrypted secret storage that agents can reference
// by name without ever exposing raw values in configuration files or logs.
//
// Secrets are stored in an AES-256-GCM encrypted vault on disk. The master
// encryption key lives in the OS keychain (macOS Keychain, GNOME
// Keyring/KWallet, Windows Credential Manager) and falls back to a local
// key file on headless systems without a keychain.
//
// The design goals are:
//   - Zero-friction: no passphrase prompts; the OS session unlock is inherited.
//   - Zero-leak: secret values never appear in logs or session history.
//   - Encrypted at rest: AES-256-GCM with a 32-byte master key.
//   - Audited: every access is recorded with agent ID and timestamp.
//   - Scoped: secrets may be restricted to specific agent IDs.
package keyring

import "time"

// Secret represents a stored secret entry.
type Secret struct {
	Name        string    `json:"name"`        // unique key, e.g. "openai.api_key"
	Description string    `json:"description"` // human-readable purpose
	Value       string    `json:"value"`       // plaintext value (only present in the decrypted vault)
	Tags        []string  `json:"tags"`        // e.g. ["provider", "openai"]
	Scope       []string  `json:"scope"`       // agent IDs allowed (empty = all)
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	CreatedBy   string    `json:"created_by"` // "tui", "webui", "agent:coder"
}

// Meta returns the safe, value-free representation of the secret for listing.
func (s *Secret) Meta() SecretMeta {
	return SecretMeta{
		Name:        s.Name,
		Description: s.Description,
		Tags:        s.Tags,
		Scope:       s.Scope,
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
		CreatedBy:   s.CreatedBy,
	}
}

// HasScope reports whether the secret restricts access to specific agents.
func (s *Secret) HasScope() bool {
	return len(s.Scope) > 0
}

// AllowsAgent reports whether the given agent may access the secret.
// An empty scope means the secret is accessible to all agents.
func (s *Secret) AllowsAgent(agentID string) bool {
	if len(s.Scope) == 0 {
		return true
	}
	for _, id := range s.Scope {
		if id == agentID {
			return true
		}
	}
	return false
}

// SecretMeta is the safe representation (no value) for listing and API responses.
type SecretMeta struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Tags        []string  `json:"tags"`
	Scope       []string  `json:"scope"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	CreatedBy   string    `json:"created_by"`
}

// AccessRecord is an audit log entry describing a single keyring operation.
type AccessRecord struct {
	SecretName string    `json:"secret_name"`
	AgentID    string    `json:"agent_id"`
	SessionKey string    `json:"session_key"`
	Action     string    `json:"action"` // "get", "set", "delete", "list"
	Timestamp  time.Time `json:"timestamp"`
	Granted    bool      `json:"granted"`
}
